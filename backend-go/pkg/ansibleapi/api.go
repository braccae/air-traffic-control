package ansibleapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"air-traffic-control/backend/pkg/ansqueue"
	"air-traffic-control/backend/pkg/collections"
	"air-traffic-control/backend/pkg/hostcache"
)

var validGroupVarFile = regexp.MustCompile(`^[A-Za-z0-9._/-]+\.ya?ml$`)

// API implements the HTTP handlers for Ansible configuration, environment,
// dynamic inventory, galaxy collection install, module specs, playbook CRUD/run,
// and group-vars endpoints.
type API struct {
	store          *hostcache.Store
	queueStore     *ansqueue.Store
	playbookRunner *PlaybookRunnerState
	repoRoot       string
	ansibleCfgPath string
	playbooksDir   string
	broadcast      func(map[string]any)
	// collections backs the update/remove/enable endpoints. Nil falls back to
	// the process-wide manager.
	collections *collections.Manager
}

// APIOptions configures the Ansible API.
type APIOptions struct {
	Store          *hostcache.Store
	QueueStore     *ansqueue.Store
	RepoRoot       string
	AnsibleCfgPath string
	PlaybooksDir   string
	Broadcast      func(map[string]any)
}

// NewAPI constructs an Ansible API instance.
func NewAPI(opts APIOptions) (*API, error) {
	repoRoot := opts.RepoRoot
	if repoRoot == "" {
		repoRoot = ansqueue.ResolveWorkDir()
	}

	cfgPath := opts.AnsibleCfgPath
	if cfgPath == "" {
		cfgPath = filepath.Join(repoRoot, "ansible", "ansible.cfg")
	}

	pbDir := opts.PlaybooksDir
	if pbDir == "" {
		pbDir = filepath.Join(repoRoot, "ansible", "playbooks")
	}

	runner := NewPlaybookRunner(repoRoot, opts.Store, opts.Broadcast)
	runner.playbooksDir = pbDir

	return &API{
		store:          opts.Store,
		queueStore:     opts.QueueStore,
		playbookRunner: runner,
		repoRoot:       repoRoot,
		ansibleCfgPath: cfgPath,
		playbooksDir:   pbDir,
		broadcast:      opts.Broadcast,
	}, nil
}

// RunPlaybook satisfies cronapi.PlaybookRunner so scheduled playbooks run
// through the same detached background runner and stream to the UI.
func (a *API) RunPlaybook(ctx context.Context, playbook, target string, extraVars map[string]any) (string, error) {
	var ev any
	if len(extraVars) > 0 {
		ev = extraVars
	}
	taskID, _, err := a.playbookRunner.Run(context.WithoutCancel(ctx), RunRequest{
		Playbook:  playbook,
		Target:    target,
		ExtraVars: ev,
		Source:    "cron",
	})
	return taskID, err
}

