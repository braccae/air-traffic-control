package contextmenu_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"air-traffic-control/backend/pkg/contextmenu"
	"air-traffic-control/backend/pkg/plugin"
	"air-traffic-control/backend/pkg/plugin/newapi"
)

type testMenuPlugin struct {
	id       string
	kind     plugin.Kind
	osFamily string
}

func (p *testMenuPlugin) Meta() plugin.Meta {
	return plugin.Meta{
		ID:    p.id,
		Kind:  p.kind,
		Name:  "zfs",
		Label: "ZFS",
		Order: 10,
	}
}

func (p *testMenuPlugin) Compatible(h plugin.HostFacts) bool {
	if p.osFamily == "" {
		return true
	}
	return h.OSFamily == p.osFamily
}

func (p *testMenuPlugin) Requirements() plugin.Requirements {
	return plugin.Requirements{}
}

func (p *testMenuPlugin) Probe(ctx context.Context, r plugin.Runner, h plugin.HostFacts) (plugin.Detection, error) {
	return plugin.Detection{Detected: true, Status: "ONLINE"}, nil
}

func (p *testMenuPlugin) ContextMenuEntries(ctx context.Context, h plugin.HostFacts) []newapi.ContextMenuEntry {
	return contextmenu.NewBuilder(p.id, contextmenu.TargetStorage).
		WithGroup("pool_ops").
		AddAction("scrub-pool", "Scrub Pool", "cleaning_services", "scrub_pool", 20).
		AddSeparator(15).
		AddDangerAction("destroy-pool", "Destroy Pool", "delete", "destroy_pool", "Destroy pool permanently?", 10).
		AddSubmenu("snapshots", "Snapshots", "photo_camera", 5,
			contextmenu.MenuItem{ID: "snap-rollback", Label: "Rollback Snapshot", Action: "rollback_snapshot", Order: 2},
			contextmenu.MenuItem{ID: "snap-create", Label: "Create Snapshot", Action: "create_snapshot", Order: 1},
		).
		BuildEntries()
}

type fakeNodeResolver struct {
	facts map[string]plugin.HostFacts
}

func (f *fakeNodeResolver) ResolveNode(nodeID string) (plugin.HostFacts, bool, error) {
	h, ok := f.facts[nodeID]
	return h, ok, nil
}

func TestTargets_ValidationAndParsing(t *testing.T) {
	targets := contextmenu.AllTargets()
	if len(targets) != 9 {
		t.Fatalf("expected 9 targets, got %d", len(targets))
	}
	for _, tgt := range targets {
		if err := contextmenu.ValidateTarget(tgt); err != nil {
			t.Errorf("expected target %q to be valid, got: %v", tgt, err)
		}
	}

	parsed, err := contextmenu.ParseTarget("")
	if err != nil || parsed != contextmenu.TargetNode {
		t.Fatalf("expected empty target to default to TargetNode, got %q (%v)", parsed, err)
	}

	parsed, err = contextmenu.ParseTarget("  STORAGE ")
	if err != nil || parsed != contextmenu.TargetStorage {
		t.Fatalf("expected STORAGE to parse as TargetStorage, got %q (%v)", parsed, err)
	}

	if _, err := contextmenu.ParseTarget("invalid_target"); err == nil {
		t.Fatal("expected error for unsupported target")
	}
}

func TestBuilder_ItemsSubmenusSeparatorsAndSections(t *testing.T) {
	b := contextmenu.NewBuilder("container/podman", contextmenu.TargetContainer).
		WithGroup("lifecycle").
		AddAction("restart", "Restart Container", "refresh", "restart_container", 20).
		AddAction("start", "Start Container", "play_arrow", "start_container", 10).
		AddSeparator(25).
		AddDangerAction("remove", "Remove Container", "delete", "remove_container", "Remove container?", 30).
		WithGroup("diagnostics").
		AddDisabledAction("inspect", "Inspect Container", "search", "inspect_container", "Container is offline", 5).
		AddSubmenu("logs", "Logs", "article", 15,
			contextmenu.MenuItem{ID: "logs-tail", Label: "Tail 100 Lines", Action: "tail_logs", Order: 2},
			contextmenu.MenuItem{ID: "logs-follow", Label: "Follow Live", Action: "follow_logs", Order: 1},
		)

	items := b.Build()
	if len(items) != 6 {
		t.Fatalf("expected 6 items, got %d", len(items))
	}

	// Order check: inspect (5), start (10), logs (15), restart (20), separator (25), remove (30)
	expectedIDs := []string{"inspect", "start", "logs", "restart", "container/podman-separator-1", "remove"}
	for i, want := range expectedIDs {
		if items[i].ID != want {
			t.Errorf("item[%d] expected ID %q, got %q", i, want, items[i].ID)
		}
	}

	if !items[0].Disabled || items[0].DisabledReason != "Container is offline" {
		t.Errorf("expected first item to be disabled with reason, got %+v", items[0])
	}
	if items[2].Kind != contextmenu.ItemKindSubmenu || len(items[2].Children) != 2 {
		t.Fatalf("expected submenu item with 2 children, got %+v", items[2])
	}
	if items[2].Children[0].ID != "logs-follow" {
		t.Errorf("expected submenu children sorted by order, got %+v", items[2].Children)
	}
	if items[4].Kind != contextmenu.ItemKindSeparator {
		t.Errorf("expected separator kind at index 4, got %+v", items[4])
	}
	if !items[5].Danger || items[5].ConfirmPrompt == "" {
		t.Errorf("expected danger action at index 5, got %+v", items[5])
	}

	sections := b.BuildSections()
	if len(sections) != 2 {
		t.Fatalf("expected 2 sections (diagnostics, lifecycle), got %d", len(sections))
	}
	if sections[0].ID != "diagnostics" || sections[0].Label != "Diagnostics" || len(sections[0].Items) != 2 {
		t.Errorf("unexpected first section: %+v", sections[0])
	}
	if sections[1].ID != "lifecycle" || sections[1].Label != "Lifecycle" || len(sections[1].Items) != 4 {
		t.Errorf("unexpected second section: %+v", sections[1])
	}
}

