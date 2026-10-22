package podman_test

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

func TestPodmanPlugin_ProbeAndCompile(t *testing.T) {
	p, ok := plugin.Get("container/podman")
	if !ok {
		t.Fatalf("container/podman not registered")
	}

	runner := &mockRunner{
		responses: map[string]string{
			"command -v podman": "podman",
			"podman ps -q":      "c1\nc2\nc3",
		},
	}

	det, err := p.Probe(context.Background(), runner, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatal(err)
	}
	if !det.Detected || !strings.Contains(det.Status, "3 container") {
		t.Errorf("unexpected probe result: %+v", det)
	}

	actor := p.(plugin.Actor)
	task, err := actor.Compile(plugin.HostFacts{NodeID: "node1"}, "restart", map[string]any{
		"name": "caddy",
		"user": "apps",
	})
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}
	if len(task.Steps) != 1 || task.Steps[0].Module != "containers.podman.podman_container" {
		t.Errorf("unexpected task steps: %+v", task.Steps)
	}
	if task.Steps[0].Params["become_user"] != "apps" {
		t.Errorf("expected become_user apps, got %v", task.Steps[0].Params["become_user"])
	}
}

func TestPodmanPlugin_ReadBothRootfulAndRootlessContainers(t *testing.T) {
	p, ok := plugin.Get("container/podman")
	if !ok {
		t.Fatalf("container/podman not registered")
	}

	reader, ok := p.(plugin.Reader)
	if !ok {
		t.Fatalf("container/podman does not implement plugin.Reader")
	}

	runner := &mockRunner{
		responses: map[string]string{
			"--unix-socket=/run/podman/podman.sock \"http://d/v4.0.0/libpod/containers/json?all=true\"": `[
				{"Id": "e377a55482d2", "Names": ["systemd-caddy"], "Image": "ghcr.io/braccae/caddy:latest", "State": "running", "Status": "healthy", "IsInfra": false}
			]`,
			"--unix-socket=/run/podman/podman.sock \"http://d/v4.0.0/libpod/pods/json\"": `[
				{"Id": "f4e8950bc5d4", "Name": "systemd-proxy", "Status": "Running"}
			]`,
			"/etc/passwd": "root:x:0:0:root:/root:/bin/bash\napps:x:2200:2200:Apps:/home/apps:/bin/bash\n",
			"ls -1d /run/user/*/podman/podman.sock": "/run/user/2200/podman/podman.sock\n",
			"--unix-socket=/run/user/2200/podman/podman.sock \"http://d/v4.0.0/libpod/containers/json?all=true\"": `[
				{"Id": "869408baab38", "Names": ["FeedCord"], "Image": "docker.io/qolors/feedcord:latest", "State": "running", "Status": "Up 3 days", "IsInfra": false}
			]`,
		},
	}

	raw, err := reader.Read(context.Background(), runner, plugin.HostFacts{NodeID: "node-1", OSFamily: "linux"})
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}

	data, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any from Read, got %T", raw)
	}

	if _, hasRoot := data["rootContainers"]; !hasRoot {
		t.Errorf("expected rootContainers key in Read output")
	}
	if _, hasProbed := data["probedUsers"]; !hasProbed {
		t.Errorf("expected probedUsers key in Read output")
	}
}

