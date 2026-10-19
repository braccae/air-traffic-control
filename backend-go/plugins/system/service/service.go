package service

import (
	"context"
	"fmt"
	"strings"

	"air-traffic-control/backend/pkg/plugin"
)

func init() {
	_ = plugin.Register(New())
}

// Plugin implements plugin.Plugin and plugin.Actor for systemd and SysV/OpenRC service management.
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
		Icon:        "activity",
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

// Actions enumerates mutation actions supported by service plugin.
func (p *Plugin) Actions() []plugin.ActionSpec {
	return []plugin.ActionSpec{
		{
			Name:        "start",
			Label:       "Start Service",
			Description: "Starts a system service",
			Options: []plugin.OptionSchema{
				{Name: "name", Type: "string", Required: true, Description: "Service unit name"},
			},
		},
		{
			Name:        "stop",
			Label:       "Stop Service",
			Description: "Stops a system service",
			Options: []plugin.OptionSchema{
				{Name: "name", Type: "string", Required: true, Description: "Service unit name"},
			},
		},
		{
			Name:        "restart",
			Label:       "Restart Service",
			Description: "Restarts a system service",
			Options: []plugin.OptionSchema{
				{Name: "name", Type: "string", Required: true, Description: "Service unit name"},
			},
		},
		{
			Name:        "reload",
			Label:       "Reload Service",
			Description: "Reloads service configuration without full restart",
			Options: []plugin.OptionSchema{
				{Name: "name", Type: "string", Required: true, Description: "Service unit name"},
			},
		},
		{
			Name:        "enable",
			Label:       "Enable Service",
			Description: "Enables service to start automatically on boot",
			Options: []plugin.OptionSchema{
				{Name: "name", Type: "string", Required: true, Description: "Service unit name"},
			},
		},
		{
			Name:        "disable",
			Label:       "Disable Service",
			Description: "Disables service from automatic startup",
			Options: []plugin.OptionSchema{
				{Name: "name", Type: "string", Required: true, Description: "Service unit name"},
			},
		},
		{
			Name:        "reboot",
			Label:       "Reboot Host",
			Description: "Safely restarts the host operating system",
			Options: []plugin.OptionSchema{
				{Name: "msg", Type: "string", Required: false, Default: "Reboot initiated by Cockpit", Description: "Broadcast reboot message"},
				{Name: "timeout", Type: "int", Required: false, Default: 300, Description: "Reboot completion timeout in seconds"},
			},
		},
	}
}

// Compile generates a declarative TaskSpec for service or reboot actions.
func (p *Plugin) Compile(h plugin.HostFacts, action string, opts map[string]any) (plugin.TaskSpec, error) {
	normAction := strings.ToLower(action)

	// Reboot action
	if normAction == "reboot" {
		timeout := 300
		if t, ok := opts["timeout"].(int); ok && t > 0 {
			timeout = t
		} else if tf, ok := opts["timeout"].(float64); ok && tf > 0 {
			timeout = int(tf)
		}
		msg, _ := opts["msg"].(string)
		if msg == "" {
			msg = "Reboot initiated by Cockpit"
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

	module := "ansible.builtin.systemd"
	if isNonSystemd {
		module = "ansible.builtin.service"
	}

	stateMap := map[string]string{
		"start":   "started",
		"stop":    "stopped",
		"restart": "restarted",
		"reload":  "reloaded",
	}

	params := map[string]any{
		"name":   name,
		"become": true,
	}

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

	return plugin.TaskSpec{
		Name: fmt.Sprintf("%s service %s", strings.Title(normAction), name),
		Steps: []plugin.TaskStep{
			{
				Name:   fmt.Sprintf("%s service %s", strings.Title(normAction), name),
				Module: module,
				Params: params,
			},
		},
	}, nil
}
