package plugin_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"air-traffic-control/backend/pkg/plugin"
	_ "air-traffic-control/backend/plugins/all"
)

// fakeSettingsPlugin is a minimal addon module that contributes a global
// settings page, exercising the SettingsPageProvider path.
type fakeSettingsPlugin struct {
	fakePlugin
	page plugin.SettingsPage
}

func (p *fakeSettingsPlugin) SettingsPage() plugin.SettingsPage { return p.page }

func demoPage() plugin.SettingsPage {
	return plugin.SettingsPage{
		ID:          "demo/alpha",
		Title:       "Demo Alpha",
		Icon:        "tune",
		Description: "demo page",
		Order:       10,
		Fields: []plugin.SettingsField{
			{Name: "mode", Label: "Mode", Type: plugin.SettingsFieldString, Default: "auto"},
			{Name: "retries", Label: "Retries", Type: plugin.SettingsFieldInt, Default: 3},
			{Name: "enabled", Label: "Enabled", Type: plugin.SettingsFieldBool, Default: true},
			{Name: "strategy", Label: "Strategy", Type: plugin.SettingsFieldSelect, Options: []plugin.SettingsFieldOption{
				{Value: "fast", Label: "Fast"},
				{Value: "safe", Label: "Safe"},
			}},
			{Name: "token", Label: "Token", Type: plugin.SettingsFieldSecret, Secret: true},
		},
	}
}

func TestRegistry_RegistersSettingsPage(t *testing.T) {
	reg := plugin.NewRegistry()
	p := &fakeSettingsPlugin{
		fakePlugin: fakePlugin{meta: plugin.Meta{ID: "demo/alpha", Kind: "demo", Name: "alpha"}},
		page:       demoPage(),
	}
	if err := reg.Register(p); err != nil {
		t.Fatalf("register failed: %v", err)
	}

	if _, ok := reg.GetSettingsPage("demo/alpha"); !ok {
		t.Fatalf("settings page demo/alpha not found after register")
	}

	pages := reg.ListSettingsPages()
	if len(pages) != 1 || pages[0].ID != "demo/alpha" {
		t.Fatalf("unexpected settings pages: %+v", pages)
	}
}

func TestRegistry_RejectsDuplicateAndEmptySettingsPage(t *testing.T) {
	reg := plugin.NewRegistry()

	empty := &fakeSettingsPlugin{
		fakePlugin: fakePlugin{meta: plugin.Meta{ID: "demo/empty", Kind: "demo", Name: "empty"}},
		page:       plugin.SettingsPage{ID: ""},
	}
	if err := reg.Register(empty); err == nil {
		t.Fatalf("expected empty settings page ID to be rejected")
	}

	first := &fakeSettingsPlugin{
		fakePlugin: fakePlugin{meta: plugin.Meta{ID: "demo/one", Kind: "demo", Name: "one"}},
		page:       plugin.SettingsPage{ID: "demo/shared"},
	}
	if err := reg.Register(first); err != nil {
		t.Fatalf("first register failed: %v", err)
	}

	second := &fakeSettingsPlugin{
		fakePlugin: fakePlugin{meta: plugin.Meta{ID: "demo/two", Kind: "demo", Name: "two"}},
		page:       plugin.SettingsPage{ID: "demo/shared"},
	}
	if err := reg.Register(second); err == nil {
		t.Fatalf("expected duplicate settings page ID to be rejected")
	}
}

func TestRegistry_SortsSettingsPagesByOrder(t *testing.T) {
	reg := plugin.NewRegistry()
	high := &fakeSettingsPlugin{
		fakePlugin: fakePlugin{meta: plugin.Meta{ID: "demo/high", Kind: "demo", Name: "high"}},
		page:       plugin.SettingsPage{ID: "demo/high", Order: 50},
	}
	low := &fakeSettingsPlugin{
		fakePlugin: fakePlugin{meta: plugin.Meta{ID: "demo/low", Kind: "demo", Name: "low"}},
		page:       plugin.SettingsPage{ID: "demo/low", Order: 5},
	}
	if err := reg.Register(high); err != nil {
		t.Fatal(err)
	}
	if err := reg.Register(low); err != nil {
		t.Fatal(err)
	}

	pages := reg.ListSettingsPages()
	if len(pages) != 2 || pages[0].ID != "demo/low" || pages[1].ID != "demo/high" {
		t.Fatalf("settings pages not ordered by Order: %+v", pages)
	}
}

