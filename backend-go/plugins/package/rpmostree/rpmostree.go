package rpmostree

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"air-traffic-control/backend/pkg/plugin"
	"air-traffic-control/backend/pkg/plugin/newapi"
	"air-traffic-control/backend/pkg/plugin/settingsframework"
)

const (
	// PluginID is the canonical identifier for the rpm-ostree package plugin.
	PluginID = "package/rpmostree"

	// SettingsPageID is the route slug of the datacenter-wide rpm-ostree settings page.
	SettingsPageID = "package/rpmostree"

	// DefaultUpdatePolicy is the default automatic update policy for rpm-ostree deployments.
	DefaultUpdatePolicy = "stage"

	// DefaultIdempotentInstall controls whether `rpm-ostree install --idempotent` is used by default.
	DefaultIdempotentInstall = true

	// DefaultAllowLiveApply controls whether live experimental overlays (`--apply-live`) are allowed by default.
	DefaultAllowLiveApply = false

	// DefaultRebootAfterUpgrade controls whether automatic reboots are staged after upgrades by default.
	DefaultRebootAfterUpgrade = false
)

// Settings field keys for the rpm-ostree datacenter settings page.
const (
	FieldUpdatePolicy       = "updatePolicy"
	FieldIdempotentInstall  = "idempotentInstall"
	FieldAllowLiveApply     = "allowLiveApply"
	FieldRebootAfterUpgrade = "rebootAfterUpgrade"
	FieldDefaultRefspec     = "defaultRefspec"
	FieldRegistryAuthVault  = "registryAuthVaultVar"
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

func init() {
	_ = plugin.Register(New())
}

// Plugin implements plugin.Plugin, plugin.Reader, plugin.Actor, and v2 plugin
// interfaces for rpm-ostree atomic systems (Fedora Silverblue, Kinoite, CoreOS).
type Plugin struct{}

// New returns a new rpm-ostree package plugin.
func New() *Plugin {
	return &Plugin{}
}

// Meta returns metadata for rpm-ostree.
func (p *Plugin) Meta() plugin.Meta {
	return plugin.Meta{
		ID:          PluginID,
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
	if h.ActivePackage != "" && h.ActivePackage != PluginID {
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

// CollectionSchedule returns the standard polling and cache TTL schedule (60s)
// for rpm-ostree deployment inspection.
func (p *Plugin) CollectionSchedule() newapi.CollectionSchedule {
	return newapi.DefaultScheduleForKind(p.Meta().Kind)
}

// CollectionSchema documents the v2 payload structure emitted by Read.
func (p *Plugin) CollectionSchema() newapi.CollectionSchema {
	return newapi.CollectionSchema{
		Version:     "v2",
		RootKey:     "deployments",
		Description: p.Meta().Description,
	}
}

// CategorizedSettingsPage declares datacenter-wide rpm-ostree atomic deployment
// and package layering defaults via settingsframework.
func (p *Plugin) CategorizedSettingsPage() newapi.CategorizedSettingsPage {
	meta := p.Meta()
	return settingsframework.NewPageBuilder(SettingsPageID, "rpm-ostree Atomic Deployment Defaults").
		WithIcon(meta.Icon).
		WithCategory(string(meta.Kind)).
		WithScope(newapi.SettingsScopeDatacenter).
		WithOrder(meta.Order).
		WithDescription("Datacenter-wide rpm-ostree automatic update policy, package layering flags, and OSTree refspec defaults for Fedora Silverblue, Kinoite, and CoreOS nodes.").
		SelectField(
			FieldUpdatePolicy,
			"Automatic Update Policy",
			DefaultUpdatePolicy,
			[]plugin.SettingsFieldOption{
				{Value: "none", Label: "Disabled (Manual Updates Only)"},
				{Value: "check", Label: "Check Only"},
				{Value: "stage", Label: "Stage Updates for Next Boot"},
			},
			settingsframework.WithDescription("Default rpm-ostreed automatic update policy across atomic hosts."),
		).
		BoolField(
			FieldIdempotentInstall,
			"Idempotent Package Layering",
			DefaultIdempotentInstall,
			settingsframework.WithDescription("Pass --idempotent to rpm-ostree install so already-layered packages succeed cleanly."),
		).
		BoolField(
			FieldAllowLiveApply,
			"Allow Live Package Overlay (--apply-live)",
			DefaultAllowLiveApply,
			settingsframework.WithDescription("Allow applying layered package additions to the live running root filesystem without an immediate reboot."),
		).
		BoolField(
			FieldRebootAfterUpgrade,
			"Reboot Automatically After Upgrade",
			DefaultRebootAfterUpgrade,
			settingsframework.WithDescription("Reboot atomic hosts automatically after staging a new rpm-ostree deployment."),
		).
		StringField(
			FieldDefaultRefspec,
			"Default OSTree Refspec / Container Image",
			"",
			settingsframework.WithPlaceholder("fedora:fedora/41/x86_64/silverblue"),
			settingsframework.WithDescription("Optional default OSTree remote refspec or container image reference for rebase operations."),
		).
		SecretField(
			FieldRegistryAuthVault,
			"OSTree / Container Registry Auth Vault Variable",
			settingsframework.WithPlaceholder("vault_rpmostree_registry_auth"),
			settingsframework.WithDescription("Optional Ansible Vault variable holding registry authentication credentials for authenticated OSTree container images."),
		).
		BuildCategorized()
}

// SettingsPage returns the underlying plugin.SettingsPage for legacy consumers.
func (p *Plugin) SettingsPage() plugin.SettingsPage {
	return p.CategorizedSettingsPage().SettingsPage
}

// UpdatePolicy returns the persisted datacenter default update policy or DefaultUpdatePolicy.
func UpdatePolicy() string {
	return settingsframework.NewStoreAdapter(nil).StringOrDefault(SettingsPageID, FieldUpdatePolicy, DefaultUpdatePolicy)
}

// IdempotentInstall returns whether idempotent package installation is enabled by default.
func IdempotentInstall() bool {
	return settingsframework.NewStoreAdapter(nil).BoolOrDefault(SettingsPageID, FieldIdempotentInstall, DefaultIdempotentInstall)
}

// AllowLiveApply returns whether live package overlay is enabled by default.
func AllowLiveApply() bool {
	return settingsframework.NewStoreAdapter(nil).BoolOrDefault(SettingsPageID, FieldAllowLiveApply, DefaultAllowLiveApply)
}

// RebootAfterUpgrade returns whether automatic reboot after upgrade is enabled by default.
func RebootAfterUpgrade() bool {
	return settingsframework.NewStoreAdapter(nil).BoolOrDefault(SettingsPageID, FieldRebootAfterUpgrade, DefaultRebootAfterUpgrade)
}

// DefaultRefspec returns the persisted default OSTree refspec, or empty string when unset.
func DefaultRefspec() string {
	return settingsframework.NewStoreAdapter(nil).StringOrDefault(SettingsPageID, FieldDefaultRefspec, "")
}

// RegistryAuthVaultVar returns the configured unmasked vault variable name if set.
func RegistryAuthVaultVar() (string, bool) {
	return settingsframework.NewStoreAdapter(nil).SecretValue(SettingsPageID, FieldRegistryAuthVault)
}

// ContextMenuEntries provides node context menu actions for rpm-ostree hosts.
func (p *Plugin) ContextMenuEntries(ctx context.Context, h plugin.HostFacts) []newapi.ContextMenuEntry {
	if !p.Compatible(h) {
		return nil
	}
	return newapi.NewContextMenuBuilder(PluginID).
		WithTarget(newapi.TargetNode).
		WithGroup(string(plugin.KindPackage)).
		AddAction("rpmostree-update", "Upgrade Deployment", "system_update", "update", 10).
		AddAction("rpmostree-install", "Install Layered Package", "add_box", "install", 20).
		AddDangerAction(
			"rpmostree-remove",
			"Remove Layered Package",
			"delete",
			"remove",
			"Uninstall this layered package from the next rpm-ostree deployment?",
			30,
		).
		Build()
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

// rpmOstreeDeployment represents a single deployment entry from `rpm-ostree status --json`.
type rpmOstreeDeployment struct {
	ID                string   `json:"id"`
	OSName            string   `json:"osname"`
	Checksum          string   `json:"checksum"`
	Version           string   `json:"version"`
	Origin            string   `json:"origin"`
	ContainerImageRef string   `json:"container-image-reference"`
	Booted            bool     `json:"booted"`
	Staged            bool     `json:"staged"`
	Pinned            bool     `json:"pinned"`
	RequestedPackages []string `json:"requested-packages"`
	Packages          []string `json:"packages"`
}

// rpmOstreeStatusDoc is the top-level document returned by `rpm-ostree status --json`.
type rpmOstreeStatusDoc struct {
	Deployments []rpmOstreeDeployment `json:"deployments"`
}

func refOfDeployment(d *rpmOstreeDeployment) string {
	if d == nil {
		return "none"
	}
	if ref := strings.TrimSpace(d.Origin); ref != "" {
		return ref
	}
	if ref := strings.TrimSpace(d.ContainerImageRef); ref != "" {
		return ref
	}
	if osName := strings.TrimSpace(d.OSName); osName != "" {
		return osName
	}
	return "none"
}

func versionOfDeployment(d *rpmOstreeDeployment) string {
	if d == nil {
		return ""
	}
	return strings.TrimSpace(d.Version)
}

func checksumOfDeployment(d *rpmOstreeDeployment) string {
	if d == nil {
		return ""
	}
	return strings.TrimSpace(d.Checksum)
}

// Read inspects the host's live `rpm-ostree status --json` state and reports
// booted, staged, and rollback deployments along with layered packages.
// It fails loudly when the runner is nil, the command fails, or the output is invalid.
func (p *Plugin) Read(ctx context.Context, r plugin.Runner, h plugin.HostFacts) (any, error) {
	if r == nil {
		return nil, fmt.Errorf("runner is nil")
	}

	cmd := "sudo -n rpm-ostree status --json 2>/dev/null || rpm-ostree status --json"
	out, err := r.RunCommand(ctx, cmd)
	if err != nil {
		return nil, fmt.Errorf("rpm-ostree status failed: %w", err)
	}
	raw := strings.TrimSpace(out)
	if raw == "" {
		return nil, fmt.Errorf("rpm-ostree status returned no output")
	}

	if idx := strings.Index(raw, "{"); idx > 0 {
		raw = raw[idx:]
	}

	var doc rpmOstreeStatusDoc
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		return nil, fmt.Errorf("failed to parse rpm-ostree status JSON: %w", err)
	}

	var booted, staged, rollback *rpmOstreeDeployment
	deployments := make([]map[string]any, 0, len(doc.Deployments))

	for i := range doc.Deployments {
		d := &doc.Deployments[i]
		reqPkgs := d.RequestedPackages
		if len(reqPkgs) == 0 && len(d.Packages) > 0 {
			reqPkgs = d.Packages
		}
		if reqPkgs == nil {
			reqPkgs = []string{}
		}
		deployments = append(deployments, map[string]any{
			"id":                strings.TrimSpace(d.ID),
			"osName":            strings.TrimSpace(d.OSName),
			"checksum":          strings.TrimSpace(d.Checksum),
			"version":           strings.TrimSpace(d.Version),
			"origin":            refOfDeployment(d),
			"booted":            d.Booted,
			"staged":            d.Staged,
			"pinned":            d.Pinned,
			"requestedPackages": reqPkgs,
		})

		if d.Booted && booted == nil {
			booted = d
		} else if d.Staged && staged == nil {
			staged = d
		} else if !d.Booted && !d.Staged && rollback == nil {
			rollback = d
		}
	}

	layeredPackages := []string{}
	if booted != nil {
		if len(booted.RequestedPackages) > 0 {
			layeredPackages = append(layeredPackages, booted.RequestedPackages...)
		} else if len(booted.Packages) > 0 {
			layeredPackages = append(layeredPackages, booted.Packages...)
		}
	}

	return map[string]any{
		"deployments":            deployments,
		"currentOrigin":          refOfDeployment(booted),
		"currentVersion":         versionOfDeployment(booted),
		"currentChecksum":        checksumOfDeployment(booted),
		"stagedOrigin":           refOfDeployment(staged),
		"stagedVersion":          versionOfDeployment(staged),
		"stagedChecksum":         checksumOfDeployment(staged),
		"rollbackOrigin":         refOfDeployment(rollback),
		"rollbackVersion":        versionOfDeployment(rollback),
		"rollbackChecksum":       checksumOfDeployment(rollback),
		"layeredPackages":        layeredPackages,
		"pendingDeployment":      staged != nil,
		"defaultUpdatePolicy":    UpdatePolicy(),
		"defaultIdempotentLayer": IdempotentInstall(),
	}, nil
}

// ManagementActions returns the structured v2 management action specifications for rpm-ostree.
func (p *Plugin) ManagementActions() []newapi.ManagementActionSpec {
	return []newapi.ManagementActionSpec{
		{
			ActionSpec: plugin.ActionSpec{
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
			Category:         newapi.ActionCategoryConfiguration,
			Idempotent:       true,
			Dangerous:        false,
			InvalidatesKinds: []plugin.Kind{plugin.KindPackage},
		},
		{
			ActionSpec: plugin.ActionSpec{
				Name:        "update",
				Label:       "Upgrade Deployment",
				Description: "Stages the latest tree deployment",
				Options:     []plugin.OptionSchema{},
			},
			Category:         newapi.ActionCategoryMaintenance,
			Idempotent:       true,
			Dangerous:        false,
			InvalidatesKinds: []plugin.Kind{plugin.KindPackage},
		},
		{
			ActionSpec: plugin.ActionSpec{
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
			Category:         newapi.ActionCategoryConfiguration,
			Idempotent:       true,
			Dangerous:        true,
			ConfirmPrompt:    "Uninstall this layered package from the next rpm-ostree deployment?",
			InvalidatesKinds: []plugin.Kind{plugin.KindPackage},
		},
	}
}

// Actions enumerates mutation actions supported by rpm-ostree, projected from ManagementActions.
func (p *Plugin) Actions() []plugin.ActionSpec {
	mgmt := p.ManagementActions()
	specs := make([]plugin.ActionSpec, len(mgmt))
	for i, m := range mgmt {
		specs[i] = m.ActionSpec
	}
	return specs
}

// ValidateAction validates options for an rpm-ostree management action against its schema.
func (p *Plugin) ValidateAction(h plugin.HostFacts, action string, opts map[string]any) error {
	norm := strings.TrimSpace(action)
	for _, spec := range p.ManagementActions() {
		if strings.EqualFold(spec.Name, norm) {
			if err := newapi.ValidateManagementOptions(spec, opts); err != nil {
				return err
			}
			if spec.Name == "install" || spec.Name == "remove" {
				name, _ := opts["name"].(string)
				if strings.TrimSpace(name) == "" {
					return fmt.Errorf("package name is required for %s", spec.Name)
				}
			}
			return nil
		}
	}
	return fmt.Errorf("unsupported rpm-ostree action: %s", action)
}

// Compile compiles an rpm-ostree action into declarative TaskSpec.
func (p *Plugin) Compile(h plugin.HostFacts, action string, opts map[string]any) (plugin.TaskSpec, error) {
	name, _ := opts["name"].(string)
	name = strings.TrimSpace(name)

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

// CompileTask implements newapi.TaskCompileProvider to compile typed TaskRequests
// into declarative Ansible TaskSpecs.
func (p *Plugin) CompileTask(ctx context.Context, h plugin.HostFacts, req newapi.TaskRequest) (plugin.TaskSpec, error) {
	action := strings.TrimSpace(req.Action)
	if action == "" && req.Params != nil {
		if a, ok := req.Params["action"].(string); ok {
			action = strings.TrimSpace(a)
		}
	}
	if action == "" {
		return plugin.TaskSpec{}, fmt.Errorf("action is required for plugin %s", PluginID)
	}
	return p.Compile(h, action, req.Params)
}
