// Package nodeapi implements the HTTP REST endpoints for node lifecycle CRUD
// and hardware probing, ported from Express (server.ts) to the pure-Go daemon.
//
// Parity Contract:
//   - Route paths, request payload parsing, status codes, and JSON error structures
//     mirror server.ts byte-for-byte.
//   - Object keys round-trip in Express insertion order to preserve diff fidelity
//     against data/hosts-cache.sqlite.
//   - Zero-Mock Invariant: Remote actions strictly communicate through the SSH client
//     pool or local loopback shortcut. No fake metrics, simulated delays, or mock nodes.
//   - Mutations cascade to hostcache and emit WebSocket notifications identically.
package nodeapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"air-traffic-control/backend/pkg/ansqueue"
	"air-traffic-control/backend/pkg/hostcache"
	"air-traffic-control/backend/pkg/tailscale"
)

// Store defines persistence operations required by the node API.
type Store interface {
	SeedHostsIfEmpty(initialHosts []hostcache.NodeRecord) ([]hostcache.NodeRecord, error)
	GetAllHosts() ([]hostcache.NodeRecord, error)
	GetHostByID(id string) (hostcache.NodeRecord, bool, error)
	UpsertHost(rec hostcache.NodeRecord) error
	UpsertHosts(nodes []hostcache.NodeRecord) error
	DeleteHost(id string) (bool, error)
	GetAllTagChunks() ([]hostcache.TagChunk, error)
	InvalidateHostSections(hostID string) error
	DeleteHostContainerCache(hostID string) error
	DeleteHostTelemetry(hostID string) error
	SaveTask(t hostcache.Task) error
	AddCustomIdentity(hostID string, identityJSON json.RawMessage) error
	UpsertHostSection(hostID, section string, dataJSON json.RawMessage) error
}

// Options configures the node API surface.
type Options struct {
	Store           Store
	QueueStore      *ansqueue.Store
	Runner          CommandRunner
	Broadcast       func(event map[string]any)
	Now             func() time.Time
	Fallback        http.Handler
	TailscaleStatus func() (*tailscale.Status, error)
	SeedBaseline    bool
	OnNodeUpdated   func(nodeID string)
}

// API handles node CRUD and hardware probing requests.
type API struct {
	store           Store
	queueStore      *ansqueue.Store
	runner          CommandRunner
	broadcast       func(event map[string]any)
	now             func() time.Time
	fallback        http.Handler
	tailscaleStatus func() (*tailscale.Status, error)
	onNodeUpdated   func(nodeID string)
}