// Register mounts all Ansible API endpoints on mux.
func (a *API) Register(mux *http.ServeMux) {
	// 1. Config
	mux.HandleFunc("/api/ansible/config", a.handleAnsibleConfig)

	// 2. Environment
	mux.HandleFunc("/api/ansible/environment", a.handleAnsibleEnvironment)

	// 3. Inventory & Galaxy
	mux.HandleFunc("/api/ansible/inventory", a.handleAnsibleInventory)
	mux.HandleFunc("/api/ansible/collections", a.handleGalaxyCollections)
	mux.HandleFunc("/api/ansible/collections/catalog", a.handleGalaxyCatalog)
	mux.HandleFunc("/api/ansible/collections/", a.handleGalaxyCollectionsSubtree)
	mux.HandleFunc("/api/ansible/collections/install", a.handleGalaxyInstall)
	mux.HandleFunc("/api/ansible/collections/remove", a.handleGalaxyRemove)
	mux.HandleFunc("/api/ansible/collections/enable", a.handleGalaxyEnable)
	mux.HandleFunc("/api/ansible/collections/update", a.handleGalaxyUpdate)
	mux.HandleFunc("/api/ansible/collections/updates", a.handleGalaxyUpdates)
	mux.HandleFunc("/api/ansible/collections/pin", a.handleGalaxyPin)
	mux.HandleFunc("/api/ansible/collections/unpin", a.handleGalaxyUnpin)
	mux.HandleFunc("/api/ansible/collections/auto-update", a.handleGalaxyAutoUpdate)

	// 4. Module specs
	mux.HandleFunc("/api/ansible/modules", a.handleAnsibleModules)
	mux.HandleFunc("/api/ansible/modules/", a.handleAnsibleModuleSubtree)

	// 5. Playbooks
	mux.HandleFunc("/api/playbooks", a.handlePlaybooks)
	mux.HandleFunc("/api/playbooks/", a.handlePlaybooksSubtree)
	mux.HandleFunc("/api/playbook/syntax-check", a.handlePlaybookSyntaxCheck)
	mux.HandleFunc("/api/playbook/run", a.handlePlaybookRun)
	mux.HandleFunc("/api/playbook/cancel", a.handlePlaybookCancel)

	// 6. Group vars
	mux.HandleFunc("/api/group-vars", a.handleGroupVars)
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

// GET & PUT /api/ansible/config
func (a *API) handleAnsibleConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		settings, _, err := LoadAnsibleConfig(a.ansibleCfgPath)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "settings": settings})

	case http.MethodPut:
		var rawBody map[string]any
		if err := json.NewDecoder(r.Body).Decode(&rawBody); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "Invalid settings body"})
			return
		}
		rawSettings := rawBody
		if nested, ok := rawBody["settings"].(map[string]any); ok {
			rawSettings = nested
		}

		existing, _, _ := LoadAnsibleConfig(a.ansibleCfgPath)

		if cb, ok := rawSettings["stdoutCallback"].(string); ok && cb != "" {
			validCallbacks := map[string]bool{"json": true, "yaml": true, "default": true}
			if !validCallbacks[cb] {
				writeJSON(w, http.StatusBadRequest, map[string]any{
					"success": false,
					"error":   fmt.Sprintf("Invalid stdoutCallback: %s. Must be one of json, yaml, default", cb),
				})
				return
			}
			existing.StdoutCallback = cb
		}

		if bm, ok := rawSettings["becomeMethod"].(string); ok && bm != "" {
			validMethods := map[string]bool{"run0": true, "sudo": true, "doas": true}
			if !validMethods[bm] {
				writeJSON(w, http.StatusBadRequest, map[string]any{
					"success": false,
					"error":   fmt.Sprintf("Invalid becomeMethod: %s. Must be one of run0, sudo, doas", bm),
				})
				return
			}
			existing.BecomeMethod = bm
		}

		if fVal, exists := rawSettings["forks"]; exists && fVal != nil {
			var forksNum int
			switch v := fVal.(type) {
			case float64:
				forksNum = int(v)
			case int:
				forksNum = v
			default:
				writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "Forks must be a number between 1 and 500"})
				return
			}
			if forksNum < 1 || forksNum > 500 {
				writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "Forks must be a number between 1 and 500"})
				return
			}
			existing.Forks = forksNum
		}

		if tVal, exists := rawSettings["timeout"]; exists && tVal != nil {
			var timeoutNum int
			switch v := tVal.(type) {
			case float64:
				timeoutNum = int(v)
			case int:
				timeoutNum = v
			default:
				writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "Timeout must be a number between 1 and 600"})
				return
			}
			if timeoutNum < 1 || timeoutNum > 600 {
				writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "Timeout must be a number between 1 and 600"})
				return
			}
			existing.Timeout = timeoutNum
		}

		if hk, ok := rawSettings["hostKeyChecking"].(bool); ok {
			existing.HostKeyChecking = hk
		}
		if pipe, ok := rawSettings["pipelining"].(bool); ok {
			existing.Pipelining = pipe
		}

		if vpfVal, exists := rawSettings["vaultPasswordFile"]; exists {
			if vpfVal == nil {
				existing.VaultPasswordFile = ""
			} else if vpfStr, ok := vpfVal.(string); ok {
				if !ValidateVaultPasswordFile(vpfStr) {
					writeJSON(w, http.StatusBadRequest, map[string]any{
						"success": false,
						"error":   "Invalid vaultPasswordFile: newlines and control characters are prohibited to prevent INI injection",
					})
					return
				}
				existing.VaultPasswordFile = vpfStr
			} else {
				writeJSON(w, http.StatusBadRequest, map[string]any{
					"success": false,
					"error":   "Invalid vaultPasswordFile: must be a string",
				})
				return
			}
		}

		savedRaw, err := SaveAnsibleConfig(a.ansibleCfgPath, existing)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": "Failed to write ansible.cfg"})
			return
		}

		if a.store != nil {
			t := hostcache.Task{
				ID:          fmt.Sprintf("task-%d", time.Now().UnixMilli()),
				StartTime:   time.Now().Format("2006-01-02 15:04:05"),
				NodeTarget:  "localhost",
				User:        "core@ansible",
				Description: "Update ansible.cfg settings",
				Progress:    100,
				ProgressEta: "Saved",
				Status:      "OK",
				Category:    "ansible",
				UpdatedAt:   time.Now().UnixMilli(),
			}
			_ = a.store.SaveTask(t)
			if a.broadcast != nil {
				a.broadcast(map[string]any{"type": "task_started", "task": t})
			}
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"success":  true,
			"settings": existing,
			"config":   existing,
			"raw":      savedRaw,
		})

	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"success": false, "error": "Method not allowed"})
	}
}

// GET /api/ansible/environment
func (a *API) handleAnsibleEnvironment(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"success": false, "error": "Method not allowed"})
		return
	}
	info := InspectEnvironment(a.repoRoot)
	writeJSON(w, http.StatusOK, info)
}

// GET /api/ansible/inventory
func (a *API) handleAnsibleInventory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"success": false, "error": "Method not allowed"})
		return
	}
	inv, err := BuildDynamicInventory(a.store)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, inv)
}

