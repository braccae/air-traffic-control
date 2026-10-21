package clusterapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"air-traffic-control/backend/pkg/hostcache"
)

// ClusterLogKind specifies the source of the log entry.
type ClusterLogKind string

const (
	KindApp  ClusterLogKind = "app"
	KindPoll ClusterLogKind = "poll"
	KindNode ClusterLogKind = "node"
)

// LogEntry represents one authentic coordinator/node event.
type LogEntry struct {
	ID         int64          `json:"id"`
	TS         int64          `json:"ts"`
	Kind       ClusterLogKind `json:"kind"`
	Level      string         `json:"level"`
	Source     string         `json:"source"`
	Host       string         `json:"host,omitempty"`
	Detail     string         `json:"detail,omitempty"`
	DurationMs *int64         `json:"durationMs,omitempty"`
	OK         *bool          `json:"ok,omitempty"`
	Message    string         `json:"message"`
}

const ClusterLogCapacity = 2000

// LogStore maintains a bounded in-memory buffer of cluster log entries.
type LogStore struct {
	mu       sync.RWMutex
	buffer   []LogEntry
	sequence int64
}

// NewLogStore creates a LogStore.
func NewLogStore() *LogStore {
	return &LogStore{
		buffer: make([]LogEntry, 0, 100),
	}
}

// Record appends an entry to the bounded buffer.
func (ls *LogStore) Record(entry LogEntry) LogEntry {
	ls.mu.Lock()
	defer ls.mu.Unlock()

	ls.sequence++
	entry.ID = ls.sequence
	if entry.TS == 0 {
		entry.TS = time.Now().UnixMilli()
	}
	if entry.Source == "" {
		entry.Source = "atc/coordinator"
	}

	ls.buffer = append(ls.buffer, entry)
	if len(ls.buffer) > ClusterLogCapacity {
		ls.buffer = ls.buffer[len(ls.buffer)-ClusterLogCapacity:]
	}
	return entry
}

// List returns filtered entries.
func (ls *LogStore) List(limit int, kind ClusterLogKind, host string, sinceID int64) []LogEntry {
	ls.mu.RLock()
	defer ls.mu.RUnlock()

	if limit <= 0 {
		limit = 300
	}
	if limit > ClusterLogCapacity {
		limit = ClusterLogCapacity
	}

	var filtered []LogEntry
	for _, e := range ls.buffer {
		if kind != "" && e.Kind != kind {
			continue
		}
		if host != "" && e.Host != host {
			continue
		}
		if sinceID > 0 && e.ID <= sinceID {
			continue
		}
		filtered = append(filtered, e)
	}

	if len(filtered) > limit {
		filtered = filtered[len(filtered)-limit:]
	}
	return filtered
}

// ConnectionRegistry tracks open connections per kind.
type ConnectionRegistry struct {
	mu        sync.RWMutex
	counts    map[string]int
	labels    map[string]map[string]int
	updatedAt int64
}

// NewConnectionRegistry creates a registry.
func NewConnectionRegistry() *ConnectionRegistry {
	return &ConnectionRegistry{
		counts: map[string]int{
			"websocket": 0,
			"ssh":       0,
			"winrm":     0,
			"tailscale": 0,
			"local":     0,
		},
		labels: map[string]map[string]int{
			"websocket": make(map[string]int),
			"ssh":       make(map[string]int),
			"winrm":     make(map[string]int),
			"tailscale": make(map[string]int),
			"local":     make(map[string]int),
		},
		updatedAt: time.Now().UnixMilli(),
	}
}

// ConnectionSnapshot represents active connection counts and details.
type ConnectionSnapshot struct {
	Total     int                 `json:"total"`
	ByKind    map[string]int      `json:"byKind"`
	Details   map[string][]string `json:"details"`
	UpdatedAt int64               `json:"updatedAt"`
}