func TestPodmanPlugin_V2InterfacesAndCapabilities(t *testing.T) {
	p, ok := plugin.Get("container/podman")
	if !ok {
		t.Fatalf("container/podman not registered")
	}

	// 1. DataCollectionPlugin
	dc, ok := p.(newapi.DataCollectionPlugin)
	if !ok {
		t.Fatal("container/podman does not implement newapi.DataCollectionPlugin")
	}
	sched := dc.CollectionSchedule()
	if sched.Interval <= 0 || sched.TTL <= 0 {
		t.Errorf("expected positive collection schedule interval and TTL, got %+v", sched)
	}
	schema := dc.CollectionSchema()
	if schema.Version != "v2" || schema.RootKey != "containers" {
		t.Errorf("unexpected collection schema: %+v", schema)
	}

	// 2. CategorizedSettingsProvider
	csp, ok := p.(newapi.CategorizedSettingsProvider)
	if !ok {
		t.Fatal("container/podman does not implement newapi.CategorizedSettingsProvider")
	}
	catPage := csp.CategorizedSettingsPage()
	if catPage.ID != "container/podman" || catPage.Category != "container" || catPage.Scope != newapi.SettingsScopeDatacenter {
		t.Errorf("unexpected categorized settings page: %+v", catPage)
	}
	if err := settingsframework.ValidateSchema(catPage.SettingsPage); err != nil {
		t.Fatalf("ValidateSchema failed: %v", err)
	}
	if err := newapi.ValidateCategorizedSettingsPage(catPage); err != nil {
		t.Fatalf("ValidateCategorizedSettingsPage failed: %v", err)
	}

	// 3. ManagementPlugin
	mp, ok := p.(newapi.ManagementPlugin)
	if !ok {
		t.Fatal("container/podman does not implement newapi.ManagementPlugin")
	}
	mgmtActions := mp.ManagementActions()
	if len(mgmtActions) != 5 {
		t.Fatalf("expected 5 management actions, got %d", len(mgmtActions))
	}
	if len(mp.Actions()) != len(mgmtActions) {
		t.Errorf("Actions() length %d does not match ManagementActions() length %d", len(mp.Actions()), len(mgmtActions))
	}

	linuxFacts := plugin.HostFacts{NodeID: "node-1", OSFamily: "linux"}
	if err := mp.ValidateAction(linuxFacts, "restart", map[string]any{"name": "caddy"}); err != nil {
		t.Errorf("expected valid restart options, got: %v", err)
	}
	if err := mp.ValidateAction(linuxFacts, "rm", map[string]any{"container": "caddy"}); err != nil {
		t.Errorf("expected valid rm alias with container fallback, got: %v", err)
	}
	if err := mp.ValidateAction(linuxFacts, "restart", map[string]any{}); err == nil {
		t.Error("expected validation error when required name option is missing")
	}
	if err := mp.ValidateAction(linuxFacts, "nonexistent", map[string]any{"name": "caddy"}); err == nil {
		t.Error("expected validation error for unsupported action")
	}

	// 4. ContextMenuProvider
	cmp, ok := p.(newapi.ContextMenuProvider)
	if !ok {
		t.Fatal("container/podman does not implement newapi.ContextMenuProvider")
	}
	entries := cmp.ContextMenuEntries(context.Background(), linuxFacts)
	if len(entries) != 5 {
		t.Fatalf("expected 5 context menu entries for compatible linux host, got %d", len(entries))
	}
	for _, entry := range entries {
		if entry.PluginID != "container/podman" || entry.Target != newapi.TargetContainer {
			t.Errorf("unexpected context menu entry metadata: %+v", entry)
		}
	}
	if entries[len(entries)-1].Action != "delete" || !entries[len(entries)-1].Danger {
		t.Errorf("expected last context menu entry to be danger delete action, got %+v", entries[len(entries)-1])
	}

	winEntries := cmp.ContextMenuEntries(context.Background(), plugin.HostFacts{NodeID: "win-1", OSFamily: "windows"})
	if len(winEntries) != 0 {
		t.Errorf("expected no context menu entries for incompatible windows host, got %+v", winEntries)
	}

	// 5. TaskCompileProvider
	tcp, ok := p.(newapi.TaskCompileProvider)
	if !ok {
		t.Fatal("container/podman does not implement newapi.TaskCompileProvider")
	}
	spec, err := tcp.CompileTask(context.Background(), linuxFacts, newapi.TaskRequest{
		ID:       "task-1",
		Type:     newapi.TaskTypePluginAction,
		NodeID:   "node-1",
		PluginID: "container/podman",
		Action:   "restart_pod",
		Params:   map[string]any{"name": "proxy-pod", "user": "apps"},
	})
	if err != nil {
		t.Fatalf("CompileTask failed: %v", err)
	}
	if len(spec.Steps) != 1 || spec.Steps[0].Module != "containers.podman.podman_pod" {
		t.Errorf("unexpected compiled task steps: %+v", spec.Steps)
	}
	if spec.Steps[0].Params["become_user"] != "apps" {
		t.Errorf("expected become_user=apps in CompileTask step, got %+v", spec.Steps[0].Params)
	}
}