func TestRenderer_PluginIntegrationAndHTTPHandler(t *testing.T) {
	reg := plugin.NewRegistry()
	zfsPlugin := &testMenuPlugin{
		id:       "storage/zfs",
		kind:     plugin.KindStorage,
		osFamily: "linux",
	}
	if err := reg.Register(zfsPlugin); err != nil {
		t.Fatalf("failed to register test plugin: %v", err)
	}

	resolver := &fakeNodeResolver{
		facts: map[string]plugin.HostFacts{
			"node-linux":   {NodeID: "node-linux", OSFamily: "linux"},
			"node-windows": {NodeID: "node-windows", OSFamily: "windows"},
		},
	}

	renderer := contextmenu.NewRenderer(contextmenu.RendererOptions{
		Registry:     reg,
		NodeResolver: resolver,
	})
	renderer.RegisterStaticItems(contextmenu.MenuItem{
		ID:       "node-ssh",
		PluginID: "connection/ssh",
		Target:   contextmenu.TargetNode,
		Group:    "access",
		Label:    "Open SSH Terminal",
		Action:   "open_terminal",
		Order:    1,
	})

	// 1. Render storage menu for compatible Linux node
	menu, err := renderer.Render(context.Background(), contextmenu.RenderRequest{
		Target:     contextmenu.TargetStorage,
		NodeID:     "node-linux",
		ResourceID: "tank/data",
	})
	if err != nil {
		t.Fatalf("Render failed: %v", err)
	}
	if len(menu.Items) != 4 {
		t.Fatalf("expected 4 storage menu items for linux node, got %d", len(menu.Items))
	}
	if menu.Items[0].ID != "snapshots" || menu.Items[0].Params["resourceId"] != "tank/data" {
		t.Errorf("expected resourceId param bound to menu items, got %+v", menu.Items[0])
	}

	// 2. Render storage menu for incompatible Windows node -> should filter out linux-only plugin
	winMenu, err := renderer.Render(context.Background(), contextmenu.RenderRequest{
		Target: contextmenu.TargetStorage,
		NodeID: "node-windows",
	})
	if err != nil {
		t.Fatalf("Render failed for windows node: %v", err)
	}
	if len(winMenu.Items) != 0 {
		t.Fatalf("expected 0 storage items for incompatible windows node, got %d", len(winMenu.Items))
	}

	// 3. Verify HTTP API endpoints
	mux := http.NewServeMux()
	api := contextmenu.NewAPI(renderer)
	api.Register(mux)

	req := httptest.NewRequest(http.MethodGet, "/api/context-menus?target=storage&nodeId=node-linux&resourceId=tank", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", rec.Code, rec.Body.String())
	}

	var decoded contextmenu.RenderedMenu
	if err := json.Unmarshal(rec.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("failed to decode response JSON: %v", err)
	}
	if decoded.Target != contextmenu.TargetStorage || len(decoded.Items) != 4 || len(decoded.Sections) != 1 {
		t.Errorf("unexpected decoded menu response: %+v", decoded)
	}
	if decoded.Sections[0].Label != "Pool Ops" {
		t.Errorf("expected formatted section label 'Pool Ops', got %q", decoded.Sections[0].Label)
	}

	// Path-based target & targets list
	targetsReq := httptest.NewRequest(http.MethodGet, "/api/context-menus/targets", nil)
	targetsRec := httptest.NewRecorder()
	mux.ServeHTTP(targetsRec, targetsReq)
	if targetsRec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for /api/context-menus/targets, got %d", targetsRec.Code)
	}

	badReq := httptest.NewRequest(http.MethodGet, "/api/context-menus?target=nonexistent", nil)
	badRec := httptest.NewRecorder()
	mux.ServeHTTP(badRec, badReq)
	if badRec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request for invalid target, got %d", badRec.Code)
	}
}
