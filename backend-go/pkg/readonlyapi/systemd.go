package readonlyapi

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"air-traffic-control/backend/pkg/plugin"
	"air-traffic-control/backend/pkg/systemd"
)

// systemdTimeout bounds a systemd read. Journal reads get a longer budget than
// plain state reads because journalctl has to index the tail it returns.
const (
	systemdStateTimeout   = 12 * time.Second
	systemdJournalTimeout = 25 * time.Second
)

// unitListResponse is the payload of GET /api/nodes/:id/systemd/units.
type unitListResponse struct {
	NodeID   string         `json:"nodeId"`
	Manager  string         `json:"manager"`
	Scope    string         `json:"scope"`
	Units    []systemd.Unit `json:"units"`
	Warnings []string       `json:"warnings,omitempty"`
}

// unitDetailResponse is the payload of GET /api/nodes/:id/systemd/unit.
type unitDetailResponse struct {
	NodeID string        `json:"nodeId"`
	Scope  string        `json:"scope"`
	State  systemd.State `json:"state"`
}

// unitJournalResponse is the payload of GET /api/nodes/:id/systemd/journal.
type unitJournalResponse struct {
	NodeID  string                 `json:"nodeId"`
	Scope   string                 `json:"scope"`
	Unit    string                 `json:"unit,omitempty"`
	Count   int                    `json:"count"`
	Entries []systemd.JournalEntry `json:"entries"`
}

// systemdTarget resolves the node plus its scope query parameter.
func (a *API) systemdTarget(w http.ResponseWriter, r *http.Request) (plugin.Runner, systemd.Scope, bool) {
	ctx := r.Context()
	facts, runner, err := a.factsAndRunner(ctx, r.PathValue("id"))
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return nil, "", false
	}
	if facts.OSFamily != "" && facts.OSFamily != "linux" {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": fmt.Sprintf("systemd integration requires a Linux target (node OS family is %q)", facts.OSFamily),
		})
		return nil, "", false
	}
	// "all" is not a systemd scope: it asks for both managers. Validate the rest
	// strictly and let the caller branch on the raw value.
	rawScope := r.URL.Query().Get("scope")
	if strings.EqualFold(strings.TrimSpace(rawScope), "all") {
		rawScope = string(systemd.ScopeSystem)
	}
	scope, err := systemd.ParseScope(rawScope)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return nil, "", false
	}
	return runner, scope, true
}

// withSelectedUser wraps the runner so user-scoped reads target the operator's
// chosen per-user manager (?uid=<id>) instead of the SSH login user's.
func withSelectedUser(run plugin.Runner, r *http.Request, scope systemd.Scope) plugin.Runner {
	uid := strings.TrimSpace(r.URL.Query().Get("uid"))
	if uid == "" || scope != systemd.ScopeUser {
		return run
	}
	return systemd.UserRunner{Base: run, UID: uid}
}

// handleSystemdUsers lists the per-user managers on the host.
func (a *API) handleSystemdUsers(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), systemdStateTimeout)
	defer cancel()

	_, runner, err := a.factsAndRunner(ctx, r.PathValue("id"))
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	users, err := systemd.ListUserManagers(ctx, runner)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"nodeId": r.PathValue("id"), "users": users})
}

// handleSystemdUnits serves the service unit list. scope=all returns the system
// manager's units followed by the user manager's units.
func (a *API) handleSystemdUnits(w http.ResponseWriter, r *http.Request) {
	runner, scope, ok := a.systemdTarget(w, r)
	if !ok {
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), systemdStateTimeout)
	defer cancel()

	runner = withSelectedUser(runner, r, scope)

	if strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("scope")), "all") {
		units, warnings, err := systemd.ListAllScopes(ctx, runner)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, unitListResponse{
			NodeID: r.PathValue("id"), Manager: "systemd", Scope: "all",
			Units: units, Warnings: warnings,
		})
		return
	}

	units, err := systemd.ListUnits(ctx, runner, scope)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, unitListResponse{
		NodeID: r.PathValue("id"), Manager: "systemd", Scope: scope.String(), Units: units,
	})
}

// handleSystemdUnit serves one unit's state from systemd's property interface.
func (a *API) handleSystemdUnit(w http.ResponseWriter, r *http.Request) {
	runner, scope, ok := a.systemdTarget(w, r)
	if !ok {
		return
	}
	runner = withSelectedUser(runner, r, scope)
	unit := strings.TrimSpace(r.URL.Query().Get("unit"))
	if unit == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "unit query parameter is required"})
		return
	}
	if _, err := systemd.NormalizeUnitName(unit); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), systemdStateTimeout)
	defer cancel()

	state, err := systemd.ShowUnit(ctx, runner, scope, unit)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, unitDetailResponse{
		NodeID: r.PathValue("id"), Scope: scope.String(), State: state,
	})
}

// handleSystemdJournal serves structured journald records for one unit.
func (a *API) handleSystemdJournal(w http.ResponseWriter, r *http.Request) {
	runner, scope, ok := a.systemdTarget(w, r)
	if !ok {
		return
	}

	runner = withSelectedUser(runner, r, scope)

	opts := systemd.JournalOptions{
		Unit:     strings.TrimSpace(r.URL.Query().Get("unit")),
		Priority: r.URL.Query().Get("priority"),
		Lines:    200,
	}
	if opts.Unit != "" {
		if _, err := systemd.NormalizeUnitName(opts.Unit); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("lines")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": fmt.Sprintf("invalid lines value %q", raw)})
			return
		}
		opts.Lines = parsed
	}

	ctx, cancel := context.WithTimeout(r.Context(), systemdJournalTimeout)
	defer cancel()

	entries, err := systemd.Journal(ctx, runner, scope, opts)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, unitJournalResponse{
		NodeID: r.PathValue("id"), Scope: scope.String(), Unit: opts.Unit,
		Count: len(entries), Entries: entries,
	})
}
