package plugin

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Registry manages thread-safe registration and discovery of compile-time plugins.
type Registry struct {
	mu      sync.RWMutex
	plugins map[string]Plugin
}

// NewRegistry initializes an empty plugin registry.
func NewRegistry() *Registry {
	return &Registry{
		plugins: make(map[string]Plugin),
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

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.plugins[id]; exists {
		return fmt.Errorf("plugin %q is already registered", id)
	}

	r.plugins[id] = p
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