func TestRealModuleContributesSettingsPage(t *testing.T) {
	// The restic backup module is a real addon module that uses the framework.
	pages := plugin.ListSettingsPages()
	found := false
	for _, p := range pages {
		if p.ID == "backup/restic" {
			found = true
			if len(p.Fields) == 0 {
				t.Fatalf("restic settings page declared no fields")
			}
		}
	}
	if !found {
		t.Fatalf("expected backup/restic to contribute a global settings page; got %+v", pages)
	}
}

func TestSettingsStore_RoundTrip(t *testing.T) {
	store, err := plugin.NewSettingsStore(filepath.Join(t.TempDir(), "settings.sqlite"))
	if err != nil {
		t.Fatalf("NewSettingsStore: %v", err)
	}
	defer store.Close()

	if err := store.Save("demo/alpha", map[string]any{"mode": "manual", "retries": 9, "enabled": false}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := store.Load("demo/alpha")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded["mode"] != "manual" {
		t.Fatalf("mode = %v, want manual", loaded["mode"])
	}
	if loaded["retries"] != float64(9) {
		t.Fatalf("retries = %v, want 9", loaded["retries"])
	}
	if loaded["enabled"] != false {
		t.Fatalf("enabled = %v, want false", loaded["enabled"])
	}

	if v, ok := store.Value("demo/alpha", "mode"); !ok || v != "manual" {
		t.Fatalf("Value(mode) = %q, %v", v, ok)
	}
}

func newSettingsAPI(t *testing.T) (*plugin.API, *plugin.SettingsStore) {
	t.Helper()
	reg := plugin.NewRegistry()
	p := &fakeSettingsPlugin{
		fakePlugin: fakePlugin{meta: plugin.Meta{ID: "demo/alpha", Kind: "demo", Name: "alpha"}},
		page:       demoPage(),
	}
	if err := reg.Register(p); err != nil {
		t.Fatalf("register: %v", err)
	}

	store, err := plugin.NewSettingsStore(filepath.Join(t.TempDir(), "settings.sqlite"))
	if err != nil {
		t.Fatalf("NewSettingsStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	api := plugin.NewAPI(plugin.APIOptions{
		Registry:       reg,
		NodeResolver:   newMemoryNodeResolver(),
		RunnerResolver: &staticRunnerResolver{},
		SettingsStore:  store,
	})
	return api, store
}

func TestAPI_ListSettingsPagesWithDefaults(t *testing.T) {
	api, _ := newSettingsAPI(t)
	mux := http.NewServeMux()
	api.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/settings/pages", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}

	var views []plugin.GlobalSettingsPageView
	if err := json.Unmarshal(rec.Body.Bytes(), &views); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(views) != 1 || views[0].ID != "demo/alpha" {
		t.Fatalf("unexpected views: %+v", views)
	}
	if views[0].Values["mode"] != "auto" {
		t.Fatalf("expected default mode=auto, got %v", views[0].Values["mode"])
	}
}

func TestAPI_PutSettingsPersists(t *testing.T) {
	api, store := newSettingsAPI(t)
	mux := http.NewServeMux()
	api.RegisterRoutes(mux)

	body, _ := json.Marshal(plugin.SettingsValuesBody{Values: map[string]any{
		"mode":     "manual",
		"retries":  12,
		"strategy": "fast",
	}})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/settings/pages/demo/alpha", bytes.NewReader(body))
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}

	loaded, err := store.Load("demo/alpha")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded["mode"] != "manual" {
		t.Fatalf("persisted mode = %v, want manual", loaded["mode"])
	}
	if loaded["retries"] != float64(12) {
		t.Fatalf("persisted retries = %v, want 12", loaded["retries"])
	}
}

