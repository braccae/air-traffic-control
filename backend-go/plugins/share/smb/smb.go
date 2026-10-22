package smb

import (
	"context"
	"fmt"
	"strings"

	"air-traffic-control/backend/pkg/plugin"
	"air-traffic-control/backend/pkg/plugin/newapi"
	"air-traffic-control/backend/pkg/plugin/settingsframework"
)

const (
	// PluginID is the canonical identifier for the Samba/SMB share plugin.
	PluginID = "share/smb"
	// SettingsPageID is the route slug of the datacenter-wide Samba settings page.
	SettingsPageID = "share/smb"
	// DefaultWorkgroup is the default Samba workgroup name.
	DefaultWorkgroup = "WORKGROUP"
	// DefaultValidUsers is the default valid users string when none is provided.
	DefaultValidUsers = "core"
	// DefaultReadOnly is the default read-only setting for new Samba shares.
	DefaultReadOnly = false
	// DefaultGuestOk is the default guest access setting for new Samba shares.
	DefaultGuestOk = false
)

// Settings field keys for the Samba datacenter settings page.
const (
	FieldDefaultWorkgroup    = "defaultWorkgroup"
	FieldDefaultValidUsers   = "defaultValidUsers"
	FieldDefaultReadOnly     = "defaultReadOnly"
	FieldDefaultGuestOk      = "defaultGuestOk"
	FieldSmbPasswordVaultVar = "smbPasswordVaultVar"
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

// Plugin implements Samba/SMB file share inspection and configuration.
type Plugin struct{}

func New() *Plugin {
	return &Plugin{}
}

func (p *Plugin) Meta() plugin.Meta {
	return plugin.Meta{
		ID:          PluginID,
		Kind:        plugin.KindShare,
		Name:        "smb",
		Label:       "Samba Shares",
		Icon:        "folder_shared",
		Description: "Samba Windows/CIFS network file sharing service and access permissions",
		Order:       20,
	}
}

func (p *Plugin) Compatible(h plugin.HostFacts) bool {
	return h.OSFamily != "windows" && h.OSFamily != "router_switch"
}

func (p *Plugin) Requirements() plugin.Requirements {
	return plugin.Requirements{
		HostPackages: map[string][]string{
			"dnf": {"samba"},
			"apt": {"samba"},
			"apk": {"samba"},
		},
		ControllerCollections: []string{
			"ansible.builtin",
		},
	}
}

// CollectionSchedule returns the standard polling and cache TTL schedule (60s)
// for Samba share telemetry inspection.
func (p *Plugin) CollectionSchedule() newapi.CollectionSchedule {
	return newapi.DefaultScheduleForKind(p.Meta().Kind)
}

// CollectionSchema documents the v2 payload structure emitted by Read.
func (p *Plugin) CollectionSchema() newapi.CollectionSchema {
	return newapi.CollectionSchema{
		Version:     "v2",
		RootKey:     "samba",
		Description: p.Meta().Description,
	}
}

// CategorizedSettingsPage declares datacenter-wide Samba/SMB share defaults via
// the v2 settings framework.
func (p *Plugin) CategorizedSettingsPage() newapi.CategorizedSettingsPage {
	meta := p.Meta()
	return settingsframework.NewPageBuilder(SettingsPageID, "Samba Share Defaults").
		WithIcon(meta.Icon).
		WithCategory(string(meta.Kind)).
		WithScope(newapi.SettingsScopeDatacenter).
		WithOrder(meta.Order).
		WithDescription("Datacenter-wide Samba/CIFS workgroup, valid users, and guest access defaults.").
		StringField(
			FieldDefaultWorkgroup,
			"Default Workgroup",
			DefaultWorkgroup,
			settingsframework.WithPlaceholder(DefaultWorkgroup),
			settingsframework.WithDescription("Default Samba workgroup name for managed CIFS servers."),
		).
		StringField(
			FieldDefaultValidUsers,
			"Default Valid Users",
			DefaultValidUsers,
			settingsframework.WithPlaceholder(DefaultValidUsers),
			settingsframework.WithDescription("Fallback space-separated list of valid users when no users are specified for a share."),
		).
		BoolField(
			FieldDefaultReadOnly,
			"Read-Only Shares by Default",
			DefaultReadOnly,
			settingsframework.WithDescription("Whether newly configured Samba shares default to read-only access."),
		).
		BoolField(
			FieldDefaultGuestOk,
			"Allow Guest Access by Default",
			DefaultGuestOk,
			settingsframework.WithDescription("Whether newly configured Samba shares allow unauthenticated guest access by default."),
		).
		SecretField(
			FieldSmbPasswordVaultVar,
			"Default Samba Password Vault Variable",
			settingsframework.WithPlaceholder("vault_smb_password"),
			settingsframework.WithDescription("Optional Ansible Vault variable holding the default Samba user password."),
		).
		BuildCategorized()
}

// SettingsPage returns the underlying plugin.SettingsPage for legacy consumers.
func (p *Plugin) SettingsPage() plugin.SettingsPage {
	return p.CategorizedSettingsPage().SettingsPage
}

// WorkgroupDefault returns the persisted datacenter default Samba workgroup or DefaultWorkgroup.
func WorkgroupDefault() string {
	return settingsframework.NewStoreAdapter(nil).StringOrDefault(SettingsPageID, FieldDefaultWorkgroup, DefaultWorkgroup)
}

// ValidUsersDefault returns the persisted datacenter default valid users or DefaultValidUsers.
func ValidUsersDefault() string {
	return settingsframework.NewStoreAdapter(nil).StringOrDefault(SettingsPageID, FieldDefaultValidUsers, DefaultValidUsers)
}

// ReadOnlyDefault returns the persisted datacenter read-only default or DefaultReadOnly.
func ReadOnlyDefault() bool {
	return settingsframework.NewStoreAdapter(nil).BoolOrDefault(SettingsPageID, FieldDefaultReadOnly, DefaultReadOnly)
}

// GuestOkDefault returns the persisted datacenter guest access default or DefaultGuestOk.
func GuestOkDefault() bool {
	return settingsframework.NewStoreAdapter(nil).BoolOrDefault(SettingsPageID, FieldDefaultGuestOk, DefaultGuestOk)
}

// SmbPasswordVaultVar returns the configured unmasked vault variable name if set.
func SmbPasswordVaultVar() (string, bool) {
	return settingsframework.NewStoreAdapter(nil).SecretValue(SettingsPageID, FieldSmbPasswordVaultVar)
}

// ContextMenuEntries provides Samba share context menu actions for compatible hosts.
func (p *Plugin) ContextMenuEntries(ctx context.Context, h plugin.HostFacts) []newapi.ContextMenuEntry {
	if !p.Compatible(h) {
		return nil
	}
	meta := p.Meta()
	return newapi.NewContextMenuBuilder(meta.ID).
		WithTarget(newapi.TargetShare).
		WithGroup(string(meta.Kind)).
		AddAction("smb-share", "Configure Samba Share", "folder_shared", "share", 10).
		AddAction("smb-reload", "Reload Samba Shares", "refresh", "reload", 20).
		AddDangerAction(
			"smb-remove",
			"Remove Samba Share",
			"folder_delete",
			"remove",
			"Remove this Samba share configuration from /etc/samba/smb.conf.d?",
			90,
		).
		Build()
}

func (p *Plugin) Probe(ctx context.Context, r plugin.Runner, h plugin.HostFacts) (plugin.Detection, error) {
	if r == nil {
		return plugin.Detection{Detected: false, Status: "OFFLINE", Error: "runner is nil"}, nil
	}

	out, err := r.RunCommand(ctx, "(sudo -n testparm -s 2>/dev/null || testparm -s 2>/dev/null || cat /etc/samba/smb.conf /etc/samba/smb.conf.d/*.conf 2>/dev/null; sudo -n net conf list 2>/dev/null || net conf list 2>/dev/null; sudo -n cat /etc/cockpit/zfs/shares.conf 2>/dev/null) || true")
	if err == nil && strings.TrimSpace(out) != "" && !strings.Contains(out, "NO_SAMBA") {
		for _, line := range strings.Split(out, "\n") {
			t := strings.TrimSpace(line)
			if strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]") {
				sec := strings.ToLower(strings.TrimSpace(t[1 : len(t)-1]))
				if sec != "" && sec != "global" && sec != "homes" && sec != "printers" && sec != "print$" {
					return plugin.Detection{
						Detected: true,
						Status:   "ONLINE",
						Details:  "Samba shares configured",
					}, nil
				}
			}
		}
	}

	svcOut, err := r.RunCommand(ctx, "systemctl is-active smb smbd 2>/dev/null || true")
	if err == nil {
		for _, l := range strings.Split(svcOut, "\n") {
			if strings.TrimSpace(l) == "active" {
				return plugin.Detection{
					Detected: true,
					Status:   "ONLINE",
					Details:  "smbd service active",
				}, nil
			}
		}
	}

	return plugin.Detection{
		Detected: false,
		Status:   "UNAVAILABLE",
		Error:    "neither active Samba shares nor running smbd service found",
	}, nil
}

