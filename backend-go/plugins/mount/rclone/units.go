package rclone

import (
	"fmt"
	"strings"
)

// DefaultDropInFile is the ATC-owned drop-in filename written inside
// /etc/systemd/system/<unit>.d/ when drop-in overrides are installed.
const DefaultDropInFile = "atc-rclone.conf"

// MountUnitBaseName converts an absolute mount target path into systemd's
// canonical unit stem (matching `systemd-escape -p --suffix=mount <target>`).
// For example, "/mnt/rclone/hetzner" -> "mnt-rclone-hetzner" and
// "/mnt/my_share" -> `mnt-my\x5fshare`.
func MountUnitBaseName(target string) string {
	trimmed := strings.Trim(strings.TrimSpace(target), "/")
	if trimmed == "" {
		return "-"
	}
	parts := strings.Split(trimmed, "/")
	escaped := make([]string, 0, len(parts))
	for _, p := range parts {
		if p == "" {
			continue
		}
		escaped = append(escaped, escapeUnitPathComponent(p))
	}
	return strings.Join(escaped, "-")
}

func escapeUnitPathComponent(part string) string {
	var b strings.Builder
	for i := 0; i < len(part); i++ {
		c := part[i]
		isAlphaNum := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
		if isAlphaNum || c == ':' || (c == '.' && i > 0) {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, `\x%02x`, c)
		}
	}
	return b.String()
}

// MountUnitName returns the canonical `<escaped-target>.mount` unit name.
func MountUnitName(target string) string {
	return MountUnitBaseName(target) + ".mount"
}

// AutomountUnitName returns the canonical `<escaped-target>.automount` unit name.
func AutomountUnitName(target string) string {
	return MountUnitBaseName(target) + ".automount"
}

// ServiceUnitName returns the `rclone-<name>.service` unit name.
func ServiceUnitName(name string) string {
	return fmt.Sprintf("rclone-%s.service", name)
}

// DropInDir returns the `/etc/systemd/system/<unit>.d` drop-in directory path.
func DropInDir(unitName string) string {
	return fmt.Sprintf("/etc/systemd/system/%s.d", unitName)
}

// DropInPath returns `/etc/systemd/system/<unit>.d/<dropin>`.
func DropInPath(unitName, dropin string) string {
	if strings.TrimSpace(dropin) == "" {
		dropin = DefaultDropInFile
	}
	return fmt.Sprintf("%s/%s", DropInDir(unitName), dropin)
}

// serviceUnit renders the systemd service that performs the rclone mount.
// It contains no credentials: rclone reads the obscured config from
// /root/.config/rclone/rclone.conf at runtime.
func serviceUnit(name, target, mountOptions, mountUser string) string {
	return fmt.Sprintf(`[Unit]
Description=rclone mount %[1]s
After=network-online.target
Wants=network-online.target

[Service]
Type=notify
User=%[4]s
ExecStart=/usr/bin/rclone mount %[1]s: %[2]s %[3]s
ExecStop=/bin/fusermount -u %[2]s
Restart=on-failure
RestartSec=10

[Install]
WantedBy=multi-user.target
`, name, target, mountOptions, mountUser)
}

// serviceDropIn renders the ATC-owned drop-in under
// /etc/systemd/system/<unit>.service.d/atc-rclone.conf.
func serviceDropIn(name, target, mountOptions, mountUser string) string {
	return fmt.Sprintf(`# Managed by Air Traffic Control — rclone mount drop-in for %[1]s (%[2]s)
[Service]
User=%[4]s
Environment="RCLONE_CONFIG=/root/.config/rclone/rclone.conf"
# Mount options: %[3]s
`, name, target, mountOptions, mountUser)
}

// mountUnit renders the .mount unit used by the systemd-automount policy.
// systemd activates it on first access to the mount point.
func mountUnit(name, target, mountOptions string) string {
	return fmt.Sprintf(`[Unit]
Description=rclone mount %[1]s
After=network-online.target
Wants=network-online.target

[Mount]
What=%[1]s:
Where=%[2]s
Type=fuse.rclone
Options=%[3]s

[Install]
WantedBy=multi-user.target
`, name, target, optionsCSV(mountOptions))
}

// mountDropIn renders the ATC-owned drop-in under
// /etc/systemd/system/<escaped-target>.mount.d/atc-rclone.conf.
func mountDropIn(name, target, mountOptions string) string {
	return fmt.Sprintf(`# Managed by Air Traffic Control — rclone mount drop-in for %[1]s (%[2]s)
[Mount]
Options=%[3]s
`, name, target, optionsCSV(mountOptions))
}

// automountUnit renders the .automount unit that arms the on-access mount.
func automountUnit(name, target string) string {
	return fmt.Sprintf(`[Unit]
Description=Automount rclone remote %[1]s
After=network-online.target

[Automount]
Where=%[2]s
TimeoutIdleSec=600

[Install]
WantedBy=multi-user.target
`, name, target)
}

// scriptUnit wraps the generated mount script in a systemd service so it is
// supervised and started at boot.
func scriptUnit(name string) string {
	return fmt.Sprintf(`[Unit]
Description=rclone mount script %[1]s
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=/usr/local/bin/rclone-mount-%[1]s.sh
Restart=on-failure
RestartSec=10

[Install]
WantedBy=multi-user.target
`, name)
}

// mountScript renders /usr/local/bin/rclone-mount-<name>.sh, the "service
// script" mount strategy: it runs `rclone mount <remote>: <target>` in the
// foreground under systemd supervision.
func mountScript(name, target, mountOptions string) string {
	return fmt.Sprintf(`#!/bin/sh
# Managed by Air Traffic Control — do not edit by hand.
# Runs an rclone FUSE mount under systemd supervision.
set -eu
exec /usr/bin/rclone mount %[1]s: %[2]s %[3]s
`, name, target, mountOptions)
}

// optionsCSV converts space-separated rclone mount arguments into the
// comma-separated form systemd's [Mount] Options= key expects.
func optionsCSV(mountOptions string) string {
	out := ""
	start := 0
	for i := 0; i <= len(mountOptions); i++ {
		if i == len(mountOptions) || mountOptions[i] == ' ' {
			if i > start {
				if out != "" {
					out += ","
				}
				out += mountOptions[start:i]
			}
			start = i + 1
		}
	}
	if out == "" {
		return "_netdev"
	}
	return "_netdev," + out
}
