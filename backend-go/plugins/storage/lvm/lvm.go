package lvm

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"air-traffic-control/backend/pkg/plugin"
)

func init() {
	_ = plugin.Register(New())
}

// Plugin implements LVM (Logical Volume Manager) inspection and provisioning.
type Plugin struct{}

func New() *Plugin {
	return &Plugin{}
}

func (p *Plugin) Meta() plugin.Meta {
	return plugin.Meta{
		ID:          "storage/lvm",
		Kind:        plugin.KindStorage,
		Name:        "lvm",
		Label:       "LVM Storage",
		Icon:        "storage",
		Description: "Logical Volume Manager volume groups (VGs), logical volumes (LVs), and thin pools",
		Order:       30,
	}
}

func (p *Plugin) Compatible(h plugin.HostFacts) bool {
	return h.OSFamily != "windows" && h.OSFamily != "router_switch"
}

func (p *Plugin) Requirements() plugin.Requirements {
	return plugin.Requirements{
		HostPackages: map[string][]string{
			"dnf": {"lvm2"},
			"apt": {"lvm2"},
			"apk": {"lvm2"},
		},
		ControllerCollections: []string{
			"community.general",
		},
	}
}

func (p *Plugin) Probe(ctx context.Context, r plugin.Runner, h plugin.HostFacts) (plugin.Detection, error) {
	if r == nil {
		return plugin.Detection{Detected: false, Status: "OFFLINE", Error: "runner is nil"}, nil
	}

	out, err := r.RunCommand(ctx, "command -v lvs && command -v vgs")
	if err != nil || strings.TrimSpace(out) == "" {
		return plugin.Detection{
			Detected: false,
			Status:   "UNAVAILABLE",
			Error:    "lvs or vgs command not found",
		}, nil
	}

	// Probe reads the same JSON report the Read path consumes so the detection
	// badge is derived from real metadata rather than from raw text that LVM
	// contaminates with stderr warnings.
	raw, err := r.RunCommand(ctx, "sudo -n vgs --reportformat json 2>/dev/null || vgs --reportformat json 2>/dev/null || true")
	if err != nil {
		return plugin.Detection{
			Detected: false,
			Status:   "ERROR",
			Error:    err.Error(),
		}, nil
	}

	report, msgs := parseLVMReport(raw)
	vgs := volumeGroupsFromReport(report)
	if len(vgs) > 0 {
		names := make([]string, 0, len(vgs))
		for _, vg := range vgs {
			names = append(names, vg.Name)
		}
		return plugin.Detection{
			Detected: true,
			Status:   fmt.Sprintf("%d VG(s)", len(vgs)),
			Details:  fmt.Sprintf("Volume Groups: %s", strings.Join(names, ", ")),
		}, nil
	}

	// No VGs in the report. If LVM itself logged an error (e.g. the probe could
	// not read metadata because of insufficient privileges) we must not claim
	// "not present" — surface the failure instead of fabricating an empty result.
	if len(msgs) > 0 {
		return plugin.Detection{
			Detected: false,
			Status:   "UNREADABLE",
			Error:    strings.Join(msgs, "; "),
			Details:  "LVM tools present but volume metadata could not be read",
		}, nil
	}

	return plugin.Detection{
		Detected: false,
		Status:   "NO_VGS",
		Details:  "LVM tools installed, no active volume groups found",
	}, nil
}

// PhysicalVolume is one row of real `pvs` output.
type PhysicalVolume struct {
	Name string `json:"name"`
	VG   string `json:"vg,omitempty"`
	Size string `json:"size,omitempty"`
	Free string `json:"free,omitempty"`
}

// VolumeGroup is one row of real `vgs` output.
type VolumeGroup struct {
	Name    string `json:"name"`
	PVCount string `json:"pvCount,omitempty"`
	LVCount string `json:"lvCount,omitempty"`
	Size    string `json:"size,omitempty"`
	Free    string `json:"free,omitempty"`
}

// LogicalVolume is one row of real `lvs` output.
type LogicalVolume struct {
	Name string `json:"name"`
	VG   string `json:"vg,omitempty"`
	Size string `json:"size,omitempty"`
	Attr string `json:"attr,omitempty"`
}

// lvmReport is the JSON envelope emitted by vgs/lvs/pvs --reportformat json.
type lvmReport struct {
	Report []struct {
		VG []map[string]any `json:"vg"`
		LV []map[string]any `json:"lv"`
		PV []map[string]any `json:"pv"`
	} `json:"report"`
	Log []struct {
		LogType    string `json:"log_type"`
		LogMessage string `json:"log_message"`
	} `json:"log"`
}

// jsonField reads a report field as a trimmed string, tolerating numbers and
// booleans that LVM may emit unquoted.
func jsonField(m map[string]any, key string) string {
	v, ok := m[key]
	if !ok || v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return strings.TrimSpace(s)
	}
	return strings.TrimSpace(fmt.Sprintf("%v", v))
}

