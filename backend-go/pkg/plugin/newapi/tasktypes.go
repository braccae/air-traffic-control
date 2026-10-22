package newapi

import (
	"fmt"
	"slices"
	"strings"

	"air-traffic-control/backend/pkg/plugin"
)

// TaskType identifies the execution class of an orchestrated task.
type TaskType = plugin.TaskType

const (
	TaskTypeAnsiblePlaybook = plugin.TaskTypeAnsiblePlaybook
	TaskTypePluginAction    = plugin.TaskTypePluginAction
	TaskTypeHostProbe       = plugin.TaskTypeHostProbe
	TaskTypeBackupSnapshot  = plugin.TaskTypeBackupSnapshot
	TaskTypeServiceControl  = plugin.TaskTypeServiceControl
	TaskTypeStorageOp       = plugin.TaskTypeStorageOp
	TaskTypeCustom          = plugin.TaskTypeCustom
)

// TaskTypeSpec describes a task type registered by the core system or a plugin.
type TaskTypeSpec = plugin.TaskTypeSpec

// TaskRequest represents a concrete request to compile or execute a typed task
// against a target node.
type TaskRequest = plugin.TaskRequest

// TaskTypeProvider is an optional interface implemented by plugins that
// register custom or specialized task types.
type TaskTypeProvider = plugin.TaskTypeProvider

// TaskCompileProvider is an optional interface implemented by plugins that
// compile typed TaskRequests into declarative Ansible TaskSpecs.
type TaskCompileProvider = plugin.TaskCompileProvider

// ValidateTaskRequest verifies that a TaskRequest matches the given TaskTypeSpec
// and satisfies all required parameter schemas.
func ValidateTaskRequest(spec TaskTypeSpec, req TaskRequest) error {
	if strings.TrimSpace(string(req.Type)) == "" {
		return fmt.Errorf("task request type cannot be empty")
	}
	if spec.Type != "" && req.Type != spec.Type {
		return fmt.Errorf("task request type %q does not match spec type %q", req.Type, spec.Type)
	}
	if strings.TrimSpace(req.NodeID) == "" {
		return fmt.Errorf("task request nodeId cannot be empty")
	}
	if req.DryRun && !spec.SupportsDryRun {
		return fmt.Errorf("task type %q does not support dry-run execution", spec.Type)
	}

	for _, param := range spec.Parameters {
		val, exists := req.Params[param.Name]
		if param.Required && (!exists || val == nil || val == "") {
			return fmt.Errorf("missing required parameter %q for task type %q", param.Name, spec.Type)
		}
		if !exists || val == nil || val == "" {
			continue
		}
		if len(param.Enum) > 0 {
			s := fmt.Sprint(val)
			if !slices.Contains(param.Enum, s) {
				return fmt.Errorf("parameter %q value %q is not in allowed enum %v", param.Name, s, param.Enum)
			}
		}
	}
	return nil
}
