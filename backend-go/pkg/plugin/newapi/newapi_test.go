package newapi_test

import (
	"context"
	"testing"
	"time"

	"air-traffic-control/backend/pkg/plugin"
	"air-traffic-control/backend/pkg/plugin/newapi"
)

type samplePlugin struct{}

func (s *samplePlugin) Meta() plugin.Meta {
	return plugin.Meta{
		ID:          "storage/zfs",
		Kind:        plugin.KindStorage,
		Name:        "zfs",
		Label:       "ZFS",
		Icon:        "database",
		Description: "OpenZFS storage pool and dataset management",
		Order:       10,
	}
}

func (s *samplePlugin) Compatible(h plugin.HostFacts) bool {
	return h.OSFamily == "linux"
}

func (s *samplePlugin) Requirements() plugin.Requirements {
	return plugin.Requirements{
		HostPackages:          map[string][]string{"dnf": {"zfs"}},
		ControllerCollections: []string{"community.general"},
	}
}

func (s *samplePlugin) Probe(ctx context.Context, r plugin.Runner, h plugin.HostFacts) (plugin.Detection, error) {
	return plugin.Detection{Detected: true, Status: "ONLINE"}, nil
}

func (s *samplePlugin) Read(ctx context.Context, r plugin.Runner, h plugin.HostFacts) (any, error) {
	return map[string]any{"pools": []string{"tank"}}, nil
}

func (s *samplePlugin) CollectionSchedule() newapi.CollectionSchedule {
	return newapi.DefaultScheduleForKind(plugin.KindStorage)
}

func (s *samplePlugin) CollectionSchema() newapi.CollectionSchema {
	return newapi.CollectionSchema{
		Version:     "v1",
		RootKey:     "pools",
		Description: "ZFS pools and datasets hierarchy",
	}
}

func (s *samplePlugin) Actions() []plugin.ActionSpec {
	actions := s.ManagementActions()
	out := make([]plugin.ActionSpec, 0, len(actions))
	for _, a := range actions {
		out = append(out, a.ActionSpec)
	}
	return out
}

func (s *samplePlugin) ManagementActions() []newapi.ManagementActionSpec {
	return []newapi.ManagementActionSpec{
		{
			ActionSpec: plugin.ActionSpec{
				Name:        "scrub_pool",
				Label:       "Scrub Pool",
				Description: "Start or stop a ZFS pool scrub",
				Options: []plugin.OptionSchema{
					{Name: "pool", Type: "string", Required: true},
					{Name: "state", Type: "select", Required: true, Enum: []string{"started", "stopped"}},
				},
			},
			Category:         newapi.ActionCategoryMaintenance,
			Idempotent:       true,
			Dangerous:        false,
			InvalidatesKinds: []plugin.Kind{plugin.KindStorage},
		},
	}
}

func (s *samplePlugin) ValidateAction(h plugin.HostFacts, action string, opts map[string]any) error {
	for _, spec := range s.ManagementActions() {
		if spec.Name == action {
			return newapi.ValidateManagementOptions(spec, opts)
		}
	}
	return nil
}

func (s *samplePlugin) Compile(h plugin.HostFacts, action string, opts map[string]any) (plugin.TaskSpec, error) {
	if err := s.ValidateAction(h, action, opts); err != nil {
		return plugin.TaskSpec{}, err
	}
	return plugin.TaskSpec{
		Name: "Scrub ZFS pool",
		Steps: []plugin.TaskStep{
			{
				Name:   "Run zpool scrub",
				Module: "ansible.builtin.command",
				Params: map[string]any{"cmd": "zpool scrub " + opts["pool"].(string)},
			},
		},
	}, nil
}

func (s *samplePlugin) SettingsPage() plugin.SettingsPage {
	return plugin.SettingsPage{
		ID:          "storage/zfs",
		Title:       "ZFS Settings",
		Icon:        "database",
		Description: "Global ZFS scrub and snapshot thresholds",
		Order:       10,
		Fields: []plugin.SettingsField{
			{Name: "auto_scrub_days", Label: "Auto Scrub Interval (days)", Type: plugin.SettingsFieldInt, Default: 30},
		},
	}
}

