package taskapi

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"air-traffic-control/backend/pkg/plugin"
	"air-traffic-control/backend/pkg/plugin/newapi"
)

// TaskType identifies the execution class of an orchestrated task.
// Aliasing newapi.TaskType keeps types interchangeable across packages without import cycles.
type TaskType = newapi.TaskType

const (
	TaskTypeAnsiblePlaybook TaskType = newapi.TaskTypeAnsiblePlaybook
	TaskTypePluginAction    TaskType = newapi.TaskTypePluginAction
	TaskTypeHostProbe       TaskType = newapi.TaskTypeHostProbe
	TaskTypeBackupSnapshot  TaskType = newapi.TaskTypeBackupSnapshot
	TaskTypeServiceControl  TaskType = newapi.TaskTypeServiceControl
	TaskTypeStorageOp       TaskType = newapi.TaskTypeStorageOp
	TaskTypeCustom          TaskType = newapi.TaskTypeCustom
)

// TaskTypeSpec describes a task type registered by the core system or a plugin.
type TaskTypeSpec = newapi.TaskTypeSpec

// TaskRequest is an alias for newapi.TaskRequest for cross-package interoperability.
type TaskRequest = newapi.TaskRequest

// AllTaskTypes returns all canonical built-in TaskType enum values in deterministic order.
func AllTaskTypes() []TaskType {
	return []TaskType{
		TaskTypeAnsiblePlaybook,
		TaskTypePluginAction,
		TaskTypeHostProbe,
		TaskTypeBackupSnapshot,
		TaskTypeServiceControl,
		TaskTypeStorageOp,
		TaskTypeCustom,
	}
}

// ValidateTaskType checks whether t is a recognized built-in TaskType or a non-empty custom task type identifier.
func ValidateTaskType(t TaskType) error {
	trimmed := strings.TrimSpace(string(t))
	if trimmed == "" {
		return fmt.Errorf("task type cannot be empty")
	}
	if slices.Contains(AllTaskTypes(), t) {
		return nil
	}
	return fmt.Errorf("unknown task type %q", t)
}

// DefaultTaskTypes returns the built-in TaskTypeSpec definitions for all core task types.
func DefaultTaskTypes() []TaskTypeSpec {
	return []TaskTypeSpec{
		{
			Type:           TaskTypeAnsiblePlaybook,
			Label:          "Ansible Playbook",
			Description:    "Execute a declarative Ansible playbook against a target node or inventory group",
			Icon:           "play_circle",
			SupportsDryRun: true,
			SupportsCancel: true,
			Parameters: []plugin.OptionSchema{
				{Name: "playbook", Type: "string", Required: true, Description: "Playbook file path or identifier"},
				{Name: "tags", Type: "string", Required: false, Description: "Optional comma-separated Ansible tags"},
			},
		},
		{
			Type:           TaskTypePluginAction,
			Label:          "Plugin Action",
			Description:    "Compile and stage a declarative mutation action from a registered plugin",
			Icon:           "extension",
			SupportsDryRun: true,
			SupportsCancel: true,
			Parameters: []plugin.OptionSchema{
				{Name: "pluginId", Type: "string", Required: false, Description: "Target plugin ID (<kind>/<name>)"},
				{Name: "action", Type: "string", Required: false, Description: "Plugin action name"},
			},
		},
		{
			Type:           TaskTypeHostProbe,
			Label:          "Host Probe",
			Description:    "Probe target host capabilities, telemetry endpoints, and installed subsystems",
			Icon:           "radar",
			SupportsDryRun: true,
			SupportsCancel: false,
			Parameters: []plugin.OptionSchema{
				{Name: "refreshCache", Type: "bool", Required: false, Default: true, Description: "Invalidate cached probe detections before probing"},
			},
		},
		{
			Type:           TaskTypeBackupSnapshot,
			Label:          "Backup Snapshot",
			Description:    "Create, verify, or prune encrypted backup snapshots",
			Icon:           "backup",
			SupportsDryRun: true,
			SupportsCancel: true,
			Parameters: []plugin.OptionSchema{
				{Name: "repository", Type: "string", Required: true, Description: "Target backup repository URI or path"},
				{Name: "operation", Type: "select", Required: false, Default: "snapshot", Enum: []string{"snapshot", "check", "prune", "restore"}, Description: "Backup operation"},
			},
		},
		{
			Type:           TaskTypeServiceControl,
			Label:          "Service Control",
			Description:    "Manage systemd units or system daemons (start, stop, restart, reload, enable, disable)",
			Icon:           "settings_power",
			SupportsDryRun: true,
			SupportsCancel: false,
			Parameters: []plugin.OptionSchema{
				{Name: "service", Type: "string", Required: true, Description: "Service or unit name"},
				{Name: "state", Type: "select", Required: true, Enum: []string{"started", "stopped", "restarted", "reloaded", "enabled", "disabled"}, Description: "Desired service state"},
			},
		},
		{
			Type:           TaskTypeStorageOp,
			Label:          "Storage Operation",
			Description:    "Perform pool, volume, or filesystem maintenance operations (scrub, trim, snapshot, resize)",
			Icon:           "hard_drive",
			SupportsDryRun: true,
			SupportsCancel: true,
			Parameters: []plugin.OptionSchema{
				{Name: "target", Type: "string", Required: true, Description: "Pool, volume, or dataset name"},
				{Name: "operation", Type: "select", Required: true, Enum: []string{"scrub", "trim", "snapshot", "balance", "check"}, Description: "Storage maintenance operation"},
			},
		},
		{
			Type:           TaskTypeCustom,
			Label:          "Custom Task",
			Description:    "Execute a custom plugin-defined or ad-hoc declarative task",
			Icon:           "tune",
			SupportsDryRun: true,
			SupportsCancel: true,
		},
	}
}

