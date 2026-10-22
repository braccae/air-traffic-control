package newapi

import (
	"fmt"
	"slices"

	"air-traffic-control/backend/pkg/plugin"
)

// ManagementActionCategory classifies mutations exposed by a ManagementPlugin.
type ManagementActionCategory string

const (
	ActionCategoryLifecycle     ManagementActionCategory = "lifecycle"
	ActionCategoryConfiguration ManagementActionCategory = "configuration"
	ActionCategoryMaintenance   ManagementActionCategory = "maintenance"
	ActionCategorySecurity      ManagementActionCategory = "security"
	ActionCategoryDiagnostic    ManagementActionCategory = "diagnostic"
)

// ManagementActionSpec extends plugin.ActionSpec with rich operational
// metadata for confirmation prompts, idempotency, and cache invalidation.
type ManagementActionSpec struct {
	plugin.ActionSpec
	Category         ManagementActionCategory `json:"category,omitempty"`
	Idempotent       bool                     `json:"idempotent,omitempty"`
	Dangerous        bool                     `json:"dangerous,omitempty"`
	ConfirmPrompt    string                   `json:"confirmPrompt,omitempty"`
	InvalidatesKinds []plugin.Kind            `json:"invalidatesKinds,omitempty"`
}

// ManagementPlugin is the contract for plugins that perform declarative host
// mutations via Ansible TaskSpecs while providing rich action metadata and
// pre-compilation option validation.
type ManagementPlugin interface {
	plugin.Plugin
	plugin.Actor
	ManagementActions() []ManagementActionSpec
	ValidateAction(h plugin.HostFacts, action string, opts map[string]any) error
}

// ValidateManagementOptions validates an options map against a ManagementActionSpec's
// declared OptionSchema rules (required fields and enum constraints).
func ValidateManagementOptions(spec ManagementActionSpec, opts map[string]any) error {
	for _, schema := range spec.Options {
		val, exists := opts[schema.Name]
		if schema.Required && (!exists || val == nil || val == "") {
			return fmt.Errorf("missing required option %q for action %q", schema.Name, spec.Name)
		}
		if !exists || val == nil || val == "" {
			continue
		}
		if len(schema.Enum) > 0 {
			s := fmt.Sprint(val)
			if !slices.Contains(schema.Enum, s) {
				return fmt.Errorf("option %q value %q is not in allowed enum %v", schema.Name, s, schema.Enum)
			}
		}
	}
	return nil
}