// POST /api/ansible/collections/install
func (a *API) handleGalaxyInstall(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"success": false, "error": "Method not allowed"})
		return
	}

	var req struct {
		Collection string `json:"collection"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Collection == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"error":   "Invalid collection name. Format must be <namespace>.<collection> (e.g. ansible.posix)",
		})
		return
	}

	if !CollectionNameRegex.MatchString(req.Collection) {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"error":   "Invalid collection name. Format must be <namespace>.<collection> (e.g. ansible.posix)",
		})
		return
	}

	out, err := InstallGalaxyCollection(a.repoRoot, req.Collection)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"success":    false,
			"collection": req.Collection,
			"error":      err.Error(),
			"output":     out,
		})
		return
	}

	// A manual install always wins over a previous opt-out.
	if mgr := a.collectionManager(); mgr != nil {
		_ = mgr.SetDisabled(req.Collection, false)
	}

	if a.store != nil {
		t := hostcache.Task{
			ID:          fmt.Sprintf("task-galaxy-%d", time.Now().UnixMilli()),
			StartTime:   time.Now().Format("2006-01-02 15:04:05"),
			NodeTarget:  "localhost",
			User:        "core@ansible",
			Description: fmt.Sprintf("ansible-galaxy collection install %s", req.Collection),
			Progress:    100,
			ProgressEta: "Installed",
			Status:      "OK",
			Category:    "ansible",
			UpdatedAt:   time.Now().UnixMilli(),
		}
		_ = a.store.SaveTask(t)
		if a.broadcast != nil {
			a.broadcast(map[string]any{"type": "task_started", "task": t})
		}
	}

	items, _ := ListGalaxyCollections(a.repoRoot)
	writeJSON(w, http.StatusOK, map[string]any{
		"success":     true,
		"collection":  req.Collection,
		"message":     fmt.Sprintf("Collection %s installed successfully", req.Collection),
		"output":      out,
		"collections": items,
	})
}

// GET /api/ansible/collections/catalog
//
// The baseline catalog is the single source of truth for what the coordinator
// keeps installed: the datacenter settings page renders exactly this list.
func (a *API) handleGalaxyCatalog(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"success": false, "error": "Method not allowed"})
		return
	}
	names := make([]string, 0, len(collections.Catalog))
	for _, entry := range collections.Catalog {
		names = append(names, entry.Name)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success":     true,
		"catalog":     collections.Catalog,
		"collections": names,
	})
}

// GET & DELETE /api/ansible/collections
func (a *API) handleGalaxyCollections(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		items, err := ListGalaxyCollections(a.repoRoot)
		disabled := []string{}
		pinned := map[string]string{}
		autoUpdate := false
		if mgr := a.collectionManager(); mgr != nil {
			disabled = mgr.Disabled()
			pinned = mgr.Pinned()
			autoUpdate = mgr.AutoUpdateEnabled()
		}
		required := []string{}
		for _, item := range items {
			if collections.Required(item.Name) {
				required = append(required, item.Name)
			}
		}
		if err != nil && len(items) == 0 {
			writeJSON(w, http.StatusOK, map[string]any{
				"success":     true,
				"collections": []GalaxyCollectionItem{},
				"disabled":    disabled,
				"required":    required,
				"pinned":      pinned,
				"autoUpdate":  autoUpdate,
				"warning":     err.Error(),
			})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"success":     true,
			"collections": items,
			"disabled":    disabled,
			"required":    required,
			"pinned":      pinned,
			"autoUpdate":  autoUpdate,
		})
	case http.MethodDelete:
		a.handleGalaxyRemove(w, r)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"success": false, "error": "Method not allowed"})
	}
}

// DELETE /api/ansible/collections/:name
func (a *API) handleGalaxyCollectionsSubtree(w http.ResponseWriter, r *http.Request) {
	trimmed := strings.TrimPrefix(r.URL.Path, "/api/ansible/collections/")
	if trimmed == "install" {
		a.handleGalaxyInstall(w, r)
		return
	}
	if trimmed == "remove" {
		a.handleGalaxyRemove(w, r)
		return
	}
	if r.Method != http.MethodDelete {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"success": false, "error": "Method not allowed"})
		return
	}
	a.removeCollectionByName(w, strings.TrimSpace(trimmed), false)
}

// POST /api/ansible/collections/remove
func (a *API) handleGalaxyRemove(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodDelete {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"success": false, "error": "Method not allowed"})
		return
	}
	var req struct {
		Collection string `json:"collection"`
		Force      bool   `json:"force"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Collection) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"error":   "Invalid collection name. Format must be <namespace>.<collection> (e.g. community.general)",
		})
		return
	}
	a.removeCollectionByName(w, strings.TrimSpace(req.Collection), req.Force)
}

