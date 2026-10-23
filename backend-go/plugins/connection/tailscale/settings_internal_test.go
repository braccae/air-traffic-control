package tailscale

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"air-traffic-control/backend/pkg/plugin"
)

// TestSettingsAPIAppliesToPluginConfig exercises the full path a UI save takes:
// PUT /api/settings/pages/connection/tailscale -> SQLite -> the configuration the
// connection plugin actually builds its tsnet server from. It also asserts the
// auth key never appears in an HTTP response.
func TestSettingsAPIAppliesToPluginConfig(t *testing.T) {
	store := withSettingsStore(t)
	t.Setenv("TS_AUTHKEY", "")
	t.Setenv("TS_CONTROL_URL", "")

	reg := plugin.NewRegistry()
	if err := reg.Register(New()); err != nil {
		t.Fatalf("register tailscale plugin: %v", err)
	}
	api := plugin.NewAPI(plugin.APIOptions{Registry: reg, SettingsStore: store})
	mux := http.NewServeMux()
	api.RegisterRoutes(mux)

	const authKey = "tskey-auth-integration-key"
	body, err := json.Marshal(plugin.SettingsValuesBody{Values: map[string]any{
		FieldAuthKey:            authKey,
		FieldFallbackHostSocket: false,
		FieldHostname:           "atc-integration",
		FieldStateDir:           t.TempDir(),
		FieldControlURL:         "https://cp.integration.internal",
		FieldEphemeral:          false,
	}})
	if err != nil {
		t.Fatalf("encode body: %v", err)
	}

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/api/settings/pages/connection/tailscale", bytes.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT status = %d body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), authKey) {
		t.Fatalf("PUT response leaked the auth key: %s", rec.Body.String())
	}

	// The plugin resolves its live configuration from the persisted values, so
	// the saved settings are applied rather than merely stored.
	cfg := New().effectiveSettings()
	if cfg.AuthKey != authKey {
		t.Fatalf("plugin AuthKey = %q, want the persisted key", cfg.AuthKey)
	}
	if cfg.Hostname != "atc-integration" {
		t.Fatalf("plugin Hostname = %q", cfg.Hostname)
	}
	if cfg.ControlURL != "https://cp.integration.internal" {
		t.Fatalf("plugin ControlURL = %q", cfg.ControlURL)
	}
	if cfg.Ephemeral {
		t.Fatalf("plugin Ephemeral = true, want the persisted false")
	}

	// A read never exposes the key either.
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/settings/pages/connection/tailscale", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET status = %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), authKey) {
		t.Fatalf("GET response leaked the auth key: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), plugin.MaskedSecretValue(FieldAuthKey)) {
		t.Fatalf("GET response did not carry the mask: %s", rec.Body.String())
	}
}

// withSettingsStore installs a temporary settings store process-wide for the
// duration of a test and restores the previous one afterwards (the store is a
// process-wide singleton so resident plugins can read their own settings).
func withSettingsStore(t *testing.T) *plugin.SettingsStore {
	t.Helper()
	previous := plugin.GlobalSettingsStore()

	store, err := plugin.NewSettingsStore(filepath.Join(t.TempDir(), "settings.sqlite"))
	if err != nil {
		t.Fatalf("NewSettingsStore: %v", err)
	}
	plugin.SetGlobalSettingsStore(store)
	t.Cleanup(func() {
		plugin.SetGlobalSettingsStore(previous)
		_ = store.Close()
	})
	return store
}

