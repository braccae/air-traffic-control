// Package readonlyapi ports the READ-ONLY Express routes from server.ts into the
// Go daemon. Every handler runs the SAME shell command the Express handler runs,
// over the REAL SSH runner, and shapes the SAME JSON envelope so the wire
// contract is identical. Nothing here fabricates data: if the host is
// unreachable the handler returns an honest error.
package readonlyapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"air-traffic-control/backend/pkg/hostcache"
	"air-traffic-control/backend/pkg/plugin"
)

// Options configures the read-only API.
type Options struct {
	// Store is the shared hosts-cache.sqlite store.
	Store *hostcache.Store
	// DBPath overrides the hosts-cache.sqlite path used to build the node resolver.
	DBPath string
}

// API serves the ported read-only routes.
type API struct {
	store          *hostcache.Store
	resolver       *plugin.SQLiteNodeResolver
	runnerResolver plugin.RunnerResolver
}

// New constructs the read-only API. It returns an error if the node resolver
// cannot be built, so the daemon fails loudly at startup rather than 500ing later.
func New(opts Options) (*API, error) {
	if opts.Store == nil {
		return nil, fmt.Errorf("readonlyapi: store is required")
	}

	res, err := plugin.NewSQLiteNodeResolver(plugin.ResolveDBPath(opts.DBPath))
	if err != nil {
		return nil, fmt.Errorf("readonlyapi: node resolver unavailable: %w", err)
	}

	return &API{
		store:          opts.Store,
		resolver:       res,
		runnerResolver: &plugin.DefaultRunnerResolver{},
	}, nil
}

// Register mounts every ported route onto mux. Each route uses a more specific
// pattern than the plugin API's "/api/nodes/" subtree so the two coexist without
// a ServeMux conflict.
func (a *API) Register(mux *http.ServeMux) {
	// NOTE: /api/telemetry and /api/nodes/:id/telemetry/glances are DEFERRED to
	// Phase 2 — they depend on the telemetryCollector.ts multi-tier chain
	// (glances REST/SSH/CLI + PCP + cockpit-bridge) and the client NodeItem.specs
	// model. See PHASE1_REPORT.md.
	mux.HandleFunc("/api/system/logs", a.handleSystemLogs)
	mux.HandleFunc("/api/podman/logs", a.handlePodmanLogs)
	mux.HandleFunc("/api/tailscale/derp-map", a.handleDerpMap)

	mux.HandleFunc("/api/nodes/{id}/storage", a.handleStorage)
	mux.HandleFunc("/api/nodes/{id}/shares", a.handleShares)
	mux.HandleFunc("/api/nodes/{id}/system-logs", a.handleSystemLogs)
	mux.HandleFunc("/api/nodes/{id}/tailscale", a.handleNodeTailscale)
	mux.HandleFunc("/api/nodes/{id}/interfaces", a.handleInterfaces)
	mux.HandleFunc("/api/nodes/{id}/disks", a.handleDisks)
	mux.HandleFunc("/api/nodes/{id}/vms", a.handleVMs)
	mux.HandleFunc("/api/nodes/{id}/firewall", a.handleFirewall)
}

// nodeRecord is the subset of a host row this package needs for findNode parity.
type nodeRecord struct {
	ID            string
	Name          string
	ShortName     string
	IP            string
	TailscaleIP   string
	TailscaleHost string
}

