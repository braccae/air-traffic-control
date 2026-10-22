package nfs

import (
	"context"
	"fmt"
	"strings"

	"air-traffic-control/backend/pkg/plugin"
	"air-traffic-control/backend/pkg/plugin/newapi"
	"air-traffic-control/backend/pkg/plugin/settingsframework"
)

// SettingsPageID is the canonical settings page route slug for the NFS share plugin.
const SettingsPageID = "share/nfs"

// Default export configuration values when not overridden per request or in datacenter settings.
const (
	DefaultExportPath    = "/export"
	DefaultExportClient  = "*"
	DefaultExportOptions = "rw,sync,no_subtree_check"
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

// Plugin implements NFS exports table inspection and share management.
type Plugin struct{}

func New() *Plugin {
	return &Plugin{}
}

func (p *Plugin) Meta() plugin.Meta {
	return plugin.Meta{
		ID:          "share/nfs",
		Kind:        plugin.KindShare,
		Name:        "nfs",
		Label:       "NFS Shares",
		Icon:        "folder_shared",
		Description: "Kernel NFS server exports (/etc/exports) and client permissions",
		Order:       10,
	}
}

func (p *Plugin) Compatible(h plugin.HostFacts) bool {
	return h.OSFamily != "windows" && h.OSFamily != "router_switch"
}

func (p *Plugin) Requirements() plugin.Requirements {
	return plugin.Requirements{
		HostPackages: map[string][]string{
			"dnf": {"nfs-utils"},
			"apt": {"nfs-kernel-server"},
			"apk": {"nfs-utils"},
		},
		ControllerCollections: []string{
			"ansible.builtin",
		},
	}
}

// CollectionSchedule returns the standard polling cadence and cache TTL schedule for NFS shares.
func (p *Plugin) CollectionSchedule() newapi.CollectionSchedule {
	return newapi.DefaultScheduleForKind(p.Meta().Kind)
}

// CollectionSchema documents the v2 payload structure emitted by Read.
func (p *Plugin) CollectionSchema() newapi.CollectionSchema {
	return newapi.CollectionSchema{
		Version:     "v2",
		RootKey:     "nfs",
		Description: p.Meta().Description,
	}
}

// CategorizedSettingsPage declares datacenter-wide NFS export defaults via settingsframework.
func (p *Plugin) CategorizedSettingsPage() newapi.CategorizedSettingsPage {
	meta := p.Meta()
	return settingsframework.NewPageBuilder(meta.ID, "NFS Shares Defaults").
		WithIcon(meta.Icon).
		WithCategory(string(meta.Kind)).
		WithScope(newapi.SettingsScopeDatacenter).
		WithOrder(meta.Order).
		WithDescription("Datacenter-wide kernel NFS server export path, client CIDR, and mount option defaults.").
		StringField("defaultPath", "Default Export Base Directory", DefaultExportPath,
			settingsframework.WithPlaceholder(DefaultExportPath),
			settingsframework.WithDescription("Fallback directory path used when configuring an NFS export without an explicit path.")).
		StringField("defaultClient", "Default Client Specification", DefaultExportClient,
			settingsframework.WithPlaceholder("192.168.1.0/24 or *"),
			settingsframework.WithDescription("Default client CIDR, hostname, or wildcard applied to new NFS exports.")).
		StringField("defaultOptions", "Default Export Options", DefaultExportOptions,
			settingsframework.WithPlaceholder(DefaultExportOptions),
			settingsframework.WithDescription("Default comma-separated NFS export flags written to /etc/exports.")).
		SelectField("nfsVersion", "Preferred NFS Protocol Version", "4", []plugin.SettingsFieldOption{
			{Value: "4", Label: "NFSv4 (4.1 / 4.2)"},
			{Value: "3", Label: "NFSv3"},
		}, settingsframework.WithDescription("Preferred NFS protocol version across managed Linux storage nodes.")).
		BoolField("rootSquash", "Enable Root Squash by Default", true,
			settingsframework.WithDescription("Map remote root UID/GID 0 to anonymous nobody/nogroup by default.")).
		BuildCategorized()
}

// SettingsPage returns the underlying plugin.SettingsPage for legacy consumers.
func (p *Plugin) SettingsPage() plugin.SettingsPage {
	return p.CategorizedSettingsPage().SettingsPage
}

func defaultExportPath() string {
	return settingsframework.NewStoreAdapter(nil).StringOrDefault(SettingsPageID, "defaultPath", DefaultExportPath)
}

func defaultExportClient() string {
	return settingsframework.NewStoreAdapter(nil).StringOrDefault(SettingsPageID, "defaultClient", DefaultExportClient)
}

func defaultExportOptions() string {
	return settingsframework.NewStoreAdapter(nil).StringOrDefault(SettingsPageID, "defaultOptions", DefaultExportOptions)
}

// ContextMenuEntries provides NFS share context menu actions for compatible hosts.
func (p *Plugin) ContextMenuEntries(ctx context.Context, h plugin.HostFacts) []newapi.ContextMenuEntry {
	if !p.Compatible(h) {
		return nil
	}
	meta := p.Meta()
	return newapi.NewContextMenuBuilder(meta.ID).
		WithTarget(newapi.TargetShare).
		WithGroup(string(meta.Kind)).
		AddAction("export", "Configure NFS Export", meta.Icon, "export", 10).
		Build()
}

func (p *Plugin) Probe(ctx context.Context, r plugin.Runner, h plugin.HostFacts) (plugin.Detection, error) {
	if r == nil {
		return plugin.Detection{Detected: false, Status: "OFFLINE", Error: "runner is nil"}, nil
	}

	out, err := r.RunCommand(ctx, "cat /var/lib/nfs/etab /etc/exports /etc/exports.d/*.exports 2>/dev/null || true")
	if err == nil && strings.TrimSpace(out) != "" {
		lines := strings.Split(strings.TrimSpace(out), "\n")
		var valid int
		for _, l := range lines {
			l = strings.TrimSpace(l)
			if l != "" && !strings.HasPrefix(l, "#") {
				valid++
			}
		}
		if valid > 0 {
			return plugin.Detection{
				Detected: true,
				Status:   fmt.Sprintf("%d export(s)", valid),
				Details:  "Active NFS exports found",
			}, nil
		}
	}

	svcOut, err := r.RunCommand(ctx, "systemctl is-active nfs-server 2>/dev/null || true")
	if err == nil && strings.TrimSpace(svcOut) == "active" {
		return plugin.Detection{
			Detected: true,
			Status:   "ONLINE",
			Details:  "nfs-server active, no exports configured",
		}, nil
	}

	return plugin.Detection{
		Detected: false,
		Status:   "UNAVAILABLE",
		Error:    "no active NFS exports or nfs-server service found",
	}, nil
}

func (p *Plugin) Read(ctx context.Context, r plugin.Runner, h plugin.HostFacts) (any, error) {
	if r == nil {
		return nil, fmt.Errorf("runner is nil")
	}

	cmd := "cat /var/lib/nfs/etab /etc/exports 2>/dev/null || echo 'NO_NFS'"
	out, err := r.RunCommand(ctx, cmd)
	if err != nil {
		return nil, err
	}

	return map[string]any{
		"nfs": strings.TrimSpace(out),
	}, nil
}

// ManagementActions returns the v2 management action specifications for NFS shares.
func (p *Plugin) ManagementActions() []newapi.ManagementActionSpec {
	return []newapi.ManagementActionSpec{
		{
			ActionSpec: plugin.ActionSpec{
				Name:        "export",
				Label:       "Configure NFS Export",
				Description: "Add or update an NFS export line in /etc/exports and reload",
				Options: []plugin.OptionSchema{
					{Name: "path", Type: "string", Required: true, Description: "Directory path to export (e.g. /export/data)"},
					{Name: "client", Type: "string", Required: false, Default: "*", Description: "Client CIDR, hostname, or *"},
					{Name: "options", Type: "string", Required: false, Default: "rw,sync,no_subtree_check", Description: "Export options"},
					{Name: "state", Type: "select", Enum: []string{"present", "absent"}, Default: "present", Description: "Export presence state"},
				},
			},
			Category:         newapi.ActionCategoryConfiguration,
			Idempotent:       true,
			Dangerous:        false,
			InvalidatesKinds: []plugin.Kind{plugin.KindShare},
		},
	}
}

func (p *Plugin) Actions() []plugin.ActionSpec {
	mgmt := p.ManagementActions()
	specs := make([]plugin.ActionSpec, len(mgmt))
	for i, m := range mgmt {
		specs[i] = m.ActionSpec
	}
	return specs
}

func normalizeActionName(action string) string {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "export", "nfs":
		return "export"
	default:
		return strings.ToLower(strings.TrimSpace(action))
	}
}

