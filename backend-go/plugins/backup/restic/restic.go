package restic

import (
	"context"
	"fmt"
	"strings"

	"air-traffic-control/backend/pkg/plugin"
	"air-traffic-control/backend/pkg/plugin/newapi"
	"air-traffic-control/backend/pkg/plugin/settingsframework"
	pkgrestic "air-traffic-control/backend/pkg/restic"
	"air-traffic-control/backend/pkg/ssh"
)

const (
	// PluginID is the canonical identifier for the Restic backup plugin.
	PluginID = "backup/restic"

	// SettingsPageID is the route slug of the datacenter-wide Restic settings page
	// contributed by this module. It is also the key under which the framework
	// persists the page's values.
	SettingsPageID = "backup/restic"

	// DefaultRepoPath is the fallback repository used when neither a per-host
	// override nor a persisted global default is configured.
	DefaultRepoPath = "/var/backups/restic"

	// Default retention policy settings.
	DefaultRetentionDaily   = 7
	DefaultRetentionWeekly  = 4
	DefaultRetentionMonthly = 12
)

// Settings field keys for the Restic datacenter settings page.
const (
	FieldRepoPath         = "repoPath"
	FieldRetentionDaily   = "retentionDaily"
	FieldRetentionWeekly  = "retentionWeekly"
	FieldRetentionMonthly = "retentionMonthly"
	FieldPasswordVaultVar = "passwordVaultVar"
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
	_ newapi.TaskTypeProvider            = (*Plugin)(nil)
	_ newapi.TaskCompileProvider         = (*Plugin)(nil)
)

func init() {
	_ = plugin.Register(New())
}

// Plugin wraps pkg/restic to provide backup snapshots and repository inspection.
type Plugin struct{}

func New() *Plugin {
	return &Plugin{}
}

func (p *Plugin) Meta() plugin.Meta {
	return plugin.Meta{
		ID:          PluginID,
		Kind:        plugin.KindBackup,
		Name:        "restic",
		Label:       "Restic Backups",
		Icon:        "backup",
		Description: "Encrypted, deduplicated snapshots and repository verification via Restic",
		Order:       10,
	}
}

func (p *Plugin) Compatible(h plugin.HostFacts) bool {
	return h.OSFamily != "windows" && h.OSFamily != "router_switch"
}

func (p *Plugin) Requirements() plugin.Requirements {
	return plugin.Requirements{
		HostPackages: map[string][]string{
			"dnf": {"restic"},
			"apt": {"restic"},
			"apk": {"restic"},
		},
	}
}

// CollectionSchedule returns the polling and cache TTL schedule for Restic backup inspection.
func (p *Plugin) CollectionSchedule() newapi.CollectionSchedule {
	return newapi.DefaultScheduleForKind(p.Meta().Kind)
}

// CollectionSchema documents the v2 payload structure returned by Read.
func (p *Plugin) CollectionSchema() newapi.CollectionSchema {
	return newapi.CollectionSchema{
		Version:     "v2",
		RootKey:     "snapshots",
		Description: p.Meta().Description,
	}
}

// CategorizedSettingsPage declares the datacenter-wide Restic settings schema
// via settingsframework.
func (p *Plugin) CategorizedSettingsPage() newapi.CategorizedSettingsPage {
	return settingsframework.NewPageBuilder(SettingsPageID, "Restic Backup Defaults").
		WithIcon("cloud_sync").
		WithCategory(string(plugin.KindBackup)).
		WithScope(newapi.SettingsScopeDatacenter).
		WithOrder(20).
		WithDescription("Datacenter-wide repository and retention defaults applied to every node unless a per-host override is configured.").
		StringField(
			FieldRepoPath,
			"Default Repository",
			DefaultRepoPath,
			settingsframework.WithRequired(true),
			settingsframework.WithPlaceholder("/var/backups/restic or s3:https://..."),
			settingsframework.WithDescription("Restic repository path or URL used when a node has no per-host override."),
		).
		IntField(
			FieldRetentionDaily,
			"Daily Snapshots Kept",
			DefaultRetentionDaily,
			settingsframework.WithMinMax(0, 365),
			settingsframework.WithDescription("Snapshots retained by the daily forget policy."),
		).
		IntField(
			FieldRetentionWeekly,
			"Weekly Snapshots Kept",
			DefaultRetentionWeekly,
			settingsframework.WithMinMax(0, 104),
			settingsframework.WithDescription("Snapshots retained by the weekly forget policy."),
		).
		IntField(
			FieldRetentionMonthly,
			"Monthly Snapshots Kept",
			DefaultRetentionMonthly,
			settingsframework.WithMinMax(0, 120),
			settingsframework.WithDescription("Snapshots retained by the monthly forget policy."),
		).
		SecretField(
			FieldPasswordVaultVar,
			"Repository Password Vault Variable",
			settingsframework.WithPlaceholder("vault_restic_password"),
			settingsframework.WithDescription("Ansible Vault variable name holding the repository password. The password itself is never stored here."),
		).
		BuildCategorized()
}

