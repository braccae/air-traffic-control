package journald_test

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

func TestJournaldPlugin_ProbeAndRead(t *testing.T) {
	p, ok := plugin.Get("logs/journald")
	if !ok {
		t.Fatalf("logs/journald not registered")
	}

	runner := &mockRunner{
		responses: map[string]string{
			"command -v journalctl": "journalctl",
			"journalctl":            "Oct 19 12:00:00 host systemd[1]: Started Caddy.\nOct 19 12:00:01 host caddy[123]: [INFO] Server listening on :443",
		},
	}

	det, err := p.Probe(context.Background(), runner, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatal(err)
	}
	if !det.Detected {
		t.Errorf("expected journalctl detected")
	}

	reader := p.(plugin.Reader)
	data, err := reader.Read(context.Background(), runner, plugin.HostFacts{
		OSFamily: "linux",
		Overrides: map[string]string{
			"unit": "caddy",
		},
	})
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	m := data.(map[string]any)
	lines := m["lines"].([]string)
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(lines))
	}
}
