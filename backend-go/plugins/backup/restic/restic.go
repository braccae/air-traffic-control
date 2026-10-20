package restic

import (
	"context"
	"fmt"
	"strings"

	"air-traffic-control/backend/pkg/plugin"
	pkgrestic "air-traffic-control/backend/pkg/restic"
	"air-traffic-control/backend/pkg/ssh"
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
		ID:          "backup/restic",
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

// SettingsPageID is the route slug of the datacenter-wide Restic settings page
// contributed by this module. It is also the key under which the framework
// persists the page's values.
const SettingsPageID = "backup/restic"

// DefaultRepoPath is the fallback repository used when neither a per-host
// override nor a persisted global default is configured.
const DefaultRepoPath = "/var/backups/restic"

// SettingsPage implements plugin.SettingsPageProvider so the module appears in
// the Datacenter Settings sidebar with no frontend work of its own.
func (p *Plugin) SettingsPage() plugin.SettingsPage {
	return plugin.SettingsPage{
		ID:          SettingsPageID,
		Title:       "Restic Backup Defaults",
		Icon:        "cloud_sync",
		Description: "Datacenter-wide repository and retention defaults applied to every node unless a per-host override is configured.",
		Order:       20,
		Fields: []plugin.SettingsField{
			{
				Name:        "repoPath",
				Label:       "Default Repository",
				Type:        plugin.SettingsFieldString,
				Default:     DefaultRepoPath,
				Required:    true,
				Placeholder: "/var/backups/restic or s3:https://...",
				Description: "Restic repository path or URL used when a node has no per-host override.",
			},
			{
				Name:        "retentionDaily",
				Label:       "Daily Snapshots Kept",
				Type:        plugin.SettingsFieldInt,
				Default:     7,
				Min:         intPtr(0),
				Max:         intPtr(365),
				Description: "Snapshots retained by the daily forget policy.",
			},
			{
				Name:        "retentionWeekly",
				Label:       "Weekly Snapshots Kept",
				Type:        plugin.SettingsFieldInt,
				Default:     4,
				Min:         intPtr(0),
				Max:         intPtr(104),
				Description: "Snapshots retained by the weekly forget policy.",
			},
			{
				Name:        "retentionMonthly",
				Label:       "Monthly Snapshots Kept",
				Type:        plugin.SettingsFieldInt,
				Default:     12,
				Min:         intPtr(0),
				Max:         intPtr(120),
				Description: "Snapshots retained by the monthly forget policy.",
			},
			{
				Name:        "passwordVaultVar",
				Label:       "Repository Password Vault Variable",
				Type:        plugin.SettingsFieldSecret,
				Secret:      true,
				Placeholder: "vault_restic_password",
				Description: "Ansible Vault variable name holding the repository password. The password itself is never stored here.",
			},
		},
	}
}

func intPtr(v int) *int { return &v }

// globalDefaultRepo returns the persisted datacenter-wide repository default, or
// an empty string when none has been configured.
func globalDefaultRepo() string {
	store := plugin.GlobalSettingsStore()
	if store == nil {
		return ""
	}
	if v, ok := store.Value(SettingsPageID, "repoPath"); ok {
		return strings.TrimSpace(v)
	}
	return ""
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

func (p *Plugin) Actions() []plugin.ActionSpec {
	return []plugin.ActionSpec{
		{
			Name:        "backup",
			Label:       "Create Backup",
			Description: "Run restic backup of targeted directories",
			Options: []plugin.OptionSchema{
				{Name: "path", Type: "string", Required: true, Description: "Directory path to back up"},
				{Name: "repo", Type: "string", Required: false, Description: "Restic repository path"},
				{Name: "tags", Type: "string", Required: false, Description: "Comma-separated tags"},
			},
		},
		{
			Name:        "check",
			Label:       "Verify Integrity",
			Description: "Verify the structural integrity and consistency of the repository",
			Options: []plugin.OptionSchema{
				{Name: "repo", Type: "string", Required: false, Description: "Restic repository path"},
			},
		},
	}
}

func (p *Plugin) Compile(h plugin.HostFacts, action string, opts map[string]any) (plugin.TaskSpec, error) {
	norm := strings.ToLower(action)
	switch norm {
	case "backup":
		path, _ := opts["path"].(string)
		if path == "" {
			return plugin.TaskSpec{}, fmt.Errorf("backup path is required")
		}
		repo, _ := opts["repo"].(string)
		if repo == "" {
			repo = "/var/backups/restic"
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
			repo = "/var/backups/restic"
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
