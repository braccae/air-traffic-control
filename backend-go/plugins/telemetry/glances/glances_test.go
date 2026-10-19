package glances_test

import (
	"context"
	"strings"
	"testing"

	"air-traffic-control/backend/pkg/plugin"
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

func TestGlancesPlugin_ProbeAndRead(t *testing.T) {
	p, ok := plugin.Get("telemetry/glances")
	if !ok {
		t.Fatalf("telemetry/glances not registered")
	}

	runner := &mockRunner{
		responses: map[string]string{
			"61208/api/4/status": "OK",
			"61208/api/4/all":    "{\"cpu\":{\"total\":15.5},\"mem\":{\"total\":17179869184,\"used\":4294967296}}",
		},
	}

	det, err := p.Probe(context.Background(), runner, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatal(err)
	}
	if !det.Detected {
		t.Errorf("expected glances detected")
	}

	reader := p.(plugin.Reader)
	data, err := reader.Read(context.Background(), runner, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	m := data.(map[string]any)
	cpu := m["cpu"].(map[string]any)
	if cpu["total"] != 15.5 {
		t.Errorf("expected cpu total 15.5, got %v", cpu["total"])
	}
}
