package package_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"air-traffic-control/backend/pkg/plugin"
	_ "air-traffic-control/backend/plugins/package/apk"
	_ "air-traffic-control/backend/plugins/package/apt"
	_ "air-traffic-control/backend/plugins/package/bootc"
	_ "air-traffic-control/backend/plugins/package/dnf"
	_ "air-traffic-control/backend/plugins/package/rpmostree"
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

func TestPackagePlugins_Registration(t *testing.T) {
	expected := []string{
		"package/bootc",
		"package/rpmostree",
		"package/dnf",
		"package/apt",
		"package/apk",
	}

	for _, id := range expected {
		p, ok := plugin.Get(id)
		if !ok {
			t.Errorf("plugin %q not registered", id)
			continue
		}
		if p.Meta().Kind != plugin.KindPackage {
			t.Errorf("plugin %q expected kind %q, got %q", id, plugin.KindPackage, p.Meta().Kind)
		}
	}
}

func TestPackagePlugins_Probe(t *testing.T) {
	ctx := context.Background()

	t.Run("dnf detected", func(t *testing.T) {
		p, ok := plugin.Get("package/dnf")
		if !ok {
			t.Fatal("plugin package/dnf not registered")
		}
		runner := &mockRunner{
			responses: map[string]string{
				"dnf": "/usr/bin/dnf\n",
			},
		}
		det, err := p.Probe(ctx, runner, plugin.HostFacts{OSFamily: "linux"})
		if err != nil {
			t.Fatalf("Probe error: %v", err)
		}
		if !det.Detected {
			t.Errorf("expected dnf to be detected")
		}
	})

	t.Run("apt detected", func(t *testing.T) {
		p, ok := plugin.Get("package/apt")
		if !ok {
			t.Fatal("plugin package/apt not registered")
		}
		runner := &mockRunner{
			responses: map[string]string{
				"apt-get": "/usr/bin/apt-get\n",
			},
		}
		det, err := p.Probe(ctx, runner, plugin.HostFacts{OSFamily: "linux"})
		if err != nil {
			t.Fatalf("Probe error: %v", err)
		}
		if !det.Detected {
			t.Errorf("expected apt to be detected")
		}
	})

	t.Run("apk detected", func(t *testing.T) {
		p, ok := plugin.Get("package/apk")
		if !ok {
			t.Fatal("plugin package/apk not registered")
		}
		runner := &mockRunner{
			responses: map[string]string{
				"apk": "/sbin/apk\n",
			},
		}
		det, err := p.Probe(ctx, runner, plugin.HostFacts{OSFamily: "linux"})
		if err != nil {
			t.Fatalf("Probe error: %v", err)
		}
		if !det.Detected {
			t.Errorf("expected apk to be detected")
		}
	})

	t.Run("bootc detected", func(t *testing.T) {
		p, ok := plugin.Get("package/bootc")
		if !ok {
			t.Fatal("plugin package/bootc not registered")
		}
		runner := &mockRunner{
			responses: map[string]string{
				"bootc": "/usr/bin/bootc\n",
			},
		}
		det, err := p.Probe(ctx, runner, plugin.HostFacts{OSFamily: "linux"})
		if err != nil {
			t.Fatalf("Probe error: %v", err)
		}
		if !det.Detected {
			t.Errorf("expected bootc to be detected")
		}
	})

	t.Run("rpmostree detected", func(t *testing.T) {
		p, ok := plugin.Get("package/rpmostree")
		if !ok {
			t.Fatal("plugin package/rpmostree not registered")
		}
		runner := &mockRunner{
			responses: map[string]string{
				"rpm-ostree": "/usr/bin/rpm-ostree\n",
			},
		}
		det, err := p.Probe(ctx, runner, plugin.HostFacts{OSFamily: "linux"})
		if err != nil {
			t.Fatalf("Probe error: %v", err)
		}
		if !det.Detected {
			t.Errorf("expected rpmostree to be detected")
		}
	})
}

func TestPackagePlugins_ActorCompile(t *testing.T) {
	tests := []struct {
		pluginID string
		action   string
		opts     map[string]any
		module   string
		state    string
	}{
		{
			pluginID: "package/dnf",
			action:   "install",
			opts:     map[string]any{"name": "nginx"},
			module:   "ansible.builtin.dnf",
			state:    "present",
		},
		{
			pluginID: "package/dnf",
			action:   "update",
			opts:     map[string]any{"name": "*"},
			module:   "ansible.builtin.dnf",
			state:    "latest",
		},
		{
			pluginID: "package/dnf",
			action:   "remove",
			opts:     map[string]any{"name": "nginx"},
			module:   "ansible.builtin.dnf",
			state:    "absent",
		},
		{
			pluginID: "package/apt",
			action:   "install",
			opts:     map[string]any{"name": "curl"},
			module:   "ansible.builtin.apt",
			state:    "present",
		},
		{
			pluginID: "package/apt",
			action:   "remove",
			opts:     map[string]any{"name": "curl"},
			module:   "ansible.builtin.apt",
			state:    "absent",
		},
		{
			pluginID: "package/apk",
			action:   "install",
			opts:     map[string]any{"name": "htop"},
			module:   "community.general.apk",
			state:    "present",
		},
		{
			pluginID: "package/bootc",
			action:   "update",
			opts:     map[string]any{},
			module:   "ansible.builtin.command",
			state:    "",
		},
		{
			pluginID: "package/rpmostree",
			action:   "update",
			opts:     map[string]any{},
			module:   "ansible.builtin.command",
			state:    "",
		},
	}

	for _, tc := range tests {
		t.Run(fmt.Sprintf("%s_%s", tc.pluginID, tc.action), func(t *testing.T) {
			p, ok := plugin.Get(tc.pluginID)
			if !ok {
				t.Fatalf("plugin %s not registered", tc.pluginID)
			}
			actor, ok := p.(plugin.Actor)
			if !ok {
				t.Fatalf("plugin %s does not implement plugin.Actor", tc.pluginID)
			}
			spec, err := actor.Compile(plugin.HostFacts{OSFamily: "linux"}, tc.action, tc.opts)
			if err != nil {
				t.Fatalf("Compile failed: %v", err)
			}
			if len(spec.Steps) == 0 {
				t.Fatalf("expected steps, got 0")
			}
			if spec.Steps[0].Module != tc.module {
				t.Errorf("expected module %q, got %q", tc.module, spec.Steps[0].Module)
			}
			if tc.state != "" && spec.Steps[0].Params["state"] != tc.state {
				t.Errorf("expected state %q, got %v", tc.state, spec.Steps[0].Params["state"])
			}
		})
	}
}