func TestSettingsPageDeclaresMaskedAuthKey(t *testing.T) {
	page := New().SettingsPage()

	if page.ID != SettingsPageID {
		t.Fatalf("page ID = %q, want %q", page.ID, SettingsPageID)
	}
	if page.Title == "" || page.Description == "" {
		t.Fatalf("page is missing title/description: %+v", page)
	}

	byName := map[string]plugin.SettingsField{}
	for _, f := range page.Fields {
		byName[f.Name] = f
	}
	for _, want := range []string{FieldAuthKey, FieldFallbackHostSocket, FieldHostname, FieldStateDir, FieldControlURL, FieldEphemeral} {
		if _, ok := byName[want]; !ok {
			t.Fatalf("settings page has no %q field: %+v", want, page.Fields)
		}
	}

	key := byName[FieldAuthKey]
	if !plugin.IsSecretField(key) {
		t.Fatalf("authKey must be a secret field: %+v", key)
	}
	if key.Default != nil {
		t.Fatalf("authKey must not carry a plaintext default: %v", key.Default)
	}
	if byName[FieldHostname].Default != DefaultHostname {
		t.Fatalf("hostname default = %v, want %q", byName[FieldHostname].Default, DefaultHostname)
	}
	if byName[FieldEphemeral].Default != true {
		t.Fatalf("ephemeral default = %v, want true", byName[FieldEphemeral].Default)
	}
}

