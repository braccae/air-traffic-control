package service

import (
	"context"
	"fmt"
	"strings"

	"air-traffic-control/backend/pkg/plugin"
	"air-traffic-control/backend/pkg/plugin/newapi"
	"air-traffic-control/backend/pkg/plugin/settingsframework"
	"air-traffic-control/backend/pkg/systemd"
)

// SettingsPageID is the canonical settings page route slug for the system service plugin.
const SettingsPageID = "system/service"

// Default configuration values when not overridden per request or in datacenter settings.
const (
	DefaultServiceScope  = "system"
	DefaultDropInFile    = "atc-override.conf"
	DefaultRebootMsg     = "Reboot initiated by Cockpit"
	DefaultRebootTimeout = 300
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

// ServiceUnit represents a single system service unit.
type ServiceUnit struct {
	Unit          string `json:"unit"`
	Load          string `json:"load"`
	Active        string `json:"active"`
	Sub           string `json:"sub"`
	Description   string `json:"description"`
	UnitFileState string `json:"unitFileState,omitempty"`
	Enabled       bool   `json:"enabled"`
	Scope         string `json:"scope,omitempty"`
}

// ServiceReadPayload represents the JSON payload returned by Reader.
type ServiceReadPayload struct {
	Manager  string        `json:"manager"` // "systemd", "sysv", "openrc"
	Services []ServiceUnit `json:"services"`
	// Warnings carries non-fatal read problems (e.g. no reachable user manager).
	Warnings []string `json:"warnings,omitempty"`
}

// Plugin implements plugin.Plugin, plugin.Reader, plugin.Actor, and v2 newapi contracts
// for systemd and SysV/OpenRC service management.
type Plugin struct{}

// New returns a new system service plugin.
func New() *Plugin {
	return &Plugin{}
}

// Meta returns metadata for service management.
func (p *Plugin) Meta() plugin.Meta {
	return plugin.Meta{
		ID:          "system/service",
		Kind:        plugin.KindSystem,
		Name:        "service",
		Label:       "System Services & Power",
		Icon:        "settings_suggest",
		Description: "System service lifecycle (systemd / OpenRC) and node power/reboot control",
		Order:       10,
	}
}

// Compatible checks if host is Linux, BSD, or Darwin.
func (p *Plugin) Compatible(h plugin.HostFacts) bool {
	if h.OSFamily == "windows" || h.OSFamily == "router_switch" {
		return false
	}
	return true
}

// Requirements returns required dependencies for system service management.
func (p *Plugin) Requirements() plugin.Requirements {
	return plugin.Requirements{
		ControllerCollections: []string{
			"ansible.builtin",
		},
	}
}

// CollectionSchedule returns the standard polling cadence and cache TTL schedule for system services.
func (p *Plugin) CollectionSchedule() newapi.CollectionSchedule {
	return newapi.DefaultScheduleForKind(p.Meta().Kind)
}

// CollectionSchema documents the v2 payload structure emitted by Read.
func (p *Plugin) CollectionSchema() newapi.CollectionSchema {
	return newapi.CollectionSchema{
		Version:     "v2",
		RootKey:     "services",
		Description: p.Meta().Description,
	}
}

// CategorizedSettingsPage declares datacenter-wide service and power defaults via settingsframework.
func (p *Plugin) CategorizedSettingsPage() newapi.CategorizedSettingsPage {
	meta := p.Meta()
	return settingsframework.NewPageBuilder(SettingsPageID, "System Services & Power Defaults").
		WithIcon(meta.Icon).
		WithCategory(string(meta.Kind)).
		WithScope(newapi.SettingsScopeDatacenter).
		WithOrder(meta.Order).
		WithDescription("Datacenter-wide systemd unit scope, drop-in override filename, and graceful reboot defaults.").
		SelectField("defaultScope", "Default Service Manager Scope", DefaultServiceScope, []plugin.SettingsFieldOption{
			{Value: "system", Label: "System Manager (root)"},
			{Value: "user", Label: "User Session Manager (--user)"},
		}, settingsframework.WithDescription("Default systemd manager scope used when no explicit scope is provided.")).
		StringField("defaultDropInFile", "Default Drop-in Override Filename", DefaultDropInFile,
			settingsframework.WithPlaceholder(DefaultDropInFile),
			settingsframework.WithDescription("Default drop-in filename written under /etc/systemd/system/<unit>.d/.")).
		StringField("defaultRebootMessage", "Default Reboot Broadcast Message", DefaultRebootMsg,
			settingsframework.WithPlaceholder(DefaultRebootMsg),
			settingsframework.WithDescription("Broadcast wall message announced prior to graceful host reboots.")).
		IntField("defaultRebootTimeout", "Default Reboot Timeout (Seconds)", DefaultRebootTimeout,
			settingsframework.WithMinMax(30, 3600),
			settingsframework.WithDescription("Maximum seconds Ansible waits for a rebooted host to return online.")).
		BoolField("collectUserUnits", "Collect User Session Units", true,
			settingsframework.WithDescription("Include per-user systemd session units alongside system units during discovery.")).
		BuildCategorized()
}

// SettingsPage returns the underlying plugin.SettingsPage for legacy consumers.
func (p *Plugin) SettingsPage() plugin.SettingsPage {
	return p.CategorizedSettingsPage().SettingsPage
}

func defaultDropInFileName() string {
	return settingsframework.NewStoreAdapter(nil).StringOrDefault(SettingsPageID, "defaultDropInFile", DefaultDropInFile)
}

func defaultRebootMessage() string {
	return settingsframework.NewStoreAdapter(nil).StringOrDefault(SettingsPageID, "defaultRebootMessage", DefaultRebootMsg)
}

func defaultRebootTimeoutSeconds() int {
	return settingsframework.NewStoreAdapter(nil).IntOrDefault(SettingsPageID, "defaultRebootTimeout", DefaultRebootTimeout)
}

// ContextMenuEntries provides system service and host power context menu actions for compatible hosts.
func (p *Plugin) ContextMenuEntries(ctx context.Context, h plugin.HostFacts) []newapi.ContextMenuEntry {
	if !p.Compatible(h) {
		return nil
	}
	meta := p.Meta()
	return newapi.NewContextMenuBuilder(meta.ID).
		WithTarget(newapi.TargetService).
		WithGroup(string(meta.Kind)).
		AddAction("start", "Start Service", "play_arrow", "start", 10).
		AddAction("stop", "Stop Service", "stop", "stop", 20).
		AddAction("restart", "Restart Service", "restart_alt", "restart", 30).
		AddAction("reload", "Reload Service", "sync", "reload", 40).
		AddAction("enable", "Enable Service", "check_circle", "enable", 50).
		AddAction("disable", "Disable Service", "block", "disable", 60).
		AddAction("daemon-reload", "Reload Systemd Daemon", "refresh", "daemon-reload", 70).
		AddDangerAction("reboot", "Reboot Host", "power_settings_new", "reboot", "Safely reboot this host?", 90).
		Build()
}

// TaskTypes registers specialized service control task support for system/service.
func (p *Plugin) TaskTypes() []newapi.TaskTypeSpec {
	meta := p.Meta()
	return []newapi.TaskTypeSpec{
		{
			Type:           newapi.TaskTypeServiceControl,
			PluginID:       meta.ID,
			Label:          "System Service & Power Control",
			Description:    "Manage systemd or OpenRC/SysV services, install unit drop-ins, or reboot the host",
			Icon:           meta.Icon,
			SupportsDryRun: true,
			SupportsCancel: false,
			Parameters: []plugin.OptionSchema{
				{
					Name:        "name",
					Type:        "string",
					Required:    false,
					Description: "Service unit name (or pass via 'service')",
				},
				{
					Name:        "state",
					Type:        "select",
					Required:    false,
					Enum:        []string{"started", "stopped", "restarted", "reloaded", "enabled", "disabled"},
					Description: "Desired service state when invoked as a generic service_control task",
				},
				scopeOption(),
			},
		},
	}
}

// Probe checks if service control utilities (systemctl, service) are present.
func (p *Plugin) Probe(ctx context.Context, r plugin.Runner, h plugin.HostFacts) (plugin.Detection, error) {
	if r == nil {
		return plugin.Detection{
			Detected: false,
			Status:   "OFFLINE",
			Error:    "runner is nil",
		}, nil
	}

	// Try systemctl first
	out, err := r.RunCommand(ctx, "command -v systemctl")
	if err == nil && strings.TrimSpace(out) != "" {
		return plugin.Detection{
			Detected: true,
			Status:   "ONLINE",
			Details:  "systemd service manager active",
		}, nil
	}

	// Fallback to service command (SysV / OpenRC / BSD)
	out, err = r.RunCommand(ctx, "command -v service")
	if err == nil && strings.TrimSpace(out) != "" {
		return plugin.Detection{
			Detected: true,
			Status:   "ONLINE",
			Details:  "SysV / OpenRC service manager active",
		}, nil
	}

	return plugin.Detection{
		Detected: false,
		Status:   "UNAVAILABLE",
		Error:    "neither systemctl nor service found on host",
	}, nil
}

// Read fetches all system services from the host.
func (p *Plugin) Read(ctx context.Context, r plugin.Runner, h plugin.HostFacts) (any, error) {
	if r == nil {
		return nil, fmt.Errorf("runner is required")
	}

	isSystemd := true
	if h.Overrides != nil {
		if initSys, ok := h.Overrides["initSystem"]; ok && initSys != "" && initSys != "systemd" {
			isSystemd = false
		}
	}

	if isSystemd {
		// 1. systemd's own JSON interface for system units, plus the user
		//    manager's units when a user session bus is reachable. Unit-file
		//    state (enabled/disabled/static/masked) is merged in per unit.
		if listed, warnings, err := systemd.ListAllScopes(ctx, r); err == nil && len(listed) > 0 {
			units := make([]ServiceUnit, 0, len(listed))
			for _, u := range listed {
				units = append(units, ServiceUnit{
					Unit:          u.Unit,
					Load:          u.Load,
					Active:        u.Active,
					Sub:           u.Sub,
					Description:   u.Description,
					UnitFileState: u.UnitFileState,
					Enabled:       u.Enabled,
					Scope:         u.Scope,
				})
			}
			return ServiceReadPayload{
				Manager:  "systemd",
				Services: units,
				Warnings: warnings,
			}, nil
		}

		// 2. Fallback for hosts whose systemd predates `--output=json` (or whose
		//    list-units output was unusable): parse the tabular listing.
		txtOut, err := r.RunCommand(ctx, "systemctl list-units --type=service --all --no-legend --no-pager")
		if err == nil && strings.TrimSpace(txtOut) != "" {
			units := parseSystemctlText(txtOut)
			for i := range units {
				units[i].Scope = string(systemd.ScopeSystem)
			}
			return ServiceReadPayload{
				Manager:  "systemd",
				Services: units,
			}, nil
		}
	}

	// 3. SysV / OpenRC fallback
	sysvOut, err := r.RunCommand(ctx, "service --status-all 2>&1 || rc-status -s 2>&1")
	if err == nil && strings.TrimSpace(sysvOut) != "" {
		units := parseSysvText(sysvOut)
		return ServiceReadPayload{
			Manager:  "sysv",
			Services: units,
		}, nil
	}

	return ServiceReadPayload{
		Manager:  "unknown",
		Services: []ServiceUnit{},
	}, nil
}

func parseSystemctlText(output string) []ServiceUnit {
	var units []ServiceUnit
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		// Strip leading bullet ● or *
		trimmed = strings.TrimPrefix(trimmed, "●")
		trimmed = strings.TrimPrefix(trimmed, "*")
		trimmed = strings.TrimSpace(trimmed)

		fields := strings.Fields(trimmed)
		if len(fields) < 4 {
			continue
		}

		unit := fields[0]
		load := fields[1]
		active := fields[2]
		sub := fields[3]
		desc := ""
		if len(fields) > 4 {
			desc = strings.Join(fields[4:], " ")
		}

		units = append(units, ServiceUnit{
			Unit:        unit,
			Load:        load,
			Active:      active,
			Sub:         sub,
			Description: desc,
		})
	}
	return units
}

