package vaultapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"air-traffic-control/backend/pkg/hostcache"
)

func setupTestVaultAPI(t *testing.T) (*API, *hostcache.Store, string) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "hosts-cache.sqlite")
	store, err := hostcache.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open hostcache store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	api, err := NewAPI(Options{
		Store:        store,
		RepoRoot:     dir,
		DefaultActor: "test-actor",
		DefaultHost:  "test-host",
	})
	if err != nil {
		t.Fatalf("failed to create vault api: %v", err)
	}
	return api, store, dir
}

func TestVaultStatusAndAudit(t *testing.T) {
	api, store, _ := setupTestVaultAPI(t)
	mux := http.NewServeMux()
	api.Register(mux)

	// GET /api/vault/status
	req := httptest.NewRequest(http.MethodGet, "/api/vault/status", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 from status, got %d", rec.Code)
	}
	var statusRes map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &statusRes); err != nil {
		t.Fatalf("failed to unmarshal status response: %v", err)
	}
	if statusRes["success"] != true {
		t.Fatalf("expected success: true, got %v", statusRes)
	}

	// Add audit entry directly
	err := store.RecordVaultAuditEvent(hostcache.VaultAuditEvent{
		ID:             "audit-1",
		Timestamp:      1000,
		VariableName:   "secret_key",
		Actor:          "admin",
		Host:           "prod-host",
		Action:         "test",
		PasswordSource: "file",
	})
	if err != nil {
		t.Fatalf("failed to record audit event: %v", err)
	}

	// GET /api/vault/audit
	req = httptest.NewRequest(http.MethodGet, "/api/vault/audit?variable=secret_key", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 from audit, got %d", rec.Code)
	}
	var auditRes struct {
		Success bool                        `json:"success"`
		Events  []hostcache.VaultAuditEvent `json:"events"`
		Total   int                         `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &auditRes); err != nil {
		t.Fatalf("failed to unmarshal audit response: %v", err)
	}
	if !auditRes.Success || auditRes.Total != 1 || len(auditRes.Events) != 1 {
		t.Fatalf("unexpected audit response: %+v", auditRes)
	}
	if auditRes.Events[0].VariableName != "secret_key" {
		t.Fatalf("unexpected variable name: %s", auditRes.Events[0].VariableName)
	}
}

func TestVaultVariablesCRUD(t *testing.T) {
	api, store, _ := setupTestVaultAPI(t)
	mux := http.NewServeMux()
	api.Register(mux)

	// 1. Initially empty
	req := httptest.NewRequest(http.MethodGet, "/api/vault/variables", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	// 2. Pre-populate a variable into store
	dummyBlock := "my_token: !vault |\n  $ANSIBLE_VAULT;1.1;AES256\n  31323334"
	if err := store.UpsertVaultVariable("my_token", dummyBlock); err != nil {
		t.Fatalf("failed to insert vault variable: %v", err)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/vault/variables", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var listRes struct {
		Success   bool           `json:"success"`
		Variables []VariableView `json:"variables"`
		Total     int            `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listRes); err != nil {
		t.Fatalf("failed to parse list response: %v", err)
	}
	if listRes.Total != 1 || listRes.Variables[0].Name != "my_token" {
		t.Fatalf("unexpected variables: %+v", listRes)
	}
	if listRes.Variables[0].MaskedValue != "•••••••• (vault: my_token)" {
		t.Fatalf("unexpected masked value: %s", listRes.Variables[0].MaskedValue)
	}

	// 3. Rename variable via PUT /api/vault/variables/:name
	renameBody := `{"newName": "new_token"}`
	req = httptest.NewRequest(http.MethodPut, "/api/vault/variables/my_token", strings.NewReader(renameBody))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on rename, got %d: %s", rec.Code, rec.Body.String())
	}

	// Old should not exist, new should exist
	_, foundOld, _ := store.GetVaultVariable("my_token")
	if foundOld {
		t.Fatalf("old variable should have been deleted on rename")
	}
	newVar, foundNew, _ := store.GetVaultVariable("new_token")
	if !foundNew {
		t.Fatalf("new variable should exist after rename")
	}
	if !strings.HasPrefix(newVar.VaultBlock, "new_token: !vault |") {
		t.Fatalf("vault block was not re-framed under new name: %s", newVar.VaultBlock)
	}

	// 4. MaterializeVaultVarsFile
	mat, err := MaterializeVaultVarsFile(store)
	if err != nil {
		t.Fatalf("MaterializeVaultVarsFile failed: %v", err)
	}
	if mat == nil {
		t.Fatalf("expected non-nil MaterializedFile")
	}
	defer mat.Cleanup()

	content, err := os.ReadFile(mat.FilePath)
	if err != nil {
		t.Fatalf("failed to read materialized file: %v", err)
	}
	if !strings.Contains(string(content), "new_token: !vault |") {
		t.Fatalf("materialized file missing entry: %s", string(content))
	}

	// 5. Delete variable
	req = httptest.NewRequest(http.MethodDelete, "/api/vault/variables/new_token", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on delete, got %d: %s", rec.Code, rec.Body.String())
	}
	_, foundAfterDelete, _ := store.GetVaultVariable("new_token")
	if foundAfterDelete {
		t.Fatalf("variable should be deleted")
	}
}

