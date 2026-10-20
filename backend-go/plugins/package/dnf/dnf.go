package dnf

import (
	"context"
	"fmt"
	"strings"

	"air-traffic-control/backend/pkg/plugin"
)

func init() {
	_ = plugin.Register(New())
}

// Plugin implements plugin.Plugin and plugin.Actor for DNF package management.
type Plugin struct{}

// New returns a new DNF package plugin.
func New() *Plugin {
	return &Plugin{}
}

// Meta returns metadata for DNF.
func (p *Plugin) Meta() plugin.Meta {
	return plugin.Meta{
		ID:          "package/dnf",
		Kind:        plugin.KindPackage,
		Name:        "dnf",
		Label:       "DNF / RPM",
		Icon:        "package_2",
		Description: "Fedora, AlmaLinux, RHEL, and CentOS package manager",
		Order:       30,
	}
}

// Compatible checks if the host is Linux and compatible with DNF.
func (p *Plugin) Compatible(h plugin.HostFacts) bool {
	if h.OSFamily != "" && h.OSFamily != "linux" {
		return false
	}
	if h.ActivePackage != "" && h.ActivePackage != "package/dnf" {
		return false
	}
	return true
}

// Requirements returns required dependencies for DNF management.
func (p *Plugin) Requirements() plugin.Requirements {
	return plugin.Requirements{
		HostPackages: map[string][]string{
			"dnf": {"dnf", "python3-dnf"},
		},
		ControllerCollections: []string{
			"ansible.builtin",
		},
	}
}

// Probe checks if dnf is installed on the target host.
func (p *Plugin) Probe(ctx context.Context, r plugin.Runner, h plugin.HostFacts) (plugin.Detection, error) {
	if r == nil {
		return plugin.Detection{
			Detected: false,
			Status:   "OFFLINE",
			Error:    "runner is nil",
		}, nil
	}

	cmd := "command -v dnf"
	out, err := r.RunCommand(ctx, cmd)
	if err != nil || strings.TrimSpace(out) == "" {
		return plugin.Detection{
			Detected: false,
			Status:   "UNAVAILABLE",
			Error:    fmt.Sprintf("dnf probe failed: %v", err),
		}, nil
	}

	return plugin.Detection{
		Detected: true,
		Status:   "ONLINE",
		Details:  strings.TrimSpace(out),
	}, nil
}

// Actions enumerates mutation actions supported by DNF.
func (p *Plugin) Actions() []plugin.ActionSpec {
	return []plugin.ActionSpec{
		{
			Name:        "install",
			Label:       "Install Package",
			Description: "Installs packages using dnf",
			Options: []plugin.OptionSchema{
				{
					Name:        "name",
					Type:        "string",
					Required:    true,
					Description: "Package name or specification",
				},
			},
		},
		{
			Name:        "update",
			Label:       "Update Packages",
			Description: "Updates packages or whole system using dnf",
			Options: []plugin.OptionSchema{
				{
					Name:        "name",
					Type:        "string",
					Required:    false,
					Default:     "*",
					Description: "Package name to update, or '*' for all packages",
				},
			},
		},
		{
			Name:        "remove",
			Label:       "Remove Package",
			Description: "Removes packages using dnf",
			Options: []plugin.OptionSchema{
				{
					Name:        "name",
					Type:        "string",
					Required:    true,
					Description: "Package name to remove",
				},
			},
		},
	}
}

// Compile compiles a DNF action into declarative TaskSpec.
func (p *Plugin) Compile(h plugin.HostFacts, action string, opts map[string]any) (plugin.TaskSpec, error) {
	name, _ := opts["name"].(string)
	if name == "" {
		if action == "update" {
			name = "*"
		} else {
			return plugin.TaskSpec{}, fmt.Errorf("package name is required for action %q", action)
		}
	}

	var state string
	switch action {
	case "install":
		state = "present"
	case "update":
		state = "latest"
	case "remove":
		state = "absent"
	default:
		return plugin.TaskSpec{}, fmt.Errorf("unsupported package action: %s", action)
	}

	step := plugin.TaskStep{
		Name:   fmt.Sprintf("DNF %s: %s", action, name),
		Module: "ansible.builtin.dnf",
		Params: map[string]any{
			"name":  name,
			"state": state,
		},
	}

	return plugin.TaskSpec{
		Name:  fmt.Sprintf("DNF package %s (%s)", action, name),
		Steps: []plugin.TaskStep{step},
	}, nil
}
