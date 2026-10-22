package libvirt_test

import (
	"context"
	"strings"
	"testing"

	"air-traffic-control/backend/pkg/plugin"
	"air-traffic-control/backend/pkg/plugin/newapi"
	"air-traffic-control/backend/pkg/plugin/settingsframework"
	_ "air-traffic-control/backend/plugins/all"
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

func TestLibvirtPlugin_ProbeReadAndCompile(t *testing.T) {
	p, ok := plugin.Get("vm/libvirt")
	if !ok {
		t.Fatalf("vm/libvirt not registered")
	}

	runner := &mockRunner{
		responses: map[string]string{
			"which virsh": "/usr/bin/virsh\n===VIRSH_LIST===\n Id   Name       State\n------------------------\n 1    win11      running\n 2    ubuntu-vm  shut off\n===SERVICES===\nactive\n===KVM===\nkvm_amd",
		},
	}

	det, err := p.Probe(context.Background(), runner, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatal(err)
	}
	if !det.Detected {
		t.Errorf("expected detected virsh")
	}

	reader := p.(plugin.Reader)
	data, err := reader.Read(context.Background(), runner, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}

	m := data.(map[string]any)
	vms := m["vms"].([]map[string]any)
	if len(vms) != 2 {
		t.Fatalf("expected 2 vms, got %d", len(vms))
	}
	if vms[0]["status"] != "RUNNING" || vms[1]["status"] != "STOPPED" {
		t.Errorf("unexpected vm statuses: %+v", vms)
	}

	actor := p.(plugin.Actor)
	task, err := actor.Compile(plugin.HostFacts{NodeID: "node1"}, "start", map[string]any{
		"name": "ubuntu-vm",
	})
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}
	if len(task.Steps) != 1 || task.Steps[0].Module != "community.general.virt" {
		t.Errorf("unexpected task steps: %+v", task.Steps)
	}
	if task.Steps[0].Params["state"] != "running" {
		t.Errorf("expected state running, got %v", task.Steps[0].Params["state"])
	}
}

func TestLibvirtPlugin_V2InterfacesAndCapabilities(t *testing.T) {
	p, ok := plugin.Get("vm/libvirt")
	if !ok {
		t.Fatalf("vm/libvirt not registered")
	}

	// 1. DataCollectionPlugin
	dc, ok := p.(newapi.DataCollectionPlugin)
	if !ok {
		t.Fatal("vm/libvirt does not implement newapi.DataCollectionPlugin")
	}
	sched := dc.CollectionSchedule()
	if sched.Interval <= 0 || sched.TTL <= 0 {
		t.Errorf("expected positive collection schedule interval and TTL, got %+v", sched)
	}
	schema := dc.CollectionSchema()
	if schema.Version != "v2" || schema.RootKey != "vms" {
		t.Errorf("unexpected collection schema: %+v", schema)
	}

	// 2. CategorizedSettingsProvider
	csp, ok := p.(newapi.CategorizedSettingsProvider)
	if !ok {
		t.Fatal("vm/libvirt does not implement newapi.CategorizedSettingsProvider")
	}
	catPage := csp.CategorizedSettingsPage()
	if catPage.ID != "vm/libvirt" || catPage.Category != "vm" || catPage.Scope != newapi.SettingsScopeDatacenter {
		t.Errorf("unexpected categorized settings page: %+v", catPage)
	}
	if err := settingsframework.ValidateSchema(catPage.SettingsPage); err != nil {
		t.Fatalf("ValidateSchema failed: %v", err)
	}
	if err := newapi.ValidateCategorizedSettingsPage(catPage); err != nil {
		t.Fatalf("ValidateCategorizedSettingsPage failed: %v", err)
	}

	// 3. ManagementPlugin
	mp, ok := p.(newapi.ManagementPlugin)
	if !ok {
		t.Fatal("vm/libvirt does not implement newapi.ManagementPlugin")
	}
	mgmtActions := mp.ManagementActions()
	if len(mgmtActions) != 8 {
		t.Fatalf("expected 8 management actions, got %d", len(mgmtActions))
	}
	if len(mp.Actions()) != len(mgmtActions) {
		t.Errorf("Actions() length %d does not match ManagementActions() length %d", len(mp.Actions()), len(mgmtActions))
	}

	linuxFacts := plugin.HostFacts{NodeID: "node-1", OSFamily: "linux"}
	if err := mp.ValidateAction(linuxFacts, "start", map[string]any{"name": "ubuntu-vm"}); err != nil {
		t.Errorf("expected valid start options, got: %v", err)
	}
	if err := mp.ValidateAction(linuxFacts, "shutdown", map[string]any{"vmid": "ubuntu-vm"}); err != nil {
		t.Errorf("expected valid shutdown options with vmid fallback, got: %v", err)
	}
	if err := mp.ValidateAction(linuxFacts, "start", map[string]any{}); err == nil {
		t.Error("expected validation error when required name option is missing")
	}
	if err := mp.ValidateAction(linuxFacts, "nonexistent", map[string]any{"name": "ubuntu-vm"}); err == nil {
		t.Error("expected validation error for unsupported action")
	}

	// 4. ContextMenuProvider
	cmp, ok := p.(newapi.ContextMenuProvider)
	if !ok {
		t.Fatal("vm/libvirt does not implement newapi.ContextMenuProvider")
	}
	entries := cmp.ContextMenuEntries(context.Background(), linuxFacts)
	if len(entries) != 8 {
		t.Fatalf("expected 8 context menu entries for compatible linux host, got %d", len(entries))
	}
	for _, entry := range entries {
		if entry.PluginID != "vm/libvirt" || entry.Target != newapi.TargetVM {
			t.Errorf("unexpected context menu entry metadata: %+v", entry)
		}
	}
	if entries[len(entries)-1].Action != "destroy" || !entries[len(entries)-1].Danger {
		t.Errorf("expected last context menu entry to be danger destroy action, got %+v", entries[len(entries)-1])
	}

	winEntries := cmp.ContextMenuEntries(context.Background(), plugin.HostFacts{NodeID: "win-1", OSFamily: "windows"})
	if len(winEntries) != 0 {
		t.Errorf("expected no context menu entries for incompatible windows host, got %+v", winEntries)
	}

	// 5. TaskCompileProvider
	tcp, ok := p.(newapi.TaskCompileProvider)
	if !ok {
		t.Fatal("vm/libvirt does not implement newapi.TaskCompileProvider")
	}
	spec, err := tcp.CompileTask(context.Background(), linuxFacts, newapi.TaskRequest{
		ID:       "task-1",
		Type:     newapi.TaskTypePluginAction,
		NodeID:   "node-1",
		PluginID: "vm/libvirt",
		Action:   "create",
		Params: map[string]any{
			"name":   "fedora-vm",
			"vcpus":  4,
			"ramGiB": 8,
			"diskGb": 40,
			"bridge": "br0",
		},
	})
	if err != nil {
		t.Fatalf("CompileTask failed: %v", err)
	}
	if len(spec.Steps) != 1 || spec.Steps[0].Module != "ansible.builtin.command" {
		t.Errorf("unexpected compiled task steps: %+v", spec.Steps)
	}
	cmdStr, _ := spec.Steps[0].Params["cmd"].(string)
	if !strings.Contains(cmdStr, `"fedora-vm"`) || !strings.Contains(cmdStr, "--vcpus 4") || !strings.Contains(cmdStr, "bridge=br0") {
		t.Errorf("unexpected compiled create command: %s", cmdStr)
	}
}
