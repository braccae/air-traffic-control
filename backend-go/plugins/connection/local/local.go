package local

import (
	"context"
	"fmt"
	"net"
	"os/exec"
	"strings"
	"time"

	"air-traffic-control/backend/pkg/plugin"
	"air-traffic-control/backend/pkg/plugin/newapi"
	"air-traffic-control/backend/pkg/plugin/settingsframework"
)

const (
	// PluginID is the canonical identifier for the local connection plugin.
	PluginID = "connection/local"
	// SettingsPageID is the settings page identifier for datacenter local execution defaults.
	SettingsPageID = "connection/local"
)

// Settings field keys for the local connection datacenter settings page.
const (
	FieldDefaultShell         = "defaultShell"
	FieldCommandTimeoutSecs   = "commandTimeoutSeconds"
	FieldWorkingDir           = "workingDir"
	FieldAllowRemoteOverride  = "allowRemoteOverride"
	FieldEnvironmentVaultVar  = "environmentVaultVar"
)

// Default local execution settings values.
const (
	DefaultShell              = "sh"
	DefaultCommandTimeoutSecs = 30
	DefaultWorkingDir         = ""
)

var (
	_ plugin.Plugin                      = (*Plugin)(nil)
	_ plugin.Dialer                      = (*Plugin)(nil)
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

// Plugin implements plugin.Plugin, plugin.Dialer, and v2 extension contracts
// for local execution on the controller host.
type Plugin struct{}

// New returns a new local connection plugin.
func New() *Plugin {
	return &Plugin{}
}

// Meta returns metadata for the local plugin.
func (p *Plugin) Meta() plugin.Meta {
	return plugin.Meta{
		ID:          PluginID,
		Kind:        plugin.KindConnection,
		Name:        "local",
		Label:       "Local / Loopback",
		Icon:        "dns",
		Description: "Direct controller local execution via exec.CommandContext",
		Order:       1,
	}
}

// Compatible determines if the host facts target the local controller.
func (p *Plugin) Compatible(h plugin.HostFacts) bool {
	if h.ActiveConnection == PluginID {
		return true
	}
	if h.ActiveConnection != "" && h.ActiveConnection != PluginID {
		return false
	}
	route := strings.TrimSpace(h.HostRoute)
	return route == "127.0.0.1" || route == "localhost" || route == "::1" || route == ""
}

// Requirements returns dependencies for the local plugin.
func (p *Plugin) Requirements() plugin.Requirements {
	return plugin.Requirements{
		ControllerCollections: []string{
			"ansible.builtin",
		},
	}
}

// CollectionSchedule returns the polling and cache TTL schedule for local connection status.
func (p *Plugin) CollectionSchedule() newapi.CollectionSchedule {
	return newapi.DefaultScheduleForKind(p.Meta().Kind)
}

// CollectionSchema documents the v2 payload structure emitted by Read.
func (p *Plugin) CollectionSchema() newapi.CollectionSchema {
	return newapi.CollectionSchema{
		Version:     "v2",
		RootKey:     "connection",
		Description: p.Meta().Description,
	}
}

// CategorizedSettingsPage declares datacenter-wide local execution defaults via settingsframework.
func (p *Plugin) CategorizedSettingsPage() newapi.CategorizedSettingsPage {
	meta := p.Meta()
	return settingsframework.NewPageBuilder(SettingsPageID, "Local / Loopback Execution Defaults").
		WithIcon(meta.Icon).
		WithCategory(string(meta.Kind)).
		WithScope(newapi.SettingsScopeDatacenter).
		WithOrder(meta.Order).
		WithDescription("Datacenter-wide local shell, command timeout, working directory, and vault environment defaults for controller loopback execution.").
		SelectField(
			FieldDefaultShell,
			"Default Local Shell",
			DefaultShell,
			[]plugin.SettingsFieldOption{
				{Value: "sh", Label: "POSIX sh (/bin/sh)"},
				{Value: "bash", Label: "GNU Bash (/bin/bash)"},
				{Value: "zsh", Label: "Zsh (/bin/zsh)"},
			},
			settingsframework.WithDescription("Shell interpreter used by local runner invocations on the controller host."),
		).
		IntField(
			FieldCommandTimeoutSecs,
			"Default Command Timeout (Seconds)",
			DefaultCommandTimeoutSecs,
			settingsframework.WithMinMax(1, 3600),
			settingsframework.WithDescription("Default timeout in seconds applied to local command probes and tasks when no context deadline is set."),
		).
		StringField(
			FieldWorkingDir,
			"Default Working Directory",
			DefaultWorkingDir,
			settingsframework.WithPlaceholder("/var/lib/air-traffic-control"),
			settingsframework.WithDescription("Optional working directory for local shell execution on the controller."),
		).
		BoolField(
			FieldAllowRemoteOverride,
			"Allow Explicit Local Override on Non-Loopback Nodes",
			true,
			settingsframework.WithDescription("Permit nodes with LAN/Tailscale routes to explicitly select connection/local when running on the controller."),
		).
		SecretField(
			FieldEnvironmentVaultVar,
			"Controller Environment Secret Vault Variable",
			settingsframework.WithPlaceholder("vault_local_env_secret"),
			settingsframework.WithDescription("Optional Ansible Vault variable name holding sensitive environment credentials for local controller tasks. Never stored or returned in plaintext."),
		).
		BuildCategorized()
}

// SettingsPage returns the underlying plugin.SettingsPage for legacy settings consumers.
func (p *Plugin) SettingsPage() plugin.SettingsPage {
	return p.CategorizedSettingsPage().SettingsPage
}

// EffectiveShell resolves the configured local shell from the settings store or returns DefaultShell.
func EffectiveShell() string {
	sh := strings.TrimSpace(settingsframework.NewStoreAdapter(nil).StringOrDefault(SettingsPageID, FieldDefaultShell, DefaultShell))
	switch sh {
	case "sh", "bash", "zsh":
		return sh
	default:
		return DefaultShell
	}
}

// EffectiveTimeout resolves the configured default command timeout from the settings store.
func EffectiveTimeout() time.Duration {
	secs := settingsframework.NewStoreAdapter(nil).IntOrDefault(SettingsPageID, FieldCommandTimeoutSecs, DefaultCommandTimeoutSecs)
	if secs <= 0 {
		secs = DefaultCommandTimeoutSecs
	}
	return time.Duration(secs) * time.Second
}

// Runner executes commands locally on the host.
type Runner struct {
	Shell      string
	WorkingDir string
}

// RunCommand executes a shell command locally.
func (r *Runner) RunCommand(ctx context.Context, cmd string) (string, error) {
	shell := DefaultShell
	if r != nil && strings.TrimSpace(r.Shell) != "" {
		shell = strings.TrimSpace(r.Shell)
	}
	c := exec.CommandContext(ctx, shell, "-c", cmd)
	if r != nil && strings.TrimSpace(r.WorkingDir) != "" {
		c.Dir = strings.TrimSpace(r.WorkingDir)
	}
	out, err := c.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("local execution failed: %w (output: %s)", err, string(out))
	}
	return string(out), nil
}

