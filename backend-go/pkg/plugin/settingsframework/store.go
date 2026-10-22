package settingsframework

import (
	"fmt"
	"strconv"
	"strings"
	"sync"

	"air-traffic-control/backend/pkg/plugin"
	"air-traffic-control/backend/pkg/plugin/newapi"
)

// Store abstracts persistence for datacenter-wide and node-scoped settings
// pages. It wraps and extends plugin.SettingsStore while also supporting
// in-memory stores for fast unit tests.
type Store interface {
	Load(pageID string) (map[string]any, error)
	Save(pageID string, values map[string]any) error
	Value(pageID, field string) (string, bool)
	Close() error
}

// ScopedKey builds the storage namespace key for a given scope, target (e.g.
// node ID when scope == "node"), and page ID. For datacenter-wide scope, the
// pageID itself is returned unchanged so existing global_settings rows remain
// 100% compatible.
func ScopedKey(scope newapi.SettingsScope, targetID, pageID string) string {
	trimmedPage := strings.TrimSpace(pageID)
	trimmedTarget := strings.TrimSpace(targetID)
	if scope == newapi.SettingsScopeNode && trimmedTarget != "" {
		return fmt.Sprintf("node:%s:%s", trimmedTarget, trimmedPage)
	}
	return trimmedPage
}

// StoreAdapter wraps a Store (such as *plugin.SettingsStore or *MemoryStore)
// with typed value accessors, schema-default fallback, and scoped persistence.
type StoreAdapter struct {
	store Store
}

// NewStoreAdapter wraps an existing Store. If store is nil, it falls back to
// plugin.GlobalSettingsStore() dynamically on each operation.
func NewStoreAdapter(store Store) *StoreAdapter {
	return &StoreAdapter{store: store}
}

// NewSQLiteStoreAdapter opens a *plugin.SettingsStore at dbPath and wraps it in
// a StoreAdapter.
func NewSQLiteStoreAdapter(dbPath string) (*StoreAdapter, *plugin.SettingsStore, error) {
	st, err := plugin.NewSettingsStore(dbPath)
	if err != nil {
		return nil, nil, err
	}
	return &StoreAdapter{store: st}, st, nil
}

func (a *StoreAdapter) resolveStore() Store {
	if a != nil && a.store != nil {
		return a.store
	}
	if global := plugin.GlobalSettingsStore(); global != nil {
		return global
	}
	return nil
}

// Underlying returns the active backing Store (or nil if none is configured).
func (a *StoreAdapter) Underlying() Store {
	return a.resolveStore()
}

// Close closes the underlying store if one is explicitly attached.
func (a *StoreAdapter) Close() error {
	if a == nil || a.store == nil {
		return nil
	}
	return a.store.Close()
}

// Load retrieves persisted values for a datacenter-wide page.
func (a *StoreAdapter) Load(pageID string) (map[string]any, error) {
	st := a.resolveStore()
	if st == nil {
		return map[string]any{}, nil
	}
	return st.Load(pageID)
}

// LoadScoped retrieves persisted values for a scoped page (datacenter or node).
func (a *StoreAdapter) LoadScoped(scope newapi.SettingsScope, targetID, pageID string) (map[string]any, error) {
	return a.Load(ScopedKey(scope, targetID, pageID))
}

// Save persists raw values for a datacenter-wide page.
func (a *StoreAdapter) Save(pageID string, values map[string]any) error {
	st := a.resolveStore()
	if st == nil {
		return fmt.Errorf("settings store unavailable")
	}
	return st.Save(pageID, values)
}

// SaveScoped persists raw values for a scoped page (datacenter or node).
func (a *StoreAdapter) SaveScoped(scope newapi.SettingsScope, targetID, pageID string, values map[string]any) error {
	return a.Save(ScopedKey(scope, targetID, pageID), values)
}

// ValidateAndSave validates and coerces incoming values against the page schema
// (respecting the Secret Protection Invariant) before persisting them.
func (a *StoreAdapter) ValidateAndSave(page plugin.SettingsPage, input map[string]any) (map[string]any, error) {
	coerced, err := CoerceValues(page, input)
	if err != nil {
		return nil, err
	}
	if err := a.Save(page.ID, coerced); err != nil {
		return nil, err
	}
	return coerced, nil
}