func (p *Plugin) Read(ctx context.Context, r plugin.Runner, h plugin.HostFacts) (any, error) {
	if r == nil {
		return nil, fmt.Errorf("runner is nil")
	}

	cmd := "sudo -n testparm -s 2>/dev/null || testparm -s 2>/dev/null || cat /etc/samba/smb.conf 2>/dev/null || echo 'NO_SAMBA'; echo '===NET_CONF==='; sudo -n net conf list 2>/dev/null || echo 'NO_NET_CONF'"
	out, err := r.RunCommand(ctx, cmd)
	if err != nil {
		return nil, err
	}

	parts := strings.Split(out, "===NET_CONF===")
	smbRaw := strings.TrimSpace(parts[0])
	netConfRaw := ""
	if len(parts) > 1 {
		netConfRaw = strings.TrimSpace(parts[1])
	}

	return map[string]any{
		"samba":   smbRaw,
		"netConf": netConfRaw,
	}, nil
}

// ManagementActions returns the structured v2 management action specifications for Samba shares.
func (p *Plugin) ManagementActions() []newapi.ManagementActionSpec {
	return []newapi.ManagementActionSpec{
		{
			ActionSpec: plugin.ActionSpec{
				Name:        "share",
				Label:       "Configure Samba Share",
				Description: "Configure a Samba share definition in /etc/samba/smb.conf.d",
				Options: []plugin.OptionSchema{
					{Name: "name", Type: "string", Required: true, Description: "Share name"},
					{Name: "path", Type: "string", Required: true, Description: "Directory path for share"},
					{Name: "users", Type: "string", Required: false, Default: "core", Description: "Valid users (space separated)"},
					{Name: "read_only", Type: "bool", Required: false, Default: false, Description: "Read-only access"},
					{Name: "guest_ok", Type: "bool", Required: false, Default: false, Description: "Allow guest access"},
					{Name: "comment", Type: "string", Required: false, Description: "Share description comment"},
					{Name: "password", Type: "string", Required: false, Secret: true, Description: "Samba user password"},
				},
			},
			Category:         newapi.ActionCategoryConfiguration,
			Idempotent:       true,
			Dangerous:        false,
			InvalidatesKinds: []plugin.Kind{plugin.KindShare},
		},
		{
			ActionSpec: plugin.ActionSpec{
				Name:        "reload",
				Label:       "Reload Samba Shares",
				Description: "Reload active Samba configuration via smbcontrol",
				Options:     []plugin.OptionSchema{},
			},
			Category:         newapi.ActionCategoryMaintenance,
			Idempotent:       true,
			Dangerous:        false,
			InvalidatesKinds: []plugin.Kind{plugin.KindShare},
		},
		{
			ActionSpec: plugin.ActionSpec{
				Name:        "remove",
				Label:       "Remove Samba Share",
				Description: "Remove a Samba share definition from /etc/samba/smb.conf.d and reload",
				Options: []plugin.OptionSchema{
					{Name: "name", Type: "string", Required: true, Description: "Share name to remove"},
				},
			},
			Category:         newapi.ActionCategoryLifecycle,
			Idempotent:       true,
			Dangerous:        true,
			ConfirmPrompt:    "Remove this Samba share configuration from /etc/samba/smb.conf.d?",
			InvalidatesKinds: []plugin.Kind{plugin.KindShare},
		},
	}
}