// TaskParams encapsulates the structured parameters for a task execution or compilation request.
type TaskParams struct {
	ID            string            `json:"id,omitempty"`
	Type          TaskType          `json:"type"`
	NodeID        string            `json:"nodeId"`
	PluginID      string            `json:"pluginId,omitempty"`
	Action        string            `json:"action,omitempty"`
	Options       map[string]any    `json:"options,omitempty"`
	DryRun        bool              `json:"dryRun,omitempty"`
	Become        *bool             `json:"become,omitempty"`
	BecomeMethod  string            `json:"becomeMethod,omitempty"`
	SecretsMasked map[string]string `json:"secretsMasked,omitempty"`
}

// Clone returns a deep copy of TaskParams so callers can safely mutate maps.
func (p TaskParams) Clone() TaskParams {
	out := p
	if p.Options != nil {
		out.Options = make(map[string]any, len(p.Options))
		for k, v := range p.Options {
			out.Options[k] = v
		}
	}
	if p.SecretsMasked != nil {
		out.SecretsMasked = make(map[string]string, len(p.SecretsMasked))
		for k, v := range p.SecretsMasked {
			out.SecretsMasked[k] = v
		}
	}
	if p.Become != nil {
		b := *p.Become
		out.Become = &b
	}
	return out
}

// StringOption returns a trimmed string option value and true if present and non-empty.
func (p TaskParams) StringOption(name string) (string, bool) {
	if p.Options == nil {
		return "", false
	}
	raw, ok := p.Options[name]
	if !ok || raw == nil {
		return "", false
	}
	s := strings.TrimSpace(fmt.Sprint(raw))
	if s == "" {
		return "", false
	}
	return s, true
}

// IntOption returns an integer option value and true if present and convertible.
func (p TaskParams) IntOption(name string) (int, bool) {
	if p.Options == nil {
		return 0, false
	}
	raw, ok := p.Options[name]
	if !ok || raw == nil {
		return 0, false
	}
	switch v := raw.(type) {
	case int:
		return v, true
	case int64:
		return int(v), true
	case float64:
		return int(v), true
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err == nil {
			return n, true
		}
	}
	return 0, false
}

// BoolOption returns a boolean option value and true if present and convertible.
func (p TaskParams) BoolOption(name string) (bool, bool) {
	if p.Options == nil {
		return false, false
	}
	raw, ok := p.Options[name]
	if !ok || raw == nil {
		return false, false
	}
	switch v := raw.(type) {
	case bool:
		return v, true
	case string:
		b, err := strconv.ParseBool(strings.TrimSpace(v))
		if err == nil {
			return b, true
		}
	}
	return false, false
}

// ToRequest converts TaskParams into a newapi.TaskRequest.
func (p TaskParams) ToRequest() newapi.TaskRequest {
	return newapi.TaskRequest{
		ID:       p.ID,
		Type:     p.Type,
		NodeID:   p.NodeID,
		PluginID: p.PluginID,
		Action:   p.Action,
		Params:   p.Options,
		DryRun:   p.DryRun,
	}
}

// FromRequest converts a newapi.TaskRequest into TaskParams.
func FromRequest(req newapi.TaskRequest) TaskParams {
	return TaskParams{
		ID:       req.ID,
		Type:     req.Type,
		NodeID:   req.NodeID,
		PluginID: req.PluginID,
		Action:   req.Action,
		Options:  req.Params,
		DryRun:   req.DryRun,
	}
}

// ApplyDefaults populates missing option values from the given TaskTypeSpec's parameter defaults
// and masks any secret parameters into SecretsMasked.
func (p *TaskParams) ApplyDefaults(spec TaskTypeSpec) {
	if p == nil {
		return
	}
	if p.Options == nil && len(spec.Parameters) > 0 {
		p.Options = make(map[string]any)
	}
	for _, param := range spec.Parameters {
		val, exists := p.Options[param.Name]
		if (!exists || val == nil || val == "") && param.Default != nil {
			p.Options[param.Name] = param.Default
			val = param.Default
			exists = true
		}
		if param.Secret && exists && val != nil && fmt.Sprint(val) != "" {
			if p.SecretsMasked == nil {
				p.SecretsMasked = make(map[string]string)
			}
			if _, masked := p.SecretsMasked[param.Name]; !masked {
				p.SecretsMasked[param.Name] = plugin.MaskedSecretValue(param.Name)
			}
		}
	}
}

// ValidateTaskParams verifies that TaskParams satisfies the given TaskTypeSpec,
// including required fields, dry-run support, and parameter enum constraints.
func ValidateTaskParams(spec TaskTypeSpec, params TaskParams) error {
	return newapi.ValidateTaskRequest(spec, params.ToRequest())
}

// TaskResult represents the outcome of compiling or executing a typed task.
type TaskResult struct {
	Success    bool             `json:"success"`
	TaskID     string           `json:"taskId,omitempty"`
	Type       TaskType         `json:"type"`
	NodeID     string           `json:"nodeId"`
	PluginID   string           `json:"pluginId,omitempty"`
	Action     string           `json:"action,omitempty"`
	DryRun     bool             `json:"dryRun,omitempty"`
	Changed    bool             `json:"changed"`
	Compiled   *plugin.TaskSpec `json:"compiled,omitempty"`
	Output     any              `json:"output,omitempty"`
	Error      string           `json:"error,omitempty"`
	DurationMs int64            `json:"durationMs,omitempty"`
}
