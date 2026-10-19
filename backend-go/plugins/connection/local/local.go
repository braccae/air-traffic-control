package local

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"air-traffic-control/backend/pkg/plugin"
)

func init() {
	_ = plugin.Register(New())
}

// Plugin implements plugin.Plugin for local execution on the controller host.
type Plugin struct{}

// New returns a new local connection plugin.
func New() *Plugin {
	return &Plugin{}
}

// Meta returns metadata for the local plugin.
func (p *Plugin) Meta() plugin.Meta {
	return plugin.Meta{
		ID:          "connection/local",
		Kind:        plugin.KindConnection,
		Name:        "local",
		Label:       "Local / Loopback",
		Icon:        "server",
		Description: "Direct controller local execution via exec.CommandContext",
		Order:       1,
	}
}

// Compatible determines if the host facts target the local controller.
func (p *Plugin) Compatible(h plugin.HostFacts) bool {
	if h.ActiveConnection == "connection/local" {
		return true
	}
	if h.ActiveConnection != "" && h.ActiveConnection != "connection/local" {
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

// Runner executes commands locally on the host.
type Runner struct{}

// RunCommand executes a shell command locally.
func (r *Runner) RunCommand(ctx context.Context, cmd string) (string, error) {
	c := exec.CommandContext(ctx, "sh", "-c", cmd)
	out, err := c.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("local execution failed: %w (output: %s)", err, string(out))
	}
	return string(out), nil
}

// NewRunner returns a local command runner.
func (p *Plugin) NewRunner(h plugin.HostFacts) (plugin.Runner, error) {
	return &Runner{}, nil
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
