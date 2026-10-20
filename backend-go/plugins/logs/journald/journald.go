package journald

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"air-traffic-control/backend/pkg/plugin"
	"air-traffic-control/backend/pkg/systemd"
)

func init() {
	_ = plugin.Register(New())
}

var (
	// reJournalUnit matches a unit filter with or without its type suffix.
	reJournalUnit = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9:_.@-]*$`)
	// reJournalPriority matches a syslog priority name or numeric level.
	reJournalPriority = regexp.MustCompile(`^[0-7]$|^(emerg|emergency|alert|crit|critical|err|error|warn|warning|notice|info|debug)$`)
	// reJournalLines matches a plain positive line count.
	reJournalLines = regexp.MustCompile(`^[0-9]{1,5}$`)
)

// shellQuote single-quotes a value for the SSH command string.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
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
	scope := ""
	if h.Overrides != nil {
		if u, ok := h.Overrides["unit"]; ok && u != "" {
			unit = strings.TrimSpace(u)
		}
		if pr, ok := h.Overrides["priority"]; ok && pr != "" {
			priority = strings.ToLower(strings.TrimSpace(pr))
		}
		if l, ok := h.Overrides["lines"]; ok && l != "" {
			linesCount = strings.TrimSpace(l)
		}
		if s, ok := h.Overrides["scope"]; ok {
			parsed, err := systemd.ParseScope(s)
			if err != nil {
				return nil, err
			}
			if parsed == systemd.ScopeUser {
				scope = " --user"
			}
		}
	}

	// Filter values are interpolated into the SSH command string, so they are
	// validated against allow-lists rather than trusted verbatim.
	if unit != "" && !reJournalUnit.MatchString(unit) {
		return nil, fmt.Errorf("invalid journal unit filter %q", unit)
	}
	if priority != "" && !reJournalPriority.MatchString(priority) {
		return nil, fmt.Errorf("invalid journal priority %q", priority)
	}
	if !reJournalLines.MatchString(linesCount) {
		return nil, fmt.Errorf("invalid journal line count %q", linesCount)
	}

	cmd := fmt.Sprintf("journalctl%s -n %s --no-pager", scope, linesCount)
	if unit != "" {
		cmd += " -u " + shellQuote(unit)
	}
	if priority != "" {
		cmd += " -p " + priority
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
