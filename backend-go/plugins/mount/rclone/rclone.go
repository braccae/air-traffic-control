// Package rclone implements the mount/rclone plugin: rclone remote detection,
// datacenter-wide global settings (SettingsPageProvider), and declarative
// compilation of the unit/drop-in/file/mount tasks that install a remote on a
// device and expose it as a live mount.
//
// Secrets never reach this plugin in plaintext. The coordinator decrypts a remote
// from ATC's Ansible Vault store (vault_variables) with Vault Audit logging,
// converts secret fields to rclone's own obscured form (rclone obscure), and
// passes the resulting rclone.conf section as opaque text. This plugin writes
// that section with ansible.builtin.blockinfile guarded by no_log, so neither unit
// files, drop-ins, nor Ansible logs ever contain rclone credentials.
package rclone

import (
	"context"
	"fmt"
	"strings"

	"air-traffic-control/backend/pkg/plugin"
	"air-traffic-control/backend/pkg/systemd"
)

func init() {
	_ = plugin.Register(New())
}

// SettingsPageID is the route slug of the datacenter-wide rclone settings page
// contributed by this module.
const SettingsPageID = "mount/rclone"

// Defaults for rclone mounts when per-install overrides are omitted.
const (
	DefaultMountBaseDir = "/mnt/rclone"
	DefaultMountPolicy  = "systemd-mount"
	DefaultMountOptions = "--vfs-cache-mode writes --dir-cache-time 5m"
	DefaultMountUser    = "root"
)

// Plugin provides rclone remote inspection, global defaults, and mount installation.
type Plugin struct{}

// New constructs a new rclone plugin instance.
func New() *Plugin { return &Plugin{} }

func (p *Plugin) Meta() plugin.Meta {
	return plugin.Meta{
		ID:          "mount/rclone",
		Kind:        plugin.KindMount,
		Name:        "rclone",
		Label:       "Rclone Mounts",
		Icon:        "cloud_queue",
		Description: "Mount cloud and remote storage on the host via rclone (systemd / fstab / script)",
		Order:       10,
	}
}

// SettingsPage implements plugin.SettingsPageProvider so datacenter-wide rclone
// defaults and remote management appear in the Datacenter Settings view and the
// global settings framework.
func (p *Plugin) SettingsPage() plugin.SettingsPage {
	return plugin.SettingsPage{
		ID:          SettingsPageID,
		Title:       "Rclone Remotes & Mount Defaults",
		Icon:        "cloud_queue",
		Description: "Datacenter-wide rclone remote defaults (base mount path, default mount strategy, VFS cache flags) and guided rclone.conf remote vault storage.",
		Order:       25,
		Fields: []plugin.SettingsField{
			{
				Name:        "defaultMountBaseDir",
				Label:       "Default Mount Base Directory",
				Type:        plugin.SettingsFieldString,
				Default:     DefaultMountBaseDir,
				Required:    true,
				Placeholder: DefaultMountBaseDir,
				Description: "Base directory under which remotes mount by default (e.g. /mnt/rclone/<remote>).",
			},
			{
				Name:        "defaultPolicy",
				Label:       "Default Client Mount Policy",
				Type:        plugin.SettingsFieldSelect,
				Default:     DefaultMountPolicy,
				Description: "Default mount strategy used when installing a remote to a Linux client.",
				Options: []plugin.SettingsFieldOption{
					{Value: "systemd-mount", Label: "systemd mount (rclone-<name>.service)"},
					{Value: "systemd-automount", Label: "systemd automount (.mount + .automount pair)"},
					{Value: "fstab", Label: "/etc/fstab entry (fuse.rclone)"},
					{Value: "script", Label: "Service script (/usr/local/bin/rclone-mount-<name>.sh)"},
				},
			},
			{
				Name:        "defaultMountOptions",
				Label:       "Default rclone Mount Flags",
				Type:        plugin.SettingsFieldString,
				Default:     DefaultMountOptions,
				Placeholder: DefaultMountOptions,
				Description: "Extra flags passed to `rclone mount` when a client does not specify custom options.",
			},
			{
				Name:        "defaultMountUser",
				Label:       "Default Mount Owner User",
				Type:        plugin.SettingsFieldString,
				Default:     DefaultMountUser,
				Placeholder: DefaultMountUser,
				Description: "System user that owns the FUSE mount process on target hosts.",
			},
			{
				Name:        "configPasswordVaultVar",
				Label:       "rclone.conf Encryption Password Vault Variable",
				Type:        plugin.SettingsFieldSecret,
				Secret:      true,
				Placeholder: "vault_rclone_config_password",
				Description: "Optional Ansible Vault variable holding RCLONE_CONFIG_PASS. Never stored or returned in plaintext.",
			},
		},
	}
}

