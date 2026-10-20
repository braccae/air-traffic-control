package plugin

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// PluginSummary represents the summary returned by GET /api/plugins
type PluginSummary struct {
	ID           string       `json:"id"`
	Kind         Kind         `json:"kind"`
	Name         string       `json:"name"`
	Label        string       `json:"label"`
	Icon         string       `json:"icon"`
	Description  string       `json:"description"`
	Order        int          `json:"order"`
	Requirements Requirements `json:"requirements"`
	Actions      []ActionSpec `json:"actions,omitempty"`
	CanRead      bool         `json:"canRead"`
	CanAct       bool         `json:"canAct"`
}

// NodePluginItem represents a detected plugin in GET /api/nodes/:id/plugins
type NodePluginItem struct {
	ID        string    `json:"id"`
	Kind      Kind      `json:"kind"`
	Name      string    `json:"name"`
	Label     string    `json:"label"`
	Icon      string    `json:"icon"`
	Section   Kind      `json:"section"`
	Order     int       `json:"order"`
	Detection Detection `json:"detection"`
}

// NodeModuleItem represents a plugin in GET /api/nodes/:id/modules
type NodeModuleItem struct {
	ID           string       `json:"id"`
	Kind         Kind         `json:"kind"`
	Name         string       `json:"name"`
	Label        string       `json:"label"`
	Icon         string       `json:"icon"`
	Section      Kind         `json:"section"`
	Order        int          `json:"order"`
	Status       string       `json:"status"` // "active", "available", "unsupported"
	Reason       string       `json:"reason,omitempty"`
	Requirements Requirements `json:"requirements"`
	Detection    Detection    `json:"detection,omitempty"`
}

// NodeResolver resolves node ID to HostFacts
type NodeResolver interface {
	ResolveNode(nodeID string) (HostFacts, bool, error)
}

// RunnerResolver creates or retrieves a Runner for given HostFacts
type RunnerResolver interface {
	ResolveRunner(ctx context.Context, facts HostFacts) (Runner, error)
}

// DefaultRunnerResolver resolves a Runner using registered connection plugins
type DefaultRunnerResolver struct{}

// ResolveRunner determines the appropriate connection plugin and returns a Runner
func (d *DefaultRunnerResolver) ResolveRunner(ctx context.Context, facts HostFacts) (Runner, error) {
	connPluginID := facts.ActiveConnection
	if connPluginID == "" {
		// Auto-detect based on route
		route := strings.TrimSpace(facts.HostRoute)
		if route == "127.0.0.1" || route == "localhost" || route == "" {
			connPluginID = "connection/local"
		} else if strings.Contains(route, ".ts.net") || strings.HasPrefix(route, "100.") {
			connPluginID = "connection/tailscale"
		} else {
			connPluginID = "connection/ssh"
		}
	}

	p, ok := Get(connPluginID)
	if !ok {
		// Fallback to connection/ssh or connection/local
		if facts.HostRoute == "127.0.0.1" || facts.HostRoute == "localhost" || facts.HostRoute == "" {
			p, ok = Get("connection/local")
		} else {
			p, ok = Get("connection/ssh")
		}
		if !ok {
			return nil, fmt.Errorf("no connection plugin available for %s", connPluginID)
		}
	}

	type runnerFactory interface {
		NewRunner(h HostFacts) (Runner, error)
	}

	if rf, ok := p.(runnerFactory); ok {
		return rf.NewRunner(facts)
	}

	return nil, fmt.Errorf("connection plugin %s does not implement NewRunner", connPluginID)
}

// SQLiteNodeResolver reads nodes from data/hosts-cache.sqlite
type SQLiteNodeResolver struct {
	dbPath string
	db     *sql.DB
	mu     sync.Mutex
}

// NewSQLiteNodeResolver creates a new SQLiteNodeResolver
func NewSQLiteNodeResolver(dbPath string) (*SQLiteNodeResolver, error) {
	if _, err := os.Stat(dbPath); err != nil {
		return nil, fmt.Errorf("sqlite database not found at %s: %w", dbPath, err)
	}

	db, err := sql.Open("sqlite", dbPath+"?mode=ro&_journal_mode=WAL")
	if err != nil {
		return nil, fmt.Errorf("failed to open sqlite database: %w", err)
	}

	return &SQLiteNodeResolver{
		dbPath: dbPath,
		db:     db,
	}, nil
}

