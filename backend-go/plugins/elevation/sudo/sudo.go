package sudo

import (
	"context"
	"fmt"
	"strings"

	"air-traffic-control/backend/pkg/plugin"
)

func init() {
	_ = plugin.Register(New())
}

// Plugin implements plugin.Plugin and plugin.Actor for standard sudo elevation.
type Plugin struct{}

// New returns a new sudo elevation plugin.
func New() *Plugin {
	return &Plugin{}
}

// Meta returns metadata for sudo.
func (p *Plugin) Meta() plugin.Meta {
	return plugin.Meta{
		ID:          "elevation/sudo",
		Kind:        plugin.KindElevation,
		Name:        "sudo",
		Label:       "sudo (passwordless)",
		Icon:        "shield",
		Description: "Standard sudo privilege escalation with passwordless fallback",
		Order:       20,
	}
}

// Compatible checks if host is Linux, BSD, or Darwin.
func (p *Plugin) Compatible(h plugin.HostFacts) bool {
	if h.OSFamily == "windows" {
		return false
	}
	if h.ActiveElevation != "" && h.ActiveElevation != "elevation/sudo" {
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

// Actions lists supported mutation actions.
func (p *Plugin) Actions() []plugin.ActionSpec {
	return []plugin.ActionSpec{
		{
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
	}
}

// Compile generates Ansible task specification with sudo become method.
func (p *Plugin) Compile(h plugin.HostFacts, action string, opts map[string]any) (plugin.TaskSpec, error) {
	cmd, _ := opts["command"].(string)
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