// parseLVMReport decodes one --reportformat json blob and returns the parsed
// report together with any error-severity log messages. An empty blob, a
// "NO_*" sentinel, or a malformed blob yields an empty report — callers treat
// that as "no data", and distinguish a genuine absence from a read failure via
// the returned log messages.
func parseLVMReport(raw string) (lvmReport, []string) {
	var rep lvmReport
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || strings.HasPrefix(trimmed, "NO_") {
		return rep, nil
	}

	// LVM occasionally prefixes the JSON with a warning line; retry from the
	// first brace before giving up.
	if err := json.Unmarshal([]byte(trimmed), &rep); err != nil {
		idx := strings.Index(trimmed, "{")
		if idx < 0 {
			return lvmReport{}, []string{fmt.Sprintf("unparseable LVM report: %v", err)}
		}
		rep = lvmReport{}
		if err2 := json.Unmarshal([]byte(trimmed[idx:]), &rep); err2 != nil {
			return lvmReport{}, []string{fmt.Sprintf("unparseable LVM report: %v", err)}
		}
	}

	var msgs []string
	for _, l := range rep.Log {
		if strings.EqualFold(l.LogType, "error") && strings.TrimSpace(l.LogMessage) != "" {
			msgs = append(msgs, strings.TrimSpace(l.LogMessage))
		}
	}
	return rep, msgs
}

