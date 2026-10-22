package plugin

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// InventoryNodeType enumerates the supported inventory host/device classes.
type InventoryNodeType string

const (
	NodeTypeLinux            InventoryNodeType = "linux"
	NodeTypeWindows          InventoryNodeType = "windows"
	NodeTypeRouterSwitch     InventoryNodeType = "router_switch"
	NodeTypeProxmox          InventoryNodeType = "proxmox"
	NodeTypeContainerHost    InventoryNodeType = "container_host"
	NodeTypeStorageAppliance InventoryNodeType = "storage_appliance"
	NodeTypeKubernetes       InventoryNodeType = "kubernetes"
)

// NodeTypeSpec describes a node classification in the datacenter inventory,
// including its default OS family, transport/elevation defaults, and expected
// capability kinds.
type NodeTypeSpec struct {
	Type              InventoryNodeType `json:"type"`
	Label             string            `json:"label"`
	Icon              string            `json:"icon"`
	Description       string            `json:"description,omitempty"`
	DefaultOSFamily   string            `json:"defaultOsFamily"`
	DefaultConnection string            `json:"defaultConnection,omitempty"`
	DefaultElevation  string            `json:"defaultElevation,omitempty"`
	Capabilities      []Kind            `json:"capabilities,omitempty"`
	Order             int               `json:"order"`
}

// NodeTypeProvider is an optional interface implemented by plugins that
// register or extend inventory node type definitions.
type NodeTypeProvider interface {
	NodeTypes() []NodeTypeSpec
}

// ValidateNodeTypeSpec verifies that a NodeTypeSpec has the required identity
// and OS family fields populated.
func ValidateNodeTypeSpec(spec NodeTypeSpec) error {
	if strings.TrimSpace(string(spec.Type)) == "" {
		return fmt.Errorf("node type cannot be empty")
	}
	if strings.TrimSpace(spec.Label) == "" {
		return fmt.Errorf("node type %q label cannot be empty", spec.Type)
	}
	if strings.TrimSpace(spec.DefaultOSFamily) == "" {
		return fmt.Errorf("node type %q defaultOsFamily cannot be empty", spec.Type)
	}
	return nil
}

// SettingsScope distinguishes datacenter-wide settings pages from per-node
// settings pages.
type SettingsScope string

const (
	SettingsScopeDatacenter SettingsScope = "datacenter"
	SettingsScopeNode       SettingsScope = "node"
)

// CategorizedSettingsPage extends SettingsPage with an explicit scope
// ("datacenter" vs "node") and grouping category for sidebar organization.
type CategorizedSettingsPage struct {
	SettingsPage
	Scope    SettingsScope `json:"scope"`
	Category string        `json:"category"`
}

// CategorizedSettingsProvider extends SettingsPageProvider so plugins
// can declare scoped and categorized settings schemas while remaining 100%
// compatible with existing SettingsPageProvider consumers.
type CategorizedSettingsProvider interface {
	SettingsPageProvider
	CategorizedSettingsPage() CategorizedSettingsPage
}

// ResolveCategorizedSettingsPage returns a normalized CategorizedSettingsPage
// for any SettingsPageProvider. If sp also implements CategorizedSettingsProvider,
// its explicit Scope and Category are preserved (defaulting to
// SettingsScopeDatacenter and a page-ID-derived category when blank).
func ResolveCategorizedSettingsPage(sp SettingsPageProvider) CategorizedSettingsPage {
	if sp == nil {
		return CategorizedSettingsPage{
			Scope:    SettingsScopeDatacenter,
			Category: "general",
		}
	}
	if csp, ok := sp.(CategorizedSettingsProvider); ok {
		page := csp.CategorizedSettingsPage()
		if page.ID == "" {
			page.SettingsPage = sp.SettingsPage()
		}
		if page.Scope == "" {
			page.Scope = SettingsScopeDatacenter
		}
		if strings.TrimSpace(page.Category) == "" {
			page.Category = defaultCategoryFromPageID(page.ID)
		}
		return page
	}

	base := sp.SettingsPage()
	scope := base.Scope
	if scope == "" {
		scope = SettingsScopeDatacenter
	}
	category := strings.TrimSpace(base.Category)
	if category == "" {
		category = defaultCategoryFromPageID(base.ID)
	}
	return CategorizedSettingsPage{
		SettingsPage: base,
		Scope:        scope,
		Category:     category,
	}
}

