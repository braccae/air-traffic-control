package hostcache

import "time"

// TTL tiers. These mirror the TTLs used by server.ts and AGENTS.md §4 so the Go
// daemon and the TS coordinator expire cached entries on the same schedule.
//
// Fast telemetry (CPU/Watts) and container/pod state are the short tiers; the
// network tier covers interfaces and routing. Storage-ish sections default to 60s.
const (
	// TelemetryTTL is the TTL for /api/telemetry (CPU usage, watts, etc.).
	TelemetryTTL = 15 * time.Second
	// ContainerTTL is the TTL for container and pod state (/api/nodes/:id/containers).
	ContainerTTL = 20 * time.Second
	// NetworkTTL is the TTL for network interfaces and routing (/api/nodes/:id/interfaces).
	NetworkTTL = 30 * time.Second
	// FirewallTTL is the TTL for firewall rules.
	FirewallTTL = 45 * time.Second
	// DisksTTL is the TTL for the disks section.
	DisksTTL = 60 * time.Second
	// VMsTTL is the TTL for the vms section.
	VMsTTL = 60 * time.Second
	// StorageTTL is the TTL for the storage section.
	StorageTTL = 60 * time.Second
	// SharesTTL is the TTL for the shares section.
	SharesTTL = 60 * time.Second
)

// SectionTTL returns the cache TTL for a host_section_cache section name. The
// mapping matches server.ts: interfaces=30s, firewall=45s, everything else
// (disks/vms/storage/shares) = 60s. Unknown sections fall back to 60s.
func SectionTTL(section string) time.Duration {
	switch section {
	case "interfaces":
		return NetworkTTL
	case "firewall":
		return FirewallTTL
	case "disks":
		return DisksTTL
	case "vms":
		return VMsTTL
	case "storage":
		return StorageTTL
	case "shares":
		return SharesTTL
	default:
		return 60 * time.Second
	}
}

// Fresh reports whether an entry updated at updatedAtMS (Unix milliseconds) is
// still within ttl. It matches the TS check
// `Date.now() - updatedAt < CACHE_TTL_MS`.
func Fresh(updatedAtMS int64, ttl time.Duration) bool {
	return time.Now().UnixMilli()-updatedAtMS < ttl.Milliseconds()
}