func TestAPI_PutSettingsRejectsUnknownField(t *testing.T) {
	api, _ := newSettingsAPI(t)
	mux := http.NewServeMux()
	api.RegisterRoutes(mux)

	body, _ := json.Marshal(plugin.SettingsValuesBody{Values: map[string]any{"nope": "x"}})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/settings/pages/demo/alpha", bytes.NewReader(body))
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestAPI_PutSettingsRejectsBadSelectValue(t *testing.T) {
	api, _ := newSettingsAPI(t)
	mux := http.NewServeMux()
	api.RegisterRoutes(mux)

	body, _ := json.Marshal(plugin.SettingsValuesBody{Values: map[string]any{"strategy": "wild"}})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/settings/pages/demo/alpha", bytes.NewReader(body))
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestAPI_GetSettingsPageUnknown404(t *testing.T) {
	api, _ := newSettingsAPI(t)
	mux := http.NewServeMux()
	api.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/settings/pages/demo/missing", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

// TestAPI_MasksStoredSecrets proves the Secret Protection Invariant on reads: a
// persisted secret is never emitted in plaintext, only as its mask.
func TestAPI_MasksStoredSecrets(t *testing.T) {
	api, store := newSettingsAPI(t)
	mux := http.NewServeMux()
	api.RegisterRoutes(mux)

	const secret = "tskey-auth-supersecret-value"
	if err := store.Save("demo/alpha", map[string]any{"token": secret}); err != nil {
		t.Fatalf("seed secret: %v", err)
	}

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/settings/pages/demo/alpha", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if bytes.Contains(rec.Body.Bytes(), []byte(secret)) {
		t.Fatalf("response leaked the secret in plaintext: %s", rec.Body.String())
	}

	var view plugin.GlobalSettingsPageView
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got, want := view.Values["token"], plugin.MaskedSecretValue("token"); got != want {
		t.Fatalf("token = %v, want mask %q", got, want)
	}

	// The list endpoint must mask too.
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/settings/pages", nil))
	if bytes.Contains(rec.Body.Bytes(), []byte(secret)) {
		t.Fatalf("list response leaked the secret in plaintext: %s", rec.Body.String())
	}
}

// TestAPI_PutMaskPreservesStoredSecret proves that echoing the mask back (which
// is what the UI does when the secret field is untouched) never overwrites the
// stored secret with the mask itself.
func TestAPI_PutMaskPreservesStoredSecret(t *testing.T) {
	api, store := newSettingsAPI(t)
	mux := http.NewServeMux()
	api.RegisterRoutes(mux)

	const secret = "tskey-auth-original"
	if err := store.Save("demo/alpha", map[string]any{"token": secret, "mode": "auto"}); err != nil {
		t.Fatalf("seed secret: %v", err)
	}

	body, _ := json.Marshal(plugin.SettingsValuesBody{Values: map[string]any{
		"token": plugin.MaskedSecretValue("token"),
		"mode":  "manual",
	}})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/api/settings/pages/demo/alpha", bytes.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}

	stored, ok := store.Value("demo/alpha", "token")
	if !ok || stored != secret {
		t.Fatalf("stored token = %q (ok=%v), want original secret preserved", stored, ok)
	}
	if mode, _ := store.Value("demo/alpha", "mode"); mode != "manual" {
		t.Fatalf("mode = %q, want manual", mode)
	}

	// Empty string must also mean "leave it alone".
	body, _ = json.Marshal(plugin.SettingsValuesBody{Values: map[string]any{"token": ""}})
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/api/settings/pages/demo/alpha", bytes.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if stored, _ := store.Value("demo/alpha", "token"); stored != secret {
		t.Fatalf("empty PUT replaced the secret with %q", stored)
	}
}

// TestAPI_PutNewSecretPersists proves a genuinely new secret is stored and still
// never reflected back in plaintext.
func TestAPI_PutNewSecretPersists(t *testing.T) {
	api, store := newSettingsAPI(t)
	mux := http.NewServeMux()
	api.RegisterRoutes(mux)

	const replacement = "tskey-auth-replacement"
	body, _ := json.Marshal(plugin.SettingsValuesBody{Values: map[string]any{"token": "  " + replacement + "  "}})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/api/settings/pages/demo/alpha", bytes.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}

	if stored, ok := store.Value("demo/alpha", "token"); !ok || stored != replacement {
		t.Fatalf("stored token = %q (ok=%v), want trimmed replacement", stored, ok)
	}
	if bytes.Contains(rec.Body.Bytes(), []byte(replacement)) {
		t.Fatalf("PUT response leaked the new secret: %s", rec.Body.String())
	}
}

// TestRealTailscaleModuleContributesSettingsPage proves ATC's own Tailscale
// settings page is registered and its auth key is a secret field.
func TestRealTailscaleModuleContributesSettingsPage(t *testing.T) {
	var page plugin.SettingsPage
	found := false
	for _, p := range plugin.ListSettingsPages() {
		if p.ID == "connection/tailscale" {
			page = p
			found = true
		}
	}
	if !found {
		t.Fatalf("expected connection/tailscale to contribute a global settings page")
	}
	if page.Title == "" || len(page.Fields) == 0 {
		t.Fatalf("tailscale settings page is empty: %+v", page)
	}

	authKey := false
	for _, f := range page.Fields {
		if f.Name == "authKey" {
			authKey = true
			if !plugin.IsSecretField(f) {
				t.Fatalf("authKey field is not secret: %+v", f)
			}
		}
	}
	if !authKey {
		t.Fatalf("tailscale settings page has no authKey field: %+v", page.Fields)
	}
}
