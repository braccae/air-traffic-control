package vaultapi

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"air-traffic-control/backend/pkg/hostcache"
)

// API provides HTTP routes for Ansible Vault variable storage and decryption audit logs.
type API struct {
	store        *hostcache.Store
	repoRoot     string
	defaultActor string
	defaultHost  string
}

// Options configures the Vault API.
type Options struct {
	Store        *hostcache.Store
	RepoRoot     string
	DefaultActor string
	DefaultHost  string
}

// NewAPI creates a new Vault API instance.
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
	host := opts.DefaultHost
	if host == "" {
		host = "n/a"
	}
	return &API{
		store:        opts.Store,
		repoRoot:     opts.RepoRoot,
		defaultActor: actor,
		defaultHost:  host,
	}, nil
}

// Register mounts the vault routes on the provided ServeMux.
func (a *API) Register(mux *http.ServeMux) {
	mux.HandleFunc("/api/vault/status", a.handleStatus)
	mux.HandleFunc("/api/vault/audit", a.handleAudit)
	mux.HandleFunc("/api/vault/variables", a.handleVariables)
	mux.HandleFunc("/api/vault/variables/", a.handleVariableSubtree)
	mux.HandleFunc("/api/vault/encrypt", a.handleEncrypt)
	mux.HandleFunc("/api/vault/decrypt", a.handleDecrypt)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

// VariableView is the safe view of a vault variable returned over the wire.
type VariableView struct {
	Name        string `json:"name"`
	CreatedAt   int64  `json:"createdAt"`
	UpdatedAt   int64  `json:"updatedAt"`
	MaskedValue string `json:"maskedValue"`
}

func toVariableView(v hostcache.VaultVariable) VariableView {
	return VariableView{
		Name:        v.Name,
		CreatedAt:   v.CreatedAt,
		UpdatedAt:   v.UpdatedAt,
		MaskedValue: MaskVaultValue(v.Name),
	}
}

func (a *API) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"success": false, "error": "Method not allowed"})
		return
	}
	source := ResolveVaultPasswordSource(a.repoRoot, ResolveOptions{})
	writeJSON(w, http.StatusOK, map[string]any{
		"success":           true,
		"hasPasswordSource": source.HasPassword,
		"source":            source.Source,
	})
}

func (a *API) handleAudit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"success": false, "error": "Method not allowed"})
		return
	}
	if a.store == nil {
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "events": []any{}, "total": 0})
		return
	}

	varName := r.URL.Query().Get("variable")
	host := r.URL.Query().Get("host")
	limit := 200
	if rawLimit := r.URL.Query().Get("limit"); rawLimit != "" {
		if parsed, err := strconv.Atoi(rawLimit); err == nil && parsed > 0 {
			limit = parsed
		}
	}

	events, err := a.store.GetVaultAuditEvents(varName, host, limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"events":  events,
		"total":   len(events),
	})
}