func (s *samplePlugin) CategorizedSettingsPage() newapi.CategorizedSettingsPage {
	return newapi.CategorizedSettingsPage{
		SettingsPage: s.SettingsPage(),
		Scope:        newapi.SettingsScopeDatacenter,
		Category:     "storage",
	}
}

func (s *samplePlugin) ContextMenuEntries(ctx context.Context, h plugin.HostFacts) []newapi.ContextMenuEntry {
	return newapi.NewContextMenuBuilder(s.Meta().ID).
		WithTarget(newapi.TargetStorage).
		WithGroup("zfs").
		AddAction("zfs-scrub", "Scrub Pool", "cleaning_services", "scrub_pool", 20).
		AddDangerAction("zfs-destroy", "Destroy Dataset", "delete", "destroy_dataset", "Destroy dataset permanently?", 10).
		AddSubmenu("zfs-snapshots", "Snapshots", "photo_camera", 15,
			newapi.ContextMenuEntry{ID: "snap-create", Label: "Create Snapshot", Order: 2},
			newapi.ContextMenuEntry{ID: "snap-rollback", Label: "Rollback Snapshot", Order: 1},
		).
		Build()
}

func (s *samplePlugin) NodeTypes() []newapi.NodeTypeSpec {
	return newapi.DefaultNodeTypes()
}

func (s *samplePlugin) TaskTypes() []newapi.TaskTypeSpec {
	return []newapi.TaskTypeSpec{
		{
			Type:           newapi.TaskTypeStorageOp,
			Label:          "ZFS Storage Operation",
			PluginID:       s.Meta().ID,
			SupportsDryRun: true,
			SupportsCancel: true,
			Parameters: []plugin.OptionSchema{
				{Name: "pool", Type: "string", Required: true},
				{Name: "op", Type: "select", Required: true, Enum: []string{"scrub", "trim"}},
			},
		},
	}
}

func TestNewAPIContracts_CompositePlugin(t *testing.T) {
	var p any = &samplePlugin{}

	if _, ok := p.(newapi.ManagementPlugin); !ok {
		t.Fatal("samplePlugin should satisfy newapi.ManagementPlugin")
	}
	if _, ok := p.(newapi.DataCollectionPlugin); !ok {
		t.Fatal("samplePlugin should satisfy newapi.DataCollectionPlugin")
	}
	if _, ok := p.(newapi.CategorizedSettingsProvider); !ok {
		t.Fatal("samplePlugin should satisfy newapi.CategorizedSettingsProvider")
	}
	if _, ok := p.(newapi.ContextMenuProvider); !ok {
		t.Fatal("samplePlugin should satisfy newapi.ContextMenuProvider")
	}
	if _, ok := p.(newapi.NodeTypeProvider); !ok {
		t.Fatal("samplePlugin should satisfy newapi.NodeTypeProvider")
	}
	if _, ok := p.(newapi.TaskTypeProvider); !ok {
		t.Fatal("samplePlugin should satisfy newapi.TaskTypeProvider")
	}
}

func TestNodeTypes_DefaultAndValidation(t *testing.T) {
	defaults := newapi.DefaultNodeTypes()
	if len(defaults) != 7 {
		t.Fatalf("expected 7 default node types, got %d", len(defaults))
	}
	for _, spec := range defaults {
		if err := newapi.ValidateNodeTypeSpec(spec); err != nil {
			t.Errorf("default node type %q failed validation: %v", spec.Type, err)
		}
	}

	if err := newapi.ValidateNodeTypeSpec(newapi.NodeTypeSpec{}); err == nil {
		t.Error("expected empty NodeTypeSpec to fail validation")
	}
}

type legacySettingsProvider struct{}

func (l *legacySettingsProvider) SettingsPage() plugin.SettingsPage {
	return plugin.SettingsPage{
		ID:    "backup/restic",
		Title: "Restic Backup",
	}
}

