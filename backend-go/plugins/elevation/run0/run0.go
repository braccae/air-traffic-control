package run0

import (
	"context"
	"fmt"
	"strings"

	"air-traffic-control/backend/pkg/plugin"
	"air-traffic-control/backend/pkg/plugin/newapi"
	"air-traffic-control/backend/pkg/plugin/settingsframework"
)

const (
	// PluginID is the canonical identifier for the run0 elevation plugin.
	PluginID = "elevation/run0"
	// SettingsPageID is the settings page identifier for datacenter run0 defaults.
	SettingsPageID = "elevation/run0"
)

// Settings field keys for the run0 datacenter settings page.
const (
	FieldDefaultTargetUser  = "defaultTargetUser"
	FieldBackgroundMode     = "backgroundMode"
	FieldPreserveEnv        = "preserveEnvironment"
	FieldPolkitActionPrefix = "polkitActionPrefix"
	FieldBecomeFlags        = "becomeFlags"
	FieldPolkitAuthVaultVar = "polkitAuthVaultVar"
)

var (
	_ plugin.Plugin                      = (*Plugin)(nil)
	_ plugin.Actor                       = (*Plugin)(nil)
	_ plugin.SettingsPageProvider        = (*Plugin)(nil)
	_ newapi.ManagementPlugin            = (*Plugin)(nil)
	_ newapi.CategorizedSettingsProvider = (*Plugin)(nil)
	_ newapi.ContextMenuProvider         = (*Plugin)(nil)
	_ newapi.TaskCompileProvider         = (*Plugin)(nil)
)

func init() {
	_ = plugin.Register(New())
}

// Plugin implements plugin.Plugin, plugin.Actor, and v2 extension contracts
// for systemd 256+ run0 polkit privilege elevation.
type Plugin struct{}

// New returns a new run0 elevation plugin.
func New() *Plugin {
	return &Plugin{}
}

// Meta returns metadata for run0.
func (p *Plugin) Meta() plugin.Meta {
	return plugin.Meta{
		ID:          PluginID,
		Kind:        plugin.KindElevation,
		Name:        "run0",
		Label:       "run0 (systemd polkit)",
		Icon:        "verified_user",
		Description: "Modern systemd transient polkit session (preferred elevation)",
		Order:       10,
	}
}

// Compatible checks if host is Linux and either has no active elevation override
// or explicitly selects elevation/run0.
func (p *Plugin) Compatible(h plugin.HostFacts) bool {
	if h.OSFamily != "" && h.OSFamily != "linux" {
		return false
	}
	if h.ActiveElevation != "" && h.ActiveElevation != PluginID {
		return false
	}
	return true
}

// Requirements returns required dependencies.
func (p *Plugin) Requirements() plugin.Requirements {
	return plugin.Requirements{
		HostPackages: map[string][]string{
			"dnf": {"systemd"},
			"apt": {"systemd"},
		},
		ControllerCollections: []string{
			"ansible.builtin",
		},
	}
}

// CategorizedSettingsPage declares datacenter-wide run0 elevation defaults.
func (p *Plugin) CategorizedSettingsPage() newapi.CategorizedSettingsPage {
	meta := p.Meta()
	return settingsframework.NewPageBuilder(SettingsPageID, "run0 (systemd polkit) Defaults").
		WithIcon(meta.Icon).
		WithCategory(string(meta.Kind)).
		WithScope(newapi.SettingsScopeDatacenter).
		WithOrder(meta.Order).
		WithDescription("Datacenter-wide systemd 256+ run0 polkit session defaults, background tinting, and Ansible become flags.").
		StringField(
			FieldDefaultTargetUser,
			"Default Target User",
			"root",
			settingsframework.WithRequired(true),
			settingsframework.WithPlaceholder("root"),
			settingsframework.WithDescription("Default target user account for run0 elevated transient service units."),
		).
		SelectField(
			FieldBackgroundMode,
			"Terminal Background Tinting",
			"no",
			[]plugin.SettingsFieldOption{
				{Value: "no", Label: "Disabled (--background=no)"},
				{Value: "41", Label: "Red Alert Tint (ANSI 41)"},
				{Value: "44", Label: "Blue Tint (ANSI 44)"},
			},
			settingsframework.WithDescription("Controls run0 --background terminal tinting during non-interactive and interactive sessions."),
		).
		BoolField(
			FieldPreserveEnv,
			"Preserve DBus & Session Environment",
			false,
			settingsframework.WithDescription("Forward safe session environment variables into the transient systemd unit."),
		).
		StringField(
			FieldPolkitActionPrefix,
			"Polkit Action ID",
			"org.freedesktop.systemd1.manage-units",
			settingsframework.WithPlaceholder("org.freedesktop.systemd1.manage-units"),
			settingsframework.WithDescription("Polkit action identifier evaluated for passwordless run0 authorization."),
		).
		StringField(
			FieldBecomeFlags,
			"Default Ansible Become Flags",
			"--background=no",
			settingsframework.WithPlaceholder("--background=no"),
			settingsframework.WithDescription("Default extra flags passed to run0 when invoked as an Ansible become method."),
		).
		SecretField(
			FieldPolkitAuthVaultVar,
			"Polkit Become Password Vault Variable",
			settingsframework.WithPlaceholder("vault_run0_become_pass"),
			settingsframework.WithDescription("Optional Ansible Vault variable name holding the become password when polkit prompts. Never stored or returned in plaintext."),
		).
		BuildCategorized()
}

