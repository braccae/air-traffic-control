package sudo

import (
	"context"
	"fmt"
	"strings"

	"air-traffic-control/backend/pkg/plugin"
	"air-traffic-control/backend/pkg/plugin/newapi"
	"air-traffic-control/backend/pkg/plugin/settingsframework"
)

const (
	// PluginID is the canonical identifier for the sudo elevation plugin.
	PluginID = "elevation/sudo"
	// SettingsPageID is the settings page identifier for datacenter sudo defaults.
	SettingsPageID = "elevation/sudo"
)

// Settings field keys for the sudo datacenter settings page.
const (
	FieldDefaultTargetUser    = "defaultTargetUser"
	FieldNonInteractive       = "nonInteractive"
	FieldPreserveEnv          = "preserveEnvironment"
	FieldBecomeFlags          = "becomeFlags"
	FieldSudoersDropInDir     = "sudoersDropInDir"
	FieldBecomePasswordSecret = "becomePasswordVaultVar"
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
// for standard sudo privilege escalation.
type Plugin struct{}

// New returns a new sudo elevation plugin.
func New() *Plugin {
	return &Plugin{}
}

// Meta returns metadata for sudo.
func (p *Plugin) Meta() plugin.Meta {
	return plugin.Meta{
		ID:          PluginID,
		Kind:        plugin.KindElevation,
		Name:        "sudo",
		Label:       "sudo (passwordless)",
		Icon:        "shield",
		Description: "Standard sudo privilege escalation with passwordless fallback",
		Order:       20,
	}
}

// Compatible checks if host is Linux, BSD, or Darwin and either has no active
// elevation override or explicitly selects elevation/sudo.
func (p *Plugin) Compatible(h plugin.HostFacts) bool {
	if h.OSFamily == "windows" {
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
			"dnf": {"sudo"},
			"apt": {"sudo"},
			"apk": {"sudo"},
		},
		ControllerCollections: []string{
			"ansible.builtin",
		},
	}
}

// CategorizedSettingsPage declares datacenter-wide sudo elevation defaults.
func (p *Plugin) CategorizedSettingsPage() newapi.CategorizedSettingsPage {
	meta := p.Meta()
	return settingsframework.NewPageBuilder(SettingsPageID, "sudo Elevation Defaults").
		WithIcon(meta.Icon).
		WithCategory(string(meta.Kind)).
		WithScope(newapi.SettingsScopeDatacenter).
		WithOrder(meta.Order).
		WithDescription("Datacenter-wide sudo privilege escalation defaults, non-interactive behavior, and Ansible become flags.").
		StringField(
			FieldDefaultTargetUser,
			"Default Target User",
			"root",
			settingsframework.WithRequired(true),
			settingsframework.WithPlaceholder("root"),
			settingsframework.WithDescription("Default target user account for sudo privilege escalation."),
		).
		BoolField(
			FieldNonInteractive,
			"Enforce Non-Interactive Mode (sudo -n)",
			true,
			settingsframework.WithDescription("Pass -n (--non-interactive) to prevent sudo from hanging on interactive password prompts."),
		).
		BoolField(
			FieldPreserveEnv,
			"Preserve User Environment (sudo -E)",
			false,
			settingsframework.WithDescription("Preserve existing environment variables when executing commands via sudo."),
		).
		StringField(
			FieldBecomeFlags,
			"Default Ansible Become Flags",
			"-H -S -n",
			settingsframework.WithPlaceholder("-H -S -n"),
			settingsframework.WithDescription("Default extra flags passed to sudo when invoked as an Ansible become method."),
		).
		StringField(
			FieldSudoersDropInDir,
			"Sudoers Drop-in Directory",
			"/etc/sudoers.d",
			settingsframework.WithPlaceholder("/etc/sudoers.d"),
			settingsframework.WithDescription("Directory path on managed hosts for managed sudoers drop-in rules."),
		).
		SecretField(
			FieldBecomePasswordSecret,
			"Sudo Become Password Vault Variable",
			settingsframework.WithPlaceholder("vault_sudo_become_pass"),
			settingsframework.WithDescription("Optional Ansible Vault variable name holding the sudo become password when NOPASSWD is not enabled. Never stored or returned in plaintext."),
		).
		BuildCategorized()
}

