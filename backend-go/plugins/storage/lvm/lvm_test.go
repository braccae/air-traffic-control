package lvm_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"air-traffic-control/backend/pkg/plugin"
	"air-traffic-control/backend/pkg/plugin/newapi"
	"air-traffic-control/backend/pkg/plugin/settingsframework"
	_ "air-traffic-control/backend/plugins/all"
	lvm "air-traffic-control/backend/plugins/storage/lvm"
)

// mockRunner returns canned output per command so each sub-command of the
// batched Read invocation can be exercised independently. Matching is ordered
// (the combined Read command also contains the vgs substring).
type mockRunner struct {
	detectTools string
	vgReport    string
	readOutput  string
	err         error
}

func (m *mockRunner) RunCommand(ctx context.Context, cmd string) (string, error) {
	if m.err != nil {
		return "", m.err
	}
	switch {
	case strings.Contains(cmd, "command -v lvs"):
		return m.detectTools, nil
	case strings.Contains(cmd, "===LVS==="):
		return m.readOutput, nil
	case strings.Contains(cmd, "vgs --reportformat json"):
		return m.vgReport, nil
	}
	return "", nil
}

const vgsJSON = `{
  "report": [
    {
      "vg": [
        {"vg_name":"vg_data","pv_count":"2","lv_count":"1","vg_size":"100.00g","vg_free":"40.00g"}
      ]
    }
  ]
}`

const lvsJSON = `{
  "report": [
    {
      "lv": [
        {"lv_name":"lv_app","vg_name":"vg_data","lv_size":"60.00g","lv_attr":"-wi-ao----"}
      ]
    }
  ]
}`

const pvsJSON = `{
  "report": [
    {
      "pv": [
        {"pv_name":"/dev/sdb1","vg_name":"vg_data","pv_size":"50.00g","pv_free":"20.00g"},
        {"pv_name":"/dev/sda2","vg_name":"vg_data","pv_size":"50.00g","pv_free":"20.00g"}
      ]
    }
  ]
}`

const emptyVGJSON = `{"report":[{"vg":[]}]}`

func TestLVMPlugin_ProbeDetectsVGs(t *testing.T) {
	p, ok := plugin.Get("storage/lvm")
	if !ok {
		t.Fatalf("storage/lvm not registered")
	}

	det, err := p.Probe(context.Background(), &mockRunner{
		detectTools: "lvs\nvgs",
		vgReport:    vgsJSON,
	}, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatal(err)
	}
	if !det.Detected {
		t.Fatalf("expected LVM detected, got %+v", det)
	}
	if !strings.Contains(det.Status, "1 VG") {
		t.Errorf("unexpected status: %s", det.Status)
	}
	if !strings.Contains(det.Details, "vg_data") {
		t.Errorf("expected real VG name in details, got %s", det.Details)
	}
}

// A host with LVM tooling but no volume groups must report "not present"
// cleanly, not a false-positive ONLINE badge.
func TestLVMPlugin_ProbeNoVGsReportsNotPresent(t *testing.T) {
	p, _ := plugin.Get("storage/lvm")

	det, err := p.Probe(context.Background(), &mockRunner{
		detectTools: "lvs\nvgs",
		vgReport:    emptyVGJSON,
	}, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatal(err)
	}
	if det.Detected {
		t.Errorf("expected detected=false with no VGs, got %+v", det)
	}
	if det.Status != "NO_VGS" {
		t.Errorf("expected NO_VGS status, got %s", det.Status)
	}
}

// When LVM cannot read metadata (e.g. insufficient privileges) the probe must
// fail loudly instead of pretending the host has no LVM.
func TestLVMPlugin_ProbeUnreadableFailsLoudly(t *testing.T) {
	p, _ := plugin.Get("storage/lvm")

	unreadable := `{
	  "report": [{"vg": []}],
	  "log": [{"log_type":"error","log_message":"/run/lock/lvm/P_global:aux: open failed: Permission denied"}]
	}`

	det, err := p.Probe(context.Background(), &mockRunner{
		detectTools: "lvs\nvgs",
		vgReport:    unreadable,
	}, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatal(err)
	}
	if det.Detected {
		t.Errorf("expected detected=false when metadata unreadable, got %+v", det)
	}
	if det.Status != "UNREADABLE" {
		t.Errorf("expected UNREADABLE status, got %s", det.Status)
	}
	if !strings.Contains(det.Error, "Permission denied") {
		t.Errorf("expected real error surfaced, got %q", det.Error)
	}
}

