package lvm

import (
	"context"
	"fmt"
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

	vgOut, err := r.RunCommand(ctx, "sudo -n vgs --noheadings -o vg_name 2>/dev/null || vgs --noheadings -o vg_name 2>/dev/null || true")
	if err == nil && strings.TrimSpace(vgOut) != "" {
		vgs := strings.Fields(strings.TrimSpace(vgOut))
		return plugin.Detection{
			Detected: true,
			Status:   fmt.Sprintf("%d VG(s)", len(vgs)),
			Details:  fmt.Sprintf("Volume Groups: %s", strings.Join(vgs, ", ")),
		}, nil
	}

	return plugin.Detection{
		Detected: true,
		Status:   "ONLINE",
		Details:  "LVM tools installed, no active volume groups",
	}, nil
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

	parts := strings.Split(out, "===LVS===")
	vgsRaw := strings.TrimSpace(parts[0])
	lvsRaw := ""
	pvsRaw := ""
	if len(parts) > 1 {
		rest := strings.Split(parts[1], "===PVS===")
		lvsRaw = strings.TrimSpace(rest[0])
		if len(rest) > 1 {
			pvsRaw = strings.TrimSpace(rest[1])
		}
	}

	return map[string]any{
		"vgs": vgsRaw,
		"lvs": lvsRaw,
		"pvs": pvsRaw,
	}, nil
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
						"vg":     vg,
						"lv":     lv,
						"size":   size,
						"resizefs": true,
						"become": true,
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