func (a *API) removeCollectionByName(w http.ResponseWriter, collection string, force bool) {
	if !CollectionNameRegex.MatchString(collection) {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"error":   "Invalid collection name. Format must be <namespace>.<collection> (e.g. community.general)",
		})
		return
	}
	// community.general provides every become plugin ATC relies on and
	// ansible.posix backs the firewall/mount/service modules used by the
	// mutation queue. Removing either silently breaks real operations.
	if collections.Required(collection) && !force {
		writeJSON(w, http.StatusConflict, map[string]any{
			"success":    false,
			"collection": collection,
			"required":   true,
			"error": fmt.Sprintf(
				"%s is required by ATC's mutation pipeline (privilege escalation / service & firewall modules) and cannot be removed without force",
				collection,
			),
		})
		return
	}

	out, err := RemoveGalaxyCollection(a.repoRoot, collection)
	if err != nil {
		status := http.StatusInternalServerError
		if strings.Contains(err.Error(), "not installed") {
			status = http.StatusNotFound
		} else if strings.Contains(err.Error(), "cannot remove") {
			status = http.StatusBadRequest
		}
		writeJSON(w, status, map[string]any{
			"success":    false,
			"collection": collection,
			"error":      err.Error(),
		})
		return
	}
	if a.store != nil {
		t := hostcache.Task{
			ID:          fmt.Sprintf("task-galaxy-rm-%d", time.Now().UnixMilli()),
			StartTime:   time.Now().Format("2006-01-02 15:04:05"),
			NodeTarget:  "localhost",
			User:        "core@ansible",
			Description: fmt.Sprintf("Remove Ansible Galaxy collection %s", collection),
			Progress:    100,
			ProgressEta: "Removed",
			Status:      "OK",
			Category:    "ansible",
			UpdatedAt:   time.Now().UnixMilli(),
		}
		_ = a.store.SaveTask(t)
		if a.broadcast != nil {
			a.broadcast(map[string]any{"type": "task_started", "task": t})
		}
	}
	// Record the opt-out so the startup catalog and host-feature triggers stop
	// reinstalling it. Without this "Remove" would undo itself within minutes.
	disabled := []string{}
	if mgr := a.collectionManager(); mgr != nil {
		if setErr := mgr.SetDisabled(collection, true); setErr != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{
				"success":    false,
				"collection": collection,
				"error":      fmt.Sprintf("collection removed but the opt-out could not be persisted: %v", setErr),
			})
			return
		}
		disabled = mgr.Disabled()
	}

	items, _ := ListGalaxyCollections(a.repoRoot)
	writeJSON(w, http.StatusOK, map[string]any{
		"success":     true,
		"collection":  collection,
		"message":     fmt.Sprintf("Collection %s removed successfully", collection),
		"output":      out,
		"collections": items,
		"disabled":    disabled,
	})
}

// GET /api/ansible/modules
func (a *API) handleAnsibleModules(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"success": false, "error": "Method not allowed"})
		return
	}

	query := strings.ToLower(r.URL.Query().Get("q"))
	all, err := ListAnsibleModules(a.repoRoot, false)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"success": false, "error": err.Error()})
		return
	}

	var modules []ModuleCatalogEntry
	for _, entry := range all {
		if query == "" || strings.Contains(strings.ToLower(entry.Name), query) || strings.Contains(strings.ToLower(entry.Description), query) {
			modules = append(modules, entry)
		}
	}
	if len(modules) > 500 {
		modules = modules[:500]
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"total":   len(all),
		"modules": modules,
	})
}

