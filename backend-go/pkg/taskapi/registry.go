package taskapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"

	"air-traffic-control/backend/pkg/plugin"
	"air-traffic-control/backend/pkg/plugin/newapi"
)

// Registry manages registered TaskTypeSpecs and their corresponding Executors.
type Registry struct {
	mu             sync.RWMutex
	specs          map[string]TaskTypeSpec // key: "<pluginID>:<type>" or "<type>"
	specsByType    map[TaskType]TaskTypeSpec
	executors      map[TaskType]Executor
	pluginRegistry *plugin.Registry
}

// NewRegistry creates a new task type Registry initialized with DefaultTaskTypes
// and default executors for built-in task types.
func NewRegistry(pluginRegistry ...*plugin.Registry) *Registry {
	var pReg *plugin.Registry
	if len(pluginRegistry) > 0 {
		pReg = pluginRegistry[0]
	}
	r := &Registry{
		specs:          make(map[string]TaskTypeSpec),
		specsByType:    make(map[TaskType]TaskTypeSpec),
		executors:      make(map[TaskType]Executor),
		pluginRegistry: pReg,
	}
	for _, spec := range DefaultTaskTypes() {
		_ = r.RegisterType(spec)
	}
	_ = r.RegisterExecutor(newDefaultAnsiblePlaybookExecutor())
	_ = r.RegisterExecutor(NewPluginActionExecutor(pReg, TaskTypePluginAction))
	_ = r.RegisterExecutor(newDefaultHostProbeExecutor())
	_ = r.RegisterExecutor(newDefaultBackupSnapshotExecutor(pReg))
	_ = r.RegisterExecutor(newDefaultServiceControlExecutor())
	_ = r.RegisterExecutor(newDefaultStorageOpExecutor(pReg))
	_ = r.RegisterExecutor(NewPluginActionExecutor(pReg, TaskTypeCustom))
	return r
}

// DefaultRegistry is the process-wide default task type registry.
var defaultRegistry = NewRegistry()

// DefaultRegistry returns the global taskapi Registry.
func DefaultRegistry() *Registry {
	return defaultRegistry
}

func specKey(spec TaskTypeSpec) string {
	pluginID := strings.TrimSpace(spec.PluginID)
	if pluginID != "" {
		return pluginID + ":" + string(spec.Type)
	}
	return string(spec.Type)
}

// RegisterType validates and registers a TaskTypeSpec.
func (r *Registry) RegisterType(spec TaskTypeSpec) error {
	if strings.TrimSpace(string(spec.Type)) == "" {
		return fmt.Errorf("task type cannot be empty")
	}
	if strings.TrimSpace(spec.Label) == "" {
		return fmt.Errorf("task type %q label cannot be empty", spec.Type)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	key := specKey(spec)
	r.specs[key] = spec
	if spec.PluginID == "" {
		r.specsByType[spec.Type] = spec
	} else if _, exists := r.specsByType[spec.Type]; !exists {
		r.specsByType[spec.Type] = spec
	}
	return nil
}

// RegisterExecutor registers an Executor for its TaskType.
func (r *Registry) RegisterExecutor(exec Executor) error {
	if exec == nil {
		return fmt.Errorf("cannot register nil task executor")
	}
	t := exec.Type()
	if strings.TrimSpace(string(t)) == "" {
		return fmt.Errorf("task executor type cannot be empty")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.executors[t] = exec
	return nil
}

// SyncFromPlugins discovers any plugins in the plugin registry that implement
// newapi.TaskTypeProvider and registers their TaskTypeSpecs.
func (r *Registry) SyncFromPlugins() {
	var plugins []plugin.Plugin
	var directSpecs []TaskTypeSpec
	if r.pluginRegistry != nil {
		plugins = r.pluginRegistry.List()
		directSpecs = r.pluginRegistry.ListTaskTypes()
	} else {
		plugins = plugin.List()
		directSpecs = plugin.ListTaskTypes()
	}

	for _, spec := range directSpecs {
		_ = r.RegisterType(spec)
	}
	for _, p := range plugins {
		if provider, ok := p.(newapi.TaskTypeProvider); ok {
			meta := p.Meta()
			for _, spec := range provider.TaskTypes() {
				if spec.PluginID == "" {
					spec.PluginID = meta.ID
				}
				_ = r.RegisterType(spec)
			}
		}
	}
}

// GetType looks up a TaskTypeSpec by TaskType and optional pluginID.
func (r *Registry) GetType(t TaskType, pluginID ...string) (TaskTypeSpec, bool) {
	r.SyncFromPlugins()

	r.mu.RLock()
	defer r.mu.RUnlock()

	if len(pluginID) > 0 && strings.TrimSpace(pluginID[0]) != "" {
		key := strings.TrimSpace(pluginID[0]) + ":" + string(t)
		if spec, ok := r.specs[key]; ok {
			return spec, true
		}
	}
	if spec, ok := r.specs[string(t)]; ok {
		return spec, true
	}
	spec, ok := r.specsByType[t]
	return spec, ok
}

// ListTypes returns all registered TaskTypeSpecs (including plugin-contributed ones),
// sorted by Type then PluginID.
func (r *Registry) ListTypes() []TaskTypeSpec {
	r.SyncFromPlugins()

	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]TaskTypeSpec, 0, len(r.specs))
	for _, spec := range r.specs {
		out = append(out, spec)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Type != out[j].Type {
			return out[i].Type < out[j].Type
		}
		return out[i].PluginID < out[j].PluginID
	})
	return out
}

