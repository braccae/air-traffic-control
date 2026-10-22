package firewalld_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"air-traffic-control/backend/pkg/plugin"
	"air-traffic-control/backend/plugins/firewall/firewalld"
	_ "air-traffic-control/backend/plugins/all"
)

type mockRunner struct {
	responses map[string]string
	err       error
}

func (m *mockRunner) RunCommand(ctx context.Context, cmd string) (string, error) {
	if m.err != nil {
		return "", m.err
	}
	for pattern, resp := range m.responses {
		if strings.Contains(cmd, pattern) {
			return resp, nil
		}
	}
	return "", nil
}

// readFixture mirrors real `firewall-cmd --list-all` output, including a
// masquerade flag and a single port-forward entry.
const readFixture = "running\n" +
	"===ZONES===\n" +
	"public\n  interfaces: eth0\n" +
	"trusted\n  interfaces: tailscale0\n" +
	"===LIST_ALL===\n" +
	"public (active)\n" +
	"  services: ssh dhcpv6-client\n" +
	"  ports: 8080/tcp 9000/udp\n" +
	"  masquerade: yes\n" +
	"  forward-ports: port=8080:proto=tcp:toport=80:toaddr=192.168.1.10\n" +
	"===NFT===\n" +
	"table inet firewalld"

func newMockRunner() *mockRunner {
	return &mockRunner{
		responses: map[string]string{
			"firewall-cmd --state": readFixture,
		},
	}
}

func TestFirewalldPlugin_ProbeReadAndCompile(t *testing.T) {
	p, ok := plugin.Get("firewall/firewalld")
	if !ok {
		t.Fatalf("firewall/firewalld not registered")
	}

	runner := newMockRunner()

	det, err := p.Probe(context.Background(), runner, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatal(err)
	}
	if !det.Detected || det.Status != "running" {
		t.Errorf("unexpected probe result: %+v", det)
	}

	reader := p.(plugin.Reader)
	data, err := reader.Read(context.Background(), runner, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}

	m := data.(map[string]any)
	if m["status"] != "running" || m["nftablesActive"] != true || m["backend"] != "nftables (firewalld)" {
		t.Errorf("unexpected read result: %+v", m)
	}

	actor := p.(plugin.Actor)
	task, err := actor.Compile(plugin.HostFacts{NodeID: "node1"}, "allow_port", map[string]any{
		"port":  "443",
		"proto": "tcp",
		"zone":  "public",
	})
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}
	if len(task.Steps) != 1 || task.Steps[0].Module != "ansible.posix.firewalld" {
		t.Errorf("unexpected task steps: %+v", task.Steps)
	}
	if task.Steps[0].Params["port"] != "443/tcp" || task.Steps[0].Params["zone"] != "public" {
		t.Errorf("unexpected task params: %+v", task.Steps[0].Params)
	}
}

func TestFirewalldPlugin_ReadForwardPortsAndMasquerade(t *testing.T) {
	p, ok := plugin.Get("firewall/firewalld")
	if !ok {
		t.Fatalf("firewall/firewalld not registered")
	}

	reader := p.(plugin.Reader)
	data, err := reader.Read(context.Background(), newMockRunner(), plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}

	m := data.(map[string]any)

	if m["masquerade"] != true {
		t.Errorf("expected masquerade=true, got %v", m["masquerade"])
	}

	fps, ok := m["forwardPorts"].([]firewalld.ForwardPort)
	if !ok {
		t.Fatalf("forwardPorts is not []firewalld.ForwardPort: %T", m["forwardPorts"])
	}
	if len(fps) != 1 {
		t.Fatalf("expected 1 forward port, got %d: %+v", len(fps), fps)
	}
	if fps[0].Port != "8080" {
		t.Errorf("expected forward port '8080', got %q", fps[0].Port)
	}
	if fps[0].Proto != "tcp" {
		t.Errorf("expected forward proto 'tcp', got %q", fps[0].Proto)
	}
	if fps[0].ToPort != "80" {
		t.Errorf("expected forward toport '80', got %q", fps[0].ToPort)
	}
	if fps[0].ToAddr != "192.168.1.10" {
		t.Errorf("expected forward toaddr '192.168.1.10', got %q", fps[0].ToAddr)
	}
}

func TestFirewalldPlugin_CompileForwardPort(t *testing.T) {
	p, ok := plugin.Get("firewall/firewalld")
	if !ok {
		t.Fatalf("firewall/firewalld not registered")
	}
	actor := p.(plugin.Actor)

	task, err := actor.Compile(plugin.HostFacts{NodeID: "node1"}, "forward_port", map[string]any{
		"port":   "8080",
		"proto":  "tcp",
		"toport": "80",
		"toaddr": "192.168.1.10",
		"zone":   "public",
	})
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}
	if len(task.Steps) != 2 {
		t.Fatalf("expected 2 steps, got %d: %+v", len(task.Steps), task.Steps)
	}
	for i, step := range task.Steps {
		if step.Module != "ansible.posix.firewalld" {
			t.Errorf("step %d: unexpected module %q", i, step.Module)
		}
	}
	if task.Steps[0].Params["masquerade"] != "true" {
		t.Errorf("step 0 should enable masquerade, got params %+v", task.Steps[0].Params)
	}
	if task.Steps[0].Params["zone"] != "public" {
		t.Errorf("step 0 zone mismatch: %+v", task.Steps[0].Params)
	}

	pf, ok := task.Steps[1].Params["port_forward"].([]map[string]any)
	if !ok {
		t.Fatalf("step 1 port_forward is not []map[string]any: %T", task.Steps[1].Params["port_forward"])
	}
	if len(pf) != 1 {
		t.Fatalf("expected 1 port_forward element, got %d", len(pf))
	}
	if pf[0]["port"] != "8080" || pf[0]["proto"] != "tcp" || pf[0]["toport"] != "80" || pf[0]["toaddr"] != "192.168.1.10" {
		t.Errorf("unexpected port_forward spec: %+v", pf[0])
	}
}

