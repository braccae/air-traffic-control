package apk

import (
	"context"
	"fmt"
	"strings"

	"air-traffic-control/backend/pkg/plugin"
)

func init() {
	_ = plugin.Register(New())
}

// Plugin implements plugin.Plugin and plugin.Actor for APK package management.
type Plugin struct{}

// New returns a new APK package plugin.
func New() *Plugin {
	return &Plugin{}
}

// Meta returns metadata for APK.
func (p *Plugin) Meta() plugin.Meta {
	return plugin.Meta{
		ID:          "package/apk",
		Kind:        plugin.KindPackage,
		Name:        "apk",
		Label:       "APK (Alpine)",
		Icon:        "package_2",
		Description: "Alpine Linux package manager",
		Order:       50,
	}
}

// Compatible checks if the host is Linux and compatible with APK.
func (p *Plugin) Compatible(h plugin.HostFacts) bool {
	if h.OSFamily != "" && h.OSFamily != "linux" {
		return false
	}
	if h.ActivePackage != "" && h.ActivePackage != "package/apk" {
		return false
	}
	return true
}

// Requirements returns required dependencies for APK management.
func (p *Plugin) Requirements() plugin.Requirements {
	return plugin.Requirements{
		HostPackages: map[string][]string{
			"apk": {"apk-tools"},
		},
		ControllerCollections: []string{
			"community.general",
		},
	}
}

// Probe checks if apk is installed on the target host.
func (p *Plugin) Probe(ctx context.Context, r plugin.Runner, h plugin.HostFacts) (plugin.Detection, error) {
	if r == nil {
		return plugin.Detection{
			Detected: false,
			Status:   "OFFLINE",
			Error:    "runner is nil",
		}, nil
	}

	cmd := "command -v apk"
	out, err := r.RunCommand(ctx, cmd)
	if err != nil || strings.TrimSpace(out) == "" {
		return plugin.Detection{
			Detected: false,
			Status:   "UNAVAILABLE",
			Error:    fmt.Sprintf("apk probe failed: %v", err),
		}, nil
	}

	return plugin.Detection{
		Detected: true,
		Status:   "ONLINE",
		Details:  strings.TrimSpace(out),
	}, nil
}

// Actions enumerates mutation actions supported by APK.
func (p *Plugin) Actions() []plugin.ActionSpec {
	return []plugin.ActionSpec{
		{
			Name:        "install",
			Label:       "Install Package",
			Description: "Installs packages using apk",
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
			Description: "Updates packages or whole system using apk",
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
			Description: "Removes packages using apk",
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

// Compile compiles an APK action into declarative TaskSpec.
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

	params := map[string]any{
		"state": state,
	}
	if name == "*" {
		params["update_cache"] = true
		params["upgrade"] = true
	} else {
		params["name"] = name
	}

	step := plugin.TaskStep{
		Name:   fmt.Sprintf("APK %s: %s", action, name),
		Module: "community.general.apk",
		Params: params,
	}

	return plugin.TaskSpec{
		Name:  fmt.Sprintf("APK package %s (%s)", action, name),
		Steps: []plugin.TaskStep{step},
	}, nil
}
