package none

import (
	"context"
	"fmt"
	"strings"

	"air-traffic-control/backend/pkg/plugin"
	"air-traffic-control/backend/pkg/plugin/newapi"
	"air-traffic-control/backend/pkg/plugin/settingsframework"
)

const (
	// PluginID is the canonical identifier for the direct/none elevation plugin.
	PluginID = "elevation/none"
	// SettingsPageID is the settings page identifier for datacenter direct execution defaults.
	SettingsPageID = "elevation/none"
)

// Settings field keys for the direct/none datacenter settings page.
const (
	FieldDefaultCommand     = "defaultCommand"
	FieldAllowRootDirect    = "allowRootDirect"
	FieldDefaultWorkingDir  = "defaultWorkingDir"
	FieldCommandTimeoutSecs = "commandTimeoutSeconds"
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
// for direct/root or unprivileged execution without privilege escalation.
type Plugin struct{}

// New returns a new direct/none elevation plugin.
func New() *Plugin {
	return &Plugin{}
}

// Meta returns metadata for none.
func (p *Plugin) Meta() plugin.Meta {
	return plugin.Meta{
		ID:          PluginID,
		Kind:        plugin.KindElevation,
		Name:        "none",
		Label:       "none (direct execution)",
		Icon:        "person_check",
		Description: "Direct command execution for root user or unprivileged tasks",
		Order:       40,
	}
}

// Compatible checks if host is compatible.
func (p *Plugin) Compatible(h plugin.HostFacts) bool {
	if h.ActiveElevation != "" && h.ActiveElevation != PluginID {
		return false
	}
	return true
}

// Requirements returns required dependencies.
func (p *Plugin) Requirements() plugin.Requirements {
	return plugin.Requirements{
		ControllerCollections: []string{
			"ansible.builtin",
		},
	}
}

// CategorizedSettingsPage declares datacenter-wide direct execution defaults.
func (p *Plugin) CategorizedSettingsPage() newapi.CategorizedSettingsPage {
	meta := p.Meta()
	return settingsframework.NewPageBuilder(SettingsPageID, "Direct Execution (none) Defaults").
		WithIcon(meta.Icon).
		WithCategory(string(meta.Kind)).
		WithScope(newapi.SettingsScopeDatacenter).
		WithOrder(meta.Order).
		WithDescription("Datacenter-wide defaults for direct unprivileged or root command execution without privilege elevation.").
		StringField(
			FieldDefaultCommand,
			"Default Probe / Fallback Command",
			"id -u",
			settingsframework.WithRequired(true),
			settingsframework.WithPlaceholder("id -u"),
			settingsframework.WithDescription("Default fallback command executed when no explicit command is supplied."),
		).
		BoolField(
			FieldAllowRootDirect,
			"Allow Direct Root Sessions",
			true,
			settingsframework.WithDescription("Permit direct command execution when connected as UID 0 without become wrappers."),
		).
		StringField(
			FieldDefaultWorkingDir,
			"Default Working Directory",
			"/tmp",
			settingsframework.WithPlaceholder("/tmp"),
			settingsframework.WithDescription("Default working directory for direct unprivileged command execution."),
		).
		IntField(
			FieldCommandTimeoutSecs,
			"Default Command Timeout (Seconds)",
			60,
			settingsframework.WithMinMax(5, 3600),
			settingsframework.WithDescription("Maximum execution timeout in seconds for direct commands."),
		).
		BuildCategorized()
}

// SettingsPage returns the underlying plugin.SettingsPage for legacy settings consumers.
func (p *Plugin) SettingsPage() plugin.SettingsPage {
	return p.CategorizedSettingsPage().SettingsPage
}

// DefaultCommand returns the persisted datacenter fallback command or "id -u".
func DefaultCommand() string {
	return settingsframework.NewStoreAdapter(nil).StringOrDefault(SettingsPageID, FieldDefaultCommand, "id -u")
}

// DefaultWorkingDir returns the persisted datacenter working directory or "/tmp".
func DefaultWorkingDir() string {
	return settingsframework.NewStoreAdapter(nil).StringOrDefault(SettingsPageID, FieldDefaultWorkingDir, "/tmp")
}

// Probe checks if current user is root or unprivileged direct execution is supported.
func (p *Plugin) Probe(ctx context.Context, r plugin.Runner, h plugin.HostFacts) (plugin.Detection, error) {
	if r == nil {
		return plugin.Detection{
			Detected: false,
			Status:   "OFFLINE",
			Error:    "runner is nil",
		}, nil
	}

	cmd := "id -u"
	out, err := r.RunCommand(ctx, cmd)
	if err != nil {
		return plugin.Detection{
			Detected: false,
			Status:   "UNAVAILABLE",
			Error:    fmt.Sprintf("id check failed: %v", err),
		}, nil
	}

	uid := strings.TrimSpace(out)
	status := "UNPRIVILEGED"
	if uid == "0" {
		status = "ONLINE"
	}

	return plugin.Detection{
		Detected: true,
		Status:   status,
		Details:  fmt.Sprintf("direct execution (uid=%s)", uid),
	}, nil
}

// ManagementActions returns structured v2 action specifications for direct execution.
func (p *Plugin) ManagementActions() []newapi.ManagementActionSpec {
	return []newapi.ManagementActionSpec{
		{
			ActionSpec: plugin.ActionSpec{
				Name:        "elevate",
				Label:       "Execute Command Directly",
				Description: "Executes a command directly without elevation",
				Options: []plugin.OptionSchema{
					{
						Name:        "command",
						Type:        "string",
						Required:    true,
						Description: "Shell command to run directly",
					},
				},
			},
			Category:         newapi.ActionCategorySecurity,
			Idempotent:       false,
			Dangerous:        false,
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

// ValidateAction validates options for a direct execution action against its schema.
func (p *Plugin) ValidateAction(h plugin.HostFacts, action string, opts map[string]any) error {
	norm := strings.TrimSpace(action)
	for _, spec := range p.ManagementActions() {
		if strings.EqualFold(spec.Name, norm) {
			return newapi.ValidateManagementOptions(spec, opts)
		}
	}
	return fmt.Errorf("unsupported none action: %s", action)
}

// ContextMenuEntries provides node-level direct execution context menu actions for compatible hosts.
func (p *Plugin) ContextMenuEntries(ctx context.Context, h plugin.HostFacts) []newapi.ContextMenuEntry {
	if !p.Compatible(h) {
		return nil
	}
	return newapi.NewContextMenuBuilder(PluginID).
		WithTarget(newapi.TargetNode).
		WithGroup(string(plugin.KindElevation)).
		AddAction(
			"none-elevate",
			"Execute Command Directly (none)",
			"person_check",
			"elevate",
			40,
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

// Compile generates Ansible task specification with none become method.
func (p *Plugin) Compile(h plugin.HostFacts, action string, opts map[string]any) (plugin.TaskSpec, error) {
	norm := strings.ToLower(strings.TrimSpace(action))
	if norm != "" && norm != "elevate" {
		return plugin.TaskSpec{}, fmt.Errorf("unsupported none action: %s", action)
	}

	var cmd string
	if opts != nil {
		cmd, _ = opts["command"].(string)
	}
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		cmd = DefaultCommand()
	}

	return plugin.TaskSpec{
		Name: fmt.Sprintf("Run command directly: %s", cmd),
		Steps: []plugin.TaskStep{
			{
				Name:   fmt.Sprintf("Execute: %s", cmd),
				Module: "ansible.builtin.command",
				Params: map[string]any{
					"cmd":           cmd,
					"become":        false,
					"become_method": "none",
				},
			},
		},
	}, nil
}
