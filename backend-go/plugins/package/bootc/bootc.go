package bootc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"air-traffic-control/backend/pkg/plugin"
	"air-traffic-control/backend/pkg/plugin/newapi"
	"air-traffic-control/backend/pkg/plugin/settingsframework"
)

const (
	// statusTimeout bounds `bootc status --json` so a hung container registry or
	// SSH session cannot block a plugin read indefinitely. The frontend treats a
	// timeout as an offline/unmonitored host instead of spinning forever.
	statusTimeout = 30 * time.Second
	// probeTimeout bounds verification that the bootc binary is runnable.
	probeTimeout = 10 * time.Second
)

var (
	_ plugin.Plugin                      = (*Plugin)(nil)
	_ plugin.Reader                      = (*Plugin)(nil)
	_ plugin.Actor                       = (*Plugin)(nil)
	_ plugin.SettingsPageProvider        = (*Plugin)(nil)
	_ newapi.DataCollectionPlugin        = (*Plugin)(nil)
	_ newapi.ManagementPlugin            = (*Plugin)(nil)
	_ newapi.CategorizedSettingsProvider = (*Plugin)(nil)
	_ newapi.ContextMenuProvider         = (*Plugin)(nil)
	_ newapi.TaskCompileProvider         = (*Plugin)(nil)
)

// SettingsPageID is the route slug of the datacenter-wide bootc settings page.
const SettingsPageID = "package/bootc"

// Default settings values for bootc image operations.
const (
	DefaultImageRegistry = "quay.io"
	DefaultRebootPolicy  = "staged"
)

func init() {
	_ = plugin.Register(New())
}

// Plugin implements plugin.Plugin, plugin.Reader, plugin.Actor, and v2 newapi
// contracts for bootc bootable container systems.
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

// CollectionSchedule returns the standard polling cadence and cache TTL
// schedule for bootc deployment inspection.
func (p *Plugin) CollectionSchedule() newapi.CollectionSchedule {
	return newapi.DefaultScheduleForKind(p.Meta().Kind)
}

// CollectionSchema documents the v2 payload structure emitted by Read.
func (p *Plugin) CollectionSchema() newapi.CollectionSchema {
	return newapi.CollectionSchema{
		Version:     "v2",
		RootKey:     "currentImage",
		Description: p.Meta().Description,
	}
}

// CategorizedSettingsPage declares datacenter-wide bootc image and upgrade
// defaults via the v2 settings framework.
func (p *Plugin) CategorizedSettingsPage() newapi.CategorizedSettingsPage {
	meta := p.Meta()
	return settingsframework.NewPageBuilder(SettingsPageID, "bootc Image & Upgrade Defaults").
		WithIcon(meta.Icon).
		WithCategory(string(meta.Kind)).
		WithScope(newapi.SettingsScopeDatacenter).
		WithOrder(meta.Order).
		WithDescription("Datacenter-wide bootable container (bootc) registry, default image reference, and staging policy defaults.").
		StringField("defaultRegistry", "Default Container Registry", DefaultImageRegistry,
			settingsframework.WithRequired(true),
			settingsframework.WithPlaceholder(DefaultImageRegistry),
			settingsframework.WithDescription("Default container registry host for bootable OS images (e.g. quay.io, ghcr.io).")).
		StringField("defaultImage", "Default OS Image Reference", "",
			settingsframework.WithPlaceholder("quay.io/org/os:latest"),
			settingsframework.WithDescription("Optional fallback container image reference used when switching images without an explicit target.")).
		SelectField("rebootPolicy", "Post-Upgrade Reboot Policy", DefaultRebootPolicy, []plugin.SettingsFieldOption{
			{Value: "staged", Label: "Stage Only (Reboot Manually)"},
			{Value: "apply", Label: "Apply Immediately (bootc upgrade --apply)"},
		}, settingsframework.WithDescription("Controls whether bootc upgrades only stage the next deployment or trigger an immediate reboot.")).
		BoolField("enforceSignatureVerification", "Enforce Container Signature Verification", true,
			settingsframework.WithDescription("Require container image signature verification when staging or switching bootc images.")).
		SecretField("registryAuthVaultVar", "Registry Pull Secret Vault Variable",
			settingsframework.WithPlaceholder("vault_bootc_registry_auth"),
			settingsframework.WithDescription("Optional Ansible Vault variable holding registry authentication credentials. Never stored or returned in plaintext.")).
		BuildCategorized()
}

// SettingsPage implements plugin.SettingsPageProvider for legacy settings routes.
func (p *Plugin) SettingsPage() plugin.SettingsPage {
	return p.CategorizedSettingsPage().SettingsPage
}

func defaultTargetImage() string {
	return settingsframework.NewStoreAdapter(nil).StringOrDefault(SettingsPageID, "defaultImage", "")
}

