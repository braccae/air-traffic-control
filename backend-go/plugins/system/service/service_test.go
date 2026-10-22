package service_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"air-traffic-control/backend/pkg/plugin"
	"air-traffic-control/backend/pkg/plugin/newapi"
	"air-traffic-control/backend/pkg/plugin/settingsframework"
	_ "air-traffic-control/backend/plugins/all"
	"air-traffic-control/backend/plugins/system/service"
)

type mockRunner struct {
	responses map[string]string
	errs      map[string]error
}

func (m *mockRunner) RunCommand(ctx context.Context, cmd string) (string, error) {
	if err, ok := m.errs[cmd]; ok && err != nil {
		return m.responses[cmd], err
	}
	if resp, ok := m.responses[cmd]; ok {
		return resp, nil
	}
	// Error patterns win over response patterns so a scoped command can fail
	// while its unscoped sibling succeeds.
	for pattern, err := range m.errs {
		if err != nil && strings.Contains(cmd, pattern) {
			return m.responses[pattern], err
		}
	}
	for pattern, resp := range m.responses {
		if strings.Contains(cmd, pattern) {
			return resp, nil
		}
	}
	return "", fmt.Errorf("command not found: %s", cmd)
}

func TestServicePlugin_RegistrationAndMeta(t *testing.T) {
	p, ok := plugin.Get("system/service")
	if !ok {
		t.Fatal("plugin system/service not registered")
	}

	meta := p.Meta()
	if meta.ID != "system/service" {
		t.Errorf("expected ID 'system/service', got %q", meta.ID)
	}
	if meta.Kind != plugin.KindSystem {
		t.Errorf("expected Kind 'system', got %q", meta.Kind)
	}

	if !p.Compatible(plugin.HostFacts{OSFamily: "linux"}) {
		t.Error("expected system/service to be compatible with linux")
	}
	if p.Compatible(plugin.HostFacts{OSFamily: "windows"}) {
		t.Error("expected system/service to be incompatible with windows")
	}
	if p.Compatible(plugin.HostFacts{OSFamily: "router_switch"}) {
		t.Error("expected system/service to be incompatible with router_switch")
	}

	actor, ok := p.(plugin.Actor)
	if !ok {
		t.Fatalf("expected system/service to implement plugin.Actor")
	}

	actions := actor.Actions()
	if len(actions) == 0 {
		t.Fatalf("expected actions list, got 0")
	}
}

func TestServicePlugin_Probe(t *testing.T) {
	ctx := context.Background()
	p, ok := plugin.Get("system/service")
	if !ok {
		t.Fatal("plugin system/service not registered")
	}

	t.Run("nil runner offline", func(t *testing.T) {
		det, err := p.Probe(ctx, nil, plugin.HostFacts{OSFamily: "linux"})
		if err != nil {
			t.Fatalf("Probe error: %v", err)
		}
		if det.Detected || det.Status != "OFFLINE" {
			t.Errorf("expected OFFLINE for nil runner, got %+v", det)
		}
	})

	t.Run("systemd detected", func(t *testing.T) {
		runner := &mockRunner{
			responses: map[string]string{
				"systemctl": "/usr/bin/systemctl\n",
			},
		}
		det, err := p.Probe(ctx, runner, plugin.HostFacts{OSFamily: "linux"})
		if err != nil {
			t.Fatalf("Probe error: %v", err)
		}
		if !det.Detected {
			t.Errorf("expected systemctl to be detected")
		}
	})

	t.Run("service fallback detected", func(t *testing.T) {
		runner := &mockRunner{
			responses: map[string]string{
				"service": "/sbin/service\n",
			},
			errs: map[string]error{
				"systemctl": fmt.Errorf("systemctl not found"),
			},
		}
		det, err := p.Probe(ctx, runner, plugin.HostFacts{OSFamily: "linux"})
		if err != nil {
			t.Fatalf("Probe error: %v", err)
		}
		if !det.Detected {
			t.Errorf("expected service to be detected")
		}
	})

	t.Run("neither detected unavailable", func(t *testing.T) {
		runner := &mockRunner{
			errs: map[string]error{
				"systemctl": fmt.Errorf("not found"),
				"service":   fmt.Errorf("not found"),
			},
		}
		det, err := p.Probe(ctx, runner, plugin.HostFacts{OSFamily: "linux"})
		if err != nil {
			t.Fatalf("Probe error: %v", err)
		}
		if det.Detected || det.Status != "UNAVAILABLE" {
			t.Errorf("expected UNAVAILABLE, got %+v", det)
		}
	})
}

