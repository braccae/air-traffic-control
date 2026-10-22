package settingsframework_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"air-traffic-control/backend/pkg/plugin"
	"air-traffic-control/backend/pkg/plugin/newapi"
	"air-traffic-control/backend/pkg/plugin/settingsframework"
)

type testCategorizedPlugin struct {
	id   string
	kind plugin.Kind
	name string
	page newapi.CategorizedSettingsPage
}

func (p *testCategorizedPlugin) Meta() plugin.Meta {
	return plugin.Meta{
		ID:    p.id,
		Kind:  p.kind,
		Name:  p.name,
		Label: p.name,
		Order: p.page.Order,
	}
}

func (p *testCategorizedPlugin) Compatible(plugin.HostFacts) bool { return true }
func (p *testCategorizedPlugin) Requirements() plugin.Requirements {
	return plugin.Requirements{}
}
func (p *testCategorizedPlugin) Probe(context.Context, plugin.Runner, plugin.HostFacts) (plugin.Detection, error) {
	return plugin.Detection{Detected: true, Status: "ONLINE"}, nil
}
func (p *testCategorizedPlugin) SettingsPage() plugin.SettingsPage {
	return p.page.SettingsPage
}
func (p *testCategorizedPlugin) CategorizedSettingsPage() newapi.CategorizedSettingsPage {
	return p.page
}

func buildResticSettingsPage() newapi.CategorizedSettingsPage {
	return settingsframework.NewPageBuilder("backup/restic", "Restic Backup Defaults").
		WithIcon("cloud_sync").
		WithDescription("Datacenter-wide repository and retention defaults").
		WithOrder(20).
		WithCategory("backup").
		WithScope(newapi.SettingsScopeDatacenter).
		StringField(
			"repoPath",
			"Default Repository",
			"/var/backups/restic",
			settingsframework.WithRequired(true),
			settingsframework.WithPlaceholder("/var/backups/restic or s3:https://..."),
			settingsframework.WithDescription("Restic repository path"),
		).
		IntField(
			"retentionDaily",
			"Daily Snapshots Kept",
			7,
			settingsframework.WithMinMax(0, 365),
		).
		BoolField(
			"pruneAfterForget",
			"Prune After Forget",
			true,
		).
		SelectField(
			"compression",
			"Compression Level",
			"auto",
			[]plugin.SettingsFieldOption{
				{Value: "auto", Label: "Auto"},
				{Value: "off", Label: "Off"},
				{Value: "max", Label: "Max"},
			},
		).
		TextField(
			"excludePatterns",
			"Exclude Patterns",
			"*.tmp\n*.cache",
		).
		SecretField(
			"passwordVaultVar",
			"Repository Password Vault Variable",
			settingsframework.WithPlaceholder("vault_restic_password"),
		).
		BuildCategorized()
}

func TestPageBuilder_AndSchemaValidation(t *testing.T) {
	catPage := buildResticSettingsPage()

	if catPage.ID != "backup/restic" {
		t.Fatalf("expected ID backup/restic, got %q", catPage.ID)
	}
	if catPage.Scope != newapi.SettingsScopeDatacenter {
		t.Fatalf("expected datacenter scope, got %q", catPage.Scope)
	}
	if catPage.Category != "backup" {
		t.Fatalf("expected backup category, got %q", catPage.Category)
	}
	if len(catPage.Fields) != 6 {
		t.Fatalf("expected 6 fields, got %d", len(catPage.Fields))
	}

	if err := settingsframework.ValidateSchema(catPage.SettingsPage); err != nil {
		t.Fatalf("ValidateSchema failed: %v", err)
	}
	if err := newapi.ValidateCategorizedSettingsPage(catPage); err != nil {
		t.Fatalf("ValidateCategorizedSettingsPage failed: %v", err)
	}

	// Verify defensive copy on BuildCategorized
	catPage.Fields[0].Name = "mutated"
	rebuilt := settingsframework.NewPageBuilder("storage/zfs", "ZFS").
		StringField("pool", "Pool", "tank").
		Build()
	if rebuilt.Fields[0].Name != "pool" {
		t.Fatalf("unexpected field name %q", rebuilt.Fields[0].Name)
	}

	// Duplicate field name should fail ValidateSchema
	badPage := settingsframework.NewPageBuilder("demo/bad", "Bad").
		StringField("dup", "One", "a").
		StringField("dup", "Two", "b").
		Build()
	if err := settingsframework.ValidateSchema(badPage); err == nil {
		t.Fatal("expected ValidateSchema to reject duplicate field names")
	}

	// Invalid min > max should fail ValidateSchema
	badRange := settingsframework.NewPageBuilder("demo/range", "Bad Range").
		IntField("count", "Count", 5, settingsframework.WithMinMax(20, 10)).
		Build()
	if err := settingsframework.ValidateSchema(badRange); err == nil {
		t.Fatal("expected ValidateSchema to reject min > max")
	}
}