func parseSysvText(output string) []ServiceUnit {
	var units []ServiceUnit
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		fields := strings.Fields(trimmed)
		if len(fields) >= 3 && fields[0] == "[" && fields[2] == "]" {
			flag := fields[1]
			unit := ""
			if len(fields) >= 4 {
				unit = fields[3]
			}
			active := "inactive"
			sub := "stopped"
			if flag == "+" {
				active = "active"
				sub = "running"
			}
			units = append(units, ServiceUnit{
				Unit:        unit + ".service",
				Load:        "loaded",
				Active:      active,
				Sub:         sub,
				Description: fmt.Sprintf("SysV service %s", unit),
			})
		}
	}
	return units
}

// scopeOption is the shared selector between system and user unit managers.
func scopeOption() plugin.OptionSchema {
	return plugin.OptionSchema{
		Name:        "scope",
		Type:        "select",
		Required:    false,
		Default:     "system",
		Enum:        []string{"system", "user"},
		Description: "systemd manager instance (user units act as the connecting user, no root required)",
	}
}

// unitOptions is the option set shared by every per-unit lifecycle action.
func unitOptions() []plugin.OptionSchema {
	return []plugin.OptionSchema{
		{Name: "name", Type: "string", Required: true, Description: "Service unit name (e.g. sshd.service)"},
		scopeOption(),
	}
}