// New creates and validates a new node API instance.
func New(opts Options) (*API, error) {
	if opts.Store == nil {
		return nil, errors.New("nodeapi: store is required")
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	tsStatus := opts.TailscaleStatus
	if tsStatus == nil {
		tsStatus = tailscale.GetLocalStatus
	}
	api := &API{
		store:           opts.Store,
		queueStore:      opts.QueueStore,
		runner:          opts.Runner,
		broadcast:       opts.Broadcast,
		now:             now,
		fallback:        opts.Fallback,
		tailscaleStatus: tsStatus,
		onNodeUpdated:   opts.OnNodeUpdated,
	}

	if opts.SeedBaseline {
		_ = api.SeedAndSyncBaseline()
	}

	return api, nil
}

// Register mounts the /api/nodes/ subtree and /api/podman/action on mux.
func (a *API) Register(mux *http.ServeMux) {
	mux.HandleFunc("/api/nodes/", a.handleNodesSubtree)
	mux.HandleFunc("/api/podman/action", a.HandlePodmanAction)
}

func (a *API) emit(event map[string]any) {
	if a.broadcast != nil {
		a.broadcast(event)
	}
}

func (a *API) emitQueueUpdated() {
	if a.queueStore != nil && a.broadcast != nil {
		a.broadcast(map[string]any{
			"type":  "queue_updated",
			"queue": a.queueStore.Snapshot(),
		})
	}
}

func (a *API) notFound(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusNotFound, map[string]any{
		"success": false,
		"error":   fmt.Sprintf("API route not found: %s %s", r.Method, r.URL.Path),
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

type nodeResponse struct {
	Success bool    `json:"success"`
	Node    *Object `json:"node"`
}

type removedResponse struct {
	Success bool    `json:"success"`
	Removed *Object `json:"removed"`
}

type probeHostResponse struct {
	Success   bool           `json:"success"`
	Hardware  ProbedHardware `json:"hardware"`
	RawOutput string         `json:"rawOutput"`
}

type boundProbeHostResponse struct {
	Success   bool           `json:"success"`
	Hardware  ProbedHardware `json:"hardware"`
	Node      *Object        `json:"node"`
	RawOutput string         `json:"rawOutput"`
}

var reUpPrefix = regexp.MustCompile(`(?i)^up\s+`)

// ---------------------------------------------------------------------------
// Route Dispatcher
// ---------------------------------------------------------------------------

func (a *API) handleNodesSubtree(w http.ResponseWriter, r *http.Request) {
	sub := strings.TrimPrefix(r.URL.Path, "/api/nodes/")

	if sub == "" {
		if r.Method == http.MethodPost {
			a.HandleCreate(w, r)
			return
		}
		if r.Method == http.MethodGet {
			a.HandleList(w, r)
			return
		}
		a.notFound(w, r)
		return
	}

	if sub == "probe-host" {
		if r.Method == http.MethodPost {
			a.handleProbeHostUnbound(w, r)
			return
		}
		a.notFound(w, r)
		return
	}

	if strings.HasSuffix(sub, "/probe-host") {
		parts := strings.Split(sub, "/")
		if len(parts) == 2 && parts[1] == "probe-host" && parts[0] != "" {
			if r.Method == http.MethodPost {
				a.handleProbeHostBound(w, r, parts[0])
				return
			}
			a.notFound(w, r)
			return
		}
	}

	if !strings.Contains(sub, "/") && sub != "" {
		id := sub
		switch r.Method {
		case http.MethodPut:
			a.handleUpdate(w, r, id)
			return
		case http.MethodDelete:
			a.handleDelete(w, r, id)
			return
		default:
			a.notFound(w, r)
			return
		}
	}

	parts := strings.SplitN(sub, "/", 2)
	nodeID := parts[0]
	rest := parts[1]

	// 1. POST /api/nodes/:id/interfaces/:name/toggle
	// 2. POST /api/nodes/:id/interfaces/connect
	if strings.HasPrefix(rest, "interfaces/") {
		ifRest := strings.TrimPrefix(rest, "interfaces/")
		if ifRest == "connect" && r.Method == http.MethodPost {
			a.handleInterfaceConnect(w, r, nodeID)
			return
		}
		if strings.HasSuffix(ifRest, "/toggle") && r.Method == http.MethodPost {
			ifaceName := strings.TrimSuffix(ifRest, "/toggle")
			a.handleInterfaceToggle(w, r, nodeID, ifaceName)
			return
		}
	}

	// 3. POST /api/nodes/:id/firewall/rules
	// 4. DELETE /api/nodes/:id/firewall/rules/:ruleId
	if rest == "firewall/rules" && r.Method == http.MethodPost {
		a.handleFirewallAddRule(w, r, nodeID)
		return
	}
	if strings.HasPrefix(rest, "firewall/rules/") && r.Method == http.MethodDelete {
		ruleID := strings.TrimPrefix(rest, "firewall/rules/")
		a.handleFirewallDeleteRule(w, r, nodeID, ruleID)
		return
	}

	// 5. POST /api/nodes/:id/disks/:name/smart-test
	if strings.HasPrefix(rest, "disks/") && strings.HasSuffix(rest, "/smart-test") && r.Method == http.MethodPost {
		diskName := strings.TrimSuffix(strings.TrimPrefix(rest, "disks/"), "/smart-test")
		a.handleDiskSmartTest(w, r, nodeID, diskName)
		return
	}

	// 6. POST /api/nodes/:id/zfs/:pool/scrub
	if strings.HasPrefix(rest, "zfs/") && strings.HasSuffix(rest, "/scrub") && r.Method == http.MethodPost {
		poolName := strings.TrimSuffix(strings.TrimPrefix(rest, "zfs/"), "/scrub")
		a.handleZfsScrub(w, r, nodeID, poolName)
		return
	}

	// 7. POST /api/nodes/:id/vms/:name/action
	// 8. POST /api/nodes/:id/vms/create
	if rest == "vms/create" && r.Method == http.MethodPost {
		a.handleVmCreate(w, r, nodeID)
		return
	}
	if strings.HasPrefix(rest, "vms/") && strings.HasSuffix(rest, "/action") && r.Method == http.MethodPost {
		vmName := strings.TrimSuffix(strings.TrimPrefix(rest, "vms/"), "/action")
		a.handleVmAction(w, r, nodeID, vmName)
		return
	}

	// 9. POST /api/nodes/:id/shares/reload
	// 10. POST /api/nodes/:id/shares/nfs
	// 11. POST /api/nodes/:id/shares/smb
	if rest == "shares/reload" && r.Method == http.MethodPost {
		a.handleSharesReload(w, r, nodeID)
		return
	}
	if rest == "shares/nfs" && r.Method == http.MethodPost {
		a.handleSharesNfs(w, r, nodeID)
		return
	}
	if rest == "shares/smb" && r.Method == http.MethodPost {
		a.handleSharesSmb(w, r, nodeID)
		return
	}

	// 12. POST /api/nodes/:id/users/create
	// 13. POST /api/nodes/:id/users/:user/password
	// 14. POST /api/nodes/:id/users/:user/smbpassword
	if rest == "users/create" && r.Method == http.MethodPost {
		a.handleUsersCreate(w, r, nodeID)
		return
	}
	if strings.HasPrefix(rest, "users/") && strings.HasSuffix(rest, "/password") && r.Method == http.MethodPost {
		user := strings.TrimSuffix(strings.TrimPrefix(rest, "users/"), "/password")
		a.handleUsersPassword(w, r, nodeID, user)
		return
	}
	if strings.HasPrefix(rest, "users/") && strings.HasSuffix(rest, "/smbpassword") && r.Method == http.MethodPost {
		user := strings.TrimSuffix(strings.TrimPrefix(rest, "users/"), "/smbpassword")
		a.handleUsersSmbPassword(w, r, nodeID, user)
		return
	}

	// 15. POST /api/nodes/:id/reboot
	if rest == "reboot" && r.Method == http.MethodPost {
		a.handleReboot(w, r, nodeID)
		return
	}

	// 16. POST /api/nodes/:id/identities
	if rest == "identities" && r.Method == http.MethodPost {
		a.handleIdentities(w, r, nodeID)
		return
	}

	// 17. GET /api/nodes/:id/privilege & POST /api/nodes/:id/privilege/test
	if rest == "privilege" && r.Method == http.MethodGet {
		a.handlePrivilegeGet(w, r, nodeID)
		return
	}
	if rest == "privilege/test" && r.Method == http.MethodPost {
		a.handlePrivilegeTest(w, r, nodeID)
		return
	}

	// 18. GET /api/nodes/:id/packages/check & POST /api/nodes/:id/packages/apply
	if rest == "packages/check" && r.Method == http.MethodGet {
		a.handlePackagesCheck(w, r, nodeID)
		return
	}
	if rest == "packages/apply" && r.Method == http.MethodPost {
		a.handlePackagesApply(w, r, nodeID)
		return
	}

	if a.fallback != nil {
		a.fallback.ServeHTTP(w, r)
		return
	}

	a.notFound(w, r)
}

// ---------------------------------------------------------------------------
// POST /api/nodes — Create Node
// ---------------------------------------------------------------------------

func (a *API) HandleCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		a.notFound(w, r)
		return
	}

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "failed to read body"})
		return
	}

	body := NewObject()
	if len(bodyBytes) > 0 {
		parsed, err := ParseObject(bodyBytes)
		if err != nil {
			// Express' express.json() rejects a malformed body with 400 before
			// the route runs; match that status instead of silently creating a
			// node from defaults.
			writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "invalid json payload"})
			return
		}
		body = parsed
	}

	newNode := NewObject()

	// 1. id
	id := fmt.Sprintf("node-%d", a.now().UnixMilli())
	if s, ok := body.GetString("id"); ok && s != "" {
		id = s
	}
	newNode.SetString("id", id)

	// 2. name
	name := "New Node"
	if s, ok := body.GetString("name"); ok && s != "" {
		name = s
	}
	newNode.SetString("name", name)

	// 3. shortName — Express: (req.body.name || 'node').split(' ')[0]
	nameForShort := "node"
	if s, ok := body.GetString("name"); ok && s != "" {
		nameForShort = s
	}
	shortName := strings.Split(nameForShort, " ")[0]
	if s, ok := body.GetString("shortName"); ok && s != "" {
		shortName = s
	}
	newNode.SetString("shortName", shortName)

	// 4. group
	group := "tag:host"
	if s, ok := body.GetString("group"); ok && s != "" {
		group = s
	}
	newNode.SetString("group", group)

	// 5. datacenter
	datacenter := "leo-banjo.ts.net"
	if s, ok := body.GetString("datacenter"); ok && s != "" {
		datacenter = s
	}
	newNode.SetString("datacenter", datacenter)

	// 6. nodeType
	nodeType := "linux"
	if s, ok := body.GetString("nodeType"); ok && s != "" {
		nodeType = s
	}
	newNode.SetString("nodeType", nodeType)

	// 7. badge
	badge := "SSH"
	if s, ok := body.GetString("badge"); ok && s != "" {
		badge = s
	}
	newNode.SetString("badge", badge)

	// 8. badgeColor
	badgeColor := "text-[#8cd6cb]"
	if s, ok := body.GetString("badgeColor"); ok && s != "" {
		badgeColor = s
	}
	newNode.SetString("badgeColor", badgeColor)

	// 9. ip
	ip := "192.168.8.200"
	if s, ok := body.GetString("ip"); ok && s != "" {
		ip = s
	}
	newNode.SetString("ip", ip)

	// 10. port
	port := 22
	if raw, ok := body.Get("port"); ok {
		if p := parsePositiveInt(raw); p > 0 {
			port = p
		}
	}
	newNode.SetJSON("port", port)

	// 11. sshUser
	sshUser := "core"
	if s, ok := body.GetString("sshUser"); ok && s != "" {
		sshUser = s
	}
	newNode.SetString("sshUser", sshUser)

	// 12. authMethod
	authMethod := "ssh-ed25519 (ssh-agent)"
	if s, ok := body.GetString("authMethod"); ok && s != "" {
		authMethod = s
	}
	newNode.SetString("authMethod", authMethod)

	// 13. tags
	if rawTags, ok := body.Get("tags"); ok && string(rawTags) != "null" {
		newNode.Set("tags", rawTags)
	} else {
		fallbackGroup := group
		newNode.SetJSON("tags", []string{fallbackGroup})
	}

	// 14. sshConnected
	newNode.SetJSON("sshConnected", true)

	// 15. rttMs
	newNode.SetString("rttMs", "2.0ms")

	// 16. tailscaleIp
	tailscaleIp := "100.100.100.100"
	if s, ok := body.GetString("tailscaleIp"); ok && s != "" {
		tailscaleIp = s
	} else if s, ok := body.GetString("ip"); ok && s != "" {
		tailscaleIp = s
	}
	newNode.SetString("tailscaleIp", tailscaleIp)

	// 17. tailscaleHost
	nameForTsHost := "node"
	if s, ok := body.GetString("name"); ok && s != "" {
		nameForTsHost = s
	}
	tailscaleHost := fmt.Sprintf("%s.leo-banjo.ts.net", strings.ToLower(nameForTsHost))
	if s, ok := body.GetString("tailscaleHost"); ok && s != "" {
		tailscaleHost = s
	}
	newNode.SetString("tailscaleHost", tailscaleHost)

	// 18. osDescription
	osDescription := "AlmaLinux 10.2 / Linux"
	if s, ok := body.GetString("osDescription"); ok && s != "" {
		osDescription = s
	}
	newNode.SetString("osDescription", osDescription)

	// 19. status
	status := "online"
	if s, ok := body.GetString("status"); ok && s != "" {
		status = s
	}
	newNode.SetString("status", status)

	// 20. uptime
	uptime := "1m"
	if s, ok := body.GetString("uptime"); ok && s != "" {
		uptime = s
	}
	newNode.SetString("uptime", uptime)

	// 21-24. hasGlances, hasCockpit, hasPodman, hasRestic
	for _, flag := range []string{"hasGlances", "hasCockpit", "hasPodman", "hasRestic"} {
		if raw, ok := body.Get(flag); ok && string(raw) != "null" {
			newNode.Set(flag, raw)
		} else {
			newNode.SetJSON(flag, true)
		}
	}

	// 25. specs
	if rawSpecs, ok := body.Get("specs"); ok && string(rawSpecs) != "null" {
		newNode.Set("specs", rawSpecs)
	} else {
		specsObj := NewObject()
		cpu := "Multi-core x86_64"
		if s, ok := body.GetString("cpu"); ok && s != "" {
			cpu = s
		}
		specsObj.SetString("cpu", cpu)

		vcpus := 8
		if raw, ok := body.Get("vcpus"); ok {
			if n := parsePositiveInt(raw); n > 0 {
				vcpus = n
			}
		}
		specsObj.SetJSON("vcpus", vcpus)

		ramGiB := 16
		if raw, ok := body.Get("ramGiB"); ok {
			if n := parsePositiveInt(raw); n > 0 {
				ramGiB = n
			}
		}
		specsObj.SetJSON("ramGiB", ramGiB)

		specsObj.SetString("motherboard", "Standard Server Platform")
		specsObj.SetString("bios", "UEFI 2.8")

		ipmi := "DHCP"
		if s, ok := body.GetString("ipmi"); ok && s != "" {
			ipmi = s
		}
		specsObj.SetString("ipmi", ipmi)

		specsObj.SetString("ecc", "Active")
		specsObj.SetString("secureBoot", "Enabled")
		specsObj.SetString("iommu", "Enabled")

		specsBytes, _ := specsObj.MarshalJSON()
		newNode.Set("specs", specsBytes)
	}

	// Trailing optional keys: include only when present in body
	optionalKeys := []string{
		"connectionProtocol",
		"vaultSecretId",
		"folder",
		"isDynamicTagMember",
		"followedTag",
	}
	for _, optKey := range optionalKeys {
		if raw, ok := body.Get(optKey); ok {
			newNode.Set(optKey, raw)
		}
	}

	rec, err := nodeRecordFromObject(newNode)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
		return
	}

	if err := a.store.UpsertHost(rec); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
		return
	}

	a.emit(map[string]any{
		"type": "node_added",
		"node": newNode,
	})

	writeJSON(w, http.StatusOK, nodeResponse{
		Success: true,
		Node:    newNode,
	})
}