func physicalVolumesFromReport(rep lvmReport) []PhysicalVolume {
	out := make([]PhysicalVolume, 0)
	for _, section := range rep.Report {
		for _, pv := range section.PV {
			name := jsonField(pv, "pv_name")
			if name == "" {
				continue
			}
			out = append(out, PhysicalVolume{
				Name: name,
				VG:   jsonField(pv, "vg_name"),
				Size: jsonField(pv, "pv_size"),
				Free: jsonField(pv, "pv_free"),
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func volumeGroupsFromReport(rep lvmReport) []VolumeGroup {
	out := make([]VolumeGroup, 0)
	for _, section := range rep.Report {
		for _, vg := range section.VG {
			name := jsonField(vg, "vg_name")
			if name == "" {
				continue
			}
			out = append(out, VolumeGroup{
				Name:    name,
				PVCount: jsonField(vg, "pv_count"),
				LVCount: jsonField(vg, "lv_count"),
				Size:    jsonField(vg, "vg_size"),
				Free:    jsonField(vg, "vg_free"),
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func logicalVolumesFromReport(rep lvmReport) []LogicalVolume {
	out := make([]LogicalVolume, 0)
	for _, section := range rep.Report {
		for _, lv := range section.LV {
			name := jsonField(lv, "lv_name")
			if name == "" {
				continue
			}
			out = append(out, LogicalVolume{
				Name: name,
				VG:   jsonField(lv, "vg_name"),
				Size: jsonField(lv, "lv_size"),
				Attr: jsonField(lv, "lv_attr"),
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// splitLVMRead pulls the three --reportformat json blobs out of the single
// batched Read command, using the sentinel separators.
func splitLVMRead(out string) (vgsRaw, lvsRaw, pvsRaw string) {
	parts := strings.Split(out, "===LVS===")
	vgsRaw = strings.TrimSpace(parts[0])
	if len(parts) > 1 {
		rest := strings.Split(parts[1], "===PVS===")
		lvsRaw = strings.TrimSpace(rest[0])
		if len(rest) > 1 {
			pvsRaw = strings.TrimSpace(rest[1])
		}
	}
	return vgsRaw, lvsRaw, pvsRaw
}

func (p *Plugin) Read(ctx context.Context, r plugin.Runner, h plugin.HostFacts) (any, error) {
	if r == nil {
		return nil, fmt.Errorf("runner is nil")
	}

	cmd := "sudo -n vgs --reportformat json 2>/dev/null || vgs --reportformat json 2>/dev/null || true; echo '===LVS==='; sudo -n lvs --reportformat json 2>/dev/null || lvs --reportformat json 2>/dev/null || true; echo '===PVS==='; sudo -n pvs --reportformat json 2>/dev/null || pvs --reportformat json 2>/dev/null || true"
	out, err := r.RunCommand(ctx, cmd)
	if err != nil {
		return nil, err
	}

	vgsRaw, lvsRaw, pvsRaw := splitLVMRead(out)

	vgRep, vgErrs := parseLVMReport(vgsRaw)
	lvRep, lvErrs := parseLVMReport(lvsRaw)
	pvRep, pvErrs := parseLVMReport(pvsRaw)

	volumes := physicalVolumesFromReport(pvRep)
	groups := volumeGroupsFromReport(vgRep)
	logicals := logicalVolumesFromReport(lvRep)

	errs := make([]string, 0, len(vgErrs)+len(lvErrs)+len(pvErrs))
	errs = append(errs, vgErrs...)
	errs = append(errs, lvErrs...)
	errs = append(errs, pvErrs...)

	present := len(volumes)+len(groups)+len(logicals) > 0

	result := map[string]any{
		"present":         present,
		"physicalVolumes": volumes,
		"volumeGroups":    groups,
		"logicalVolumes":  logicals,
		"pvs":             pvsRaw,
		"vgs":             vgsRaw,
		"lvs":             lvsRaw,
	}
	if len(errs) > 0 {
		result["errors"] = errs
	}
	if !present {
		result["message"] = "No LVM physical volumes, volume groups, or logical volumes detected"
	}
	return result, nil
}

func (p *Plugin) Actions() []plugin.ActionSpec {
	return []plugin.ActionSpec{
		{
			Name:        "create_lv",
			Label:       "Create Logical Volume",
			Description: "Create a new logical volume in an existing volume group",
			Options: []plugin.OptionSchema{
				{Name: "vg", Type: "string", Required: true, Description: "Volume group name"},
				{Name: "lv", Type: "string", Required: true, Description: "Logical volume name"},
				{Name: "size", Type: "string", Required: true, Description: "Volume size (e.g. 10G, 500M)"},
			},
		},
		{
			Name:        "extend_lv",
			Label:       "Extend Logical Volume",
			Description: "Extend an existing logical volume",
			Options: []plugin.OptionSchema{
				{Name: "vg", Type: "string", Required: true, Description: "Volume group name"},
				{Name: "lv", Type: "string", Required: true, Description: "Logical volume name"},
				{Name: "size", Type: "string", Required: true, Description: "New size or size increment (e.g. +5G)"},
			},
		},
		{
			Name:        "remove_lv",
			Label:       "Remove Logical Volume",
			Description: "Delete a logical volume",
			Options: []plugin.OptionSchema{
				{Name: "vg", Type: "string", Required: true, Description: "Volume group name"},
				{Name: "lv", Type: "string", Required: true, Description: "Logical volume name to remove"},
			},
		},
	}
}

func (p *Plugin) Compile(h plugin.HostFacts, action string, opts map[string]any) (plugin.TaskSpec, error) {
	norm := strings.ToLower(action)
	switch norm {
	case "create_lv":
		vg, _ := opts["vg"].(string)
		lv, _ := opts["lv"].(string)
		size, _ := opts["size"].(string)
		if vg == "" || lv == "" || size == "" {
			return plugin.TaskSpec{}, fmt.Errorf("vg, lv, and size are required")
		}
		return plugin.TaskSpec{
			Name: fmt.Sprintf("Create LVM volume %s/%s (%s)", vg, lv, size),
			Steps: []plugin.TaskStep{
				{
					Name:   fmt.Sprintf("Create logical volume %s on %s", lv, vg),
					Module: "community.general.lvol",
					Params: map[string]any{
						"vg":     vg,
						"lv":     lv,
						"size":   size,
						"state":  "present",
						"become": true,
					},
				},
			},
		}, nil

	case "extend_lv":
		vg, _ := opts["vg"].(string)
		lv, _ := opts["lv"].(string)
		size, _ := opts["size"].(string)
		if vg == "" || lv == "" || size == "" {
			return plugin.TaskSpec{}, fmt.Errorf("vg, lv, and size are required")
		}
		return plugin.TaskSpec{
			Name: fmt.Sprintf("Extend LVM volume %s/%s to %s", vg, lv, size),
			Steps: []plugin.TaskStep{
				{
					Name:   fmt.Sprintf("Resize logical volume %s on %s", lv, vg),
					Module: "community.general.lvol",
					Params: map[string]any{
						"vg":       vg,
						"lv":       lv,
						"size":     size,
						"resizefs": true,
						"become":   true,
					},
				},
			},
		}, nil

	case "remove_lv":
		vg, _ := opts["vg"].(string)
		lv, _ := opts["lv"].(string)
		if vg == "" || lv == "" {
			return plugin.TaskSpec{}, fmt.Errorf("vg and lv are required")
		}
		return plugin.TaskSpec{
			Name: fmt.Sprintf("Remove LVM volume %s/%s", vg, lv),
			Steps: []plugin.TaskStep{
				{
					Name:   fmt.Sprintf("Remove logical volume %s from %s", lv, vg),
					Module: "community.general.lvol",
					Params: map[string]any{
						"vg":     vg,
						"lv":     lv,
						"state":  "absent",
						"force":  true,
						"become": true,
					},
				},
			},
		}, nil

	default:
		return plugin.TaskSpec{}, fmt.Errorf("unsupported lvm action: %s", action)
	}
}