// GET /api/ansible/modules/:name/spec
func (a *API) handleAnsibleModuleSubtree(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"success": false, "error": "Method not allowed"})
		return
	}

	trimmed := strings.TrimPrefix(r.URL.Path, "/api/ansible/modules/")
	parts := strings.Split(trimmed, "/")
	if len(parts) != 2 || parts[1] != "spec" {
		writeJSON(w, http.StatusNotFound, map[string]any{"success": false, "error": "Not found"})
		return
	}
	name := parts[0]

	if !IsSafeModuleName(name) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": fmt.Sprintf("Invalid module name: %s", name)})
		return
	}

	spec, err := GetAnsibleModuleSpec(a.repoRoot, name, false)
	if err != nil {
		status := http.StatusServiceUnavailable
		errStr := strings.ToLower(err.Error())
		if strings.Contains(errStr, "not found") || strings.Contains(errStr, "unable to find") || strings.Contains(errStr, "no documentation") {
			status = http.StatusNotFound
		}
		writeJSON(w, status, map[string]any{"success": false, "error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"success": true, "spec": spec})
}

func (a *API) handlePlaybooksSubtree(w http.ResponseWriter, r *http.Request) {
	trimmed := strings.TrimPrefix(r.URL.Path, "/api/playbooks/")
	if trimmed == "parse" {
		a.handlePlaybookParse(w, r)
		return
	}
	if strings.HasSuffix(trimmed, "/queue") && r.Method == http.MethodPost {
		name := strings.TrimSuffix(trimmed, "/queue")
		a.handlePlaybookQueue(w, r, name)
		return
	}
	a.handlePlaybookItem(w, r)
}

// GET & POST /api/playbooks
func (a *API) handlePlaybooks(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api/playbooks" {
		a.handlePlaybookItem(w, r)
		return
	}
	switch r.Method {
	case http.MethodGet:
		metas, err := ListPlaybooksMeta(a.playbooksDir)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}

		// Also support returning simple filenames list if client requests
		files := make([]string, 0)
		for _, m := range metas {
			files = append(files, m.Filename)
		}
		if metas == nil {
			metas = make([]PlaybookMeta, 0)
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"playbooks": files,
			"items":     metas,
		})

	case http.MethodPost:
		var req struct {
			Name    string `json:"name"`
			Content string `json:"content"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "Playbook name is required"})
			return
		}

		normalized := NormalizePlaybookName(req.Name)
		val := ValidatePlaybookName(normalized)
		if !val.Valid {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": val.Error})
			return
		}

		res, err := CreatePlaybook(a.playbooksDir, normalized, req.Content)
		if err != nil {
			status := http.StatusInternalServerError
			if strings.Contains(err.Error(), "already exists") {
				status = http.StatusConflict
			}
			writeJSON(w, status, map[string]any{"error": err.Error()})
			return
		}

		writeJSON(w, http.StatusCreated, map[string]any{"success": true, "name": res.Name, "content": res.Content})

	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "Method not allowed"})
	}
}

// GET, PUT, DELETE /api/playbooks/:name
func (a *API) handlePlaybookItem(w http.ResponseWriter, r *http.Request) {
	trimmed := strings.TrimPrefix(r.URL.Path, "/api/playbooks/")
	name := filepath.Base(trimmed)

	val := ValidatePlaybookName(name)
	if !val.Valid {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": val.Error})
		return
	}

	switch r.Method {
	case http.MethodGet:
		res, err := ReadPlaybook(a.playbooksDir, name)
		if err != nil {
			status := http.StatusInternalServerError
			if os.IsNotExist(err) || strings.Contains(err.Error(), "not found") {
				status = http.StatusNotFound
			}
			writeJSON(w, status, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "name": res.Name, "content": res.Content})

	case http.MethodPut:
		var req struct {
			Content string `json:"content"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "Playbook content must be a string"})
			return
		}
		res, err := UpdatePlaybook(a.playbooksDir, name, req.Content)
		if err != nil {
			status := http.StatusInternalServerError
			if os.IsNotExist(err) || strings.Contains(err.Error(), "not found") {
				status = http.StatusNotFound
			}
			writeJSON(w, status, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "name": res.Name, "content": res.Content})

	case http.MethodDelete:
		if IsProtectedPlaybook(name) {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": fmt.Sprintf("Cannot delete protected baseline playbook %s", name)})
			return
		}
		deletedName, err := DeletePlaybook(a.playbooksDir, name)
		if err != nil {
			status := http.StatusInternalServerError
			if os.IsNotExist(err) || strings.Contains(err.Error(), "not found") {
				status = http.StatusNotFound
			}
			writeJSON(w, status, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "name": deletedName})

	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "Method not allowed"})
	}
}

// POST /api/playbooks/parse
func (a *API) handlePlaybookParse(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"success": false, "error": "Method not allowed"})
		return
	}

	var req struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "Playbook content must be a string"})
		return
	}

	doc, errPayload, err := ParsePlaybookYamlDocument(req.Content)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errPayload)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"success":  true,
		"document": doc,
	})
}