func TestCoerceValues_AndSecretProtectionInvariant(t *testing.T) {
	page := buildResticSettingsPage().SettingsPage

	// Valid coercion (string int conversion, bool conversion, select validation, secret trimming)
	coerced, err := settingsframework.CoerceValues(page, map[string]any{
		"repoPath":         "/mnt/backup",
		"retentionDaily":   "14",
		"pruneAfterForget": "false",
		"compression":      "max",
		"passwordVaultVar": "  super-secret-token  ",
	})
	if err != nil {
		t.Fatalf("CoerceValues failed: %v", err)
	}
	if coerced["retentionDaily"] != 14 {
		t.Errorf("expected int 14, got %#v", coerced["retentionDaily"])
	}
	if coerced["pruneAfterForget"] != false {
		t.Errorf("expected bool false, got %#v", coerced["pruneAfterForget"])
	}
	if coerced["passwordVaultVar"] != "super-secret-token" {
		t.Errorf("expected trimmed secret, got %#v", coerced["passwordVaultVar"])
	}

	// Masked secret value must be ignored so existing stored secret is never overwritten
	coercedMask, err := settingsframework.CoerceValues(page, map[string]any{
		"repoPath":         "/mnt/backup",
		"passwordVaultVar": plugin.MaskedSecretValue("passwordVaultVar"),
	})
	if err != nil {
		t.Fatalf("CoerceValues with mask failed: %v", err)
	}
	if _, exists := coercedMask["passwordVaultVar"]; exists {
		t.Fatalf("masked secret must not be included in coerced output: %+v", coercedMask)
	}

	// Out-of-range integer must fail
	if _, err := settingsframework.CoerceValues(page, map[string]any{
		"repoPath":       "/mnt/backup",
		"retentionDaily": 999,
	}); err == nil {
		t.Fatal("expected error for retentionDaily > 365")
	}

	// Missing required field must fail
	if _, err := settingsframework.CoerceValues(page, map[string]any{
		"repoPath": "",
	}); err == nil {
		t.Fatal("expected error for empty required field repoPath")
	}

	// Unknown field must fail
	if _, err := settingsframework.CoerceValues(page, map[string]any{
		"repoPath": "/mnt/backup",
		"bogus":    "value",
	}); err == nil {
		t.Fatal("expected error for unknown field")
	}
}

func TestStoreAdapter_SQLiteAndScopedPersistence(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "settings.sqlite")
	adapter, rawStore, err := settingsframework.NewSQLiteStoreAdapter(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStoreAdapter: %v", err)
	}
	defer adapter.Close()

	page := buildResticSettingsPage().SettingsPage

	// Save datacenter-wide settings
	if _, err := adapter.ValidateAndSave(page, map[string]any{
		"repoPath":         "s3:https://backup.local/restic",
		"retentionDaily":   30,
		"pruneAfterForget": false,
		"passwordVaultVar": "tskey-secret-value",
	}); err != nil {
		t.Fatalf("ValidateAndSave: %v", err)
	}

	// Verify typed getters
	if got := adapter.StringOrDefault(page.ID, "repoPath", "/default"); got != "s3:https://backup.local/restic" {
		t.Errorf("StringOrDefault = %q", got)
	}
	if got := adapter.IntOrDefault(page.ID, "retentionDaily", 7); got != 30 {
		t.Errorf("IntOrDefault = %d, want 30", got)
	}
	if got := adapter.BoolOrDefault(page.ID, "pruneAfterForget", true); got != false {
		t.Errorf("BoolOrDefault = %v, want false", got)
	}
	if sec, ok := adapter.SecretValue(page.ID, "passwordVaultVar"); !ok || sec != "tskey-secret-value" {
		t.Errorf("SecretValue = %q, %v", sec, ok)
	}

	// Verify RenderPageView masks secrets while EffectiveValues preserves them for backend callers
	view, err := settingsframework.RenderPageView(page, rawStore)
	if err != nil {
		t.Fatalf("RenderPageView: %v", err)
	}
	if got, want := view.Values["passwordVaultVar"], plugin.MaskedSecretValue("passwordVaultVar"); got != want {
		t.Errorf("RenderPageView secret = %v, want %q", got, want)
	}

	eff, err := adapter.EffectiveValues(page)
	if err != nil {
		t.Fatalf("EffectiveValues: %v", err)
	}
	if got := eff["passwordVaultVar"]; got != "tskey-secret-value" {
		t.Errorf("EffectiveValues secret = %v, want unmasked value", got)
	}

	// Verify node-scoped persistence does not collide with datacenter-scoped persistence
	if _, err := adapter.ValidateAndSaveScoped(newapi.SettingsScopeNode, "node-alpha", page, map[string]any{
		"repoPath":       "/srv/node-alpha/restic",
		"retentionDaily": 3,
	}); err != nil {
		t.Fatalf("ValidateAndSaveScoped: %v", err)
	}

	nodeView, err := settingsframework.RenderCategorizedView(newapi.CategorizedSettingsPage{
		SettingsPage: page,
		Scope:        newapi.SettingsScopeNode,
		Category:     "backup",
	}, rawStore, "node-alpha")
	if err != nil {
		t.Fatalf("RenderCategorizedView(node): %v", err)
	}
	if got := nodeView.Values["repoPath"]; got != "/srv/node-alpha/restic" {
		t.Errorf("node scoped repoPath = %v, want /srv/node-alpha/restic", got)
	}
	if got := adapter.StringOrDefault(page.ID, "repoPath", ""); got != "s3:https://backup.local/restic" {
		t.Errorf("datacenter repoPath was overwritten by node-scoped save: %q", got)
	}
}