func globalSetting(field, fallback string) string {
	store := plugin.GlobalSettingsStore()
	if store == nil {
		return fallback
	}
	if v, ok := store.Value(SettingsPageID, field); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return fallback
}

func (p *Plugin) Compatible(h plugin.HostFacts) bool {
	// FUSE mounts are a Linux concept; rclone mount is not usable on Windows or
	// router/switch targets.
	return h.OSFamily == "linux"
}

func (p *Plugin) Requirements() plugin.Requirements {
	return plugin.Requirements{
		HostPackages: map[string][]string{
			"dnf": {"rclone", "fuse"},
			"apt": {"rclone", "fuse3"},
			"apk": {"rclone", "fuse"},
		},
		ControllerCollections: []string{"ansible.builtin", "ansible.posix", "community.general"},
	}
}

func (p *Plugin) Probe(ctx context.Context, r plugin.Runner, h plugin.HostFacts) (plugin.Detection, error) {
	if r == nil {
		return plugin.Detection{Detected: false, Status: "OFFLINE", Error: "runner is nil"}, nil
	}

	out, err := r.RunCommand(ctx, "command -v rclone")
	if err != nil || strings.TrimSpace(out) == "" {
		return plugin.Detection{
			Detected: false,
			Status:   "UNAVAILABLE",
			Error:    "rclone binary not found",
		}, nil
	}
	bin := strings.TrimSpace(out)

	// Report the count of active rclone mounts honestly; absence of any mount is
	// not an error, it is simply zero.
	mounts := 0
	if mout, merr := r.RunCommand(ctx, "mount 2>/dev/null | grep -c 'fuse.rclone' || true"); merr == nil {
		if n := strings.TrimSpace(mout); n != "" {
			mounts = atoiSafe(n)
		}
	}

	status := "ONLINE"
	if mounts > 0 {
		status = fmt.Sprintf("%d mount(s)", mounts)
	}

	return plugin.Detection{
		Detected: true,
		Status:   status,
		Details:  fmt.Sprintf("rclone at %s", bin),
	}, nil
}

func (p *Plugin) Read(ctx context.Context, r plugin.Runner, h plugin.HostFacts) (any, error) {
	if r == nil {
		return nil, fmt.Errorf("runner is nil")
	}

	remotes := []string{}
	if out, err := r.RunCommand(ctx, "rclone listremotes 2>/dev/null || true"); err == nil {
		for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
			line = strings.TrimSpace(line)
			if line != "" {
				remotes = append(remotes, strings.TrimSuffix(line, ":"))
			}
		}
	}

	mountLines := []string{}
	if out, err := r.RunCommand(ctx, "mount 2>/dev/null | grep 'fuse.rclone' || true"); err == nil {
		for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
			if strings.TrimSpace(line) != "" {
				mountLines = append(mountLines, strings.TrimSpace(line))
			}
		}
	}

	return map[string]any{
		"remotes": remotes,
		"mounts":  mountLines,
	}, nil
}