func TestServicePlugin_Compile(t *testing.T) {
	p, ok := plugin.Get("system/service")
	if !ok {
		t.Fatal("plugin system/service not registered")
	}
	actor := p.(plugin.Actor)

	actions := []struct {
		action        string
		expectedState string
	}{
		{"start", "started"},
		{"stop", "stopped"},
		{"restart", "restarted"},
		{"reload", "reloaded"},
	}

	for _, tc := range actions {
		t.Run(tc.action, func(t *testing.T) {
			spec, err := actor.Compile(plugin.HostFacts{OSFamily: "linux"}, tc.action, map[string]any{
				"name": "caddy",
			})
			if err != nil {
				t.Fatalf("Compile error: %v", err)
			}
			if len(spec.Steps) == 0 {
				t.Fatalf("expected steps, got 0")
			}
			if spec.Steps[0].Module != "ansible.builtin.systemd_service" {
				t.Errorf("expected ansible.builtin.systemd_service, got %s", spec.Steps[0].Module)
			}
			if spec.Steps[0].Params["name"] != "caddy.service" {
				t.Errorf("expected normalised unit name caddy.service, got %v", spec.Steps[0].Params["name"])
			}
			if spec.Steps[0].Params["state"] != tc.expectedState {
				t.Errorf("expected state %s, got %v", tc.expectedState, spec.Steps[0].Params["state"])
			}
		})
	}

	t.Run("enable and disable", func(t *testing.T) {
		enableSpec, err := actor.Compile(plugin.HostFacts{OSFamily: "linux"}, "enable", map[string]any{
			"name": "caddy",
		})
		if err != nil {
			t.Fatalf("Compile error: %v", err)
		}
		if enableSpec.Steps[0].Params["enabled"] != true {
			t.Errorf("expected enabled=true, got %v", enableSpec.Steps[0].Params["enabled"])
		}

		disableSpec, err := actor.Compile(plugin.HostFacts{OSFamily: "linux"}, "disable", map[string]any{
			"name": "caddy",
		})
		if err != nil {
			t.Fatalf("Compile error: %v", err)
		}
		if disableSpec.Steps[0].Params["enabled"] != false {
			t.Errorf("expected enabled=false, got %v", disableSpec.Steps[0].Params["enabled"])
		}
	})

	t.Run("reboot action", func(t *testing.T) {
		spec, err := actor.Compile(plugin.HostFacts{OSFamily: "linux"}, "reboot", map[string]any{
			"timeout": 120,
			"msg":     "Operator reboot",
		})
		if err != nil {
			t.Fatalf("Compile error: %v", err)
		}
		if spec.Steps[0].Module != "ansible.builtin.reboot" {
			t.Errorf("expected ansible.builtin.reboot, got %s", spec.Steps[0].Module)
		}
	})

	t.Run("non-systemd host fallback", func(t *testing.T) {
		spec, err := actor.Compile(plugin.HostFacts{
			OSFamily: "linux",
			Overrides: map[string]string{
				"initSystem": "openrc",
			},
		}, "restart", map[string]any{
			"name": "caddy",
		})
		if err != nil {
			t.Fatalf("Compile error: %v", err)
		}
		if spec.Steps[0].Module != "ansible.builtin.service" {
			t.Errorf("expected ansible.builtin.service, got %s", spec.Steps[0].Module)
		}
	})

	t.Run("daemon-reload action", func(t *testing.T) {
		spec, err := actor.Compile(plugin.HostFacts{OSFamily: "linux"}, "daemon-reload", nil)
		if err != nil {
			t.Fatalf("Compile error: %v", err)
		}
		if spec.Steps[0].Params["daemon_reload"] != true {
			t.Errorf("expected daemon_reload=true, got %v", spec.Steps[0].Params["daemon_reload"])
		}
		if spec.Steps[0].Module != "ansible.builtin.systemd_service" {
			t.Errorf("expected ansible.builtin.systemd_service, got %s", spec.Steps[0].Module)
		}
	})

	t.Run("user scope restart has no become and sets scope", func(t *testing.T) {
		spec, err := actor.Compile(plugin.HostFacts{OSFamily: "linux"}, "restart", map[string]any{
			"name":  "gpg-agent.service",
			"scope": "user",
		})
		if err != nil {
			t.Fatalf("Compile error: %v", err)
		}
		params := spec.Steps[0].Params
		if params["scope"] != "user" {
			t.Errorf("expected scope=user, got %v", params["scope"])
		}
		if _, ok := params["become"]; ok {
			t.Errorf("user-scope unit actions must not request privilege escalation, got %v", params["become"])
		}
		if params["state"] != "restarted" {
			t.Errorf("expected state=restarted, got %v", params["state"])
		}
	})

	t.Run("invalid scope rejected", func(t *testing.T) {
		if _, err := actor.Compile(plugin.HostFacts{OSFamily: "linux"}, "restart", map[string]any{
			"name":  "caddy.service",
			"scope": "everything",
		}); err == nil {
			t.Fatal("expected error for unknown scope")
		}
	})

	t.Run("invalid unit name rejected before staging", func(t *testing.T) {
		if _, err := actor.Compile(plugin.HostFacts{OSFamily: "linux"}, "restart", map[string]any{
			"name": "caddy.service; reboot",
		}); err == nil {
			t.Fatal("expected error for injected unit name")
		}
	})

	t.Run("write-dropin targets /etc/systemd/system/<unit>.d", func(t *testing.T) {
		spec, err := actor.Compile(plugin.HostFacts{OSFamily: "linux"}, "write-dropin", map[string]any{
			"name":    "sshd",
			"dropin":  "atc-limits.conf",
			"content": "[Service]\nTasksMax=4096\n",
		})
		if err != nil {
			t.Fatalf("Compile error: %v", err)
		}
		if len(spec.Steps) != 2 {
			t.Fatalf("expected copy + daemon_reload steps, got %d", len(spec.Steps))
		}
		if spec.Steps[0].Module != "ansible.builtin.copy" {
			t.Errorf("expected ansible.builtin.copy, got %s", spec.Steps[0].Module)
		}
		if spec.Steps[0].Params["dest"] != "/etc/systemd/system/sshd.service.d/atc-limits.conf" {
			t.Errorf("unexpected drop-in destination: %v", spec.Steps[0].Params["dest"])
		}
		if spec.Steps[0].Params["become"] != true {
			t.Errorf("system drop-in write must escalate")
		}
		if spec.Steps[1].Module != "ansible.builtin.systemd_service" || spec.Steps[1].Params["daemon_reload"] != true {
			t.Errorf("expected daemon_reload step, got %+v", spec.Steps[1])
		}
	})

	t.Run("write-dropin rejects traversal and empty content", func(t *testing.T) {
		if _, err := actor.Compile(plugin.HostFacts{OSFamily: "linux"}, "write-dropin", map[string]any{
			"name":    "sshd.service",
			"dropin":  "../escape.conf",
			"content": "[Service]\nTasksMax=1\n",
		}); err == nil {
			t.Fatal("expected error for drop-in path traversal")
		}
		if _, err := actor.Compile(plugin.HostFacts{OSFamily: "linux"}, "write-dropin", map[string]any{
			"name":    "sshd.service",
			"content": "   ",
		}); err == nil {
			t.Fatal("expected error for empty drop-in content")
		}
	})
}

