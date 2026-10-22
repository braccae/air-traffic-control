package smart_test

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
}

func (m *mockRunner) RunCommand(ctx context.Context, cmd string) (string, error) {
	if strings.Contains(cmd, "===SMART===") {
		return m.responses["===SMART==="], nil
	}
	for pattern, resp := range m.responses {
		if strings.Contains(cmd, pattern) {
			return resp, nil
		}
	}
	return "", nil
}

func TestSMARTPlugin_ProbeAndRead(t *testing.T) {
	p, ok := plugin.Get("disk/smart")
	if !ok {
		t.Fatalf("disk/smart not registered")
	}

	runner := &mockRunner{
		responses: map[string]string{
			"command -v lsblk": "lsblk",
			"smartctl --scan":  "/dev/nvme0n1 -d nvme # /dev/nvme0n1, NVM Express device",
			"===SMART===":      "{\"blockdevices\": [{\"name\": \"nvme0n1\", \"size\": 1000204886016, \"type\": \"disk\", \"model\": \"Samsung SSD 980\", \"rota\": false, \"children\": [{\"name\": \"nvme0n1p1\", \"size\": 536870912, \"type\": \"part\", \"fstype\": \"vfat\", \"mountpoints\": [\"/boot/efi\"]}]}]}\n===SMART===\n/dev/nvme0n1 -d nvme # /dev/nvme0n1, NVM Express device",
		},
	}

	det, err := p.Probe(context.Background(), runner, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatal(err)
	}
	if !det.Detected || !strings.Contains(det.Details, "SMART enabled") {
		t.Errorf("unexpected probe result: %+v", det)
	}

	reader := p.(plugin.Reader)
	data, err := reader.Read(context.Background(), runner, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}

	m := data.(map[string]any)
	disks := m["disks"].([]map[string]any)
	if len(disks) != 1 {
		t.Fatalf("expected 1 disk, got %d", len(disks))
	}
	if disks[0]["name"] != "nvme0n1" || disks[0]["smartStatus"] != "PASSED (0 Errors)" {
		t.Errorf("unexpected disk data: %+v", disks[0])
	}
}

func TestSMARTPlugin_Compile(t *testing.T) {
	p, ok := plugin.Get("disk/smart")
	if !ok {
		t.Fatalf("disk/smart not registered")
	}

	actor := p.(plugin.Actor)
	spec, err := actor.Compile(plugin.HostFacts{NodeID: "node1", OSFamily: "linux"}, "smart_test", map[string]any{
		"disk": "/dev/nvme0n1",
	})
	if err != nil {
		t.Fatalf("Compile failed: %v", err)
	}
	if spec.Name != "SMART self-test on /dev/nvme0n1" {
		t.Errorf("unexpected task name: %s", spec.Name)
	}
	if len(spec.Steps) != 1 || spec.Steps[0].Module != "ansible.builtin.command" {
		t.Fatalf("unexpected steps: %+v", spec.Steps)
	}
	if cmd, _ := spec.Steps[0].Params["cmd"].(string); cmd != "smartctl -t short /dev/nvme0n1" {
		t.Errorf("unexpected command: %s", cmd)
	}
}

