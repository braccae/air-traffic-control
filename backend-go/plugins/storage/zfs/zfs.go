package zfs

import (
	"context"
	"fmt"
	"strings"

	"air-traffic-control/backend/pkg/plugin"
)

func init() {
	_ = plugin.Register(New())
}

// Plugin implements ZFS pool and dataset inspection and mutation.
type Plugin struct{}

func New() *Plugin {
	return &Plugin{}
}

func (p *Plugin) Meta() plugin.Meta {
	return plugin.Meta{
		ID:          "storage/zfs",
		Kind:        plugin.KindStorage,
		Name:        "zfs",
		Label:       "ZFS Storage",
		Icon:        "inventory_2",
		Description: "OpenZFS pools, datasets, snapshots, and background scrub management",
		Order:       10,
	}
}

func (p *Plugin) Compatible(h plugin.HostFacts) bool {
	return h.OSFamily != "windows" && h.OSFamily != "router_switch"
}

func (p *Plugin) Requirements() plugin.Requirements {
	return plugin.Requirements{
		HostPackages: map[string][]string{
			"dnf": {"zfs"},
			"apt": {"zfsutils-linux"},
			"apk": {"zfs"},
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

	out, err := r.RunCommand(ctx, "command -v zfs && command -v zpool")
	if err != nil || strings.TrimSpace(out) == "" {
		return plugin.Detection{
			Detected: false,
			Status:   "UNAVAILABLE",
			Error:    "zfs or zpool command not found",
		}, nil
	}

	// Count pools
	poolOut, err := r.RunCommand(ctx, "zpool list -H -o name 2>/dev/null || true")
	if err == nil && strings.TrimSpace(poolOut) != "" && !strings.Contains(strings.ToLower(poolOut), "no pools") {
		pools := strings.Fields(strings.TrimSpace(poolOut))
		if len(pools) > 0 {
			return plugin.Detection{
				Detected: true,
				Status:   fmt.Sprintf("%d pool(s)", len(pools)),
				Details:  fmt.Sprintf("Pools: %s", strings.Join(pools, ", ")),
			}, nil
		}
	}

	return plugin.Detection{
		Detected: false,
		Status:   "NO_POOLS",
		Details:  "ZFS binaries present, but no active pools imported",
	}, nil
}

func (p *Plugin) Read(ctx context.Context, r plugin.Runner, h plugin.HostFacts) (any, error) {
	if r == nil {
		return nil, fmt.Errorf("runner is nil")
	}

	cmd := "findmnt -J -l -t zfs 2>/dev/null || true; echo '===ZFS==='; (sudo -n zfs list 2>/dev/null || zfs list 2>/dev/null || echo 'NO_ZFS'); echo '===ZPOOL==='; (sudo -n zpool status 2>/dev/null || zpool status 2>/dev/null || echo 'NO_ZPOOL'); echo '===ZPOOL_LIST==='; (sudo -n zpool list -H 2>/dev/null || zpool list -H 2>/dev/null || echo 'NO_ZPOOL_LIST')"
	out, err := r.RunCommand(ctx, cmd)
	if err != nil {
		return nil, err
	}

	parts := strings.Split(out, "===ZFS===")
	findmntRaw := strings.TrimSpace(parts[0])

	var zfsRaw, zpoolRaw, zpoolListRaw string
	if len(parts) > 1 {
		rest1 := strings.Split(parts[1], "===ZPOOL===")
		zfsRaw = strings.TrimSpace(rest1[0])
		if len(rest1) > 1 {
			rest2 := strings.Split(rest1[1], "===ZPOOL_LIST===")
			zpoolRaw = strings.TrimSpace(rest2[0])
			if len(rest2) > 1 {
				zpoolListRaw = strings.TrimSpace(rest2[1])
			}
		}
	}

	return map[string]any{
		"findmnt":     findmntRaw,
		"zfsList":     zfsRaw,
		"zpoolStatus": zpoolRaw,
		"zpoolList":   zpoolListRaw,
	}, nil
}

func (p *Plugin) Actions() []plugin.ActionSpec {
	return []plugin.ActionSpec{
		{
			Name:        "scrub",
			Label:       "Scrub Pool",
			Description: "Initiate or stop a storage pool data integrity scrub",
			Options: []plugin.OptionSchema{
				{Name: "pool", Type: "string", Required: true, Description: "ZFS pool name"},
				{Name: "state", Type: "select", Enum: []string{"scrubbing", "stopped"}, Default: "scrubbing", Description: "Scrub status"},
			},
		},
		{
			Name:        "snapshot",
			Label:       "Create Snapshot",
			Description: "Take an atomic read-only point-in-time snapshot of a dataset",
			Options: []plugin.OptionSchema{
				{Name: "dataset", Type: "string", Required: true, Description: "Dataset path (e.g. tank/data)"},
				{Name: "snapshot", Type: "string", Required: true, Description: "Snapshot name tag (e.g. auto-2026-10-19)"},
			},
		},
		{
			Name:        "create_dataset",
			Label:       "Create Dataset",
			Description: "Create a new ZFS filesystem dataset",
			Options: []plugin.OptionSchema{
				{Name: "name", Type: "string", Required: true, Description: "Dataset path (e.g. tank/containers)"},
				{Name: "mountpoint", Type: "string", Required: false, Description: "Custom mountpoint path"},
			},
		},
		{
			Name:        "destroy_dataset",
			Label:       "Destroy Dataset",
			Description: "Permanently delete a dataset and its contents",
			Options: []plugin.OptionSchema{
				{Name: "name", Type: "string", Required: true, Description: "Dataset path to destroy"},
			},
		},
	}
}

func (p *Plugin) Compile(h plugin.HostFacts, action string, opts map[string]any) (plugin.TaskSpec, error) {
	norm := strings.ToLower(action)
	switch norm {
	case "scrub":
		pool, _ := opts["pool"].(string)
		if pool == "" {
			return plugin.TaskSpec{}, fmt.Errorf("pool name is required")
		}
		state, _ := opts["state"].(string)
		if state == "" {
			state = "scrubbing"
		}
		return plugin.TaskSpec{
			Name: fmt.Sprintf("Scrub ZFS pool %s", pool),
			Steps: []plugin.TaskStep{
				{
					Name:   fmt.Sprintf("Ensure ZFS pool %s scrub is %s", pool, state),
					Module: "community.general.zpool",
					Params: map[string]any{
						"name":   pool,
						"state":  state,
						"become": true,
					},
				},
			},
		}, nil

	case "snapshot":
		dataset, _ := opts["dataset"].(string)
		snapName, _ := opts["snapshot"].(string)
		if dataset == "" || snapName == "" {
			return plugin.TaskSpec{}, fmt.Errorf("dataset and snapshot name are required")
		}
		target := fmt.Sprintf("%s@%s", dataset, snapName)
		return plugin.TaskSpec{
			Name: fmt.Sprintf("Snapshot ZFS dataset %s", target),
			Steps: []plugin.TaskStep{
				{
					Name:   fmt.Sprintf("Create snapshot %s", target),
					Module: "community.general.zfs",
					Params: map[string]any{
						"name":   target,
						"state":  "present",
						"become": true,
					},
				},
			},
		}, nil

	case "create_dataset":
		name, _ := opts["name"].(string)
		if name == "" {
			return plugin.TaskSpec{}, fmt.Errorf("dataset name is required")
		}
		params := map[string]any{
			"name":   name,
			"state":  "present",
			"become": true,
		}
		if mp, ok := opts["mountpoint"].(string); ok && mp != "" {
			params["mountpoint"] = mp
		}
		return plugin.TaskSpec{
			Name: fmt.Sprintf("Create ZFS dataset %s", name),
			Steps: []plugin.TaskStep{
				{
					Name:   fmt.Sprintf("Create ZFS dataset %s", name),
					Module: "community.general.zfs",
					Params: params,
				},
			},
		}, nil

	case "destroy_dataset":
		name, _ := opts["name"].(string)
		if name == "" {
			return plugin.TaskSpec{}, fmt.Errorf("dataset name is required")
		}
		return plugin.TaskSpec{
			Name: fmt.Sprintf("Destroy ZFS dataset %s", name),
			Steps: []plugin.TaskStep{
				{
					Name:   fmt.Sprintf("Destroy ZFS dataset %s", name),
					Module: "community.general.zfs",
					Params: map[string]any{
						"name":   name,
						"state":  "absent",
						"become": true,
					},
				},
			},
		}, nil

	default:
		return plugin.TaskSpec{}, fmt.Errorf("unsupported zfs action: %s", action)
	}
}
