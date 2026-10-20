package readonlyapi

import (
	"net/http"
	"strconv"
	"strings"
)

// usersCmd is the exact command server.ts runs for GET /api/nodes/:id/users.
const usersCmd = "cat /etc/passwd 2>/dev/null; echo \"===SUBUID===\"; cat /etc/subuid 2>/dev/null || true"

// HostUserItem mirrors the Express HostUserItem projection (sharesManager.ts).
type HostUserItem struct {
	Username     string `json:"username"`
	UID          int    `json:"uid"`
	GID          int    `json:"gid"`
	Gecos        string `json:"gecos"`
	Home         string `json:"home"`
	Shell        string `json:"shell"`
	IsSystemUser bool   `json:"isSystemUser"`
}

// parseEtcPasswd ports parseEtcPasswd from src/utils/sharesManager.ts.
//
// System users are UID < 1000 (or nobody 65534) that are NOT listed in
// /etc/subuid. The TS `hasInteractiveShell` local is computed but never used in
// the classification, so it is intentionally omitted here.
func parseEtcPasswd(passwdContent, subuidContent string) []HostUserItem {
	if passwdContent == "" {
		return []HostUserItem{}
	}

	subuidUsers := make(map[string]struct{})
	if subuidContent != "" {
		for _, line := range strings.Split(subuidContent, "\n") {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" || strings.HasPrefix(trimmed, "#") {
				continue
			}
			parts := strings.Split(trimmed, ":")
			if len(parts) > 0 && strings.TrimSpace(parts[0]) != "" {
				subuidUsers[strings.TrimSpace(parts[0])] = struct{}{}
			}
		}
	}

	results := make([]HostUserItem, 0)
	for _, line := range strings.Split(passwdContent, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		parts := strings.Split(trimmed, ":")
		if len(parts) < 7 {
			continue
		}

		username := parts[0]
		uid, errUID := strconv.Atoi(parts[2])
		gid, errGID := strconv.Atoi(parts[3])
		if errUID != nil || errGID != nil {
			continue
		}
		gecos := parts[4]
		home := parts[5]
		shell := parts[6]

		isNobody := uid == 65534
		_, isInSubuid := subuidUsers[username]
		isSystemUser := (uid < 1000 || isNobody) && !isInSubuid

		results = append(results, HostUserItem{
			Username:     username,
			UID:          uid,
			GID:          gid,
			Gecos:        gecos,
			Home:         home,
			Shell:        shell,
			IsSystemUser: isSystemUser,
		})
	}

	return results
}

// handleUsers ports GET /api/nodes/:id/users.
func (a *API) handleUsers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	node, err := a.findNode(r.PathValue("id"))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": "Failed to retrieve host users"})
		return
	}

	_, runner, err := a.factsAndRunner(r.Context(), node.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
		return
	}

	out, err := runNodeCommand(r.Context(), runner, usersCmd)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": "Failed to retrieve host users"})
		return
	}

	passwdRaw, subuidRaw := splitMarker(out, "===SUBUID===")
	allUsers := parseEtcPasswd(passwdRaw, subuidRaw)

	interactiveUsers := make([]HostUserItem, 0, len(allUsers))
	for _, u := range allUsers {
		if !u.IsSystemUser {
			interactiveUsers = append(interactiveUsers, u)
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"success":  true,
		"users":    interactiveUsers,
		"allUsers": allUsers,
	})
}
