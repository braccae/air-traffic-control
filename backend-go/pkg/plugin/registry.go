package plugin

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Registry manages thread-safe registration and discovery of compile-time plugins.
type Registry struct {
	mu       sync.RWMutex
	plugins  map[string]Plugin
	settings map[string]SettingsPageProvider
}

// NewRegistry initializes an empty plugin registry.
func NewRegistry() *Registry {
	return &Registry{
		plugins:  make(map[string]Plugin),
		settings: make(map[string]SettingsPageProvider),
	}
}

// Global default registry
var defaultRegistry = NewRegistry()

// Register validates and registers a plugin with the global registry.
func Register(p Plugin) error {
	return defaultRegistry.Register(p)
}

// Get looks up a plugin by its ID in the global registry.
func Get(id string) (Plugin, bool) {
	return defaultRegistry.Get(id)
}

// List returns all registered plugins from the global registry, sorted by kind and order.
func List() []Plugin {
	return defaultRegistry.List()
}

// ListByKind returns all registered plugins of a given Kind from the global registry, sorted by order.
func ListByKind(kind Kind) []Plugin {
	return defaultRegistry.ListByKind(kind)
}

// FilterCompatible returns all plugins in the global registry compatible with the given HostFacts.
func FilterCompatible(facts HostFacts) []Plugin {
	return defaultRegistry.FilterCompatible(facts)
}

// ListSettingsPages returns every global settings page registered in the global
// registry, ordered by Order then ID.
func ListSettingsPages() []SettingsPage {
	return defaultRegistry.ListSettingsPages()
}

// GetSettingsPage returns the settings page provider registered under the given
// page ID from the global registry.
func GetSettingsPage(id string) (SettingsPageProvider, bool) {
	return defaultRegistry.GetSettingsPage(id)
}

// Register registers a plugin into this Registry instance after validation.
func (r *Registry) Register(p Plugin) error {
	if p == nil {
		return fmt.Errorf("cannot register nil plugin")
	}

	meta := p.Meta()
	id := strings.TrimSpace(meta.ID)
	if id == "" {
		return fmt.Errorf("plugin ID cannot be empty")
	}

	parts := strings.Split(id, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return fmt.Errorf("plugin ID %q must follow format <kind>/<name>", id)
	}

	if meta.Kind != "" && string(meta.Kind) != parts[0] {
		return fmt.Errorf("plugin ID %q does not match declared Kind %q", id, meta.Kind)
	}

	// Validate any contributed global settings page before committing anything
	// so a bad page schema cannot leave a half-registered plugin behind.
	var settingsProvider SettingsPageProvider
	var settingsID string
	if sp, ok := p.(SettingsPageProvider); ok {
		page := sp.SettingsPage()
		settingsID = strings.TrimSpace(page.ID)
		if settingsID == "" {
			return fmt.Errorf("plugin %q declares an empty settings page ID", id)
		}
		settingsProvider = sp
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.plugins[id]; exists {
		return fmt.Errorf("plugin %q is already registered", id)
	}
	if settingsProvider != nil {
		if _, exists := r.settings[settingsID]; exists {
			return fmt.Errorf("settings page %q is already registered", settingsID)
		}
	}

	r.plugins[id] = p
	if settingsProvider != nil {
		r.settings[settingsID] = settingsProvider
	}
	return nil
}

// Get returns the plugin with the specified ID, or false if not found.
func (r *Registry) Get(id string) (Plugin, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	p, exists := r.plugins[id]
	return p, exists
}

// List returns all registered plugins sorted by Kind, Order, then Name.
func (r *Registry) List() []Plugin {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]Plugin, 0, len(r.plugins))
	for _, p := range r.plugins {
		result = append(result, p)
	}

	sortPlugins(result)
	return result
}

// ListByKind returns all registered plugins for the given kind sorted by Order, then Name.
func (r *Registry) ListByKind(kind Kind) []Plugin {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []Plugin
	for _, p := range r.plugins {
		if p.Meta().Kind == kind {
			result = append(result, p)
		}
	}

	sortPlugins(result)
	return result
}

// FilterCompatible returns plugins compatible with the given host facts.
func (r *Registry) FilterCompatible(facts HostFacts) []Plugin {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []Plugin
	for _, p := range r.plugins {
		if p.Compatible(facts) {
			result = append(result, p)
		}
	}

	sortPlugins(result)
	return result
}

// ListSettingsPages returns every contributed global settings page, ordered by
// Order then ID for stable sidebar presentation.
func (r *Registry) ListSettingsPages() []SettingsPage {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]SettingsPage, 0, len(r.settings))
	for _, sp := range r.settings {
		result = append(result, sp.SettingsPage())
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Order != result[j].Order {
			return result[i].Order < result[j].Order
		}
		return result[i].ID < result[j].ID
	})
	return result
}

// GetSettingsPage returns the provider registered under the given page ID.
func (r *Registry) GetSettingsPage(id string) (SettingsPageProvider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	sp, exists := r.settings[id]
	return sp, exists
}

func sortPlugins(plugins []Plugin) {
	sort.Slice(plugins, func(i, j int) bool {
		mi := plugins[i].Meta()
		mj := plugins[j].Meta()
		if mi.Kind != mj.Kind {
			return mi.Kind < mj.Kind
		}
		if mi.Order != mj.Order {
			return mi.Order < mj.Order
		}
		return mi.Name < mj.Name
	})
}