// ValidateCategorizedSettingsPage checks that a CategorizedSettingsPage has a
// valid ID, Scope, and non-empty Category.
func ValidateCategorizedSettingsPage(page CategorizedSettingsPage) error {
	if strings.TrimSpace(page.ID) == "" {
		return fmt.Errorf("settings page ID cannot be empty")
	}
	switch page.Scope {
	case SettingsScopeDatacenter, SettingsScopeNode:
	default:
		return fmt.Errorf("settings page %q has invalid scope %q", page.ID, page.Scope)
	}
	if strings.TrimSpace(page.Category) == "" {
		return fmt.Errorf("settings page %q category cannot be empty", page.ID)
	}
	return nil
}

func defaultCategoryFromPageID(id string) string {
	trimmed := strings.TrimSpace(id)
	if idx := strings.IndexByte(trimmed, '/'); idx > 0 {
		return trimmed[:idx]
	}
	return "general"
}

// ContextMenuTarget identifies the UI entity type that a context menu targets.
type ContextMenuTarget string

const (
	TargetNode      ContextMenuTarget = "node"
	TargetContainer ContextMenuTarget = "container"
	TargetVM        ContextMenuTarget = "vm"
	TargetStorage   ContextMenuTarget = "storage"
	TargetShare     ContextMenuTarget = "share"
	TargetNetwork   ContextMenuTarget = "network"
	TargetFirewall  ContextMenuTarget = "firewall"
	TargetService   ContextMenuTarget = "service"
	TargetDatabase  ContextMenuTarget = "database"
)

// ContextMenuEntry represents a single actionable item or nested submenu
// contributed by a plugin to a UI context menu.
type ContextMenuEntry struct {
	ID             string             `json:"id"`
	PluginID       string             `json:"pluginId,omitempty"`
	Target         ContextMenuTarget  `json:"target"`
	Label          string             `json:"label"`
	Icon           string             `json:"icon,omitempty"`
	Group          string             `json:"group,omitempty"`
	Order          int                `json:"order"`
	Action         string             `json:"action,omitempty"`
	ConfirmPrompt  string             `json:"confirmPrompt,omitempty"`
	Danger         bool               `json:"danger,omitempty"`
	Disabled       bool               `json:"disabled,omitempty"`
	DisabledReason string             `json:"disabledReason,omitempty"`
	Children       []ContextMenuEntry `json:"children,omitempty"`
}

// ContextMenuProvider is an optional interface implemented by plugins that
// contribute context menu entries for a host or resource target.
type ContextMenuProvider interface {
	ContextMenuEntries(ctx context.Context, h HostFacts) []ContextMenuEntry
}

// TaskType identifies the execution class of an orchestrated task.
type TaskType string

const (
	TaskTypeAnsiblePlaybook TaskType = "ansible_playbook"
	TaskTypePluginAction    TaskType = "plugin_action"
	TaskTypeHostProbe       TaskType = "host_probe"
	TaskTypeBackupSnapshot  TaskType = "backup_snapshot"
	TaskTypeServiceControl  TaskType = "service_control"
	TaskTypeStorageOp       TaskType = "storage_op"
	TaskTypeCustom          TaskType = "custom"
)

// TaskTypeSpec describes a task type registered by the core system or a plugin.
type TaskTypeSpec struct {
	Type           TaskType       `json:"type"`
	Label          string         `json:"label"`
	Description    string         `json:"description,omitempty"`
	Icon           string         `json:"icon,omitempty"`
	PluginID       string         `json:"pluginId,omitempty"`
	Parameters     []OptionSchema `json:"parameters,omitempty"`
	SupportsDryRun bool           `json:"supportsDryRun,omitempty"`
	SupportsCancel bool           `json:"supportsCancel,omitempty"`
}

// TaskRequest represents a concrete request to compile or execute a typed task
// against a target node.
type TaskRequest struct {
	ID       string         `json:"id"`
	Type     TaskType       `json:"type"`
	NodeID   string         `json:"nodeId"`
	PluginID string         `json:"pluginId,omitempty"`
	Action   string         `json:"action,omitempty"`
	Params   map[string]any `json:"params,omitempty"`
	DryRun   bool           `json:"dryRun,omitempty"`
}

// TaskTypeProvider is an optional interface implemented by plugins that
// register custom or specialized task types.
type TaskTypeProvider interface {
	TaskTypes() []TaskTypeSpec
}