// GetExecutor returns the Executor registered for the given TaskType.
func (r *Registry) GetExecutor(t TaskType) (Executor, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	exec, ok := r.executors[t]
	return exec, ok
}

// Compile validates TaskParams against the matching TaskTypeSpec and invokes the
// registered Executor's Compile method.
func (r *Registry) Compile(ctx context.Context, facts plugin.HostFacts, params TaskParams) (plugin.TaskSpec, error) {
	spec, ok := r.GetType(params.Type, params.PluginID)
	if !ok {
		return plugin.TaskSpec{}, fmt.Errorf("unknown task type %q", params.Type)
	}

	cloned := params.Clone()
	cloned.ApplyDefaults(spec)
	if err := ValidateTaskParams(spec, cloned); err != nil {
		return plugin.TaskSpec{}, err
	}

	exec, ok := r.GetExecutor(cloned.Type)
	if !ok {
		// If a plugin ID is provided, fall back to the plugin action executor.
		if cloned.PluginID != "" {
			exec = NewPluginActionExecutor(r.pluginRegistry, cloned.Type)
		} else {
			return plugin.TaskSpec{}, fmt.Errorf("no executor registered for task type %q", cloned.Type)
		}
	}

	return exec.Compile(ctx, facts, cloned)
}

// Execute validates TaskParams against the matching TaskTypeSpec and invokes the
// registered Executor's Execute method.
func (r *Registry) Execute(ctx context.Context, facts plugin.HostFacts, params TaskParams) (TaskResult, error) {
	spec, ok := r.GetType(params.Type, params.PluginID)
	if !ok {
		err := fmt.Errorf("unknown task type %q", params.Type)
		return TaskResult{
			Success:  false,
			TaskID:   params.ID,
			Type:     params.Type,
			NodeID:   params.NodeID,
			PluginID: params.PluginID,
			Action:   params.Action,
			DryRun:   params.DryRun,
			Error:    err.Error(),
		}, err
	}

	cloned := params.Clone()
	cloned.ApplyDefaults(spec)
	if err := ValidateTaskParams(spec, cloned); err != nil {
		return TaskResult{
			Success:  false,
			TaskID:   cloned.ID,
			Type:     cloned.Type,
			NodeID:   cloned.NodeID,
			PluginID: cloned.PluginID,
			Action:   cloned.Action,
			DryRun:   cloned.DryRun,
			Error:    err.Error(),
		}, err
	}

	exec, ok := r.GetExecutor(cloned.Type)
	if !ok {
		if cloned.PluginID != "" {
			exec = NewPluginActionExecutor(r.pluginRegistry, cloned.Type)
		} else {
			err := fmt.Errorf("no executor registered for task type %q", cloned.Type)
			return TaskResult{
				Success:  false,
				TaskID:   cloned.ID,
				Type:     cloned.Type,
				NodeID:   cloned.NodeID,
				PluginID: cloned.PluginID,
				Action:   cloned.Action,
				DryRun:   cloned.DryRun,
				Error:    err.Error(),
			}, err
		}
	}

	return exec.Execute(ctx, facts, cloned)
}

// APIOptions configures the task type HTTP API.
type APIOptions struct {
	Registry       *Registry
	PluginRegistry *plugin.Registry
	NodeResolver   plugin.NodeResolver
}

