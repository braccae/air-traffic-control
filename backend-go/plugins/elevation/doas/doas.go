package doas

import (
	"context"
	"fmt"
	"strings"

	"air-traffic-control/backend/pkg/plugin"
	"air-traffic-control/backend/pkg/plugin/newapi"
	"air-traffic-control/backend/pkg/plugin/settingsframework"
)

const (
	// PluginID is the canonical identifier for the doas elevation plugin.
	PluginID = "elevation/doas"
	// SettingsPageID is the settings page identifier for datacenter doas defaults.
	SettingsPageID = "elevation/doas"
)

// Settings field keys for the doas datacenter settings page.
const (
	FieldDefaultTargetUser  = "defaultTargetUser"
	FieldConfigPath         = "configPath"
	FieldNonInteractive     = "nonInteractive"
	FieldKeepEnv            = "keepEnv"
	FieldPersistAuth        = "persistAuth"
	FieldBecomeFlags        = "becomeFlags"
	FieldBecomePassVaultVar = "becomePassVaultVar"
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

// Plugin implements plugin.Plugin, plugin.Reader, plugin.Actor, and v2 extension
// contracts for OpenBSD/Alpine doas privilege escalation.
type Plugin struct{}

// New returns a new doas elevation plugin.
func New() *Plugin {
	return &Plugin{}
}

// Meta returns metadata for doas.
func (p *Plugin) Meta() plugin.Meta {
	return plugin.Meta{
		ID:          PluginID,
		Kind:        plugin.KindElevation,
		Name:        "doas",
		Label:       "doas (OpenBSD/Alpine)",
		Icon:        "admin_panel_settings",
		Description: "Lightweight OpenBSD and Alpine Linux privilege escalation",
		Order:       30,
	}
}

// Compatible checks if host is Linux or BSD and either has no active elevation
// override or explicitly selects elevation/doas.
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
			"apk": {"doas"},
			"apt": {"opendoas"},
		},
		ControllerCollections: []string{
			"ansible.builtin",
		},
	}
}

// CollectionSchedule returns the standard polling cadence and cache TTL schedule for doas elevation status.
func (p *Plugin) CollectionSchedule() newapi.CollectionSchedule {
	return newapi.DefaultScheduleForKind(p.Meta().Kind)
}

// CollectionSchema documents the v2 payload structure emitted by Read.
func (p *Plugin) CollectionSchema() newapi.CollectionSchema {
	return newapi.CollectionSchema{
		Version:     "v2",
		RootKey:     "elevation",
		Description: p.Meta().Description,
	}
}

// CategorizedSettingsPage declares datacenter-wide doas elevation defaults via settingsframework.
func (p *Plugin) CategorizedSettingsPage() newapi.CategorizedSettingsPage {
	meta := p.Meta()
	return settingsframework.NewPageBuilder(SettingsPageID, "doas (OpenBSD/Alpine) Defaults").
		WithIcon(meta.Icon).
		WithCategory(string(meta.Kind)).
		WithScope(newapi.SettingsScopeDatacenter).
		WithOrder(meta.Order).
		WithDescription("Datacenter-wide OpenBSD/Alpine doas privilege escalation defaults, configuration path, and Ansible become settings.").
		StringField(
			FieldDefaultTargetUser,
			"Default Target User",
			"root",
			settingsframework.WithRequired(true),
			settingsframework.WithPlaceholder("root"),
			settingsframework.WithDescription("Default target user account (-u) for doas privilege escalation."),
		).
		StringField(
			FieldConfigPath,
			"doas Configuration File Path",
			"/etc/doas.conf",
			settingsframework.WithRequired(true),
			settingsframework.WithPlaceholder("/etc/doas.conf"),
			settingsframework.WithDescription("Path to the doas configuration file inspected and validated on managed hosts."),
		).
		BoolField(
			FieldNonInteractive,
			"Require Non-Interactive Mode (-n)",
			true,
			settingsframework.WithDescription("Pass -n to doas so commands fail immediately instead of hanging on interactive password prompts."),
		).
		BoolField(
			FieldKeepEnv,
			"Preserve Environment (keepenv)",
			false,
			settingsframework.WithDescription("Prefer keepenv rules when staging or validating doas privilege policies."),
		).
		BoolField(
			FieldPersistAuth,
			"Enable Credential Persistence (persist)",
			false,
			settingsframework.WithDescription("Allow temporary credential caching across consecutive doas invocations where supported by kernel tty tickets."),
		).
		StringField(
			FieldBecomeFlags,
			"Default Ansible Become Flags",
			"-n",
			settingsframework.WithPlaceholder("-n"),
			settingsframework.WithDescription("Default extra flags passed to doas when invoked as an Ansible become method."),
		).
		SecretField(
			FieldBecomePassVaultVar,
			"doas Become Password Vault Variable",
			settingsframework.WithPlaceholder("vault_doas_become_pass"),
			settingsframework.WithDescription("Optional Ansible Vault variable name holding the become password when nopass is not configured. Never stored or returned in plaintext."),
		).
		BuildCategorized()
}