// TaskCompileProvider is an optional interface implemented by plugins that
// compile typed TaskRequests into declarative Ansible TaskSpecs.
type TaskCompileProvider interface {
	CompileTask(ctx context.Context, h HostFacts, req TaskRequest) (TaskSpec, error)
}

// Registry manages thread-safe registration and discovery of compile-time plugins
// along with their contributed inventory node types, categorized settings pages,
// context menu providers, and task types.
type Registry struct {
	mu                   sync.RWMutex
	plugins              map[string]Plugin
	settings             map[string]SettingsPageProvider
	categorizedSettings  map[string]CategorizedSettingsPage
	nodeTypes            map[InventoryNodeType]NodeTypeSpec
	contextMenuProviders map[string]ContextMenuProvider
	taskTypes            map[string]TaskTypeSpec // key: "<pluginID>:<type>" or "<type>"
	taskTypesByType      map[TaskType]TaskTypeSpec
}

// NewRegistry initializes an empty plugin registry.
func NewRegistry() *Registry {
	return &Registry{
		plugins:              make(map[string]Plugin),
		settings:             make(map[string]SettingsPageProvider),
		categorizedSettings:  make(map[string]CategorizedSettingsPage),
		nodeTypes:            make(map[InventoryNodeType]NodeTypeSpec),
		contextMenuProviders: make(map[string]ContextMenuProvider),
		taskTypes:            make(map[string]TaskTypeSpec),
		taskTypesByType:      make(map[TaskType]TaskTypeSpec),
	}
}

// Global default registry
var defaultRegistry = NewRegistry()

// DefaultRegistry returns the process-wide default plugin Registry.
func DefaultRegistry() *Registry {
	return defaultRegistry
}

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

// ListCategorizedSettingsPages returns every settings page with its resolved
// scope and category from the global registry, ordered by Order then ID.
func ListCategorizedSettingsPages() []CategorizedSettingsPage {
	return defaultRegistry.ListCategorizedSettingsPages()
}

// ListSettingsPagesByScope returns settings pages matching the given scope
// ("datacenter" or "node") from the global registry.
func ListSettingsPagesByScope(scope SettingsScope) []CategorizedSettingsPage {
	return defaultRegistry.ListSettingsPagesByScope(scope)
}

// ListSettingsPagesByCategory returns settings pages matching the given scope
// or category filter from the global registry.
func ListSettingsPagesByCategory(scopeOrCategory string) []CategorizedSettingsPage {
	return defaultRegistry.ListSettingsPagesByCategory(scopeOrCategory)
}

// GetSettingsPage returns the settings page provider registered under the given
// page ID from the global registry.
func GetSettingsPage(id string) (SettingsPageProvider, bool) {
	return defaultRegistry.GetSettingsPage(id)
}

// GetCategorizedSettingsPage returns the resolved CategorizedSettingsPage for
// the given page ID from the global registry.
func GetCategorizedSettingsPage(id string) (CategorizedSettingsPage, bool) {
	return defaultRegistry.GetCategorizedSettingsPage(id)
}

// RegisterNodeType registers an inventory NodeTypeSpec in the global registry.
func RegisterNodeType(spec NodeTypeSpec) error {
	return defaultRegistry.RegisterNodeType(spec)
}

// GetNodeType retrieves an inventory NodeTypeSpec by its type identifier from
// the global registry.
func GetNodeType(t InventoryNodeType) (NodeTypeSpec, bool) {
	return defaultRegistry.GetNodeType(t)
}

// ListNodeTypes returns all registered inventory NodeTypeSpecs from the global
// registry, ordered by Order then Type.
func ListNodeTypes() []NodeTypeSpec {
	return defaultRegistry.ListNodeTypes()
}

// RegisterContextMenuProvider registers a standalone ContextMenuProvider under
// the given provider ID in the global registry.
func RegisterContextMenuProvider(id string, provider ContextMenuProvider) error {
	return defaultRegistry.RegisterContextMenuProvider(id, provider)
}

// ListContextMenuProviders returns all registered ContextMenuProviders from the
// global registry in deterministic ID order.
func ListContextMenuProviders() []ContextMenuProvider {
	return defaultRegistry.ListContextMenuProviders()
}

