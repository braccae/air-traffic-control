package lvm_test

import (
	"context"
	"strings"
	"testing"

	"air-traffic-control/backend/pkg/plugin"
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