// ResolveDBPath locates the hosts cache database. An explicit path or the
// ATC_DB_PATH environment variable takes precedence. Otherwise it walks upward
// from the current working directory looking for data/hosts-cache.sqlite, so
// the daemon works whether it is launched from the repo root or from
// backend-go/. It never returns an empty string.
func ResolveDBPath(explicit string) string {
	if explicit != "" {
		return explicit
	}
	if env := os.Getenv("ATC_DB_PATH"); env != "" {
		return env
	}

	const rel = "data/hosts-cache.sqlite"
	if _, err := os.Stat(rel); err == nil {
		return rel
	}

	if wd, err := os.Getwd(); err == nil {
		dir := wd
		for {
			candidate := filepath.Join(dir, rel)
			if _, err := os.Stat(candidate); err == nil {
				return candidate
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}

	return rel
}

// Close closes the underlying SQLite database
func (r *SQLiteNodeResolver) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.db != nil {
		return r.db.Close()
	}
	return nil
}

type sqliteNodeRecord struct {
	ID              string            `json:"id"`
	Name            string            `json:"name"`
	ShortName       string            `json:"shortName"`
	IP              string            `json:"ip"`
	Port            int               `json:"port"`
	SSHUser         string            `json:"sshUser"`
	TailscaleIP     string            `json:"tailscaleIp"`
	TailscaleHost   string            `json:"tailscaleHost"`
	NodeType        string            `json:"nodeType"`
	PrivilegeMethod string            `json:"privilegeMethod"`
	PluginOverrides map[string]string `json:"pluginOverrides"`
}

// ResolveNode queries SQLite for node facts
func (r *SQLiteNodeResolver) ResolveNode(nodeID string) (HostFacts, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	var id, ip, sshUser, tailscaleIP, tailscaleHost, nodeJSON string
	var port int

	row := r.db.QueryRow("SELECT id, ip, port, ssh_user, tailscale_ip, tailscale_host, node_json FROM hosts WHERE id = ? OR name = ? OR short_name = ?", nodeID, nodeID, nodeID)
	err := row.Scan(&id, &ip, &port, &sshUser, &tailscaleIP, &tailscaleHost, &nodeJSON)
	if err != nil {
		if err == sql.ErrNoRows {
			return HostFacts{}, false, nil
		}
		return HostFacts{}, false, err
	}

	var parsed sqliteNodeRecord
	if err := json.Unmarshal([]byte(nodeJSON), &parsed); err != nil {
		parsed.ID = id
		parsed.IP = ip
		parsed.Port = port
		parsed.SSHUser = sshUser
		parsed.TailscaleIP = tailscaleIP
		parsed.TailscaleHost = tailscaleHost
	}

	route := parsed.TailscaleHost
	if route == "" {
		route = parsed.TailscaleIP
	}
	if route == "" {
		route = parsed.IP
	}
	if route == "" {
		route = "127.0.0.1"
	}

	osFamily := "linux"
	if parsed.NodeType == "windows" {
		osFamily = "windows"
	} else if parsed.NodeType == "router_switch" {
		osFamily = "router_switch"
	}

	activeConn := ""
	if parsed.PluginOverrides != nil && parsed.PluginOverrides["connection"] != "" {
		activeConn = parsed.PluginOverrides["connection"]
	} else if route == "127.0.0.1" || route == "localhost" {
		activeConn = "connection/local"
	} else if strings.Contains(route, ".ts.net") || strings.HasPrefix(route, "100.") {
		activeConn = "connection/tailscale"
	} else {
		activeConn = "connection/ssh"
	}

	activeElev := ""
	if parsed.PluginOverrides != nil && parsed.PluginOverrides["elevation"] != "" {
		activeElev = parsed.PluginOverrides["elevation"]
	} else if parsed.PrivilegeMethod != "" {
		activeElev = "elevation/" + parsed.PrivilegeMethod
	} else {
		activeElev = "elevation/run0"
	}

	activePkg := ""
	if parsed.PluginOverrides != nil && parsed.PluginOverrides["package"] != "" {
		activePkg = parsed.PluginOverrides["package"]
	}

	overrides := make(map[string]string)
	if parsed.PluginOverrides != nil {
		for k, v := range parsed.PluginOverrides {
			overrides[k] = v
		}
	}
	if parsed.SSHUser != "" {
		overrides["user"] = parsed.SSHUser
	}
	if parsed.Port > 0 {
		overrides["port"] = fmt.Sprintf("%d", parsed.Port)
	}

	facts := HostFacts{
		NodeID:           parsed.ID,
		OSFamily:         osFamily,
		HostRoute:        route,
		ActiveConnection: activeConn,
		ActiveElevation:  activeElev,
		ActivePackage:    activePkg,
		Overrides:        overrides,
	}

	return facts, true, nil
}

// In-memory Probe Cache Entry
type cachedDetection struct {
	detection Detection
	expiresAt time.Time
}

// ProbeCache stores probe results with kind-specific TTLs
type ProbeCache struct {
	mu      sync.RWMutex
	entries map[string]cachedDetection // key: "<nodeID>:<pluginID>"
}

// NewProbeCache creates a new ProbeCache
func NewProbeCache() *ProbeCache {
	return &ProbeCache{
		entries: make(map[string]cachedDetection),
	}
}

// TTLForKind returns the cache TTL according to AGENTS.md §4:
// Container: 20s, Network: 30s, Firewall: 45s, Storage/Shares/Disks/VMs: 60s, Default: 60s
func TTLForKind(kind Kind) time.Duration {
	switch kind {
	case KindContainer:
		return 20 * time.Second
	case KindNetwork:
		return 30 * time.Second
	case KindFirewall:
		return 45 * time.Second
	case KindStorage, KindShare, KindDisk, KindVM, KindMount, KindBackup:
		return 60 * time.Second
	default:
		return 60 * time.Second
	}
}

// Get retrieves a cached detection if valid
func (c *ProbeCache) Get(nodeID, pluginID string) (Detection, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	key := nodeID + ":" + pluginID
	entry, ok := c.entries[key]
	if !ok {
		return Detection{}, false
	}
	if time.Now().After(entry.expiresAt) {
		return Detection{}, false
	}
	return entry.detection, true
}

// Set stores a detection with TTL
func (c *ProbeCache) Set(nodeID, pluginID string, kind Kind, det Detection) {
	c.mu.Lock()
	defer c.mu.Unlock()

	key := nodeID + ":" + pluginID
	c.entries[key] = cachedDetection{
		detection: det,
		expiresAt: time.Now().Add(TTLForKind(kind)),
	}
}

// Invalidate clears cache entries for a node (or all nodes if nodeID is empty)
func (c *ProbeCache) Invalidate(nodeID string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if nodeID == "" || nodeID == "all" {
		c.entries = make(map[string]cachedDetection)
		return
	}

	prefix := nodeID + ":"
	for k := range c.entries {
		if strings.HasPrefix(k, prefix) {
			delete(c.entries, k)
		}
	}
}

// API provides HTTP handlers for plugin management
type API struct {
	registry       *Registry
	nodeResolver   NodeResolver
	runnerResolver RunnerResolver
	cache          *ProbeCache
}

// APIOptions holds dependencies for initializing API
type APIOptions struct {
	Registry       *Registry
	NodeResolver   NodeResolver
	RunnerResolver RunnerResolver
	Cache          *ProbeCache
	// DBPath optionally overrides the hosts-cache.sqlite location used to
	// auto-wire the default SQLite-backed NodeResolver. When empty, ATC_DB_PATH
	// is consulted, then the path is resolved relative to the current working
	// directory by walking upward.
	DBPath string
}

// NewAPI constructs a new API instance
func NewAPI(opts APIOptions) *API {
	reg := opts.Registry
	if reg == nil {
		reg = defaultRegistry
	}

	nodeRes := opts.NodeResolver
	if nodeRes == nil {
		// Auto-wire the SQLite-backed resolver. Resolve the database path
		// robustly rather than relying on a CWD-relative literal, because the
		// daemon may be launched from the repo root or from backend-go/.
		dbPath := ResolveDBPath(opts.DBPath)
		if r, err := NewSQLiteNodeResolver(dbPath); err == nil {
			nodeRes = r
		} else {
			log.Printf("plugin: node resolver unavailable (%v); endpoints that require a node resolver will fail", err)
		}
	}

	runnerRes := opts.RunnerResolver
	if runnerRes == nil {
		runnerRes = &DefaultRunnerResolver{}
	}

	cache := opts.Cache
	if cache == nil {
		cache = NewProbeCache()
	}

	return &API{
		registry:       reg,
		nodeResolver:   nodeRes,
		runnerResolver: runnerRes,
		cache:          cache,
	}
}

// RegisterRoutes mounts all plugin management endpoints onto the given ServeMux
func (a *API) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/plugins", a.handleListPlugins)
	mux.HandleFunc("/api/nodes/", a.handleNodeRoutes)
}

