package none

import (
	"context"
	"fmt"
	"strings"

	"air-traffic-control/backend/pkg/plugin"
)

func init() {
	_ = plugin.Register(New())
}

// Plugin implements plugin.Plugin and plugin.Actor for direct/root unprivileged execution.
type Plugin struct{}

// New returns a new direct/none elevation plugin.
func New() *Plugin {
	return &Plugin{}
}

// Meta returns metadata for none.
func (p *Plugin) Meta() plugin.Meta {
	return plugin.Meta{
		ID:          "elevation/none",
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
	if h.ActiveElevation != "" && h.ActiveElevation != "elevation/none" {
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

// Actions lists supported mutation actions.
func (p *Plugin) Actions() []plugin.ActionSpec {
	return []plugin.ActionSpec{
		{
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
	}
}

// Compile generates Ansible task specification with none become method.
func (p *Plugin) Compile(h plugin.HostFacts, action string, opts map[string]any) (plugin.TaskSpec, error) {
	cmd, _ := opts["command"].(string)
	if cmd == "" {
		cmd = "id -u"
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