func TestCategorizedSettings_ResolutionAndValidation(t *testing.T) {
	sp := &samplePlugin{}
	catPage := newapi.ResolveCategorizedSettingsPage(sp)
	if catPage.Scope != newapi.SettingsScopeDatacenter || catPage.Category != "storage" {
		t.Fatalf("unexpected categorized page: %+v", catPage)
	}
	if err := newapi.ValidateCategorizedSettingsPage(catPage); err != nil {
		t.Fatalf("expected valid categorized page, got: %v", err)
	}

	legacy := &legacySettingsProvider{}
	upgraded := newapi.ResolveCategorizedSettingsPage(legacy)
	if upgraded.Scope != newapi.SettingsScopeDatacenter {
		t.Errorf("expected default scope datacenter, got %q", upgraded.Scope)
	}
	if upgraded.Category != "backup" {
		t.Errorf("expected inferred category 'backup', got %q", upgraded.Category)
	}
}

func TestContextMenuBuilder_SortingAndDefaults(t *testing.T) {
	sp := &samplePlugin{}
	entries := sp.ContextMenuEntries(context.Background(), plugin.HostFacts{NodeID: "node-1"})
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}
	if entries[0].ID != "zfs-destroy" || !entries[0].Danger {
		t.Errorf("expected danger entry first (order 10), got %+v", entries[0])
	}
	if entries[1].ID != "zfs-snapshots" || len(entries[1].Children) != 2 {
		t.Errorf("expected submenu second (order 15) with 2 children, got %+v", entries[1])
	}
	if entries[1].Children[0].ID != "snap-rollback" {
		t.Errorf("expected submenu children sorted by order, got %+v", entries[1].Children)
	}
	if entries[2].ID != "zfs-scrub" || entries[2].PluginID != "storage/zfs" {
		t.Errorf("expected action entry third (order 20), got %+v", entries[2])
	}
}

func TestManagementValidation_AndSchedule(t *testing.T) {
	sp := &samplePlugin{}
	facts := plugin.HostFacts{NodeID: "node-1", OSFamily: "linux"}

	if err := sp.ValidateAction(facts, "scrub_pool", map[string]any{"pool": "tank", "state": "started"}); err != nil {
		t.Fatalf("expected valid options, got: %v", err)
	}
	if err := sp.ValidateAction(facts, "scrub_pool", map[string]any{"pool": "tank"}); err == nil {
		t.Fatal("expected error for missing required 'state' option")
	}
	if err := sp.ValidateAction(facts, "scrub_pool", map[string]any{"pool": "tank", "state": "invalid"}); err == nil {
		t.Fatal("expected error for invalid enum value")
	}

	sched := sp.CollectionSchedule()
	if sched.TTL != 60*time.Second {
		t.Errorf("expected 60s TTL for storage kind, got %v", sched.TTL)
	}
	telSched := newapi.DefaultScheduleForKind(plugin.KindTelemetry)
	if telSched.TTL != 15*time.Second || !telSched.Realtime {
		t.Errorf("expected 15s realtime schedule for telemetry, got %+v", telSched)
	}
}

func TestTaskTypes_Validation(t *testing.T) {
	sp := &samplePlugin{}
	spec := sp.TaskTypes()[0]

	validReq := newapi.TaskRequest{
		ID:       "req-1",
		Type:     newapi.TaskTypeStorageOp,
		NodeID:   "node-1",
		PluginID: "storage/zfs",
		Params:   map[string]any{"pool": "tank", "op": "scrub"},
		DryRun:   true,
	}
	if err := newapi.ValidateTaskRequest(spec, validReq); err != nil {
		t.Fatalf("expected valid TaskRequest, got: %v", err)
	}

	badReq := validReq
	badReq.Params = map[string]any{"pool": "tank", "op": "unknown"}
	if err := newapi.ValidateTaskRequest(spec, badReq); err == nil {
		t.Fatal("expected error for invalid task parameter enum")
	}
}