func (p *Plugin) Actions() []plugin.ActionSpec {
	return []plugin.ActionSpec{
		{
			Name:        "install_remote",
			Label:       "Install Remote",
			Description: "Write an rclone remote config to the device and install its mount using the chosen policy",
			Options: []plugin.OptionSchema{
				{Name: "name", Type: "string", Required: true, Description: "rclone remote name (e.g. hetzner)"},
				{Name: "configContent", Type: "string", Required: true, Secret: true, Description: "rclone.conf section (obscured secret values only)"},
				{Name: "mountTarget", Type: "string", Required: true, Description: "Absolute mount point (e.g. /mnt/rclone/hetzner)"},
				{Name: "policy", Type: "select", Required: false, Default: "systemd-mount", Enum: []string{"systemd-mount", "systemd-automount", "fstab", "script"}, Description: "Mount strategy"},
				{Name: "mountOptions", Type: "string", Required: false, Default: "--vfs-cache-mode writes --dir-cache-time 5m", Description: "Extra rclone mount arguments"},
				{Name: "mountUser", Type: "string", Required: false, Default: "root", Description: "User that owns the FUSE mount"},
				{Name: "scope", Type: "select", Required: false, Default: "system", Enum: []string{"system", "user"}, Description: "systemd manager scope"},
			},
		},
		{
			Name:        "remove_remote",
			Label:       "Remove Remote",
			Description: "Unmount and remove a previously installed rclone remote",
			Options: []plugin.OptionSchema{
				{Name: "name", Type: "string", Required: true, Description: "rclone remote name"},
				{Name: "mountTarget", Type: "string", Required: false, Description: "Mount point to unmount"},
				{Name: "policy", Type: "select", Required: false, Default: "systemd-mount", Enum: []string{"systemd-mount", "systemd-automount", "fstab", "script"}, Description: "Mount strategy used at install time"},
				{Name: "scope", Type: "select", Required: false, Default: "system", Enum: []string{"system", "user"}, Description: "systemd manager scope"},
			},
		},
	}
}

func (p *Plugin) Compile(h plugin.HostFacts, action string, opts map[string]any) (plugin.TaskSpec, error) {
	switch strings.ToLower(action) {
	case "install_remote":
		return compileInstall(h, opts)
	case "remove_remote":
		return compileRemove(h, opts)
	default:
		return plugin.TaskSpec{}, fmt.Errorf("unsupported rclone action: %s", action)
	}
}

