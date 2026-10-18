package cockpit

import (
	"testing"

	"prox-orch/backend/pkg/ssh"
)

type mockSSHRunner struct {
	lastCmd string
	output  string
	err     error
}

func (m *mockSSHRunner) RunCommand(cfg ssh.Config, cmd string) (string, error) {
	m.lastCmd = cmd
	return m.output, m.err
}

func TestFetchRecentLogs_ParsesJournalJSON(t *testing.T) {
	runner := &mockSSHRunner{
		output: `{"__REALTIME_TIMESTAMP":"1715682000","MESSAGE":"Started Rootless Podman container","_SYSTEMD_UNIT":"user@1000.service"}` + "\n" +
			`{"__REALTIME_TIMESTAMP":"1715682005","MESSAGE":"Healthcheck OK","_SYSTEMD_UNIT":"podman.service"}`,
	}
	session := NewBridgeSession(runner, ssh.Config{Host: "192.168.1.150", Port: 22, User: "root"})
	logs, err := session.FetchRecentLogs(20, "podman.service")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(logs) != 2 {
		t.Fatalf("expected 2 log entries, got %d", len(logs))
	}
	if logs[0].Message != "Started Rootless Podman container" || logs[0].Service != "user@1000.service" {
		t.Errorf("unexpected first log entry: %+v", logs[0])
	}
}

func TestControlService_ExecutesSystemctl(t *testing.T) {
	runner := &mockSSHRunner{}
	session := NewBridgeSession(runner, ssh.Config{Host: "192.168.1.150", Port: 22, User: "root"})
	if err := session.ControlService("cockpit.socket", "restart"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if runner.lastCmd != "systemctl restart cockpit.socket" {
		t.Errorf("expected systemctl restart cockpit.socket, got %q", runner.lastCmd)
	}
}

