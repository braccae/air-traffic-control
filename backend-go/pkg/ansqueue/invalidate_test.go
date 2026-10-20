package ansqueue

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"air-traffic-control/backend/pkg/hostcache"
)

func seedHost(t *testing.T, store *hostcache.Store, hostID string) {
	t.Helper()
	for _, section := range []string{"interfaces", "tailscale", "disks", "systemd", "firewall", "shares"} {
		if err := store.UpsertHostSection(hostID, section, json.RawMessage(`{"ok":true}`)); err != nil {
			t.Fatalf("seed section %s: %v", section, err)
		}
	}
	if err := store.UpsertHostTelemetry(hostID, json.RawMessage(`{"cpu":1}`)); err != nil {
		t.Fatalf("seed telemetry: %v", err)
	}
	if err := store.UpsertHostContainers(hostID, hostcache.ContainersUpdate{
		ProbedUsersJSON:    json.RawMessage(`[]`),
		IdentitiesJSON:     json.RawMessage(`[]`),
		RootContainersJSON: json.RawMessage(`[]`),
	}); err != nil {
		t.Fatalf("seed containers: %v", err)
	}
}

func hasSection(t *testing.T, store *hostcache.Store, hostID, section string) bool {
	t.Helper()
	_, ok, err := store.GetHostSection(hostID, section)
	if err != nil {
		t.Fatalf("GetHostSection: %v", err)
	}
	return ok
}

func TestCacheInvalidatorMappedSections(t *testing.T) {
	cache, err := hostcache.Open(filepath.Join(t.TempDir(), "cache.sqlite"))
	if err != nil {
		t.Fatalf("hostcache.Open: %v", err)
	}
	defer cache.Close()

	inv := NewCacheInvalidator(cache)
	hostID := "node-a"
	seedHost(t, cache, hostID)

	if err := inv.InvalidateForTask(Task{TargetNodeID: hostID, Category: "network"}); err != nil {
		t.Fatalf("InvalidateForTask: %v", err)
	}

	if hasSection(t, cache, hostID, "interfaces") || hasSection(t, cache, hostID, "tailscale") {
		t.Fatal("network sections were not invalidated")
	}
	for _, keep := range []string{"disks", "systemd", "firewall", "shares"} {
		if !hasSection(t, cache, hostID, keep) {
			t.Fatalf("section %q must be untouched by a network mutation", keep)
		}
	}
	if _, ok, err := cache.GetHostTelemetry(hostID); err != nil || ok {
		t.Fatalf("telemetry must always be invalidated (ok=%v err=%v)", ok, err)
	}
	if _, ok, err := cache.GetHostContainers(hostID); err != nil || !ok {
		t.Fatalf("container row must survive a network mutation (ok=%v err=%v)", ok, err)
	}
}

func TestCacheInvalidatorContainerAndTelemetry(t *testing.T) {
	cache, err := hostcache.Open(filepath.Join(t.TempDir(), "cache.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer cache.Close()

	inv := NewCacheInvalidator(cache)
	hostID := "node-b"
	seedHost(t, cache, hostID)

	if err := inv.InvalidateForTask(Task{TargetNodeID: hostID, Category: "container"}); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := cache.GetHostContainers(hostID); err != nil || ok {
		t.Fatalf("container row must be deleted (ok=%v err=%v)", ok, err)
	}
	if !hasSection(t, cache, hostID, "interfaces") || !hasSection(t, cache, hostID, "disks") {
		t.Fatal("container mutations must not clear section rows")
	}
	if _, ok, _ := cache.GetHostTelemetry(hostID); ok {
		t.Fatal("telemetry must be invalidated")
	}
}

func TestCacheInvalidatorUnknownCategoryClearsAllSections(t *testing.T) {
	cache, err := hostcache.Open(filepath.Join(t.TempDir(), "cache.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer cache.Close()

	inv := NewCacheInvalidator(cache)
	hostID := "node-c"
	seedHost(t, cache, hostID)

	if err := inv.InvalidateForTask(Task{TargetNodeID: hostID, Category: "something-new"}); err != nil {
		t.Fatal(err)
	}
	for _, section := range []string{"interfaces", "tailscale", "disks", "systemd", "firewall", "shares"} {
		if hasSection(t, cache, hostID, section) {
			t.Fatalf("unknown category left section %q cached", section)
		}
	}
}

func TestCacheInvalidatorRequiresHost(t *testing.T) {
	cache, err := hostcache.Open(filepath.Join(t.TempDir(), "cache.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer cache.Close()

	inv := NewCacheInvalidator(cache)
	seedHost(t, cache, "node-d")

	if err := inv.InvalidateForTask(Task{Category: "system"}); err == nil {
		t.Fatal("expected an error for an empty targetNodeId")
	}
	if !hasSection(t, cache, "node-d", "systemd") {
		t.Fatal("an unscoped task must not invalidate anything")
	}
}

func TestNilInvalidatorIsSafe(t *testing.T) {
	var inv *CacheInvalidator
	if err := inv.InvalidateForTask(Task{TargetNodeID: "h", Category: "system"}); err != nil {
		t.Fatalf("nil invalidator returned an error: %v", err)
	}
	if err := (NoopInvalidator{}).InvalidateForTask(Task{TargetNodeID: "h"}); err != nil {
		t.Fatalf("NoopInvalidator returned an error: %v", err)
	}
}