func TestFirewalldPlugin_CompileForwardPortValidation(t *testing.T) {
	p, ok := plugin.Get("firewall/firewalld")
	if !ok {
		t.Fatalf("firewall/firewalld not registered")
	}
	actor := p.(plugin.Actor)

	cases := []struct {
		name string
		opts map[string]any
	}{
		{"invalid external port", map[string]any{"port": "70000", "proto": "tcp", "toport": "80"}},
		{"invalid protocol", map[string]any{"port": "8080", "proto": "icmp", "toport": "80"}},
		{"missing destination port", map[string]any{"port": "8080", "proto": "tcp"}},
		{"invalid destination address", map[string]any{"port": "8080", "proto": "tcp", "toport": "80", "toaddr": "not-an-ip"}},
		{"missing external port", map[string]any{"proto": "tcp", "toport": "80"}},
	}
	for _, tc := range cases {
		if _, err := actor.Compile(plugin.HostFacts{NodeID: "node1"}, "forward_port", tc.opts); err == nil {
			t.Errorf("%s: expected an error, got none", tc.name)
		}
	}
}

func TestFirewalldPlugin_ProbeDetectionStates(t *testing.T) {
	p, ok := plugin.Get("firewall/firewalld")
	if !ok {
		t.Fatalf("firewall/firewalld not registered")
	}

	t.Run("absent when all commands return empty", func(t *testing.T) {
		runner := &mockRunner{responses: map[string]string{}}
		det, err := p.Probe(context.Background(), runner, plugin.HostFacts{OSFamily: "linux"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if det.Detected {
			t.Errorf("expected Detected=false when commands return empty, got %+v", det)
		}
		if det.Status != "UNAVAILABLE" {
			t.Errorf("expected Status=UNAVAILABLE, got %q", det.Status)
		}
	})

	t.Run("absent when all commands fail with error", func(t *testing.T) {
		runner := &mockRunner{err: errors.New("command failed")}
		det, err := p.Probe(context.Background(), runner, plugin.HostFacts{OSFamily: "linux"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if det.Detected {
			t.Errorf("expected Detected=false when commands fail, got %+v", det)
		}
		if det.Status != "UNAVAILABLE" {
			t.Errorf("expected Status=UNAVAILABLE, got %q", det.Status)
		}
	})

	t.Run("absent when sudo permission error is returned", func(t *testing.T) {
		runner := &mockRunner{
			responses: map[string]string{
				"firewall-cmd --state": "sudo: a password is required",
				"command -v":           "sudo: a password is required",
				"systemctl":            "sudo: a password is required",
			},
		}
		det, err := p.Probe(context.Background(), runner, plugin.HostFacts{OSFamily: "linux"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if det.Detected {
			t.Errorf("expected Detected=false on permission error, got %+v", det)
		}
	})

	t.Run("running when firewall-cmd --state prints running", func(t *testing.T) {
		runner := &mockRunner{
			responses: map[string]string{
				"firewall-cmd --state": "running\n",
			},
		}
		det, err := p.Probe(context.Background(), runner, plugin.HostFacts{OSFamily: "linux"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !det.Detected || det.Status != "running" {
			t.Errorf("expected Detected=true, Status=running, got %+v", det)
		}
	})

	t.Run("inactive when only command -v firewall-cmd resolves", func(t *testing.T) {
		runner := &mockRunner{
			responses: map[string]string{
				"firewall-cmd --state":     "not running\n",
				"command -v firewall-cmd": "/usr/bin/firewall-cmd\n",
			},
		}
		det, err := p.Probe(context.Background(), runner, plugin.HostFacts{OSFamily: "linux"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !det.Detected || det.Status != "inactive" {
			t.Errorf("expected Detected=true, Status=inactive, got %+v", det)
		}
	})

	t.Run("inactive when only systemctl unit is present", func(t *testing.T) {
		runner := &mockRunner{
			responses: map[string]string{
				"systemctl": "# /usr/lib/systemd/system/firewalld.service\n[Unit]\nDescription=firewalld\n",
			},
		}
		det, err := p.Probe(context.Background(), runner, plugin.HostFacts{OSFamily: "linux"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !det.Detected || det.Status != "inactive" {
			t.Errorf("expected Detected=true, Status=inactive, got %+v", det)
		}
	})
}

func TestFirewalldPlugin_ReadInactiveDoesNotClaimBackend(t *testing.T) {
	p, ok := plugin.Get("firewall/firewalld")
	if !ok {
		t.Fatalf("firewall/firewalld not registered")
	}

	runner := &mockRunner{
		responses: map[string]string{
			"firewall-cmd --state": "not running\n===ZONES===\n===LIST_ALL===\n===NFT===\nNO_NFT",
		},
	}

	reader := p.(plugin.Reader)
	data, err := reader.Read(context.Background(), runner, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}

	m := data.(map[string]any)
	if m["status"] != "inactive" {
		t.Errorf("expected status=inactive, got %v", m["status"])
	}
	if m["backend"] != "" {
		t.Errorf("expected empty backend when firewalld is not running, got %q", m["backend"])
	}
}