// HandleListPlugins implements GET /api/plugins
func (a *API) HandleListPlugins(w http.ResponseWriter, r *http.Request) {
	a.handleListPlugins(w, r)
}

// NodeRoutesHandler returns an http.Handler that handles /api/nodes/ plugin routes.
func (a *API) NodeRoutesHandler() http.Handler {
	return http.HandlerFunc(a.handleNodeRoutes)
}

// handleListPlugins implements GET /api/plugins
func (a *API) handleListPlugins(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	all := a.registry.List()
	summaries := make([]PluginSummary, 0, len(all))

	for _, p := range all {
		meta := p.Meta()
		summary := PluginSummary{
			ID:           meta.ID,
			Kind:         meta.Kind,
			Name:         meta.Name,
			Label:        meta.Label,
			Icon:         meta.Icon,
			Description:  meta.Description,
			Order:        meta.Order,
			Requirements: p.Requirements(),
		}

		if _, ok := p.(Reader); ok {
			summary.CanRead = true
		}
		if actor, ok := p.(Actor); ok {
			summary.CanAct = true
			summary.Actions = actor.Actions()
		}

		summaries = append(summaries, summary)
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(summaries)
}

// handleNodeRoutes handles dynamic paths under /api/nodes/
func (a *API) handleNodeRoutes(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/nodes/")
	parts := strings.Split(strings.Trim(path, "/"), "/")

	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}

	nodeID := parts[0]

	// /api/nodes/:id/exec
	if len(parts) == 2 && parts[1] == "exec" && r.Method == http.MethodPost {
		a.handleNodeExec(w, r, nodeID)
		return
	}

	// /api/nodes/:id/plugins/refresh
	if len(parts) == 3 && parts[1] == "plugins" && parts[2] == "refresh" && r.Method == http.MethodPost {
		a.handleRefresh(w, r, nodeID)
		return
	}

	// /api/nodes/:id/plugins
	if len(parts) == 2 && parts[1] == "plugins" && r.Method == http.MethodGet {
		a.handleNodePlugins(w, r, nodeID)
		return
	}

	// /api/nodes/:id/modules
	if len(parts) == 2 && parts[1] == "modules" && r.Method == http.MethodGet {
		a.handleNodeModules(w, r, nodeID)
		return
	}

	// /api/nodes/:id/plugins/:kind/:name
	if len(parts) == 4 && parts[1] == "plugins" && r.Method == http.MethodGet {
		kind := Kind(parts[2])
		name := parts[3]
		a.handlePluginRead(w, r, nodeID, kind, name)
		return
	}

	// /api/nodes/:id/plugins/:kind/:name/actions/:action
	if len(parts) == 6 && parts[1] == "plugins" && parts[4] == "actions" && r.Method == http.MethodPost {
		kind := Kind(parts[2])
		name := parts[3]
		action := parts[5]
		a.handlePluginAction(w, r, nodeID, kind, name, action)
		return
	}

	http.NotFound(w, r)
}