// ---------------------------------------------------------------------------
// PUT /api/nodes/:id — Update Node
// ---------------------------------------------------------------------------

func (a *API) handleUpdate(w http.ResponseWriter, r *http.Request, id string) {
	rec, found, err := a.store.GetHostByID(id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if !found {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "Node not found"})
		return
	}

	nodeObj, err := ParseObject(rec.NodeJSON)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": fmt.Sprintf("corrupt node json: %v", err)})
		return
	}

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "failed to read body"})
		return
	}

	bodyObj, err := ParseObject(bodyBytes)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json payload"})
		return
	}

	// Capture existing name and specs before spreading updates
	existingName, _ := nodeObj.GetString("name")
	origSpecsObj := NewObject()
	if rawSpecs, ok := nodeObj.Get("specs"); ok && len(rawSpecs) > 0 && string(rawSpecs) != "null" {
		if parsed, err := ParseObject(rawSpecs); err == nil {
			origSpecsObj = parsed
		}
	}

	// Spread entire body into nodeObj (including unknown keys and testSsh)
	for _, k := range bodyObj.Keys() {
		rawVal, _ := bodyObj.Get(k)
		nodeObj.Set(k, rawVal)
	}

	// Name resolution: if body has 'name', trimmed value or existing name when empty
	nextName := existingName
	if bodyObj.Has("name") {
		s, _ := bodyObj.GetString("name")
		trimmed := strings.TrimSpace(s)
		if trimmed != "" {
			nextName = trimmed
		}
	}
	nodeObj.SetString("name", nextName)

	// ShortName resolution: if body has 'shortName', trimmed value or first token of nextName
	nextShortName := strings.Split(nextName, " ")[0]
	if bodyObj.Has("shortName") {
		s, _ := bodyObj.GetString("shortName")
		trimmed := strings.TrimSpace(s)
		if trimmed != "" {
			nextShortName = trimmed
		}
	}
	nodeObj.SetString("shortName", nextShortName)

	// Specs shallow merge: {...existingSpecs, ...bodySpecs} only when body has specs
	if bodyObj.Has("specs") {
		mergedSpecs := origSpecsObj.Clone()
		if rawBodySpecs, ok := bodyObj.Get("specs"); ok && len(rawBodySpecs) > 0 && string(rawBodySpecs) != "null" {
			if bodySpecs, err := ParseObject(rawBodySpecs); err == nil {
				for _, sk := range bodySpecs.Keys() {
					sRaw, _ := bodySpecs.Get(sk)
					mergedSpecs.Set(sk, sRaw)
				}
			}
		}
		mergedBytes, _ := mergedSpecs.MarshalJSON()
		nodeObj.Set("specs", mergedBytes)
	} else {
		// Restore original specs if body did not supply specs
		origBytes, _ := origSpecsObj.MarshalJSON()
		nodeObj.Set("specs", origBytes)
	}

	// testSsh: if truthy, execute connectivity probe with 4s timeout
	if rawTestSsh, ok := bodyObj.Get("testSsh"); ok && isTruthy(rawTestSsh) {
		if a.runner != nil {
			out, err := a.runner.RunNodeCommand(r.Context(), nodeObj, "uname -n && uptime -p 2>/dev/null || uptime", 4*time.Second)
			if err == nil {
				nodeObj.SetJSON("sshConnected", true)
				nodeObj.SetString("status", "online")
				lines := strings.Split(strings.TrimSpace(out), "\n")
				if len(lines) > 1 {
					cleanUptime := reUpPrefix.ReplaceAllString(lines[1], "")
					cleanUptime = strings.TrimSpace(cleanUptime)
					if cleanUptime != "" {
						nodeObj.SetString("uptime", cleanUptime)
					}
				}
			} else {
				nodeObj.SetJSON("sshConnected", false)
			}
		} else {
			nodeObj.SetJSON("sshConnected", false)
		}
	}

	updatedRec, err := nodeRecordFromObject(nodeObj)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}

	if err := a.store.UpsertHost(updatedRec); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}

	_ = a.store.InvalidateHostSections(id)
	_ = a.store.DeleteHostContainerCache(id)
	_ = a.store.DeleteHostTelemetry(id)
	if a.onNodeUpdated != nil {
		a.onNodeUpdated(id)
	}

	a.emit(map[string]any{
		"type": "node_updated",
		"node": nodeObj,
	})

	writeJSON(w, http.StatusOK, nodeResponse{
		Success: true,
		Node:    nodeObj,
	})
}