// NewRunner returns a local command runner.
func (p *Plugin) NewRunner(h plugin.HostFacts) (plugin.Runner, error) {
	shell := EffectiveShell()
	workDir := strings.TrimSpace(settingsframework.NewStoreAdapter(nil).StringOrDefault(SettingsPageID, FieldWorkingDir, DefaultWorkingDir))
	if h.Overrides != nil {
		if s, ok := h.Overrides["shell"]; ok && strings.TrimSpace(s) != "" {
			shell = strings.TrimSpace(s)
		}
		if wd, ok := h.Overrides["working_dir"]; ok && strings.TrimSpace(wd) != "" {
			workDir = strings.TrimSpace(wd)
		}
	}
	return &Runner{
		Shell:      shell,
		WorkingDir: workDir,
	}, nil
}

// Dial satisfies plugin.Dialer, opening a local network connection using net.Dialer.
func (p *Plugin) Dial(ctx context.Context, network, address string) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, network, address)
}

// Probe checks if the local environment is operational.
func (p *Plugin) Probe(ctx context.Context, r plugin.Runner, h plugin.HostFacts) (plugin.Detection, error) {
	if r == nil {
		r = &Runner{}
	}
	out, err := r.RunCommand(ctx, "uname -s -r")
	if err != nil {
		return plugin.Detection{
			Detected: false,
			Status:   "ERROR",
			Error:    err.Error(),
		}, nil
	}
	return plugin.Detection{
		Detected: true,
		Status:   "ONLINE",
		Details:  strings.TrimSpace(out),
	}, nil
}

// Read inspects live local controller identity and loopback execution status.
func (p *Plugin) Read(ctx context.Context, r plugin.Runner, h plugin.HostFacts) (any, error) {
	if r == nil {
		var err error
		r, err = p.NewRunner(h)
		if err != nil {
			return nil, err
		}
	}

	out, err := r.RunCommand(ctx, "uname -s -n -r -m")
	if err != nil {
		return nil, fmt.Errorf("local connection read failed: %w", err)
	}

	route := strings.TrimSpace(h.HostRoute)
	if route == "" {
		route = "127.0.0.1"
	}

	return map[string]any{
		"connection": map[string]any{
			"plugin":    PluginID,
			"transport": "local",
			"hostRoute": route,
			"shell":     EffectiveShell(),
			"uname":     strings.TrimSpace(out),
			"status":    "ONLINE",
		},
		"plugin":    PluginID,
		"transport": "local",
		"hostRoute": route,
		"shell":     EffectiveShell(),
		"uname":     strings.TrimSpace(out),
		"status":    "ONLINE",
	}, nil
}