// ListContextMenuEntries collects and sorts context menu entries matching target
// and h from all compatible plugins and standalone providers in the global registry.
func ListContextMenuEntries(ctx context.Context, target ContextMenuTarget, h HostFacts) []ContextMenuEntry {
	return defaultRegistry.ListContextMenuEntries(ctx, target, h)
}

// RegisterTaskType registers a TaskTypeSpec in the global registry.
func RegisterTaskType(spec TaskTypeSpec) error {
	return defaultRegistry.RegisterTaskType(spec)
}

// GetTaskType looks up a TaskTypeSpec by TaskType in the global registry.
func GetTaskType(t TaskType) (TaskTypeSpec, bool) {
	return defaultRegistry.GetTaskType(t)
}

// ListTaskTypes returns all registered TaskTypeSpecs from the global registry,
// ordered by Type then PluginID.
func ListTaskTypes() []TaskTypeSpec {
	return defaultRegistry.ListTaskTypes()
}

// Register registers a plugin into this Registry instance after validation.
// Optional extension interfaces (SettingsPageProvider, CategorizedSettingsProvider,
// NodeTypeProvider, ContextMenuProvider, TaskTypeProvider) are validated before
// any state is mutated so registration is atomic.
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

	// Validate any contributed settings page before committing anything
	// so a bad page schema cannot leave a half-registered plugin behind.
	var settingsProvider SettingsPageProvider
	var categorizedPage CategorizedSettingsPage
	var settingsID string
	if sp, ok := p.(SettingsPageProvider); ok {
		categorizedPage = ResolveCategorizedSettingsPage(sp)
		settingsID = strings.TrimSpace(categorizedPage.ID)
		if settingsID == "" {
			return fmt.Errorf("plugin %q declares an empty settings page ID", id)
		}
		if err := ValidateCategorizedSettingsPage(categorizedPage); err != nil {
			return fmt.Errorf("plugin %q invalid settings page: %w", id, err)
		}
		categorizedPage.ID = settingsID
		if categorizedPage.SettingsPage.Scope == "" {
			categorizedPage.SettingsPage.Scope = categorizedPage.Scope
		}
		if categorizedPage.SettingsPage.Category == "" {
			categorizedPage.SettingsPage.Category = categorizedPage.Category
		}
		settingsProvider = sp
	}

	// Validate any contributed inventory node types.
	var nodeTypeSpecs []NodeTypeSpec
	if ntp, ok := p.(NodeTypeProvider); ok {
		for _, spec := range ntp.NodeTypes() {
			if err := ValidateNodeTypeSpec(spec); err != nil {
				return fmt.Errorf("plugin %q invalid node type: %w", id, err)
			}
			nodeTypeSpecs = append(nodeTypeSpecs, spec)
		}
	}

	// Check for ContextMenuProvider.
	var cmProvider ContextMenuProvider
	if cmp, ok := p.(ContextMenuProvider); ok {
		cmProvider = cmp
	}

	// Validate any contributed task types.
	var taskSpecs []TaskTypeSpec
	if ttp, ok := p.(TaskTypeProvider); ok {
		for _, spec := range ttp.TaskTypes() {
			if strings.TrimSpace(string(spec.Type)) == "" {
				return fmt.Errorf("plugin %q declares a task type with empty type", id)
			}
			if strings.TrimSpace(spec.Label) == "" {
				return fmt.Errorf("plugin %q task type %q label cannot be empty", id, spec.Type)
			}
			if spec.PluginID == "" {
				spec.PluginID = id
			}
			taskSpecs = append(taskSpecs, spec)
		}
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
		r.categorizedSettings[settingsID] = categorizedPage
	}
	for _, spec := range nodeTypeSpecs {
		r.nodeTypes[spec.Type] = spec
	}
	if cmProvider != nil {
		r.contextMenuProviders[id] = cmProvider
	}
	for _, spec := range taskSpecs {
		key := taskSpecKey(spec)
		r.taskTypes[key] = spec
		r.taskTypesByType[spec.Type] = spec
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

// ListSettingsPages returns every contributed settings page, ordered by
// Order then ID for stable sidebar presentation.
func (r *Registry) ListSettingsPages() []SettingsPage {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]SettingsPage, 0, len(r.settings))
	for id, sp := range r.settings {
		page := sp.SettingsPage()
		if cat, ok := r.categorizedSettings[id]; ok {
			if page.Scope == "" {
				page.Scope = cat.Scope
			}
			if page.Category == "" {
				page.Category = cat.Category
			}
		}
		result = append(result, page)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Order != result[j].Order {
			return result[i].Order < result[j].Order
		}
		return result[i].ID < result[j].ID
	})
	return result
}