func TestServicePlugin_Read(t *testing.T) {
	ctx := context.Background()
	p, ok := plugin.Get("system/service")
	if !ok {
		t.Fatal("plugin system/service not registered")
	}

	reader, ok := p.(plugin.Reader)
	if !ok {
		t.Fatalf("expected system/service to implement plugin.Reader")
	}

	t.Run("systemd json read merges unit file state and scopes", func(t *testing.T) {
		listMock := `[{"unit":"sshd.service","load":"loaded","active":"active","sub":"running","description":"OpenSSH Server"}]`
		filesMock := `[{"unit_file":"sshd.service","state":"enabled","preset":"disabled"}]`
		runner := &mockRunner{
			responses: map[string]string{
				"list-units":      listMock,
				"list-unit-files": filesMock,
			},
			errs: map[string]error{
				"systemctl --user": fmt.Errorf("Failed to connect to bus: No medium found"),
			},
		}

		res, err := reader.Read(ctx, runner, plugin.HostFacts{OSFamily: "linux"})
		if err != nil {
			t.Fatalf("Read error: %v", err)
		}
		payload, ok := res.(service.ServiceReadPayload)
		if !ok {
			t.Fatalf("unexpected payload type %T", res)
		}
		if payload.Manager != "systemd" {
			t.Errorf("expected systemd manager, got %q", payload.Manager)
		}
		if len(payload.Services) != 1 {
			t.Fatalf("expected 1 unit, got %d", len(payload.Services))
		}
		got := payload.Services[0]
		if got.Unit != "sshd.service" || got.Active != "active" || got.Sub != "running" {
			t.Errorf("unexpected unit: %+v", got)
		}
		if !got.Enabled || got.UnitFileState != "enabled" {
			t.Errorf("expected enabled unit-file state: %+v", got)
		}
		if got.Scope != "system" {
			t.Errorf("expected system scope, got %q", got.Scope)
		}
		if len(payload.Warnings) != 1 {
			t.Errorf("expected a user-manager warning, got %v", payload.Warnings)
		}
	})

	t.Run("systemd plain text table fallback", func(t *testing.T) {
		textMock := `
  accounts-daemon.service loaded active running Accounts Service
● apparmor.service        not-found inactive dead apparmor.service
`
		runner := &mockRunner{
			responses: map[string]string{
				"--no-legend": textMock,
			},
			errs: map[string]error{
				"list-units": fmt.Errorf("invalid option"),
			},
		}

		res, err := reader.Read(ctx, runner, plugin.HostFacts{OSFamily: "linux"})
		if err != nil {
			t.Fatalf("Read error: %v", err)
		}
		if res == nil {
			t.Fatalf("expected non-nil response")
		}
	})
}

