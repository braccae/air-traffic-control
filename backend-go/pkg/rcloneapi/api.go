package rcloneapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"air-traffic-control/backend/pkg/ansqueue"
	"air-traffic-control/backend/pkg/hostcache"
	"air-traffic-control/backend/pkg/plugin"
	"air-traffic-control/backend/pkg/vaultapi"
)

// API provides HTTP routes for Rclone remotes and mounts.
type API struct {
	store        *hostcache.Store
	queueStore   *ansqueue.Store
	repoRoot     string
	defaultActor string
	broadcast    func(map[string]any)
}

// Options configures the Rclone API.
type Options struct {
	Store        *hostcache.Store
	QueueStore   *ansqueue.Store
	RepoRoot     string
	DefaultActor string
	Broadcast    func(map[string]any)
}

// NewAPI creates an Rclone API instance.
func NewAPI(opts Options) (*API, error) {
	actor := opts.DefaultActor
	if actor == "" {
		if u := os.Getenv("SUDO_USER"); u != "" {
			actor = u
		} else if u := os.Getenv("USER"); u != "" {
			actor = u
		} else {
			actor = "atc-coordinator"
		}
	}
	return &API{
		store:        opts.Store,
		queueStore:   opts.QueueStore,
		repoRoot:     opts.RepoRoot,
		defaultActor: actor,
		broadcast:    opts.Broadcast,
	}, nil
}