// ListCategorizedSettingsPages returns every registered settings page with its
// resolved Scope and Category, ordered by Order then ID.
func (r *Registry) ListCategorizedSettingsPages() []CategorizedSettingsPage {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]CategorizedSettingsPage, 0, len(r.categorizedSettings))
	for _, page := range r.categorizedSettings {
		result = append(result, page)
	}
	sortCategorizedSettingsPages(result)
	return result
}

// ListSettingsPagesByScope returns registered settings pages filtered by scope
// ("datacenter" or "node"), ordered by Order then ID. If scope is empty, all
// pages are returned.
func (r *Registry) ListSettingsPagesByScope(scope SettingsScope) []CategorizedSettingsPage {
	r.mu.RLock()
	defer r.mu.RUnlock()

	target := SettingsScope(strings.TrimSpace(string(scope)))
	var result []CategorizedSettingsPage
	for _, page := range r.categorizedSettings {
		if target == "" || page.Scope == target {
			result = append(result, page)
		}
	}
	sortCategorizedSettingsPages(result)
	return result
}

// ListSettingsPagesByCategory returns registered settings pages matching the
// given filter. If filter matches a SettingsScope ("datacenter" or "node"),
// pages of that scope are returned; otherwise pages whose Category matches
// filter (case-insensitive) are returned. An empty filter returns all pages.
func (r *Registry) ListSettingsPagesByCategory(filter string) []CategorizedSettingsPage {
	trimmed := strings.TrimSpace(filter)
	if trimmed == "" {
		return r.ListCategorizedSettingsPages()
	}
	switch SettingsScope(strings.ToLower(trimmed)) {
	case SettingsScopeDatacenter:
		return r.ListSettingsPagesByScope(SettingsScopeDatacenter)
	case SettingsScopeNode:
		return r.ListSettingsPagesByScope(SettingsScopeNode)
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []CategorizedSettingsPage
	for _, page := range r.categorizedSettings {
		if strings.EqualFold(page.Category, trimmed) {
			result = append(result, page)
		}
	}
	sortCategorizedSettingsPages(result)
	return result
}

// GetSettingsPage returns the provider registered under the given page ID.
func (r *Registry) GetSettingsPage(id string) (SettingsPageProvider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	sp, exists := r.settings[id]
	return sp, exists
}

// GetCategorizedSettingsPage returns the resolved CategorizedSettingsPage for
// the given page ID.
func (r *Registry) GetCategorizedSettingsPage(id string) (CategorizedSettingsPage, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	page, exists := r.categorizedSettings[id]
	return page, exists
}

// RegisterNodeType validates and registers a standalone or plugin-contributed
// inventory NodeTypeSpec.
func (r *Registry) RegisterNodeType(spec NodeTypeSpec) error {
	if err := ValidateNodeTypeSpec(spec); err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.nodeTypes[spec.Type]; exists {
		return fmt.Errorf("node type %q is already registered", spec.Type)
	}
	r.nodeTypes[spec.Type] = spec
	return nil
}

// GetNodeType returns the NodeTypeSpec registered for the given InventoryNodeType.
func (r *Registry) GetNodeType(t InventoryNodeType) (NodeTypeSpec, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	spec, exists := r.nodeTypes[t]
	return spec, exists
}

// ListNodeTypes returns all registered inventory NodeTypeSpecs sorted by Order
// then Type.
func (r *Registry) ListNodeTypes() []NodeTypeSpec {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]NodeTypeSpec, 0, len(r.nodeTypes))
	for _, spec := range r.nodeTypes {
		result = append(result, spec)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Order != result[j].Order {
			return result[i].Order < result[j].Order
		}
		return result[i].Type < result[j].Type
	})
	return result
}

