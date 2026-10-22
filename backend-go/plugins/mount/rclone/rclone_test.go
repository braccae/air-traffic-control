package rclone_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"air-traffic-control/backend/pkg/plugin"
	"air-traffic-control/backend/pkg/plugin/newapi"
	"air-traffic-control/backend/pkg/plugin/settingsframework"
	_ "air-traffic-control/backend/plugins/all"
	"air-traffic-control/backend/plugins/mount/rclone"
)

type mockRunner struct{ responses map[string]string }

func (m *mockRunner) RunCommand(ctx context.Context, cmd string) (string, error) {
	for pattern, resp := range m.responses {
		if strings.Contains(cmd, pattern) {
			return resp, nil
		}
	}
	return "", nil
}

func TestRclonePlugin_Registered(t *testing.T) {
	p, ok := plugin.Get("mount/rclone")
	if !ok {
		t.Fatal("mount/rclone plugin not registered")
	}
	meta := p.Meta()
	if meta.Kind != plugin.KindMount || meta.Name != "rclone" {
		t.Errorf("unexpected meta: %+v", meta)
	}
	if !p.Compatible(plugin.HostFacts{OSFamily: "linux"}) {
		t.Error("expected compatible with linux")
	}
	if p.Compatible(plugin.HostFacts{OSFamily: "windows"}) {
		t.Error("expected incompatible with windows")
	}

	// Verify SettingsPageProvider is registered for datacenter settings.
	sp, ok := p.(plugin.SettingsPageProvider)
	if !ok {
		t.Fatal("expected mount/rclone to implement plugin.SettingsPageProvider")
	}
	page := sp.SettingsPage()
	if page.ID != "mount/rclone" || len(page.Fields) == 0 {
		t.Errorf("unexpected settings page: %+v", page)
	}
}

func TestRclonePlugin_ProbeAndRead(t *testing.T) {
	p, _ := plugin.Get("mount/rclone")
	runner := &mockRunner{responses: map[string]string{
		"command -v rclone":     "/usr/bin/rclone\n",
		"grep -c 'fuse.rclone'": "2\n",
		"rclone listremotes":    "hetzner:\nbackup:\n",
		"grep 'fuse.rclone'":    "/dev/fuse on /mnt/rclone/hetzner type fuse.rclone\n",
	}}

	det, err := p.Probe(context.Background(), runner, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatalf("probe error: %v", err)
	}
	if !det.Detected || det.Status != "2 mount(s)" {
		t.Errorf("unexpected detection: %+v", det)
	}

	reader, ok := p.(plugin.Reader)
	if !ok {
		t.Fatal("expected Reader implementation")
	}
	data, err := reader.Read(context.Background(), runner, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatalf("read error: %v", err)
	}
	m := data.(map[string]any)
	remotes := m["remotes"].([]string)
	if len(remotes) != 2 || remotes[0] != "hetzner" {
		t.Errorf("unexpected remotes: %+v", remotes)
	}
}

func TestRclonePlugin_ProbeUnavailable(t *testing.T) {
	p, _ := plugin.Get("mount/rclone")
	det, err := p.Probe(context.Background(), &mockRunner{responses: map[string]string{}}, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatalf("probe error: %v", err)
	}
	if det.Detected {
		t.Error("expected not detected when rclone missing")
	}
}

