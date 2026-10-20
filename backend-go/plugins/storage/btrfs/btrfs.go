package btrfs

import (
	"context"
	"fmt"
	"strings"

	"air-traffic-control/backend/pkg/plugin"
)

func init() {
	_ = plugin.Register(New())
}

// Plugin implements Btrfs filesystem and subvolume management.
type Plugin struct{}

func New() *Plugin {
	return &Plugin{}
}

func (p *Plugin) Meta() plugin.Meta {
	return plugin.Meta{
		ID:          "storage/btrfs",
		Kind:        plugin.KindStorage,
		Name:        "btrfs",
		Label:       "Btrfs Storage",
		Icon:        "folder_managed",
		Description: "Btrfs filesystems, multi-device pools, and subvolume snapshot management",
		Order:       20,
	}
}

func (p *Plugin) Compatible(h plugin.HostFacts) bool {
	return h.OSFamily != "windows" && h.OSFamily != "router_switch"
}

func (p *Plugin) Requirements() plugin.Requirements {
	return plugin.Requirements{
		HostPackages: map[string][]string{
			"dnf": {"btrfs-progs"},
			"apt": {"btrfs-progs"},
			"apk": {"btrfs-progs"},
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

	out, err := r.RunCommand(ctx, "command -v btrfs")
	if err != nil || strings.TrimSpace(out) == "" {
		return plugin.Detection{
			Detected: false,
			Status:   "UNAVAILABLE",
			Error:    "btrfs command not found",
		}, nil
	}

	fsOut, err := r.RunCommand(ctx, "btrfs filesystem show 2>/dev/null || true")
	if err == nil && (strings.Contains(fsOut, "Label:") || strings.Contains(fsOut, "uuid:")) {
		return plugin.Detection{
			Detected: true,
			Status:   "ONLINE",
			Details:  "Active Btrfs filesystems found",
		}, nil
	}

	return plugin.Detection{
		Detected: false,
		Status:   "NO_FILESYSTEMS",
		Details:  "btrfs installed, but no active filesystems found",
	}, nil
}

func (p *Plugin) Read(ctx context.Context, r plugin.Runner, h plugin.HostFacts) (any, error) {
	if r == nil {
		return nil, fmt.Errorf("runner is nil")
	}

	cmd := "btrfs filesystem show 2>/dev/null || true; echo '===SUBVOLUMES==='; btrfs subvolume list / 2>/dev/null || true"
	out, err := r.RunCommand(ctx, cmd)
	if err != nil {
		return nil, err
	}

	parts := strings.Split(out, "===SUBVOLUMES===")
	fsRaw := strings.TrimSpace(parts[0])
	subvolsRaw := ""
	if len(parts) > 1 {
		subvolsRaw = strings.TrimSpace(parts[1])
	}

	return map[string]any{
		"btrfs":       fsRaw,
		"subvolumes": subvolsRaw,
	}, nil
}

func (p *Plugin) Actions() []plugin.ActionSpec {
	return []plugin.ActionSpec{
		{
			Name:        "create_subvolume",
			Label:       "Create Subvolume",
			Description: "Create a Btrfs subvolume",
			Options: []plugin.OptionSchema{
				{Name: "path", Type: "string", Required: true, Description: "Target subvolume directory path"},
			},
		},
		{
			Name:        "delete_subvolume",
			Label:       "Delete Subvolume",
			Description: "Delete a Btrfs subvolume",
			Options: []plugin.OptionSchema{
				{Name: "path", Type: "string", Required: true, Description: "Subvolume path to delete"},
			},
		},
	}
}

func (p *Plugin) Compile(h plugin.HostFacts, action string, opts map[string]any) (plugin.TaskSpec, error) {
	norm := strings.ToLower(action)
	switch norm {
	case "create_subvolume":
		path, _ := opts["path"].(string)
		if path == "" {
			return plugin.TaskSpec{}, fmt.Errorf("subvolume path is required")
		}
		return plugin.TaskSpec{
			Name: fmt.Sprintf("Create Btrfs subvolume at %s", path),
			Steps: []plugin.TaskStep{
				{
					Name:   fmt.Sprintf("Create Btrfs subvolume %s", path),
					Module: "community.general.btrfs_subvolume",
					Params: map[string]any{
						"name":   path,
						"state":  "present",
						"become": true,
					},
				},
			},
		}, nil

	case "delete_subvolume":
		path, _ := opts["path"].(string)
		if path == "" {
			return plugin.TaskSpec{}, fmt.Errorf("subvolume path is required")
		}
		return plugin.TaskSpec{
			Name: fmt.Sprintf("Delete Btrfs subvolume at %s", path),
			Steps: []plugin.TaskStep{
				{
					Name:   fmt.Sprintf("Delete Btrfs subvolume %s", path),
					Module: "community.general.btrfs_subvolume",
					Params: map[string]any{
						"name":   path,
						"state":  "absent",
						"become": true,
					},
				},
			},
		}, nil

	default:
		return plugin.TaskSpec{}, fmt.Errorf("unsupported btrfs action: %s", action)
	}
}