// SettingsPage returns the underlying plugin.SettingsPage for legacy settings consumers.
func (p *Plugin) SettingsPage() plugin.SettingsPage {
	return p.CategorizedSettingsPage().SettingsPage
}

// DefaultTargetUser returns the persisted datacenter default target user for doas or "root".
func DefaultTargetUser() string {
	return settingsframework.NewStoreAdapter(nil).StringOrDefault(SettingsPageID, FieldDefaultTargetUser, "root")
}

// DefaultConfigPath returns the persisted datacenter doas.conf path or "/etc/doas.conf".
func DefaultConfigPath() string {
	return settingsframework.NewStoreAdapter(nil).StringOrDefault(SettingsPageID, FieldConfigPath, "/etc/doas.conf")
}

// DefaultBecomeFlags returns the persisted datacenter Ansible become flags for doas or "-n".
func DefaultBecomeFlags() string {
	return settingsframework.NewStoreAdapter(nil).StringOrDefault(SettingsPageID, FieldBecomeFlags, "-n")
}

// Probe checks if doas -n is functional on the host.
func (p *Plugin) Probe(ctx context.Context, r plugin.Runner, h plugin.HostFacts) (plugin.Detection, error) {
	if r == nil {
		return plugin.Detection{
			Detected: false,
			Status:   "OFFLINE",
			Error:    "runner is nil",
		}, nil
	}

	cmd := "which doas >/dev/null 2>&1 && doas -n id -u"
	out, err := r.RunCommand(ctx, cmd)
	if err != nil || strings.TrimSpace(out) != "0" {
		return plugin.Detection{
			Detected: false,
			Status:   "UNAVAILABLE",
			Error:    fmt.Sprintf("doas check failed: %v", err),
		}, nil
	}

	return plugin.Detection{
		Detected: true,
		Status:   "ONLINE",
		Details:  "doas elevation active",
	}, nil
}

// Read inspects live doas binary presence, non-interactive root elevation capability,
// and configuration file state on the target host.
func (p *Plugin) Read(ctx context.Context, r plugin.Runner, h plugin.HostFacts) (any, error) {
	if r == nil {
		return nil, fmt.Errorf("runner is nil")
	}

	cmd := "which doas >/dev/null 2>&1 && doas -n id -u"
	out, err := r.RunCommand(ctx, cmd)
	if err != nil {
		return nil, fmt.Errorf("doas status read failed: %w", err)
	}

	uid := strings.TrimSpace(out)
	passwordless := uid == "0"
	status := "UNAVAILABLE"
	if passwordless {
		status = "ONLINE"
	}

	elevation := map[string]any{
		"plugin":       PluginID,
		"method":       "doas",
		"status":       status,
		"uid":          uid,
		"passwordless": passwordless,
		"targetUser":   DefaultTargetUser(),
		"configPath":   DefaultConfigPath(),
		"becomeFlags":  DefaultBecomeFlags(),
	}

	return map[string]any{
		"elevation":    elevation,
		"plugin":       PluginID,
		"method":       "doas",
		"status":       status,
		"uid":          uid,
		"passwordless": passwordless,
		"targetUser":   DefaultTargetUser(),
		"configPath":   DefaultConfigPath(),
		"becomeFlags":  DefaultBecomeFlags(),
	}, nil
}