// Register mounts the rclone routes.
func (a *API) Register(mux *http.ServeMux) {
	mux.HandleFunc("/api/rclone/providers", a.handleProviders)
	mux.HandleFunc("/api/rclone/remotes", a.handleRemotes)
	mux.HandleFunc("/api/rclone/remotes/", a.handleRemoteSubtree)
	mux.HandleFunc("/api/rclone/nodes/", a.handleNodesSubtree)
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

func (a *API) handleProviders(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"success": false, "error": "Method not allowed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "providers": Providers})
}

func (a *API) handleRemotes(w http.ResponseWriter, r *http.Request) {
	if a.store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"success": false, "error": "Store not configured"})
		return
	}

	switch r.Method {
	case http.MethodGet:
		remotes, err := a.store.GetAllRcloneRemotes()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
			return
		}
		views := make([]RemoteView, 0, len(remotes))
		for _, rem := range remotes {
			views = append(views, ToRemoteView(rem))
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "remotes": views})

	case http.MethodPost:
		var req struct {
			ID       string            `json:"id"`
			Name     string            `json:"name"`
			Provider string            `json:"provider"`
			Fields   map[string]string `json:"fields"`
			Verify   bool              `json:"verify"`
			Password string            `json:"password"`
			Actor    string            `json:"actor"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "Invalid payload"})
			return
		}
		provider := FindProvider(req.Provider)
		if provider == nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": fmt.Sprintf("Unknown provider: %s", req.Provider)})
			return
		}
		cleanName, err := SanitizeRemoteName(req.Name)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": err.Error()})
			return
		}

		// Check existing to preserve secret fields if not supplied
		var existing *hostcache.RcloneRemote
		if req.ID != "" {
			if r, found, _ := a.store.GetRcloneRemote(req.ID); found {
				existing = &r
			}
		}
		if existing == nil {
			if r, found, _ := a.store.GetRcloneRemoteByName(cleanName); found {
				existing = &r
			}
		}

		// Decrypt existing fields if needed
		existingFields := make(map[string]string)
		if existing != nil {
			existingFields, _ = a.deserializeRemoteConfig(r.Context(), existing.ConfigJSON, req.Password, req.Actor, fmt.Sprintf("POST /api/rclone/remotes (update %s)", cleanName))
		}

		merged := make(map[string]string)
		for k, v := range existingFields {
			merged[k] = v
		}
		for k, v := range req.Fields {
			trimmed := strings.TrimSpace(v)
			if trimmed == "" || strings.HasPrefix(trimmed, "••••••••") {
				continue
			}
			merged[k] = trimmed
		}

		// Validate required fields
		for _, f := range provider.Fields {
			if f.Required {
				if v, ok := merged[f.Name]; !ok || strings.TrimSpace(v) == "" {
					writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": fmt.Sprintf("Missing required field: %s", f.Label)})
					return
				}
			}
		}

		if req.Verify {
			vRes := VerifyRemoteConfig(r.Context(), cleanName, req.Provider, merged)
			if !vRes.OK {
				writeJSON(w, http.StatusBadRequest, map[string]any{
					"success":      false,
					"error":        fmt.Sprintf("Remote verification failed: %s", vRes.Error),
					"verification": vRes,
				})
				return
			}
		}

		// Serialize to vault
		configMap := make(map[string]string)
		for _, f := range provider.Fields {
			val, ok := merged[f.Name]
			if !ok || strings.TrimSpace(val) == "" {
				continue
			}
			if f.Secret {
				if strings.HasPrefix(val, VaultRefPrefix) {
					configMap[f.Name] = val
					continue
				}
				varName := BuildRcloneVaultVarName(cleanName, f.Name)
				vaultBlock, err := vaultapi.EncryptVaultString(r.Context(), a.repoRoot, val, varName, vaultapi.EncryptOptions{Password: req.Password})
				if err != nil {
					writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": err.Error()})
					return
				}
				if err := a.store.UpsertVaultVariable(varName, vaultBlock); err != nil {
					writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
					return
				}
				configMap[f.Name] = VaultRefPrefix + varName
			} else {
				configMap[f.Name] = val
			}
		}

		configJSONBytes, _ := json.Marshal(configMap)
		now := time.Now().UnixMilli()
		remoteID := req.ID
		if remoteID == "" {
			if existing != nil {
				remoteID = existing.ID
			} else {
				rnd := make([]byte, 4)
				_, _ = rand.Read(rnd)
				remoteID = fmt.Sprintf("rclone-%d-%s", now, hex.EncodeToString(rnd))
			}
		}

		record := hostcache.RcloneRemote{
			ID:         remoteID,
			Name:       cleanName,
			Provider:   req.Provider,
			Type:       provider.Type,
			ConfigJSON: string(configJSONBytes),
			CreatedAt:  now,
			UpdatedAt:  now,
		}
		if existing != nil {
			record.CreatedAt = existing.CreatedAt
		}

		if err := a.store.UpsertRcloneRemote(record); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
			return
		}

		// Update name in mounts if needed
		if mounts, err := a.store.GetRcloneMounts(""); err == nil {
			for _, m := range mounts {
				if m.RemoteID == record.ID && m.RemoteName != cleanName {
					m.RemoteName = cleanName
					m.UpdatedAt = now
					_ = a.store.UpsertRcloneMount(m)
				}
			}
		}

		allRemotes, _ := a.store.GetAllRcloneRemotes()
		views := make([]RemoteView, 0, len(allRemotes))
		for _, rem := range allRemotes {
			views = append(views, ToRemoteView(rem))
		}
		a.emit(map[string]any{"type": "rclone_remotes_updated", "remotes": views})

		writeJSON(w, http.StatusOK, map[string]any{"success": true, "remote": ToRemoteView(record)})

	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"success": false, "error": "Method not allowed"})
	}
}

func (a *API) handleRemoteSubtree(w http.ResponseWriter, r *http.Request) {
	if a.store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"success": false, "error": "Store not configured"})
		return
	}
	sub := strings.TrimPrefix(r.URL.Path, "/api/rclone/remotes/")
	if sub == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "Missing remote ID"})
		return
	}

	parts := strings.Split(sub, "/")
	id := parts[0]

	if len(parts) == 1 {
		switch r.Method {
		case http.MethodGet:
			remote, found, err := a.store.GetRcloneRemote(id)
			if err != nil || !found {
				writeJSON(w, http.StatusNotFound, map[string]any{"success": false, "error": "Remote not found"})
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"success": true, "remote": ToRemoteView(remote)})

		case http.MethodDelete:
			existing, found, _ := a.store.GetRcloneRemote(id)
			if found {
				var parsed map[string]string
				_ = json.Unmarshal([]byte(existing.ConfigJSON), &parsed)
				for _, v := range parsed {
					if strings.HasPrefix(v, VaultRefPrefix) {
						varName := strings.TrimSpace(strings.TrimPrefix(v, VaultRefPrefix))
						_, _ = a.store.DeleteVaultVariable(varName)
					}
				}
			}
			deleted, err := a.store.DeleteRcloneRemote(id)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
				return
			}
			allRemotes, _ := a.store.GetAllRcloneRemotes()
			views := make([]RemoteView, 0, len(allRemotes))
			for _, rem := range allRemotes {
				views = append(views, ToRemoteView(rem))
			}
			a.emit(map[string]any{"type": "rclone_remotes_updated", "remotes": views})
			writeJSON(w, http.StatusOK, map[string]any{"success": true, "deleted": deleted})

		default:
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"success": false, "error": "Method not allowed"})
		}
		return
	}

	action := parts[1]
	switch action {
	case "verify":
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"success": false, "error": "Method not allowed"})
			return
		}
		remote, found, _ := a.store.GetRcloneRemote(id)
		if !found {
			writeJSON(w, http.StatusNotFound, map[string]any{"success": false, "error": "Remote not found"})
			return
		}

		var req struct {
			Actor    string `json:"actor"`
			Password string `json:"password"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)

		fields, err := a.deserializeRemoteConfig(r.Context(), remote.ConfigJSON, req.Password, req.Actor, fmt.Sprintf("rclone.verifyRemote:%s", remote.Name))
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
			return
		}

		res := VerifyRemoteConfig(r.Context(), remote.Name, remote.Provider, fields)
		status := http.StatusOK
		if !res.OK {
			status = http.StatusBadGateway
		}
		writeJSON(w, status, map[string]any{"success": res.OK, "verification": res})

	case "install":
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"success": false, "error": "Method not allowed"})
			return
		}
		remote, found, _ := a.store.GetRcloneRemote(id)
		if !found {
			writeJSON(w, http.StatusNotFound, map[string]any{"success": false, "error": "Remote not found"})
			return
		}

		var req struct {
			NodeID       string `json:"nodeId"`
			MountTarget  string `json:"mountTarget"`
			Policy       string `json:"policy"`
			MountOptions string `json:"mountOptions"`
			Actor        string `json:"actor"`
			Password     string `json:"password"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "Invalid payload"})
			return
		}
		if req.NodeID == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "nodeId is required"})
			return
		}
		policy := req.Policy
		if policy == "" {
			policy = "systemd-mount"
		}
		if !IsValidMountPolicy(policy) {
			writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": fmt.Sprintf("Invalid mount policy: %s", policy)})
			return
		}

		targetNode, found, err := a.store.GetHostByID(req.NodeID)
		if err != nil || !found {
			writeJSON(w, http.StatusNotFound, map[string]any{"success": false, "error": fmt.Sprintf("Node not found: %s", req.NodeID)})
			return
		}

		targetMount := req.MountTarget
		if targetMount == "" {
			targetMount = fmt.Sprintf("/mnt/rclone/%s", remote.Name)
		}
		cleanTarget, err := SanitizeMountTarget(targetMount)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": err.Error()})
			return
		}

		targetHostRoute := targetNode.TailscaleHost
		if targetHostRoute == "" {
			targetHostRoute = targetNode.TailscaleIP
		}
		if targetHostRoute == "" {
			targetHostRoute = targetNode.IP
		}
		if targetHostRoute == "" {
			targetHostRoute = targetNode.Name
		}

		fields, err := a.deserializeRemoteConfig(r.Context(), remote.ConfigJSON, req.Password, req.Actor, fmt.Sprintf("rclone.install:%s (%s)", remote.Name, policy))
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
			return
		}

		configContent, err := BuildRcloneConfigSection(r.Context(), remote.Name, remote.Provider, fields)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": err.Error()})
			return
		}

		mountOpts := req.MountOptions
		if mountOpts == "" {
			mountOpts = "--vfs-cache-mode writes --dir-cache-time 5m"
		}

		// Compile task using Go plugin
		var taskSpec plugin.TaskSpec
		p, ok := plugin.Get("mount/rclone")
		if !ok {
			writeJSON(w, http.StatusBadGateway, map[string]any{"success": false, "error": "mount/rclone plugin not registered"})
			return
		}
		actor, ok := p.(plugin.Actor)
		if !ok {
			writeJSON(w, http.StatusBadGateway, map[string]any{"success": false, "error": "mount/rclone does not implement Actor"})
			return
		}

		facts := plugin.HostFacts{
			NodeID:    targetNode.ID,
			HostRoute: targetHostRoute,
			OSFamily:  "linux",
		}
		taskSpec, err = actor.Compile(facts, "install_remote", map[string]any{
			"name":          remote.Name,
			"configContent": configContent,
			"mountTarget":   cleanTarget,
			"policy":        policy,
			"mountOptions":  mountOpts,
		})
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]any{"success": false, "error": fmt.Sprintf("Failed to compile rclone task: %v", err)})
			return
		}

		// Convert steps to ansqueue.Step
		queueSteps := make([]ansqueue.Step, 0, len(taskSpec.Steps))
		for _, s := range taskSpec.Steps {
			queueSteps = append(queueSteps, ansqueue.Step{
				Name:       s.Name,
				Module:     s.Module,
				Parameters: s.Params,
			})
		}

		vaultMasks := make(map[string]string)
		for k, v := range MaskRemoteConfig(remote.ConfigJSON, remote.Name) {
			if strings.HasPrefix(v, "••••••••") {
				vaultMasks[k] = v
			}
		}
		for k, v := range taskSpec.SecretsMasked {
			vaultMasks[k] = v
		}

		var stagedTask *ansqueue.Task
		if a.queueStore != nil {
			becomeVal := true
			primaryMod := "ansible.builtin.copy"
			if len(queueSteps) > 0 && queueSteps[0].Module != "" {
				primaryMod = queueSteps[0].Module
			}
			taskToStage := ansqueue.Task{
				Title:           taskSpec.Name,
				TargetNodeID:    targetNode.ID,
				TargetNodeName:  targetNode.Name,
				TargetHostRoute: targetHostRoute,
				TargetSSHUser:   targetNode.SSHUser,
				TargetSSHPort:   targetNode.Port,
				Category:        "storage",
				PrimaryModule:   primaryMod,
				Steps:           queueSteps,
				SecretsMasked:   vaultMasks,
				Become:          &becomeVal,
				BecomeMethod:    "run0",
				Status:          ansqueue.StatusStaged,
				CreatedAt:       time.Now().UnixMilli(),
			}
			t, err := a.queueStore.Enqueue(taskToStage)
			if err == nil {
				stagedTask = &t
			}
		}

		now := time.Now().UnixMilli()
		mountRecord := hostcache.RcloneMount{
			NodeID:      targetNode.ID,
			RemoteID:    remote.ID,
			RemoteName:  remote.Name,
			MountTarget: cleanTarget,
			Policy:      policy,
			Status:      "pending",
			InstalledAt: now,
			UpdatedAt:   now,
		}
		_ = a.store.UpsertRcloneMount(mountRecord)

		if mounts, err := a.store.GetRcloneMounts(targetNode.ID); err == nil {
			a.emit(map[string]any{"type": "rclone_mounts_updated", "mounts": mounts})
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"success":       true,
			"mount":         mountRecord,
			"task":          stagedTask,
			"secretsMasked": vaultMasks,
		})

	default:
		writeJSON(w, http.StatusNotFound, map[string]any{"success": false, "error": "Unknown rclone action"})
	}
}

func (a *API) handleNodesSubtree(w http.ResponseWriter, r *http.Request) {
	if a.store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"success": false, "error": "Store not configured"})
		return
	}
	sub := strings.TrimPrefix(r.URL.Path, "/api/rclone/nodes/")
	parts := strings.Split(sub, "/")
	nodeID := parts[0]

	if len(parts) == 2 && parts[1] == "mounts" {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"success": false, "error": "Method not allowed"})
			return
		}
		mounts, err := a.store.GetRcloneMounts(nodeID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "mounts": mounts})
		return
	}

	if len(parts) == 3 && parts[1] == "mounts" {
		remoteID := parts[2]
		if r.Method != http.MethodDelete {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"success": false, "error": "Method not allowed"})
			return
		}
		deleted, err := a.store.DeleteRcloneMount(nodeID, remoteID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
			return
		}
		mounts, _ := a.store.GetRcloneMounts(nodeID)
		a.emit(map[string]any{"type": "rclone_mounts_updated", "mounts": mounts})
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "deleted": deleted})
		return
	}

	writeJSON(w, http.StatusNotFound, map[string]any{"success": false, "error": "Route not found"})
}

func (a *API) deserializeRemoteConfig(ctx context.Context, configJSON string, password, actor, action string) (map[string]string, error) {
	var parsed map[string]string
	_ = json.Unmarshal([]byte(configJSON), &parsed)
	if parsed == nil {
		return make(map[string]string), nil
	}
	out := make(map[string]string, len(parsed))

	act := actor
	if act == "" {
		act = a.defaultActor
	}

	for k, v := range parsed {
		if strings.HasPrefix(v, VaultRefPrefix) {
			varName := strings.TrimSpace(strings.TrimPrefix(v, VaultRefPrefix))
			vv, found, err := a.store.GetVaultVariable(varName)
			if err != nil || !found {
				return nil, fmt.Errorf("Missing Ansible Vault variable %q for rclone secret field %q", varName, k)
			}
			plaintext, err := vaultapi.DecryptVaultString(ctx, a.repoRoot, vv.VaultBlock, vaultapi.DecryptOptions{Password: password})
			if err != nil {
				return nil, fmt.Errorf("Failed to decrypt %q: %w", varName, err)
			}
			out[k] = plaintext

			// Audit log entry
			passSource := "inline"
			if password == "" {
				passSource = vaultapi.ResolveVaultPasswordSource(a.repoRoot, vaultapi.ResolveOptions{}).Source
			}
			rnd := make([]byte, 8)
			_, _ = rand.Read(rnd)
			_ = a.store.RecordVaultAuditEvent(hostcache.VaultAuditEvent{
				ID:             hex.EncodeToString(rnd),
				Timestamp:      time.Now().UnixMilli(),
				VariableName:   varName,
				Actor:          act,
				Host:           "coordinator",
				Action:         action,
				PasswordSource: passSource,
			})
		} else {
			out[k] = v
		}
	}
	return out, nil
}