func TestHandler_CategoriesAndDatacenterRoutes(t *testing.T) {
	reg := plugin.NewRegistry()
	resticPage := buildResticSettingsPage()
	tailscalePage := settingsframework.NewPageBuilder("connection/tailscale", "ATC Tailscale").
		WithCategory("connection").
		WithOrder(10).
		StringField("hostname", "Hostname", "atc-tsnet", settingsframework.WithRequired(true)).
		SecretField("authKey", "Auth Key").
		BuildCategorized()

	if err := reg.Register(&testCategorizedPlugin{
		id:   "backup/restic",
		kind: plugin.KindBackup,
		name: "restic",
		page: resticPage,
	}); err != nil {
		t.Fatalf("register restic: %v", err)
	}
	if err := reg.Register(&testCategorizedPlugin{
		id:   "connection/tailscale",
		kind: plugin.KindConnection,
		name: "tailscale",
		page: tailscalePage,
	}); err != nil {
		t.Fatalf("register tailscale: %v", err)
	}

	memStore := settingsframework.NewMemoryStore()
	handler := settingsframework.NewHandler(reg, memStore)
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	// 1. GET /api/settings/categories
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/settings/categories?scope=datacenter", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/settings/categories status=%d body=%s", rec.Code, rec.Body.String())
	}
	var groups []settingsframework.CategoryGroup
	if err := json.Unmarshal(rec.Body.Bytes(), &groups); err != nil {
		t.Fatalf("decode groups: %v", err)
	}
	if len(groups) != 2 || groups[0].Category != "backup" || groups[1].Category != "connection" {
		t.Fatalf("unexpected category groups: %+v", groups)
	}

	// 2. PUT /api/settings/datacenter/connection/tailscale
	putBody, _ := json.Marshal(map[string]any{
		"values": map[string]any{
			"hostname": "custom-mesh-node",
			"authKey":  "tskey-auth-secret-999",
		},
	})
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/api/settings/datacenter/connection/tailscale", bytes.NewReader(putBody)))
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT status=%d body=%s", rec.Code, rec.Body.String())
	}
	if bytes.Contains(rec.Body.Bytes(), []byte("tskey-auth-secret-999")) {
		t.Fatalf("PUT response leaked secret in plaintext: %s", rec.Body.String())
	}

	var updatedView settingsframework.CategorizedPageView
	if err := json.Unmarshal(rec.Body.Bytes(), &updatedView); err != nil {
		t.Fatalf("decode updatedView: %v", err)
	}
	if updatedView.Values["hostname"] != "custom-mesh-node" {
		t.Errorf("hostname = %v, want custom-mesh-node", updatedView.Values["hostname"])
	}
	if updatedView.Values["authKey"] != plugin.MaskedSecretValue("authKey") {
		t.Errorf("authKey = %v, want masked value", updatedView.Values["authKey"])
	}

	// 3. Re-PUT with masked authKey preserves secret
	putBody2, _ := json.Marshal(map[string]any{
		"values": map[string]any{
			"hostname": "second-hostname",
			"authKey":  plugin.MaskedSecretValue("authKey"),
		},
	})
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/api/settings/datacenter/connection/tailscale", bytes.NewReader(putBody2)))
	if rec.Code != http.StatusOK {
		t.Fatalf("second PUT status=%d body=%s", rec.Code, rec.Body.String())
	}
	if rawSecret, ok := memStore.Value("connection/tailscale", "authKey"); !ok || rawSecret != "tskey-auth-secret-999" {
		t.Fatalf("stored secret was overwritten or lost: %q (ok=%v)", rawSecret, ok)
	}
}
