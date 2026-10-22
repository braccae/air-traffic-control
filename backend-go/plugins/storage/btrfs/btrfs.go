package btrfs

import (
	"context"
	"fmt"
	"strings"

	"air-traffic-control/backend/pkg/plugin"
	"air-traffic-control/backend/pkg/plugin/newapi"
	"air-traffic-control/backend/pkg/plugin/settingsframework"
)

func init() {
	_ = plugin.Register(New())
}

// SettingsPageID is the route slug of the datacenter-wide Btrfs settings page.
const SettingsPageID = "storage/btrfs"

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
	_ newapi.TaskTypeProvider            = (*Plugin)(nil)
)

// Plugin implements Btrfs filesystem and subvolume management.
type Plugin struct{}

func New() *Plugin {
	return &Plugin{}
}

func (p *Plugin) Meta() plugin.Meta {
	return plugin.Meta{
		ID:          SettingsPageID,
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

// CollectionSchedule returns the polling and cache TTL schedule for Btrfs storage telemetry.
func (p *Plugin) CollectionSchedule() newapi.CollectionSchedule {
	return newapi.DefaultScheduleForKind(p.Meta().Kind)
}

// CollectionSchema documents the payload returned by Read.
func (p *Plugin) CollectionSchema() newapi.CollectionSchema {
	return newapi.CollectionSchema{
		Version:     "v2",
		RootKey:     "btrfs",
		Description: p.Meta().Description,
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
		"btrfs":      fsRaw,
		"subvolumes": subvolsRaw,
	}, nil
}

// CategorizedSettingsPage declares datacenter-wide Btrfs defaults via settingsframework.
func (p *Plugin) CategorizedSettingsPage() newapi.CategorizedSettingsPage {
	meta := p.Meta()
	return settingsframework.NewPageBuilder(meta.ID, meta.Label+" Defaults").
		WithIcon(meta.Icon).
		WithCategory(string(meta.Kind)).
		WithScope(newapi.SettingsScopeDatacenter).
		WithOrder(meta.Order).
		WithDescription("Datacenter-wide Btrfs subvolume root, scrub cadence, and snapshot retention defaults.").
		StringField(
			"defaultSubvolumeRoot",
			"Default Subvolume Root",
			"/var/lib/btrfs",
			settingsframework.WithPlaceholder("/var/lib/btrfs"),
			settingsframework.WithDescription("Default mountpoint prefix for managed Btrfs subvolumes."),
		).
		IntField(
			"scrubIntervalDays",
			"Scrub Interval (Days)",
			30,
			settingsframework.WithMinMax(1, 365),
			settingsframework.WithDescription("Recommended interval in days between Btrfs filesystem integrity scrubs."),
		).
		BoolField(
			"autoSnapshots",
			"Enable Automatic Subvolume Snapshots",
			true,
			settingsframework.WithDescription("Automatically snapshot managed subvolumes before destructive tasks."),
		).
		SelectField(
			"compression",
			"Default Compression Algorithm",
			"zstd",
			[]plugin.SettingsFieldOption{
				{Value: "zstd", Label: "zstd"},
				{Value: "lzo", Label: "lzo"},
				{Value: "zlib", Label: "zlib"},
				{Value: "none", Label: "None"},
			},
			settingsframework.WithDescription("Default transparent compression option for new Btrfs mounts."),
		).
		BuildCategorized()
}

// SettingsPage returns the underlying global SettingsPage schema.
func (p *Plugin) SettingsPage() plugin.SettingsPage {
	return p.CategorizedSettingsPage().SettingsPage
}

// ManagementActions returns rich action specifications for Btrfs mutations.
func (p *Plugin) ManagementActions() []newapi.ManagementActionSpec {
	return []newapi.ManagementActionSpec{
		{
			ActionSpec: plugin.ActionSpec{
				Name:        "create_subvolume",
				Label:       "Create Subvolume",
				Description: "Create a Btrfs subvolume",
				Options: []plugin.OptionSchema{
					{Name: "path", Type: "string", Required: true, Description: "Target subvolume directory path"},
				},
			},
			Category:         newapi.ActionCategoryConfiguration,
			Idempotent:       true,
			Dangerous:        false,
			InvalidatesKinds: []plugin.Kind{plugin.KindStorage},
		},
		{
			ActionSpec: plugin.ActionSpec{
				Name:        "delete_subvolume",
				Label:       "Delete Subvolume",
				Description: "Delete a Btrfs subvolume",
				Options: []plugin.OptionSchema{
					{Name: "path", Type: "string", Required: true, Description: "Subvolume path to delete"},
				},
			},
			Category:         newapi.ActionCategoryLifecycle,
			Idempotent:       true,
			Dangerous:        true,
			ConfirmPrompt:    "Permanently delete this Btrfs subvolume and its contents?",
			InvalidatesKinds: []plugin.Kind{plugin.KindStorage},
		},
	}
}

func (p *Plugin) Actions() []plugin.ActionSpec {
	mgmt := p.ManagementActions()
	specs := make([]plugin.ActionSpec, len(mgmt))
	for i, m := range mgmt {
		specs[i] = m.ActionSpec
	}
	return specs
}

// ValidateAction validates options against the declared ManagementActionSpec.
func (p *Plugin) ValidateAction(h plugin.HostFacts, action string, opts map[string]any) error {
	for _, spec := range p.ManagementActions() {
		if strings.EqualFold(spec.Name, action) {
			return newapi.ValidateManagementOptions(spec, opts)
		}
	}
	return fmt.Errorf("unsupported btrfs action: %s", action)
}

func (p *Plugin) Compile(h plugin.HostFacts, action string, opts map[string]any) (plugin.TaskSpec, error) {
	norm := strings.ToLower(strings.TrimSpace(action))
	switch norm {
	case "create_subvolume":
		path, _ := opts["path"].(string)
		path = strings.TrimSpace(path)
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
		path = strings.TrimSpace(path)
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

// ContextMenuEntries provides storage context menu items for compatible hosts.
func (p *Plugin) ContextMenuEntries(ctx context.Context, h plugin.HostFacts) []newapi.ContextMenuEntry {
	if !p.Compatible(h) {
		return nil
	}
	return newapi.NewContextMenuBuilder(p.Meta().ID).
		WithTarget(newapi.TargetStorage).
		WithGroup(string(p.Meta().Kind)).
		AddAction("btrfs-create-subvolume", "Create Subvolume", "create_new_folder", "create_subvolume", 10).
		AddDangerAction(
			"btrfs-delete-subvolume",
			"Delete Subvolume",
			"delete_forever",
			"delete_subvolume",
			"Permanently delete this Btrfs subvolume and its contents?",
			90,
		).
		Build()
}

// TaskTypes registers specialized storage operation task support for Btrfs.
func (p *Plugin) TaskTypes() []newapi.TaskTypeSpec {
	return []newapi.TaskTypeSpec{
		{
			Type:           newapi.TaskTypeStorageOp,
			PluginID:       p.Meta().ID,
			Label:          "Btrfs Storage Operation",
			Description:    "Create or delete Btrfs subvolumes via declarative Ansible tasks",
			Icon:           p.Meta().Icon,
			SupportsDryRun: true,
			SupportsCancel: true,
			Parameters: []plugin.OptionSchema{
				{
					Name:        "action",
					Type:        "select",
					Required:    false,
					Default:     "create_subvolume",
					Enum:        []string{"create_subvolume", "delete_subvolume"},
					Description: "Btrfs subvolume operation to perform",
				},
				{
					Name:        "path",
					Type:        "string",
					Required:    true,
					Description: "Target Btrfs subvolume directory path",
				},
			},
		},
	}
}

// CompileTask compiles a typed TaskRequest into a declarative Ansible TaskSpec.
func (p *Plugin) CompileTask(ctx context.Context, h plugin.HostFacts, req newapi.TaskRequest) (plugin.TaskSpec, error) {
	action := strings.TrimSpace(req.Action)
	if action == "" && req.Params != nil {
		if a, ok := req.Params["action"].(string); ok {
			action = strings.TrimSpace(a)
		}
	}
	if action == "" && req.Type == newapi.TaskTypeStorageOp {
		action = "create_subvolume"
	}
	if action == "" {
		return plugin.TaskSpec{}, fmt.Errorf("action is required for plugin %s", p.Meta().ID)
	}
	return p.Compile(h, action, req.Params)
}
