package taskapi

import (
	"context"
	"fmt"
	"strings"
	"time"

	"air-traffic-control/backend/pkg/plugin"
	"air-traffic-control/backend/pkg/plugin/newapi"
)

// Executor defines the task execution and compilation interface for a specific TaskType.
type Executor interface {
	// Type returns the TaskType handled by this executor.
	Type() TaskType
	// Compile validates parameters and compiles the task into a declarative plugin.TaskSpec.
	Compile(ctx context.Context, facts plugin.HostFacts, params TaskParams) (plugin.TaskSpec, error)
	// Execute runs or stages the task and returns a structured TaskResult.
	Execute(ctx context.Context, facts plugin.HostFacts, params TaskParams) (TaskResult, error)
}

// CompileFunc is a function signature for compiling a task into a plugin.TaskSpec.
type CompileFunc func(ctx context.Context, facts plugin.HostFacts, params TaskParams) (plugin.TaskSpec, error)

// ExecuteFunc is a function signature for executing a task and returning a TaskResult.
type ExecuteFunc func(ctx context.Context, facts plugin.HostFacts, params TaskParams) (TaskResult, error)

// FuncExecutor adapts CompileFunc and ExecuteFunc callbacks into an Executor.
type FuncExecutor struct {
	taskType  TaskType
	compileFn CompileFunc
	executeFn ExecuteFunc
}

// NewFuncExecutor constructs an Executor backed by the provided compile and execute callbacks.
// If executeFn is nil, Execute defaults to running Compile and returning the compiled TaskSpec in TaskResult.
func NewFuncExecutor(taskType TaskType, compileFn CompileFunc, executeFn ExecuteFunc) *FuncExecutor {
	return &FuncExecutor{
		taskType:  taskType,
		compileFn: compileFn,
		executeFn: executeFn,
	}
}

// Type returns the TaskType handled by this FuncExecutor.
func (e *FuncExecutor) Type() TaskType {
	return e.taskType
}

// Compile invokes the underlying compile callback.
func (e *FuncExecutor) Compile(ctx context.Context, facts plugin.HostFacts, params TaskParams) (plugin.TaskSpec, error) {
	if e.compileFn == nil {
		return plugin.TaskSpec{}, fmt.Errorf("task type %q does not support compilation", e.taskType)
	}
	return e.compileFn(ctx, facts, params)
}

// Execute invokes the underlying execute callback or falls back to Compile when executeFn is nil.
func (e *FuncExecutor) Execute(ctx context.Context, facts plugin.HostFacts, params TaskParams) (TaskResult, error) {
	start := time.Now()
	if e.executeFn != nil {
		res, err := e.executeFn(ctx, facts, params)
		if res.Type == "" {
			res.Type = e.taskType
		}
		if res.NodeID == "" {
			res.NodeID = params.NodeID
		}
		if res.TaskID == "" {
			res.TaskID = params.ID
		}
		if res.DurationMs == 0 {
			res.DurationMs = time.Since(start).Milliseconds()
		}
		return res, err
	}

	compiled, err := e.Compile(ctx, facts, params)
	duration := time.Since(start).Milliseconds()
	if err != nil {
		return TaskResult{
			Success:    false,
			TaskID:     params.ID,
			Type:       e.taskType,
			NodeID:     params.NodeID,
			PluginID:   params.PluginID,
			Action:     params.Action,
			DryRun:     params.DryRun,
			Error:      err.Error(),
			DurationMs: duration,
		}, err
	}
	return TaskResult{
		Success:    true,
		TaskID:     params.ID,
		Type:       e.taskType,
		NodeID:     params.NodeID,
		PluginID:   params.PluginID,
		Action:     params.Action,
		DryRun:     params.DryRun,
		Changed:    !params.DryRun,
		Compiled:   &compiled,
		DurationMs: duration,
	}, nil
}

// PluginActionExecutor executes and compiles tasks by delegating to registered plugins
// that implement newapi.TaskCompileProvider or plugin.Actor.
type PluginActionExecutor struct {
	pluginRegistry *plugin.Registry
	taskType       TaskType
}

// NewPluginActionExecutor creates an Executor that delegates compilation to plugins in pluginRegistry.
func NewPluginActionExecutor(pluginRegistry *plugin.Registry, taskType TaskType) *PluginActionExecutor {
	if taskType == "" {
		taskType = TaskTypePluginAction
	}
	return &PluginActionExecutor{
		pluginRegistry: pluginRegistry,
		taskType:       taskType,
	}
}

// Type returns the TaskType handled by this executor.
func (e *PluginActionExecutor) Type() TaskType {
	return e.taskType
}

