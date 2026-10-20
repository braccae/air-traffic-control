package ansqueue

import (
	"errors"
	"fmt"
	"strings"

	"air-traffic-control/backend/pkg/hostcache"
)

// Invalidator drops the cache entries a completed mutation may have staled.
type Invalidator interface {
	InvalidateForTask(task Task) error
}

// CacheInvalidator invalidates the shared hosts-cache.sqlite entries for the
// host a task mutated. It is nil-safe: a nil store (or a nil *CacheInvalidator)
// invalidates nothing and reports no error, so a daemon without a cache keeps
// serving the queue.
type CacheInvalidator struct {
	store *hostcache.Store
}

// NewCacheInvalidator builds an invalidator over the shared cache store.
func NewCacheInvalidator(store *hostcache.Store) *CacheInvalidator {
	return &CacheInvalidator{store: store}
}

// categorySections maps a task category to the host_section_cache sections the
// write can stale. An unlisted category falls back to invalidating every
// section for the host, exactly like server.ts does today.
var categorySections = map[string][]string{
	"system":    {"systemd", "system-logs"},
	"network":   {"interfaces", "tailscale"},
	"firewall":  {"firewall"},
	"storage":   {"disks", "storage"},
	"shares":    {"shares"},
	"container": nil, // no section: the container row is deleted instead
	"package":   {"system", "systemd"},
	"vm":        {"vms"},
	"telemetry": nil, // no section: the telemetry row is deleted instead
}

// InvalidateForTask deletes the mapped section rows, the container row for
// container mutations, and always the telemetry row (15s tier — any mutation can
// change a live metric). Every mutation is scoped to task.TargetNodeID: an
// unscoped task is an error and invalidates nothing.
func (c *CacheInvalidator) InvalidateForTask(task Task) error {
	if task.TargetNodeID == "" {
		return errors.New("ansqueue: cannot invalidate cache for a task with an empty targetNodeId")
	}
	if c == nil || c.store == nil {
		return nil
	}

	hostID := task.TargetNodeID
	dir := strings.ToLower(strings.TrimSpace(task.Category))

	sections, known := categorySections[dir]
	switch {
	case !known:
		// Unknown category: invalidate every section for the host.
		if err := c.store.InvalidateHostSections(hostID); err != nil {
			return fmt.Errorf("ansqueue: invalidate sections for %s: %w", hostID, err)
		}
	case len(sections) > 0:
		if err := c.store.DeleteHostSections(hostID, sections); err != nil {
			return fmt.Errorf("ansqueue: invalidate sections %v for %s: %w", sections, hostID, err)
		}
	}

	if known && dir == "container" {
		if err := c.store.DeleteHostContainerCache(hostID); err != nil {
			return fmt.Errorf("ansqueue: invalidate containers for %s: %w", hostID, err)
		}
	}

	// Unconditional: the telemetry tier is 15s and a mutation can move any metric.
	if err := c.store.DeleteHostTelemetry(hostID); err != nil {
		return fmt.Errorf("ansqueue: invalidate telemetry for %s: %w", hostID, err)
	}
	return nil
}

// NoopInvalidator is the nil-safe invalidator for callers without a cache.
type NoopInvalidator struct{}

// InvalidateForTask satisfies Invalidator and does nothing.
func (NoopInvalidator) InvalidateForTask(Task) error { return nil }