// ManagementActions returns the structured v2 management action specifications for system services.
func (p *Plugin) ManagementActions() []newapi.ManagementActionSpec {
	return []newapi.ManagementActionSpec{
		{
			ActionSpec: plugin.ActionSpec{
				Name:        "daemon-reload",
				Label:       "Reload Daemon",
				Description: "Reloads systemd manager configuration (declarative daemon_reload)",
				Options:     []plugin.OptionSchema{scopeOption()},
			},
			Category:         newapi.ActionCategoryMaintenance,
			Idempotent:       true,
			Dangerous:        false,
			InvalidatesKinds: []plugin.Kind{plugin.KindSystem},
		},
		{
			ActionSpec: plugin.ActionSpec{
				Name:        "start",
				Label:       "Start Service",
				Description: "Starts a system service",
				Options:     unitOptions(),
			},
			Category:         newapi.ActionCategoryLifecycle,
			Idempotent:       true,
			Dangerous:        false,
			InvalidatesKinds: []plugin.Kind{plugin.KindSystem},
		},
		{
			ActionSpec: plugin.ActionSpec{
				Name:        "stop",
				Label:       "Stop Service",
				Description: "Stops a system service",
				Options:     unitOptions(),
			},
			Category:         newapi.ActionCategoryLifecycle,
			Idempotent:       true,
			Dangerous:        true,
			ConfirmPrompt:    "Stop this system service?",
			InvalidatesKinds: []plugin.Kind{plugin.KindSystem},
		},
		{
			ActionSpec: plugin.ActionSpec{
				Name:        "restart",
				Label:       "Restart Service",
				Description: "Restarts a system service",
				Options:     unitOptions(),
			},
			Category:         newapi.ActionCategoryLifecycle,
			Idempotent:       false,
			Dangerous:        false,
			InvalidatesKinds: []plugin.Kind{plugin.KindSystem},
		},
		{
			ActionSpec: plugin.ActionSpec{
				Name:        "reload",
				Label:       "Reload Service",
				Description: "Reloads service configuration without full restart",
				Options:     unitOptions(),
			},
			Category:         newapi.ActionCategoryLifecycle,
			Idempotent:       true,
			Dangerous:        false,
			InvalidatesKinds: []plugin.Kind{plugin.KindSystem},
		},
		{
			ActionSpec: plugin.ActionSpec{
				Name:        "enable",
				Label:       "Enable Service",
				Description: "Enables service to start automatically on boot",
				Options:     unitOptions(),
			},
			Category:         newapi.ActionCategoryConfiguration,
			Idempotent:       true,
			Dangerous:        false,
			InvalidatesKinds: []plugin.Kind{plugin.KindSystem},
		},
		{
			ActionSpec: plugin.ActionSpec{
				Name:        "disable",
				Label:       "Disable Service",
				Description: "Disables service from automatic startup",
				Options:     unitOptions(),
			},
			Category:         newapi.ActionCategoryConfiguration,
			Idempotent:       true,
			Dangerous:        true,
			ConfirmPrompt:    "Disable this service from starting on boot?",
			InvalidatesKinds: []plugin.Kind{plugin.KindSystem},
		},
		{
			ActionSpec: plugin.ActionSpec{
				Name:  "write-dropin",
				Label: "Install Drop-in",
				Description: "Writes an ATC-owned drop-in under /etc/systemd/system/<unit>.d/ and reloads the manager. " +
					"Upstream unit files are never modified.",
				Options: []plugin.OptionSchema{
					{Name: "name", Type: "string", Required: true, Description: "Target unit name (e.g. sshd.service)"},
					{Name: "dropin", Type: "string", Required: false, Default: "atc-override.conf", Description: "Drop-in file name (must end in .conf)"},
					{Name: "content", Type: "string", Required: true, Description: "Drop-in [Service]/[Unit] section body"},
				},
			},
			Category:         newapi.ActionCategoryConfiguration,
			Idempotent:       true,
			Dangerous:        false,
			InvalidatesKinds: []plugin.Kind{plugin.KindSystem},
		},
		{
			ActionSpec: plugin.ActionSpec{
				Name:        "reboot",
				Label:       "Reboot Host",
				Description: "Safely restarts the host operating system",
				Options: []plugin.OptionSchema{
					{Name: "msg", Type: "string", Required: false, Default: "Reboot initiated by Cockpit", Description: "Broadcast reboot message"},
					{Name: "timeout", Type: "int", Required: false, Default: 300, Description: "Reboot completion timeout in seconds"},
				},
			},
			Category:         newapi.ActionCategoryLifecycle,
			Idempotent:       false,
			Dangerous:        true,
			ConfirmPrompt:    "Safely reboot the host operating system?",
			InvalidatesKinds: []plugin.Kind{plugin.KindSystem},
		},
	}
}

