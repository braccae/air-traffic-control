package tailscale

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

// Plugin implements local and peer Tailscale mesh status inspection.
type Plugin struct{}

func New() *Plugin {
	return &Plugin{}
}

func (p *Plugin) Meta() plugin.Meta {
	return plugin.Meta{
		ID:          "network/tailscale",
		Kind:        plugin.KindNetwork,
		Name:        "tailscale",
		Label:       "Tailscale Mesh Status",
		Icon:        "hub",
		Description: "Tailscale mesh peering, MagicDNS status, direct vs DERP relays, and peer latency",
		Order:       20,
	}
}

func (p *Plugin) Compatible(h plugin.HostFacts) bool {
	return true
}

func (p *Plugin) Requirements() plugin.Requirements {
	return plugin.Requirements{
		HostPackages: map[string][]string{
			"dnf": {"tailscale"},
			"apt": {"tailscale"},
			"apk": {"tailscale"},
		},
	}
}

func (p *Plugin) Probe(ctx context.Context, r plugin.Runner, h plugin.HostFacts) (plugin.Detection, error) {
	if r == nil {
		return plugin.Detection{Detected: false, Status: "OFFLINE", Error: "runner is nil"}, nil
	}

	out, err := r.RunCommand(ctx, "tailscale status --json 2>/dev/null || true")
	if err != nil || strings.TrimSpace(out) == "" {
		return plugin.Detection{
			Detected: false,
			Status:   "UNAVAILABLE",
			Error:    "tailscale status command failed or tailscale not running",
		}, nil
	}

	var status map[string]any
	if err := json.Unmarshal([]byte(out), &status); err != nil {
		return plugin.Detection{
			Detected: false,
			Status:   "UNAVAILABLE",
			Error:    "failed to parse tailscale status json",
		}, nil
	}

	state, _ := status["BackendState"].(string)
	if state == "" {
		state = "ONLINE"
	}

	return plugin.Detection{
		Detected: true,
		Status:   state,
		Details:  "Tailscale mesh active",
	}, nil
}

func (p *Plugin) Read(ctx context.Context, r plugin.Runner, h plugin.HostFacts) (any, error) {
	if r == nil {
		return nil, fmt.Errorf("runner is nil")
	}

	out, err := r.RunCommand(ctx, "tailscale status --json")
	if err != nil {
		return nil, err
	}

	var status map[string]any
	if err := json.Unmarshal([]byte(out), &status); err != nil {
		return nil, fmt.Errorf("invalid tailscale status json: %w", err)
	}

	return status, nil
}
