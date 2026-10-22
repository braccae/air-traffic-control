package zfs

import (
	"context"
	"fmt"
	"strings"

	"air-traffic-control/backend/pkg/plugin"
	"air-traffic-control/backend/pkg/plugin/newapi"
	"air-traffic-control/backend/pkg/plugin/settingsframework"
)

const (
	// PluginID is the canonical identifier for the ZFS storage plugin.
	PluginID = "storage/zfs"
	// SettingsPageID is the settings page identifier for datacenter ZFS defaults.
	SettingsPageID = "storage/zfs"
	// DefaultPoolName is the fallback ZFS pool name when no per-host override or setting is configured.
	DefaultPoolName = "tank"
	// DefaultScrubIntervalDays is the default interval in days between pool scrubs.
	DefaultScrubIntervalDays = 30
)

// Settings field keys for the ZFS datacenter settings page.
const (
	FieldDefaultPool                  = "defaultPool"
	FieldScrubIntervalDays            = "scrubIntervalDays"
	FieldAutoSnapshot                 = "autoSnapshot"
	FieldEncryptionPassphraseVaultVar = "encryptionPassphraseVaultVar"
)

var (
	_ plugin.Plugin                      = (*Plugin)(nil)
	_ plugin.Reader                      = (*Plugin)(nil)
	_ plugin.Actor                       = (*Plugin)(nil)
	_ plugin.SettingsPageProvider        = (*Plugin)(nil)
	_ newapi.DataCollectionPlugin        = (*Plugin)(nil)
	_ newapi.ManagementPlugin            = (*Plugin)(nil)
	_ newapi.CategorizedSettingsProvider = (*Plugin)(nil)
	_ newapi.ContextMenuProvider         = (*Plugin)(nil)
	_ newapi.TaskCompileProvider         = (*Plugin)(nil)
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
		ID:          PluginID,
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

// CollectionSchedule returns the polling and cache TTL schedule for ZFS storage inspection.
func (p *Plugin) CollectionSchedule() newapi.CollectionSchedule {
	return newapi.DefaultScheduleForKind(plugin.KindStorage)
}

// CollectionSchema documents the payload structure returned by Read.
func (p *Plugin) CollectionSchema() newapi.CollectionSchema {
	return newapi.CollectionSchema{
		Version:     "v2",
		RootKey:     "zfsList",
		Description: p.Meta().Description,
	}
}

// CategorizedSettingsPage declares the datacenter-scoped settings schema for ZFS defaults.
func (p *Plugin) CategorizedSettingsPage() newapi.CategorizedSettingsPage {
	return settingsframework.NewPageBuilder(SettingsPageID, "ZFS Storage Defaults").
		WithIcon(p.Meta().Icon).
		WithCategory(string(p.Meta().Kind)).
		WithScope(newapi.SettingsScopeDatacenter).
		WithOrder(p.Meta().Order).
		WithDescription("Datacenter-wide OpenZFS scrub cadence and snapshot retention defaults.").
		StringField(
			FieldDefaultPool,
			"Default Pool Name",
			DefaultPoolName,
			settingsframework.WithPlaceholder(DefaultPoolName),
			settingsframework.WithDescription("Fallback ZFS pool name when no per-host pool is specified."),
		).
		IntField(
			FieldScrubIntervalDays,
			"Scrub Interval (Days)",
			DefaultScrubIntervalDays,
			settingsframework.WithMinMax(1, 365),
			settingsframework.WithDescription("Recommended interval in days between pool integrity scrubs."),
		).
		BoolField(
			FieldAutoSnapshot,
			"Enable Automatic Snapshots",
			true,
			settingsframework.WithDescription("Enable periodic automatic ZFS snapshots across managed pools."),
		).
		SecretField(
			FieldEncryptionPassphraseVaultVar,
			"Encryption Passphrase Vault Variable",
			settingsframework.WithPlaceholder("vault_zfs_passphrase"),
			settingsframework.WithDescription("Ansible Vault variable name holding the dataset encryption passphrase."),
		).
		BuildCategorized()
}

// SettingsPage returns the underlying plugin.SettingsPage for legacy settings consumers.
func (p *Plugin) SettingsPage() plugin.SettingsPage {
	return p.CategorizedSettingsPage().SettingsPage
}

// DefaultPool returns the persisted datacenter default pool name or DefaultPoolName.
func DefaultPool() string {
	return settingsframework.NewStoreAdapter(nil).StringOrDefault(SettingsPageID, FieldDefaultPool, DefaultPoolName)
}

// ScrubIntervalDays returns the persisted datacenter scrub interval in days or DefaultScrubIntervalDays.
func ScrubIntervalDays() int {
	return settingsframework.NewStoreAdapter(nil).IntOrDefault(SettingsPageID, FieldScrubIntervalDays, DefaultScrubIntervalDays)
}

// AutoSnapshotEnabled returns the persisted automatic snapshot toggle or true by default.
func AutoSnapshotEnabled() bool {
	return settingsframework.NewStoreAdapter(nil).BoolOrDefault(SettingsPageID, FieldAutoSnapshot, true)
}

// EncryptionPassphraseVaultVar returns the configured unmasked vault variable name if set.
func EncryptionPassphraseVaultVar() (string, bool) {
	return settingsframework.NewStoreAdapter(nil).SecretValue(SettingsPageID, FieldEncryptionPassphraseVaultVar)
}

// ContextMenuEntries contributes ZFS storage actions to the UI context menu for compatible hosts.
func (p *Plugin) ContextMenuEntries(ctx context.Context, h plugin.HostFacts) []newapi.ContextMenuEntry {
	if !p.Compatible(h) {
		return nil
	}
	return newapi.NewContextMenuBuilder(PluginID).
		WithTarget(newapi.TargetStorage).
		WithGroup(string(plugin.KindStorage)).
		AddAction("zfs-scrub", "Scrub Pool", "cleaning_services", "scrub", 10).
		AddAction("zfs-snapshot", "Create Snapshot", "photo_camera", "snapshot", 20).
		AddAction("zfs-create-dataset", "Create Dataset", "create_new_folder", "create_dataset", 30).
		AddDangerAction("zfs-destroy-dataset", "Destroy Dataset", "delete_forever", "destroy_dataset",
			"Permanently delete this dataset and its contents?", 90).
		Build()
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

// ManagementActions returns the structured v2 management action specifications for ZFS.
func (p *Plugin) ManagementActions() []newapi.ManagementActionSpec {
	return []newapi.ManagementActionSpec{
		{
			ActionSpec: plugin.ActionSpec{
				Name:        "scrub",
				Label:       "Scrub Pool",
				Description: "Initiate or stop a storage pool data integrity scrub",
				Options: []plugin.OptionSchema{
					{Name: "pool", Type: "string", Required: true, Description: "ZFS pool name"},
					{Name: "state", Type: "select", Enum: []string{"scrubbing", "stopped"}, Default: "scrubbing", Description: "Scrub status"},
				},
			},
			Category:         newapi.ActionCategoryMaintenance,
			Idempotent:       true,
			InvalidatesKinds: []plugin.Kind{plugin.KindStorage},
		},
		{
			ActionSpec: plugin.ActionSpec{
				Name:        "snapshot",
				Label:       "Create Snapshot",
				Description: "Take an atomic read-only point-in-time snapshot of a dataset",
				Options: []plugin.OptionSchema{
					{Name: "dataset", Type: "string", Required: true, Description: "Dataset path (e.g. tank/data)"},
					{Name: "snapshot", Type: "string", Required: true, Description: "Snapshot name tag (e.g. auto-2026-10-19)"},
				},
			},
			Category:         newapi.ActionCategoryLifecycle,
			Idempotent:       true,
			InvalidatesKinds: []plugin.Kind{plugin.KindStorage},
		},
		{
			ActionSpec: plugin.ActionSpec{
				Name:        "create_dataset",
				Label:       "Create Dataset",
				Description: "Create a new ZFS filesystem dataset",
				Options: []plugin.OptionSchema{
					{Name: "name", Type: "string", Required: true, Description: "Dataset path (e.g. tank/containers)"},
					{Name: "mountpoint", Type: "string", Required: false, Description: "Custom mountpoint path"},
				},
			},
			Category:         newapi.ActionCategoryLifecycle,
			Idempotent:       true,
			InvalidatesKinds: []plugin.Kind{plugin.KindStorage},
		},
		{
			ActionSpec: plugin.ActionSpec{
				Name:        "destroy_dataset",
				Label:       "Destroy Dataset",
				Description: "Permanently delete a dataset and its contents",
				Options: []plugin.OptionSchema{
					{Name: "name", Type: "string", Required: true, Description: "Dataset path to destroy"},
				},
			},
			Category:         newapi.ActionCategoryLifecycle,
			Dangerous:        true,
			ConfirmPrompt:    "Permanently delete this dataset and its contents?",
			InvalidatesKinds: []plugin.Kind{plugin.KindStorage},
		},
	}
}

// Actions projects the legacy ActionSpec slice from ManagementActions.
func (p *Plugin) Actions() []plugin.ActionSpec {
	mgmt := p.ManagementActions()
	specs := make([]plugin.ActionSpec, len(mgmt))
	for i, m := range mgmt {
		specs[i] = m.ActionSpec
	}
	return specs
}

// ValidateAction validates options for a ZFS management action against its schema.
func (p *Plugin) ValidateAction(h plugin.HostFacts, action string, opts map[string]any) error {
	for _, spec := range p.ManagementActions() {
		if strings.EqualFold(spec.Name, action) {
			return newapi.ValidateManagementOptions(spec, opts)
		}
	}
	return fmt.Errorf("unsupported zfs action: %s", action)
}

func (p *Plugin) Compile(h plugin.HostFacts, action string, opts map[string]any) (plugin.TaskSpec, error) {
	norm := strings.ToLower(strings.TrimSpace(action))
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
		if err := p.ValidateAction(h, norm, map[string]any{"pool": pool, "state": state}); err != nil {
			return plugin.TaskSpec{}, err
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

// CompileTask implements newapi.TaskCompileProvider to compile typed TaskRequests into Ansible TaskSpecs.
func (p *Plugin) CompileTask(ctx context.Context, h plugin.HostFacts, req newapi.TaskRequest) (plugin.TaskSpec, error) {
	params := make(map[string]any, len(req.Params))
	for k, v := range req.Params {
		params[k] = v
	}

	action := strings.TrimSpace(req.Action)
	if action == "" {
		if a, ok := params["action"].(string); ok && strings.TrimSpace(a) != "" {
			action = strings.TrimSpace(a)
		} else if op, ok := params["operation"].(string); ok && strings.TrimSpace(op) != "" {
			action = strings.TrimSpace(op)
		}
	}
	if action == "" {
		return plugin.TaskSpec{}, fmt.Errorf("action is required for plugin %s", PluginID)
	}

	// Normalize TaskTypeStorageOp "target" parameter into the action-specific option key when needed.
	if target, ok := params["target"].(string); ok && strings.TrimSpace(target) != "" {
		switch strings.ToLower(action) {
		case "scrub":
			if _, hasPool := params["pool"]; !hasPool {
				params["pool"] = strings.TrimSpace(target)
			}
		case "snapshot":
			if _, hasDataset := params["dataset"]; !hasDataset {
				params["dataset"] = strings.TrimSpace(target)
			}
		case "create_dataset", "destroy_dataset":
			if _, hasName := params["name"]; !hasName {
				params["name"] = strings.TrimSpace(target)
			}
		}
	}

	return p.Compile(h, action, params)
}
