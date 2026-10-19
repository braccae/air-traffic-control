package elevation_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"air-traffic-control/backend/pkg/plugin"
	_ "air-traffic-control/backend/plugins/elevation/doas"
	_ "air-traffic-control/backend/plugins/elevation/none"
	_ "air-traffic-control/backend/plugins/elevation/run0"
	_ "air-traffic-control/backend/plugins/elevation/sudo"
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

func TestElevationPlugins_RegistrationAndMeta(t *testing.T) {
	plugins := plugin.ListByKind(plugin.KindElevation)
	if len(plugins) < 4 {
		t.Fatalf("expected at least 4 elevation plugins, got %d", len(plugins))
	}

	expectedIDs := []string{
		"elevation/run0",
		"elevation/sudo",
		"elevation/doas",
		"elevation/none",
	}

	for i, expected := range expectedIDs {
		if plugins[i].Meta().ID != expected {
			t.Errorf("at index %d, expected plugin ID %q, got %q", i, expected, plugins[i].Meta().ID)
		}
	}
}

func TestElevationPlugins_Probe(t *testing.T) {
	ctx := context.Background()

	t.Run("run0 probe success", func(t *testing.T) {
		p, ok := plugin.Get("elevation/run0")
		if !ok {
			t.Fatal("plugin elevation/run0 not registered")
		}
		runner := &mockRunner{
			responses: map[string]string{
				"run0": "0\n",
			},
		}
		det, err := p.Probe(ctx, runner, plugin.HostFacts{OSFamily: "linux"})
		if err != nil {
			t.Fatalf("Probe error: %v", err)
		}
		if !det.Detected {
			t.Errorf("expected run0 to be detected")
		}
		if det.Status != "ONLINE" && det.Status != "ACTIVE" {
			t.Errorf("expected ACTIVE/ONLINE status, got %q", det.Status)
		}
	})

	t.Run("sudo probe success", func(t *testing.T) {
		p, ok := plugin.Get("elevation/sudo")
		if !ok {
			t.Fatal("plugin elevation/sudo not registered")
		}
		runner := &mockRunner{
			responses: map[string]string{
				"sudo -n": "0\n",
			},
		}
		det, err := p.Probe(ctx, runner, plugin.HostFacts{OSFamily: "linux"})
		if err != nil {
			t.Fatalf("Probe error: %v", err)
		}
		if !det.Detected {
			t.Errorf("expected sudo to be detected")
		}
	})

	t.Run("doas probe success", func(t *testing.T) {
		p, ok := plugin.Get("elevation/doas")
		if !ok {
			t.Fatal("plugin elevation/doas not registered")
		}
		runner := &mockRunner{
			responses: map[string]string{
				"doas -n": "0\n",
			},
		}
		det, err := p.Probe(ctx, runner, plugin.HostFacts{OSFamily: "linux"})
		if err != nil {
			t.Fatalf("Probe error: %v", err)
		}
		if !det.Detected {
			t.Errorf("expected doas to be detected")
		}
	})

	t.Run("none probe success", func(t *testing.T) {
		p, ok := plugin.Get("elevation/none")
		if !ok {
			t.Fatal("plugin elevation/none not registered")
		}
		runner := &mockRunner{
			responses: map[string]string{
				"id -u": "0\n",
			},
		}
		det, err := p.Probe(ctx, runner, plugin.HostFacts{OSFamily: "linux"})
		if err != nil {
			t.Fatalf("Probe error: %v", err)
		}
		if !det.Detected {
			t.Errorf("expected none to be detected for root")
		}
	})
}

func TestElevationPlugins_ActorCompile(t *testing.T) {
	tests := []struct {
		pluginID string
		expected string
	}{
		{"elevation/run0", "run0"},
		{"elevation/sudo", "sudo"},
		{"elevation/doas", "doas"},
		{"elevation/none", "none"},
	}

	for _, tc := range tests {
		t.Run(tc.pluginID, func(t *testing.T) {
			p, ok := plugin.Get(tc.pluginID)
			if !ok {
				t.Fatalf("plugin %s not registered", tc.pluginID)
			}
			actor, ok := p.(plugin.Actor)
			if !ok {
				t.Fatalf("plugin %s does not implement plugin.Actor", tc.pluginID)
			}
			taskSpec, err := actor.Compile(plugin.HostFacts{OSFamily: "linux"}, "elevate", map[string]any{
				"command": "systemctl restart caddy",
			})
			if err != nil {
				t.Fatalf("Compile error: %v", err)
			}
			if len(taskSpec.Steps) == 0 {
				t.Fatalf("expected task steps, got 0")
			}
			if taskSpec.Steps[0].Params["become_method"] != tc.expected {
				t.Errorf("expected become_method %q, got %v", tc.expected, taskSpec.Steps[0].Params["become_method"])
			}
		})
	}
}
