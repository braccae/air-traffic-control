package local_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"air-traffic-control/backend/pkg/plugin"
	localplugin "air-traffic-control/backend/plugins/connection/local"
)

func TestLocalPlugin_MetaAndCompatibility(t *testing.T) {
	p := localplugin.New()
	meta := p.Meta()

	if meta.ID != "connection/local" {
		t.Errorf("expected ID 'connection/local', got %q", meta.ID)
	}
	if meta.Kind != plugin.KindConnection {
		t.Errorf("expected Kind 'connection', got %q", meta.Kind)
	}

	// Should be compatible when HostRoute is 127.0.0.1, localhost, or ActiveConnection is local
	if !p.Compatible(plugin.HostFacts{HostRoute: "127.0.0.1"}) {
		t.Errorf("expected local plugin to be compatible with 127.0.0.1")
	}
	if !p.Compatible(plugin.HostFacts{HostRoute: "localhost"}) {
		t.Errorf("expected local plugin to be compatible with localhost")
	}
	if !p.Compatible(plugin.HostFacts{ActiveConnection: "connection/local"}) {
		t.Errorf("expected local plugin to be compatible with activeConnection connection/local")
	}
	if p.Compatible(plugin.HostFacts{HostRoute: "192.168.1.50"}) {
		t.Errorf("expected local plugin to be incompatible with remote host without explicit connection/local")
	}
}

func TestLocalPlugin_RunCommandAndProbe(t *testing.T) {
	p := localplugin.New()
	facts := plugin.HostFacts{
		NodeID:    "controller",
		OSFamily:  "linux",
		HostRoute: "127.0.0.1",
	}

	runner, err := p.NewRunner(facts)
	if err != nil {
		t.Fatalf("failed to create local runner: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	out, err := runner.RunCommand(ctx, "echo test-local-output")
	if err != nil {
		t.Fatalf("RunCommand failed: %v", err)
	}
	if !strings.Contains(out, "test-local-output") {
		t.Errorf("unexpected output: %q", out)
	}

	// Probe
	detection, err := p.Probe(ctx, runner, facts)
	if err != nil {
		t.Fatalf("Probe failed: %v", err)
	}
	if !detection.Detected || detection.Status != "ONLINE" {
		t.Errorf("expected detected and ONLINE, got %+v", detection)
	}
}
