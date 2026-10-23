package unified_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"air-traffic-control/backend/pkg/plugin"
	_ "air-traffic-control/backend/plugins/all"
)

type stubRunner struct {
	responses map[string]string
}

func (s *stubRunner) RunCommand(ctx context.Context, cmd string) (string, error) {
	for pattern, resp := range s.responses {
		if strings.Contains(cmd, pattern) {
			return resp, nil
		}
	}
	return "", nil
}

func TestUnifiedPlugin_Registered(t *testing.T) {
	p, ok := plugin.Get("database/unified")
	if !ok {
		t.Fatal("database/unified plugin not registered")
	}
	meta := p.Meta()
	if meta.Kind != plugin.KindDatabase || meta.Name != "unified" {
		t.Errorf("unexpected meta: %+v", meta)
	}
	for name, assertion := range map[string]func() bool{
		"Reader":  func() bool { _, ok := p.(plugin.Reader); return ok },
		"Actor":   func() bool { _, ok := p.(plugin.Actor); return ok },
		"Querier": func() bool { _, ok := p.(plugin.Querier); return ok },
	} {
		if !assertion() {
			t.Errorf("database/unified must implement plugin.%s", name)
		}
	}
}

func TestUnifiedPlugin_ProbeWithoutConfiguration(t *testing.T) {
	p, _ := plugin.Get("database/unified")
	runner := &stubRunner{responses: map[string]string{}}
	det, err := p.Probe(context.Background(), runner, plugin.HostFacts{
		NodeID:    "node-test",
		OSFamily:  "linux",
		HostRoute: "127.0.0.1",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if det.Detected {
		t.Error("expected detected=false when no engine overrides are configured")
	}
	if !strings.Contains(strings.ToUpper(det.Status), "NO CONFIG") {
		t.Errorf("expected NO CONFIG status, got %q", det.Status)
	}
}

func TestUnifiedPlugin_ProbeWithUnreachableEngine(t *testing.T) {
	p, _ := plugin.Get("database/unified")
	runner := &stubRunner{responses: map[string]string{}}
	det, err := p.Probe(context.Background(), runner, plugin.HostFacts{
		NodeID:    "node-test",
		OSFamily:  "linux",
		HostRoute: "127.0.0.1",
		Overrides: map[string]string{"postgres_port": "1"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if det.Detected {
		t.Error("expected detected=false when the configured engine is unreachable and no CLI exists")
	}
	if !strings.Contains(det.Status, "0/1") {
		t.Errorf("expected reachability summary 0/1, got %q", det.Status)
	}
}

func TestUnifiedPlugin_Read(t *testing.T) {
	p, _ := plugin.Get("database/unified")
	runner := &stubRunner{responses: map[string]string{
		"command -v psql": "/usr/bin/psql\n",
	}}
	reader := p.(plugin.Reader)
	data, err := reader.Read(context.Background(), runner, plugin.HostFacts{
		NodeID:    "node-test",
		OSFamily:  "linux",
		HostRoute: "127.0.0.1",
		Overrides: map[string]string{"postgres_port": "1", "postgres_user": "postgres"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Read returns the package's ReadData struct; round-trip through JSON to
	// assert without importing the internal type name.
	encoded, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("failed to encode read result: %v", err)
	}
	var decoded struct {
		Drivers []string `json:"drivers"`
		Engines []struct {
			Engine    string `json:"engine"`
			Reachable bool   `json:"reachable"`
			CLI       string `json:"cli"`
		} `json:"engines"`
	}
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("failed to decode read result: %v", err)
	}
	if len(decoded.Drivers) == 0 {
		t.Error("expected compiled driver list")
	}
	found := false
	for _, e := range decoded.Engines {
		if e.Engine == "postgres" {
			found = true
			if e.Reachable {
				t.Error("expected unreachable postgres on port 1")
			}
			if e.CLI != "/usr/bin/psql" {
				t.Errorf("expected detected psql CLI, got %q", e.CLI)
			}
		}
	}
	if !found {
		t.Error("expected postgres engine in read result")
	}
}

func TestUnifiedPlugin_QueryUnsupportedEngineFallsBack(t *testing.T) {
	p, _ := plugin.Get("database/unified")
	querier := p.(plugin.Querier)
	res, err := querier.Query(context.Background(), &stubRunner{}, plugin.HostFacts{HostRoute: "127.0.0.1"}, plugin.QueryRequest{
		Engine: "db2",
		Query:  "SELECT 1",
	})
	if err != nil {
		t.Fatalf("unexpected hard error: %v", err)
	}
	if res.Fallback != "cli" {
		t.Errorf("expected cli fallback for unsupported engine, got %+v", res)
	}
	if res.Error == "" {
		t.Error("expected an explanatory error string")
	}
}

func TestUnifiedPlugin_QueryRocksDBFallsBack(t *testing.T) {
	p, _ := plugin.Get("database/unified")
	querier := p.(plugin.Querier)
	res, err := querier.Query(context.Background(), &stubRunner{}, plugin.HostFacts{
		HostRoute: "127.0.0.1",
		Overrides: map[string]string{"rocksdb_path": "/var/lib/rocksdb"},
	}, plugin.QueryRequest{Engine: "rocksdb", Query: "scan"})
	if err != nil {
		t.Fatalf("unexpected hard error: %v", err)
	}
	if res.Fallback != "cli" {
		t.Errorf("expected cli fallback for rocksdb, got %+v", res)
	}
}

func TestUnifiedPlugin_QueryRequiresQueryText(t *testing.T) {
	p, _ := plugin.Get("database/unified")
	querier := p.(plugin.Querier)
	_, err := querier.Query(context.Background(), &stubRunner{}, plugin.HostFacts{}, plugin.QueryRequest{Engine: "postgres"})
	if err == nil {
		t.Error("expected an error for an empty query")
	}
}

func TestUnifiedPlugin_CompileInstallCLIClient(t *testing.T) {
	p, _ := plugin.Get("database/unified")
	actor := p.(plugin.Actor)
	specs := actor.Actions()
	if len(specs) == 0 {
		t.Fatal("expected at least one action")
	}
	task, err := actor.Compile(plugin.HostFacts{ActivePackage: "apt"}, "install_cli_client", map[string]any{"engine": "postgres"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(task.Steps) == 0 {
		t.Fatal("expected a compiled step")
	}
	if task.Steps[0].Module != "ansible.builtin.package" {
		t.Errorf("expected ansible.builtin.package, got %s", task.Steps[0].Module)
	}
	params := task.Steps[0].Params
	if params["state"] != "present" {
		t.Errorf("expected state=present, got %v", params["state"])
	}
}

func TestUnifiedPlugin_CompileRejectsUnknownAction(t *testing.T) {
	p, _ := plugin.Get("database/unified")
	actor := p.(plugin.Actor)
	if _, err := actor.Compile(plugin.HostFacts{}, "drop_everything", nil); err == nil {
		t.Error("expected error for unsupported action")
	}
}