// SettingsPage returns the underlying plugin.SettingsPage for legacy settings consumers.
func (p *Plugin) SettingsPage() plugin.SettingsPage {
	return p.CategorizedSettingsPage().SettingsPage
}

// DefaultTargetUser returns the persisted datacenter default target user for run0 or "root".
func DefaultTargetUser() string {
	return settingsframework.NewStoreAdapter(nil).StringOrDefault(SettingsPageID, FieldDefaultTargetUser, "root")
}

// DefaultBackgroundMode returns the persisted run0 --background mode or "no".
func DefaultBackgroundMode() string {
	return settingsframework.NewStoreAdapter(nil).StringOrDefault(SettingsPageID, FieldBackgroundMode, "no")
}

// Probe checks if run0 is present and functional on the host.
func (p *Plugin) Probe(ctx context.Context, r plugin.Runner, h plugin.HostFacts) (plugin.Detection, error) {
	if r == nil {
		return plugin.Detection{
			Detected: false,
			Status:   "OFFLINE",
			Error:    "runner is nil",
		}, nil
	}

	cmd := "which run0 >/dev/null 2>&1 && run0 --background=no id -u"
	out, err := r.RunCommand(ctx, cmd)
	if err != nil || strings.TrimSpace(out) != "0" {
		return plugin.Detection{
			Detected: false,
			Status:   "UNAVAILABLE",
			Error:    fmt.Sprintf("run0 elevation check failed: %v", err),
		}, nil
	}

	return plugin.Detection{
		Detected: true,
		Status:   "ONLINE",
		Details:  "systemd 256+ run0 polkit elevation active",
	}, nil
}

// ManagementActions returns structured v2 action specifications for run0 privilege elevation.
func (p *Plugin) ManagementActions() []newapi.ManagementActionSpec {
	return []newapi.ManagementActionSpec{
		{
			ActionSpec: plugin.ActionSpec{
				Name:        "elevate",
				Label:       "Execute Elevated Command",
				Description: "Executes an administrative command via run0",
				Options: []plugin.OptionSchema{
					{
						Name:        "command",
						Type:        "string",
						Required:    true,
						Description: "Shell command to run with elevated privileges",
					},
				},
			},
			Category:         newapi.ActionCategorySecurity,
			Idempotent:       false,
			Dangerous:        true,
			ConfirmPrompt:    "Execute this administrative command with root privileges via run0?",
			InvalidatesKinds: []plugin.Kind{plugin.KindElevation},
		},
	}
}

// Actions lists supported mutation actions derived from ManagementActions.
func (p *Plugin) Actions() []plugin.ActionSpec {
	mgmt := p.ManagementActions()
	out := make([]plugin.ActionSpec, len(mgmt))
	for i, m := range mgmt {
		out[i] = m.ActionSpec
	}
	return out
}

// ValidateAction validates options for a run0 action against its schema.
func (p *Plugin) ValidateAction(h plugin.HostFacts, action string, opts map[string]any) error {
	norm := strings.TrimSpace(action)
	for _, spec := range p.ManagementActions() {
		if strings.EqualFold(spec.Name, norm) {
			return newapi.ValidateManagementOptions(spec, opts)
		}
	}
	return fmt.Errorf("unsupported run0 action: %s", action)
}

// ContextMenuEntries provides node-level run0 elevation context menu actions for compatible hosts.
func (p *Plugin) ContextMenuEntries(ctx context.Context, h plugin.HostFacts) []newapi.ContextMenuEntry {
	if !p.Compatible(h) {
		return nil
	}
	return newapi.NewContextMenuBuilder(PluginID).
		WithTarget(newapi.TargetNode).
		WithGroup(string(plugin.KindElevation)).
		AddDangerAction(
			"run0-elevate",
			"Execute Elevated Command (run0)",
			"verified_user",
			"elevate",
			"Execute this administrative command with root privileges via run0?",
			10,
		).
		Build()
}

// CompileTask implements newapi.TaskCompileProvider to compile typed TaskRequests into Ansible TaskSpecs.
func (p *Plugin) CompileTask(ctx context.Context, h plugin.HostFacts, req newapi.TaskRequest) (plugin.TaskSpec, error) {
	action := strings.TrimSpace(req.Action)
	if action == "" && req.Params != nil {
		if a, ok := req.Params["action"].(string); ok {
			action = strings.TrimSpace(a)
		}
	}
	if action == "" {
		return plugin.TaskSpec{}, fmt.Errorf("action is required for plugin %s", PluginID)
	}
	return p.Compile(h, action, req.Params)
}

// Compile generates Ansible task specification with run0 become method.
func (p *Plugin) Compile(h plugin.HostFacts, action string, opts map[string]any) (plugin.TaskSpec, error) {
	norm := strings.ToLower(strings.TrimSpace(action))
	if norm != "" && norm != "elevate" {
		return plugin.TaskSpec{}, fmt.Errorf("unsupported run0 action: %s", action)
	}

	var cmd string
	if opts != nil {
		cmd, _ = opts["command"].(string)
	}
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		cmd = "id -u"
	}

	return plugin.TaskSpec{
		Name: fmt.Sprintf("Run elevated command via run0: %s", cmd),
		Steps: []plugin.TaskStep{
			{
				Name:   fmt.Sprintf("Execute: %s", cmd),
				Module: "ansible.builtin.command",
				Params: map[string]any{
					"cmd":           cmd,
					"become":        true,
					"become_method": "run0",
				},
			},
		},
	}, nil
}
