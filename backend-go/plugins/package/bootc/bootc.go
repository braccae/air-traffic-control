package bootc

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"air-traffic-control/backend/pkg/plugin"
)

func init() {
	_ = plugin.Register(New())
}

// Plugin implements plugin.Plugin, plugin.Reader and plugin.Actor for bootc
// bootable container systems.
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

// bootcImageRef is the innermost container image reference object emitted by
// `bootc status --json` as status.<entry>.image.image.
type bootcImageRef struct {
	Image string `json:"image"`
}

// bootcImageStatus is status.<entry>.image: the tracked image reference plus
// its resolved version/digest metadata.
type bootcImageStatus struct {
	Image   bootcImageRef `json:"image"`
	Version string        `json:"version"`
	Digest  string        `json:"imageDigest"`
}

// bootcBootEntry is a single bootc deployment (booted, staged or rollback).
type bootcBootEntry struct {
	Image bootcImageStatus `json:"image"`
}

// bootcStatusDoc is the subset of the `bootc status --json` document that the
// bootc module needs. The top-level document is a BootcHost with a status
// object carrying the booted, staged and rollback deployments.
type bootcStatusDoc struct {
	Status struct {
		Booted   *bootcBootEntry `json:"booted"`
		Staged   *bootcBootEntry `json:"staged"`
		Rollback *bootcBootEntry `json:"rollback"`
	} `json:"status"`
}

// imageRefOf returns the container image reference for a deployment, or "none"
// when the deployment (or its image reference) is absent. It never fabricates
// a value.
func imageRefOf(entry *bootcBootEntry) string {
	if entry == nil {
		return "none"
	}
	ref := strings.TrimSpace(entry.Image.Image.Image)
	if ref == "" {
		return "none"
	}
	return ref
}

// versionOf returns the resolved image version for a deployment, or an empty
// string when unavailable.
func versionOf(entry *bootcBootEntry) string {
	if entry == nil {
		return ""
	}
	return strings.TrimSpace(entry.Image.Version)
}

// digestOf returns the resolved image digest for a deployment, or an empty
// string when unavailable.
func digestOf(entry *bootcBootEntry) string {
	if entry == nil {
		return ""
	}
	return strings.TrimSpace(entry.Image.Digest)
}

// Read reports the current (booted), staged and fallback (rollback) container
// images from `bootc status --json` on the target host. Absent fields are
// reported honestly as "none" — never fabricated.
func (p *Plugin) Read(ctx context.Context, r plugin.Runner, h plugin.HostFacts) (any, error) {
	if r == nil {
		return nil, fmt.Errorf("runner is nil")
	}

	// bootc status requires root; prefer passwordless sudo and fall back to a
	// direct invocation when the runner already has privileges.
	cmd := "sudo -n bootc status --json 2>/dev/null || bootc status --json"
	out, err := r.RunCommand(ctx, cmd)
	if err != nil {
		return nil, fmt.Errorf("bootc status failed: %w", err)
	}
	raw := strings.TrimSpace(out)
	if raw == "" {
		return nil, fmt.Errorf("bootc status returned no output")
	}

	// The runner merges stderr into stdout; isolate the JSON document.
	if idx := strings.Index(raw, "{"); idx > 0 {
		raw = raw[idx:]
	}

	var doc bootcStatusDoc
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		return nil, fmt.Errorf("failed to parse bootc status JSON: %w", err)
	}

	return map[string]any{
		"currentImage":         imageRefOf(doc.Status.Booted),
		"currentImageVersion":  versionOf(doc.Status.Booted),
		"currentImageDigest":   digestOf(doc.Status.Booted),
		"stagedImage":          imageRefOf(doc.Status.Staged),
		"stagedImageVersion":   versionOf(doc.Status.Staged),
		"stagedImageDigest":    digestOf(doc.Status.Staged),
		"fallbackImage":        imageRefOf(doc.Status.Rollback),
		"fallbackImageVersion": versionOf(doc.Status.Rollback),
		"fallbackImageDigest":  digestOf(doc.Status.Rollback),
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