// RegisterContextMenuProvider registers a ContextMenuProvider under an explicit
// provider ID.
func (r *Registry) RegisterContextMenuProvider(id string, provider ContextMenuProvider) error {
	trimmed := strings.TrimSpace(id)
	if trimmed == "" {
		return fmt.Errorf("context menu provider ID cannot be empty")
	}
	if provider == nil {
		return fmt.Errorf("cannot register nil context menu provider")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.contextMenuProviders[trimmed]; exists {
		return fmt.Errorf("context menu provider %q is already registered", trimmed)
	}
	r.contextMenuProviders[trimmed] = provider
	return nil
}

// ListContextMenuProviders returns all registered ContextMenuProviders in
// deterministic provider ID order.
func (r *Registry) ListContextMenuProviders() []ContextMenuProvider {
	r.mu.RLock()
	defer r.mu.RUnlock()

	ids := make([]string, 0, len(r.contextMenuProviders))
	for id := range r.contextMenuProviders {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	result := make([]ContextMenuProvider, 0, len(ids))
	for _, id := range ids {
		result = append(result, r.contextMenuProviders[id])
	}
	return result
}

// ListContextMenuEntries collects context menu entries from all registered
// providers (filtering out plugins incompatible with h when h.OSFamily is
// non-empty), filters by target (when target is non-empty), and returns them
// sorted by Order, Label, then ID.
func (r *Registry) ListContextMenuEntries(ctx context.Context, target ContextMenuTarget, h HostFacts) []ContextMenuEntry {
	r.mu.RLock()
	ids := make([]string, 0, len(r.contextMenuProviders))
	for id := range r.contextMenuProviders {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	type providerEntry struct {
		id       string
		plugin   Plugin
		provider ContextMenuProvider
	}
	providers := make([]providerEntry, 0, len(ids))
	for _, id := range ids {
		providers = append(providers, providerEntry{
			id:       id,
			plugin:   r.plugins[id],
			provider: r.contextMenuProviders[id],
		})
	}
	r.mu.RUnlock()

	var result []ContextMenuEntry
	for _, item := range providers {
		if item.plugin != nil && h.OSFamily != "" && !item.plugin.Compatible(h) {
			continue
		}
		entries := item.provider.ContextMenuEntries(ctx, h)
		for _, entry := range entries {
			if entry.PluginID == "" {
				entry.PluginID = item.id
			}
			if target != "" && entry.Target != "" && entry.Target != target {
				continue
			}
			if entry.Target == "" && target != "" {
				entry.Target = target
			}
			result = append(result, entry)
		}
	}

	sort.SliceStable(result, func(i, j int) bool {
		if result[i].Order != result[j].Order {
			return result[i].Order < result[j].Order
		}
		if result[i].Label != result[j].Label {
			return result[i].Label < result[j].Label
		}
		return result[i].ID < result[j].ID
	})
	return result
}

// RegisterTaskType validates and registers a TaskTypeSpec into this Registry.
func (r *Registry) RegisterTaskType(spec TaskTypeSpec) error {
	if strings.TrimSpace(string(spec.Type)) == "" {
		return fmt.Errorf("task type cannot be empty")
	}
	if strings.TrimSpace(spec.Label) == "" {
		return fmt.Errorf("task type %q label cannot be empty", spec.Type)
	}

	key := taskSpecKey(spec)

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.taskTypes[key]; exists {
		return fmt.Errorf("task type %q is already registered", key)
	}
	r.taskTypes[key] = spec
	r.taskTypesByType[spec.Type] = spec
	return nil
}

// GetTaskType returns the TaskTypeSpec registered for the given TaskType.
func (r *Registry) GetTaskType(t TaskType) (TaskTypeSpec, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	spec, exists := r.taskTypesByType[t]
	return spec, exists
}

// ListTaskTypes returns all registered TaskTypeSpecs sorted by Type then PluginID.
func (r *Registry) ListTaskTypes() []TaskTypeSpec {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]TaskTypeSpec, 0, len(r.taskTypes))
	for _, spec := range r.taskTypes {
		result = append(result, spec)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Type != result[j].Type {
			return result[i].Type < result[j].Type
		}
		return result[i].PluginID < result[j].PluginID
	})
	return result
}

func taskSpecKey(spec TaskTypeSpec) string {
	pluginID := strings.TrimSpace(spec.PluginID)
	if pluginID != "" {
		return pluginID + ":" + string(spec.Type)
	}
	return string(spec.Type)
}

func sortCategorizedSettingsPages(pages []CategorizedSettingsPage) {
	sort.Slice(pages, func(i, j int) bool {
		if pages[i].Order != pages[j].Order {
			return pages[i].Order < pages[j].Order
		}
		return pages[i].ID < pages[j].ID
	})
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