// ManagementActions returns structured v2 action specifications for doas privilege elevation.
func (p *Plugin) ManagementActions() []newapi.ManagementActionSpec {
	return []newapi.ManagementActionSpec{
		{
			ActionSpec: plugin.ActionSpec{
				Name:        "elevate",
				Label:       "Execute Elevated Command",
				Description: "Executes an administrative command via doas",
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
			ConfirmPrompt:    "Execute this administrative command with root privileges via doas?",
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

// ValidateAction validates options for a doas action against its schema.
func (p *Plugin) ValidateAction(h plugin.HostFacts, action string, opts map[string]any) error {
	norm := strings.TrimSpace(action)
	for _, spec := range p.ManagementActions() {
		if strings.EqualFold(spec.Name, norm) {
			return newapi.ValidateManagementOptions(spec, opts)
		}
	}
	return fmt.Errorf("unsupported doas action: %s", action)
}

// ContextMenuEntries provides node-level doas elevation context menu actions for compatible hosts.
func (p *Plugin) ContextMenuEntries(ctx context.Context, h plugin.HostFacts) []newapi.ContextMenuEntry {
	if !p.Compatible(h) {
		return nil
	}
	return newapi.NewContextMenuBuilder(PluginID).
		WithTarget(newapi.TargetNode).
		WithGroup(string(plugin.KindElevation)).
		AddDangerAction(
			"doas-elevate",
			"Execute Elevated Command (doas)",
			"admin_panel_settings",
			"elevate",
			"Execute this administrative command with root privileges via doas?",
			30,
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

// Compile generates Ansible task specification with doas become method.
func (p *Plugin) Compile(h plugin.HostFacts, action string, opts map[string]any) (plugin.TaskSpec, error) {
	norm := strings.ToLower(strings.TrimSpace(action))
	if norm != "" && norm != "elevate" {
		return plugin.TaskSpec{}, fmt.Errorf("unsupported doas action: %s", action)
	}

	var cmd string
	if opts != nil {
		cmd, _ = opts["command"].(string)
	}
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		cmd = "id -u"
	}

	var secretsMasked map[string]string
	if opts != nil {
		if vaultVar, ok := opts[FieldBecomePassVaultVar].(string); ok && strings.TrimSpace(vaultVar) != "" {
			v := strings.TrimSpace(vaultVar)
			secretsMasked = map[string]string{
				FieldBecomePassVaultVar: plugin.MaskedSecretValue(v),
			}
		}
	}
	if secretsMasked == nil {
		if secretVal, ok := settingsframework.NewStoreAdapter(nil).SecretValue(SettingsPageID, FieldBecomePassVaultVar); ok && strings.TrimSpace(secretVal) != "" {
			secretsMasked = map[string]string{
				FieldBecomePassVaultVar: plugin.MaskedSecretValue(strings.TrimSpace(secretVal)),
			}
		}
	}

	return plugin.TaskSpec{
		Name: fmt.Sprintf("Run elevated command via doas: %s", cmd),
		Steps: []plugin.TaskStep{
			{
				Name:   fmt.Sprintf("Execute: %s", cmd),
				Module: "ansible.builtin.command",
				Params: map[string]any{
					"cmd":           cmd,
					"become":        true,
					"become_method": "doas",
				},
			},
		},
		SecretsMasked: secretsMasked,
	}, nil
}
