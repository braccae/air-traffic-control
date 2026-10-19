package journald

import (
	"context"
	"fmt"
	"strings"

	"air-traffic-control/backend/pkg/plugin"
)

func init() {
	_ = plugin.Register(New())
}

// Plugin implements systemd journald log retrieval and inspection.
type Plugin struct{}

func New() *Plugin {
	return &Plugin{}
}

func (p *Plugin) Meta() plugin.Meta {
	return plugin.Meta{
		ID:          "logs/journald",
		Kind:        plugin.KindLogs,
		Name:        "journald",
		Label:       "System & Journal Logs",
		Icon:        "description",
		Description: "Systemd journald log collector with priority, unit, and service filtering",
		Order:       10,
	}
}

func (p *Plugin) Compatible(h plugin.HostFacts) bool {
	return h.OSFamily != "windows" && h.OSFamily != "router_switch"
}

func (p *Plugin) Requirements() plugin.Requirements {
	return plugin.Requirements{
		HostPackages: map[string][]string{
			"dnf": {"systemd"},
			"apt": {"systemd"},
		},
	}
}

func (p *Plugin) Probe(ctx context.Context, r plugin.Runner, h plugin.HostFacts) (plugin.Detection, error) {
	if r == nil {
		return plugin.Detection{Detected: false, Status: "OFFLINE", Error: "runner is nil"}, nil
	}

	out, err := r.RunCommand(ctx, "command -v journalctl")
	if err != nil || strings.TrimSpace(out) == "" {
		return plugin.Detection{
			Detected: false,
			Status:   "UNAVAILABLE",
			Error:    "journalctl command not found",
		}, nil
	}

	return plugin.Detection{
		Detected: true,
		Status:   "ONLINE",
		Details:  "systemd journald active",
	}, nil
}

func (p *Plugin) Read(ctx context.Context, r plugin.Runner, h plugin.HostFacts) (any, error) {
	if r == nil {
		return nil, fmt.Errorf("runner is nil")
	}

	unit := ""
	priority := ""
	linesCount := "100"
	if h.Overrides != nil {
		if u, ok := h.Overrides["unit"]; ok && u != "" {
			unit = u
		}
		if pr, ok := h.Overrides["priority"]; ok && pr != "" {
			priority = pr
		}
		if l, ok := h.Overrides["lines"]; ok && l != "" {
			linesCount = l
		}
	}

	cmd := fmt.Sprintf("journalctl -n %s --no-pager", linesCount)
	if unit != "" {
		cmd += fmt.Sprintf(" -u %s", unit)
	}
	if priority != "" {
		cmd += fmt.Sprintf(" -p %s", priority)
	}

	out, err := r.RunCommand(ctx, cmd)
	if err != nil {
		return nil, err
	}

	rawLines := strings.Split(out, "\n")
	var lines []string
	for _, l := range rawLines {
		t := strings.TrimRight(l, "\r\n ")
		if t != "" {
			lines = append(lines, t)
		}
	}

	return map[string]any{
		"lines": lines,
		"unit":  unit,
		"count": len(lines),
	}, nil
}