func (e *PluginActionExecutor) lookupPlugin(id string) (plugin.Plugin, bool) {
	if e.pluginRegistry != nil {
		return e.pluginRegistry.Get(id)
	}
	return plugin.Get(id)
}

// Compile resolves the target plugin from params (or Options["pluginId"]) and compiles the task
// via newapi.TaskCompileProvider or plugin.Actor.
func (e *PluginActionExecutor) Compile(ctx context.Context, facts plugin.HostFacts, params TaskParams) (plugin.TaskSpec, error) {
	pluginID := strings.TrimSpace(params.PluginID)
	if pluginID == "" {
		if s, ok := params.StringOption("pluginId"); ok {
			pluginID = s
		}
	}
	if pluginID == "" {
		return plugin.TaskSpec{}, fmt.Errorf("pluginId is required for task type %q", e.taskType)
	}

	p, ok := e.lookupPlugin(pluginID)
	if !ok {
		return plugin.TaskSpec{}, fmt.Errorf("plugin %q not found", pluginID)
	}

	// For TaskTypePluginAction when an explicit action is specified, prefer Actor.Compile
	// unless the plugin does not implement Actor.
	if e.taskType == TaskTypePluginAction && strings.TrimSpace(params.Action) != "" {
		if actor, ok := p.(plugin.Actor); ok {
			opts := params.Options
			if opts == nil {
				opts = map[string]any{}
			}
			return actor.Compile(facts, strings.TrimSpace(params.Action), opts)
		}
	}

	if tcp, ok := p.(newapi.TaskCompileProvider); ok {
		req := params.ToRequest()
		req.PluginID = pluginID
		return tcp.CompileTask(ctx, facts, req)
	}

	actor, ok := p.(plugin.Actor)
	if !ok {
		return plugin.TaskSpec{}, fmt.Errorf("plugin %q does not implement Actor or TaskCompileProvider", pluginID)
	}

	action := strings.TrimSpace(params.Action)
	if action == "" {
		if s, ok := params.StringOption("action"); ok {
			action = s
		}
	}
	if action == "" {
		return plugin.TaskSpec{}, fmt.Errorf("action is required for plugin %q", pluginID)
	}

	opts := params.Options
	if opts == nil {
		opts = map[string]any{}
	}
	return actor.Compile(facts, action, opts)
}

// Execute compiles the plugin task and returns a structured TaskResult containing the compiled TaskSpec.
func (e *PluginActionExecutor) Execute(ctx context.Context, facts plugin.HostFacts, params TaskParams) (TaskResult, error) {
	start := time.Now()
	spec, err := e.Compile(ctx, facts, params)
	duration := time.Since(start).Milliseconds()
	if err != nil {
		return TaskResult{
			Success:    false,
			TaskID:     params.ID,
			Type:       e.taskType,
			NodeID:     params.NodeID,
			PluginID:   params.PluginID,
			Action:     params.Action,
			DryRun:     params.DryRun,
			Error:      err.Error(),
			DurationMs: duration,
		}, err
	}
	return TaskResult{
		Success:    true,
		TaskID:     params.ID,
		Type:       e.taskType,
		NodeID:     params.NodeID,
		PluginID:   params.PluginID,
		Action:     params.Action,
		DryRun:     params.DryRun,
		Changed:    !params.DryRun,
		Compiled:   &spec,
		DurationMs: duration,
	}, nil
}

// newDefaultAnsiblePlaybookExecutor builds the built-in executor for TaskTypeAnsiblePlaybook.
func newDefaultAnsiblePlaybookExecutor() Executor {
	return NewFuncExecutor(TaskTypeAnsiblePlaybook, func(ctx context.Context, facts plugin.HostFacts, params TaskParams) (plugin.TaskSpec, error) {
		playbook, ok := params.StringOption("playbook")
		if !ok {
			return plugin.TaskSpec{}, fmt.Errorf("missing required parameter \"playbook\" for task type %q", TaskTypeAnsiblePlaybook)
		}
		stepParams := map[string]any{
			"playbook": playbook,
		}
		if tags, hasTags := params.StringOption("tags"); hasTags {
			stepParams["tags"] = tags
		}
		return plugin.TaskSpec{
			Name: fmt.Sprintf("Run playbook %s", playbook),
			Steps: []plugin.TaskStep{
				{
					Name:   fmt.Sprintf("Import playbook %s", playbook),
					Module: "ansible.builtin.import_playbook",
					Params: stepParams,
				},
			},
			SecretsMasked: params.SecretsMasked,
		}, nil
	}, nil)
}