func TestExtractRawVaultPayloadAndFormatting(t *testing.T) {
	block := `secret_var: !vault |
  $ANSIBLE_VAULT;1.1;AES256
  38323465373634356464653931613038663738343161623933393836373138356262333830303964
  3664366661333161336466383635393730313032333866340a346563393437346162653232323336
`
	raw, err := ExtractRawVaultPayload(block)
	if err != nil {
		t.Fatalf("unexpected error extracting payload: %v", err)
	}
	if !strings.HasPrefix(raw, "$ANSIBLE_VAULT;1.1;AES256") {
		t.Fatalf("unexpected raw header: %s", raw)
	}

	varName := ExtractVaultVariableName(block)
	if varName != "secret_var" {
		t.Fatalf("expected variable name 'secret_var', got '%s'", varName)
	}

	formatted := FormatVaultEntry("renamed_var", raw)
	if !strings.HasPrefix(formatted, "renamed_var: !vault |\n  $ANSIBLE_VAULT;1.1;AES256") {
		t.Fatalf("unexpected formatted block: %s", formatted)
	}
}

func TestVaultEncryptPersistsToStore(t *testing.T) {
	// Ensure ansible-vault can be found
	os.Setenv("ANSIBLE_VENV_PATH", "/var/home/core/Projects/air-traffic-control/data/ansible-venv")
	defer os.Unsetenv("ANSIBLE_VENV_PATH")

	api, store, dir := setupTestVaultAPI(t)
	api.repoRoot = "/var/home/core/Projects/air-traffic-control"
	mux := http.NewServeMux()
	api.Register(mux)

	// 1. Initially empty
	req := httptest.NewRequest(http.MethodGet, "/api/vault/variables", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	var listRes struct {
		Total int `json:"total"`
	}
	json.Unmarshal(rec.Body.Bytes(), &listRes)
	if listRes.Total != 0 {
		t.Fatalf("expected empty table, got %d", listRes.Total)
	}

	// 2. Encrypt an SSH key via /api/vault/encrypt
	sshKey := "-----BEGIN OPENSSH PRIVATE KEY-----\nfake-key-data\n-----END OPENSSH PRIVATE KEY-----"
	body := fmt.Sprintf(`{"plaintext": %q, "varName": "ssh_key_test", "password": "test-pass"}`, sshKey)
	req = httptest.NewRequest(http.MethodPost, "/api/vault/encrypt", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("encrypt failed: %d %s", rec.Code, rec.Body.String())
	}
	var encRes struct {
		Success    bool   `json:"success"`
		VaultBlock string `json:"vaultBlock"`
	}
	json.Unmarshal(rec.Body.Bytes(), &encRes)
	if !encRes.Success || !strings.Contains(encRes.VaultBlock, "$ANSIBLE_VAULT") {
		t.Fatalf("unexpected encrypt response: %+v", encRes)
	}

	// 3. Verify persistence to vault_variables table
	vv, found, err := store.GetVaultVariable("ssh_key_test")
	if err != nil || !found {
		t.Fatalf("vault variable not persisted: err=%v found=%v", err, found)
	}
	if !strings.Contains(vv.VaultBlock, "$ANSIBLE_VAULT") {
		t.Fatalf("stored block missing vault header")
	}

	// 4. Verify retrievable via list
	req = httptest.NewRequest(http.MethodGet, "/api/vault/variables", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	json.Unmarshal(rec.Body.Bytes(), &listRes)
	if listRes.Total != 1 {
		t.Fatalf("expected 1 variable, got %d", listRes.Total)
	}

	// 5. Verify round-trip decrypt
	plaintext, err := DecryptVaultString(context.Background(), "/var/home/core/Projects/air-traffic-control", vv.VaultBlock, DecryptOptions{Password: "test-pass"})
	if err != nil {
		t.Fatalf("decrypt failed: %v", err)
	}
	if plaintext != sshKey {
		t.Fatalf("decrypted plaintext mismatch")
	}

	// 6. Empty table regression: new store has zero rows
	dir2 := t.TempDir()
	store2, err := hostcache.Open(filepath.Join(dir2, "hosts-cache.sqlite"))
	if err != nil {
		t.Fatalf("failed to open second store: %v", err)
	}
	defer store2.Close()
	vars, err := store2.ListVaultVariables()
	if err != nil || len(vars) != 0 {
		t.Fatalf("expected empty store, got %d (err=%v)", len(vars), err)
	}

	_ = dir
}
