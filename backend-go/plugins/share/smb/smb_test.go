package smb_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"air-traffic-control/backend/pkg/plugin"
	"air-traffic-control/backend/pkg/plugin/newapi"
	"air-traffic-control/backend/pkg/plugin/settingsframework"
	_ "air-traffic-control/backend/plugins/all"
	"air-traffic-control/backend/plugins/share/smb"
)

type mockRunner struct {
	responses map[string]string
}

func (m *mockRunner) RunCommand(ctx context.Context, cmd string) (string, error) {
	for pattern, resp := range m.responses {
		if strings.Contains(cmd, pattern) {
			return resp, nil
		}
	}
	return "", nil
}

func TestSMBPlugin_MetaAndCompatibility(t *testing.T) {
	p, ok := plugin.Get("share/smb")
	if !ok {
		t.Fatalf("share/smb plugin not registered")
	}

	meta := p.Meta()
	if meta.ID != "share/smb" || meta.Kind != plugin.KindShare || meta.Name != "smb" || meta.Order != 20 {
		t.Errorf("unexpected meta: %+v", meta)
	}

	if !p.Compatible(plugin.HostFacts{OSFamily: "linux"}) {
		t.Errorf("expected compatible with linux")
	}
	if p.Compatible(plugin.HostFacts{OSFamily: "windows"}) {
		t.Errorf("expected incompatible with windows")
	}
	if p.Compatible(plugin.HostFacts{OSFamily: "router_switch"}) {
		t.Errorf("expected incompatible with router_switch")
	}
}

func TestSMBPlugin_NewAPIInterfaces(t *testing.T) {
	p, ok := plugin.Get("share/smb")
	if !ok {
		t.Fatal("share/smb not registered")
	}

	// 1. DataCollectionPlugin
	dc, ok := p.(newapi.DataCollectionPlugin)
	if !ok {
		t.Fatal("expected share/smb to implement newapi.DataCollectionPlugin")
	}
	sched := dc.CollectionSchedule()
	if sched.TTL != 60*time.Second || sched.Interval != 60*time.Second {
		t.Errorf("expected 60s schedule for share/smb, got %+v", sched)
	}
	schema := dc.CollectionSchema()
	if schema.Version != "v2" || schema.RootKey != "samba" {
		t.Errorf("unexpected collection schema: %+v", schema)
	}

	// 2. ManagementPlugin
	mgmt, ok := p.(newapi.ManagementPlugin)
	if !ok {
		t.Fatal("expected share/smb to implement newapi.ManagementPlugin")
	}
	mgmtActions := mgmt.ManagementActions()
	if len(mgmtActions) != 3 {
		t.Fatalf("expected 3 management actions, got %d", len(mgmtActions))
	}
	if len(p.(plugin.Actor).Actions()) != len(mgmtActions) {
		t.Errorf("expected Actions() length %d to match ManagementActions() length %d", len(p.(plugin.Actor).Actions()), len(mgmtActions))
	}

	facts := plugin.HostFacts{NodeID: "node1", OSFamily: "linux"}
	if err := mgmt.ValidateAction(facts, "share", map[string]any{"name": "media", "path": "/srv/media"}); err != nil {
		t.Errorf("expected valid share action, got: %v", err)
	}
	if err := mgmt.ValidateAction(facts, "create", map[string]any{"shareName": "media", "sharePath": "/srv/media"}); err != nil {
		t.Errorf("expected valid create alias action, got: %v", err)
	}
	if err := mgmt.ValidateAction(facts, "reload", map[string]any{}); err != nil {
		t.Errorf("expected valid reload action, got: %v", err)
	}
	if err := mgmt.ValidateAction(facts, "remove", map[string]any{"name": "media"}); err != nil {
		t.Errorf("expected valid remove action, got: %v", err)
	}
	if err := mgmt.ValidateAction(facts, "share", map[string]any{"name": "media"}); err == nil {
		t.Error("expected error when required path option is missing")
	}
	if err := mgmt.ValidateAction(facts, "remove", map[string]any{}); err == nil {
		t.Error("expected error when required name option is missing for remove")
	}
	if err := mgmt.ValidateAction(facts, "unknown_action", map[string]any{}); err == nil {
		t.Error("expected error for unknown action")
	}
}

