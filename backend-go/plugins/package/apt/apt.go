package apt

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"air-traffic-control/backend/pkg/plugin"
	"air-traffic-control/backend/pkg/plugin/newapi"
	"air-traffic-control/backend/pkg/plugin/settingsframework"
)

const (
	// PluginID is the canonical identifier for the APT package manager plugin.
	PluginID = "package/apt"
	// SettingsPageID is the settings page identifier for datacenter APT defaults.
	SettingsPageID = "package/apt"
)

// Settings field keys for the APT datacenter settings page.
const (
	FieldUpdateCacheDefault  = "updateCacheDefault"
	FieldUpgradeStrategy     = "upgradeStrategy"
	FieldInstallRecommends   = "installRecommends"
	FieldAutoAutoremove      = "autoAutoremove"
	FieldSecurityOnly        = "securityOnly"
	FieldHoldPackages        = "holdPackages"
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
// extension contracts for APT / Dpkg package management.
type Plugin struct{}

// New returns a new APT package plugin.
func New() *Plugin {
	return &Plugin{}
}

// Meta returns metadata for APT.
func (p *Plugin) Meta() plugin.Meta {
	return plugin.Meta{
		ID:          PluginID,
		Kind:        plugin.KindPackage,
		Name:        "apt",
		Label:       "APT / Dpkg",
		Icon:        "package_2",
		Description: "Debian, Ubuntu, and Raspbian package manager",
		Order:       40,
	}
}

// Compatible checks if the host is Linux and compatible with APT.
func (p *Plugin) Compatible(h plugin.HostFacts) bool {
	if h.OSFamily != "" && h.OSFamily != "linux" {
		return false
	}
	if h.ActivePackage != "" && h.ActivePackage != PluginID {
		return false
	}
	return true
}

// Requirements returns required dependencies for APT management.
func (p *Plugin) Requirements() plugin.Requirements {
	return plugin.Requirements{
		HostPackages: map[string][]string{
			"apt": {"apt", "python3-apt"},
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

// CategorizedSettingsPage declares datacenter-wide APT package management defaults.
func (p *Plugin) CategorizedSettingsPage() newapi.CategorizedSettingsPage {
	meta := p.Meta()
	return settingsframework.NewPageBuilder(SettingsPageID, "APT / Dpkg Package Defaults").
		WithIcon(meta.Icon).
		WithCategory(string(meta.Kind)).
		WithScope(newapi.SettingsScopeDatacenter).
		WithOrder(meta.Order).
		WithDescription("Datacenter-wide APT package upgrade strategy, cache refresh policy, recommended dependencies, and repository proxy settings.").
		BoolField(
			FieldUpdateCacheDefault,
			"Refresh APT Cache Before Updates",
			true,
			settingsframework.WithDescription("Run apt-get update before applying package upgrades."),
		).
		SelectField(
			FieldUpgradeStrategy,
			"Default System Upgrade Mode",
			"dist",
			[]plugin.SettingsFieldOption{
				{Value: "dist", Label: "Distribution Upgrade (dist)"},
				{Value: "full", Label: "Full Upgrade (full)"},
				{Value: "safe", Label: "Safe Upgrade (safe)"},
				{Value: "yes", Label: "Standard Upgrade (yes)"},
			},
			settingsframework.WithDescription("Default ansible.builtin.apt upgrade strategy used when updating all packages ('*')."),
		).
		BoolField(
			FieldInstallRecommends,
			"Install Recommended Packages",
			true,
			settingsframework.WithDescription("Include Debian/Ubuntu recommended packages during package installation."),
		).
		BoolField(
			FieldAutoAutoremove,
			"Autoremove Unused Dependencies",
			false,
			settingsframework.WithDescription("Automatically remove unused dependency packages after APT transactions."),
		).
		BoolField(
			FieldSecurityOnly,
			"Prioritize Security Pocket Updates",
			false,
			settingsframework.WithDescription("Highlight and prioritize security pocket advisories during update checks."),
		).
		StringField(
			FieldHoldPackages,
			"Pinned / Held Packages",
			"",
			settingsframework.WithPlaceholder("linux-image-*, docker-ce"),
			settingsframework.WithDescription("Comma- or space-separated package names or globs held back from automated upgrades."),
		).
		StringField(
			FieldProxyURL,
			"APT HTTP/HTTPS Proxy URL",
			"",
			settingsframework.WithPlaceholder("http://apt-cacher.internal:3142"),
			settingsframework.WithDescription("Optional HTTP/HTTPS proxy or apt-cacher-ng URL for APT repository traffic."),
		).
		SecretField(
			FieldProxyPasswordSecret,
			"APT Proxy Password Vault Variable",
			settingsframework.WithPlaceholder("vault_apt_proxy_password"),
			settingsframework.WithDescription("Ansible Vault variable name holding the APT proxy password. Never stored or returned in plaintext."),
		).
		BuildCategorized()
}

// SettingsPage returns the underlying plugin.SettingsPage for legacy settings consumers.
func (p *Plugin) SettingsPage() plugin.SettingsPage {
	return p.CategorizedSettingsPage().SettingsPage
}

// DefaultUpgradeStrategy returns the persisted datacenter APT upgrade mode or "dist".
func DefaultUpgradeStrategy() string {
	return settingsframework.NewStoreAdapter(nil).StringOrDefault(SettingsPageID, FieldUpgradeStrategy, "dist")
}

// DefaultUpdateCache returns whether APT cache refresh is enabled by default.
func DefaultUpdateCache() bool {
	return settingsframework.NewStoreAdapter(nil).BoolOrDefault(SettingsPageID, FieldUpdateCacheDefault, true)
}

// Probe checks if apt-get is installed on the target host.
func (p *Plugin) Probe(ctx context.Context, r plugin.Runner, h plugin.HostFacts) (plugin.Detection, error) {
	if r == nil {
		return plugin.Detection{
			Detected: false,
			Status:   "OFFLINE",
			Error:    "runner is nil",
		}, nil
	}

	cmd := "command -v apt-get"
	out, err := r.RunCommand(ctx, cmd)
	if err != nil || strings.TrimSpace(out) == "" {
		return plugin.Detection{
			Detected: false,
			Status:   "UNAVAILABLE",
			Error:    fmt.Sprintf("apt probe failed: %v", err),
		}, nil
	}

	return plugin.Detection{
		Detected: true,
		Status:   "ONLINE",
		Details:  strings.TrimSpace(out),
	}, nil
}

// PackageUpdateEntry represents a single available package update parsed from
// `apt-get -s -o Debug::NoLocking=true upgrade` or `apt list --upgradable`.
type PackageUpdateEntry struct {
	Name           string `json:"name"`
	CurrentVersion string `json:"currentVersion,omitempty"`
	NewVersion     string `json:"newVersion"`
	Arch           string `json:"arch,omitempty"`
	Repository     string `json:"repository,omitempty"`
	Security       bool   `json:"security"`
}

var (
	aptSimInstRe    = regexp.MustCompile(`^Inst\s+([^\s]+)(?:\s+\[([^\]]+)\])?\s+\(([^\s,]+)(?:\s+([^\[\)]+))?(?:\s+\[([^\]]+)\])?\)`)
	aptUpgradableRe = regexp.MustCompile(`^([^/\s]+)/([^\s]+)\s+([^\s]+)\s+([^\s]+)(?:\s+\[upgradable from:\s*([^\]]+)\])?`)
)

// Read inspects available APT package updates on the target host using a
// non-destructive simulated upgrade (`apt-get -s -o Debug::NoLocking=true upgrade`).
func (p *Plugin) Read(ctx context.Context, r plugin.Runner, h plugin.HostFacts) (any, error) {
	if r == nil {
		return nil, fmt.Errorf("runner is nil")
	}

	cmd := "apt-get -s -o Debug::NoLocking=true upgrade"
	out, err := r.RunCommand(ctx, cmd)
	if err != nil {
		return nil, fmt.Errorf("apt-get simulated upgrade check failed: %w", err)
	}

	updates := ParseAptCheckUpdateOutput(out)
	rebootRequired := false
	for _, u := range updates {
		if isCriticalUpdatePackage(u.Name) {
			rebootRequired = true
			break
		}
	}

	return map[string]any{
		"manager":        "apt",
		"isImmutable":    false,
		"updates":        updates,
		"updatesCount":   len(updates),
		"rebootRequired": rebootRequired,
		"rawOutput":      strings.TrimSpace(out),
	}, nil
}

// ParseAptCheckUpdateOutput parses `apt-get -s upgrade` ("Inst ...") and
// `apt list --upgradable` lines into structured PackageUpdateEntry items.
func ParseAptCheckUpdateOutput(raw string) []PackageUpdateEntry {
	updates := make([]PackageUpdateEntry, 0)
	if strings.TrimSpace(raw) == "" {
		return updates
	}

	lines := strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n")
	for _, rawLine := range lines {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "Listing...") || strings.HasPrefix(line, "Reading package lists...") {
			continue
		}

		if strings.HasPrefix(line, "Inst ") {
			if m := aptSimInstRe.FindStringSubmatch(line); len(m) == 6 {
				name := strings.TrimSpace(m[1])
				currVer := strings.TrimSpace(m[2])
				newVer := strings.TrimSpace(m[3])
				repo := strings.TrimSpace(m[4])
				arch := strings.TrimSpace(m[5])
				isSec := strings.Contains(strings.ToLower(repo), "security")
				updates = append(updates, PackageUpdateEntry{
					Name:           name,
					CurrentVersion: currVer,
					NewVersion:     newVer,
					Arch:           arch,
					Repository:     repo,
					Security:       isSec,
				})
				continue
			}
		}

		if m := aptUpgradableRe.FindStringSubmatch(line); len(m) == 6 {
			name := strings.TrimSpace(m[1])
			repo := strings.TrimSpace(m[2])
			newVer := strings.TrimSpace(m[3])
			arch := strings.TrimSpace(m[4])
			currVer := strings.TrimSpace(m[5])
			isSec := strings.Contains(strings.ToLower(repo), "security")
			updates = append(updates, PackageUpdateEntry{
				Name:           name,
				CurrentVersion: currVer,
				NewVersion:     newVer,
				Arch:           arch,
				Repository:     repo,
				Security:       isSec,
			})
		}
	}

	return updates
}

func isCriticalUpdatePackage(pkgName string) bool {
	lower := strings.ToLower(pkgName)
	for _, critical := range []string{"kernel", "linux", "systemd", "glibc", "libc6", "grub", "shim", "microcode", "dbus", "kmod"} {
		if strings.Contains(lower, critical) {
			return true
		}
	}
	return false
}

// ManagementActions returns structured v2 action specifications for APT package management.
func (p *Plugin) ManagementActions() []newapi.ManagementActionSpec {
	return []newapi.ManagementActionSpec{
		{
			ActionSpec: plugin.ActionSpec{
				Name:        "install",
				Label:       "Install Package",
				Description: "Installs packages using apt",
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
			Dangerous:        false,
			InvalidatesKinds: []plugin.Kind{plugin.KindPackage},
		},
		{
			ActionSpec: plugin.ActionSpec{
				Name:        "update",
				Label:       "Update Packages",
				Description: "Updates packages or whole system using apt",
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
			Dangerous:        false,
			InvalidatesKinds: []plugin.Kind{plugin.KindPackage},
		},
		{
			ActionSpec: plugin.ActionSpec{
				Name:        "remove",
				Label:       "Remove Package",
				Description: "Removes packages using apt",
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
			ConfirmPrompt:    "Remove this package from the host using APT?",
			InvalidatesKinds: []plugin.Kind{plugin.KindPackage},
		},
	}
}

// Actions enumerates mutation actions supported by APT.
func (p *Plugin) Actions() []plugin.ActionSpec {
	mgmt := p.ManagementActions()
	out := make([]plugin.ActionSpec, len(mgmt))
	for i, m := range mgmt {
		out[i] = m.ActionSpec
	}
	return out
}

// ValidateAction validates options for an APT action against its schema.
func (p *Plugin) ValidateAction(h plugin.HostFacts, action string, opts map[string]any) error {
	for _, spec := range p.ManagementActions() {
		if strings.EqualFold(spec.Name, strings.TrimSpace(action)) {
			return newapi.ValidateManagementOptions(spec, opts)
		}
	}
	return fmt.Errorf("unsupported package action: %s", action)
}

// ContextMenuEntries provides node-level APT package management context menu actions for compatible hosts.
func (p *Plugin) ContextMenuEntries(ctx context.Context, h plugin.HostFacts) []newapi.ContextMenuEntry {
	if !p.Compatible(h) {
		return nil
	}
	return newapi.NewContextMenuBuilder(PluginID).
		WithTarget(newapi.TargetNode).
		WithGroup(string(plugin.KindPackage)).
		AddAction("apt-install", "Install Package (APT)", "download", "install", 10).
		AddAction("apt-update", "Update Packages (APT)", "system_update_alt", "update", 20).
		AddDangerAction(
			"apt-remove",
			"Remove Package (APT)",
			"delete",
			"remove",
			"Remove this package from the host using APT?",
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

// Compile compiles an APT action into declarative TaskSpec.
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
		params["upgrade"] = "dist"
	} else {
		params["name"] = name
	}

	step := plugin.TaskStep{
		Name:   fmt.Sprintf("APT %s: %s", action, name),
		Module: "ansible.builtin.apt",
		Params: params,
	}

	return plugin.TaskSpec{
		Name:  fmt.Sprintf("APT package %s (%s)", action, name),
		Steps: []plugin.TaskStep{step},
	}, nil
}