// ---------------------------------------------------------------------------
// DELETE /api/nodes/:id — Remove Node
// ---------------------------------------------------------------------------

func (a *API) handleDelete(w http.ResponseWriter, r *http.Request, id string) {
	rec, found, err := a.store.GetHostByID(id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if !found {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "Node not found"})
		return
	}

	removedObj, err := ParseObject(rec.NodeJSON)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": fmt.Sprintf("corrupt node json: %v", err)})
		return
	}

	deleted, err := a.store.DeleteHost(id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if !deleted {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "Node not found"})
		return
	}

	a.emit(map[string]any{
		"type":   "node_deleted",
		"nodeId": id,
	})

	writeJSON(w, http.StatusOK, removedResponse{
		Success: true,
		Removed: removedObj,
	})
}

// ---------------------------------------------------------------------------
// POST /api/nodes/probe-host — Unbound Probe
// ---------------------------------------------------------------------------

type probeHostRequest struct {
	Host       string `json:"host"`
	Port       int    `json:"port"`
	SSHUser    string `json:"sshUser"`
	AuthMethod string `json:"authMethod"`
}

func (a *API) handleProbeHostUnbound(w http.ResponseWriter, r *http.Request) {
	var req probeHostRequest
	req.Port = 22
	req.SSHUser = "core"
	req.AuthMethod = "ssh-ed25519 (ssh-agent)"

	bodyBytes, err := io.ReadAll(r.Body)
	if err == nil && len(bodyBytes) > 0 {
		_ = json.Unmarshal(bodyBytes, &req)
	}

	host := strings.TrimSpace(req.Host)
	if host == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "host is required"})
		return
	}

	var targetNode *Object

	allHosts, err := a.store.GetAllHosts()
	if err == nil {
		hLower := strings.ToLower(host)
		for _, h := range allHosts {
			if strings.ToLower(h.ID) == hLower ||
				strings.ToLower(h.Name) == hLower ||
				strings.ToLower(h.ShortName) == hLower ||
				h.IP == host ||
				h.TailscaleIP == host ||
				strings.ToLower(h.TailscaleHost) == hLower {
				if parsed, err := ParseObject(h.NodeJSON); err == nil {
					targetNode = parsed
					if req.Port > 0 {
						targetNode.SetJSON("port", req.Port)
					}
					if req.SSHUser != "" {
						targetNode.SetString("sshUser", req.SSHUser)
					}
					if req.AuthMethod != "" {
						targetNode.SetString("authMethod", req.AuthMethod)
					}
					break
				}
			}
		}
	}

	if targetNode == nil {
		targetNode = NewObject()
		targetNode.SetString("id", fmt.Sprintf("probe-%d", a.now().UnixMilli()))
		targetNode.SetString("name", host)
		targetNode.SetString("shortName", strings.Split(host, ".")[0])
		targetNode.SetString("group", "tag:host")
		targetNode.SetString("datacenter", "leo-banjo.ts.net")
		targetNode.SetString("nodeType", "linux")
		targetNode.SetString("ip", host)

		port := req.Port
		if port <= 0 {
			port = 22
		}
		targetNode.SetJSON("port", port)

		sshUser := req.SSHUser
		if sshUser == "" {
			sshUser = "core"
		}
		targetNode.SetString("sshUser", sshUser)

		authMethod := req.AuthMethod
		if authMethod == "" {
			authMethod = "ssh-ed25519 (ssh-agent)"
		}
		targetNode.SetString("authMethod", authMethod)

		tailscaleIp := ""
		if strings.HasPrefix(host, "100.") {
			tailscaleIp = host
		}
		targetNode.SetString("tailscaleIp", tailscaleIp)

		tailscaleHost := host
		if !strings.Contains(host, ".") {
			tailscaleHost = fmt.Sprintf("%s.leo-banjo.ts.net", host)
		}
		targetNode.SetString("tailscaleHost", tailscaleHost)

		targetNode.SetString("osDescription", "Probing...")
		targetNode.SetString("status", "online")
		targetNode.SetString("uptime", "unknown")
		targetNode.SetJSON("hasGlances", false)
		targetNode.SetJSON("hasCockpit", true)
		targetNode.SetJSON("hasPodman", true)
		targetNode.SetJSON("hasRestic", true)

		specsObj := NewObject()
		specsObj.SetString("cpu", "")
		specsObj.SetJSON("vcpus", 1)
		specsObj.SetJSON("ramGiB", 1)
		specsObj.SetString("motherboard", "")
		specsObj.SetString("bios", "")
		specsObj.SetString("ipmi", "DHCP")
		specsObj.SetString("ecc", "Active")
		specsObj.SetString("secureBoot", "Enabled")
		specsObj.SetString("iommu", "Enabled")
		specsBytes, _ := specsObj.MarshalJSON()
		targetNode.Set("specs", specsBytes)
	}

	if a.runner == nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"success": false,
			"error":   fmt.Sprintf("Failed to probe hardware on %s: runner not configured", host),
		})
		return
	}

	probeCmd := ProbeCommand()
	out, err := a.runner.RunNodeCommand(r.Context(), targetNode, probeCmd, 8*time.Second)
	if err != nil {
		errMsg := strings.TrimSpace(err.Error())
		if errMsg == "" {
			errMsg = fmt.Sprintf("Failed to probe hardware on %s", host)
		}
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"success": false,
			"error":   errMsg,
		})
		return
	}

	hardware := ParseHardwareProbeOutput(out)
	writeJSON(w, http.StatusOK, probeHostResponse{
		Success:   true,
		Hardware:  hardware,
		RawOutput: out,
	})
}