func str(opts map[string]any, key, def string) string {
	if v, ok := opts[key]; ok {
		if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return def
}

// isNonSystemdHost reports whether the target host uses OpenRC / SysV rather
// than systemd, matching backend-go/plugins/system/service conventions.
func isNonSystemdHost(h plugin.HostFacts) bool {
	if h.Overrides != nil {
		if initSys, ok := h.Overrides["initSystem"]; ok && initSys != "" && initSys != "systemd" {
			return true
		}
		if osDesc, ok := h.Overrides["osDescription"]; ok && strings.Contains(strings.ToLower(osDesc), "alpine") {
			return true
		}
	}
	return false
}

func compileInstall(h plugin.HostFacts, opts map[string]any) (plugin.TaskSpec, error) {
	name := str(opts, "name", "")
	if name == "" {
		return plugin.TaskSpec{}, fmt.Errorf("remote name is required")
	}
	if !isSafeRemoteName(name) {
		return plugin.TaskSpec{}, fmt.Errorf("invalid remote name %q: use letters, digits, dash, underscore, dot", name)
	}
	configContent := str(opts, "configContent", "")
	if configContent == "" {
		return plugin.TaskSpec{}, fmt.Errorf("configContent is required")
	}
	target := str(opts, "mountTarget", "")
	if target == "" {
		return plugin.TaskSpec{}, fmt.Errorf("mountTarget is required")
	}
	if !strings.HasPrefix(target, "/") || strings.ContainsAny(target, "\n\r\x00;`$&|<>\"") {
		return plugin.TaskSpec{}, fmt.Errorf("invalid mountTarget %q: must be a safe absolute path", target)
	}
	policy := str(opts, "policy", globalSetting("defaultPolicy", DefaultMountPolicy))
	mountOptions := str(opts, "mountOptions", globalSetting("defaultMountOptions", DefaultMountOptions))
	mountUser := str(opts, "mountUser", globalSetting("defaultMountUser", DefaultMountUser))

	if !validPolicy(policy) {
		return plugin.TaskSpec{}, fmt.Errorf("invalid mount policy %q", policy)
	}

	scope, err := systemd.ParseScope(str(opts, "scope", "system"))
	if err != nil {
		return plugin.TaskSpec{}, err
	}

	// Non-systemd Linux fallback: if systemd is absent and a systemd-mount or
	// systemd-automount policy was requested, degrade gracefully to fstab.
	if isNonSystemdHost(h) && (policy == "systemd-mount" || policy == "systemd-automount") {
		policy = "fstab"
	}

	// Every policy starts by ensuring the remote config section exists on the host.
	// blockinfile is idempotent (markers) and no_log keeps obscured values out of
	// the Ansible output.
	steps := []plugin.TaskStep{
		{
			Name:   fmt.Sprintf("Ensure rclone config directory for %s", name),
			Module: "ansible.builtin.file",
			Params: map[string]any{
				"path":   "/root/.config/rclone",
				"state":  "directory",
				"mode":   "0700",
				"become": true,
			},
		},
		{
			Name:   fmt.Sprintf("Write obscured rclone remote %s", name),
			Module: "ansible.builtin.blockinfile",
			Params: map[string]any{
				"path":   "/root/.config/rclone/rclone.conf",
				"create": true,
				"mode":   "0600",
				"marker": fmt.Sprintf("# {mark} ATC rclone remote: %s", name),
				"block":  configContent,
				"no_log": true,
				"become": true,
			},
		},
	}

	switch policy {
	case "systemd-mount":
		unitSteps, err := serviceUnitSteps(name, target, mountOptions, mountUser, scope, false)
		if err != nil {
			return plugin.TaskSpec{}, err
		}
		steps = append(steps, unitSteps...)
	case "systemd-automount":
		unitSteps, err := serviceUnitSteps(name, target, mountOptions, mountUser, scope, true)
		if err != nil {
			return plugin.TaskSpec{}, err
		}
		steps = append(steps, unitSteps...)
	case "fstab":
		steps = append(steps,
			plugin.TaskStep{
				Name:   fmt.Sprintf("Create directory %s", target),
				Module: "ansible.builtin.file",
				Params: map[string]any{
					"path":   target,
					"state":  "directory",
					"mode":   "0755",
					"become": true,
				},
			},
			plugin.TaskStep{
				Name:   fmt.Sprintf("Mount rclone remote %s via fstab", name),
				Module: "ansible.posix.mount",
				Params: map[string]any{
					"path":   target,
					"src":    fmt.Sprintf("%s:", name),
					"fstype": "fuse.rclone",
					"opts":   fmt.Sprintf("x-systemd.automount,_netdev,user,args2env,%s", strings.ReplaceAll(mountOptions, " ", ",")),
					"state":  "mounted",
					"become": true,
				},
			},
		)
	case "script":
		scriptSteps, err := scriptPolicySteps(name, target, mountOptions, mountUser, scope)
		if err != nil {
			return plugin.TaskSpec{}, err
		}
		steps = append(steps, scriptSteps...)
	}

	return plugin.TaskSpec{
		Name:  fmt.Sprintf("Install rclone remote %s (%s)", name, policy),
		Steps: steps,
		SecretsMasked: map[string]string{
			"configContent": "•••••••• (rclone obscured config)",
		},
	}, nil
}

// systemdUnitParams builds the parameter map for `ansible.builtin.systemd_service`
// respecting system vs user scope conventions from `backend-go/pkg/systemd`.
func systemdUnitParams(scope systemd.Scope, extra map[string]any) map[string]any {
	params := make(map[string]any, len(extra)+2)
	for k, v := range extra {
		params[k] = v
	}
	if scope == systemd.ScopeUser {
		params["scope"] = string(systemd.ScopeUser)
	} else {
		params["become"] = true
	}
	return params
}

// serviceUnitSteps compiles the systemd unit(s), their `/etc/systemd/system/<unit>.d/atc-rclone.conf`
// drop-in subfolder overrides, and `ansible.builtin.systemd_service` activation steps.
func serviceUnitSteps(name, target, mountOptions, mountUser string, scope systemd.Scope, automount bool) ([]plugin.TaskStep, error) {
	steps := []plugin.TaskStep{
		{
			Name:   fmt.Sprintf("Create directory %s", target),
			Module: "ansible.builtin.file",
			Params: map[string]any{
				"path":   target,
				"state":  "directory",
				"mode":   "0755",
				"become": true,
			},
		},
	}

	if automount {
		mountUnitName, err := systemd.NormalizeUnitName(MountUnitName(target))
		if err != nil {
			return nil, err
		}
		automountUnitName, err := systemd.NormalizeUnitName(AutomountUnitName(target))
		if err != nil {
			return nil, err
		}
		dropInDir := DropInDir(mountUnitName)
		dropInFile := DropInPath(mountUnitName, DefaultDropInFile)

		steps = append(steps,
			plugin.TaskStep{
				Name:   fmt.Sprintf("Install %s", mountUnitName),
				Module: "ansible.builtin.copy",
				Params: map[string]any{
					"dest":    fmt.Sprintf("/etc/systemd/system/%s", mountUnitName),
					"content": mountUnit(name, target, mountOptions),
					"owner":   "root",
					"group":   "root",
					"mode":    "0644",
					"become":  true,
				},
			},
			plugin.TaskStep{
				Name:   fmt.Sprintf("Ensure drop-in directory %s", dropInDir),
				Module: "ansible.builtin.file",
				Params: map[string]any{
					"path":   dropInDir,
					"state":  "directory",
					"owner":  "root",
					"group":  "root",
					"mode":   "0755",
					"become": true,
				},
			},
			plugin.TaskStep{
				Name:   fmt.Sprintf("Write ATC drop-in %s", dropInFile),
				Module: "ansible.builtin.copy",
				Params: map[string]any{
					"dest":    dropInFile,
					"content": mountDropIn(name, target, mountOptions),
					"owner":   "root",
					"group":   "root",
					"mode":    "0644",
					"become":  true,
				},
			},
			plugin.TaskStep{
				Name:   fmt.Sprintf("Install %s", automountUnitName),
				Module: "ansible.builtin.copy",
				Params: map[string]any{
					"dest":    fmt.Sprintf("/etc/systemd/system/%s", automountUnitName),
					"content": automountUnit(name, target),
					"owner":   "root",
					"group":   "root",
					"mode":    "0644",
					"become":  true,
				},
			},
			plugin.TaskStep{
				Name:   "Reload systemd manager configuration",
				Module: "ansible.builtin.systemd_service",
				Params: systemdUnitParams(scope, map[string]any{
					"daemon_reload": true,
				}),
			},
			plugin.TaskStep{
				Name:   fmt.Sprintf("Enable and start %s", automountUnitName),
				Module: "ansible.builtin.systemd_service",
				Params: systemdUnitParams(scope, map[string]any{
					"name":    automountUnitName,
					"enabled": true,
					"state":   "started",
				}),
			},
		)
		return steps, nil
	}

	svcUnitName, err := systemd.NormalizeUnitName(ServiceUnitName(name))
	if err != nil {
		return nil, err
	}
	dropInDir := DropInDir(svcUnitName)
	dropInFile := DropInPath(svcUnitName, DefaultDropInFile)

	steps = append(steps,
		plugin.TaskStep{
			Name:   fmt.Sprintf("Install %s", svcUnitName),
			Module: "ansible.builtin.copy",
			Params: map[string]any{
				"dest":    fmt.Sprintf("/etc/systemd/system/%s", svcUnitName),
				"content": serviceUnit(name, target, mountOptions, mountUser),
				"owner":   "root",
				"group":   "root",
				"mode":    "0644",
				"become":  true,
			},
		},
		plugin.TaskStep{
			Name:   fmt.Sprintf("Ensure drop-in directory %s", dropInDir),
			Module: "ansible.builtin.file",
			Params: map[string]any{
				"path":   dropInDir,
				"state":  "directory",
				"owner":  "root",
				"group":  "root",
				"mode":   "0755",
				"become": true,
			},
		},
		plugin.TaskStep{
			Name:   fmt.Sprintf("Write ATC drop-in %s", dropInFile),
			Module: "ansible.builtin.copy",
			Params: map[string]any{
				"dest":    dropInFile,
				"content": serviceDropIn(name, target, mountOptions, mountUser),
				"owner":   "root",
				"group":   "root",
				"mode":    "0644",
				"become":  true,
			},
		},
		plugin.TaskStep{
			Name:   "Reload systemd manager configuration",
			Module: "ansible.builtin.systemd_service",
			Params: systemdUnitParams(scope, map[string]any{
				"daemon_reload": true,
			}),
		},
		plugin.TaskStep{
			Name:   fmt.Sprintf("Enable and start %s", svcUnitName),
			Module: "ansible.builtin.systemd_service",
			Params: systemdUnitParams(scope, map[string]any{
				"name":    svcUnitName,
				"enabled": true,
				"state":   "started",
			}),
		},
	)

	return steps, nil
}

func scriptPolicySteps(name, target, mountOptions, mountUser string, scope systemd.Scope) ([]plugin.TaskStep, error) {
	svcUnitName, err := systemd.NormalizeUnitName(ServiceUnitName(name))
	if err != nil {
		return nil, err
	}
	dropInDir := DropInDir(svcUnitName)
	dropInFile := DropInPath(svcUnitName, DefaultDropInFile)

	return []plugin.TaskStep{
		{
			Name:   fmt.Sprintf("Create directory %s", target),
			Module: "ansible.builtin.file",
			Params: map[string]any{"path": target, "state": "directory", "mode": "0755", "become": true},
		},
		{
			Name:   fmt.Sprintf("Install rclone-mount-%s.sh", name),
			Module: "ansible.builtin.copy",
			Params: map[string]any{
				"dest":    fmt.Sprintf("/usr/local/bin/rclone-mount-%s.sh", name),
				"content": mountScript(name, target, mountOptions),
				"owner":   "root",
				"group":   "root",
				"mode":    "0755",
				"become":  true,
			},
		},
		{
			Name:   fmt.Sprintf("Install %s", svcUnitName),
			Module: "ansible.builtin.copy",
			Params: map[string]any{
				"dest":    fmt.Sprintf("/etc/systemd/system/%s", svcUnitName),
				"content": scriptUnit(name),
				"owner":   "root",
				"group":   "root",
				"mode":    "0644",
				"become":  true,
			},
		},
		{
			Name:   fmt.Sprintf("Ensure drop-in directory %s", dropInDir),
			Module: "ansible.builtin.file",
			Params: map[string]any{
				"path":   dropInDir,
				"state":  "directory",
				"owner":  "root",
				"group":  "root",
				"mode":   "0755",
				"become": true,
			},
		},
		{
			Name:   fmt.Sprintf("Write ATC drop-in %s", dropInFile),
			Module: "ansible.builtin.copy",
			Params: map[string]any{
				"dest":    dropInFile,
				"content": serviceDropIn(name, target, mountOptions, mountUser),
				"owner":   "root",
				"group":   "root",
				"mode":    "0644",
				"become":  true,
			},
		},
		{
			Name:   "Reload systemd manager configuration",
			Module: "ansible.builtin.systemd_service",
			Params: systemdUnitParams(scope, map[string]any{"daemon_reload": true}),
		},
		{
			Name:   fmt.Sprintf("Enable and start %s", svcUnitName),
			Module: "ansible.builtin.systemd_service",
			Params: systemdUnitParams(scope, map[string]any{
				"name":    svcUnitName,
				"enabled": true,
				"state":   "started",
			}),
		},
	}, nil
}

func compileRemove(_ plugin.HostFacts, opts map[string]any) (plugin.TaskSpec, error) {
	name := str(opts, "name", "")
	if name == "" {
		return plugin.TaskSpec{}, fmt.Errorf("remote name is required")
	}
	if !isSafeRemoteName(name) {
		return plugin.TaskSpec{}, fmt.Errorf("invalid remote name %q", name)
	}
	target := str(opts, "mountTarget", "")
	policy := str(opts, "policy", "systemd-mount")
	scope, err := systemd.ParseScope(str(opts, "scope", "system"))
	if err != nil {
		return plugin.TaskSpec{}, err
	}

	unitToStop := ServiceUnitName(name)
	if policy == "systemd-automount" && target != "" {
		unitToStop = AutomountUnitName(target)
	}
	normalizedUnit, err := systemd.NormalizeUnitName(unitToStop)
	if err != nil {
		return plugin.TaskSpec{}, err
	}

	steps := []plugin.TaskStep{
		{
			Name:   fmt.Sprintf("Stop %s", normalizedUnit),
			Module: "ansible.builtin.systemd_service",
			Params: systemdUnitParams(scope, map[string]any{
				"name":    normalizedUnit,
				"state":   "stopped",
				"enabled": false,
			}),
		},
		{
			Name:   fmt.Sprintf("Remove %s unit file", normalizedUnit),
			Module: "ansible.builtin.file",
			Params: map[string]any{
				"path":   fmt.Sprintf("/etc/systemd/system/%s", normalizedUnit),
				"state":  "absent",
				"become": true,
			},
		},
		{
			Name:   fmt.Sprintf("Remove %s drop-in directory", normalizedUnit),
			Module: "ansible.builtin.file",
			Params: map[string]any{
				"path":   DropInDir(normalizedUnit),
				"state":  "absent",
				"become": true,
			},
		},
		{
			Name:   fmt.Sprintf("Remove rclone config section for %s", name),
			Module: "ansible.builtin.blockinfile",
			Params: map[string]any{
				"path":   "/root/.config/rclone/rclone.conf",
				"marker": fmt.Sprintf("# {mark} ATC rclone remote: %s", name),
				"state":  "absent",
				"become": true,
			},
		},
	}

	if target != "" {
		steps = append(steps, plugin.TaskStep{
			Name:   fmt.Sprintf("Unmount %s", target),
			Module: "ansible.posix.mount",
			Params: map[string]any{
				"path":   target,
				"state":  "unmounted",
				"become": true,
			},
		})
	}

	steps = append(steps, plugin.TaskStep{
		Name:   "Reload systemd manager configuration",
		Module: "ansible.builtin.systemd_service",
		Params: systemdUnitParams(scope, map[string]any{"daemon_reload": true}),
	})

	return plugin.TaskSpec{
		Name:  fmt.Sprintf("Remove rclone remote %s", name),
		Steps: steps,
	}, nil
}

func validPolicy(policy string) bool {
	switch policy {
	case "systemd-mount", "systemd-automount", "fstab", "script":
		return true
	}
	return false
}

func isSafeRemoteName(name string) bool {
	if name == "" || len(name) > 64 {
		return false
	}
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') ||
			r == '-' || r == '_' || r == '.' {
			continue
		}
		return false
	}
	return true
}

func atoiSafe(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return n
		}
		n = n*10 + int(r-'0')
	}
	return n
}