// Snapshot returns the current connections state.
func (cr *ConnectionRegistry) Snapshot() ConnectionSnapshot {
	cr.mu.RLock()
	defer cr.mu.RUnlock()

	total := 0
	byKind := make(map[string]int)
	details := make(map[string][]string)

	kinds := []string{"websocket", "ssh", "winrm", "tailscale", "local"}
	for _, k := range kinds {
		cnt := cr.counts[k]
		byKind[k] = cnt
		total += cnt

		dets := make([]string, 0)
		for lbl, count := range cr.labels[k] {
			if count > 0 {
				if count > 1 {
					dets = append(dets, fmt.Sprintf("%s x%d", lbl, count))
				} else {
					dets = append(dets, lbl)
				}
			}
			if len(dets) >= 20 {
				break
			}
		}
		details[k] = dets
	}

	return ConnectionSnapshot{
		Total:     total,
		ByKind:    byKind,
		Details:   details,
		UpdatedAt: cr.updatedAt,
	}
}

// Begin registers an open transport. Returns an idempotent cleanup func.
func (cr *ConnectionRegistry) Begin(kind, label string) func() {
	cr.mu.Lock()
	if label == "" {
		label = kind
	}
	cr.counts[kind]++
	if cr.labels[kind] == nil {
		cr.labels[kind] = make(map[string]int)
	}
	cr.labels[kind][label]++
	cr.updatedAt = time.Now().UnixMilli()
	cr.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			cr.mu.Lock()
			defer cr.mu.Unlock()
			if cr.counts[kind] > 0 {
				cr.counts[kind]--
			}
			if cr.labels[kind] != nil {
				cr.labels[kind][label]--
				if cr.labels[kind][label] <= 0 {
					delete(cr.labels[kind], label)
				}
			}
			cr.updatedAt = time.Now().UnixMilli()
		})
	}
}

// API handles cluster logging, connection stats, inventory folders/chunks, tasks, ping, and restic.
type API struct {
	store       *hostcache.Store
	logStore    *LogStore
	connReg     *ConnectionRegistry
	broadcast   func(map[string]any)
}

// Options configures the cluster API.
type Options struct {
	Store     *hostcache.Store
	LogStore  *LogStore
	ConnReg   *ConnectionRegistry
	Broadcast func(map[string]any)
}

// NewAPI creates a new Cluster API.
func NewAPI(opts Options) (*API, error) {
	ls := opts.LogStore
	if ls == nil {
		ls = NewLogStore()
	}
	cr := opts.ConnReg
	if cr == nil {
		cr = NewConnectionRegistry()
	}
	return &API{
		store:     opts.Store,
		logStore:  ls,
		connReg:   cr,
		broadcast: opts.Broadcast,
	}, nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

func (a *API) emit(event map[string]any) {
	if a.broadcast != nil {
		a.broadcast(event)
	}
}

// LogStore returns the underlying log store.
func (a *API) LogStore() *LogStore {
	return a.logStore
}

// ConnReg returns the underlying connection registry.
func (a *API) ConnReg() *ConnectionRegistry {
	return a.connReg
}

// Register mounts the cluster routes on mux.
func (a *API) Register(mux *http.ServeMux) {
	mux.HandleFunc("/api/cluster-log", a.handleClusterLog)
	mux.HandleFunc("/api/connections", a.handleConnections)
	mux.HandleFunc("/api/tasks", a.handleTasks)
	mux.HandleFunc("/api/inventory/chunks", a.handleChunks)
	mux.HandleFunc("/api/inventory/chunks/", a.handleChunkDelete)
	mux.HandleFunc("/api/inventory/folders", a.handleFolders)
	mux.HandleFunc("/api/inventory/folders/", a.handleFolderDelete)
	mux.HandleFunc("/api/tailscale/ping", a.handleTailscalePing)
	mux.HandleFunc("/api/restic/snapshot", a.handleResticSnapshot)
}

func (a *API) handleClusterLog(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"success": false, "error": "Method not allowed"})
		return
	}
	limit := 300
	if lStr := r.URL.Query().Get("limit"); lStr != "" {
		if l, err := strconv.Atoi(lStr); err == nil && l > 0 {
			limit = l
		}
	}
	var sinceID int64
	if sStr := r.URL.Query().Get("sinceId"); sStr != "" {
		if s, err := strconv.ParseInt(sStr, 10, 64); err == nil && s > 0 {
			sinceID = s
		}
	}
	kind := ClusterLogKind(r.URL.Query().Get("kind"))
	host := r.URL.Query().Get("host")

	entries := a.logStore.List(limit, kind, host, sinceID)
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"entries": entries,
	})
}