func TestSMBPlugin_SettingsFramework(t *testing.T) {
	p, ok := plugin.Get("share/smb")
	if !ok {
		t.Fatal("share/smb not registered")
	}

	csp, ok := p.(newapi.CategorizedSettingsProvider)
	if !ok {
		t.Fatal("expected share/smb to implement newapi.CategorizedSettingsProvider")
	}

	catPage := csp.CategorizedSettingsPage()
	if catPage.ID != smb.SettingsPageID {
		t.Errorf("expected settings page ID %q, got %q", smb.SettingsPageID, catPage.ID)
	}
	if catPage.Scope != newapi.SettingsScopeDatacenter || catPage.Category != "share" {
		t.Errorf("expected datacenter/share categorized page, got scope=%q category=%q", catPage.Scope, catPage.Category)
	}
	if err := settingsframework.ValidateSchema(catPage.SettingsPage); err != nil {
		t.Fatalf("ValidateSchema failed: %v", err)
	}
	if err := newapi.ValidateCategorizedSettingsPage(catPage); err != nil {
		t.Fatalf("ValidateCategorizedSettingsPage failed: %v", err)
	}

	if _, ok := plugin.GetSettingsPage("share/smb"); !ok {
		t.Error("expected share/smb settings page to be registered in global registry")
	}
	if regCat, ok := plugin.GetCategorizedSettingsPage("share/smb"); !ok || regCat.Category != "share" {
		t.Errorf("expected share/smb categorized settings page in global registry, got %+v (ok=%v)", regCat, ok)
	}

	if got := smb.WorkgroupDefault(); got != smb.DefaultWorkgroup {
		t.Errorf("WorkgroupDefault() = %q, want %q", got, smb.DefaultWorkgroup)
	}
	if got := smb.ValidUsersDefault(); got != smb.DefaultValidUsers {
		t.Errorf("ValidUsersDefault() = %q, want %q", got, smb.DefaultValidUsers)
	}
	if smb.ReadOnlyDefault() {
		t.Error("expected ReadOnlyDefault() false")
	}
	if smb.GuestOkDefault() {
		t.Error("expected GuestOkDefault() false")
	}
	if _, ok := smb.SmbPasswordVaultVar(); ok {
		t.Error("expected SmbPasswordVaultVar() unset by default")
	}
}

func TestSMBPlugin_ContextMenu(t *testing.T) {
	p, ok := plugin.Get("share/smb")
	if !ok {
		t.Fatal("share/smb not registered")
	}

	cmp, ok := p.(newapi.ContextMenuProvider)
	if !ok {
		t.Fatal("expected share/smb to implement newapi.ContextMenuProvider")
	}

	ctx := context.Background()
	linuxFacts := plugin.HostFacts{NodeID: "node1", OSFamily: "linux"}
	entries := cmp.ContextMenuEntries(ctx, linuxFacts)
	if len(entries) != 3 {
		t.Fatalf("expected 3 context menu entries for linux host, got %d", len(entries))
	}
	if entries[0].ID != "smb-share" || entries[0].Target != newapi.TargetShare || entries[0].PluginID != "share/smb" {
		t.Errorf("unexpected first context menu entry: %+v", entries[0])
	}
	if entries[2].ID != "smb-remove" || !entries[2].Danger || entries[2].ConfirmPrompt == "" {
		t.Errorf("expected danger entry last, got %+v", entries[2])
	}

	// Incompatible host returns nil
	winEntries := cmp.ContextMenuEntries(ctx, plugin.HostFacts{NodeID: "win1", OSFamily: "windows"})
	if len(winEntries) != 0 {
		t.Errorf("expected 0 context menu entries for windows host, got %d", len(winEntries))
	}

	// Global registry query
	regEntries := plugin.ListContextMenuEntries(ctx, newapi.TargetShare, linuxFacts)
	foundSMB := false
	for _, e := range regEntries {
		if e.PluginID == "share/smb" {
			foundSMB = true
			break
		}
	}
	if !foundSMB {
		t.Error("expected global registry ListContextMenuEntries(TargetShare) to include share/smb entries")
	}
}

