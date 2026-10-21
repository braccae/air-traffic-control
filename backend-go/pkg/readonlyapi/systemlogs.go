package readonlyapi

import (
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

// logFilter mirrors the SystemLogsFilter produced by sanitizeLogFilter in
// src/utils/systemLogs.ts. Optional fields use omitempty so the JSON matches
// Express, where JSON.stringify drops `undefined` keys.
type logFilter struct {
	Source   string `json:"source"`
	Unit     string `json:"unit,omitempty"`
	User     string `json:"user,omitempty"`
	Priority string `json:"priority,omitempty"`
	Since    string `json:"since,omitempty"`
	Grep     string `json:"grep,omitempty"`
	Lines    int    `json:"lines"`
}

// validPriorities mirrors VALID_PRIORITIES in src/utils/systemLogs.ts.
var validPriorities = map[string]string{
	"emerg": "emerg", "emergency": "emerg", "0": "emerg",
	"alert": "alert", "1": "alert",
	"crit": "crit", "critical": "crit", "2": "crit",
	"err": "err", "error": "err", "3": "err",
	"warn": "warning", "warning": "warning", "4": "warning",
	"notice": "notice", "5": "notice",
	"info": "info", "6": "info",
	"debug": "debug", "7": "debug",
}

// validSources mirrors VALID_SOURCES in src/utils/systemLogs.ts.
var validSources = map[string]bool{"journald": true, "dmesg": true, "audit": true, "sshd": true}

var (
	reUnit      = regexp.MustCompile(`^[a-zA-Z0-9_@.-]+$`)
	reUser      = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)
	reSinceSafe = regexp.MustCompile(`^[a-zA-Z0-9\s:_-]+$`)
	reControl   = regexp.MustCompile("[\r\n\x00-\x1f\x7f]")
	reDigits    = regexp.MustCompile(`^\d+$`)
)

// sanitizeLogFilter ports sanitizeLogFilter() from src/utils/systemLogs.ts.
func sanitizeLogFilter(raw map[string]string) logFilter {
	f := logFilter{Source: "journald", Lines: 100}

	// lines: clamp to [1, 1000], default 100.
	if v, ok := raw["lines"]; ok && v != "" {
		if parsed, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			if parsed < 1 {
				parsed = 1
			}
			if parsed > 1000 {
				parsed = 1000
			}
			f.Lines = parsed
		}
	}

	// source: must be in the allow-list, default journald.
	src := strings.ToLower(strings.TrimSpace(raw["source"]))
	if validSources[src] {
		f.Source = src
	}

	// unit: alphanumeric + dot/hyphen/underscore/at only.
	if u := strings.TrimSpace(raw["unit"]); u != "" && reUnit.MatchString(u) {
		f.Unit = u
	}

	// user: alphanumeric + underscore/hyphen only.
	if u := strings.TrimSpace(raw["user"]); u != "" && reUser.MatchString(u) {
		f.User = u
	}

	// priority: normalise known names/levels, ignore "all" and unknowns.
	if p := strings.TrimSpace(raw["priority"]); p != "" {
		norm := strings.ToLower(p)
		if norm != "all" {
			if mapped, ok := validPriorities[norm]; ok {
				f.Priority = mapped
			}
		}
	}

	// since: normalise known shorthands, accept safe date/time strings.
	if s := strings.TrimSpace(raw["since"]); s != "" {
		switch strings.ToLower(s) {
		case "15m", "15 minutes ago":
			f.Since = "15 minutes ago"
		case "1h", "1 hour ago":
			f.Since = "1 hour ago"
		case "6h", "6 hours ago":
			f.Since = "6 hours ago"
		case "24h", "24 hours ago", "1d":
			f.Since = "24 hours ago"
		case "7d", "7 days ago":
			f.Since = "7 days ago"
		case "all":
			// no since filter
		default:
			if reSinceSafe.MatchString(s) {
				f.Since = s
			}
		}
	}

	// grep: strip control characters and newlines.
	if g := raw["grep"]; g != "" {
		cleaned := strings.TrimSpace(reControl.ReplaceAllString(g, ""))
		if cleaned != "" {
			f.Grep = cleaned
		}
	}

	return f
}