func TestEffectiveSettingsAppliesPersistedValues(t *testing.T) {
	store := withSettingsStore(t)
	t.Setenv("TS_AUTHKEY", "")
	t.Setenv("TS_CONTROL_URL", "")

	if err := store.Save(SettingsPageID, map[string]any{
		FieldAuthKey:            "tskey-auth-from-settings",
		FieldFallbackHostSocket: true,
		FieldHostname:           "atc-from-settings",
		FieldStateDir:           "/var/lib/atc/tsnet",
		FieldControlURL:         "https://headscale.example.internal",
		FieldEphemeral:          false,
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	cfg := New().effectiveSettings()
	if cfg.AuthKey != "tskey-auth-from-settings" {
		t.Fatalf("AuthKey = %q", cfg.AuthKey)
	}
	if !cfg.FallbackHostSocket {
		t.Fatalf("FallbackHostSocket = false, want persisted true")
	}
	if cfg.Hostname != "atc-from-settings" {
		t.Fatalf("Hostname = %q", cfg.Hostname)
	}
	if cfg.StateDir != "/var/lib/atc/tsnet" {
		t.Fatalf("StateDir = %q", cfg.StateDir)
	}
	if cfg.ControlURL != "https://headscale.example.internal" {
		t.Fatalf("ControlURL = %q", cfg.ControlURL)
	}
	if cfg.Ephemeral {
		t.Fatalf("Ephemeral = true, want persisted false")
	}
}

func TestEffectiveSettingsFallsBackToEnvAndDefaults(t *testing.T) {
	withSettingsStore(t)
	t.Setenv("TS_AUTHKEY", "tskey-auth-env")
	t.Setenv("TS_CONTROL_URL", "")

	cfg := New().effectiveSettings()
	if cfg.AuthKey != "tskey-auth-env" {
		t.Fatalf("AuthKey = %q, want env fallback", cfg.AuthKey)
	}
	if cfg.Hostname != DefaultHostname {
		t.Fatalf("Hostname = %q, want %q", cfg.Hostname, DefaultHostname)
	}
	if cfg.StateDir != DefaultStateDir {
		t.Fatalf("StateDir = %q, want %q", cfg.StateDir, DefaultStateDir)
	}
	if !cfg.Ephemeral {
		t.Fatalf("Ephemeral = false, want default true")
	}
}

func TestEffectiveSettingsIgnoresMaskedAuthKey(t *testing.T) {
	store := withSettingsStore(t)
	t.Setenv("TS_AUTHKEY", "")

	if err := store.Save(SettingsPageID, map[string]any{FieldAuthKey: plugin.MaskedSecretValue(FieldAuthKey)}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if cfg := New().effectiveSettings(); cfg.AuthKey != "" {
		t.Fatalf("AuthKey = %q, want empty (a mask must never be used as a key)", cfg.AuthKey)
	}
}

func TestEffectiveSettingsOptionOverridesPersisted(t *testing.T) {
	store := withSettingsStore(t)
	t.Setenv("TS_AUTHKEY", "")

	if err := store.Save(SettingsPageID, map[string]any{FieldAuthKey: "tskey-auth-settings"}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	cfg := New(WithAuthKey("tskey-auth-option")).effectiveSettings()
	if cfg.AuthKey != "tskey-auth-option" {
		t.Fatalf("AuthKey = %q, want the explicit option", cfg.AuthKey)
	}
}

func TestFingerprintTracksEveryAppliedSetting(t *testing.T) {
	base := tsnetSettings{Hostname: DefaultHostname, StateDir: DefaultStateDir, AuthKey: "k", Ephemeral: true}

	changed := base
	changed.AuthKey = "k2"
	if changed.fingerprint() == base.fingerprint() {
		t.Fatalf("fingerprint did not change when the auth key changed")
	}

	changed = base
	changed.Hostname = "other"
	if changed.fingerprint() == base.fingerprint() {
		t.Fatalf("fingerprint did not change when the hostname changed")
	}

	changed = base
	changed.ControlURL = "https://headscale.example"
	if changed.fingerprint() == base.fingerprint() {
		t.Fatalf("fingerprint did not change when the control URL changed")
	}

	changed = base
	changed.Ephemeral = false
	if changed.fingerprint() == base.fingerprint() {
		t.Fatalf("fingerprint did not change when ephemeral changed")
	}

	changed = base
	changed.FallbackHostSocket = true
	if changed.fingerprint() == base.fingerprint() {
		t.Fatalf("fingerprint did not change when the host-socket fallback changed")
	}

	if base.fingerprint() != base.fingerprint() {
		t.Fatalf("fingerprint is not stable")
	}
}

// TestEffectiveSettingsMigratesLegacyAuthKey proves a secret persisted under the
// pre-rename "authKey" field is still resolved when the new field is absent.
func TestEffectiveSettingsMigratesLegacyAuthKey(t *testing.T) {
	store := withSettingsStore(t)
	t.Setenv("TS_AUTHKEY", "")

	if err := store.Save(SettingsPageID, map[string]any{
		FieldAuthKeyLegacy: "tskey-auth-legacy",
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	cfg := New().effectiveSettings()
	if cfg.AuthKey != "tskey-auth-legacy" {
		t.Fatalf("AuthKey = %q, want legacy value", cfg.AuthKey)
	}
	if cfg.FallbackHostSocket {
		t.Fatalf("FallbackHostSocket = true, want false by default")
	}
}

// TestEnsureTsnetFailsLoudlyWithoutKeyOrFallback proves the daemon refuses to
// silently borrow the host socket when neither an auth key nor the explicit
// local-dev fallback is configured.
func TestEnsureTsnetFailsLoudlyWithoutKeyOrFallback(t *testing.T) {
	withSettingsStore(t)
	t.Setenv("TS_AUTHKEY", "")

	p := New(WithStateDir(t.TempDir()))
	_, err := p.ensureTsnet()
	if err == nil {
		t.Fatalf("ensureTsnet returned nil error without an auth key or fallback")
	}
	if !strings.Contains(err.Error(), "no auth key configured") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestResetTsnetStateRemovesStoredNodeState(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "tailscaled.state")
	if err := os.WriteFile(statePath, []byte("node-state"), 0o600); err != nil {
		t.Fatalf("seed state file: %v", err)
	}

	if err := resetTsnetState(dir); err != nil {
		t.Fatalf("resetTsnetState: %v", err)
	}
	if _, err := os.Stat(statePath); !os.IsNotExist(err) {
		t.Fatalf("state file still present after reset (err=%v)", err)
	}

	// Resetting a directory with no state must be a no-op rather than an error.
	if err := resetTsnetState(dir); err != nil {
		t.Fatalf("resetTsnetState on clean dir: %v", err)
	}
}
