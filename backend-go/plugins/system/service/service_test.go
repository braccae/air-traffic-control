package service_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"air-traffic-control/backend/pkg/plugin"
	_ "air-traffic-control/backend/plugins/system/service"
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
	for pattern, resp := range m.responses {
		if strings.Contains(cmd, pattern) {
			if err, ok := m.errs[pattern]; ok && err != nil {
				return resp, err
			}
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
			if spec.Steps[0].Module != "ansible.builtin.systemd" {
				t.Errorf("expected ansible.builtin.systemd, got %s", spec.Steps[0].Module)
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
}