// SettingsPage implements plugin.SettingsPageProvider so the module appears in
// the Datacenter Settings sidebar with no frontend work of its own.
func (p *Plugin) SettingsPage() plugin.SettingsPage {
	return p.CategorizedSettingsPage().SettingsPage
}

// DefaultRepository returns the persisted datacenter default repository path or DefaultRepoPath.
func DefaultRepository() string {
	return settingsframework.NewStoreAdapter(nil).StringOrDefault(SettingsPageID, FieldRepoPath, DefaultRepoPath)
}

// RetentionDaily returns the persisted daily snapshot retention count or DefaultRetentionDaily.
func RetentionDaily() int {
	return settingsframework.NewStoreAdapter(nil).IntOrDefault(SettingsPageID, FieldRetentionDaily, DefaultRetentionDaily)
}

// RetentionWeekly returns the persisted weekly snapshot retention count or DefaultRetentionWeekly.
func RetentionWeekly() int {
	return settingsframework.NewStoreAdapter(nil).IntOrDefault(SettingsPageID, FieldRetentionWeekly, DefaultRetentionWeekly)
}

// RetentionMonthly returns the persisted monthly snapshot retention count or DefaultRetentionMonthly.
func RetentionMonthly() int {
	return settingsframework.NewStoreAdapter(nil).IntOrDefault(SettingsPageID, FieldRetentionMonthly, DefaultRetentionMonthly)
}

// PasswordVaultVar returns the configured unmasked vault variable name if set.
func PasswordVaultVar() (string, bool) {
	return settingsframework.NewStoreAdapter(nil).SecretValue(SettingsPageID, FieldPasswordVaultVar)
}

// globalDefaultRepo returns the persisted datacenter-wide repository default, or
// an empty string when none has been configured.
func globalDefaultRepo() string {
	return settingsframework.NewStoreAdapter(nil).StringOrDefault(SettingsPageID, FieldRepoPath, "")
}

// ContextMenuEntries contributes Restic backup actions to the UI context menu for compatible hosts.
func (p *Plugin) ContextMenuEntries(ctx context.Context, h plugin.HostFacts) []newapi.ContextMenuEntry {
	if !p.Compatible(h) {
		return nil
	}
	return newapi.NewContextMenuBuilder(PluginID).
		WithTarget(newapi.TargetNode).
		WithGroup(string(plugin.KindBackup)).
		AddAction("restic-backup", "Create Backup", "backup", "backup", 10).
		AddAction("restic-check", "Verify Integrity", "fact_check", "check", 20).
		Build()
}

// TaskTypes registers specialized backup snapshot task support for Restic.
func (p *Plugin) TaskTypes() []newapi.TaskTypeSpec {
	return []newapi.TaskTypeSpec{
		{
			Type:           newapi.TaskTypeBackupSnapshot,
			PluginID:       PluginID,
			Label:          "Restic Backup Snapshot",
			Description:    "Create encrypted Restic backup snapshots or verify repository integrity",
			Icon:           p.Meta().Icon,
			SupportsDryRun: true,
			SupportsCancel: true,
			Parameters: []plugin.OptionSchema{
				{
					Name:        "operation",
					Type:        "select",
					Required:    false,
					Default:     "backup",
					Enum:        []string{"backup", "snapshot", "check"},
					Description: "Restic backup operation to perform",
				},
				{
					Name:        "path",
					Type:        "string",
					Required:    false,
					Description: "Directory path to back up (required for backup/snapshot)",
				},
				{
					Name:        "repo",
					Type:        "string",
					Required:    false,
					Description: "Restic repository path or URL",
				},
				{
					Name:        "tags",
					Type:        "string",
					Required:    false,
					Description: "Comma-separated snapshot tags",
				},
			},
		},
	}
}

func (p *Plugin) Probe(ctx context.Context, r plugin.Runner, h plugin.HostFacts) (plugin.Detection, error) {
	if r == nil {
		return plugin.Detection{Detected: false, Status: "OFFLINE", Error: "runner is nil"}, nil
	}

	out, err := r.RunCommand(ctx, "command -v restic")
	if err != nil || strings.TrimSpace(out) == "" {
		return plugin.Detection{
			Detected: false,
			Status:   "UNAVAILABLE",
			Error:    "restic binary not found",
		}, nil
	}

	return plugin.Detection{
		Detected: true,
		Status:   "ONLINE",
		Details:  "restic binary installed",
	}, nil
}

type runnerAdapter struct {
	ctx    context.Context
	runner plugin.Runner
}

func (a *runnerAdapter) RunCommand(cfg ssh.Config, command string) (string, error) {
	return a.runner.RunCommand(a.ctx, command)
}

