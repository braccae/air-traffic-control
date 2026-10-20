package readonlyapi

import (
	"net/http"

	"air-traffic-control/backend/pkg/glances"
)

// handleNodeGlances ports GET /api/nodes/:id/telemetry/glances (server.ts).
//
// Express resolves the node with a strict id match (404 when absent) and runs
// the four-tier degradation chain from src/utils/telemetryCollector.ts. This
// handler runs the same chain through pkg/glances over the real SSH runner and
// returns the identical {success, glances} / 502 {success, error} envelopes.
func (a *API) handleNodeGlances(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Express matches the id strictly here (`nodes.find(n => n.id === id)`) and
	// answers 404 for anything else — unlike findNode's first-host fallback.
	rec, ok, err := a.store.GetHostByID(r.PathValue("id"))
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"success": false, "error": err.Error()})
		return
	}
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"success": false, "error": "Node not found"})
		return
	}

	_, runner, err := a.factsAndRunner(r.Context(), rec.ID)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"success": false, "error": err.Error()})
		return
	}

	info := parseTelemetryInfo(rec)
	payload, err := glances.Collect(r.Context(), runner, glances.CollectOptions{
		Route: info.glanceRoute(),
		Port:  info.APIPort,
		Name:  info.Name,
	})
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"success": false, "error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"success": true, "glances": payload})
}
