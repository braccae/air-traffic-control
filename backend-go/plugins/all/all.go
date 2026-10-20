package all

import (
	// Connection plugins
	_ "air-traffic-control/backend/plugins/connection/local"
	_ "air-traffic-control/backend/plugins/connection/ssh"
	_ "air-traffic-control/backend/plugins/connection/tailscale"

	// Elevation plugins
	_ "air-traffic-control/backend/plugins/elevation/doas"
	_ "air-traffic-control/backend/plugins/elevation/none"
	_ "air-traffic-control/backend/plugins/elevation/run0"
	_ "air-traffic-control/backend/plugins/elevation/sudo"

	// Package manager plugins
	_ "air-traffic-control/backend/plugins/package/apk"
	_ "air-traffic-control/backend/plugins/package/apt"
	_ "air-traffic-control/backend/plugins/package/bootc"
	_ "air-traffic-control/backend/plugins/package/dnf"
	_ "air-traffic-control/backend/plugins/package/rpmostree"

	// System service plugins
	_ "air-traffic-control/backend/plugins/system/service"

	// Storage plugins
	_ "air-traffic-control/backend/plugins/storage/btrfs"
	_ "air-traffic-control/backend/plugins/storage/lvm"
	_ "air-traffic-control/backend/plugins/storage/zfs"

	// Share plugins
	_ "air-traffic-control/backend/plugins/share/nfs"
	_ "air-traffic-control/backend/plugins/share/smb"

	// Container plugin
	_ "air-traffic-control/backend/plugins/container/podman"

	// Backup plugin
	_ "air-traffic-control/backend/plugins/backup/restic"

	// Mount plugin
	_ "air-traffic-control/backend/plugins/mount/rclone"

	// Logs plugin
	_ "air-traffic-control/backend/plugins/logs/journald"

	// Disk & SMART plugin
	_ "air-traffic-control/backend/plugins/disk/smart"

	// Virtual machine plugins
	_ "air-traffic-control/backend/plugins/vm/libvirt"
	_ "air-traffic-control/backend/plugins/vm/lxc"
	_ "air-traffic-control/backend/plugins/vm/proxmox"

	// Telemetry plugin
	_ "air-traffic-control/backend/plugins/telemetry/glances"

	// Network & Firewall plugins
	_ "air-traffic-control/backend/plugins/firewall/firewalld"
	_ "air-traffic-control/backend/plugins/network/interfaces"
	_ "air-traffic-control/backend/plugins/network/tailscale"
)
