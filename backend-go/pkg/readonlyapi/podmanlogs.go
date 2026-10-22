package readonlyapi

import (
	"net/http"
	"strconv"
	"strings"
)

// handlePodmanLogs ports GET /api/podman/logs (server.ts).
//
// The route has no Express counterpart for the container branch on Go yet; this
// is the same command the Express handler runs, selected by the same target
// heuristics, executed over the real SSH runner.
func (a *API) handlePodmanLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	q := r.URL.Query()
	host := q.Get("host")
	target := q.Get("target")
	if target == "" {
		target = "system"
	}
	user := q.Get("user")

	uid := 0
	hasUID := false
	if raw := q.Get("uid"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			uid = parsed
			hasUID = true
		}
	}

	node, err := a.findNode(host)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"lines":   []string{},
			"error":   "Failed to fetch logs for " + target,
			"host":    "",
		})
		return
	}

	_, runner, err := a.factsAndRunner(r.Context(), node.ID)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"lines":   []string{},
			"error":   err.Error(),
			"host":    node.TailscaleHost,
		})
		return
	}

	var cmd string
	switch {
	case strings.Contains(target, "dmesg") || strings.Contains(target, "kernel") || strings.Contains(target, "journal"):
		cmd = "run0 --background=no journalctl -n 100 --no-pager 2>&1 || sudo -n journalctl -n 100 --no-pager 2>&1 || doas -n journalctl -n 100 --no-pager 2>&1 || dmesg | tail -n 100 2>&1"
	case strings.Contains(target, "audit") || strings.Contains(target, "pam"):
		cmd = "run0 --background=no journalctl -u sshd -n 100 --no-pager 2>&1 || sudo -n journalctl -u sshd -n 100 --no-pager 2>&1 || doas -n journalctl -u sshd -n 100 --no-pager 2>&1 || last -n 30 2>&1"
	case user != "" && user != "root":
		effectiveUID := 1000
		if hasUID {
			effectiveUID = uid
		}
		cmd = "run0 --background=no --user " + user + " podman logs --tail 100 " + target + " 2>&1 || " +
			"sudo -n -u " + user + " XDG_RUNTIME_DIR=/run/user/" + strconv.Itoa(effectiveUID) + " podman logs --tail 100 " + target + " 2>&1 || " +
			"doas -n -u " + user + " XDG_RUNTIME_DIR=/run/user/" + strconv.Itoa(effectiveUID) + " podman logs --tail 100 " + target + " 2>&1 || " +
			"podman logs --tail 100 " + target + " 2>&1"
	default:
		cmd = "run0 --background=no podman logs --tail 100 " + target + " 2>&1 || " +
			"sudo -n podman logs --tail 100 " + target + " 2>&1 || " +
			"doas -n podman logs --tail 100 " + target + " 2>&1 || " +
			"podman logs --tail 100 " + target + " 2>&1 || " +
			"run0 --background=no docker logs --tail 100 " + target + " 2>&1 || " +
			"sudo -n docker logs --tail 100 " + target + " 2>&1 || " +
			"docker logs --tail 100 " + target + " 2>&1"
	}

	out, err := runNodeCommand(r.Context(), runner, cmd)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"lines":   []string{},
			"error":   err.Error(),
			"host":    node.TailscaleHost,
		})
		return
	}

	lines := make([]string, 0)
	for _, l := range strings.Split(out, "\n") {
		if t := strings.TrimRight(l, " \t\r\n"); t != "" {
			lines = append(lines, t)
		}
	}

	if len(lines) > 0 {
		writeJSON(w, http.StatusOK, map[string]any{
			"success": true,
			"lines":   lines,
			"host":    node.TailscaleHost,
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"success": false,
		"lines":   []string{},
		"error":   "No logs available for " + target,
		"host":    node.TailscaleHost,
	})
}