// escapeShellSingleQuote ports escapeShellSingleQuote() from src/utils/systemLogs.ts.
func escapeShellSingleQuote(s string) string {
	return strings.ReplaceAll(s, "'", `'\''`)
}

// buildJournalctlCommand ports buildJournalctlCommand() from src/utils/systemLogs.ts.
func buildJournalctlCommand(f logFilter) string {
	s := sanitizeLogFilter(map[string]string{
		"source":   f.Source,
		"unit":     f.Unit,
		"user":     f.User,
		"priority": f.Priority,
		"since":    f.Since,
		"grep":     f.Grep,
		"lines":    strconv.Itoa(f.Lines),
	})

	parts := []string{"--no-pager", fmt.Sprintf("-n %d", s.Lines)}

	switch s.Source {
	case "dmesg":
		parts = append(parts, "-k")
	case "audit", "sshd":
		parts = append(parts, "-u sshd")
	}

	if s.Unit != "" && s.Source != "audit" && s.Source != "sshd" {
		parts = append(parts, "-u "+s.Unit)
	}

	if s.User != "" {
		switch {
		case reDigits.MatchString(s.User):
			parts = append(parts, "_UID="+s.User)
		case s.User == "root":
			parts = append(parts, "_UID=0")
		case s.User == "core":
			parts = append(parts, "_UID=1000")
		default:
			parts = append(parts, fmt.Sprintf("_UID=$(id -u %s 2>/dev/null || echo 65534)", s.User))
		}
	}

	if s.Priority != "" {
		parts = append(parts, "-p "+s.Priority)
	}

	if s.Since != "" {
		parts = append(parts, fmt.Sprintf("--since=\"%s\"", s.Since))
	}

	if s.Grep != "" {
		parts = append(parts, fmt.Sprintf("-g '%s'", escapeShellSingleQuote(s.Grep)))
	}

	flags := strings.Join(parts, " ")

	if s.Source == "dmesg" {
		return fmt.Sprintf("(journalctl %s 2>/dev/null || sudo -n journalctl %s 2>/dev/null || dmesg -T 2>/dev/null | tail -n %d)", flags, flags, s.Lines)
	}

	return fmt.Sprintf("journalctl %s 2>/dev/null || sudo -n journalctl %s 2>&1", flags, flags)
}

// handleSystemLogs ports GET /api/nodes/:id/system-logs and GET /api/system/logs.
func (a *API) handleSystemLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	q := r.URL.Query()
	queryNodeID := r.PathValue("id")
	if queryNodeID == "" {
		queryNodeID = q.Get("node")
	}
	if queryNodeID == "" {
		queryNodeID = q.Get("host")
	}

	node, err := a.findNode(queryNodeID)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"success": false, "error": "Failed to retrieve system logs from node", "lines": []string{}, "host": "", "filter": sanitizeLogFilter(map[string]string{})})
		return
	}

	filter := sanitizeLogFilter(map[string]string{
		"source":   q.Get("source"),
		"unit":     q.Get("unit"),
		"user":     q.Get("user"),
		"priority": q.Get("priority"),
		"since":    q.Get("since"),
		"grep":     q.Get("grep"),
		"lines":    q.Get("lines"),
	})

	_, runner, err := a.factsAndRunner(r.Context(), node.ID)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"success": false,
			"error":   err.Error(),
			"lines":   []string{},
			"host":    node.TailscaleHost,
			"filter":  filter,
		})
		return
	}

	cmd := buildJournalctlCommand(filter)
	out, err := runNodeCommand(r.Context(), runner, cmd)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"success": false,
			"error":   err.Error(),
			"lines":   []string{},
			"host":    node.TailscaleHost,
			"filter":  filter,
		})
		return
	}

	raw := strings.Split(out, "\n")
	lines := make([]string, 0, len(raw))
	for _, l := range raw {
		if t := strings.TrimRight(l, " \t\r\n"); t != "" {
			lines = append(lines, t)
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"lines":   lines,
		"host":    node.TailscaleHost,
		"filter":  filter,
	})
}