// Actions enumerates mutation actions supported by service plugin.
func (p *Plugin) Actions() []plugin.ActionSpec {
	mgmt := p.ManagementActions()
	specs := make([]plugin.ActionSpec, len(mgmt))
	for i, m := range mgmt {
		specs[i] = m.ActionSpec
	}
	return specs
}

func normalizeActionName(action string) string {
	norm := strings.ToLower(strings.TrimSpace(action))
	switch norm {
	case "daemon_reload":
		return "daemon-reload"
	case "write_dropin", "install-dropin":
		return "write-dropin"
	default:
		return norm
	}
}

// ValidateAction validates options for a service management action against its schema.
func (p *Plugin) ValidateAction(h plugin.HostFacts, action string, opts map[string]any) error {
	canonical := normalizeActionName(action)
	normalizedOpts := make(map[string]any, len(opts))
	for k, v := range opts {
		normalizedOpts[k] = v
	}
	if _, hasName := normalizedOpts["name"]; !hasName {
		if svc, ok := normalizedOpts["service"].(string); ok && strings.TrimSpace(svc) != "" {
			normalizedOpts["name"] = svc
		}
	}
	for _, spec := range p.ManagementActions() {
		if strings.EqualFold(spec.Name, canonical) {
			return newapi.ValidateManagementOptions(spec, normalizedOpts)
		}
	}
	return fmt.Errorf("unsupported service action: %s", action)
}