func TestRclonePlugin_CompilePolicyVariantsAndSystemdAlignment(t *testing.T) {
	p, _ := plugin.Get("mount/rclone")
	actor, ok := p.(plugin.Actor)
	if !ok {
		t.Fatal("expected Actor implementation")
	}

	base := map[string]any{
		"name":          "hetzner",
		"configContent": "[hetzner]\ntype = sftp\npass = obscuredvalue",
		"mountTarget":   "/mnt/rclone/hetzner",
	}

	compilePolicy := func(policy string) plugin.TaskSpec {
		opts := map[string]any{}
		for k, v := range base {
			opts[k] = v
		}
		opts["policy"] = policy
		spec, err := actor.Compile(plugin.HostFacts{OSFamily: "linux"}, "install_remote", opts)
		if err != nil {
			t.Fatalf("compile %s: %v", policy, err)
		}
		return spec
	}

	modulesFor := func(spec plugin.TaskSpec) []string {
		var mods []string
		for _, s := range spec.Steps {
			mods = append(mods, s.Module)
		}
		return mods
	}

	fstabSpec := compilePolicy("fstab")
	if got := modulesFor(fstabSpec); !contains(got, "ansible.posix.mount") {
		t.Errorf("fstab policy should use ansible.posix.mount, got %v", got)
	}

	systemdSpec := compilePolicy("systemd-mount")
	if got := modulesFor(systemdSpec); !contains(got, "ansible.builtin.systemd_service") {
		t.Errorf("systemd-mount should use ansible.builtin.systemd_service, got %v", got)
	}
	// Verify ATC drop-in subfolder /etc/systemd/system/rclone-hetzner.service.d/atc-rclone.conf
	foundDropIn := false
	for _, s := range systemdSpec.Steps {
		if s.Module == "ansible.builtin.copy" {
			if dest, _ := s.Params["dest"].(string); dest == "/etc/systemd/system/rclone-hetzner.service.d/atc-rclone.conf" {
				foundDropIn = true
			}
		}
	}
	if !foundDropIn {
		t.Error("expected systemd-mount to write /etc/systemd/system/rclone-hetzner.service.d/atc-rclone.conf drop-in")
	}

	automountSpec := compilePolicy("systemd-automount")
	if got := modulesFor(automountSpec); !contains(got, "ansible.builtin.systemd_service") {
		t.Errorf("systemd-automount should use ansible.builtin.systemd_service, got %v", got)
	}
	// systemd requires .mount and .automount unit names to match the escaped Where= path.
	wantMountUnit := rclone.MountUnitName("/mnt/rclone/hetzner")
	wantAutomountUnit := rclone.AutomountUnitName("/mnt/rclone/hetzner")
	if wantMountUnit != "mnt-rclone-hetzner.mount" || wantAutomountUnit != "mnt-rclone-hetzner.automount" {
		t.Fatalf("unexpected escaped unit names: %s / %s", wantMountUnit, wantAutomountUnit)
	}
	foundMountUnit := false
	foundMountDropIn := false
	foundAutomountStart := false
	for _, s := range automountSpec.Steps {
		if s.Module == "ansible.builtin.copy" {
			dest, _ := s.Params["dest"].(string)
			if dest == "/etc/systemd/system/"+wantMountUnit {
				foundMountUnit = true
			}
			if dest == "/etc/systemd/system/"+wantMountUnit+".d/atc-rclone.conf" {
				foundMountDropIn = true
			}
		}
		if s.Module == "ansible.builtin.systemd_service" {
			if name, _ := s.Params["name"].(string); name == wantAutomountUnit {
				foundAutomountStart = true
			}
		}
	}
	if !foundMountUnit || !foundMountDropIn || !foundAutomountStart {
		t.Errorf("automount steps missing expected unit/drop-in/start: mount=%v dropin=%v start=%v",
			foundMountUnit, foundMountDropIn, foundAutomountStart)
	}

	scriptSpec := compilePolicy("script")
	if got := modulesFor(scriptSpec); !contains(got, "ansible.builtin.copy") {
		t.Errorf("script policy should copy a script, got %v", got)
	}

	// Non-systemd Linux fallback (OpenRC/Alpine) degrades systemd-mount to fstab.
	nonSystemdSpec, err := actor.Compile(plugin.HostFacts{
		OSFamily:  "linux",
		Overrides: map[string]string{"initSystem": "openrc"},
	}, "install_remote", map[string]any{
		"name":          "hetzner",
		"configContent": "[hetzner]\ntype = sftp\n",
		"mountTarget":   "/mnt/rclone/hetzner",
		"policy":        "systemd-mount",
	})
	if err != nil {
		t.Fatalf("compile non-systemd fallback: %v", err)
	}
	if got := modulesFor(nonSystemdSpec); !contains(got, "ansible.posix.mount") {
		t.Errorf("expected non-systemd host to fall back to ansible.posix.mount, got %v", got)
	}
}

