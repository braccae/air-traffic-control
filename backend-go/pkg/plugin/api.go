package plugin

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"

	"air-traffic-control/backend/pkg/collections"
	"air-traffic-control/backend/pkg/hostcache"
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

// storeNodeResolver wraps a hostcache.Store to implement NodeResolver
type storeNodeResolver struct {
	Store *hostcache.Store
}

// NewStoreNodeResolver returns a NodeResolver backed by an already-open host
// store. Use this instead of the SQLite-backed resolver when the store is
// in-memory (path ":memory:") and has no file to open a second handle against.
func NewStoreNodeResolver(store *hostcache.Store) NodeResolver {
	return &storeNodeResolver{Store: store}
}

func (r *storeNodeResolver) ResolveNode(nodeID string) (HostFacts, bool, error) {
	record, ok, err := r.Store.GetHostByID(nodeID)
	if err != nil {
		return HostFacts{}, false, err
	}
	if !ok {
		all, err := r.Store.GetAllHosts()
		if err != nil {
			return HostFacts{}, false, err
		}
		q := strings.ToLower(strings.TrimSpace(nodeID))
		for _, h := range all {
			if strings.ToLower(h.ID) == q ||
				strings.ToLower(h.Name) == q ||
				strings.ToLower(h.ShortName) == q ||
				strings.ToLower(h.TailscaleHost) == q ||
				strings.ToLower(h.TailscaleIP) == q ||
				strings.ToLower(h.IP) == q {
				record = h
				ok = true
				break
			}
		}
	}
	if !ok {
		return HostFacts{}, false, nil
	}

	portStr := ""
	if record.Port > 0 {
		portStr = fmt.Sprintf("%d", record.Port)
	}
	facts, err := buildHostFacts(record.ID, record.IP, portStr, record.SSHUser, record.TailscaleIP, record.TailscaleHost, record.VaultSecretID, record.NodeJSON)
	if err != nil {
		return facts, false, err
	}
	return facts, true, nil
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
// ATC_DB_PATH environment variable takes precedence. Otherwise it prefers the
// inventory at the repository root, then walks upward from the current working
// directory. Preferring the root stops a daemon launched from backend-go/ from
// silently forking the inventory into backend-go/data/hosts-cache.sqlite.
// It never returns an empty string.
func ResolveDBPath(explicit string) string {
	if explicit != "" {
		return explicit
	}
	if env := os.Getenv("ATC_DB_PATH"); env != "" {
		return env
	}

	const rel = "data/hosts-cache.sqlite"

	if root, ok := repoRoot(); ok {
		candidate := filepath.Join(root, rel)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}

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

// repoRoot walks up from the working directory looking for the monorepo marker
// (backend-go/go.mod) so the coordinator and the daemon agree on the inventory
// location regardless of which directory they were started from.
func repoRoot() (string, bool) {
	wd, err := os.Getwd()
	if err != nil {
		return "", false
	}
	dir := wd
	for {
		if fi, err := os.Stat(filepath.Join(dir, "backend-go", "go.mod")); err == nil && !fi.IsDir() {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
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
	APIPort         int               `json:"apiPort"`
	APIUser         string            `json:"apiUser"`
	VaultSecretID   string            `json:"vaultSecretId"`
	AuthMethod      string            `json:"authMethod"`
	PluginOverrides map[string]string `json:"pluginOverrides"`

	// Explicit HostFacts fields (supported when NodeJSON carries direct HostFacts)
	OSFamily         string            `json:"osFamily"`
	HostRoute        string            `json:"hostRoute"`
	ActiveConnection string            `json:"activeConnection"`
	ActiveElevation  string            `json:"activeElevation"`
	ActivePackage    string            `json:"activePackage"`
	Overrides        map[string]string `json:"overrides"`
}

func buildHostFacts(id, ip, portStr, sshUser, tailscaleIP, tailscaleHost, vaultSecretID string, rawJSON []byte) (HostFacts, error) {
	var parsed sqliteNodeRecord
	if len(rawJSON) > 0 {
		_ = json.Unmarshal(rawJSON, &parsed)
	}
	if parsed.ID == "" {
		parsed.ID = id
	}
	if parsed.IP == "" {
		parsed.IP = ip
	}
	if parsed.Port <= 0 && portStr != "" {
		if p, err := strconv.Atoi(portStr); err == nil && p > 0 {
			parsed.Port = p
		}
	}
	if parsed.SSHUser == "" {
		parsed.SSHUser = sshUser
	}
	if parsed.TailscaleIP == "" {
		parsed.TailscaleIP = tailscaleIP
	}
	if parsed.TailscaleHost == "" {
		parsed.TailscaleHost = tailscaleHost
	}
	if parsed.VaultSecretID == "" {
		parsed.VaultSecretID = vaultSecretID
	}
	if parsed.Overrides == nil {
		parsed.Overrides = make(map[string]string)
	}
	if parsed.VaultSecretID != "" {
		parsed.Overrides["vaultSecretId"] = parsed.VaultSecretID
	}
	// Surface the declared auth method so connection plugins can tell a keyless
	// Tailscale SSH host (none-auth is expected) from a host that simply has no
	// credentials configured.
	if parsed.AuthMethod != "" {
		parsed.Overrides["authMethod"] = parsed.AuthMethod
	}

	isLocalHost := parsed.IP == "127.0.0.1" ||
		parsed.IP == "localhost" ||
		parsed.IP == "::1"

	route := strings.TrimSpace(parsed.HostRoute)
	if route == "" {
		if isLocalHost {
			route = "127.0.0.1"
		} else if parsed.TailscaleIP != "" {
			route = parsed.TailscaleIP
		} else if parsed.TailscaleHost != "" {
			route = parsed.TailscaleHost
		} else if parsed.IP != "" {
			route = parsed.IP
		} else {
			return HostFacts{}, fmt.Errorf("node %q has no routable address (no hostRoute, tailscaleIp, tailscaleHost, or ip)", id)
		}
	}

	osFamily := parsed.OSFamily
	if osFamily == "" {
		osFamily = "linux"
		if parsed.NodeType == "windows" {
			osFamily = "windows"
		} else if parsed.NodeType == "router_switch" {
			osFamily = "router_switch"
		}
	}

	activeConn := parsed.ActiveConnection
	if parsed.PluginOverrides != nil && parsed.PluginOverrides["connection"] != "" {
		activeConn = parsed.PluginOverrides["connection"]
	} else if activeConn == "" {
		if isLocalHost || route == "127.0.0.1" || route == "localhost" {
			activeConn = "connection/local"
		} else if strings.Contains(route, ".ts.net") || strings.HasPrefix(route, "100.") {
			activeConn = "connection/tailscale"
		} else {
			activeConn = "connection/ssh"
		}
	}

	activeElev := parsed.ActiveElevation
	if parsed.PluginOverrides != nil && parsed.PluginOverrides["elevation"] != "" {
		activeElev = parsed.PluginOverrides["elevation"]
	} else if activeElev == "" {
		if parsed.PrivilegeMethod != "" {
			activeElev = "elevation/" + parsed.PrivilegeMethod
		} else {
			activeElev = "elevation/run0"
		}
	}

	activePkg := parsed.ActivePackage
	if parsed.PluginOverrides != nil && parsed.PluginOverrides["package"] != "" {
		activePkg = parsed.PluginOverrides["package"]
	}

	overrides := make(map[string]string)
	for k, v := range parsed.Overrides {
		overrides[k] = v
	}
	for k, v := range parsed.PluginOverrides {
		overrides[k] = v
	}
	if parsed.SSHUser != "" {
		overrides["user"] = parsed.SSHUser
	}
	if parsed.IP != "" {
		overrides["ip"] = parsed.IP
		overrides["host"] = parsed.IP
	}
	if parsed.Port > 0 {
		overrides["port"] = fmt.Sprintf("%d", parsed.Port)
	}
	if parsed.APIPort > 0 {
		overrides["api_port"] = fmt.Sprintf("%d", parsed.APIPort)
	}
	if parsed.APIUser != "" {
		overrides["api_user"] = parsed.APIUser
	}

	return HostFacts{
		NodeID:           parsed.ID,
		OSFamily:         osFamily,
		HostRoute:        route,
		ActiveConnection: activeConn,
		ActiveElevation:  activeElev,
		ActivePackage:    activePkg,
		Overrides:        overrides,
	}, nil
}

// ResolveNode queries SQLite for node facts
func (r *SQLiteNodeResolver) ResolveNode(nodeID string) (HostFacts, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	var id string = ""
	var ip string = ""
	var sshUser string = ""
	var tailscaleIP string = ""
	var tailscaleHost string = ""
	var nodeJSON string = ""
	var vaultSecretID sql.NullString
	var port int

	row := r.db.QueryRow("SELECT id, ip, port, ssh_user, tailscale_ip, tailscale_host, vault_secret_id, node_json FROM hosts WHERE id = ? OR name = ? OR short_name = ?", nodeID, nodeID, nodeID)
	err := row.Scan(&id, &ip, &port, &sshUser, &tailscaleIP, &tailscaleHost, &vaultSecretID, &nodeJSON)
	if err != nil {
		if err == sql.ErrNoRows {
			return HostFacts{}, false, nil
		}
		return HostFacts{}, false, err
	}

	portStr := ""
	if port > 0 {
		portStr = fmt.Sprintf("%d", port)
	}
	facts, err := buildHostFacts(
		id, ip, portStr, sshUser, tailscaleIP, tailscaleHost,
		vaultSecretID.String, []byte(nodeJSON),
	)
	if err != nil {
		return facts, false, err
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
	case KindStorage, KindShare, KindDisk, KindVM, KindMount, KindBackup, KindDatabase:
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
	settingsStore  *SettingsStore
	// collections installs controller-side Galaxy collections the detected
	// capabilities need. Optional: nil disables the auto-install behaviour.
	collections *collections.Manager
}

// NodeResolver returns the wired node resolver. It may be nil when no store or
// database path was available at construction time; callers must nil-check
// before use (e.g. the background cache manager).
func (a *API) NodeResolver() NodeResolver {
	if a == nil {
		return nil
	}
	return a.nodeResolver
}

// RunnerResolver returns the wired runner resolver. Never nil for a non-nil API
// (NewAPI substitutes a DefaultRunnerResolver), but nil-check defensively.
func (a *API) RunnerResolver() RunnerResolver {
	if a == nil {
		return nil
	}
	return a.runnerResolver
}

// Registry returns the wired plugin registry. Never nil for a non-nil API.
func (a *API) Registry() *Registry {
	if a == nil {
		return nil
	}
	return a.registry
}

// SettingsStore returns the wired settings store.
func (a *API) SettingsStore() *SettingsStore {
	if a == nil {
		return nil
	}
	return a.settingsStore
}

// Cache returns the wired probe cache. Never nil for a non-nil API.
func (a *API) Cache() *ProbeCache {
	if a == nil {
		return nil
	}
	return a.cache
}

// globalCollections is the process-wide collection manager installed at
// startup. Resident plugins and late-constructed API instances use it so a
// detected capability can install its modules without extra wiring.
var (
	globalCollectionsMu sync.RWMutex
	globalCollections   *collections.Manager
)

// SetCollectionsManager publishes the process-wide Galaxy collection manager.
func SetCollectionsManager(m *collections.Manager) {
	globalCollectionsMu.Lock()
	defer globalCollectionsMu.Unlock()
	globalCollections = m
}

// GlobalCollectionsManager returns the process-wide manager, or nil when none
// has been installed (unit tests).
func GlobalCollectionsManager() *collections.Manager {
	globalCollectionsMu.RLock()
	defer globalCollectionsMu.RUnlock()
	return globalCollections
}

// APIOptions holds dependencies for initializing API
type APIOptions struct {
	Registry       *Registry
	NodeResolver   NodeResolver
	RunnerResolver RunnerResolver
	Cache          *ProbeCache
	// SettingsStore backs the global settings pages endpoint. When nil, the
	// store is auto-wired against hosts-cache.sqlite.
	SettingsStore *SettingsStore
	// Collections keeps Galaxy collections in sync with detected capabilities.
	Collections *collections.Manager
	// Store optionally provides a pre-initialized hostcache store. When set,
	// the NodeResolver is auto-wired from this store, bypassing DBPath.
	Store *hostcache.Store
	// DBPath optionally overrides the hosts-cache.sqlite location used to
	// auto-wire the default SQLite-backed NodeResolver and settings store. When
	// empty, ATC_DB_PATH is consulted, then the path is resolved relative to the
	// current working directory by walking upward.
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
		// If store is provided, use it to create a NodeResolver
		if opts.Store != nil {
			nodeRes = &storeNodeResolver{Store: opts.Store}
		} else {
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
	}

	runnerRes := opts.RunnerResolver
	if runnerRes == nil {
		runnerRes = &DefaultRunnerResolver{}
	}

	cache := opts.Cache
	if cache == nil {
		cache = NewProbeCache()
	}

	settingsStore := opts.SettingsStore
	if settingsStore == nil {
		if st, err := NewSettingsStore(ResolveDBPath(opts.DBPath)); err == nil {
			settingsStore = st
		} else {
			log.Printf("plugin: settings store unavailable (%v); global settings endpoints disabled", err)
		}
	}
	// Publish the store process-wide so resident plugins can read their own
	// persisted global settings during Read/Compile.
	SetGlobalSettingsStore(settingsStore)

	return &API{
		registry:       reg,
		nodeResolver:   nodeRes,
		runnerResolver: runnerRes,
		cache:          cache,
		settingsStore:  settingsStore,
		collections:    pickCollections(opts.Collections),
	}
}

// RegisterRoutes mounts all plugin management endpoints onto the given ServeMux
func (a *API) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/plugins", a.handleListPlugins)
	mux.HandleFunc("/api/nodes/", a.handleNodeRoutes)
	a.RegisterSettingsRoutes(mux)
}

// RegisterSettingsRoutes mounts the global settings endpoints onto the given ServeMux
func (a *API) RegisterSettingsRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/settings/pages", a.handleSettingsPages)
	mux.HandleFunc("/api/settings/pages/", a.handleSettingsPageRoutes)
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

	// /api/nodes/:id/plugins/:kind/:name/query
	if len(parts) == 5 && parts[1] == "plugins" && parts[4] == "query" && r.Method == http.MethodPost {
		kind := Kind(parts[2])
		name := parts[3]
		a.handlePluginQuery(w, r, nodeID, kind, name)
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
		http.Error(w, err.Error(), NodeResolveStatus(err))
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
		return HostFacts{}, nil, fmt.Errorf("%w: %s", ErrNodeNotFound, nodeID)
	}

	runner, err := a.runnerResolver.ResolveRunner(ctx, facts)
	if err != nil {
		return facts, nil, fmt.Errorf("failed to resolve runner for node %s: %w", nodeID, err)
	}

	return facts, runner, nil
}

// ErrNodeNotFound marks a resolve failure caused by an unknown node id. It lets
// handlers answer 404 for a genuinely missing node without reporting an
// unreachable one (dial timeout, SSH refused) as "not found".
var ErrNodeNotFound = errors.New("node not found")

// NodeResolveStatus maps a resolveFactsAndRunner error to an HTTP status:
// unknown node -> 404, everything else (transport/dial/SSH failures) -> 502.
func NodeResolveStatus(err error) int {
	if errors.Is(err, ErrNodeNotFound) {
		return http.StatusNotFound
	}
	return http.StatusBadGateway
}

// handleNodePlugins implements GET /api/nodes/:id/plugins
func (a *API) handleNodePlugins(w http.ResponseWriter, r *http.Request, nodeID string) {
	ctx := r.Context()
	facts, runner, err := a.resolveFactsAndRunner(ctx, nodeID)
	if err != nil {
		http.Error(w, err.Error(), NodeResolveStatus(err))
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
			a.ensureCollections(p, det, facts)
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
		http.Error(w, err.Error(), NodeResolveStatus(err))
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
			a.ensureCollections(p, det, facts)
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
		http.Error(w, err.Error(), NodeResolveStatus(err))
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
		rawBytes, _ := io.ReadAll(r.Body)
		if len(rawBytes) > 0 {
			_ = json.Unmarshal(rawBytes, &reqBody)
			if len(reqBody.Options) == 0 {
				var flat map[string]any
				if err := json.Unmarshal(rawBytes, &flat); err == nil {
					delete(flat, "options")
					if len(flat) > 0 {
						reqBody.Options = flat
					}
				}
			}
		}
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
		http.Error(w, err.Error(), NodeResolveStatus(err))
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

// handlePluginQuery implements POST /api/nodes/:id/plugins/:kind/:name/query
//
// It executes an ad-hoc query through a plugin that implements Querier. The
// handler always answers 200 with a structured QueryResult: when the native
// driver cannot connect or does not support the statement, the result carries
// Fallback="cli" so the frontend can retry through the vendor CLI transport.
func (a *API) handlePluginQuery(w http.ResponseWriter, r *http.Request, nodeID string, kind Kind, name string) {
	ctx := r.Context()
	pluginID := fmt.Sprintf("%s/%s", kind, name)

	p, ok := a.registry.Get(pluginID)
	if !ok {
		http.Error(w, fmt.Sprintf("plugin %s not found", pluginID), http.StatusNotFound)
		return
	}

	querier, ok := p.(Querier)
	if !ok {
		http.Error(w, fmt.Sprintf("plugin %s does not implement Querier", pluginID), http.StatusBadRequest)
		return
	}

	var req QueryRequest
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
			return
		}
	}
	if strings.TrimSpace(req.Query) == "" {
		http.Error(w, "query is required", http.StatusBadRequest)
		return
	}

	facts, runner, err := a.resolveFactsAndRunner(ctx, nodeID)
	if err != nil {
		http.Error(w, err.Error(), NodeResolveStatus(err))
		return
	}

	result, queryErr := querier.Query(ctx, runner, facts, req)
	if queryErr != nil {
		if result.Driver == "" {
			result.Driver = "go"
		}
		if result.Engine == "" {
			result.Engine = req.Engine
		}
		if result.Error == "" {
			result.Error = queryErr.Error()
		}
		if result.Fallback == "" {
			result.Fallback = "cli"
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}

// -------------------------------------------------------------
// Global (datacenter-wide) settings pages contributed by addon modules
// -------------------------------------------------------------

// GlobalSettingsPageView is a settings page schema paired with the current
// persisted values, as returned by GET /api/settings/pages[/:id].
type GlobalSettingsPageView struct {
	SettingsPage
	Values map[string]any `json:"values"`
}

// SettingsValuesBody is the request payload for PUT /api/settings/pages/:id.
type SettingsValuesBody struct {
	Values map[string]any `json:"values"`
}

// handleSettingsPages implements GET /api/settings/pages: it lists every global
// settings page contributed by an addon module, each with its current values.
func (a *API) handleSettingsPages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	pages := a.registry.ListSettingsPages()
	views := make([]GlobalSettingsPageView, 0, len(pages))
	for _, page := range pages {
		view, err := a.settingsView(page)
		if err != nil {
			http.Error(w, fmt.Sprintf("failed to load settings for %s: %v", page.ID, err), http.StatusInternalServerError)
			return
		}
		views = append(views, view)
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(views)
}

// handleSettingsPageRoutes implements GET and PUT on /api/settings/pages/:id.
// The id may itself contain a slash (for IDs of the form "<kind>/<name>").
func (a *API) handleSettingsPageRoutes(w http.ResponseWriter, r *http.Request) {
	pageID := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/settings/pages/"), "/")
	if pageID == "" {
		http.NotFound(w, r)
		return
	}

	provider, ok := a.registry.GetSettingsPage(pageID)
	if !ok {
		http.Error(w, fmt.Sprintf("settings page %q not found", pageID), http.StatusNotFound)
		return
	}
	page := provider.SettingsPage()

	switch r.Method {
	case http.MethodGet:
		view, err := a.settingsView(page)
		if err != nil {
			http.Error(w, fmt.Sprintf("failed to load settings for %s: %v", pageID, err), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(view)

	case http.MethodPut:
		if a.settingsStore == nil {
			http.Error(w, "settings store unavailable", http.StatusServiceUnavailable)
			return
		}
		var body SettingsValuesBody
		if r.Body != nil {
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
				return
			}
		}
		if body.Values == nil {
			body.Values = map[string]any{}
		}
		coerced, err := coerceSettingsValues(page, body.Values)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := a.settingsStore.Save(pageID, coerced); err != nil {
			http.Error(w, fmt.Sprintf("failed to persist settings for %s: %v", pageID, err), http.StatusInternalServerError)
			return
		}
		view, err := a.settingsView(page)
		if err != nil {
			http.Error(w, fmt.Sprintf("failed to reload settings for %s: %v", pageID, err), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(view)

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// settingsView merges persisted values over the schema defaults for a page.
func (a *API) settingsView(page SettingsPage) (GlobalSettingsPageView, error) {
	stored := map[string]any{}
	if a.settingsStore != nil {
		loaded, err := a.settingsStore.Load(page.ID)
		if err != nil {
			return GlobalSettingsPageView{}, err
		}
		stored = loaded
	}

	values := make(map[string]any, len(page.Fields))
	for _, f := range page.Fields {
		if IsSecretField(f) {
			// Secret Protection Invariant: a write-only secret never leaves the
			// process in plaintext. Clients receive only the mask, which they
			// echo back unchanged when they have not replaced the value.
			if v, ok := stored[f.Name]; ok {
				if s, isString := v.(string); isString && strings.TrimSpace(s) != "" {
					values[f.Name] = MaskedSecretValue(f.Name)
					continue
				}
			}
			values[f.Name] = ""
			continue
		}
		if v, ok := stored[f.Name]; ok {
			values[f.Name] = v
			continue
		}
		if f.Default != nil {
			values[f.Name] = f.Default
			continue
		}
		values[f.Name] = zeroSettingValue(f.Type)
	}

	return GlobalSettingsPageView{SettingsPage: page, Values: values}, nil
}

func zeroSettingValue(t SettingsFieldType) any {
	switch t {
	case SettingsFieldInt:
		return 0
	case SettingsFieldBool:
		return false
	default:
		return ""
	}
}

// coerceSettingsValues validates an inbound values map against the page schema
// and returns the strongly typed, ready-to-persist values.
func coerceSettingsValues(page SettingsPage, input map[string]any) (map[string]any, error) {
	known := make(map[string]SettingsField, len(page.Fields))
	for _, f := range page.Fields {
		known[f.Name] = f
	}
	for k := range input {
		if _, ok := known[k]; !ok {
			return nil, fmt.Errorf("unknown settings field %q for page %s", k, page.ID)
		}
	}

	out := make(map[string]any, len(page.Fields))
	for _, f := range page.Fields {
		raw, present := input[f.Name]
		if IsSecretField(f) {
			// A secret field is write-only. An omitted, empty, or mask value
			// means "leave the stored secret alone"; only real new material is
			// persisted, so re-saving an unrelated field can never blank or
			// overwrite the secret with its own mask.
			if !present || raw == nil {
				continue
			}
			s, isString := raw.(string)
			if !isString || strings.TrimSpace(s) == "" || IsMaskedSecretValue(s) {
				continue
			}
			out[f.Name] = strings.TrimSpace(s)
			continue
		}
		if !present || raw == nil || raw == "" {
			if f.Required {
				return nil, fmt.Errorf("missing required settings field %q", f.Name)
			}
			continue
		}
		v, err := coerceSettingField(f, raw)
		if err != nil {
			return nil, err
		}
		out[f.Name] = v
	}
	return out, nil
}

func coerceSettingField(f SettingsField, raw any) (any, error) {
	switch f.Type {
	case SettingsFieldInt:
		switch n := raw.(type) {
		case float64:
			return int(n), nil
		case int:
			return n, nil
		case string:
			parsed, err := strconv.Atoi(strings.TrimSpace(n))
			if err != nil {
				return nil, fmt.Errorf("settings field %q expects an integer", f.Name)
			}
			return parsed, nil
		default:
			return nil, fmt.Errorf("settings field %q expects an integer", f.Name)
		}

	case SettingsFieldBool:
		switch b := raw.(type) {
		case bool:
			return b, nil
		case string:
			parsed, err := strconv.ParseBool(strings.TrimSpace(b))
			if err != nil {
				return nil, fmt.Errorf("settings field %q expects a boolean", f.Name)
			}
			return parsed, nil
		default:
			return nil, fmt.Errorf("settings field %q expects a boolean", f.Name)
		}

	case SettingsFieldSelect:
		s := fmt.Sprint(raw)
		for _, opt := range f.Options {
			if opt.Value == s {
				return s, nil
			}
		}
		return nil, fmt.Errorf("settings field %q value %q is not an allowed option", f.Name, s)

	default: // string, text, secret
		if _, ok := raw.(string); !ok {
			return nil, fmt.Errorf("settings field %q expects a string", f.Name)
		}
		return raw.(string), nil
	}
}

// ensureCollections installs the Galaxy collections that a detected capability
// needs. It runs off the request path so a slow galaxy download never blocks a
// probe response.
func (a *API) ensureCollections(p Plugin, det Detection, facts HostFacts) {
	if a == nil {
		return
	}
	if a.collections != nil {
		ensureCollectionsWith(a.collections, p, det, facts)
		return
	}
	EnsureCollectionsForDetection(p, det, facts)
}

// EnsureCollectionsForDetection installs the Galaxy collections a detected
// capability needs, using the process-wide manager. Probing code (the node
// plugin endpoints and the background cache manager) calls this so a newly
// discovered PostgreSQL/MariaDB/MongoDB/Podman/Proxmox capability pulls its
// module tree without waiting for an operator to open the host's Modules tab.
func EnsureCollectionsForDetection(p Plugin, det Detection, facts HostFacts) {
	ensureCollectionsWith(GlobalCollectionsManager(), p, det, facts)
}

func ensureCollectionsWith(mgr *collections.Manager, p Plugin, det Detection, facts HostFacts) {
	if mgr == nil || !det.Detected || p == nil {
		return
	}
	meta := p.Meta()

	names := collections.CollectionsForPlugin(meta.ID, det.Details)
	names = append(names, p.Requirements().ControllerCollections...)
	if strings.EqualFold(strings.TrimSpace(facts.OSFamily), "windows") {
		names = append(names, collections.AnsibleWindows)
	}

	needed := dedupeCollections(names)
	if len(needed) == 0 {
		return
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		installed := mgr.Ensure(ctx, needed, "detected "+meta.ID)
		if len(installed) > 0 {
			log.Printf("plugin: installed Galaxy collections for %s: %v", meta.ID, installed)
		}
	}()
}

// dedupeCollections removes blanks, aliases and duplicates from a plugin's
// collection requirements.
func dedupeCollections(names []string) []string {
	seen := make(map[string]bool, len(names))
	out := make([]string, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" || name == "ansible.builtin" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

// pickCollections prefers the explicitly wired manager and falls back to the
// process-wide one installed at startup.
func pickCollections(explicit *collections.Manager) *collections.Manager {
	if explicit != nil {
		return explicit
	}
	return GlobalCollectionsManager()
}