// CompileTask adapts a typed v2 TaskRequest to the plugin's declarative Ansible TaskSpec compiler.
func (p *Plugin) CompileTask(ctx context.Context, h plugin.HostFacts, req newapi.TaskRequest) (plugin.TaskSpec, error) {
	params := make(map[string]any, len(req.Params))
	for k, v := range req.Params {
		params[k] = v
	}

	action := strings.TrimSpace(req.Action)
	if action == "" {
		if a, ok := params["action"].(string); ok && strings.TrimSpace(a) != "" {
			action = strings.TrimSpace(a)
		} else if state, ok := params["state"].(string); ok && strings.TrimSpace(state) != "" {
			switch strings.ToLower(strings.TrimSpace(state)) {
			case "started", "start":
				action = "start"
			case "stopped", "stop":
				action = "stop"
			case "restarted", "restart":
				action = "restart"
			case "reloaded", "reload":
				action = "reload"
			case "enabled", "enable":
				action = "enable"
			case "disabled", "disable":
				action = "disable"
			default:
				action = strings.TrimSpace(state)
			}
		}
	}
	if action == "" {
		return plugin.TaskSpec{}, fmt.Errorf("action is required for plugin %s", p.Meta().ID)
	}
	return p.Compile(h, action, params)
}

// scopeFromOpts reads the requested systemd scope, defaulting to system.
func scopeFromOpts(opts map[string]any) (systemd.Scope, error) {
	raw, _ := opts["scope"].(string)
	return systemd.ParseScope(raw)
}

