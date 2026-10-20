// Package rclone implements the mount/rclone plugin: rclone remote detection and
// declarative compilation of the unit/file/mount tasks that install a remote on a
// device and expose it as a live mount.
//
// Secrets never reach this plugin in plaintext. The coordinator decrypts a remote
// in memory, converts secret fields to rclone's own obscured form (rclone obscure),
// and passes the resulting rclone.conf section as opaque text. This plugin writes
// that section with ansible.builtin.blockinfile guarded by no_log, so neither unit
// files nor Ansible logs ever contain rclone credentials.
package rclone

import (
	"context"
	"fmt"
	"strings"

	"air-traffic-control/backend/pkg/plugin"
)

func init() {
	_ = plugin.Register(New())
}

// Plugin provides rclone remote inspection and mount installation.
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
		ControllerCollections: []string{"ansible.posix", "community.general"},
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
			},
		},
	}
}

func (p *Plugin) Compile(h plugin.HostFacts, action string, opts map[string]any) (plugin.TaskSpec, error) {
	switch strings.ToLower(action) {
	case "install_remote":
		return compileInstall(opts)
	case "remove_remote":
		return compileRemove(opts)
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

func compileInstall(opts map[string]any) (plugin.TaskSpec, error) {
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
	policy := str(opts, "policy", "systemd-mount")
	mountOptions := str(opts, "mountOptions", "--vfs-cache-mode writes --dir-cache-time 5m")
	mountUser := str(opts, "mountUser", "root")

	if !validPolicy(policy) {
		return plugin.TaskSpec{}, fmt.Errorf("invalid mount policy %q", policy)
	}

	// Every policy starts by ensuring the remote config section exists on the host.
	// blockinfile is idempotent (markers) and no_log keeps obscured values out of
	// the Ansible output.
	steps := []plugin.TaskStep{
		{
			Name:   fmt.Sprintf("Ensure rclone config directory for %s", name),
			Module: "ansible.builtin.file",
			Params: map[string]any{
				"path":  "/root/.config/rclone",
				"state": "directory",
				"mode":  "0700",
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
			},
		},
	}

	switch policy {
	case "systemd-mount":
		steps = append(steps, serviceUnitSteps(name, target, mountOptions, mountUser, false)...)
	case "systemd-automount":
		steps = append(steps, serviceUnitSteps(name, target, mountOptions, mountUser, true)...)
	case "fstab":
		steps = append(steps,
			plugin.TaskStep{
				Name:   fmt.Sprintf("Mount rclone remote %s via fstab", name),
				Module: "ansible.posix.mount",
				Params: map[string]any{
					"path":   target,
					"src":    fmt.Sprintf("%s:", name),
					"fstype": "fuse.rclone",
					"opts":   fmt.Sprintf("x-systemd.automount,_netdev,user,args2env,%s", strings.ReplaceAll(mountOptions, " ", ",")),
					"state":  "mounted",
				},
			},
		)
	case "script":
		steps = append(steps, scriptPolicySteps(name, target, mountOptions, mountUser)...)
	}

	return plugin.TaskSpec{
		Name:  fmt.Sprintf("Install rclone remote %s (%s)", name, policy),
		Steps: steps,
		SecretsMasked: map[string]string{
			"configContent": "•••••••• (rclone obscured config)",
		},
	}, nil
}

// serviceUnitSteps compiles the systemd service (and, for automount, the
// .mount/.automount pair) that keeps the rclone mount active.
func serviceUnitSteps(name, target, mountOptions, mountUser string, automount bool) []plugin.TaskStep {
	serviceName := fmt.Sprintf("rclone-%s", name)
	steps := []plugin.TaskStep{
		{
			Name:   fmt.Sprintf("Create directory %s", target),
			Module: "ansible.builtin.file",
			Params: map[string]any{
				"path":  target,
				"state": "directory",
				"mode":  "0755",
			},
		},
		{
			Name:   fmt.Sprintf("Install %s.service", serviceName),
			Module: "ansible.builtin.copy",
			Params: map[string]any{
				"dest":    fmt.Sprintf("/etc/systemd/system/%s.service", serviceName),
				"content": serviceUnit(name, target, mountOptions, mountUser),
				"mode":    "0644",
			},
		},
	}

	if automount {
		steps = append(steps,
			plugin.TaskStep{
				Name:   fmt.Sprintf("Install %s.mount", serviceName),
				Module: "ansible.builtin.copy",
				Params: map[string]any{
					"dest":    fmt.Sprintf("/etc/systemd/system/%s.mount", serviceName),
					"content": mountUnit(name, target, mountOptions),
					"mode":    "0644",
				},
			},
			plugin.TaskStep{
				Name:   fmt.Sprintf("Install %s.automount", serviceName),
				Module: "ansible.builtin.copy",
				Params: map[string]any{
					"dest":    fmt.Sprintf("/etc/systemd/system/%s.automount", serviceName),
					"content": automountUnit(name, target),
					"mode":    "0644",
				},
			},
		)
	}

	steps = append(steps, plugin.TaskStep{
		Name:   "Reload systemd manager configuration",
		Module: "ansible.builtin.systemd",
		Params: map[string]any{
			"daemon_reload": true,
		},
	})

	if automount {
		// Starting the automount unit is what actually arms the on-access mount;
		// systemd activates the sibling .mount on first access.
		steps = append(steps, plugin.TaskStep{
			Name:   fmt.Sprintf("Enable and start %s.automount", serviceName),
			Module: "ansible.builtin.systemd",
			Params: map[string]any{
				"name":    fmt.Sprintf("%s.automount", serviceName),
				"enabled": true,
				"state":   "started",
			},
		})
	} else {
		steps = append(steps, plugin.TaskStep{
			Name:   fmt.Sprintf("Enable and start %s.service", serviceName),
			Module: "ansible.builtin.systemd",
			Params: map[string]any{
				"name":    fmt.Sprintf("%s.service", serviceName),
				"enabled": true,
				"state":   "started",
			},
		})
	}

	return steps
}

func scriptPolicySteps(name, target, mountOptions, mountUser string) []plugin.TaskStep {
	serviceName := fmt.Sprintf("rclone-%s", name)
	return []plugin.TaskStep{
		{
			Name:   fmt.Sprintf("Create directory %s", target),
			Module: "ansible.builtin.file",
			Params: map[string]any{"path": target, "state": "directory", "mode": "0755"},
		},
		{
			Name:   fmt.Sprintf("Install rclone-mount-%s.sh", name),
			Module: "ansible.builtin.copy",
			Params: map[string]any{
				"dest":    fmt.Sprintf("/usr/local/bin/rclone-mount-%s.sh", name),
				"content": mountScript(name, target, mountOptions),
				"mode":    "0755",
			},
		},
		{
			Name:   fmt.Sprintf("Install %s.service", serviceName),
			Module: "ansible.builtin.copy",
			Params: map[string]any{
				"dest":    fmt.Sprintf("/etc/systemd/system/%s.service", serviceName),
				"content": scriptUnit(name),
				"mode":    "0644",
			},
		},
		{
			Name:   "Reload systemd manager configuration",
			Module: "ansible.builtin.systemd",
			Params: map[string]any{"daemon_reload": true},
		},
		{
			Name:   fmt.Sprintf("Enable and start %s.service", serviceName),
			Module: "ansible.builtin.systemd",
			Params: map[string]any{
				"name":    fmt.Sprintf("%s.service", serviceName),
				"enabled": true,
				"state":   "started",
			},
		},
	}
}

func compileRemove(opts map[string]any) (plugin.TaskSpec, error) {
	name := str(opts, "name", "")
	if name == "" {
		return plugin.TaskSpec{}, fmt.Errorf("remote name is required")
	}
	if !isSafeRemoteName(name) {
		return plugin.TaskSpec{}, fmt.Errorf("invalid remote name %q", name)
	}
	target := str(opts, "mountTarget", "")
	policy := str(opts, "policy", "systemd-mount")
	serviceName := fmt.Sprintf("rclone-%s", name)

	steps := []plugin.TaskStep{
		{
			Name:   fmt.Sprintf("Stop %s units", name),
			Module: "ansible.builtin.systemd",
			Params: map[string]any{
				"name":    serviceName + unitSuffix(policy),
				"state":   "stopped",
				"enabled": false,
			},
		},
		{
			Name:   fmt.Sprintf("Remove %s unit files", name),
			Module: "ansible.builtin.file",
			Params: map[string]any{
				"path":  fmt.Sprintf("/etc/systemd/system/%s.*", serviceName),
				"state": "absent",
			},
		},
		{
			Name:   fmt.Sprintf("Remove rclone config section for %s", name),
			Module: "ansible.builtin.blockinfile",
			Params: map[string]any{
				"path":   "/root/.config/rclone/rclone.conf",
				"marker": fmt.Sprintf("# {mark} ATC rclone remote: %s", name),
				"state":  "absent",
			},
		},
	}

	if target != "" {
		steps = append(steps, plugin.TaskStep{
			Name:   fmt.Sprintf("Unmount %s", target),
			Module: "ansible.posix.mount",
			Params: map[string]any{
				"path":  target,
				"state": "unmounted",
			},
		})
	}

	steps = append(steps, plugin.TaskStep{
		Name:   "Reload systemd manager configuration",
		Module: "ansible.builtin.systemd",
		Params: map[string]any{"daemon_reload": true},
	})

	return plugin.TaskSpec{
		Name:  fmt.Sprintf("Remove rclone remote %s", name),
		Steps: steps,
	}, nil
}

func unitSuffix(policy string) string {
	switch policy {
	case "systemd-automount":
		return ".automount"
	default:
		return ".service"
	}
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
