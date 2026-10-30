package terminal_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"air-traffic-control/backend/pkg/hostcache"
	"air-traffic-control/backend/pkg/nodeapi"
	"air-traffic-control/backend/pkg/terminal"
)

type mockResolver struct {
	node *nodeapi.Object
}

func (m *mockResolver) FindNode(query string) (*nodeapi.Object, error) {
	if m.node != nil {
		return m.node, nil
	}
	return nil, fmt.Errorf("node not found")
}

type mockRunner struct {
	lastCmd string
	out     string
	err     error
}

func (m *mockRunner) RunNodeCommand(ctx context.Context, node *nodeapi.Object, command string, timeout time.Duration) (string, error) {
	m.lastCmd = command
	return m.out, m.err
}

type mockStore struct {
	tasks []hostcache.Task
}

func (m *mockStore) SaveTask(t hostcache.Task) error {
	m.tasks = append(m.tasks, t)
	return nil
}

func TestTerminalExec_EmptyCommand(t *testing.T) {
	api := terminal.NewAPI(nil, nil, nil)
	mux := http.NewServeMux()
	api.Register(mux)

	body := bytes.NewBufferString(`{"command": "", "host": "node-dev-pc"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/terminal/exec", body)
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var res terminal.ExecResult
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if res.Output != "" || res.ExitCode != 0 || !res.Success {
		t.Fatalf("unexpected result: %+v", res)
	}
}

func TestTerminalExec_HelpCommand(t *testing.T) {
	api := terminal.NewAPI(nil, nil, nil)
	mux := http.NewServeMux()
	api.Register(mux)

	body := bytes.NewBufferString(`{"command": "help", "host": "node-dev-pc"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/terminal/exec", body)
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	var res terminal.ExecResult
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if res.ExitCode != 0 || !res.Success || len(res.Output) == 0 {
		t.Fatalf("unexpected help result: %+v", res)
	}
}

func TestTerminalExec_SuccessExecution(t *testing.T) {
	node := nodeapi.NewObject()
	node.SetString("id", "node-1")
	node.SetString("name", "server1")

	runner := &mockRunner{out: "Linux 6.8.4\n"}
	store := &mockStore{}
	api := terminal.NewAPI(&mockResolver{node: node}, runner, store)

	mux := http.NewServeMux()
	api.Register(mux)

	body := bytes.NewBufferString(`{"command": "uname -a", "nodeId": "node-1"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/terminal/exec", body)
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	var res terminal.ExecResult
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if res.ExitCode != 0 || !res.Success || res.Output != "Linux 6.8.4" {
		t.Fatalf("unexpected exec result: %+v", res)
	}
	if len(store.tasks) < 2 {
		t.Fatalf("expected at least 2 task updates (running + completed), got %d", len(store.tasks))
	}
	lastStatus := store.tasks[len(store.tasks)-1].Status
	if lastStatus != "OK" && lastStatus != "COMPLETED" {
		t.Fatalf("expected task status OK or COMPLETED, got %s", lastStatus)
	}
}

func TestTerminalExec_FallbackExecution(t *testing.T) {
	node := nodeapi.NewObject()
	node.SetString("id", "node-1")
	node.SetString("name", "server1")

	runner := &mockRunner{out: "", err: fmt.Errorf("ssh failed")}
	store := &mockStore{}
	api := terminal.NewAPI(&mockResolver{node: node}, runner, store)

	mux := http.NewServeMux()
	api.Register(mux)

	body := bytes.NewBufferString(`{"command": "uptime", "nodeId": "node-1"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/terminal/exec", body)
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	var res terminal.ExecResult
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if res.ExitCode != 0 || !res.Success || len(res.Output) == 0 {
		t.Fatalf("expected fallback output for uptime, got: %+v", res)
	}
}

func TestTerminalExec_LocalhostWithoutNode(t *testing.T) {
	runner := &mockRunner{out: "localhost-ok\n"}
	store := &mockStore{}
	// Resolver returns nil, simulating localhost query or node not found
	api := terminal.NewAPI(&mockResolver{node: nil}, runner, store)

	mux := http.NewServeMux()
	api.Register(mux)

	body := bytes.NewBufferString(`{"command": "echo hello", "host": "localhost"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/terminal/exec", body)
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	var res terminal.ExecResult
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if !res.Success || res.Output != "localhost-ok" {
		t.Fatalf("expected successful execution on localhost without node, got: %+v", res)
	}
}

func TestLocalPTY_OpenAndResize(t *testing.T) {
	session, err := terminal.OpenLocalPTY(80, 24)
	if err != nil {
		t.Fatalf("OpenLocalPTY failed: %v", err)
	}
	defer session.Close()

	if err := session.Resize(100, 30); err != nil {
		t.Fatalf("session.Resize failed: %v", err)
	}

	// Write command
	_, err = session.Write([]byte("echo hello-pty\n"))
	if err != nil {
		t.Fatalf("session.Write failed: %v", err)
	}
}

func TestRedactSecretsMasksInlineCredentials(t *testing.T) {
	cases := map[string]string{
		`PGPASSWORD='b3ff5d6a' psql -p 5432 -c '\dt'`: `PGPASSWORD='••••••••' psql -p 5432 -c '\dt'`,
		`psql --password=hunter2 -c '\dt'`:            `psql --password=•••••••• -c '\dt'`,
		`MYSQL_PWD=rootpass mysql -uroot`:             `MYSQL_PWD=•••••••• mysql -uroot`,
		`redis-cli --token s3cr3t ping`:               `redis-cli --token •••••••• ping`,
		`systemctl status nginx`:                      `systemctl status nginx`,
	}
	for input, want := range cases {
		if got := terminal.RedactSecrets(input); got != want {
			t.Errorf("RedactSecrets(%q) = %q, want %q", input, got, want)
		}
	}
}