// Actions projects the legacy ActionSpec slice from ManagementActions.
func (p *Plugin) Actions() []plugin.ActionSpec {
	mgmt := p.ManagementActions()
	specs := make([]plugin.ActionSpec, len(mgmt))
	for i, m := range mgmt {
		specs[i] = m.ActionSpec
	}
	return specs
}

// ValidateAction validates options against the declared ManagementActionSpec.
func (p *Plugin) ValidateAction(h plugin.HostFacts, action string, opts map[string]any) error {
	norm := strings.ToLower(strings.TrimSpace(action))
	switch norm {
	case "smb", "create":
		norm = "share"
	case "delete":
		norm = "remove"
	}

	normalizedOpts := make(map[string]any, len(opts))
	for k, v := range opts {
		normalizedOpts[k] = v
	}
	if _, hasName := normalizedOpts["name"]; !hasName {
		if sn, ok := normalizedOpts["shareName"].(string); ok && strings.TrimSpace(sn) != "" {
			normalizedOpts["name"] = strings.TrimSpace(sn)
		}
	}
	if _, hasPath := normalizedOpts["path"]; !hasPath {
		if sp, ok := normalizedOpts["sharePath"].(string); ok && strings.TrimSpace(sp) != "" {
			normalizedOpts["path"] = strings.TrimSpace(sp)
		}
	}

	for _, spec := range p.ManagementActions() {
		if strings.EqualFold(spec.Name, norm) {
			return newapi.ValidateManagementOptions(spec, normalizedOpts)
		}
	}
	return fmt.Errorf("unsupported smb action: %s", action)
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
	switch norm {
	case "share", "smb", "create":
		return p.compileShare(opts)
	case "reload":
		return plugin.TaskSpec{
			Name: "Reload Samba shares configuration",
			Steps: []plugin.TaskStep{
				{
					Name:   "Reload Samba configuration",
					Module: "ansible.builtin.command",
					Params: map[string]any{
						"cmd":    "smbcontrol all reload-config",
						"become": true,
					},
				},
			},
		}, nil
	case "remove", "delete":
		name, _ := opts["name"].(string)
		if name == "" {
			name, _ = opts["shareName"].(string)
		}
		name = strings.TrimSpace(name)
		if name == "" {
			return plugin.TaskSpec{}, fmt.Errorf("share name is required for remove action")
		}
		return plugin.TaskSpec{
			Name: fmt.Sprintf("Remove share %s", name),
			Steps: []plugin.TaskStep{
				{
					Name:   fmt.Sprintf("Remove Samba share config for %s", name),
					Module: "ansible.builtin.file",
					Params: map[string]any{
						"path":   fmt.Sprintf("/etc/samba/smb.conf.d/%s.conf", name),
						"state":  "absent",
						"become": true,
					},
				},
				{
					Name:   "Reload Samba configuration",
					Module: "ansible.builtin.command",
					Params: map[string]any{
						"cmd":    "smbcontrol all reload-config",
						"become": true,
					},
				},
			},
		}, nil
	default:
		return plugin.TaskSpec{}, fmt.Errorf("unsupported smb action: %s", action)
	}
}

