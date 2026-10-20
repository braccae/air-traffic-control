package run0

import (
	"context"
	"fmt"
	"strings"

	"air-traffic-control/backend/pkg/plugin"
)

func init() {
	_ = plugin.Register(New())
}

// Plugin implements plugin.Plugin and plugin.Actor for systemd 256+ run0 elevation.
type Plugin struct{}

// New returns a new run0 elevation plugin.
func New() *Plugin {
	return &Plugin{}
}

// Meta returns metadata for run0.
func (p *Plugin) Meta() plugin.Meta {
	return plugin.Meta{
		ID:          "elevation/run0",
		Kind:        plugin.KindElevation,
		Name:        "run0",
		Label:       "run0 (systemd polkit)",
		Icon:        "verified_user",
		Description: "Modern systemd transient polkit session (preferred elevation)",
		Order:       10,
	}
}

// Compatible checks if host is Linux.
func (p *Plugin) Compatible(h plugin.HostFacts) bool {
	if h.OSFamily != "" && h.OSFamily != "linux" {
		return false
	}
	if h.ActiveElevation != "" && h.ActiveElevation != "elevation/run0" {
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

// Actions lists supported mutation actions.
func (p *Plugin) Actions() []plugin.ActionSpec {
	return []plugin.ActionSpec{
		{
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
	}
}

// Compile generates Ansible task specification with run0 become method.
func (p *Plugin) Compile(h plugin.HostFacts, action string, opts map[string]any) (plugin.TaskSpec, error) {
	cmd, _ := opts["command"].(string)
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
