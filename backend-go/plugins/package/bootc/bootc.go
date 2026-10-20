package bootc

import (
	"context"
	"fmt"
	"strings"

	"air-traffic-control/backend/pkg/plugin"
)

func init() {
	_ = plugin.Register(New())
}

// Plugin implements plugin.Plugin and plugin.Actor for bootc bootable container systems.
type Plugin struct{}

// New returns a new bootc package plugin.
func New() *Plugin {
	return &Plugin{}
}

// Meta returns metadata for bootc.
func (p *Plugin) Meta() plugin.Meta {
	return plugin.Meta{
		ID:          "package/bootc",
		Kind:        plugin.KindPackage,
		Name:        "bootc",
		Label:       "bootc (Bootable Containers)",
		Icon:        "deployed_code",
		Description: "Transactional container-based operating system updater",
		Order:       10,
	}
}

// Compatible checks if the host is Linux and compatible with bootc.
func (p *Plugin) Compatible(h plugin.HostFacts) bool {
	if h.OSFamily != "" && h.OSFamily != "linux" {
		return false
	}
	if h.ActivePackage != "" && h.ActivePackage != "package/bootc" {
		return false
	}
	return true
}

// Requirements returns required dependencies for bootc management.
func (p *Plugin) Requirements() plugin.Requirements {
	return plugin.Requirements{
		HostPackages: map[string][]string{
			"dnf": {"bootc"},
		},
		ControllerCollections: []string{
			"ansible.builtin",
		},
	}
}

// Probe checks if bootc is installed on the target host.
func (p *Plugin) Probe(ctx context.Context, r plugin.Runner, h plugin.HostFacts) (plugin.Detection, error) {
	if r == nil {
		return plugin.Detection{
			Detected: false,
			Status:   "OFFLINE",
			Error:    "runner is nil",
		}, nil
	}

	cmd := "command -v bootc"
	out, err := r.RunCommand(ctx, cmd)
	if err != nil || strings.TrimSpace(out) == "" {
		return plugin.Detection{
			Detected: false,
			Status:   "UNAVAILABLE",
			Error:    fmt.Sprintf("bootc probe failed: %v", err),
		}, nil
	}

	return plugin.Detection{
		Detected: true,
		Status:   "ONLINE",
		Details:  strings.TrimSpace(out),
	}, nil
}

// Actions enumerates mutation actions supported by bootc.
func (p *Plugin) Actions() []plugin.ActionSpec {
	return []plugin.ActionSpec{
		{
			Name:        "update",
			Label:       "Upgrade Container Image",
			Description: "Pulls and stages the latest bootable container image",
			Options:     []plugin.OptionSchema{},
		},
		{
			Name:        "switch",
			Label:       "Switch Container Image",
			Description: "Rebinds the operating system to a new bootable container image",
			Options: []plugin.OptionSchema{
				{
					Name:        "image",
					Type:        "string",
					Required:    true,
					Description: "Target container image reference (e.g. quay.io/org/os:latest)",
				},
			},
		},
	}
}

// Compile compiles a bootc action into declarative TaskSpec.
func (p *Plugin) Compile(h plugin.HostFacts, action string, opts map[string]any) (plugin.TaskSpec, error) {
	var cmd string
	switch action {
	case "update":
		cmd = "bootc upgrade"
	case "switch":
		image, _ := opts["image"].(string)
		if image == "" {
			return plugin.TaskSpec{}, fmt.Errorf("image parameter is required for switch")
		}
		cmd = fmt.Sprintf("bootc switch %s", image)
	default:
		return plugin.TaskSpec{}, fmt.Errorf("unsupported bootc action: %s", action)
	}

	step := plugin.TaskStep{
		Name:   fmt.Sprintf("bootc %s", action),
		Module: "ansible.builtin.command",
		Params: map[string]any{
			"cmd": cmd,
		},
	}

	return plugin.TaskSpec{
		Name:  fmt.Sprintf("bootc %s", action),
		Steps: []plugin.TaskStep{step},
	}, nil
}