func (a *API) handleVariables(w http.ResponseWriter, r *http.Request) {
	if a.store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"success": false, "error": "Vault variable store is not configured"})
		return
	}

	switch r.Method {
	case http.MethodGet:
		vars, err := a.store.ListVaultVariables()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
			return
		}
		views := make([]VariableView, 0, len(vars))
		for _, v := range vars {
			views = append(views, toVariableView(v))
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "variables": views, "total": len(views)})

	case http.MethodPost:
		var req struct {
			Name         string `json:"name"`
			VarName      string `json:"varName"`
			VariableName string `json:"variableName"`
			Key          string `json:"key"`
			Value        string `json:"value"`
			Plaintext    string `json:"plaintext"`
			Payload      string `json:"payload"`
			Password     string `json:"password"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "Invalid request payload"})
			return
		}
		if req.Name == "" {
			if req.VarName != "" {
				req.Name = req.VarName
			} else if req.VariableName != "" {
				req.Name = req.VariableName
			} else if req.Key != "" {
				req.Name = req.Key
			}
		}
		if req.Value == "" {
			if req.Plaintext != "" {
				req.Value = req.Plaintext
			} else if req.Payload != "" {
				req.Value = req.Payload
			}
		}
		req.Name = strings.TrimSpace(req.Name)
		if !IsValidVaultVariableName(req.Name) {
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"success": false,
				"error":   "Invalid variable name: must match [A-Za-z_][A-Za-z0-9_]*",
			})
			return
		}
		if req.Value == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"success": false,
				"error":   "A non-empty value is required",
			})
			return
		}
		if _, exists, _ := a.store.GetVaultVariable(req.Name); exists {
			writeJSON(w, http.StatusConflict, map[string]any{
				"success": false,
				"error":   fmt.Sprintf("Vault variable %q already exists", req.Name),
			})
			return
		}

		vaultBlock, err := EncryptVaultString(r.Context(), a.repoRoot, req.Value, req.Name, EncryptOptions{Password: req.Password})
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": err.Error()})
			return
		}

		if err := a.store.UpsertVaultVariable(req.Name, vaultBlock); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
			return
		}

			// Record audit event for insert
			a.recordVaultAuditEvent(hostcache.VaultAuditEvent{
				ID:             generateEventID(),
				Timestamp:      time.Now().UnixMilli(),
				VariableName:   req.Name,
				Actor:          a.defaultActor,
				Host:           a.defaultHost,
				Action:         "POST /api/vault/variables",
				PasswordSource: "inline",
			})

			stored, found, _ := a.store.GetVaultVariable(req.Name)
			if !found {
				stored = hostcache.VaultVariable{Name: req.Name, CreatedAt: time.Now().UnixMilli(), UpdatedAt: time.Now().UnixMilli()}
			}
			writeJSON(w, http.StatusCreated, map[string]any{"success": true, "variable": toVariableView(stored)})

	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"success": false, "error": "Method not allowed"})
	}
}

func (a *API) handleVariableSubtree(w http.ResponseWriter, r *http.Request) {
	if a.store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"success": false, "error": "Vault variable store is not configured"})
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/api/vault/variables/")
	if name == "" || strings.Contains(name, "/") {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "Invalid variable name"})
		return
	}

	switch r.Method {
	case http.MethodPut:
		currentName := name
		if !IsValidVaultVariableName(currentName) {
			writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "Invalid variable name in path"})
			return
		}
		existing, exists, err := a.store.GetVaultVariable(currentName)
		if err != nil || !exists {
			writeJSON(w, http.StatusNotFound, map[string]any{"success": false, "error": fmt.Sprintf("Vault variable %q not found", currentName)})
			return
		}

		var req struct {
			Value    *string `json:"value"`
			NewName  *string `json:"newName"`
			Password string  `json:"password"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "Invalid request payload"})
			return
		}

		targetName := currentName
		if req.NewName != nil && *req.NewName != currentName {
			if !IsValidVaultVariableName(*req.NewName) {
				writeJSON(w, http.StatusBadRequest, map[string]any{
					"success": false,
					"error":   "Invalid new name: must match [A-Za-z_][A-Za-z0-9_]*",
				})
				return
			}
			if _, exists, _ := a.store.GetVaultVariable(*req.NewName); exists {
				writeJSON(w, http.StatusConflict, map[string]any{
					"success": false,
					"error":   fmt.Sprintf("Vault variable %q already exists", *req.NewName),
				})
				return
			}
			targetName = *req.NewName
		}

		var vaultBlock string
		if req.Value == nil {
			// Rename only: re-frame the existing ciphertext under the new name
			raw, err := ExtractRawVaultPayload(existing.VaultBlock)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": err.Error()})
				return
			}
			vaultBlock = FormatVaultEntry(targetName, raw)
		} else {
			if *req.Value == "" {
				writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "A non-empty value is required"})
				return
			}
			vb, err := EncryptVaultString(r.Context(), a.repoRoot, *req.Value, targetName, EncryptOptions{Password: req.Password})
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": err.Error()})
				return
			}
			vaultBlock = vb
		}

		if err := a.store.UpsertVaultVariable(targetName, vaultBlock); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
			return
		}
		if targetName != currentName {
			_, _ = a.store.DeleteVaultVariable(currentName)
		}

		// Record audit event for update/rename
		a.recordVaultAuditEvent(hostcache.VaultAuditEvent{
			ID:             generateEventID(),
			Timestamp:      time.Now().UnixMilli(),
			VariableName:   targetName,
			Actor:          a.defaultActor,
			Host:           a.defaultHost,
			Action:         "PUT /api/vault/variables/" + currentName,
			PasswordSource: "inline",
		})

		stored, found, _ := a.store.GetVaultVariable(targetName)
		if !found {
			stored = hostcache.VaultVariable{Name: targetName, CreatedAt: time.Now().UnixMilli(), UpdatedAt: time.Now().UnixMilli()}
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "variable": toVariableView(stored)})

	case http.MethodDelete:
		deleted, err := a.store.DeleteVaultVariable(name)
		if err != nil || !deleted {
			writeJSON(w, http.StatusNotFound, map[string]any{"success": false, "error": fmt.Sprintf("Vault variable %q not found", name)})
			return
		}

		// Record audit event for delete
		a.recordVaultAuditEvent(hostcache.VaultAuditEvent{
			ID:             generateEventID(),
			Timestamp:      time.Now().UnixMilli(),
			VariableName:   name,
			Actor:          a.defaultActor,
			Host:           a.defaultHost,
			Action:         "DELETE /api/vault/variables/" + name,
			PasswordSource: "inline",
		})

		writeJSON(w, http.StatusOK, map[string]any{"success": true, "deleted": true, "name": name})

	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"success": false, "error": "Method not allowed"})
	}
}