// ContextMenuEntries provides contextual bootc actions for compatible Linux hosts.
func (p *Plugin) ContextMenuEntries(ctx context.Context, h plugin.HostFacts) []newapi.ContextMenuEntry {
	if !p.Compatible(h) {
		return nil
	}
	meta := p.Meta()
	return newapi.NewContextMenuBuilder(meta.ID).
		WithTarget(newapi.TargetNode).
		WithGroup(string(meta.Kind)).
		AddAction("update", "Upgrade Container Image", "system_update_alt", "update", 10).
		AddDangerAction("switch", "Switch Container Image", "swap_horiz", "switch",
			"Rebind this host to a new bootable container image?", 20).
		Build()
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

	// Bound the probe so a broken or hung bootc binary cannot block plugin
	// discovery. Verifying `bootc --version` avoids reporting a false positive
	// for a binary that exists but cannot execute.
	probeCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	cmd := "command -v bootc && bootc --version"
	out, err := r.RunCommand(probeCtx, cmd)
	if err != nil || strings.TrimSpace(out) == "" {
		status := "UNAVAILABLE"
		if errors.Is(probeCtx.Err(), context.DeadlineExceeded) {
			status = "OFFLINE"
		}
		return plugin.Detection{
			Detected: false,
			Status:   status,
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
	// direct invocation when the runner already has privileges. `--format json`
	// is the current flag; `--json` keeps older bootc builds working.
	cmd := "sudo -n bootc status --format json 2>/dev/null || bootc status --format json 2>/dev/null || sudo -n bootc status --json 2>/dev/null || bootc status --json"

	// Hard bound on the external command. A hung registry, SSH session or
	// container runtime must fail fast instead of spinning the UI forever.
	statusCtx, cancel := context.WithTimeout(ctx, statusTimeout)
	defer cancel()

	out, err := r.RunCommand(statusCtx, cmd)
	if err != nil {
		if errors.Is(statusCtx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("bootc status timed out after %s: %w", statusTimeout, err)
		}
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

	currentImage := imageRefOf(doc.Status.Booted)
	currentVersion := versionOf(doc.Status.Booted)
	stagedImage := imageRefOf(doc.Status.Staged)
	stagedVersion := versionOf(doc.Status.Staged)
	fallbackImage := imageRefOf(doc.Status.Rollback)
	fallbackVersion := versionOf(doc.Status.Rollback)

	// Normalize the staged deployment into the same update shape emitted by the
	// dnf/apt/apk readers so the package check endpoint can consume it directly.
	updates := make([]map[string]any, 0)
	if stagedImage != "none" {
		updates = append(updates, map[string]any{
			"name":           stagedImage,
			"currentVersion": currentVersion,
			"newVersion":     stagedVersion,
			"repository":     "bootc-registry",
			"security":       false,
		})
	}
	rebootRequired := stagedImage != "none"

	return map[string]any{
		"manager":              "bootc",
		"isImmutable":          true,
		"currentImage":         currentImage,
		"currentImageVersion":  currentVersion,
		"currentImageDigest":   digestOf(doc.Status.Booted),
		"stagedImage":          stagedImage,
		"stagedImageVersion":   stagedVersion,
		"stagedImageDigest":    digestOf(doc.Status.Staged),
		"fallbackImage":        fallbackImage,
		"fallbackImageVersion": fallbackVersion,
		"fallbackImageDigest":  digestOf(doc.Status.Rollback),
		"updates":              updates,
		"updatesCount":         len(updates),
		"rebootRequired":       rebootRequired,
		"rawOutput":            raw,
	}, nil
}

// ManagementActions returns the v2 management action specifications for bootc.
func (p *Plugin) ManagementActions() []newapi.ManagementActionSpec {
	return []newapi.ManagementActionSpec{
		{
			ActionSpec: plugin.ActionSpec{
				Name:        "update",
				Label:       "Upgrade Container Image",
				Description: "Pulls and stages the latest bootable container image",
				Options:     []plugin.OptionSchema{},
			},
			Category:         newapi.ActionCategoryMaintenance,
			Idempotent:       true,
			Dangerous:        false,
			InvalidatesKinds: []plugin.Kind{plugin.KindPackage},
		},
		{
			ActionSpec: plugin.ActionSpec{
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
			Category:         newapi.ActionCategoryLifecycle,
			Idempotent:       true,
			Dangerous:        true,
			ConfirmPrompt:    "Rebind this host to a new bootable container image?",
			InvalidatesKinds: []plugin.Kind{plugin.KindPackage},
		},
	}
}

// Actions enumerates mutation actions supported by bootc.
func (p *Plugin) Actions() []plugin.ActionSpec {
	mgmt := p.ManagementActions()
	specs := make([]plugin.ActionSpec, len(mgmt))
	for i, m := range mgmt {
		specs[i] = m.ActionSpec
	}
	return specs
}

// ValidateAction validates options for a declared bootc management action.
func (p *Plugin) ValidateAction(h plugin.HostFacts, action string, opts map[string]any) error {
	for _, spec := range p.ManagementActions() {
		if strings.EqualFold(spec.Name, action) {
			return newapi.ValidateManagementOptions(spec, opts)
		}
	}
	return fmt.Errorf("unsupported %s action: %s", p.Meta().Name, action)
}

// CompileTask adapts a typed v2 TaskRequest to the plugin's declarative
// Ansible TaskSpec compiler.
func (p *Plugin) CompileTask(ctx context.Context, h plugin.HostFacts, req newapi.TaskRequest) (plugin.TaskSpec, error) {
	action := strings.TrimSpace(req.Action)
	if action == "" && req.Params != nil {
		if a, ok := req.Params["action"].(string); ok {
			action = strings.TrimSpace(a)
		}
	}
	if action == "" {
		return plugin.TaskSpec{}, fmt.Errorf("action is required for plugin %s", p.Meta().ID)
	}
	return p.Compile(h, action, req.Params)
}

// Compile compiles a bootc action into declarative TaskSpec.
func (p *Plugin) Compile(h plugin.HostFacts, action string, opts map[string]any) (plugin.TaskSpec, error) {
	var cmd string
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "update":
		action = "update"
		cmd = "bootc upgrade"
	case "switch":
		action = "switch"
		image, _ := opts["image"].(string)
		image = strings.TrimSpace(image)
		if image == "" {
			image = strings.TrimSpace(defaultTargetImage())
		}
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
