package apk

import (
	"context"
	"fmt"
	"strings"

	"air-traffic-control/backend/pkg/plugin"
	"air-traffic-control/backend/pkg/plugin/newapi"
	"air-traffic-control/backend/pkg/plugin/settingsframework"
)

const (
	// PluginID is the canonical identifier for the APK package manager plugin.
	PluginID = "package/apk"
	// SettingsPageID is the settings page identifier for datacenter APK defaults.
	SettingsPageID = "package/apk"
)

// Settings field keys for the APK datacenter settings page.
const (
	FieldUpdateCacheDefault  = "updateCacheDefault"
	FieldNoCache             = "noCache"
	FieldAvailableUpgrade    = "availableUpgrade"
	FieldDefaultRepositories = "defaultRepositories"
	FieldWorldFilePath       = "worldFilePath"
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
// extension contracts for APK (Alpine Linux) package management.
type Plugin struct{}

// New returns a new APK package plugin.
func New() *Plugin {
	return &Plugin{}
}

// Meta returns metadata for APK.
func (p *Plugin) Meta() plugin.Meta {
	return plugin.Meta{
		ID:          PluginID,
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
	if h.ActivePackage != "" && h.ActivePackage != PluginID {
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

// CategorizedSettingsPage declares datacenter-wide APK package management defaults.
func (p *Plugin) CategorizedSettingsPage() newapi.CategorizedSettingsPage {
	meta := p.Meta()
	return settingsframework.NewPageBuilder(SettingsPageID, "APK (Alpine) Package Defaults").
		WithIcon(meta.Icon).
		WithCategory(string(meta.Kind)).
		WithScope(newapi.SettingsScopeDatacenter).
		WithOrder(meta.Order).
		WithDescription("Datacenter-wide Alpine APK package update policies, repository index caching, world file path, and proxy settings.").
		BoolField(
			FieldUpdateCacheDefault,
			"Refresh Repository Index Before Updates",
			true,
			settingsframework.WithDescription("Refresh the APK repository index (apk update) before applying package updates."),
		).
		BoolField(
			FieldNoCache,
			"Do Not Cache Package Index Locally (--no-cache)",
			false,
			settingsframework.WithDescription("Read package indices directly from the network without storing a local cache copy."),
		).
		BoolField(
			FieldAvailableUpgrade,
			"Reset Pinned Packages on Full Upgrade (--available)",
			false,
			settingsframework.WithDescription("Prefer versions available in current repositories when upgrading across Alpine releases."),
		).
		StringField(
			FieldDefaultRepositories,
			"Additional APK Repository URLs",
			"",
			settingsframework.WithPlaceholder("https://dl-cdn.alpinelinux.org/alpine/edge/community"),
			settingsframework.WithDescription("Optional comma-separated extra Alpine repository URLs passed to APK operations."),
		).
		StringField(
			FieldWorldFilePath,
			"APK World File Path",
			"/etc/apk/world",
			settingsframework.WithPlaceholder("/etc/apk/world"),
			settingsframework.WithDescription("Path to the Alpine APK world file tracking explicitly installed packages."),
		).
		StringField(
			FieldProxyURL,
			"Repository HTTP/HTTPS Proxy URL",
			"",
			settingsframework.WithPlaceholder("http://proxy.internal:3128"),
			settingsframework.WithDescription("Optional HTTP/HTTPS proxy URL for Alpine APK repository traffic."),
		).
		SecretField(
			FieldProxyPasswordSecret,
			"Repository Proxy Password Vault Variable",
			settingsframework.WithPlaceholder("vault_apk_proxy_password"),
			settingsframework.WithDescription("Ansible Vault variable name holding the repository proxy password."),
		).
		BuildCategorized()
}

// SettingsPage returns the underlying plugin.SettingsPage for legacy settings consumers.
func (p *Plugin) SettingsPage() plugin.SettingsPage {
	return p.CategorizedSettingsPage().SettingsPage
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

// PackageUpdateEntry represents a single available package update parsed from `apk version -v -l '<'`.
type PackageUpdateEntry struct {
	Name           string `json:"name"`
	CurrentVersion string `json:"currentVersion,omitempty"`
	NewVersion     string `json:"newVersion"`
	Security       bool   `json:"security,omitempty"`
}

// Read inspects available APK package updates on the target host via `apk version -v -l '<'`.
func (p *Plugin) Read(ctx context.Context, r plugin.Runner, h plugin.HostFacts) (any, error) {
	if r == nil {
		return nil, fmt.Errorf("runner is nil")
	}

	cmd := "apk version -v -l '<'"
	out, err := r.RunCommand(ctx, cmd)
	if err != nil {
		return nil, fmt.Errorf("apk version check failed: %w", err)
	}

	updates := parseApkVersionOutput(out)
	rebootRequired := false
	for _, u := range updates {
		if isCriticalUpdatePackage(u.Name) {
			rebootRequired = true
			break
		}
	}

	return map[string]any{
		"manager":        "apk",
		"isImmutable":    false,
		"updates":        updates,
		"updatesCount":   len(updates),
		"rebootRequired": rebootRequired,
		"rawOutput":      strings.TrimSpace(out),
	}, nil
}

func parseApkVersionOutput(raw string) []PackageUpdateEntry {
	updates := make([]PackageUpdateEntry, 0)
	if strings.TrimSpace(raw) == "" {
		return updates
	}

	lines := strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n")
	for _, rawLine := range lines {
		line := strings.TrimSpace(rawLine)
		if line == "" || !strings.Contains(line, "<") {
			continue
		}
		if strings.HasPrefix(line, "Installed:") || strings.HasPrefix(line, "WARNING:") {
			continue
		}

		parts := strings.SplitN(line, "<", 2)
		if len(parts) < 2 {
			continue
		}
		left := strings.TrimSpace(parts[0])
		right := strings.TrimSpace(parts[1])
		if left == "" || right == "" {
			continue
		}
		// Strip trailing repository tag annotations if present (e.g. "1.2.4-r2 ")
		if fields := strings.Fields(right); len(fields) > 0 {
			right = fields[0]
		}

		name, currentVer := splitApkNameVersion(left)
		if name == "" {
			continue
		}

		isSec := isCriticalUpdatePackage(name) ||
			strings.Contains(strings.ToLower(name), "ssl") ||
			strings.Contains(strings.ToLower(name), "crypto")

		updates = append(updates, PackageUpdateEntry{
			Name:           name,
			CurrentVersion: currentVer,
			NewVersion:     right,
			Security:       isSec,
		})
	}
	return updates
}

// splitApkNameVersion splits an Alpine package-version identifier such as
// "musl-1.2.4_git20230717-r1" or "busybox-extras-1.36.1-r4" into its package
// name and current version by locating the last hyphen followed by a digit.
func splitApkNameVersion(pkgVer string) (string, string) {
	lastIdx := -1
	for i := 0; i < len(pkgVer)-1; i++ {
		if pkgVer[i] == '-' && pkgVer[i+1] >= '0' && pkgVer[i+1] <= '9' {
			lastIdx = i
		}
	}
	if lastIdx > 0 {
		return pkgVer[:lastIdx], pkgVer[lastIdx+1:]
	}
	return pkgVer, ""
}

func isCriticalUpdatePackage(pkgName string) bool {
	lower := strings.ToLower(pkgName)
	for _, critical := range []string{"kernel", "linux", "systemd", "glibc", "musl", "grub", "shim", "microcode", "dbus", "kmod"} {
		if strings.Contains(lower, critical) {
			return true
		}
	}
	return false
}

// ManagementActions returns structured v2 action specifications for APK package management.
func (p *Plugin) ManagementActions() []newapi.ManagementActionSpec {
	return []newapi.ManagementActionSpec{
		{
			ActionSpec: plugin.ActionSpec{
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
			Category:         newapi.ActionCategoryLifecycle,
			Idempotent:       true,
			InvalidatesKinds: []plugin.Kind{plugin.KindPackage},
		},
		{
			ActionSpec: plugin.ActionSpec{
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
			Category:         newapi.ActionCategoryMaintenance,
			Idempotent:       true,
			InvalidatesKinds: []plugin.Kind{plugin.KindPackage},
		},
		{
			ActionSpec: plugin.ActionSpec{
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
			Category:         newapi.ActionCategoryLifecycle,
			Idempotent:       true,
			Dangerous:        true,
			ConfirmPrompt:    "Remove this package from the host using APK?",
			InvalidatesKinds: []plugin.Kind{plugin.KindPackage},
		},
	}
}

// Actions enumerates mutation actions supported by APK.
func (p *Plugin) Actions() []plugin.ActionSpec {
	mgmt := p.ManagementActions()
	out := make([]plugin.ActionSpec, len(mgmt))
	for i, m := range mgmt {
		out[i] = m.ActionSpec
	}
	return out
}

// ValidateAction validates options for an APK action against its schema.
func (p *Plugin) ValidateAction(h plugin.HostFacts, action string, opts map[string]any) error {
	for _, spec := range p.ManagementActions() {
		if strings.EqualFold(spec.Name, strings.TrimSpace(action)) {
			return newapi.ValidateManagementOptions(spec, opts)
		}
	}
	return fmt.Errorf("unsupported package action: %s", action)
}

// ContextMenuEntries provides node-level APK package management context menu actions for compatible hosts.
func (p *Plugin) ContextMenuEntries(ctx context.Context, h plugin.HostFacts) []newapi.ContextMenuEntry {
	if !p.Compatible(h) {
		return nil
	}
	return newapi.NewContextMenuBuilder(PluginID).
		WithTarget(newapi.TargetNode).
		WithGroup(string(plugin.KindPackage)).
		AddAction("apk-install", "Install Package (APK)", "download", "install", 10).
		AddAction("apk-update", "Update Packages (APK)", "system_update_alt", "update", 20).
		AddDangerAction(
			"apk-remove",
			"Remove Package (APK)",
			"delete",
			"remove",
			"Remove this package from the host using APK?",
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