// Read must return real PV/VG/LV rows whose values match the host's own
// pvs/vgs/lvs output.
func TestLVMPlugin_ReadReturnsRealVolumes(t *testing.T) {
	p, _ := plugin.Get("storage/lvm")

	readOutput := vgsJSON + "===LVS===\n" + lvsJSON + "===PVS===\n" + pvsJSON

	data, err := p.(plugin.Reader).Read(context.Background(), &mockRunner{
		readOutput: readOutput,
	}, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}

	m, ok := data.(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %T", data)
	}
	if present, _ := m["present"].(bool); !present {
		t.Errorf("expected present=true, got %+v", m["present"])
	}

	pvs, ok := m["physicalVolumes"].([]lvm.PhysicalVolume)
	if !ok {
		t.Fatalf("physicalVolumes not parsed: %T", m["physicalVolumes"])
	}
	if len(pvs) != 2 {
		t.Fatalf("expected 2 physical volumes, got %d (%+v)", len(pvs), pvs)
	}
	// Sorted by name: /dev/sda2 then /dev/sdb1.
	if pvs[0].Name != "/dev/sda2" || pvs[0].VG != "vg_data" || pvs[0].Size != "50.00g" {
		t.Errorf("unexpected pv[0]: %+v", pvs[0])
	}

	vgs, ok := m["volumeGroups"].([]lvm.VolumeGroup)
	if !ok {
		t.Fatalf("volumeGroups not parsed: %T", m["volumeGroups"])
	}
	if len(vgs) != 1 || vgs[0].Name != "vg_data" || vgs[0].PVCount != "2" || vgs[0].LVCount != "1" {
		t.Errorf("unexpected volume groups: %+v", vgs)
	}

	lvs, ok := m["logicalVolumes"].([]lvm.LogicalVolume)
	if !ok {
		t.Fatalf("logicalVolumes not parsed: %T", m["logicalVolumes"])
	}
	if len(lvs) != 1 || lvs[0].Name != "lv_app" || lvs[0].VG != "vg_data" || lvs[0].Size != "60.00g" {
		t.Errorf("unexpected logical volumes: %+v", lvs)
	}

	// Raw blobs preserved for fidelity/back-compat.
	if !strings.Contains(m["vgs"].(string), "vg_data") {
		t.Errorf("expected raw vgs blob, got %q", m["vgs"])
	}
}

// A host with no LVM must return present=false with a clean message instead of
// empty/incorrect blobs.
func TestLVMPlugin_ReadNoLVMReportsNotPresent(t *testing.T) {
	p, _ := plugin.Get("storage/lvm")

	readOutput := emptyVGJSON + "===LVS===\n" + `{"report":[{"lv":[]}]}` + "===PVS===\n" + `{"report":[{"pv":[]}]}`

	data, err := p.(plugin.Reader).Read(context.Background(), &mockRunner{
		readOutput: readOutput,
	}, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	m := data.(map[string]any)
	if present, _ := m["present"].(bool); present {
		t.Errorf("expected present=false, got %+v", m)
	}
	if msg, _ := m["message"].(string); msg == "" {
		t.Errorf("expected clean not-present message, got %+v", m)
	}
	if _, hasErr := m["errors"]; hasErr {
		t.Errorf("did not expect errors for a clean no-LVM host: %+v", m["errors"])
	}
}

func TestLVMPlugin_Compile(t *testing.T) {
	p, _ := plugin.Get("storage/lvm")

	task, err := p.(plugin.Actor).Compile(plugin.HostFacts{NodeID: "node1"}, "create_lv", map[string]any{
		"vg":   "vg_data",
		"lv":   "lv_app",
		"size": "20G",
	})
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}
	if len(task.Steps) != 1 || task.Steps[0].Module != "community.general.lvol" {
		t.Errorf("unexpected steps: %+v", task.Steps)
	}
}