// ValidateAndSaveScoped validates and coerces incoming values against the page
// schema before persisting them under the scoped key.
func (a *StoreAdapter) ValidateAndSaveScoped(scope newapi.SettingsScope, targetID string, page plugin.SettingsPage, input map[string]any) (map[string]any, error) {
	coerced, err := CoerceValues(page, input)
	if err != nil {
		return nil, err
	}
	if err := a.SaveScoped(scope, targetID, page.ID, coerced); err != nil {
		return nil, err
	}
	return coerced, nil
}

// Value returns a single persisted setting as a string, or ("", false) when
// unset or when the store is unavailable.
func (a *StoreAdapter) Value(pageID, field string) (string, bool) {
	st := a.resolveStore()
	if st == nil {
		return "", false
	}
	return st.Value(pageID, field)
}

// StringOrDefault returns a trimmed string setting or defaultVal when unset.
func (a *StoreAdapter) StringOrDefault(pageID, field, defaultVal string) string {
	if v, ok := a.Value(pageID, field); ok {
		trimmed := strings.TrimSpace(v)
		if trimmed != "" {
			return trimmed
		}
	}
	return defaultVal
}

// IntOrDefault returns an integer setting or defaultVal when unset or invalid.
func (a *StoreAdapter) IntOrDefault(pageID, field string, defaultVal int) int {
	if v, ok := a.Value(pageID, field); ok {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return n
		}
	}
	return defaultVal
}

// BoolOrDefault returns a boolean setting or defaultVal when unset or invalid.
func (a *StoreAdapter) BoolOrDefault(pageID, field string, defaultVal bool) bool {
	if v, ok := a.Value(pageID, field); ok {
		if b, err := strconv.ParseBool(strings.TrimSpace(v)); err == nil {
			return b
		}
	}
	return defaultVal
}

// SecretValue returns a stored secret's real plaintext value for internal
// module use (such as dialing tsnet or unlocking restic), returning ("", false)
// if unset or if the stored value is a mask placeholder.
func (a *StoreAdapter) SecretValue(pageID, field string) (string, bool) {
	v, ok := a.Value(pageID, field)
	if !ok {
		return "", false
	}
	trimmed := strings.TrimSpace(v)
	if trimmed == "" || plugin.IsMaskedSecretValue(trimmed) {
		return "", false
	}
	return trimmed, true
}

// EffectiveValues loads stored values for a page and merges them with the page's
// schema defaults (including unmasked secrets for internal backend callers).
// For API responses sent to clients, use RenderPageView or RenderCategorizedView
// instead so secrets are masked.
func (a *StoreAdapter) EffectiveValues(page plugin.SettingsPage) (map[string]any, error) {
	stored, err := a.Load(page.ID)
	if err != nil {
		return nil, err
	}
	return MergeDefaults(page, stored, false), nil
}

// MemoryStore is a thread-safe in-memory implementation of Store for unit tests.
type MemoryStore struct {
	mu    sync.RWMutex
	pages map[string]map[string]any
}

// NewMemoryStore creates an empty in-memory settings store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		pages: make(map[string]map[string]any),
	}
}

// Load returns a copy of the stored values for pageID.
func (m *MemoryStore) Load(pageID string) (map[string]any, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make(map[string]any)
	if existing, ok := m.pages[pageID]; ok {
		for k, v := range existing {
			out[k] = v
		}
	}
	return out, nil
}

// Save upserts values for pageID.
func (m *MemoryStore) Save(pageID string, values map[string]any) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	pageMap, ok := m.pages[pageID]
	if !ok {
		pageMap = make(map[string]any)
		m.pages[pageID] = pageMap
	}
	for k, v := range values {
		pageMap[k] = v
	}
	return nil
}

// Value returns a single stored field as a string.
func (m *MemoryStore) Value(pageID, field string) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	pageMap, ok := m.pages[pageID]
	if !ok {
		return "", false
	}
	val, exists := pageMap[field]
	if !exists || val == nil {
		return "", false
	}
	if s, isStr := val.(string); isStr {
		return s, true
	}
	return fmt.Sprint(val), true
}

// Close is a no-op for MemoryStore.
func (m *MemoryStore) Close() error {
	return nil
}