func TestSMARTPlugin_NewAPIInterfacesAndSchemas(t *testing.T) {
	p, ok := plugin.Get("disk/smart")
	if !ok {
		t.Fatalf("disk/smart not registered in DefaultRegistry")
	}

	// 1. DataCollectionPlugin
	dc, ok := p.(newapi.DataCollectionPlugin)
	if !ok {
		t.Fatalf("disk/smart does not implement newapi.DataCollectionPlugin")
	}
	sched := dc.CollectionSchedule()
	if sched.Interval != 60*time.Second || sched.TTL != 60*time.Second {
		t.Errorf("unexpected collection schedule for disk/smart: %+v", sched)
	}
	schema := dc.CollectionSchema()
	if schema.Version != "v2" || schema.RootKey != "disks" {
		t.Errorf("unexpected collection schema: %+v", schema)
	}

	// 2. ManagementPlugin & action parity
	mgmt, ok := p.(newapi.ManagementPlugin)
	if !ok {
		t.Fatalf("disk/smart does not implement newapi.ManagementPlugin")
	}
	mgmtActions := mgmt.ManagementActions()
	legacyActions := p.(plugin.Actor).Actions()
	if len(mgmtActions) != 1 || len(legacyActions) != len(mgmtActions) {
		t.Fatalf("expected 1 management action matching legacy Actions(), got mgmt=%d legacy=%d", len(mgmtActions), len(legacyActions))
	}
	if mgmtActions[0].Name != "smart_test" || legacyActions[0].Name != "smart_test" {
		t.Errorf("action name mismatch: mgmt=%q legacy=%q", mgmtActions[0].Name, legacyActions[0].Name)
	}
	if mgmtActions[0].Category != newapi.ActionCategoryDiagnostic {
		t.Errorf("expected diagnostic category, got %q", mgmtActions[0].Category)
	}

	facts := plugin.HostFacts{NodeID: "node1", OSFamily: "linux"}
	if err := mgmt.ValidateAction(facts, "smart_test", map[string]any{"disk": "sda"}); err != nil {
		t.Errorf("expected valid smart_test options to pass ValidateAction, got %v", err)
	}
	if err := mgmt.ValidateAction(facts, "short_test", map[string]any{"device": "/dev/nvme0n1"}); err != nil {
		t.Errorf("expected legacy alias short_test with device option to pass ValidateAction, got %v", err)
	}
	if err := mgmt.ValidateAction(facts, "smart_test", map[string]any{}); err == nil {
		t.Errorf("expected missing required disk option to fail ValidateAction")
	}
	if err := mgmt.ValidateAction(facts, "unknown_action", nil); err == nil {
		t.Errorf("expected unknown action to fail ValidateAction")
	}

	// 3. CategorizedSettingsProvider & settingsframework validation
	csp, ok := p.(newapi.CategorizedSettingsProvider)
	if !ok {
		t.Fatalf("disk/smart does not implement newapi.CategorizedSettingsProvider")
	}
	catPage := csp.CategorizedSettingsPage()
	if err := settingsframework.ValidateSchema(catPage.SettingsPage); err != nil {
		t.Fatalf("ValidateSchema failed for disk/smart settings page: %v", err)
	}
	if err := newapi.ValidateCategorizedSettingsPage(catPage); err != nil {
		t.Fatalf("ValidateCategorizedSettingsPage failed for disk/smart: %v", err)
	}
	if catPage.ID != "disk/smart" || catPage.Scope != newapi.SettingsScopeDatacenter || catPage.Category != "disk" {
		t.Errorf("unexpected categorized settings page metadata: %+v", catPage)
	}
	if _, ok := plugin.DefaultRegistry().GetSettingsPage("disk/smart"); !ok {
		t.Errorf("expected disk/smart settings page to be indexed in DefaultRegistry")
	}

	// 4. ContextMenuProvider
	cmp, ok := p.(newapi.ContextMenuProvider)
	if !ok {
		t.Fatalf("disk/smart does not implement newapi.ContextMenuProvider")
	}
	entries := cmp.ContextMenuEntries(context.Background(), facts)
	if len(entries) != 1 {
		t.Fatalf("expected 1 context menu entry for compatible linux host, got %d (%+v)", len(entries), entries)
	}
	if entries[0].PluginID != "disk/smart" || entries[0].Target != newapi.TargetNode || entries[0].Action != "smart_test" {
		t.Errorf("unexpected context menu entry metadata: %+v", entries[0])
	}
	if winEntries := cmp.ContextMenuEntries(context.Background(), plugin.HostFacts{OSFamily: "windows"}); len(winEntries) != 0 {
		t.Errorf("expected nil/empty context menu entries for incompatible windows host, got %+v", winEntries)
	}

	// 5. TaskCompileProvider
	tcp, ok := p.(newapi.TaskCompileProvider)
	if !ok {
		t.Fatalf("disk/smart does not implement newapi.TaskCompileProvider")
	}
	compiled, err := tcp.CompileTask(context.Background(), facts, newapi.TaskRequest{
		Type:     newapi.TaskTypePluginAction,
		PluginID: "disk/smart",
		Action:   "smart_test",
		NodeID:   "node1",
		Params: map[string]any{
			"disk": "sda",
		},
	})
	if err != nil {
		t.Fatalf("CompileTask failed: %v", err)
	}
	if len(compiled.Steps) != 1 || compiled.Steps[0].Module != "ansible.builtin.command" {
		t.Errorf("unexpected CompileTask steps: %+v", compiled.Steps)
	}
	if _, err := tcp.CompileTask(context.Background(), facts, newapi.TaskRequest{PluginID: "disk/smart"}); err == nil {
		t.Errorf("expected CompileTask with empty action to fail")
	}
}