func TestRclonePlugin_CompileMasksSecretsAndRejectsBadInput(t *testing.T) {
	p, _ := plugin.Get("mount/rclone")
	actor := p.(plugin.Actor)

	spec, err := actor.Compile(plugin.HostFacts{OSFamily: "linux"}, "install_remote", map[string]any{
		"name":          "hetzner",
		"configContent": "[hetzner]\ntype=sftp\npass=obscuredsecret",
		"mountTarget":   "/mnt/rclone/hetzner",
		"policy":        "systemd-mount",
	})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if spec.SecretsMasked["configContent"] == "" {
		t.Error("expected configContent to be masked in SecretsMasked")
	}
	// The obscured config is written with no_log so it never reaches Ansible logs.
	foundNoLog := false
	for _, s := range spec.Steps {
		if s.Module == "ansible.builtin.blockinfile" {
			if v, ok := s.Params["no_log"].(bool); !ok || !v {
				t.Error("config write step must set no_log: true")
			} else {
				foundNoLog = true
			}
		}
	}
	if !foundNoLog {
		t.Error("expected a blockinfile step writing the remote config")
	}

	// Unit content must never embed the obscured secret value.
	for _, s := range spec.Steps {
		if s.Module == "ansible.builtin.copy" {
			if content, ok := s.Params["content"].(string); ok {
				if strings.Contains(content, "obscuredsecret") {
					t.Error("unit/script content must not contain remote credentials")
				}
			}
		}
	}

	for _, bad := range []map[string]any{
		{"configContent": "x", "mountTarget": "/mnt/x"},                                 // missing name
		{"name": "bad name!", "configContent": "x", "mountTarget": "/mnt/x"},            // bad name
		{"name": "ok", "configContent": "x", "mountTarget": "/mnt/x", "policy": "nope"}, // bad policy
		{"name": "ok", "configContent": "x"},                                            // missing target
	} {
		if _, err := actor.Compile(plugin.HostFacts{OSFamily: "linux"}, "install_remote", bad); err == nil {
			t.Errorf("expected error for %+v", bad)
		}
	}
}

