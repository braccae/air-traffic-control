package nfs_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"air-traffic-control/backend/pkg/plugin"
	"air-traffic-control/backend/pkg/plugin/newapi"
	"air-traffic-control/backend/pkg/plugin/settingsframework"
	_ "air-traffic-control/backend/plugins/all"
)

type mockRunner struct {
	responses map[string]string
	err       error
}

func (m *mockRunner) RunCommand(ctx context.Context, cmd string) (string, error) {
	if m.err != nil {
		return "", m.err
	}
	for pattern, resp := range m.responses {
		if strings.Contains(cmd, pattern) {
			return resp, nil
		}
	}
	return "", nil
}

func TestNFSPlugin_RegisteredAndBaseContract(t *testing.T) {
	p, ok := plugin.Get("share/nfs")
	if !ok {
		t.Fatal("share/nfs plugin not registered")
	}
	meta := p.Meta()
	if meta.ID != "share/nfs" || meta.Kind != plugin.KindShare || meta.Name != "nfs" {
		t.Errorf("unexpected meta: %+v", meta)
	}
	if !p.Compatible(plugin.HostFacts{OSFamily: "linux"}) {
		t.Error("expected share/nfs to be compatible with linux")
	}
	if p.Compatible(plugin.HostFacts{OSFamily: "windows"}) {
		t.Error("expected share/nfs to be incompatible with windows")
	}
	if p.Compatible(plugin.HostFacts{OSFamily: "router_switch"}) {
		t.Error("expected share/nfs to be incompatible with router_switch")
	}
	reqs := p.Requirements()
	if len(reqs.HostPackages["dnf"]) == 0 || len(reqs.HostPackages["apt"]) == 0 || len(reqs.HostPackages["apk"]) == 0 {
		t.Errorf("expected host packages for dnf/apt/apk, got %+v", reqs)
	}
}

func TestNFSPlugin_ProbeReadAndCompile(t *testing.T) {
	p, ok := plugin.Get("share/nfs")
	if !ok {
		t.Fatalf("share/nfs not registered")
	}

	// 1. Nil runner -> OFFLINE
	det, err := p.Probe(context.Background(), nil, plugin.HostFacts{OSFamily: "linux"})
	if err != nil || det.Detected || det.Status != "OFFLINE" {
		t.Errorf("expected OFFLINE for nil runner, got %+v (err=%v)", det, err)
	}

	// 2. Active exports detected
	runner := &mockRunner{
		responses: map[string]string{
			"cat /var/lib/nfs/etab /etc/exports": "# comment line\n/export/media *(rw,sync)\n/export/data 100.64.0.0/10(rw)",
		},
	}
	det, err = p.Probe(context.Background(), runner, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatal(err)
	}
	if !det.Detected || det.Status != "2 export(s)" {
		t.Errorf("unexpected probe result: %+v", det)
	}

	// 3. No exports, but nfs-server active -> ONLINE
	svcRunner := &mockRunner{
		responses: map[string]string{
			"systemctl is-active nfs-server": "active",
		},
	}
	det, err = p.Probe(context.Background(), svcRunner, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatal(err)
	}
	if !det.Detected || det.Status != "ONLINE" {
		t.Errorf("expected ONLINE when nfs-server active, got %+v", det)
	}

	// 4. Neither exports nor service -> UNAVAILABLE
	emptyRunner := &mockRunner{responses: map[string]string{}}
	det, err = p.Probe(context.Background(), emptyRunner, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatal(err)
	}
	if det.Detected || det.Status != "UNAVAILABLE" {
		t.Errorf("expected UNAVAILABLE when no exports or service found, got %+v", det)
	}

	// 5. Read
	reader := p.(plugin.Reader)
	if _, err := reader.Read(context.Background(), nil, plugin.HostFacts{OSFamily: "linux"}); err == nil {
		t.Error("expected error when reading with nil runner")
	}
	readOut, err := reader.Read(context.Background(), runner, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	data := readOut.(map[string]any)
	nfsStr, _ := data["nfs"].(string)
	if !strings.Contains(nfsStr, "/export/media *(rw,sync)") {
		t.Errorf("unexpected read output: %+v", data)
	}

	// 6. Compile (explicit and default options)
	actor := p.(plugin.Actor)
	task, err := actor.Compile(plugin.HostFacts{NodeID: "node1"}, "export", map[string]any{
		"path":    "/srv/nfs",
		"client":  "192.168.1.0/24",
		"options": "rw,async,no_root_squash",
		"state":   "present",
	})
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}
	if len(task.Steps) != 2 || task.Steps[0].Module != "ansible.builtin.lineinfile" || task.Steps[1].Module != "ansible.builtin.command" {
		t.Fatalf("unexpected task steps: %+v", task.Steps)
	}
	if task.Steps[0].Params["line"] != "/srv/nfs 192.168.1.0/24(rw,async,no_root_squash)" {
		t.Errorf("unexpected export line: %v", task.Steps[0].Params["line"])
	}

	defaultTask, err := actor.Compile(plugin.HostFacts{NodeID: "node1"}, "nfs", map[string]any{})
	if err != nil {
		t.Fatalf("compile with defaults and 'nfs' alias failed: %v", err)
	}
	if defaultTask.Steps[0].Params["line"] != "/export *(rw,sync,no_subtree_check)" {
		t.Errorf("unexpected default export line: %v", defaultTask.Steps[0].Params["line"])
	}

	if _, err := actor.Compile(plugin.HostFacts{NodeID: "node1"}, "unknown_action", map[string]any{}); err == nil {
		t.Error("expected error for unknown action")
	}
}

