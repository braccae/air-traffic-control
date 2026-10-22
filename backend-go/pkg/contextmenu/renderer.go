package contextmenu

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"air-traffic-control/backend/pkg/plugin"
	"air-traffic-control/backend/pkg/plugin/newapi"
)

// Renderer evaluates registered context menu providers and static menu items
// for a given RenderRequest and produces a structured RenderedMenu.
type Renderer interface {
	Render(ctx context.Context, req RenderRequest) (RenderedMenu, error)
}

// DefaultRenderer aggregates context menu items from a plugin.Registry (any
// registered plugin implementing newapi.ContextMenuProvider) as well as any
// explicitly registered static or dynamic providers.
type DefaultRenderer struct {
	mu           sync.RWMutex
	registry     *plugin.Registry
	nodeResolver plugin.NodeResolver
	providers    map[string]newapi.ContextMenuProvider
	staticItems  []MenuItem
}

// RendererOptions configures a DefaultRenderer instance.
type RendererOptions struct {
	Registry     *plugin.Registry
	NodeResolver plugin.NodeResolver
}

// NewRenderer creates a new DefaultRenderer backed by the given Registry and
// optional NodeResolver. When Registry is nil, it uses the global plugin registry
// via plugin.List().
func NewRenderer(opts RendererOptions) *DefaultRenderer {
	return &DefaultRenderer{
		registry:     opts.Registry,
		nodeResolver: opts.NodeResolver,
		providers:    make(map[string]newapi.ContextMenuProvider),
	}
}

// RegisterProvider registers a standalone ContextMenuProvider under the given ID.
func (r *DefaultRenderer) RegisterProvider(id string, provider newapi.ContextMenuProvider) error {
	trimmed := strings.TrimSpace(id)
	if trimmed == "" {
		return fmt.Errorf("context menu provider ID cannot be empty")
	}
	if provider == nil {
		return fmt.Errorf("context menu provider %q cannot be nil", trimmed)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.providers[trimmed] = provider
	return nil
}

// RegisterStaticItems registers static MenuItems that are included whenever
// their Target matches the render request.
func (r *DefaultRenderer) RegisterStaticItems(items ...MenuItem) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, item := range items {
		if item.Target == "" {
			item.Target = TargetNode
		}
		if item.Kind == "" {
			item.Kind = ItemKindAction
		}
		r.staticItems = append(r.staticItems, cloneItem(item))
	}
}

// Render collects, filters, sorts, and groups all context menu items matching req.
func (r *DefaultRenderer) Render(ctx context.Context, req RenderRequest) (RenderedMenu, error) {
	target := req.Target
	if target == "" {
		target = TargetNode
	}
	if err := ValidateTarget(target); err != nil {
		return RenderedMenu{}, err
	}

	facts, err := r.resolveHostFacts(req.NodeID)
	if err != nil {
		return RenderedMenu{}, err
	}

	pluginFilter := strings.TrimSpace(req.PluginID)
	var collected []MenuItem

	// 1. Collect from plugins in the registry implementing newapi.ContextMenuProvider.
	plugins := r.listPlugins()
	for _, p := range plugins {
		meta := p.Meta()
		if pluginFilter != "" && meta.ID != pluginFilter {
			continue
		}
		// If a specific node was resolved, only include plugins compatible with its facts.
		if facts.NodeID != "" && facts.OSFamily != "" && !p.Compatible(facts) {
			continue
		}
		provider, ok := p.(newapi.ContextMenuProvider)
		if !ok {
			continue
		}
		for _, entry := range provider.ContextMenuEntries(ctx, facts) {
			if entry.PluginID == "" {
				entry.PluginID = meta.ID
			}
			item := FromNewAPIEntry(entry)
			if item.Target == target {
				collected = append(collected, r.bindResourceParams(item, req))
			}
		}
	}

	// 2. Collect from standalone providers registered directly on the renderer.
	r.mu.RLock()
	standaloneIDs := make([]string, 0, len(r.providers))
	for id := range r.providers {
		standaloneIDs = append(standaloneIDs, id)
	}
	staticCopy := make([]MenuItem, len(r.staticItems))
	copy(staticCopy, r.staticItems)
	r.mu.RUnlock()

	for _, id := range standaloneIDs {
		if pluginFilter != "" && id != pluginFilter {
			continue
		}
		r.mu.RLock()
		prov := r.providers[id]
		r.mu.RUnlock()
		if prov == nil {
			continue
		}
		for _, entry := range prov.ContextMenuEntries(ctx, facts) {
			if entry.PluginID == "" {
				entry.PluginID = id
			}
			item := FromNewAPIEntry(entry)
			if item.Target == target {
				collected = append(collected, r.bindResourceParams(item, req))
			}
		}
	}

	// 3. Collect from static items.
	for _, item := range staticCopy {
		if pluginFilter != "" && item.PluginID != pluginFilter {
			continue
		}
		if item.Target == target {
			collected = append(collected, r.bindResourceParams(cloneItem(item), req))
		}
	}

	SortItems(collected)
	if collected == nil {
		collected = []MenuItem{}
	}
	sections := GroupItemsIntoSections(collected)

	return RenderedMenu{
		Target:     target,
		NodeID:     req.NodeID,
		ResourceID: req.ResourceID,
		Sections:   sections,
		Items:      collected,
	}, nil
}

