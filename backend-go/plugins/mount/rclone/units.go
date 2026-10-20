package rclone

import "fmt"

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

// mountUnit renders the .mount unit used by the systemd-automount policy.
// systemd activates it on first access to the mount point.
func mountUnit(name, target, mountOptions string) string {
	return fmt.Sprintf(`[Unit]
Description=rclone mount %[1]s
After=network-online.target

[Mount]
What=%[1]s:
Where=%[2]s
Type=fuse.rclone
Options=%[3]s

[Install]
WantedBy=multi-user.target
`, name, target, optionsCSV(mountOptions))
}

// automountUnit renders the .automount unit that arms the on-access mount.
func automountUnit(name, target string) string {
	return fmt.Sprintf(`[Unit]
Description=Automount rclone remote %[1]s

[Automount]
Where=%[2]s

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
