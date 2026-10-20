package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"air-traffic-control/backend/pkg/plugin"
)

func init() {
	_ = plugin.Register(New())
}

// ServiceUnit represents a single system service unit.
type ServiceUnit struct {
	Unit        string `json:"unit"`
	Load        string `json:"load"`
	Active      string `json:"active"`
	Sub         string `json:"sub"`
	Description string `json:"description"`
}

// ServiceReadPayload represents the JSON payload returned by Reader.
type ServiceReadPayload struct {
	Manager  string        `json:"manager"` // "systemd", "sysv", "openrc"
	Services []ServiceUnit `json:"services"`
}

// Plugin implements plugin.Plugin, plugin.Reader, and plugin.Actor for systemd and SysV/OpenRC service management.
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
		// 1. Try systemctl list-units with JSON output (systemd 246+)
		jsonOut, err := r.RunCommand(ctx, "systemctl list-units --type=service --all -o json")
		if err == nil && strings.TrimSpace(jsonOut) != "" && strings.HasPrefix(strings.TrimSpace(jsonOut), "[") {
			var units []ServiceUnit
			if err := json.Unmarshal([]byte(strings.TrimSpace(jsonOut)), &units); err == nil && len(units) > 0 {
				return ServiceReadPayload{
					Manager:  "systemd",
					Services: units,
				}, nil
			}
		}

		// 2. Fallback: plain text tabular list-units
		txtOut, err := r.RunCommand(ctx, "systemctl list-units --type=service --all --no-legend --no-pager")
		if err == nil && strings.TrimSpace(txtOut) != "" {
			units := parseSystemctlText(txtOut)
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

// Actions enumerates mutation actions supported by service plugin.
func (p *Plugin) Actions() []plugin.ActionSpec {
	return []plugin.ActionSpec{
		{
			Name:        "daemon-reload",
			Label:       "Reload Daemon",
			Description: "Reloads systemd manager configuration (systemctl daemon-reload)",
			Options:     []plugin.OptionSchema{},
		},
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

	// Daemon reload action
	if normAction == "daemon-reload" || normAction == "daemon_reload" {
		return plugin.TaskSpec{
			Name: fmt.Sprintf("Reload systemd daemon on %s", h.NodeID),
			Steps: []plugin.TaskStep{
				{
					Name:   "Reload systemd manager",
					Module: "ansible.builtin.systemd",
					Params: map[string]any{
						"daemon_reload": true,
						"become":        true,
					},
				},
			},
		}, nil
	}

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