// POST /api/playbook/syntax-check
func (a *API) handlePlaybookSyntaxCheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"success": false, "error": "Method not allowed"})
		return
	}

	var req struct {
		Playbook string `json:"playbook"`
		Content  string `json:"content"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.Playbook == "" {
		req.Playbook = "site.yml"
	}

	val := ValidatePlaybookName(req.Playbook)
	if !val.Valid {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "playbook": req.Playbook, "error": val.Error})
		return
	}

	out, err := CheckPlaybookSyntax(a.repoRoot, a.playbooksDir, req.Playbook, req.Content)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"success":  false,
			"playbook": req.Playbook,
			"error":    err.Error(),
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"success":  true,
		"playbook": req.Playbook,
		"output":   out,
	})
}

// POST /api/playbooks/:name/queue
func (a *API) handlePlaybookQueue(w http.ResponseWriter, r *http.Request, name string) {
	val := ValidatePlaybookName(name)
	if !val.Valid {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": val.Error})
		return
	}

	playbookPath := filepath.Join(a.playbooksDir, val.Name)
	if _, err := os.Stat(playbookPath); os.IsNotExist(err) {
		writeJSON(w, http.StatusNotFound, map[string]any{"success": false, "error": fmt.Sprintf("Playbook %s not found", val.Name)})
		return
	}

	if a.queueStore == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"success": false, "error": "Queue store not configured"})
		return
	}

	var req struct {
		Target    *string `json:"target"`
		CheckMode bool    `json:"checkMode"`
		Tags      string  `json:"tags"`
		ExtraVars any     `json:"extraVars"`
		Execute   bool    `json:"execute"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	target := "all"
	if req.Target != nil {
		target = strings.TrimSpace(*req.Target)
		if target == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": `target must be a non-empty inventory pattern (use "all")`})
			return
		}
	}

	pbRun := &ansqueue.PlaybookRun{
		Playbook:  val.Name,
		Target:    target,
		CheckMode: req.CheckMode,
		Tags:      strings.TrimSpace(req.Tags),
		ExtraVars: req.ExtraVars,
	}

	become := false
	qTask := ansqueue.Task{
		ID:              fmt.Sprintf("queue-playbook-%d", time.Now().UnixMilli()),
		Title:           fmt.Sprintf("Run playbook %s (%s)", val.Name, target),
		TargetNodeID:    fmt.Sprintf("playbook:%s", val.Name),
		TargetNodeName:  target,
		TargetHostRoute: target,
		Category:        "custom",
		PrimaryModule:   "ansible-playbook",
		Steps:           []ansqueue.Step{},
		PlaybookRun:     pbRun,
		Become:          &become,
		Status:          ansqueue.StatusStaged,
		CreatedAt:       time.Now().UnixMilli(),
	}

	enqueued, err := a.queueStore.Enqueue(qTask)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
		return
	}

	if a.broadcast != nil {
		snap := a.queueStore.Snapshot()
		a.broadcast(map[string]any{
			"type":  "queue_updated",
			"queue": map[string]any{"tasks": snap.Tasks, "isRunning": snap.IsRunning},
		})
	}

	if !req.Execute {
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "task": enqueued})
		return
	}

	inventoryPath := filepath.Join(a.repoRoot, "inventory", "tailscale_inventory.py")
	if _, err := os.Stat(inventoryPath); os.IsNotExist(err) {
		inventoryPath = ""
	}
	outcome, err := a.queueStore.ExecuteNextTask(context.WithoutCancel(r.Context()), ansqueue.Options{
		WorkDir:       a.repoRoot,
		TaskID:        enqueued.ID,
		InventoryPath: inventoryPath,
		Invalidator:   ansqueue.NewCacheInvalidator(a.store),
		TaskStore:     a.store,
		Broadcast:     a.broadcast,
	})
	if a.broadcast != nil {
		snap := a.queueStore.Snapshot()
		a.broadcast(map[string]any{
			"type":           "queue_execution_finished",
			"taskId":         enqueued.ID,
			"result":         outcome.Result,
			"remainingCount": outcome.RemainingCount,
			"haltedOnError":  outcome.HaltedOnError,
			"queue":          map[string]any{"tasks": snap.Tasks, "isRunning": snap.IsRunning},
		})
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
		return
	}
	success := outcome.Result != nil && outcome.Result.Success
	writeJSON(w, http.StatusOK, map[string]any{
		"success":        success,
		"task":           enqueued,
		"result":         outcome.Result,
		"remainingCount": outcome.RemainingCount,
		"haltedOnError":  outcome.HaltedOnError,
	})
}

// POST /api/playbook/run
func (a *API) handlePlaybookRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "Method not allowed"})
		return
	}

	var req struct {
		Playbook  string `json:"playbook"`
		Target    string `json:"target"`
		CheckMode bool   `json:"checkMode"`
		Tags      string `json:"tags"`
		ExtraVars any    `json:"extraVars"`
		Mode      string `json:"mode"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.Playbook == "" {
		req.Playbook = "site.yml"
	}
	if req.Target == "" {
		req.Target = "all"
	}

	// If mode == 'queue', stage into ansqueue
	if req.Mode == "queue" && a.queueStore != nil {
		tags := strings.TrimSpace(req.Tags)
		become := false
		qTask := ansqueue.Task{
			ID:              fmt.Sprintf("task-queue-%d", time.Now().UnixMilli()),
			TargetNodeID:    req.Target,
			TargetNodeName:  req.Target,
			TargetHostRoute: req.Target,
			Title:           fmt.Sprintf("Run playbook %s", req.Playbook),
			Category:        "custom",
			PrimaryModule:   "ansible-playbook",
			Status:          ansqueue.StatusStaged,
			Steps: []ansqueue.Step{
				{
					Name:   fmt.Sprintf("ansible-playbook %s", req.Playbook),
					Module: "ansible.builtin.command",
					Parameters: map[string]any{
						"_raw_params": fmt.Sprintf("ansible-playbook -i inventory/tailscale_inventory.py ansible/playbooks/%s", req.Playbook),
					},
				},
			},
			PlaybookRun: &ansqueue.PlaybookRun{
				Playbook:  req.Playbook,
				Target:    req.Target,
				CheckMode: req.CheckMode,
				Tags:      tags,
				ExtraVars: req.ExtraVars,
			},
			Become:    &become,
			CreatedAt: time.Now().UnixMilli(),
		}
		enqueued, err := a.queueStore.Enqueue(qTask)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		if a.broadcast != nil {
			snap := a.queueStore.Snapshot()
			a.broadcast(map[string]any{
				"type":  "queue_updated",
				"queue": map[string]any{"tasks": snap.Tasks, "isRunning": snap.IsRunning},
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "taskId": enqueued.ID, "task": enqueued})
		return
	}

	taskId, task, err := a.playbookRunner.Run(context.WithoutCancel(r.Context()), RunRequest{
		Playbook:  req.Playbook,
		Target:    req.Target,
		CheckMode: req.CheckMode,
		Tags:      req.Tags,
		ExtraVars: req.ExtraVars,
	})
	if err != nil {
		status := http.StatusInternalServerError
		if strings.Contains(err.Error(), "not found") {
			status = http.StatusNotFound
		} else if strings.Contains(err.Error(), "Invalid") || strings.Contains(err.Error(), "traversal") {
			status = http.StatusBadRequest
		}
		writeJSON(w, status, map[string]any{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"success": true, "taskId": taskId, "task": task})
}

// POST /api/playbook/cancel
func (a *API) handlePlaybookCancel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "Method not allowed"})
		return
	}

	var req struct {
		TaskId string `json:"taskId"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.TaskId == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "taskId required"})
		return
	}

	if a.playbookRunner.Cancel(req.TaskId) {
		writeJSON(w, http.StatusOK, map[string]any{
			"success": true,
			"message": fmt.Sprintf("Playbook task %s cancelled", req.TaskId),
		})
	} else {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "Active playbook process not found"})
	}
}

