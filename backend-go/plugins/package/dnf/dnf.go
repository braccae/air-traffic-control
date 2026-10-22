package dnf

import (
	"context"
	"fmt"
	"strings"

	"air-traffic-control/backend/pkg/plugin"
	"air-traffic-control/backend/pkg/plugin/newapi"
	"air-traffic-control/backend/pkg/plugin/settingsframework"
)

const (
	// PluginID is the canonical identifier for the DNF package manager plugin.
	PluginID = "package/dnf"
	// SettingsPageID is the settings page identifier for datacenter DNF defaults.
	SettingsPageID = "package/dnf"
)

// Settings field keys for the DNF datacenter settings page.
const (
	FieldSecurityOnly        = "securityOnly"
	FieldAutoAutoremove      = "autoAutoremove"
	FieldUpdateCacheDefault  = "updateCacheDefault"
	FieldExcludePackages     = "excludePackages"
	FieldEnableWeakDeps      = "installWeakDeps"
	FieldGPGCheck            = "gpgCheck"
	FieldProxyURL            = "proxyUrl"
	FieldProxyPasswordSecret = "proxyPasswordVaultVar"
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

// Plugin implements plugin.Plugin, plugin.Reader, plugin.Actor, and v2
// extension contracts for DNF / RPM package management.
type Plugin struct{}

// New returns a new DNF package plugin.
func New() *Plugin {
	return &Plugin{}
}

// Meta returns metadata for DNF.
func (p *Plugin) Meta() plugin.Meta {
	return plugin.Meta{
		ID:          PluginID,
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
	if h.ActivePackage != "" && h.ActivePackage != PluginID {
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

// CollectionSchedule returns the standard polling and cache TTL schedule for package managers.
func (p *Plugin) CollectionSchedule() newapi.CollectionSchedule {
	return newapi.DefaultScheduleForKind(p.Meta().Kind)
}

// CollectionSchema documents the payload structure returned by Read.
func (p *Plugin) CollectionSchema() newapi.CollectionSchema {
	return newapi.CollectionSchema{
		Version:     "v2",
		RootKey:     "updates",
		Description: p.Meta().Description,
	}
}

// CategorizedSettingsPage declares datacenter-wide DNF package management defaults.
func (p *Plugin) CategorizedSettingsPage() newapi.CategorizedSettingsPage {
	meta := p.Meta()
	return settingsframework.NewPageBuilder(SettingsPageID, "DNF / RPM Package Defaults").
		WithIcon(meta.Icon).
		WithCategory(string(meta.Kind)).
		WithScope(newapi.SettingsScopeDatacenter).
		WithOrder(meta.Order).
		WithDescription("Datacenter-wide DNF package update policies, GPG verification, weak dependencies, and repository proxy settings.").
		BoolField(
			FieldSecurityOnly,
			"Security Updates Only by Default",
			false,
			settingsframework.WithDescription("Restrict default package update runs to security advisories (--security)."),
		).
		BoolField(
			FieldUpdateCacheDefault,
			"Refresh Metadata Cache Before Updates",
			true,
			settingsframework.WithDescription("Refresh DNF repository metadata cache before applying package updates."),
		).
		BoolField(
			FieldAutoAutoremove,
			"Autoremove Unneeded Dependencies",
			false,
			settingsframework.WithDescription("Automatically remove leaf dependency packages that are no longer required."),
		).
		BoolField(
			FieldEnableWeakDeps,
			"Install Weak Dependencies",
			true,
			settingsframework.WithDescription("Include recommended weak dependencies when installing new packages."),
		).
		BoolField(
			FieldGPGCheck,
			"Enforce RPM GPG Signature Check",
			true,
			settingsframework.WithDescription("Verify GPG signatures on RPM packages before installation or upgrade."),
		).
		StringField(
			FieldExcludePackages,
			"Excluded Packages",
			"",
			settingsframework.WithPlaceholder("kernel*, kmod-*"),
			settingsframework.WithDescription("Comma- or space-separated package globs excluded from automated updates."),
		).
		StringField(
			FieldProxyURL,
			"Repository HTTP/HTTPS Proxy URL",
			"",
			settingsframework.WithPlaceholder("http://proxy.internal:3128"),
			settingsframework.WithDescription("Optional HTTP/HTTPS proxy URL for DNF repository traffic."),
		).
		SecretField(
			FieldProxyPasswordSecret,
			"Repository Proxy Password Vault Variable",
			settingsframework.WithPlaceholder("vault_dnf_proxy_password"),
			settingsframework.WithDescription("Ansible Vault variable name holding the repository proxy password."),
		).
		BuildCategorized()
}

// SettingsPage returns the underlying plugin.SettingsPage for legacy settings consumers.
func (p *Plugin) SettingsPage() plugin.SettingsPage {
	return p.CategorizedSettingsPage().SettingsPage
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

// PackageUpdateEntry represents a single available package update parsed from `dnf check-update -q`.
type PackageUpdateEntry struct {
	Name       string `json:"name"`
	NewVersion string `json:"newVersion"`
	Arch       string `json:"arch,omitempty"`
	Repository string `json:"repository,omitempty"`
}

// Read inspects available DNF package updates on the target host via `dnf check-update -q`.
// Note: `dnf check-update` exits with code 100 when updates are available, so the shell
// invocation normalizes exit code 100 to 0 while propagating genuine errors.
func (p *Plugin) Read(ctx context.Context, r plugin.Runner, h plugin.HostFacts) (any, error) {
	if r == nil {
		return nil, fmt.Errorf("runner is nil")
	}

	cmd := "dnf check-update -q || [ $? -eq 100 ]"
	out, err := r.RunCommand(ctx, cmd)
	if err != nil {
		return nil, fmt.Errorf("dnf check-update failed: %w", err)
	}

	updates := parseDnfCheckUpdateOutput(out)
	rebootRequired := false
	for _, u := range updates {
		if isCriticalUpdatePackage(u.Name) {
			rebootRequired = true
			break
		}
	}

	return map[string]any{
		"manager":        "dnf",
		"isImmutable":    false,
		"updates":        updates,
		"updatesCount":   len(updates),
		"rebootRequired": rebootRequired,
		"rawOutput":      strings.TrimSpace(out),
	}, nil
}

func parseDnfCheckUpdateOutput(raw string) []PackageUpdateEntry {
	updates := make([]PackageUpdateEntry, 0)
	if strings.TrimSpace(raw) == "" {
		return updates
	}

	lines := strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n")
	merged := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "Obsoleting Packages") {
			break
		}
		if strings.HasPrefix(trimmed, "Security:") || strings.HasPrefix(trimmed, "Last metadata expiration check:") {
			continue
		}
		if (strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t")) && len(merged) > 0 {
			merged[len(merged)-1] += " " + trimmed
		} else {
			merged = append(merged, trimmed)
		}
	}

	for _, line := range merged {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		pkgArch := fields[0]
		version := fields[1]
		repo := fields[2]

		name := pkgArch
		arch := ""
		if dot := strings.LastIndex(pkgArch, "."); dot > 0 && dot < len(pkgArch)-1 {
			name = pkgArch[:dot]
			arch = pkgArch[dot+1:]
		}
		updates = append(updates, PackageUpdateEntry{
			Name:       name,
			NewVersion: version,
			Arch:       arch,
			Repository: repo,
		})
	}
	return updates
}

func isCriticalUpdatePackage(pkgName string) bool {
	lower := strings.ToLower(pkgName)
	for _, critical := range []string{"kernel", "linux", "systemd", "glibc", "grub", "shim", "microcode", "dbus", "kmod"} {
		if strings.Contains(lower, critical) {
			return true
		}
	}
	return false
}

// ManagementActions returns structured v2 action specifications for DNF package management.
func (p *Plugin) ManagementActions() []newapi.ManagementActionSpec {
	return []newapi.ManagementActionSpec{
		{
			ActionSpec: plugin.ActionSpec{
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
			Category:         newapi.ActionCategoryLifecycle,
			Idempotent:       true,
			InvalidatesKinds: []plugin.Kind{plugin.KindPackage},
		},
		{
			ActionSpec: plugin.ActionSpec{
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
			Category:         newapi.ActionCategoryMaintenance,
			Idempotent:       true,
			InvalidatesKinds: []plugin.Kind{plugin.KindPackage},
		},
		{
			ActionSpec: plugin.ActionSpec{
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
			Category:         newapi.ActionCategoryLifecycle,
			Idempotent:       true,
			Dangerous:        true,
			ConfirmPrompt:    "Remove this package from the host using DNF?",
			InvalidatesKinds: []plugin.Kind{plugin.KindPackage},
		},
	}
}

// Actions enumerates mutation actions supported by DNF.
func (p *Plugin) Actions() []plugin.ActionSpec {
	mgmt := p.ManagementActions()
	out := make([]plugin.ActionSpec, len(mgmt))
	for i, m := range mgmt {
		out[i] = m.ActionSpec
	}
	return out
}

// ValidateAction validates options for a DNF action against its schema.
func (p *Plugin) ValidateAction(h plugin.HostFacts, action string, opts map[string]any) error {
	for _, spec := range p.ManagementActions() {
		if strings.EqualFold(spec.Name, strings.TrimSpace(action)) {
			return newapi.ValidateManagementOptions(spec, opts)
		}
	}
	return fmt.Errorf("unsupported package action: %s", action)
}

// ContextMenuEntries provides node-level DNF package management context menu actions for compatible hosts.
func (p *Plugin) ContextMenuEntries(ctx context.Context, h plugin.HostFacts) []newapi.ContextMenuEntry {
	if !p.Compatible(h) {
		return nil
	}
	return newapi.NewContextMenuBuilder(PluginID).
		WithTarget(newapi.TargetNode).
		WithGroup(string(plugin.KindPackage)).
		AddAction("dnf-install", "Install Package (DNF)", "download", "install", 10).
		AddAction("dnf-update", "Update Packages (DNF)", "system_update_alt", "update", 20).
		AddDangerAction(
			"dnf-remove",
			"Remove Package (DNF)",
			"delete",
			"remove",
			"Remove this package from the host using DNF?",
			90,
		).
		Build()
}

// CompileTask implements newapi.TaskCompileProvider to compile typed TaskRequests into Ansible TaskSpecs.
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