func (p *Plugin) Read(ctx context.Context, r plugin.Runner, h plugin.HostFacts) (any, error) {
	if r == nil {
		return nil, fmt.Errorf("runner is nil")
	}

	repo := DefaultRepoPath
	if h.Overrides != nil && h.Overrides["resticRepo"] != "" {
		repo = h.Overrides["resticRepo"]
	} else if global := globalDefaultRepo(); global != "" {
		repo = global
	}

	adapter := &runnerAdapter{ctx: ctx, runner: r}
	mgr := pkgrestic.NewManager(adapter, ssh.Config{}, repo)

	password := ""
	if h.Overrides != nil && h.Overrides["resticPassword"] != "" {
		password = h.Overrides["resticPassword"]
	}

	snapshots, err := mgr.ListSnapshots(password)
	if err != nil {
		return map[string]any{
			"repo":      repo,
			"snapshots": []any{},
			"error":     err.Error(),
		}, nil
	}

	return map[string]any{
		"repo":      repo,
		"snapshots": snapshots,
	}, nil
}

// ManagementActions returns the structured v2 management action specifications for Restic.
func (p *Plugin) ManagementActions() []newapi.ManagementActionSpec {
	return []newapi.ManagementActionSpec{
		{
			ActionSpec: plugin.ActionSpec{
				Name:        "backup",
				Label:       "Create Backup",
				Description: "Run restic backup of targeted directories",
				Options: []plugin.OptionSchema{
					{Name: "path", Type: "string", Required: true, Description: "Directory path to back up"},
					{Name: "repo", Type: "string", Required: false, Description: "Restic repository path"},
					{Name: "tags", Type: "string", Required: false, Description: "Comma-separated tags"},
				},
			},
			Category:         newapi.ActionCategoryMaintenance,
			Idempotent:       false,
			Dangerous:        false,
			InvalidatesKinds: []plugin.Kind{plugin.KindBackup},
		},
		{
			ActionSpec: plugin.ActionSpec{
				Name:        "check",
				Label:       "Verify Integrity",
				Description: "Verify the structural integrity and consistency of the repository",
				Options: []plugin.OptionSchema{
					{Name: "repo", Type: "string", Required: false, Description: "Restic repository path"},
				},
			},
			Category:         newapi.ActionCategoryDiagnostic,
			Idempotent:       true,
			Dangerous:        false,
			InvalidatesKinds: []plugin.Kind{plugin.KindBackup},
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

// ValidateAction validates options for a Restic management action against its schema.
func (p *Plugin) ValidateAction(h plugin.HostFacts, action string, opts map[string]any) error {
	for _, spec := range p.ManagementActions() {
		if strings.EqualFold(spec.Name, action) {
			return newapi.ValidateManagementOptions(spec, opts)
		}
	}
	return fmt.Errorf("unsupported restic action: %s", action)
}

func (p *Plugin) Compile(h plugin.HostFacts, action string, opts map[string]any) (plugin.TaskSpec, error) {
	norm := strings.ToLower(strings.TrimSpace(action))
	switch norm {
	case "backup":
		path, _ := opts["path"].(string)
		if path == "" {
			return plugin.TaskSpec{}, fmt.Errorf("backup path is required")
		}
		repo, _ := opts["repo"].(string)
		if repo == "" {
			repo = DefaultRepoPath
		}
		tags, _ := opts["tags"].(string)
		tagArg := ""
		if tags != "" {
			tagArg = fmt.Sprintf("--tag %s", tags)
		}
		cmd := fmt.Sprintf("restic -r %s backup %s %s", repo, tagArg, path)

		return plugin.TaskSpec{
			Name: fmt.Sprintf("Run restic backup of %s", path),
			Steps: []plugin.TaskStep{
				{
					Name:   fmt.Sprintf("Restic backup %s", path),
					Module: "ansible.builtin.command",
					Params: map[string]any{
						"cmd":    cmd,
						"become": true,
					},
				},
			},
		}, nil

	case "check":
		repo, _ := opts["repo"].(string)
		if repo == "" {
			repo = DefaultRepoPath
		}
		cmd := fmt.Sprintf("restic -r %s check", repo)
		return plugin.TaskSpec{
			Name: fmt.Sprintf("Check restic repo %s", repo),
			Steps: []plugin.TaskStep{
				{
					Name:   fmt.Sprintf("Restic check %s", repo),
					Module: "ansible.builtin.command",
					Params: map[string]any{
						"cmd":    cmd,
						"become": true,
					},
				},
			},
		}, nil

	default:
		return plugin.TaskSpec{}, fmt.Errorf("unsupported restic action: %s", action)
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
	if strings.EqualFold(action, "snapshot") {
		action = "backup"
	}
	if action == "" && req.Type == newapi.TaskTypeBackupSnapshot {
		if _, hasPath := params["path"]; hasPath {
			action = "backup"
		} else {
			action = "check"
		}
	}
	if action == "" {
		return plugin.TaskSpec{}, fmt.Errorf("action is required for plugin %s", PluginID)
	}

	// Normalize TaskTypeBackupSnapshot "repository" parameter into "repo" when needed.
	if repo, ok := params["repository"].(string); ok && strings.TrimSpace(repo) != "" {
		if _, hasRepo := params["repo"]; !hasRepo {
			params["repo"] = strings.TrimSpace(repo)
		}
	}

	return p.Compile(h, action, params)
}