func TestRclonePlugin_NewAPIInterfacesAndSchemas(t *testing.T) {
	p, ok := plugin.Get("mount/rclone")
	if !ok {
		t.Fatalf("mount/rclone not registered in DefaultRegistry")
	}

	// 1. DataCollectionPlugin
	dc, ok := p.(newapi.DataCollectionPlugin)
	if !ok {
		t.Fatalf("mount/rclone does not implement newapi.DataCollectionPlugin")
	}
	sched := dc.CollectionSchedule()
	if sched.Interval != 60*time.Second || sched.TTL != 60*time.Second {
		t.Errorf("unexpected collection schedule for mount/rclone: %+v", sched)
	}
	schema := dc.CollectionSchema()
	if schema.Version != "v2" || schema.RootKey != "mounts" {
		t.Errorf("unexpected collection schema: %+v", schema)
	}

	// 2. ManagementPlugin & action parity
	mgmt, ok := p.(newapi.ManagementPlugin)
	if !ok {
		t.Fatalf("mount/rclone does not implement newapi.ManagementPlugin")
	}
	mgmtActions := mgmt.ManagementActions()
	legacyActions := p.(plugin.Actor).Actions()
	if len(mgmtActions) != 2 || len(legacyActions) != len(mgmtActions) {
		t.Fatalf("expected 2 management actions matching legacy Actions(), got mgmt=%d legacy=%d", len(mgmtActions), len(legacyActions))
	}
	for i, ma := range mgmtActions {
		if ma.Name != legacyActions[i].Name {
			t.Errorf("action[%d] name mismatch: %q vs %q", i, ma.Name, legacyActions[i].Name)
		}
		if ma.Name == "remove_remote" && (!ma.Dangerous || ma.ConfirmPrompt == "") {
			t.Errorf("expected remove_remote to be marked dangerous with a confirmation prompt: %+v", ma)
		}
	}

	facts := plugin.HostFacts{NodeID: "node1", OSFamily: "linux"}
	if err := mgmt.ValidateAction(facts, "install_remote", map[string]any{
		"name":          "hetzner",
		"configContent": "[hetzner]\ntype=sftp\n",
		"mountTarget":   "/mnt/rclone/hetzner",
		"policy":        "systemd-mount",
	}); err != nil {
		t.Errorf("expected valid install_remote options to pass ValidateAction, got %v", err)
	}
	if err := mgmt.ValidateAction(facts, "install_remote", map[string]any{"name": "hetzner"}); err == nil {
		t.Errorf("expected missing required options to fail ValidateAction")
	}
	if err := mgmt.ValidateAction(facts, "install_remote", map[string]any{
		"name":          "hetzner",
		"configContent": "x",
		"mountTarget":   "/mnt/rclone/hetzner",
		"policy":        "invalid-policy",
	}); err == nil {
		t.Errorf("expected invalid enum policy to fail ValidateAction")
	}
	if err := mgmt.ValidateAction(facts, "unknown_action", nil); err == nil {
		t.Errorf("expected unknown action to fail ValidateAction")
	}

	// 3. CategorizedSettingsProvider & settingsframework validation
	csp, ok := p.(newapi.CategorizedSettingsProvider)
	if !ok {
		t.Fatalf("mount/rclone does not implement newapi.CategorizedSettingsProvider")
	}
	catPage := csp.CategorizedSettingsPage()
	if err := settingsframework.ValidateSchema(catPage.SettingsPage); err != nil {
		t.Fatalf("ValidateSchema failed for mount/rclone settings page: %v", err)
	}
	if err := newapi.ValidateCategorizedSettingsPage(catPage); err != nil {
		t.Fatalf("ValidateCategorizedSettingsPage failed for mount/rclone: %v", err)
	}
	if catPage.ID != "mount/rclone" || catPage.Scope != newapi.SettingsScopeDatacenter || catPage.Category != "mount" {
		t.Errorf("unexpected categorized settings page metadata: %+v", catPage)
	}
	if _, ok := plugin.GetCategorizedSettingsPage("mount/rclone"); !ok {
		t.Errorf("expected mount/rclone categorized settings page to be indexed in DefaultRegistry")
	}

	// 4. ContextMenuProvider
	cmp, ok := p.(newapi.ContextMenuProvider)
	if !ok {
		t.Fatalf("mount/rclone does not implement newapi.ContextMenuProvider")
	}
	entries := cmp.ContextMenuEntries(context.Background(), facts)
	if len(entries) != 2 {
		t.Fatalf("expected 2 context menu entries for compatible linux host, got %d (%+v)", len(entries), entries)
	}
	for _, entry := range entries {
		if entry.PluginID != "mount/rclone" || entry.Target != newapi.TargetNode || entry.Action == "" {
			t.Errorf("unexpected context menu entry metadata: %+v", entry)
		}
	}
	if winEntries := cmp.ContextMenuEntries(context.Background(), plugin.HostFacts{OSFamily: "windows"}); len(winEntries) != 0 {
		t.Errorf("expected nil/empty context menu entries for incompatible windows host, got %+v", winEntries)
	}

	// 5. TaskCompileProvider
	tcp, ok := p.(newapi.TaskCompileProvider)
	if !ok {
		t.Fatalf("mount/rclone does not implement newapi.TaskCompileProvider")
	}
	compiled, err := tcp.CompileTask(context.Background(), facts, newapi.TaskRequest{
		Type:     newapi.TaskTypePluginAction,
		PluginID: "mount/rclone",
		Action:   "remove_remote",
		NodeID:   "node1",
		Params: map[string]any{
			"name":        "hetzner",
			"mountTarget": "/mnt/rclone/hetzner",
		},
	})
	if err != nil {
		t.Fatalf("CompileTask failed: %v", err)
	}
	if len(compiled.Steps) == 0 || compiled.Name != "Remove rclone remote hetzner" {
		t.Errorf("unexpected CompileTask result: %+v", compiled)
	}
}

func contains(list []string, needle string) bool {
	for _, s := range list {
		if s == needle {
			return true
		}
	}
	return false
}