// API exposes HTTP endpoints for task type discovery and execution.
type API struct {
	registry     *Registry
	nodeResolver plugin.NodeResolver
}

// NewAPI constructs a new task type HTTP API.
func NewAPI(opts ...APIOptions) *API {
	var o APIOptions
	if len(opts) > 0 {
		o = opts[0]
	}
	reg := o.Registry
	if reg == nil {
		if o.PluginRegistry != nil {
			reg = NewRegistry(o.PluginRegistry)
		} else {
			reg = defaultRegistry
		}
	}
	return &API{
		registry:     reg,
		nodeResolver: o.NodeResolver,
	}
}

// Registry returns the underlying taskapi Registry.
func (a *API) Registry() *Registry {
	return a.registry
}

// Register mounts the task type routes (`/api/task-types` and `/api/task-types/`) onto mux.
func (a *API) Register(mux *http.ServeMux) {
	mux.HandleFunc("/api/task-types", a.HandleTaskTypes)
	mux.HandleFunc("/api/task-types/", a.HandleTaskTypeSubroutes)
}

// RegisterRoutes is an alias for Register to match plugin.API conventions.
func (a *API) RegisterRoutes(mux *http.ServeMux) {
	a.Register(mux)
}

// HandleTaskTypes handles `GET /api/task-types` (listing all registered TaskTypeSpecs,
// optionally filtered by `?type=` or `?pluginId=`).
func (a *API) HandleTaskTypes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{
			"success": false,
			"error":   "Method not allowed",
		})
		return
	}

	typeFilter := strings.TrimSpace(r.URL.Query().Get("type"))
	pluginFilter := strings.TrimSpace(r.URL.Query().Get("pluginId"))

	all := a.registry.ListTypes()
	filtered := make([]TaskTypeSpec, 0, len(all))
	for _, spec := range all {
		if typeFilter != "" && string(spec.Type) != typeFilter {
			continue
		}
		if pluginFilter != "" && spec.PluginID != pluginFilter {
			continue
		}
		filtered = append(filtered, spec)
	}

	writeJSON(w, http.StatusOK, filtered)
}

// HandleTaskTypeSubroutes handles `/api/task-types/execute` (`POST`) and `/api/task-types/:type` (`GET`).
func (a *API) HandleTaskTypeSubroutes(w http.ResponseWriter, r *http.Request) {
	sub := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/task-types/"), "/")
	if sub == "" {
		a.HandleTaskTypes(w, r)
		return
	}

	if sub == "execute" {
		a.handleExecute(w, r)
		return
	}

	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{
			"success": false,
			"error":   "Method not allowed",
		})
		return
	}

	pluginID := strings.TrimSpace(r.URL.Query().Get("pluginId"))
	spec, ok := a.registry.GetType(TaskType(sub), pluginID)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{
			"success": false,
			"error":   fmt.Sprintf("task type %q not found", sub),
		})
		return
	}
	writeJSON(w, http.StatusOK, spec)
}

func (a *API) handleExecute(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{
			"success": false,
			"error":   "Method not allowed",
		})
		return
	}

	raw, err := io.ReadAll(r.Body)
	if err != nil || len(raw) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"error":   "request body is required",
		})
		return
	}

	var params TaskParams
	if err := json.Unmarshal(raw, &params); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"error":   fmt.Sprintf("invalid JSON body: %v", err),
		})
		return
	}

	// Support newapi.TaskRequest wire payloads that use "params" instead of "options".
	if len(params.Options) == 0 {
		var req newapi.TaskRequest
		if err := json.Unmarshal(raw, &req); err == nil && len(req.Params) > 0 {
			params.Options = req.Params
		}
	}

	facts := plugin.HostFacts{
		NodeID:    params.NodeID,
		OSFamily:  "linux",
		HostRoute: params.NodeID,
	}
	if a.nodeResolver != nil && strings.TrimSpace(params.NodeID) != "" {
		resolved, ok, err := a.nodeResolver.ResolveNode(params.NodeID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{
				"success": false,
				"error":   fmt.Sprintf("failed to resolve node %s: %v", params.NodeID, err),
			})
			return
		}
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]any{
				"success": false,
				"error":   fmt.Sprintf("node %s not found", params.NodeID),
			})
			return
		}
		facts = resolved
	}

	res, execErr := a.registry.Execute(r.Context(), facts, params)
	if execErr != nil {
		writeJSON(w, http.StatusBadRequest, res)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