// newDefaultServiceControlExecutor builds the built-in executor for TaskTypeServiceControl.
func newDefaultServiceControlExecutor() Executor {
	return NewFuncExecutor(TaskTypeServiceControl, func(ctx context.Context, facts plugin.HostFacts, params TaskParams) (plugin.TaskSpec, error) {
		svc, ok := params.StringOption("service")
		if !ok {
			return plugin.TaskSpec{}, fmt.Errorf("missing required parameter \"service\" for task type %q", TaskTypeServiceControl)
		}
		state, ok := params.StringOption("state")
		if !ok {
			return plugin.TaskSpec{}, fmt.Errorf("missing required parameter \"state\" for task type %q", TaskTypeServiceControl)
		}
		stepParams := map[string]any{
			"name": svc,
		}
		switch state {
		case "enabled":
			stepParams["enabled"] = true
		case "disabled":
			stepParams["enabled"] = false
		default:
			stepParams["state"] = state
		}
		return plugin.TaskSpec{
			Name: fmt.Sprintf("Service %s: %s", svc, state),
			Steps: []plugin.TaskStep{
				{
					Name:   fmt.Sprintf("Ensure systemd service %s is %s", svc, state),
					Module: "ansible.builtin.systemd",
					Params: stepParams,
				},
			},
			SecretsMasked: params.SecretsMasked,
		}, nil
	}, nil)
}

// newDefaultHostProbeExecutor builds the built-in executor for TaskTypeHostProbe.
func newDefaultHostProbeExecutor() Executor {
	return NewFuncExecutor(TaskTypeHostProbe, func(ctx context.Context, facts plugin.HostFacts, params TaskParams) (plugin.TaskSpec, error) {
		return plugin.TaskSpec{
			Name: fmt.Sprintf("Probe host %s", facts.NodeID),
			Steps: []plugin.TaskStep{
				{
					Name:   "Gather host subset facts",
					Module: "ansible.builtin.setup",
					Params: map[string]any{
						"gather_subset": []string{"!all", "!min", "network", "hardware"},
					},
				},
			},
			SecretsMasked: params.SecretsMasked,
		}, nil
	}, nil)
}

// newDefaultBackupSnapshotExecutor builds the built-in executor for TaskTypeBackupSnapshot.
func newDefaultBackupSnapshotExecutor(pluginRegistry *plugin.Registry) Executor {
	pluginExec := NewPluginActionExecutor(pluginRegistry, TaskTypeBackupSnapshot)
	return NewFuncExecutor(TaskTypeBackupSnapshot, func(ctx context.Context, facts plugin.HostFacts, params TaskParams) (plugin.TaskSpec, error) {
		if params.PluginID != "" {
			return pluginExec.Compile(ctx, facts, params)
		}
		repo, ok := params.StringOption("repository")
		if !ok {
			return plugin.TaskSpec{}, fmt.Errorf("missing required parameter \"repository\" for task type %q", TaskTypeBackupSnapshot)
		}
		op, hasOp := params.StringOption("operation")
		if !hasOp {
			op = "snapshot"
		}
		return plugin.TaskSpec{
			Name: fmt.Sprintf("Backup %s on %s", op, repo),
			Steps: []plugin.TaskStep{
				{
					Name:   fmt.Sprintf("Verify backup repository %s metadata", repo),
					Module: "ansible.builtin.stat",
					Params: map[string]any{
						"path": repo,
					},
				},
			},
			SecretsMasked: params.SecretsMasked,
		}, nil
	}, nil)
}

// newDefaultStorageOpExecutor builds the built-in executor for TaskTypeStorageOp.
func newDefaultStorageOpExecutor(pluginRegistry *plugin.Registry) Executor {
	pluginExec := NewPluginActionExecutor(pluginRegistry, TaskTypeStorageOp)
	return NewFuncExecutor(TaskTypeStorageOp, func(ctx context.Context, facts plugin.HostFacts, params TaskParams) (plugin.TaskSpec, error) {
		if params.PluginID != "" {
			return pluginExec.Compile(ctx, facts, params)
		}
		target, ok := params.StringOption("target")
		if !ok {
			return plugin.TaskSpec{}, fmt.Errorf("missing required parameter \"target\" for task type %q", TaskTypeStorageOp)
		}
		op, ok := params.StringOption("operation")
		if !ok {
			return plugin.TaskSpec{}, fmt.Errorf("missing required parameter \"operation\" for task type %q", TaskTypeStorageOp)
		}
		return plugin.TaskSpec{
			Name: fmt.Sprintf("Storage %s on %s", op, target),
			Steps: []plugin.TaskStep{
				{
					Name:   fmt.Sprintf("Check storage target %s for %s", target, op),
					Module: "ansible.builtin.stat",
					Params: map[string]any{
						"path": target,
					},
				},
			},
			SecretsMasked: params.SecretsMasked,
		}, nil
	}, nil)
}