// ManagementActions returns structured v2 action specifications for local connection diagnostics and verification.
func (p *Plugin) ManagementActions() []newapi.ManagementActionSpec {
	return []newapi.ManagementActionSpec{
		{
			ActionSpec: plugin.ActionSpec{
				Name:        "ping",
				Label:       "Verify Local Loopback",
				Description: "Run an ansible.builtin.ping check against the local controller loopback connection",
				Options: []plugin.OptionSchema{
					{
						Name:        "data",
						Type:        "string",
						Required:    false,
						Default:     "pong",
						Description: "Optional data payload returned by ansible.builtin.ping",
					},
				},
			},
			Category:         newapi.ActionCategoryDiagnostic,
			Idempotent:       true,
			Dangerous:        false,
			InvalidatesKinds: []plugin.Kind{plugin.KindConnection},
		},
		{
			ActionSpec: plugin.ActionSpec{
				Name:        "gather_facts",
				Label:       "Gather Local Controller Facts",
				Description: "Collect local system facts via ansible.builtin.setup",
				Options: []plugin.OptionSchema{
					{
						Name:        "filter",
						Type:        "string",
						Required:    false,
						Default:     "*",
						Description: "Optional fact filter pattern passed to ansible.builtin.setup",
					},
				},
			},
			Category:         newapi.ActionCategoryDiagnostic,
			Idempotent:       true,
			Dangerous:        false,
			InvalidatesKinds: []plugin.Kind{plugin.KindConnection},
		},
	}
}

// Actions derives legacy plugin.ActionSpec definitions from ManagementActions().
func (p *Plugin) Actions() []plugin.ActionSpec {
	mgmt := p.ManagementActions()
	out := make([]plugin.ActionSpec, len(mgmt))
	for i, m := range mgmt {
		out[i] = m.ActionSpec
	}
	return out
}

// ValidateAction validates options for a local connection action against its schema.
func (p *Plugin) ValidateAction(h plugin.HostFacts, action string, opts map[string]any) error {
	for _, spec := range p.ManagementActions() {
		if strings.EqualFold(spec.Name, strings.TrimSpace(action)) {
			return newapi.ValidateManagementOptions(spec, opts)
		}
	}
	return fmt.Errorf("unsupported local connection action: %s", action)
}

// ContextMenuEntries provides node-level context menu actions for compatible local loopback hosts.
func (p *Plugin) ContextMenuEntries(ctx context.Context, h plugin.HostFacts) []newapi.ContextMenuEntry {
	if !p.Compatible(h) {
		return nil
	}
	return newapi.NewContextMenuBuilder(PluginID).
		WithTarget(newapi.TargetNode).
		WithGroup(string(plugin.KindConnection)).
		AddAction("local-ping", "Verify Local Loopback", "network_check", "ping", 10).
		AddAction("local-gather-facts", "Gather Local Controller Facts", "info", "gather_facts", 20).
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

// Compile compiles a local connection action into a declarative Ansible TaskSpec.
func (p *Plugin) Compile(h plugin.HostFacts, action string, opts map[string]any) (plugin.TaskSpec, error) {
	if err := p.ValidateAction(h, action, opts); err != nil {
		return plugin.TaskSpec{}, err
	}

	norm := strings.ToLower(strings.TrimSpace(action))
	var step plugin.TaskStep
	var specName string

	switch norm {
	case "ping":
		data := "pong"
		if opts != nil {
			if d, ok := opts["data"].(string); ok && strings.TrimSpace(d) != "" {
				data = strings.TrimSpace(d)
			}
		}
		specName = "Verify local loopback connectivity"
		step = plugin.TaskStep{
			Name:   "Ping local controller",
			Module: "ansible.builtin.ping",
			Params: map[string]any{
				"data":       data,
				"connection": "local",
			},
		}
	case "gather_facts":
		filter := "*"
		if opts != nil {
			if f, ok := opts["filter"].(string); ok && strings.TrimSpace(f) != "" {
				filter = strings.TrimSpace(f)
			}
		}
		specName = "Gather local controller facts"
		step = plugin.TaskStep{
			Name:   "Collect local facts via setup",
			Module: "ansible.builtin.setup",
			Params: map[string]any{
				"filter":     filter,
				"connection": "local",
			},
		}
	default:
		return plugin.TaskSpec{}, fmt.Errorf("unsupported local connection action: %s", action)
	}

	var secretsMasked map[string]string
	if opts != nil {
		if vaultVar, ok := opts[FieldEnvironmentVaultVar].(string); ok && strings.TrimSpace(vaultVar) != "" {
			v := strings.TrimSpace(vaultVar)
			secretsMasked = map[string]string{
				FieldEnvironmentVaultVar: plugin.MaskedSecretValue(v),
			}
		}
	}
	if secretsMasked == nil {
		if secretVal, ok := settingsframework.NewStoreAdapter(nil).SecretValue(SettingsPageID, FieldEnvironmentVaultVar); ok && strings.TrimSpace(secretVal) != "" {
			secretsMasked = map[string]string{
				FieldEnvironmentVaultVar: plugin.MaskedSecretValue(strings.TrimSpace(secretVal)),
			}
		}
	}

	return plugin.TaskSpec{
		Name:          specName,
		Steps:         []plugin.TaskStep{step},
		SecretsMasked: secretsMasked,
	}, nil
}