// ExecRequestBody holds the command payload for POST /api/nodes/:id/exec
type ExecRequestBody struct {
	Command string `json:"command"`
}

// handleNodeExec executes a command over the node's pooled persistent connection
func (a *API) handleNodeExec(w http.ResponseWriter, r *http.Request, nodeID string) {
	ctx := r.Context()
	var req ExecRequestBody
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Command) == "" {
		http.Error(w, "command is required", http.StatusBadRequest)
		return
	}

	_, runner, err := a.resolveFactsAndRunner(ctx, nodeID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	out, err := runner.RunCommand(ctx, req.Command)
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": false,
			"error":   err.Error(),
			"stdout":  out,
		})
		return
	}

	_ = json.NewEncoder(w).Encode(map[string]any{
		"success": true,
		"stdout":  out,
	})
}

// handleRefresh implements POST /api/nodes/:id/plugins/refresh
func (a *API) handleRefresh(w http.ResponseWriter, r *http.Request, nodeID string) {
	a.cache.Invalidate(nodeID)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"success": true,
		"nodeId":  nodeID,
		"message": "Probe cache invalidated",
	})
}

func (a *API) resolveFactsAndRunner(ctx context.Context, nodeID string) (HostFacts, Runner, error) {
	if a.nodeResolver == nil {
		return HostFacts{}, nil, fmt.Errorf("no node resolver configured")
	}

	facts, ok, err := a.nodeResolver.ResolveNode(nodeID)
	if err != nil {
		return HostFacts{}, nil, fmt.Errorf("failed to resolve node %s: %w", nodeID, err)
	}
	if !ok {
		return HostFacts{}, nil, fmt.Errorf("node %s not found", nodeID)
	}

	runner, err := a.runnerResolver.ResolveRunner(ctx, facts)
	if err != nil {
		return facts, nil, fmt.Errorf("failed to resolve runner for node %s: %w", nodeID, err)
	}

	return facts, runner, nil
}