func (p *Plugin) compileShare(opts map[string]any) (plugin.TaskSpec, error) {
	name, _ := opts["name"].(string)
	if name == "" {
		name, _ = opts["shareName"].(string)
	}
	if name == "" {
		name = "share"
	}

	path, _ := opts["path"].(string)
	if path == "" {
		path, _ = opts["sharePath"].(string)
	}
	if path == "" {
		path = "/srv/share"
	}

	var usersStr string
	if uSlice, ok := opts["users"].([]string); ok {
		usersStr = strings.Join(uSlice, " ")
	} else if uSliceAny, ok := opts["users"].([]any); ok {
		var strList []string
		for _, u := range uSliceAny {
			strList = append(strList, fmt.Sprint(u))
		}
		usersStr = strings.Join(strList, " ")
	} else if uStr, ok := opts["users"].(string); ok && uStr != "" {
		usersStr = uStr
	} else if uStr, ok := opts["validUsers"].(string); ok && uStr != "" {
		usersStr = uStr
	} else {
		usersStr = ValidUsersDefault()
	}

	templateParams := map[string]any{
		"src":         "smb.conf.j2",
		"dest":        fmt.Sprintf("/etc/samba/smb.conf.d/%s.conf", name),
		"owner":       "root",
		"group":       "root",
		"mode":        "0644",
		"share_path":  path,
		"valid_users": usersStr,
		"become":      true,
	}

	if ro, ok := opts["read_only"].(bool); ok {
		if ro {
			templateParams["read_only"] = "yes"
		} else {
			templateParams["read_only"] = "no"
		}
	} else if ro, ok := opts["readOnly"].(bool); ok {
		if ro {
			templateParams["read_only"] = "yes"
		} else {
			templateParams["read_only"] = "no"
		}
	}

	if g, ok := opts["guest_ok"].(bool); ok {
		if g {
			templateParams["guest_ok"] = "yes"
		} else {
			templateParams["guest_ok"] = "no"
		}
	} else if g, ok := opts["guestOk"].(bool); ok {
		if g {
			templateParams["guest_ok"] = "yes"
		} else {
			templateParams["guest_ok"] = "no"
		}
	}

	if c, ok := opts["comment"].(string); ok && c != "" {
		templateParams["comment"] = c
	}

	secretsMasked := make(map[string]string)
	if vaultKey, ok := opts["passwordVaultKey"].(string); ok && vaultKey != "" {
		secretsMasked["smb_password"] = fmt.Sprintf("•••••••• (vault: %s)", vaultKey)
	} else if pwd, ok := opts["password"].(string); ok && pwd != "" {
		secretsMasked["smb_password"] = "••••••••"
	} else if defaultVaultKey, hasDefaultVault := SmbPasswordVaultVar(); hasDefaultVault && defaultVaultKey != "" {
		secretsMasked["smb_password"] = fmt.Sprintf("•••••••• (vault: %s)", defaultVaultKey)
	}

	step := plugin.TaskStep{
		Name:   fmt.Sprintf("Configure share %s at %s", name, path),
		Module: "ansible.builtin.template",
		Params: templateParams,
	}

	task := plugin.TaskSpec{
		Name:  fmt.Sprintf("Configure share %s", name),
		Steps: []plugin.TaskStep{step},
	}
	if len(secretsMasked) > 0 {
		task.SecretsMasked = secretsMasked
	}

	return task, nil
}