func (r *DefaultRenderer) listPlugins() []plugin.Plugin {
	if r.registry != nil {
		return r.registry.List()
	}
	return plugin.List()
}

func (r *DefaultRenderer) resolveHostFacts(nodeID string) (plugin.HostFacts, error) {
	trimmed := strings.TrimSpace(nodeID)
	if trimmed == "" {
		return plugin.HostFacts{}, nil
	}
	if r.nodeResolver == nil {
		return plugin.HostFacts{
			NodeID:   trimmed,
			OSFamily: "linux",
		}, nil
	}
	facts, ok, err := r.nodeResolver.ResolveNode(trimmed)
	if err != nil {
		return plugin.HostFacts{}, fmt.Errorf("failed to resolve node %q: %w", trimmed, err)
	}
	if !ok {
		return plugin.HostFacts{
			NodeID:   trimmed,
			OSFamily: "linux",
		}, nil
	}
	return facts, nil
}

func (r *DefaultRenderer) bindResourceParams(item MenuItem, req RenderRequest) MenuItem {
	if req.NodeID == "" && req.ResourceID == "" {
		return item
	}
	if item.Kind == ItemKindSeparator {
		return item
	}
	if item.Params == nil {
		item.Params = make(map[string]any)
	}
	if req.NodeID != "" {
		if _, exists := item.Params["nodeId"]; !exists {
			item.Params["nodeId"] = req.NodeID
		}
	}
	if req.ResourceID != "" {
		if _, exists := item.Params["resourceId"]; !exists {
			item.Params["resourceId"] = req.ResourceID
		}
	}
	if len(item.Children) > 0 {
		for i, child := range item.Children {
			item.Children[i] = r.bindResourceParams(child, req)
		}
	}
	return item
}

// API exposes HTTP endpoints for querying and rendering context menus.
type API struct {
	renderer Renderer
}

// NewAPI constructs a new HTTP API wrapping the given Renderer.
// If renderer is nil, a DefaultRenderer is initialized with default options.
func NewAPI(renderer Renderer) *API {
	if renderer == nil {
		renderer = NewRenderer(RendererOptions{})
	}
	return &API{renderer: renderer}
}

// RegisterRoutes mounts `/api/context-menus` and `/api/context-menus/` onto mux.
func (a *API) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/context-menus", a.HandleContextMenus)
	mux.HandleFunc("/api/context-menus/", a.HandleContextMenus)
}

// Register is an alias for RegisterRoutes to match sibling API packages in backend-go.
func (a *API) Register(mux *http.ServeMux) {
	a.RegisterRoutes(mux)
}

// HandleContextMenus serves GET /api/context-menus and GET /api/context-menus/:target.
// Query parameters:
//   - target: context menu target (node, container, vm, storage, share, network, firewall, service, database)
//   - nodeId: optional node identifier for host-fact-aware menu entries
//   - resourceId: optional target resource identifier (e.g. container name, dataset, VM ID)
//   - pluginId: optional filter restricting entries to a single plugin ID
func (a *API) HandleContextMenus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	rawTarget := r.URL.Query().Get("target")
	if rawTarget == "" && strings.HasPrefix(r.URL.Path, "/api/context-menus/") {
		pathSuffix := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/context-menus/"), "/")
		if pathSuffix == "targets" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(AllTargets())
			return
		}
		rawTarget = pathSuffix
	}

	target, err := ParseTarget(rawTarget)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	req := RenderRequest{
		Target:     target,
		NodeID:     strings.TrimSpace(r.URL.Query().Get("nodeId")),
		ResourceID: strings.TrimSpace(r.URL.Query().Get("resourceId")),
		PluginID:   strings.TrimSpace(r.URL.Query().Get("pluginId")),
	}

	rendered, err := a.renderer.Render(r.Context(), req)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(rendered)
}