func TestLVMPlugin_NewAPIInterfacesAndSchemas(t *testing.T) {
	p, ok := plugin.Get("storage/lvm")
	if !ok {
		t.Fatalf("storage/lvm not registered in DefaultRegistry")
	}

	// 1. DataCollectionPlugin
	dc, ok := p.(newapi.DataCollectionPlugin)
	if !ok {
		t.Fatalf("storage/lvm does not implement newapi.DataCollectionPlugin")
	}
	sched := dc.CollectionSchedule()
	if sched.Interval != 60*time.Second || sched.TTL != 60*time.Second {
		t.Errorf("unexpected collection schedule for storage/lvm: %+v", sched)
	}
	schema := dc.CollectionSchema()
	if schema.Version != "v2" || schema.RootKey != "volumeGroups" {
		t.Errorf("unexpected collection schema: %+v", schema)
	}

	// 2. ManagementPlugin & action parity
	mgmt, ok := p.(newapi.ManagementPlugin)
	if !ok {
		t.Fatalf("storage/lvm does not implement newapi.ManagementPlugin")
	}
	mgmtActions := mgmt.ManagementActions()
	legacyActions := p.(plugin.Actor).Actions()
	if len(mgmtActions) != 3 || len(legacyActions) != len(mgmtActions) {
		t.Fatalf("expected 3 management actions matching legacy Actions(), got mgmt=%d legacy=%d", len(mgmtActions), len(legacyActions))
	}
	for i, ma := range mgmtActions {
		if ma.Name != legacyActions[i].Name {
			t.Errorf("action[%d] name mismatch: %q vs %q", i, ma.Name, legacyActions[i].Name)
		}
		if ma.Name == "remove_lv" && (!ma.Dangerous || ma.ConfirmPrompt == "") {
			t.Errorf("expected remove_lv to be marked dangerous with a confirmation prompt: %+v", ma)
		}
	}

	facts := plugin.HostFacts{NodeID: "node1", OSFamily: "linux"}
	if err := mgmt.ValidateAction(facts, "create_lv", map[string]any{
		"vg":   "vg_data",
		"lv":   "lv_app",
		"size": "10G",
	}); err != nil {
		t.Errorf("expected valid create_lv options to pass ValidateAction, got %v", err)
	}
	if err := mgmt.ValidateAction(facts, "create_lv", map[string]any{"vg": "vg_data"}); err == nil {
		t.Errorf("expected missing required options to fail ValidateAction")
	}
	if err := mgmt.ValidateAction(facts, "unknown_action", nil); err == nil {
		t.Errorf("expected unknown action to fail ValidateAction")
	}

	// 3. CategorizedSettingsProvider & settingsframework validation
	csp, ok := p.(newapi.CategorizedSettingsProvider)
	if !ok {
		t.Fatalf("storage/lvm does not implement newapi.CategorizedSettingsProvider")
	}
	catPage := csp.CategorizedSettingsPage()
	if err := settingsframework.ValidateSchema(catPage.SettingsPage); err != nil {
		t.Fatalf("ValidateSchema failed for storage/lvm settings page: %v", err)
	}
	if err := newapi.ValidateCategorizedSettingsPage(catPage); err != nil {
		t.Fatalf("ValidateCategorizedSettingsPage failed for storage/lvm: %v", err)
	}
	if catPage.ID != "storage/lvm" || catPage.Scope != newapi.SettingsScopeDatacenter || catPage.Category != "storage" {
		t.Errorf("unexpected categorized settings page metadata: %+v", catPage)
	}
	if _, ok := plugin.DefaultRegistry().GetSettingsPage("storage/lvm"); !ok {
		t.Errorf("expected storage/lvm settings page to be indexed in DefaultRegistry")
	}

	// 4. ContextMenuProvider
	cmp, ok := p.(newapi.ContextMenuProvider)
	if !ok {
		t.Fatalf("storage/lvm does not implement newapi.ContextMenuProvider")
	}
	entries := cmp.ContextMenuEntries(context.Background(), facts)
	if len(entries) != 3 {
		t.Fatalf("expected 3 context menu entries for compatible linux host, got %d (%+v)", len(entries), entries)
	}
	for _, entry := range entries {
		if entry.PluginID != "storage/lvm" || entry.Target != newapi.TargetStorage {
			t.Errorf("unexpected context menu entry metadata: %+v", entry)
		}
	}
	if winEntries := cmp.ContextMenuEntries(context.Background(), plugin.HostFacts{OSFamily: "windows"}); len(winEntries) != 0 {
		t.Errorf("expected nil/empty context menu entries for incompatible windows host, got %+v", winEntries)
	}

	// 5. TaskTypeProvider & TaskCompileProvider
	ttp, ok := p.(newapi.TaskTypeProvider)
	if !ok {
		t.Fatalf("storage/lvm does not implement newapi.TaskTypeProvider")
	}
	taskSpecs := ttp.TaskTypes()
	if len(taskSpecs) != 1 || taskSpecs[0].Type != newapi.TaskTypeStorageOp || taskSpecs[0].PluginID != "storage/lvm" {
		t.Errorf("unexpected task types for storage/lvm: %+v", taskSpecs)
	}

	tcp, ok := p.(newapi.TaskCompileProvider)
	if !ok {
		t.Fatalf("storage/lvm does not implement newapi.TaskCompileProvider")
	}
	compiled, err := tcp.CompileTask(context.Background(), facts, newapi.TaskRequest{
		Type:     newapi.TaskTypeStorageOp,
		PluginID: "storage/lvm",
		Action:   "extend_lv",
		NodeID:   "node1",
		Params: map[string]any{
			"vg":   "vg_data",
			"lv":   "lv_app",
			"size": "+10G",
		},
	})
	if err != nil {
		t.Fatalf("CompileTask failed: %v", err)
	}
	if len(compiled.Steps) != 1 || compiled.Steps[0].Module != "community.general.lvol" {
		t.Errorf("unexpected CompileTask steps: %+v", compiled.Steps)
	}
}
