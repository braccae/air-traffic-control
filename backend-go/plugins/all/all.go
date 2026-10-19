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
)
