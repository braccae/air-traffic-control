package lxc_test

import (
	"context"
	"strings"
	"testing"

	"air-traffic-control/backend/pkg/plugin"
	"air-traffic-control/backend/pkg/plugin/newapi"
	"air-traffic-control/backend/pkg/plugin/settingsframework"
	_ "air-traffic-control/backend/plugins/all"
	"air-traffic-control/backend/plugins/vm/lxc"
)

type mockRunner struct {
	responses map[string]string
}

// RunCommand returns the response for the longest matching pattern so a more
// specific key wins deterministically over an incidental substring match.
func (m *mockRunner) RunCommand(_ context.Context, cmd string) (string, error) {
	best, bestLen := "", -1
	for pattern, resp := range m.responses {
		if strings.Contains(cmd, pattern) && len(pattern) > bestLen {
			best, bestLen = resp, len(pattern)
		}
	}
	if bestLen < 0 {
		return "", nil
	}
	return best, nil
}

func TestLXCPlugin_Registered(t *testing.T) {
	p, ok := plugin.Get("vm/lxc")
	if !ok {
		t.Fatal("vm/lxc plugin not registered")
	}
	if p.Meta().Kind != plugin.KindVM {
		t.Errorf("unexpected kind: %v", p.Meta().Kind)
	}
	if _, ok := p.(plugin.Reader); !ok {
		t.Error("vm/lxc must implement plugin.Reader")
	}
	if _, ok := p.(plugin.Actor); !ok {
		t.Error("vm/lxc must implement plugin.Actor")
	}
	if _, ok := p.(plugin.SettingsPageProvider); !ok {
		t.Error("vm/lxc must implement plugin.SettingsPageProvider")
	}
}

// Probe must report honestly when virsh is not installed — never a fabricated
// "detected".
func TestLXCPlugin_ProbeNoVirsh(t *testing.T) {
	p, _ := plugin.Get("vm/lxc")
	r := &mockRunner{responses: map[string]string{
		"command -v virsh": "NO_VIRSH",
	}}
	det, err := p.Probe(context.Background(), r, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatalf("probe errored: %v", err)
	}
	if det.Detected {
		t.Fatalf("must not detect lxc without virsh: %+v", det)
	}
	if det.Status != "UNAVAILABLE" {
		t.Errorf("expected UNAVAILABLE, got %q", det.Status)
	}
}

// Probe must report honestly when virsh exists but the LXC driver is not
// reachable (e.g. libvirtd running for QEMU/KVM only).
func TestLXCPlugin_ProbeDriverUnavailable(t *testing.T) {
	p, _ := plugin.Get("vm/lxc")
	r := &mockRunner{responses: map[string]string{
		"command -v virsh": "/usr/bin/virsh",
		"lxc:/// version":  "error: failed to connect to the hypervisor",
	}}
	det, err := p.Probe(context.Background(), r, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatalf("probe errored: %v", err)
	}
	if det.Detected {
		t.Fatalf("must not detect lxc when driver unreachable: %+v", det)
	}
	if !strings.Contains(det.Error, "no libvirt LXC host") {
		t.Errorf("expected honest driver error, got %q", det.Error)
	}
}

// Read must NOT fabricate containers when the host is unavailable.
func TestLXCPlugin_ReadNoLibvirtFabricatesNothing(t *testing.T) {
	p, _ := plugin.Get("vm/lxc")
	reader := p.(plugin.Reader)

	r := &mockRunner{responses: map[string]string{
		"command -v virsh":   "/usr/bin/virsh",
		"lxc:/// list --all": "error: failed to connect to the hypervisor\nLXC_LIST_FAILED",
	}}
	data, err := reader.Read(context.Background(), r, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatalf("read should return a structured honest result, not an error: %v", err)
	}
	res, ok := data.(lxc.ReadResult)
	if !ok {
		t.Fatalf("unexpected payload type %T", data)
	}
	if res.HasLibvirt {
		t.Error("hasLibvirt must be false when the host is unreachable")
	}
	if len(res.Containers) != 0 {
		t.Errorf("no containers may be fabricated, got %d", len(res.Containers))
	}
	if res.LibvirtError == "" {
		t.Error("an honest error string must be present")
	}
	if res.Verified {
		t.Error("plugin must not claim verification without a real host")
	}
	if len(res.Unimplemented) == 0 {
		t.Error("unimplemented operations must still be surfaced when unavailable")
	}
}

