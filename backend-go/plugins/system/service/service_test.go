package service_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"air-traffic-control/backend/pkg/plugin"
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