// findNode mirrors server.ts findNode(): match on id | name | shortName |
// tailscaleHost | tailscaleIp | ip (case-insensitive) and fall back to the first
// host when nothing matches, exactly like the Express helper.
func (a *API) findNode(query string) (nodeRecord, error) {
	hosts, err := a.store.GetAllHosts()
	if err != nil {
		return nodeRecord{}, fmt.Errorf("failed to read hosts: %w", err)
	}
	if len(hosts) == 0 {
		return nodeRecord{}, fmt.Errorf("no hosts configured")
	}

	if query == "" {
		h := hosts[0]
		return nodeRecord{ID: h.ID, Name: h.Name, ShortName: h.ShortName, IP: h.IP, TailscaleIP: h.TailscaleIP, TailscaleHost: h.TailscaleHost}, nil
	}

	q := strings.ToLower(query)
	for _, h := range hosts {
		if strings.ToLower(h.ID) == q ||
			strings.ToLower(h.Name) == q ||
			strings.ToLower(h.ShortName) == q ||
			strings.ToLower(h.TailscaleHost) == q ||
			h.TailscaleIP == q ||
			h.IP == q {
			return nodeRecord{ID: h.ID, Name: h.Name, ShortName: h.ShortName, IP: h.IP, TailscaleIP: h.TailscaleIP, TailscaleHost: h.TailscaleHost}, nil
		}
	}

	h := hosts[0]
	return nodeRecord{ID: h.ID, Name: h.Name, ShortName: h.ShortName, IP: h.IP, TailscaleIP: h.TailscaleIP, TailscaleHost: h.TailscaleHost}, nil
}

// factsAndRunner resolves HostFacts + a Runner for the given node id.
func (a *API) factsAndRunner(ctx context.Context, nodeID string) (plugin.HostFacts, plugin.Runner, error) {
	facts, ok, err := a.resolver.ResolveNode(nodeID)
	if err != nil {
		return plugin.HostFacts{}, nil, fmt.Errorf("failed to resolve node %s: %w", nodeID, err)
	}
	if !ok {
		return plugin.HostFacts{}, nil, fmt.Errorf("node %s not found", nodeID)
	}
	runner, err := a.runnerResolver.ResolveRunner(ctx, facts)
	if err != nil {
		return plugin.HostFacts{}, nil, fmt.Errorf("failed to resolve runner for node %s: %w", nodeID, err)
	}
	return facts, runner, nil
}

// runNodeCommand runs a command on the node with an 8s budget, matching the
// Express runNodeCommand default timeout.
func runNodeCommand(ctx context.Context, runner plugin.Runner, cmd string) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	return runner.RunCommand(cctx, cmd)
}

// splitMarker splits s on the first occurrence of marker into before/after,
// mirroring the Express `const [a, b] = out.split('===MARKER===')` idiom where
// only the first split matters for the ordered marker chain.
func splitMarker(s, marker string) (string, string) {
	idx := strings.Index(s, marker)
	if idx < 0 {
		return s, ""
	}
	return s[:idx], s[idx+len(marker):]
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// sectionCachedResponse assembles the Express "fromCache" envelope:
// {success:true, ...cachedData, fromCache:true, cachedAt:<ms>}.
func (a *API) sectionCachedResponse(w http.ResponseWriter, id, section string) bool {
	entry, ok, err := a.store.GetHostSection(id, section)
	if err != nil || !ok || len(entry.DataJSON) == 0 {
		return false
	}
	var data map[string]any
	if err := json.Unmarshal(entry.DataJSON, &data); err != nil {
		return false
	}
	resp := map[string]any{"success": true, "fromCache": true, "cachedAt": entry.UpdatedAt}
	for k, v := range data {
		resp[k] = v
	}
	writeJSON(w, http.StatusOK, resp)
	return true
}

// sectionResult marshals a result object into the section cache and returns the
// Express fresh envelope {success:true, ...result, fromCache:false, cachedAt:now}.
func (a *API) sectionResult(w http.ResponseWriter, id, section string, result map[string]any) {
	if raw, err := json.Marshal(result); err == nil {
		_ = a.store.UpsertHostSection(id, section, raw)
	}
	resp := map[string]any{"success": true, "fromCache": false, "cachedAt": time.Now().UnixMilli()}
	for k, v := range result {
		resp[k] = v
	}
	writeJSON(w, http.StatusOK, resp)
}

// sectionIsFresh reports whether the cached section row is within its TTL and
// refresh was not requested.
func (a *API) sectionIsFresh(id, section string, refresh bool) bool {
	if refresh {
		return false
	}
	entry, ok, err := a.store.GetHostSection(id, section)
	if err != nil || !ok {
		return false
	}
	return hostcache.Fresh(entry.UpdatedAt, hostcache.SectionTTL(section))
}