// Read must parse real virsh output into containers with real specs.
func TestLXCPlugin_ReadParsesContainers(t *testing.T) {
	p, _ := plugin.Get("vm/lxc")
	reader := p.(plugin.Reader)

	list := " Id   Name       State\n" +
		"----------------------------\n" +
		" 1234 web-01     running\n" +
		" -    db-01      shut off\n"

	r := &mockRunner{responses: map[string]string{
		"command -v virsh":   "/usr/bin/virsh",
		"lxc:/// list --all": list,
		"dumpxml 'web-01'":   "<domain type='lxc'><name>web-01</name><vcpu>4</vcpu><memory unit='KiB'>2097152</memory></domain>",
		"dumpxml 'db-01'":    "<domain type='lxc'><name>db-01</name><vcpu>2</vcpu><memory unit='KiB'>1048576</memory></domain>",
	}}
	data, err := reader.Read(context.Background(), r, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	res, ok := data.(lxc.ReadResult)
	if !ok {
		t.Fatalf("unexpected payload type %T", data)
	}
	if !res.HasLibvirt {
		t.Fatal("expected hasLibvirt true")
	}
	if len(res.Containers) != 2 {
		t.Fatalf("expected 2 containers, got %d", len(res.Containers))
	}
	if res.Containers[0].Name != "web-01" || res.Containers[0].Status != "RUNNING" {
		t.Errorf("unexpected first container: %+v", res.Containers[0])
	}
	if res.Containers[1].Status != "STOPPED" {
		t.Errorf("unexpected second container status: %+v", res.Containers[1])
	}
	if res.Containers[0].VCPUs != 4 || res.Containers[0].RAMMiB != 2048 {
		t.Errorf("specs not parsed from dumpxml: %+v", res.Containers[0])
	}
	if len(res.Unimplemented) == 0 {
		t.Error("unimplemented operations must be reported to the UI")
	}
	if res.Verified {
		t.Error("plugin must not claim verification without a real host")
	}
}

func TestLXCPlugin_Compile(t *testing.T) {
	p, _ := plugin.Get("vm/lxc")
	actor := p.(plugin.Actor)

	cases := map[string]string{
		"start":    "running",
		"shutdown": "shutdown",
		"destroy":  "destroyed",
		"suspend":  "paused",
	}
	for action, wantState := range cases {
		task, err := actor.Compile(plugin.HostFacts{NodeID: "n1"}, action, map[string]any{"name": "web-01"})
		if err != nil {
			t.Fatalf("compile %s failed: %v", action, err)
		}
		if len(task.Steps) != 1 || task.Steps[0].Module != "community.general.virt" {
			t.Errorf("%s: unexpected steps %+v", action, task.Steps)
		}
		if task.Steps[0].Params["state"] != wantState {
			t.Errorf("%s: expected state %s, got %v", action, wantState, task.Steps[0].Params["state"])
		}
		if task.Steps[0].Params["uri"] != "lxc:///" {
			t.Errorf("%s: expected lxc:/// uri, got %v", action, task.Steps[0].Params["uri"])
		}
	}

	if _, err := actor.Compile(plugin.HostFacts{}, "start", map[string]any{}); err == nil {
		t.Error("expected error when name is missing")
	}
	if _, err := actor.Compile(plugin.HostFacts{}, "clone", map[string]any{"name": "x"}); err == nil {
		t.Error("expected error for unsupported action")
	}
}

func TestLXCPlugin_V2NewAPIInterfaces(t *testing.T) {
	p, ok := plugin.Get("vm/lxc")
	if !ok {
		t.Fatalf("vm/lxc not registered")
	}

	// 1. DataCollectionPlugin
	dc, ok := p.(newapi.DataCollectionPlugin)
	if !ok {
		t.Fatalf("expected vm/lxc to implement newapi.DataCollectionPlugin")
	}
	sched := dc.CollectionSchedule()
	if sched.Interval <= 0 || sched.TTL <= 0 {
		t.Fatalf("expected positive schedule Interval and TTL, got %+v", sched)
	}
	schema := dc.CollectionSchema()
	if schema.Version != "v2" || schema.RootKey != "containers" {
		t.Fatalf("unexpected collection schema: %+v", schema)
	}

	// 2. ManagementPlugin
	mgmt, ok := p.(newapi.ManagementPlugin)
	if !ok {
		t.Fatalf("expected vm/lxc to implement newapi.ManagementPlugin")
	}
	mgmtActions := mgmt.ManagementActions()
	if len(mgmtActions) != 4 {
		t.Fatalf("expected 4 management actions, got %d", len(mgmtActions))
	}
	legacyActions := mgmt.Actions()
	if len(legacyActions) != len(mgmtActions) {
		t.Fatalf("Actions() length %d does not match ManagementActions() length %d", len(legacyActions), len(mgmtActions))
	}

	facts := plugin.HostFacts{NodeID: "node-1", OSFamily: "linux"}
	if err := mgmt.ValidateAction(facts, "start", map[string]any{"name": "web-01"}); err != nil {
		t.Fatalf("expected valid start options, got error: %v", err)
	}
	if err := mgmt.ValidateAction(facts, "destroy", map[string]any{"name": "web-01"}); err != nil {
		t.Fatalf("expected valid destroy options, got error: %v", err)
	}
	if err := mgmt.ValidateAction(facts, "start", map[string]any{}); err == nil {
		t.Fatalf("expected error when required name option is missing")
	}
	if err := mgmt.ValidateAction(facts, "clone", map[string]any{"name": "web-01"}); err == nil {
		t.Fatalf("expected error for unsupported action")
	}

	// 3. CategorizedSettingsProvider
	csp, ok := p.(newapi.CategorizedSettingsProvider)
	if !ok {
		t.Fatalf("expected vm/lxc to implement newapi.CategorizedSettingsProvider")
	}
	catPage := csp.CategorizedSettingsPage()
	if err := settingsframework.ValidateSchema(csp.SettingsPage()); err != nil {
		t.Fatalf("settings schema validation failed: %v", err)
	}
	if err := newapi.ValidateCategorizedSettingsPage(catPage); err != nil {
		t.Fatalf("categorized settings page validation failed: %v", err)
	}
	if catPage.ID != "vm/lxc" || catPage.Category != "vm" || catPage.Scope != newapi.SettingsScopeDatacenter {
		t.Fatalf("unexpected categorized settings page metadata: %+v", catPage)
	}
	if _, ok := plugin.GetCategorizedSettingsPage("vm/lxc"); !ok {
		t.Fatalf("expected vm/lxc categorized settings page to be registered in default registry")
	}

	// 4. ContextMenuProvider
	cmp, ok := p.(newapi.ContextMenuProvider)
	if !ok {
		t.Fatalf("expected vm/lxc to implement newapi.ContextMenuProvider")
	}
	entries := cmp.ContextMenuEntries(context.Background(), facts)
	if len(entries) != 4 {
		t.Fatalf("expected 4 context menu entries for compatible linux host, got %d", len(entries))
	}
	for _, entry := range entries {
		if entry.PluginID != "vm/lxc" || entry.Target != newapi.TargetVM || entry.Action == "" {
			t.Errorf("unexpected context menu entry: %+v", entry)
		}
	}
	if !entries[len(entries)-1].Danger || entries[len(entries)-1].ConfirmPrompt == "" {
		t.Errorf("expected destroy entry to be marked Danger with a ConfirmPrompt, got %+v", entries[len(entries)-1])
	}
	if incompat := cmp.ContextMenuEntries(context.Background(), plugin.HostFacts{OSFamily: "windows"}); len(incompat) != 0 {
		t.Fatalf("expected no context menu entries for incompatible windows host, got %+v", incompat)
	}

	// 5. TaskCompileProvider
	tcp, ok := p.(newapi.TaskCompileProvider)
	if !ok {
		t.Fatalf("expected vm/lxc to implement newapi.TaskCompileProvider")
	}
	compiledTask, err := tcp.CompileTask(context.Background(), facts, newapi.TaskRequest{
		Type:     newapi.TaskTypePluginAction,
		PluginID: "vm/lxc",
		Action:   "shutdown",
		NodeID:   "node-1",
		Params: map[string]any{
			"name": "web-01",
		},
	})
	if err != nil {
		t.Fatalf("CompileTask failed: %v", err)
	}
	if len(compiledTask.Steps) != 1 || compiledTask.Steps[0].Module != "community.general.virt" {
		t.Fatalf("unexpected CompileTask output: %+v", compiledTask)
	}
	if compiledTask.Steps[0].Params["state"] != "shutdown" || compiledTask.Steps[0].Params["uri"] != "lxc:///" {
		t.Errorf("unexpected CompileTask params: %+v", compiledTask.Steps[0].Params)
	}

	// Fallback action from Params["action"]
	compiledSuspend, err := tcp.CompileTask(context.Background(), facts, newapi.TaskRequest{
		Type:     newapi.TaskTypePluginAction,
		PluginID: "vm/lxc",
		NodeID:   "node-1",
		Params: map[string]any{
			"action": "suspend",
			"name":   "db-01",
		},
	})
	if err != nil {
		t.Fatalf("CompileTask with params[action] failed: %v", err)
	}
	if len(compiledSuspend.Steps) != 1 || compiledSuspend.Steps[0].Params["state"] != "paused" {
		t.Fatalf("unexpected CompileTask suspend output: %+v", compiledSuspend)
	}
}