// GET, PUT, DELETE /api/group-vars
func (a *API) handleGroupVars(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		vars, err := a.store.GetAllGroupVars()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, vars)

	case http.MethodPut:
		var req struct {
			File    string `json:"file"`
			Content string `json:"content"`
			Vars    any    `json:"vars"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.File == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "file and content required"})
			return
		}

		// Validation: path validation (no .., no leading /, valid name)
		cleanFile := filepath.Clean(req.File)
		if strings.HasPrefix(req.File, "/") || strings.Contains(req.File, "..") || !validGroupVarFile.MatchString(cleanFile) {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "Invalid group var file path. Must end with .yml or .yaml and not traverse directories."})
			return
		}

		content := req.Content
		if content == "" && req.Vars != nil {
			b, _ := json.Marshal(req.Vars)
			content = string(b)
		}

		if err := a.store.UpsertGroupVar(req.File, content); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}

		task := hostcache.Task{
			ID:          fmt.Sprintf("task-%d", time.Now().UnixMilli()),
			StartTime:   time.Now().Format("2006-01-02 15:04:05"),
			NodeTarget:  req.File,
			User:        "core@ssh-agent",
			Description: fmt.Sprintf("ansible-vault encrypt & save %s (SQLite cached)", req.File),
			Progress:    100,
			ProgressEta: "Saved (AES256)",
			Status:      "OK",
			Category:    "vault",
			UpdatedAt:   time.Now().UnixMilli(),
		}
		_ = a.store.SaveTask(task)
		if a.broadcast != nil {
			a.broadcast(map[string]any{"type": "task_started", "task": task})
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"success": true,
			"file":    req.File,
			"content": content,
			"vars":    req.Vars,
		})

	case http.MethodDelete:
		file := r.URL.Query().Get("file")
		if file == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "file parameter required"})
			return
		}

		removed, err := a.store.DeleteGroupVar(file)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		if !removed {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": fmt.Sprintf("Group var file %s not found", file)})
			return
		}

		remaining, _ := a.store.GetAllGroupVars()
		writeJSON(w, http.StatusOK, map[string]any{
			"success":   true,
			"deleted":   file,
			"remaining": remaining,
		})

	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "Method not allowed"})
	}
}

// ---------------------------------------------------------------------------
// Collection lifecycle: updates, opt-out and re-enable
// ---------------------------------------------------------------------------

// collectionManager returns the wired manager, falling back to the process-wide
// one installed at startup.
func (a *API) collectionManager() *collections.Manager {
	if a != nil && a.collections != nil {
		return a.collections
	}
	return collections.Default()
}

// SetCollectionsManager wires the collection manager explicitly (tests use this
// to avoid depending on global startup wiring).
func (a *API) SetCollectionsManager(m *collections.Manager) {
	if a != nil {
		a.collections = m
	}
}

// GET /api/ansible/collections/updates
//
// Compares installed versions with the newest published ones. A collection whose
// lookup failed reports checkError instead of a fabricated "up to date".
func (a *API) handleGalaxyUpdates(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"success": false, "error": "Method not allowed"})
		return
	}
	mgr := a.collectionManager()
	if mgr == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"success": false,
			"error":   "Collection manager is not initialised",
		})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	statuses := mgr.CheckUpdates(ctx)
	updatesAvailable := 0
	for _, status := range statuses {
		if status.UpdateAvailable {
			updatesAvailable++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success":          true,
		"updates":          statuses,
		"updatesAvailable": updatesAvailable,
	})
}

// POST /api/ansible/collections/update  {"collection":"ns.name"} or {"all":true}
func (a *API) handleGalaxyUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"success": false, "error": "Method not allowed"})
		return
	}
	var req struct {
		Collection string `json:"collection"`
		All        bool   `json:"all"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "Invalid JSON body"})
		return
	}
	req.Collection = strings.TrimSpace(req.Collection)
	if !req.All && req.Collection == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"error":   "Provide a collection name or {\"all\":true}",
		})
		return
	}
	if req.Collection != "" && !CollectionNameRegex.MatchString(req.Collection) {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"error":   "Invalid collection name. Format must be <namespace>.<collection>",
		})
		return
	}
	if req.Collection != "" && req.Collection == "ansible.builtin" {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"error":   "ansible.builtin ships with ansible-core and cannot be updated independently",
		})
		return
	}

	mgr := a.collectionManager()
	if mgr == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"success": false,
			"error":   "Collection manager is not initialised",
		})
		return
	}

	names := []string{}
	if !req.All {
		names = append(names, req.Collection)
	}

	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 20*time.Minute)
	defer cancel()

	results := mgr.Update(ctx, names)
	// An explicit update is also an explicit "I want this installed".
	for _, name := range names {
		for _, result := range results {
			if result.Name == name && result.Error == "" {
				_ = mgr.SetDisabled(name, false)
			}
		}
	}
	installErr := ""
	for _, result := range results {
		if result.Error != "" && installErr == "" {
			installErr = result.Error
		}
	}

	items, _ := ListGalaxyCollections(a.repoRoot)
	status := http.StatusOK
	if installErr != "" {
		status = http.StatusInternalServerError
	}
	writeJSON(w, status, map[string]any{
		"success":     installErr == "",
		"results":     results,
		"collections": items,
		"error":       installErr,
	})
}