// SettingsPage returns the underlying plugin.SettingsPage for legacy settings consumers.
func (p *Plugin) SettingsPage() plugin.SettingsPage {
	return p.CategorizedSettingsPage().SettingsPage
}

// DefaultTargetUser returns the persisted datacenter default target user for sudo or "root".
func DefaultTargetUser() string {
	return settingsframework.NewStoreAdapter(nil).StringOrDefault(SettingsPageID, FieldDefaultTargetUser, "root")
}

// DefaultNonInteractive returns whether sudo non-interactive mode (-n) is enabled by default.
func DefaultNonInteractive() bool {
	return settingsframework.NewStoreAdapter(nil).BoolOrDefault(SettingsPageID, FieldNonInteractive, true)
}

// Probe checks if sudo -n is functional on the host.
func (p *Plugin) Probe(ctx context.Context, r plugin.Runner, h plugin.HostFacts) (plugin.Detection, error) {
	if r == nil {
		return plugin.Detection{
			Detected: false,
			Status:   "OFFLINE",
			Error:    "runner is nil",
		}, nil
	}

	cmd := "sudo -n id -u"
	out, err := r.RunCommand(ctx, cmd)
	if err != nil || strings.TrimSpace(out) != "0" {
		return plugin.Detection{
			Detected: false,
			Status:   "UNAVAILABLE",
			Error:    fmt.Sprintf("sudo check failed: %v", err),
		}, nil
	}

	return plugin.Detection{
		Detected: true,
		Status:   "ONLINE",
		Details:  "sudo passwordless elevation active",
	}, nil
}

// ManagementActions returns structured v2 action specifications for sudo privilege elevation.
func (p *Plugin) ManagementActions() []newapi.ManagementActionSpec {
	return []newapi.ManagementActionSpec{
		{
			ActionSpec: plugin.ActionSpec{
				Name:        "elevate",
				Label:       "Execute Elevated Command",
				Description: "Executes an administrative command via sudo",
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
			ConfirmPrompt:    "Execute this administrative command with root privileges via sudo?",
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

// ValidateAction validates options for a sudo action against its schema.
func (p *Plugin) ValidateAction(h plugin.HostFacts, action string, opts map[string]any) error {
	norm := strings.TrimSpace(action)
	for _, spec := range p.ManagementActions() {
		if strings.EqualFold(spec.Name, norm) {
			return newapi.ValidateManagementOptions(spec, opts)
		}
	}
	return fmt.Errorf("unsupported sudo action: %s", action)
}

// ContextMenuEntries provides node-level sudo elevation context menu actions for compatible hosts.
func (p *Plugin) ContextMenuEntries(ctx context.Context, h plugin.HostFacts) []newapi.ContextMenuEntry {
	if !p.Compatible(h) {
		return nil
	}
	return newapi.NewContextMenuBuilder(PluginID).
		WithTarget(newapi.TargetNode).
		WithGroup(string(plugin.KindElevation)).
		AddDangerAction(
			"sudo-elevate",
			"Execute Elevated Command (sudo)",
			"shield",
			"elevate",
			"Execute this administrative command with root privileges via sudo?",
			20,
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

// Compile generates Ansible task specification with sudo become method.
func (p *Plugin) Compile(h plugin.HostFacts, action string, opts map[string]any) (plugin.TaskSpec, error) {
	norm := strings.ToLower(strings.TrimSpace(action))
	if norm != "" && norm != "elevate" {
		return plugin.TaskSpec{}, fmt.Errorf("unsupported sudo action: %s", action)
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
		Name: fmt.Sprintf("Run elevated command via sudo: %s", cmd),
		Steps: []plugin.TaskStep{
			{
				Name:   fmt.Sprintf("Execute: %s", cmd),
				Module: "ansible.builtin.command",
				Params: map[string]any{
					"cmd":           cmd,
					"become":        true,
					"become_method": "sudo",
				},
			},
		},
	}, nil
}