// Compile generates a declarative TaskSpec for service or reboot actions.
func (p *Plugin) Compile(h plugin.HostFacts, action string, opts map[string]any) (plugin.TaskSpec, error) {
	normAction := strings.ToLower(strings.TrimSpace(action))

	// Daemon reload action
	if normAction == "daemon-reload" || normAction == "daemon_reload" {
		scope, err := scopeFromOpts(opts)
		if err != nil {
			return plugin.TaskSpec{}, err
		}
		params := map[string]any{"daemon_reload": true}
		name := fmt.Sprintf("Reload systemd daemon on %s", h.NodeID)
		stepName := "Reload systemd manager"
		if scope == systemd.ScopeUser {
			params["scope"] = string(systemd.ScopeUser)
			name = fmt.Sprintf("Reload systemd user daemon on %s", h.NodeID)
			stepName = "Reload systemd user manager"
		} else {
			params["become"] = true
		}
		return plugin.TaskSpec{
			Name: name,
			Steps: []plugin.TaskStep{
				{
					Name:   stepName,
					Module: "ansible.builtin.systemd_service",
					Params: params,
				},
			},
		}, nil
	}

	// ATC-owned drop-in: never touches upstream unit files, only
	// /etc/systemd/system/<unit>.d/<dropin> plus a manager reload.
	if normAction == "write-dropin" || normAction == "write_dropin" || normAction == "install-dropin" {
		return compileDropIn(h, opts)
	}

	// Reboot action
	if normAction == "reboot" {
		timeout := defaultRebootTimeoutSeconds()
		if t, ok := opts["timeout"].(int); ok && t > 0 {
			timeout = t
		} else if tf, ok := opts["timeout"].(float64); ok && tf > 0 {
			timeout = int(tf)
		}
		msg, _ := opts["msg"].(string)
		if msg == "" {
			msg = defaultRebootMessage()
		}

		params := map[string]any{
			"msg":            msg,
			"reboot_timeout": timeout,
			"become":         true,
		}

		return plugin.TaskSpec{
			Name: fmt.Sprintf("Reboot node %s", h.NodeID),
			Steps: []plugin.TaskStep{
				{
					Name:   fmt.Sprintf("Reboot host (%s)", msg),
					Module: "ansible.builtin.reboot",
					Params: params,
				},
			},
		}, nil
	}

	// Service lifecycle actions
	name, _ := opts["name"].(string)
	if name == "" {
		name, _ = opts["service"].(string)
	}
	if name == "" {
		return plugin.TaskSpec{}, fmt.Errorf("service name is required")
	}

	// Detect if target uses OpenRC / SysV instead of systemd
	isNonSystemd := false
	if h.Overrides != nil {
		if initSys, ok := h.Overrides["initSystem"]; ok && initSys != "" && initSys != "systemd" {
			isNonSystemd = true
		}
		if osDesc, ok := h.Overrides["osDescription"]; ok && strings.Contains(strings.ToLower(osDesc), "alpine") {
			isNonSystemd = true
		}
	}

	if !isNonSystemd {
		normalized, err := systemd.NormalizeUnitName(name)
		if err != nil {
			return plugin.TaskSpec{}, err
		}
		name = normalized
	}

	scope, err := scopeFromOpts(opts)
	if err != nil {
		return plugin.TaskSpec{}, err
	}

	module := "ansible.builtin.systemd_service"
	if isNonSystemd {
		module = "ansible.builtin.service"
		scope = systemd.ScopeSystem
	}

	stateMap := map[string]string{
		"start":   "started",
		"stop":    "stopped",
		"restart": "restarted",
		"reload":  "reloaded",
	}

	params := map[string]any{"name": name}

	switch normAction {
	case "start", "stop", "restart", "reload":
		params["state"] = stateMap[normAction]
	case "enable":
		params["enabled"] = true
	case "disable":
		params["enabled"] = false
	default:
		return plugin.TaskSpec{}, fmt.Errorf("unsupported service action: %s", action)
	}

	if !isNonSystemd && scope == systemd.ScopeUser {
		// `scope: user` drives the per-user manager as the connecting user, so
		// no privilege escalation is requested for user units.
		params["scope"] = string(systemd.ScopeUser)
	} else {
		params["become"] = true
	}

	label := fmt.Sprintf("%s service %s", strings.Title(normAction), name)
	if scope == systemd.ScopeUser {
		label = fmt.Sprintf("%s user service %s", strings.Title(normAction), name)
	}

	return plugin.TaskSpec{
		Name: label,
		Steps: []plugin.TaskStep{
			{
				Name:   label,
				Module: module,
				Params: params,
			},
		},
	}, nil
}