// POST /api/ansible/collections/enable  {"collection":"ns.name"}
func (a *API) handleGalaxyEnable(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"success": false, "error": "Method not allowed"})
		return
	}
	var req struct {
		Collection string `json:"collection"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Collection) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "collection is required"})
		return
	}
	req.Collection = strings.TrimSpace(req.Collection)
	if !CollectionNameRegex.MatchString(req.Collection) {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"error":   "Invalid collection name. Format must be <namespace>.<collection>",
		})
		return
	}

	mgr := a.collectionManager()
	if mgr == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"success": false,
			"error":   "Collection manager is not initialised",
		})
		return
	}

	if err := mgr.SetDisabled(req.Collection, false); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
		return
	}

	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 10*time.Minute)
	defer cancel()
	installed := mgr.Ensure(ctx, []string{req.Collection}, "re-enabled by operator")

	items, _ := ListGalaxyCollections(a.repoRoot)
	writeJSON(w, http.StatusOK, map[string]any{
		"success":     true,
		"collection":  req.Collection,
		"reinstalled": len(installed) > 0,
		"collections": items,
		"disabled":    mgr.Disabled(),
	})
}

// POST /api/ansible/collections/pin  {"collection":"ns.name","version":"1.2.3"}
//
// An empty version pins whatever is installed right now, which is the common
// "hold this working version" action.
func (a *API) handleGalaxyPin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"success": false, "error": "Method not allowed"})
		return
	}
	var req struct {
		Collection string `json:"collection"`
		Version    string `json:"version"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Collection) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "collection is required"})
		return
	}
	req.Collection = strings.TrimSpace(req.Collection)
	if !CollectionNameRegex.MatchString(req.Collection) || req.Collection == "ansible.builtin" {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"error":   "Invalid collection name. Format must be <namespace>.<collection>",
		})
		return
	}

	mgr := a.collectionManager()
	if mgr == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"success": false, "error": "Collection manager is not initialised"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	version, err := mgr.Pin(ctx, req.Collection, req.Version)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"success":    false,
			"collection": req.Collection,
			"error":      err.Error(),
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"success":    true,
		"collection": req.Collection,
		"version":    version,
		"pinned":     mgr.Pinned(),
	})
}

// POST /api/ansible/collections/unpin  {"collection":"ns.name"}
func (a *API) handleGalaxyUnpin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"success": false, "error": "Method not allowed"})
		return
	}
	var req struct {
		Collection string `json:"collection"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Collection) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "collection is required"})
		return
	}
	req.Collection = strings.TrimSpace(req.Collection)

	mgr := a.collectionManager()
	if mgr == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"success": false, "error": "Collection manager is not initialised"})
		return
	}
	if err := mgr.Unpin(req.Collection); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success":    true,
		"collection": req.Collection,
		"pinned":     mgr.Pinned(),
	})
}

// POST /api/ansible/collections/auto-update  {"enabled":true}
func (a *API) handleGalaxyAutoUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"success": false, "error": "Method not allowed"})
		return
	}
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "Invalid JSON body"})
		return
	}
	mgr := a.collectionManager()
	if mgr == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"success": false, "error": "Collection manager is not initialised"})
		return
	}
	if err := mgr.SetAutoUpdate(req.Enabled); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success":    true,
		"autoUpdate": mgr.AutoUpdateEnabled(),
	})
}
