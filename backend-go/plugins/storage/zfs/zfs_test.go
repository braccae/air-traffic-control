package zfs_test

import (
	"context"
	"strings"
	"testing"

	"air-traffic-control/backend/pkg/plugin"
	_ "air-traffic-control/backend/plugins/all"
)

type mockRunner struct {
	responses map[string]string
}

func (m *mockRunner) RunCommand(ctx context.Context, cmd string) (string, error) {
	for pattern, resp := range m.responses {
		if strings.Contains(cmd, pattern) {
			return resp, nil
		}
	}
	return "", nil
}

func TestZFSPlugin_MetaAndCompatibility(t *testing.T) {
	p, ok := plugin.Get("storage/zfs")
	if !ok {
		t.Fatalf("storage/zfs plugin not registered")
	}

	meta := p.Meta()
	if meta.Kind != plugin.KindStorage || meta.Name != "zfs" {
		t.Errorf("unexpected meta: %+v", meta)
	}

	if !p.Compatible(plugin.HostFacts{OSFamily: "linux"}) {
		t.Errorf("expected compatible with linux")
	}
	if p.Compatible(plugin.HostFacts{OSFamily: "windows"}) {
		t.Errorf("expected incompatible with windows")
	}
}

func TestZFSPlugin_Probe(t *testing.T) {
	p, ok := plugin.Get("storage/zfs")
	if !ok {
		t.Fatal("storage/zfs not registered")
	}

	runner := &mockRunner{
		responses: map[string]string{
			"command -v zfs": "zfs\nzpool",
			"zpool list -H":  "tank\nrpool",
		},
	}

	det, err := p.Probe(context.Background(), runner, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatalf("probe error: %v", err)
	}
	if !det.Detected {
		t.Errorf("expected detected=true")
	}
	if !strings.Contains(det.Status, "2 pool") {
		t.Errorf("unexpected status: %s", det.Status)
	}

	// Test no pools found
	emptyRunner := &mockRunner{
		responses: map[string]string{
			"command -v zfs": "zfs\nzpool",
			"zpool list -H":  "no pools available",
		},
	}
	emptyDet, err := p.Probe(context.Background(), emptyRunner, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatalf("probe error: %v", err)
	}
	if emptyDet.Detected {
		t.Errorf("expected detected=false when no pools are available")
	}
}

func TestZFSPlugin_Read(t *testing.T) {
	p, ok := plugin.Get("storage/zfs")
	if !ok {
		t.Fatal("storage/zfs not registered")
	}

	reader, ok := p.(plugin.Reader)
	if !ok {
		t.Fatalf("expected plugin to implement plugin.Reader")
	}

	runner := &mockRunner{
		responses: map[string]string{
			"findmnt": "{\"filesystems\": []}\n===ZFS===\ntank/data 10G 5G 5G /tank/data\n===ZPOOL===\npool: tank state: ONLINE\n===ZPOOL_LIST===\ntank 100G 20G 80G ONLINE",
		},
	}

	data, err := reader.Read(context.Background(), runner, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}

	m, ok := data.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any return, got %T", data)
	}
	if zfsList, _ := m["zfsList"].(string); !strings.Contains(zfsList, "tank/data") {
		t.Errorf("expected zfsList containing tank/data, got %q", zfsList)
	}
}

func TestZFSPlugin_ActionsAndCompile(t *testing.T) {
	p, ok := plugin.Get("storage/zfs")
	if !ok {
		t.Fatal("storage/zfs not registered")
	}

	actor, ok := p.(plugin.Actor)
	if !ok {
		t.Fatalf("expected plugin to implement plugin.Actor")
	}

	// 1. Scrub action
	task, err := actor.Compile(plugin.HostFacts{NodeID: "node1"}, "scrub", map[string]any{
		"pool": "tank",
	})
	if err != nil {
		t.Fatalf("scrub compile failed: %v", err)
	}
	if len(task.Steps) != 1 || task.Steps[0].Module != "community.general.zpool" {
		t.Errorf("unexpected scrub steps: %+v", task.Steps)
	}

	// 2. Snapshot action
	task, err = actor.Compile(plugin.HostFacts{NodeID: "node1"}, "snapshot", map[string]any{
		"dataset":  "tank/data",
		"snapshot": "2026-10-19",
	})
	if err != nil {
		t.Fatalf("snapshot compile failed: %v", err)
	}
	if task.Steps[0].Params["name"] != "tank/data@2026-10-19" {
		t.Errorf("unexpected snapshot name: %v", task.Steps[0].Params["name"])
	}
}