// compileDropIn stages an ATC-owned drop-in write plus a manager reload. It never
// writes to the upstream unit file: the drop-in path is always
// /etc/systemd/system/<unit>.d/<dropin>.
func compileDropIn(h plugin.HostFacts, opts map[string]any) (plugin.TaskSpec, error) {
	rawName, _ := opts["name"].(string)
	name, err := systemd.NormalizeUnitName(rawName)
	if err != nil {
		return plugin.TaskSpec{}, err
	}

	dropin, _ := opts["dropin"].(string)
	if strings.TrimSpace(dropin) == "" {
		dropin = defaultDropInFileName()
	}
	dropin = strings.TrimSpace(dropin)
	if !strings.HasSuffix(dropin, ".conf") || strings.ContainsAny(dropin, "/ 	\n") || strings.Contains(dropin, "..") {
		return plugin.TaskSpec{}, fmt.Errorf("invalid drop-in file name %q (must be a plain .conf name)", dropin)
	}

	content, _ := opts["content"].(string)
	if strings.TrimSpace(content) == "" {
		return plugin.TaskSpec{}, fmt.Errorf("drop-in content is required")
	}

	dest := fmt.Sprintf("/etc/systemd/system/%s.d/%s", name, dropin)
	title := fmt.Sprintf("Install ATC drop-in for %s on %s", name, h.NodeID)

	return plugin.TaskSpec{
		Name: title,
		Steps: []plugin.TaskStep{
			{
				Name:   fmt.Sprintf("Write ATC drop-in %s", dest),
				Module: "ansible.builtin.copy",
				Params: map[string]any{
					"dest":    dest,
					"content": content,
					"owner":   "root",
					"group":   "root",
					"mode":    "0644",
					"become":  true,
				},
			},
			{
				Name:   "Reload systemd manager",
				Module: "ansible.builtin.systemd_service",
				Params: map[string]any{
					"daemon_reload": true,
					"become":        true,
				},
			},
		},
	}, nil
}