func TestServicePlugin_NewAPICompliance(t *testing.T) {
	p, ok := plugin.Get("system/service")
	if !ok {
		t.Fatalf("system/service not registered in DefaultRegistry")
	}

	// 1. DataCollectionPlugin
	dc, ok := p.(newapi.DataCollectionPlugin)
	if !ok {
		t.Fatalf("system/service does not implement newapi.DataCollectionPlugin")
	}
	sched := dc.CollectionSchedule()
	if sched.Interval != 60*time.Second || sched.TTL != 60*time.Second {
		t.Errorf("unexpected collection schedule for system/service: %+v", sched)
	}
	schema := dc.CollectionSchema()
	if schema.Version != "v2" || schema.RootKey != "services" {
		t.Errorf("unexpected collection schema: %+v", schema)
	}

	// 2. ManagementPlugin & action parity
	mgmt, ok := p.(newapi.ManagementPlugin)
	if !ok {
		t.Fatalf("system/service does not implement newapi.ManagementPlugin")
	}
	mgmtActions := mgmt.ManagementActions()
	legacyActions := p.(plugin.Actor).Actions()
	if len(mgmtActions) != 9 || len(legacyActions) != len(mgmtActions) {
		t.Fatalf("expected 9 management actions matching legacy Actions(), got mgmt=%d legacy=%d", len(mgmtActions), len(legacyActions))
	}
	for i := range mgmtActions {
		if mgmtActions[i].Name != legacyActions[i].Name {
			t.Errorf("action mismatch at %d: mgmt=%q legacy=%q", i, mgmtActions[i].Name, legacyActions[i].Name)
		}
		if len(mgmtActions[i].InvalidatesKinds) == 0 || mgmtActions[i].InvalidatesKinds[0] != plugin.KindSystem {
			t.Errorf("expected action %q to invalidate KindSystem, got %+v", mgmtActions[i].Name, mgmtActions[i].InvalidatesKinds)
		}
	}

	facts := plugin.HostFacts{NodeID: "node1", OSFamily: "linux"}
	if err := mgmt.ValidateAction(facts, "restart", map[string]any{
		"name":  "sshd.service",
		"scope": "system",
	}); err != nil {
		t.Errorf("expected valid restart options to pass ValidateAction, got %v", err)
	}
	if err := mgmt.ValidateAction(facts, "restart", map[string]any{
		"service": "sshd.service",
	}); err != nil {
		t.Errorf("expected 'service' alias for unit name to pass ValidateAction, got %v", err)
	}
	if err := mgmt.ValidateAction(facts, "daemon_reload", map[string]any{
		"scope": "user",
	}); err != nil {
		t.Errorf("expected daemon_reload alias to pass ValidateAction, got %v", err)
	}
	if err := mgmt.ValidateAction(facts, "restart", map[string]any{}); err == nil {
		t.Errorf("expected missing required 'name' option to fail ValidateAction")
	}
	if err := mgmt.ValidateAction(facts, "restart", map[string]any{
		"name":  "sshd.service",
		"scope": "invalid_scope",
	}); err == nil {
		t.Errorf("expected invalid enum 'scope' option to fail ValidateAction")
	}
	if err := mgmt.ValidateAction(facts, "unknown_action", nil); err == nil {
		t.Errorf("expected unknown action to fail ValidateAction")
	}

	// 3. CategorizedSettingsProvider & settingsframework validation
	csp, ok := p.(newapi.CategorizedSettingsProvider)
	if !ok {
		t.Fatalf("system/service does not implement newapi.CategorizedSettingsProvider")
	}
	catPage := csp.CategorizedSettingsPage()
	if err := settingsframework.ValidateSchema(catPage.SettingsPage); err != nil {
		t.Fatalf("ValidateSchema failed for system/service settings page: %v", err)
	}
	if err := newapi.ValidateCategorizedSettingsPage(catPage); err != nil {
		t.Fatalf("ValidateCategorizedSettingsPage failed for system/service: %v", err)
	}
	if catPage.ID != "system/service" || catPage.Scope != newapi.SettingsScopeDatacenter || catPage.Category != "system" {
		t.Errorf("unexpected categorized settings page metadata: %+v", catPage)
	}
	if _, ok := plugin.DefaultRegistry().GetSettingsPage("system/service"); !ok {
		t.Errorf("expected system/service settings page to be indexed in DefaultRegistry")
	}

	// 4. ContextMenuProvider
	cmp, ok := p.(newapi.ContextMenuProvider)
	if !ok {
		t.Fatalf("system/service does not implement newapi.ContextMenuProvider")
	}
	entries := cmp.ContextMenuEntries(context.Background(), facts)
	if len(entries) != 8 {
		t.Fatalf("expected 8 context menu entries for compatible linux host, got %d (%+v)", len(entries), entries)
	}
	if entries[0].PluginID != "system/service" || entries[0].Target != newapi.TargetService || entries[0].Action != "start" {
		t.Errorf("unexpected first context menu entry: %+v", entries[0])
	}
	if entries[len(entries)-1].Action != "reboot" || !entries[len(entries)-1].Danger {
		t.Errorf("expected last context menu entry to be danger reboot action, got %+v", entries[len(entries)-1])
	}
	if winEntries := cmp.ContextMenuEntries(context.Background(), plugin.HostFacts{OSFamily: "windows"}); len(winEntries) != 0 {
		t.Errorf("expected nil/empty context menu entries for incompatible windows host, got %+v", winEntries)
	}
	if routerEntries := cmp.ContextMenuEntries(context.Background(), plugin.HostFacts{OSFamily: "router_switch"}); len(routerEntries) != 0 {
		t.Errorf("expected nil/empty context menu entries for incompatible router_switch host, got %+v", routerEntries)
	}

	// 5. TaskTypeProvider & TaskCompileProvider
	ttp, ok := p.(newapi.TaskTypeProvider)
	if !ok {
		t.Fatalf("system/service does not implement newapi.TaskTypeProvider")
	}
	taskTypes := ttp.TaskTypes()
	if len(taskTypes) != 1 || taskTypes[0].Type != newapi.TaskTypeServiceControl || taskTypes[0].PluginID != "system/service" {
		t.Errorf("unexpected TaskTypes(): %+v", taskTypes)
	}

	tcp, ok := p.(newapi.TaskCompileProvider)
	if !ok {
		t.Fatalf("system/service does not implement newapi.TaskCompileProvider")
	}
	compiled, err := tcp.CompileTask(context.Background(), facts, newapi.TaskRequest{
		Type:     newapi.TaskTypePluginAction,
		PluginID: "system/service",
		Action:   "restart",
		NodeID:   "node1",
		Params: map[string]any{
			"name": "nginx",
		},
	})
	if err != nil {
		t.Fatalf("CompileTask failed: %v", err)
	}
	if len(compiled.Steps) != 1 || compiled.Steps[0].Params["name"] != "nginx.service" || compiled.Steps[0].Params["state"] != "restarted" {
		t.Errorf("unexpected CompileTask steps: %+v", compiled.Steps)
	}

	compiledServiceControl, err := tcp.CompileTask(context.Background(), facts, newapi.TaskRequest{
		Type:     newapi.TaskTypeServiceControl,
		PluginID: "system/service",
		NodeID:   "node1",
		Params: map[string]any{
			"service": "sshd",
			"state":   "reloaded",
		},
	})
	if err != nil {
		t.Fatalf("CompileTask for TaskTypeServiceControl failed: %v", err)
	}
	if len(compiledServiceControl.Steps) != 1 || compiledServiceControl.Steps[0].Params["name"] != "sshd.service" || compiledServiceControl.Steps[0].Params["state"] != "reloaded" {
		t.Errorf("unexpected CompileTask service_control steps: %+v", compiledServiceControl.Steps)
	}

	if _, err := tcp.CompileTask(context.Background(), facts, newapi.TaskRequest{
		Type:     newapi.TaskTypePluginAction,
		PluginID: "system/service",
		NodeID:   "node1",
	}); err == nil {
		t.Errorf("expected CompileTask with empty action to return an error")
	}
}
