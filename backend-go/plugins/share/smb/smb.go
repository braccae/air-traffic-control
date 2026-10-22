package smb

import (
	"context"
	"fmt"
	"strings"

	"air-traffic-control/backend/pkg/plugin"
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
		ID:          "share/smb",
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

func (p *Plugin) Actions() []plugin.ActionSpec {
	return []plugin.ActionSpec{
		{
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
	}
}

func (p *Plugin) Compile(h plugin.HostFacts, action string, opts map[string]any) (plugin.TaskSpec, error) {
	norm := strings.ToLower(action)
	if norm != "share" && norm != "smb" {
		return plugin.TaskSpec{}, fmt.Errorf("unsupported smb action: %s", action)
	}

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
		usersStr = "core"
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