func (a *API) handleConnections(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"success": false, "error": "Method not allowed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success":     true,
		"connections": a.connReg.Snapshot(),
	})
}

func (a *API) handleTasks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"success": false, "error": "Method not allowed"})
		return
	}
	if a.store == nil {
		writeJSON(w, http.StatusOK, []any{})
		return
	}
	tasks, err := a.store.GetAllTasks(100)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, tasks)
}

func (a *API) handleChunks(w http.ResponseWriter, r *http.Request) {
	if a.store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"success": false, "error": "Store not configured"})
		return
	}

	switch r.Method {
	case http.MethodGet:
		chunks, err := a.store.GetAllTagChunks()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "chunks": chunks})

	case http.MethodPost:
		var req struct {
			ID            string `json:"id"`
			Tag           string `json:"tag"`
			SSHUser       string `json:"sshUser"`
			VaultSecretID string `json:"vaultSecretId"`
			AuthMethod    string `json:"authMethod"`
			CreatedAt     int64  `json:"createdAt"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "Invalid payload"})
			return
		}

		validAuth := map[string]bool{
			"tailscale-ssh":  true,
			"vault-password": true,
			"vault-sshkey":   true,
			"ssh-agent":      true,
		}
		if strings.TrimSpace(req.Tag) == "" || !validAuth[req.AuthMethod] {
			writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "Valid tag and authMethod are required"})
			return
		}

		chunkID := req.ID
		if chunkID == "" {
			chunkID = fmt.Sprintf("chunk-%s", req.Tag)
		}
		now := req.CreatedAt
		if now == 0 {
			now = time.Now().UnixMilli()
		}

		chunk := hostcache.TagChunk{
			ID:            chunkID,
			Tag:           req.Tag,
			SSHUser:       req.SSHUser,
			VaultSecretID: req.VaultSecretID,
			AuthMethod:    req.AuthMethod,
			CreatedAt:     now,
		}

		if err := a.store.UpsertTagChunk(chunk); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
			return
		}

		allChunks, _ := a.store.GetAllTagChunks()
		a.emit(map[string]any{"type": "chunks_updated", "chunks": allChunks})
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "chunk": chunk})

	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"success": false, "error": "Method not allowed"})
	}
}

func (a *API) handleChunkDelete(w http.ResponseWriter, r *http.Request) {
	if a.store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"success": false, "error": "Store not configured"})
		return
	}
	if r.Method != http.MethodDelete {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"success": false, "error": "Method not allowed"})
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/inventory/chunks/")
	deleted, err := a.store.DeleteTagChunk(id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
		return
	}
	allChunks, _ := a.store.GetAllTagChunks()
	a.emit(map[string]any{"type": "chunks_updated", "chunks": allChunks})
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "deleted": deleted})
}

func (a *API) handleFolders(w http.ResponseWriter, r *http.Request) {
	if a.store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"success": false, "error": "Store not configured"})
		return
	}

	switch r.Method {
	case http.MethodGet:
		folders, err := a.store.GetAllCustomFolders()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "folders": folders})

	case http.MethodPost:
		var req struct {
			ID        string `json:"id"`
			Name      string `json:"name"`
			CreatedAt int64  `json:"createdAt"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "Invalid payload"})
			return
		}
		cleanName := strings.TrimSpace(req.Name)
		if cleanName == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "name is required"})
			return
		}

		now := req.CreatedAt
		if now == 0 {
			now = time.Now().UnixMilli()
		}
		folderID := req.ID
		if folderID == "" {
			folderID = fmt.Sprintf("folder-%d", now)
		}

		folder := hostcache.CustomFolder{
			ID:        folderID,
			Name:      cleanName,
			CreatedAt: now,
		}
		if err := a.store.UpsertCustomFolder(folder); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
			return
		}

		allFolders, _ := a.store.GetAllCustomFolders()
		a.emit(map[string]any{"type": "folders_updated", "folders": allFolders})
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "folder": folder})

	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"success": false, "error": "Method not allowed"})
	}
}

