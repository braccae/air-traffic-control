package rpmostree

import (
	"context"
	"fmt"
	"strings"

	"air-traffic-control/backend/pkg/plugin"
)

func init() {
	_ = plugin.Register(New())
}

// Plugin implements plugin.Plugin and plugin.Actor for rpm-ostree atomic systems.
type Plugin struct{}

// New returns a new rpm-ostree package plugin.
func New() *Plugin {
	return &Plugin{}
}

// Meta returns metadata for rpm-ostree.
func (p *Plugin) Meta() plugin.Meta {
	return plugin.Meta{
		ID:          "package/rpmostree",
		Kind:        plugin.KindPackage,
		Name:        "rpmostree",
		Label:       "rpm-ostree (Atomic / Silverblue)",
		Icon:        "layers",
		Description: "Hybrid image/package system for Fedora Silverblue, Kinoite, and CoreOS",
		Order:       20,
	}
}

// Compatible checks if the host is Linux and compatible with rpm-ostree.
func (p *Plugin) Compatible(h plugin.HostFacts) bool {
	if h.OSFamily != "" && h.OSFamily != "linux" {
		return false
	}
	if h.ActivePackage != "" && h.ActivePackage != "package/rpmostree" {
		return false
	}
	return true
}

// Requirements returns required dependencies for rpm-ostree management.
func (p *Plugin) Requirements() plugin.Requirements {
	return plugin.Requirements{
		HostPackages: map[string][]string{
			"dnf": {"rpm-ostree"},
		},
		ControllerCollections: []string{
			"ansible.builtin",
		},
	}
}

// Probe checks if rpm-ostree is installed on the target host.
func (p *Plugin) Probe(ctx context.Context, r plugin.Runner, h plugin.HostFacts) (plugin.Detection, error) {
	if r == nil {
		return plugin.Detection{
			Detected: false,
			Status:   "OFFLINE",
			Error:    "runner is nil",
		}, nil
	}

	cmd := "command -v rpm-ostree"
	out, err := r.RunCommand(ctx, cmd)
	if err != nil || strings.TrimSpace(out) == "" {
		return plugin.Detection{
			Detected: false,
			Status:   "UNAVAILABLE",
			Error:    fmt.Sprintf("rpm-ostree probe failed: %v", err),
		}, nil
	}

	return plugin.Detection{
		Detected: true,
		Status:   "ONLINE",
		Details:  strings.TrimSpace(out),
	}, nil
}

// Actions enumerates mutation actions supported by rpm-ostree.
func (p *Plugin) Actions() []plugin.ActionSpec {
	return []plugin.ActionSpec{
		{
			Name:        "install",
			Label:       "Install Layered Package",
			Description: "Layers a package into the ostree deployment",
			Options: []plugin.OptionSchema{
				{
					Name:        "name",
					Type:        "string",
					Required:    true,
					Description: "Package name to layer",
				},
			},
		},
		{
			Name:        "update",
			Label:       "Upgrade Deployment",
			Description: "Stages the latest tree deployment",
			Options:     []plugin.OptionSchema{},
		},
		{
			Name:        "remove",
			Label:       "Remove Layered Package",
			Description: "Uninstalls a layered package",
			Options: []plugin.OptionSchema{
				{
					Name:        "name",
					Type:        "string",
					Required:    true,
					Description: "Package name to unlayer",
				},
			},
		},
	}
}

// Compile compiles an rpm-ostree action into declarative TaskSpec.
func (p *Plugin) Compile(h plugin.HostFacts, action string, opts map[string]any) (plugin.TaskSpec, error) {
	name, _ := opts["name"].(string)

	var cmd string
	switch action {
	case "install":
		if name == "" {
			return plugin.TaskSpec{}, fmt.Errorf("package name is required for install")
		}
		cmd = fmt.Sprintf("rpm-ostree install --idempotent %s", name)
	case "update":
		cmd = "rpm-ostree upgrade"
	case "remove":
		if name == "" {
			return plugin.TaskSpec{}, fmt.Errorf("package name is required for remove")
		}
		cmd = fmt.Sprintf("rpm-ostree uninstall %s", name)
	default:
		return plugin.TaskSpec{}, fmt.Errorf("unsupported rpm-ostree action: %s", action)
	}

	step := plugin.TaskStep{
		Name:   fmt.Sprintf("rpm-ostree %s", action),
		Module: "ansible.builtin.command",
		Params: map[string]any{
			"cmd": cmd,
		},
	}

	return plugin.TaskSpec{
		Name:  fmt.Sprintf("rpm-ostree %s", action),
		Steps: []plugin.TaskStep{step},
	}, nil
}