// handleNodePlugins implements GET /api/nodes/:id/plugins
func (a *API) handleNodePlugins(w http.ResponseWriter, r *http.Request, nodeID string) {
	ctx := r.Context()
	facts, runner, err := a.resolveFactsAndRunner(ctx, nodeID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	compatible := a.registry.FilterCompatible(facts)
	var result []NodePluginItem

	for _, p := range compatible {
		meta := p.Meta()

		det, ok := a.cache.Get(nodeID, meta.ID)
		if !ok {
			var probeErr error
			det, probeErr = p.Probe(ctx, runner, facts)
			if probeErr != nil {
				det = Detection{
					Detected: false,
					Status:   "ERROR",
					Error:    probeErr.Error(),
				}
			}
			a.cache.Set(nodeID, meta.ID, meta.Kind, det)
		}

		if det.Detected {
			result = append(result, NodePluginItem{
				ID:        meta.ID,
				Kind:      meta.Kind,
				Name:      meta.Name,
				Label:     meta.Label,
				Icon:      meta.Icon,
				Section:   meta.Kind,
				Order:     meta.Order,
				Detection: det,
			})
		}
	}

	sort.Slice(result, func(i, j int) bool {
		if result[i].Section != result[j].Section {
			return result[i].Section < result[j].Section
		}
		if result[i].Order != result[j].Order {
			return result[i].Order < result[j].Order
		}
		return result[i].Name < result[j].Name
	})

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}

// handleNodeModules implements GET /api/nodes/:id/modules
func (a *API) handleNodeModules(w http.ResponseWriter, r *http.Request, nodeID string) {
	ctx := r.Context()
	facts, runner, err := a.resolveFactsAndRunner(ctx, nodeID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	all := a.registry.List()
	var result []NodeModuleItem

	for _, p := range all {
		meta := p.Meta()
		isCompat := p.Compatible(facts)

		item := NodeModuleItem{
			ID:           meta.ID,
			Kind:         meta.Kind,
			Name:         meta.Name,
			Label:        meta.Label,
			Icon:         meta.Icon,
			Section:      meta.Kind,
			Order:        meta.Order,
			Requirements: p.Requirements(),
		}

		if !isCompat {
			item.Status = "unsupported"
			item.Reason = fmt.Sprintf("Incompatible with host OS (%s) or connection", facts.OSFamily)
			result = append(result, item)
			continue
		}

		det, ok := a.cache.Get(nodeID, meta.ID)
		if !ok {
			var probeErr error
			det, probeErr = p.Probe(ctx, runner, facts)
			if probeErr != nil {
				det = Detection{
					Detected: false,
					Status:   "ERROR",
					Error:    probeErr.Error(),
				}
			}
			a.cache.Set(nodeID, meta.ID, meta.Kind, det)
		}

		item.Detection = det
		if det.Detected {
			item.Status = "active"
		} else {
			item.Status = "available"
			if det.Error != "" {
				item.Reason = det.Error
			} else {
				item.Reason = "Package or subsystem not detected on host"
			}
		}

		result = append(result, item)
	}

	sort.Slice(result, func(i, j int) bool {
		if result[i].Section != result[j].Section {
			return result[i].Section < result[j].Section
		}
		if result[i].Order != result[j].Order {
			return result[i].Order < result[j].Order
		}
		return result[i].Name < result[j].Name
	})

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}

// handlePluginRead implements GET /api/nodes/:id/plugins/:kind/:name
func (a *API) handlePluginRead(w http.ResponseWriter, r *http.Request, nodeID string, kind Kind, name string) {
	ctx := r.Context()
	pluginID := fmt.Sprintf("%s/%s", kind, name)

	p, ok := a.registry.Get(pluginID)
	if !ok {
		http.Error(w, fmt.Sprintf("plugin %s not found", pluginID), http.StatusNotFound)
		return
	}

	reader, ok := p.(Reader)
	if !ok {
		http.Error(w, fmt.Sprintf("plugin %s does not implement Reader", pluginID), http.StatusBadRequest)
		return
	}

	facts, runner, err := a.resolveFactsAndRunner(ctx, nodeID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	data, err := reader.Read(ctx, runner, facts)
	if err != nil {
		http.Error(w, fmt.Sprintf("read failed for %s: %v", pluginID, err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(data)
}

// ActionRequestBody holds the options map sent in POST action compile request
type ActionRequestBody struct {
	Options map[string]any `json:"options"`
}

// handlePluginAction implements POST /api/nodes/:id/plugins/:kind/:name/actions/:action
func (a *API) handlePluginAction(w http.ResponseWriter, r *http.Request, nodeID string, kind Kind, name string, action string) {
	ctx := r.Context()
	pluginID := fmt.Sprintf("%s/%s", kind, name)

	p, ok := a.registry.Get(pluginID)
	if !ok {
		http.Error(w, fmt.Sprintf("plugin %s not found", pluginID), http.StatusNotFound)
		return
	}

	actor, ok := p.(Actor)
	if !ok {
		http.Error(w, fmt.Sprintf("plugin %s does not implement Actor", pluginID), http.StatusBadRequest)
		return
	}

	// Validate action against supported actions
	var actionSpec *ActionSpec
	for _, spec := range actor.Actions() {
		if spec.Name == action {
			actionSpec = &spec
			break
		}
	}
	if actionSpec == nil {
		http.Error(w, fmt.Sprintf("action %q not supported by plugin %s", action, pluginID), http.StatusBadRequest)
		return
	}

	var reqBody ActionRequestBody
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&reqBody)
	}
	if reqBody.Options == nil {
		reqBody.Options = make(map[string]any)
	}

	// Validate options against option schema
	for _, opt := range actionSpec.Options {
		val, exists := reqBody.Options[opt.Name]
		if opt.Required && (!exists || val == nil || val == "") {
			http.Error(w, fmt.Sprintf("missing required option %q for action %q", opt.Name, action), http.StatusBadRequest)
			return
		}
	}

	facts, _, err := a.resolveFactsAndRunner(ctx, nodeID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	task, err := actor.Compile(facts, action, reqBody.Options)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to compile action %s: %v", action, err), http.StatusBadRequest)
		return
	}

	// Invalidate cache for this node and kind upon action compilation
	a.cache.Invalidate(nodeID)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(task)
}
