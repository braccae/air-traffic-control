package docker_test

import (
	"context"
	"strings"
	"testing"

	"air-traffic-control/backend/pkg/plugin"
	"air-traffic-control/backend/pkg/plugin/newapi"
	"air-traffic-control/backend/pkg/plugin/settingsframework"
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

func TestDockerPlugin_Registered(t *testing.T) {
	p, ok := plugin.Get("container/docker")
	if !ok {
		t.Fatalf("container/docker plugin not registered")
	}
	if p.Meta().Kind != plugin.KindContainer {
		t.Errorf("unexpected kind: %v", p.Meta().Kind)
	}
	if p.Meta().Name != "docker" {
		t.Errorf("unexpected name: %v", p.Meta().Name)
	}
}

func TestDockerPlugin_ProbeAndCompile(t *testing.T) {
	p, ok := plugin.Get("container/docker")
	if !ok {
		t.Fatalf("container/docker not registered")
	}

	runner := &mockRunner{
		responses: map[string]string{
			"command -v docker": "/usr/bin/docker",
			"docker ps -a -q":   "5\n",
		},
	}

	det, err := p.Probe(context.Background(), runner, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatal(err)
	}
	if !det.Detected || !strings.Contains(det.Status, "5 container") {
		t.Errorf("unexpected probe result: %+v", det)
	}

	actor, ok := p.(plugin.Actor)
	if !ok {
		t.Fatalf("docker plugin does not implement Actor")
	}

	// 1. Restart container (declarative community.docker.docker_container)
	task, err := actor.Compile(plugin.HostFacts{NodeID: "likes-it-cold"}, "restart", map[string]any{
		"name": "nextcloud-aio-apache",
		"user": "root",
	})
	if err != nil {
		t.Fatalf("compile restart failed: %v", err)
	}
	if len(task.Steps) != 1 || task.Steps[0].Module != "community.docker.docker_container" {
		t.Errorf("unexpected task steps: %+v", task.Steps)
	}
	if task.Steps[0].Params["name"] != "nextcloud-aio-apache" || task.Steps[0].Params["restart"] != true {
		t.Errorf("unexpected params: %+v", task.Steps[0].Params)
	}

	// 2. Start container with rootless user become
	startTask, err := actor.Compile(plugin.HostFacts{NodeID: "likes-it-cold"}, "start", map[string]any{
		"name": "rootless-app",
		"user": "apps",
	})
	if err != nil {
		t.Fatalf("compile start failed: %v", err)
	}
	if len(startTask.Steps) != 1 || startTask.Steps[0].Module != "community.docker.docker_container" {
		t.Errorf("unexpected start steps: %+v", startTask.Steps)
	}
	if startTask.Steps[0].Params["become_user"] != "apps" || startTask.Steps[0].Params["become"] != true {
		t.Errorf("expected become apps: %+v", startTask.Steps[0].Params)
	}

	// 3. Pull image (declarative community.docker.docker_image)
	pullTask, err := actor.Compile(plugin.HostFacts{NodeID: "likes-it-cold"}, "pull", map[string]any{
		"image": "alpine:latest",
	})
	if err != nil {
		t.Fatalf("compile pull failed: %v", err)
	}
	if len(pullTask.Steps) != 1 || pullTask.Steps[0].Module != "community.docker.docker_image" {
		t.Errorf("unexpected pull steps: %+v", pullTask.Steps)
	}
	if pullTask.Steps[0].Params["name"] != "alpine:latest" {
		t.Errorf("unexpected pull params: %+v", pullTask.Steps[0].Params)
	}

	// 4. Remove container
	rmTask, err := actor.Compile(plugin.HostFacts{NodeID: "likes-it-cold"}, "remove", map[string]any{
		"name": "old-container",
	})
	if err != nil {
		t.Fatalf("compile remove failed: %v", err)
	}
	if len(rmTask.Steps) != 1 || rmTask.Steps[0].Params["state"] != "absent" {
		t.Errorf("unexpected remove params: %+v", rmTask.Steps[0].Params)
	}
}

func TestDockerPlugin_V2NewAPIInterfaces(t *testing.T) {
	p, ok := plugin.Get("container/docker")
	if !ok {
		t.Fatalf("container/docker not registered")
	}

	// 1. DataCollectionPlugin
	dc, ok := p.(newapi.DataCollectionPlugin)
	if !ok {
		t.Fatalf("expected container/docker to implement newapi.DataCollectionPlugin")
	}
	sched := dc.CollectionSchedule()
	if sched.Interval <= 0 || sched.TTL <= 0 {
		t.Fatalf("expected positive schedule Interval and TTL, got %+v", sched)
	}
	schema := dc.CollectionSchema()
	if schema.Version != "v2" || schema.RootKey != "containers" {
		t.Fatalf("unexpected collection schema: %+v", schema)
	}

	// 2. ManagementPlugin
	mgmt, ok := p.(newapi.ManagementPlugin)
	if !ok {
		t.Fatalf("expected container/docker to implement newapi.ManagementPlugin")
	}
	mgmtActions := mgmt.ManagementActions()
	if len(mgmtActions) != 5 {
		t.Fatalf("expected 5 management actions, got %d", len(mgmtActions))
	}
	legacyActions := mgmt.Actions()
	if len(legacyActions) != len(mgmtActions) {
		t.Fatalf("Actions() length %d does not match ManagementActions() length %d", len(legacyActions), len(mgmtActions))
	}

	facts := plugin.HostFacts{NodeID: "likes-it-cold", OSFamily: "linux"}
	if err := mgmt.ValidateAction(facts, "start", map[string]any{"name": "web"}); err != nil {
		t.Fatalf("expected valid start options, got error: %v", err)
	}
	if err := mgmt.ValidateAction(facts, "start", map[string]any{"container": "web"}); err != nil {
		t.Fatalf("expected valid start options via container alias, got error: %v", err)
	}
	if err := mgmt.ValidateAction(facts, "start", map[string]any{}); err == nil {
		t.Fatalf("expected error when required name option is missing")
	}
	if err := mgmt.ValidateAction(facts, "pull", map[string]any{"image": "alpine:latest"}); err != nil {
		t.Fatalf("expected valid pull options, got error: %v", err)
	}
	if err := mgmt.ValidateAction(facts, "pull", map[string]any{}); err == nil {
		t.Fatalf("expected error when required image option is missing")
	}
	if err := mgmt.ValidateAction(facts, "unknown_action", map[string]any{}); err == nil {
		t.Fatalf("expected error for unknown action")
	}

	// 3. CategorizedSettingsProvider
	csp, ok := p.(newapi.CategorizedSettingsProvider)
	if !ok {
		t.Fatalf("expected container/docker to implement newapi.CategorizedSettingsProvider")
	}
	catPage := csp.CategorizedSettingsPage()
	if err := settingsframework.ValidateSchema(csp.SettingsPage()); err != nil {
		t.Fatalf("settings schema validation failed: %v", err)
	}
	if err := newapi.ValidateCategorizedSettingsPage(catPage); err != nil {
		t.Fatalf("categorized settings page validation failed: %v", err)
	}
	if catPage.ID != "container/docker" || catPage.Category != "container" || catPage.Scope != newapi.SettingsScopeDatacenter {
		t.Fatalf("unexpected categorized settings page metadata: %+v", catPage)
	}
	if _, ok := plugin.GetCategorizedSettingsPage("container/docker"); !ok {
		t.Fatalf("expected container/docker categorized settings page to be registered in default registry")
	}

	// 4. ContextMenuProvider
	cmp, ok := p.(newapi.ContextMenuProvider)
	if !ok {
		t.Fatalf("expected container/docker to implement newapi.ContextMenuProvider")
	}
	entries := cmp.ContextMenuEntries(context.Background(), facts)
	if len(entries) != 5 {
		t.Fatalf("expected 5 context menu entries for compatible linux host, got %d", len(entries))
	}
	for _, entry := range entries {
		if entry.PluginID != "container/docker" || entry.Target != newapi.TargetContainer || entry.Action == "" {
			t.Errorf("unexpected context menu entry: %+v", entry)
		}
	}
	if incompat := cmp.ContextMenuEntries(context.Background(), plugin.HostFacts{OSFamily: "windows"}); len(incompat) != 0 {
		t.Fatalf("expected no context menu entries for incompatible windows host, got %+v", incompat)
	}

	// 5. TaskCompileProvider
	tcp, ok := p.(newapi.TaskCompileProvider)
	if !ok {
		t.Fatalf("expected container/docker to implement newapi.TaskCompileProvider")
	}
	compiledTask, err := tcp.CompileTask(context.Background(), facts, newapi.TaskRequest{
		Type:     newapi.TaskTypePluginAction,
		PluginID: "container/docker",
		Action:   "restart",
		NodeID:   "likes-it-cold",
		Params: map[string]any{
			"name": "nextcloud-aio-apache",
			"user": "apps",
		},
	})
	if err != nil {
		t.Fatalf("CompileTask failed: %v", err)
	}
	if len(compiledTask.Steps) != 1 || compiledTask.Steps[0].Module != "community.docker.docker_container" {
		t.Fatalf("unexpected CompileTask output: %+v", compiledTask)
	}
	if compiledTask.Steps[0].Params["become_user"] != "apps" {
		t.Errorf("expected become_user=apps in CompileTask output, got %+v", compiledTask.Steps[0].Params)
	}

	// Fallback action from Params["action"]
	compiledPull, err := tcp.CompileTask(context.Background(), facts, newapi.TaskRequest{
		Type:     newapi.TaskTypePluginAction,
		PluginID: "container/docker",
		NodeID:   "likes-it-cold",
		Params: map[string]any{
			"action": "pull",
			"image":  "redis:7-alpine",
		},
	})
	if err != nil {
		t.Fatalf("CompileTask with params[action] failed: %v", err)
	}
	if len(compiledPull.Steps) != 1 || compiledPull.Steps[0].Module != "community.docker.docker_image" {
		t.Fatalf("unexpected CompileTask pull output: %+v", compiledPull)
	}
}