func TestSMBPlugin_ProbeAndRead(t *testing.T) {
	p, ok := plugin.Get("share/smb")
	if !ok {
		t.Fatal("share/smb not registered")
	}

	// Nil runner -> OFFLINE
	det, err := p.Probe(context.Background(), nil, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatalf("probe error on nil runner: %v", err)
	}
	if det.Detected || det.Status != "OFFLINE" {
		t.Errorf("expected OFFLINE on nil runner, got %+v", det)
	}

	// Configured share -> ONLINE
	runner := &mockRunner{
		responses: map[string]string{
			"testparm": "[global]\nworkgroup = SAMBA\n[media]\npath = /srv/media\n===NET_CONF===\n[backups]\npath = /srv/backups",
		},
	}
	det, err = p.Probe(context.Background(), runner, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatalf("probe error: %v", err)
	}
	if !det.Detected || det.Status != "ONLINE" {
		t.Errorf("expected ONLINE detection, got %+v", det)
	}

	// Active service fallback -> ONLINE
	svcRunner := &mockRunner{
		responses: map[string]string{
			"systemctl is-active": "inactive\nactive",
		},
	}
	det, err = p.Probe(context.Background(), svcRunner, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatalf("probe error: %v", err)
	}
	if !det.Detected || det.Status != "ONLINE" {
		t.Errorf("expected ONLINE detection via systemctl, got %+v", det)
	}

	// Read
	reader := p.(plugin.Reader)
	res, err := reader.Read(context.Background(), runner, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatalf("read error: %v", err)
	}
	m, ok := res.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any from Read, got %T", res)
	}
	if !strings.Contains(m["samba"].(string), "[media]") {
		t.Errorf("unexpected samba output: %v", m["samba"])
	}
	if !strings.Contains(m["netConf"].(string), "[backups]") {
		t.Errorf("unexpected netConf output: %v", m["netConf"])
	}
}

func TestSMBPlugin_CompileAndCompileTask(t *testing.T) {
	p, ok := plugin.Get("share/smb")
	if !ok {
		t.Fatal("share/smb not registered")
	}

	actor := p.(plugin.Actor)
	facts := plugin.HostFacts{NodeID: "node1", OSFamily: "linux"}

	// 1. Share / Create action with secrets masking
	task, err := actor.Compile(facts, "share", map[string]any{
		"name":             "backups",
		"path":             "/mnt/backups",
		"users":            []string{"core", "media"},
		"read_only":        true,
		"guest_ok":         false,
		"comment":          "Backup storage",
		"passwordVaultKey": "vault_smb_pwd",
	})
	if err != nil {
		t.Fatalf("compile share failed: %v", err)
	}
	if len(task.Steps) != 1 || task.Steps[0].Module != "ansible.builtin.template" {
		t.Fatalf("unexpected task steps: %+v", task.Steps)
	}
	params := task.Steps[0].Params
	if params["dest"] != "/etc/samba/smb.conf.d/backups.conf" || params["share_path"] != "/mnt/backups" || params["valid_users"] != "core media" || params["read_only"] != "yes" || params["guest_ok"] != "no" {
		t.Errorf("unexpected template params: %+v", params)
	}
	if task.SecretsMasked["smb_password"] != "•••••••• (vault: vault_smb_pwd)" {
		t.Errorf("expected masked secret in task: %+v", task.SecretsMasked)
	}

	// 2. Create alias action (used by frontend storageDriver)
	createTask, err := actor.Compile(facts, "create", map[string]any{
		"shareName": "photos",
		"sharePath": "/tank/photos",
		"readOnly":  false,
		"guestOk":   true,
		"password":  "rawsecret",
	})
	if err != nil {
		t.Fatalf("compile create failed: %v", err)
	}
	if createTask.Steps[0].Params["dest"] != "/etc/samba/smb.conf.d/photos.conf" || createTask.SecretsMasked["smb_password"] != "••••••••" {
		t.Errorf("unexpected create task: %+v", createTask)
	}

	// 3. Reload action
	reloadTask, err := actor.Compile(facts, "reload", map[string]any{})
	if err != nil {
		t.Fatalf("compile reload failed: %v", err)
	}
	if len(reloadTask.Steps) != 1 || reloadTask.Steps[0].Module != "ansible.builtin.command" {
		t.Errorf("unexpected reload steps: %+v", reloadTask.Steps)
	}

	// 4. Remove action
	removeTask, err := actor.Compile(facts, "remove", map[string]any{"name": "oldshare"})
	if err != nil {
		t.Fatalf("compile remove failed: %v", err)
	}
	if len(removeTask.Steps) != 2 || removeTask.Steps[0].Module != "ansible.builtin.file" {
		t.Errorf("unexpected remove steps: %+v", removeTask.Steps)
	}

	// 5. TaskCompileProvider
	tcp, ok := p.(newapi.TaskCompileProvider)
	if !ok {
		t.Fatal("expected share/smb to implement newapi.TaskCompileProvider")
	}
	compiledTask, err := tcp.CompileTask(context.Background(), facts, newapi.TaskRequest{
		ID:       "req-smb-1",
		Type:     newapi.TaskTypePluginAction,
		NodeID:   "node1",
		PluginID: "share/smb",
		Action:   "share",
		Params: map[string]any{
			"name": "media",
			"path": "/srv/media",
		},
	})
	if err != nil {
		t.Fatalf("CompileTask failed: %v", err)
	}
	if compiledTask.Name != "Configure share media" {
		t.Errorf("unexpected CompileTask name: %q", compiledTask.Name)
	}
}