func TestNFSPlugin_NewAPICompliance(t *testing.T) {
	p, ok := plugin.Get("share/nfs")
	if !ok {
		t.Fatalf("share/nfs not registered in DefaultRegistry")
	}

	// 1. DataCollectionPlugin
	dc, ok := p.(newapi.DataCollectionPlugin)
	if !ok {
		t.Fatalf("share/nfs does not implement newapi.DataCollectionPlugin")
	}
	sched := dc.CollectionSchedule()
	if sched.Interval != 60*time.Second || sched.TTL != 60*time.Second {
		t.Errorf("unexpected collection schedule for share/nfs: %+v", sched)
	}
	schema := dc.CollectionSchema()
	if schema.Version != "v2" || schema.RootKey != "nfs" {
		t.Errorf("unexpected collection schema: %+v", schema)
	}

	// 2. ManagementPlugin & action parity
	mgmt, ok := p.(newapi.ManagementPlugin)
	if !ok {
		t.Fatalf("share/nfs does not implement newapi.ManagementPlugin")
	}
	mgmtActions := mgmt.ManagementActions()
	legacyActions := p.(plugin.Actor).Actions()
	if len(mgmtActions) != 1 || len(legacyActions) != len(mgmtActions) {
		t.Fatalf("expected 1 management action matching legacy Actions(), got mgmt=%d legacy=%d", len(mgmtActions), len(legacyActions))
	}
	if mgmtActions[0].Name != "export" || legacyActions[0].Name != "export" {
		t.Errorf("unexpected action name: %+v", mgmtActions[0])
	}
	if !mgmtActions[0].Idempotent || len(mgmtActions[0].InvalidatesKinds) == 0 || mgmtActions[0].InvalidatesKinds[0] != plugin.KindShare {
		t.Errorf("unexpected management metadata: %+v", mgmtActions[0])
	}

	facts := plugin.HostFacts{NodeID: "node1", OSFamily: "linux"}
	if err := mgmt.ValidateAction(facts, "export", map[string]any{
		"path":   "/export/data",
		"client": "10.0.0.0/8",
		"state":  "present",
	}); err != nil {
		t.Errorf("expected valid export options to pass ValidateAction, got %v", err)
	}
	if err := mgmt.ValidateAction(facts, "nfs", map[string]any{
		"path": "/export/data",
	}); err != nil {
		t.Errorf("expected 'nfs' alias with valid path to pass ValidateAction, got %v", err)
	}
	if err := mgmt.ValidateAction(facts, "export", map[string]any{}); err == nil {
		t.Errorf("expected missing required 'path' option to fail ValidateAction")
	}
	if err := mgmt.ValidateAction(facts, "export", map[string]any{
		"path":  "/export/data",
		"state": "invalid_state",
	}); err == nil {
		t.Errorf("expected invalid enum 'state' option to fail ValidateAction")
	}
	if err := mgmt.ValidateAction(facts, "unknown_action", nil); err == nil {
		t.Errorf("expected unknown action to fail ValidateAction")
	}

	// 3. CategorizedSettingsProvider & settingsframework validation
	csp, ok := p.(newapi.CategorizedSettingsProvider)
	if !ok {
		t.Fatalf("share/nfs does not implement newapi.CategorizedSettingsProvider")
	}
	catPage := csp.CategorizedSettingsPage()
	if err := settingsframework.ValidateSchema(catPage.SettingsPage); err != nil {
		t.Fatalf("ValidateSchema failed for share/nfs settings page: %v", err)
	}
	if err := newapi.ValidateCategorizedSettingsPage(catPage); err != nil {
		t.Fatalf("ValidateCategorizedSettingsPage failed for share/nfs: %v", err)
	}
	if catPage.ID != "share/nfs" || catPage.Scope != newapi.SettingsScopeDatacenter || catPage.Category != "share" {
		t.Errorf("unexpected categorized settings page metadata: %+v", catPage)
	}
	if _, ok := plugin.DefaultRegistry().GetSettingsPage("share/nfs"); !ok {
		t.Errorf("expected share/nfs settings page to be indexed in DefaultRegistry")
	}

	// 4. ContextMenuProvider
	cmp, ok := p.(newapi.ContextMenuProvider)
	if !ok {
		t.Fatalf("share/nfs does not implement newapi.ContextMenuProvider")
	}
	entries := cmp.ContextMenuEntries(context.Background(), facts)
	if len(entries) != 1 {
		t.Fatalf("expected 1 context menu entry for compatible linux host, got %d (%+v)", len(entries), entries)
	}
	if entries[0].PluginID != "share/nfs" || entries[0].Target != newapi.TargetShare || entries[0].Action != "export" {
		t.Errorf("unexpected context menu entry metadata: %+v", entries[0])
	}
	if winEntries := cmp.ContextMenuEntries(context.Background(), plugin.HostFacts{OSFamily: "windows"}); len(winEntries) != 0 {
		t.Errorf("expected nil/empty context menu entries for incompatible windows host, got %+v", winEntries)
	}
	if routerEntries := cmp.ContextMenuEntries(context.Background(), plugin.HostFacts{OSFamily: "router_switch"}); len(routerEntries) != 0 {
		t.Errorf("expected nil/empty context menu entries for incompatible router_switch host, got %+v", routerEntries)
	}

	// 5. TaskCompileProvider
	tcp, ok := p.(newapi.TaskCompileProvider)
	if !ok {
		t.Fatalf("share/nfs does not implement newapi.TaskCompileProvider")
	}
	compiled, err := tcp.CompileTask(context.Background(), facts, newapi.TaskRequest{
		Type:     newapi.TaskTypePluginAction,
		PluginID: "share/nfs",
		Action:   "export",
		NodeID:   "node1",
		Params: map[string]any{
			"path":    "/srv/media",
			"client":  "100.64.0.0/10",
			"options": "ro,sync,no_subtree_check",
		},
	})
	if err != nil {
		t.Fatalf("CompileTask failed: %v", err)
	}
	if len(compiled.Steps) != 2 || compiled.Steps[0].Params["line"] != "/srv/media 100.64.0.0/10(ro,sync,no_subtree_check)" {
		t.Errorf("unexpected CompileTask steps: %+v", compiled.Steps)
	}

	compiledViaParam, err := tcp.CompileTask(context.Background(), facts, newapi.TaskRequest{
		Type:     newapi.TaskTypePluginAction,
		PluginID: "share/nfs",
		NodeID:   "node1",
		Params: map[string]any{
			"action": "export",
			"path":   "/srv/backups",
		},
	})
	if err != nil {
		t.Fatalf("CompileTask with action in Params failed: %v", err)
	}
	if len(compiledViaParam.Steps) != 2 || compiledViaParam.Steps[0].Params["line"] != "/srv/backups *(rw,sync,no_subtree_check)" {
		t.Errorf("unexpected CompileTask steps: %+v", compiledViaParam.Steps)
	}

	if _, err := tcp.CompileTask(context.Background(), facts, newapi.TaskRequest{
		Type:     newapi.TaskTypePluginAction,
		PluginID: "share/nfs",
		NodeID:   "node1",
	}); err == nil {
		t.Errorf("expected CompileTask with empty action to return an error")
	}
}