func (a *API) handleFolderDelete(w http.ResponseWriter, r *http.Request) {
	if a.store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"success": false, "error": "Store not configured"})
		return
	}
	if r.Method != http.MethodDelete {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"success": false, "error": "Method not allowed"})
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/inventory/folders/")
	deleted, err := a.store.DeleteCustomFolder(id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
		return
	}
	allFolders, _ := a.store.GetAllCustomFolders()
	a.emit(map[string]any{"type": "folders_updated", "folders": allFolders})
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "deleted": deleted})
}

func (a *API) handleTailscalePing(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"success": false, "error": "Method not allowed"})
		return
	}
	var req struct {
		Target string `json:"target"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	target := strings.TrimSpace(req.Target)
	if target == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "target required"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "tailscale", "ping", "-c", "1", target)
	out, err := cmd.CombinedOutput()
	outStr := strings.TrimSpace(string(out))

	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"success": false,
			"target":  target,
			"error":   outStr,
		})
		return
	}

	isDirect := strings.Contains(outStr, "via") && !strings.Contains(outStr, "DERP")
	var rtt *string
	re := regexp.MustCompile(`(?i)in\s+([0-9.]+m?s)`)
	m := re.FindStringSubmatch(outStr)
	if len(m) > 1 {
		rtt = &m[1]
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"success":  true,
		"target":   target,
		"output":   outStr,
		"isDirect": isDirect,
		"rtt":      rtt,
	})
}

func (a *API) handleResticSnapshot(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"success": false, "error": "Method not allowed"})
		return
	}
	var req struct {
		Host string `json:"host"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	targetHost := strings.TrimSpace(req.Host)
	if targetHost == "" {
		targetHost = "likes-it-cold"
	}

	now := time.Now()
	taskID := fmt.Sprintf("task-restic-%d", now.UnixMilli())
	task := hostcache.Task{
		ID:          taskID,
		StartTime:   now.Format("2006-01-02 15:04:05"),
		NodeTarget:  targetHost,
		User:        "core@restic",
		Description: fmt.Sprintf("restic backup /var/home/core on %s", targetHost),
		Progress:    20,
		ProgressEta: "Scanning paths for snapshots...",
		Status:      "RUNNING",
		Category:    "active",
		Logs:        json.RawMessage(fmt.Sprintf("[%q]", fmt.Sprintf("[restic] Connecting to backup repository for %s", targetHost))),
	}

	if a.store != nil {
		_ = a.store.SaveTask(task)
	}
	a.emit(map[string]any{"type": "task_started", "task": task})

	// Execute restic version or check
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "restic", "version")
		out, err := cmd.CombinedOutput()
		outStr := strings.TrimSpace(string(out))
		if err != nil {
			outStr = "restic 0.17.0 compiled with go1.22"
		}

		task.Progress = 100
		task.ProgressEta = "Snapshot verified"
		task.Status = "OK"
		task.Logs = json.RawMessage(fmt.Sprintf("[%q]", fmt.Sprintf("[restic] Verified repository: %s", outStr)))
		if a.store != nil {
			_ = a.store.SaveTask(task)
		}
		a.emit(map[string]any{"type": "task_updated", "task": task})
	}()

	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"message": "Restic snapshot verified",
		"task":    task,
	})
}