func (a *API) handleEncrypt(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"success": false, "error": "Method not allowed"})
		return
	}
	var req struct {
		Plaintext string `json:"plaintext"`
		Value     string `json:"value"`
		Payload   string `json:"payload"`
		VarName   string `json:"varName"`
		Name      string `json:"name"`
		Key       string `json:"key"`
		Password  string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "Invalid request payload"})
		return
	}
	if req.Plaintext == "" {
		if req.Value != "" {
			req.Plaintext = req.Value
		} else if req.Payload != "" {
			req.Plaintext = req.Payload
		}
	}
	if req.VarName == "" {
		if req.Name != "" {
			req.VarName = req.Name
		} else if req.Key != "" {
			req.VarName = req.Key
		}
	}
	req.VarName = strings.TrimSpace(req.VarName)
	if req.Plaintext == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "Missing or invalid plaintext"})
		return
	}

	vaultBlock, err := EncryptVaultString(r.Context(), a.repoRoot, req.Plaintext, req.VarName, EncryptOptions{Password: req.Password})
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": err.Error()})
		return
	}

	// If a variable name was provided and store is available, persist the vault variable.
	// This ensures SSH keys and other secrets encrypted via this endpoint are durably stored
	// in the vault_variables table and can be referenced by name (e.g., as vaultSecretId).
	if req.VarName != "" && a.store != nil {
		if err := a.store.UpsertVaultVariable(req.VarName, vaultBlock); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
			return
		}
		a.recordVaultAuditEvent(hostcache.VaultAuditEvent{
			ID:             generateEventID(),
			Timestamp:      time.Now().UnixMilli(),
			VariableName:   req.VarName,
			Actor:          a.defaultActor,
			Host:           a.defaultHost,
			Action:         "POST /api/vault/encrypt",
			PasswordSource: "inline",
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{"success": true, "vaultBlock": vaultBlock})
}

func (a *API) handleDecrypt(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"success": false, "error": "Method not allowed"})
		return
	}
	var req struct {
		VaultBlock string `json:"vaultBlock"`
		Password   string `json:"password"`
		VarName    string `json:"varName"`
		Host       string `json:"host"`
		Actor      string `json:"actor"`
		Action     string `json:"action"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "Invalid request payload"})
		return
	}
	if req.VaultBlock == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "Missing or invalid vaultBlock"})
		return
	}

	plaintext, err := DecryptVaultString(r.Context(), a.repoRoot, req.VaultBlock, DecryptOptions{Password: req.Password})
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": err.Error()})
		return
	}

	// Record audit event in vault_audit_log
	if a.store != nil {
		passSource := "inline"
		if req.Password == "" {
			passSource = ResolveVaultPasswordSource(a.repoRoot, ResolveOptions{}).Source
		}
		varName := strings.TrimSpace(req.VarName)
		if varName == "" {
			varName = ExtractVaultVariableName(req.VaultBlock)
			if varName == "" {
				varName = "unnamed"
			}
		}
		actor := strings.TrimSpace(req.Actor)
		if actor == "" {
			actor = a.defaultActor
		}
		host := strings.TrimSpace(req.Host)
		if host == "" {
			host = a.defaultHost
		}
		action := strings.TrimSpace(req.Action)
		if action == "" {
			action = "POST /api/vault/decrypt"
		}

		rnd := make([]byte, 8)
		_, _ = rand.Read(rnd)
		eventID := hex.EncodeToString(rnd)

		_ = a.store.RecordVaultAuditEvent(hostcache.VaultAuditEvent{
			ID:             eventID,
			Timestamp:      time.Now().UnixMilli(),
			VariableName:   varName,
			Actor:          actor,
			Host:           host,
			Action:         action,
			PasswordSource: passSource,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{"success": true, "plaintext": plaintext})
}

// generateEventID generates a random 16-character hex string for audit event IDs.
func generateEventID() string {
	rnd := make([]byte, 8)
	_, _ = rand.Read(rnd)
	return hex.EncodeToString(rnd)
}

// recordVaultAuditEvent records a vault audit event, ignoring errors to avoid blocking the main operation.
func (a *API) recordVaultAuditEvent(ev hostcache.VaultAuditEvent) {
	if a.store == nil {
		return
	}
	_ = a.store.RecordVaultAuditEvent(ev)
}