// ValidateAction validates options against the declared ManagementActionSpec.
func (p *Plugin) ValidateAction(h plugin.HostFacts, action string, opts map[string]any) error {
	canonical := normalizeActionName(action)
	for _, spec := range p.ManagementActions() {
		if strings.EqualFold(spec.Name, canonical) {
			return newapi.ValidateManagementOptions(spec, opts)
		}
	}
	return fmt.Errorf("unsupported nfs action: %s", action)
}

// CompileTask adapts a typed v2 TaskRequest to the plugin's declarative Ansible TaskSpec compiler.
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

func (p *Plugin) Compile(h plugin.HostFacts, action string, opts map[string]any) (plugin.TaskSpec, error) {
	norm := strings.ToLower(strings.TrimSpace(action))
	if norm != "export" && norm != "nfs" {
		return plugin.TaskSpec{}, fmt.Errorf("unsupported nfs action: %s", action)
	}

	path, _ := opts["path"].(string)
	if path == "" {
		path = defaultExportPath()
	}
	client, _ := opts["client"].(string)
	if client == "" {
		client = defaultExportClient()
	}
	options, _ := opts["options"].(string)
	if options == "" {
		options = defaultExportOptions()
	}
	state, _ := opts["state"].(string)
	if state == "" {
		state = "present"
	}

	exportLine := fmt.Sprintf("%s %s(%s)", path, client, options)

	steps := []plugin.TaskStep{
		{
			Name:   fmt.Sprintf("Configure NFS export for %s", path),
			Module: "ansible.builtin.lineinfile",
			Params: map[string]any{
				"path":   "/etc/exports",
				"line":   exportLine,
				"state":  state,
				"create": true,
				"mode":   "0644",
				"become": true,
			},
		},
		{
			Name:   "Reload NFS exports table",
			Module: "ansible.builtin.command",
			Params: map[string]any{
				"cmd":    "exportfs -ra",
				"become": true,
			},
		},
	}

	return plugin.TaskSpec{
		Name:  fmt.Sprintf("Configure NFS export at %s", path),
		Steps: steps,
	}, nil
}