// ---------------------------------------------------------------------------
// POST /api/nodes/:id/probe-host — Bound Probe
// ---------------------------------------------------------------------------

func (a *API) handleProbeHostBound(w http.ResponseWriter, r *http.Request, id string) {
	rec, found, err := a.store.GetHostByID(id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"success": false,
			"error":   err.Error(),
		})
		return
	}
	if !found {
		writeJSON(w, http.StatusNotFound, map[string]any{
			"success": false,
			"error":   fmt.Sprintf("Node %s not found", id),
		})
		return
	}

	nodeObj, err := ParseObject(rec.NodeJSON)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"success": false,
			"error":   fmt.Sprintf("corrupt node json: %v", err),
		})
		return
	}

	nodeName, _ := nodeObj.GetString("name")

	if a.runner == nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"success": false,
			"error":   fmt.Sprintf("Failed to probe hardware on node %s: runner not configured", nodeName),
		})
		return
	}

	probeCmd := ProbeCommand()
	out, err := a.runner.RunNodeCommand(r.Context(), nodeObj, probeCmd, 8*time.Second)
	if err != nil {
		errMsg := strings.TrimSpace(err.Error())
		if errMsg == "" {
			errMsg = fmt.Sprintf("Failed to probe hardware on node %s", nodeName)
		}
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"success": false,
			"error":   errMsg,
		})
		return
	}

	hardware := ParseHardwareProbeOutput(out)
	updatedObj := ApplyProbe(nodeObj, hardware)

	updatedRec, err := nodeRecordFromObject(updatedObj)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	if err := a.store.UpsertHost(updatedRec); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	a.emit(map[string]any{
		"type": "node_updated",
		"node": updatedObj,
	})

	writeJSON(w, http.StatusOK, boundProbeHostResponse{
		Success:   true,
		Hardware:  hardware,
		Node:      updatedObj,
		RawOutput: out,
	})
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func nodeRecordFromObject(obj *Object) (hostcache.NodeRecord, error) {
	blob, err := obj.MarshalJSON()
	if err != nil {
		return hostcache.NodeRecord{}, err
	}
	id, _ := obj.GetString("id")
	name, _ := obj.GetString("name")
	shortName, _ := obj.GetString("shortName")
	group, _ := obj.GetString("group")
	ip, _ := obj.GetString("ip")
	tailscaleIp, _ := obj.GetString("tailscaleIp")
	tailscaleHost, _ := obj.GetString("tailscaleHost")
	sshUser, _ := obj.GetString("sshUser")
	status, _ := obj.GetString("status")
	folder, _ := obj.GetString("folder")
	port := 22
	if rawPort, ok := obj.Get("port"); ok {
		if p := parsePositiveInt(rawPort); p > 0 {
			port = p
		}
	}
	return hostcache.NodeRecord{
		ID:            id,
		Name:          name,
		ShortName:     shortName,
		Group:         group,
		IP:            ip,
		Port:          port,
		SSHUser:       sshUser,
		TailscaleIP:   tailscaleIp,
		TailscaleHost: tailscaleHost,
		Status:        status,
		Folder:        folder,
		NodeJSON:      blob,
	}, nil
}
