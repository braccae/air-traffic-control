package cockpit

import (
	"errors"
	"strings"
	"testing"

	"air-traffic-control/backend/pkg/ssh"
)

type mockSSHRunner struct {
	lastCmd  string
	output   string
	err      error
	cmdFunc  func(cmd string) (string, error)
}

func (m *mockSSHRunner) RunCommand(cfg ssh.Config, cmd string) (string, error) {
	m.lastCmd = cmd
	if m.cmdFunc != nil {
		return m.cmdFunc(cmd)
	}
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

func TestWrapElevatedCommand_Run0Preferred(t *testing.T) {
	wrapped := WrapElevatedCommand("systemctl restart caddy", ElevationRun0, "core")
	expected := "run0 --background=no systemctl restart caddy"
	if wrapped != expected {
		t.Errorf("expected %q, got %q", expected, wrapped)
	}

	// Strips leading sudo
	wrappedFromSudo := WrapElevatedCommand("sudo systemctl restart caddy", ElevationRun0, "core")
	if wrappedFromSudo != expected {
		t.Errorf("expected %q, got %q", expected, wrappedFromSudo)
	}
}

func TestWrapElevatedCommand_SudoFallback(t *testing.T) {
	wrapped := WrapElevatedCommand("systemctl stop podman", ElevationSudo, "apps")
	expected := "sudo -n systemctl stop podman"
	if wrapped != expected {
		t.Errorf("expected %q, got %q", expected, wrapped)
	}
}

func TestWrapElevatedCommand_RootNoOp(t *testing.T) {
	wrapped := WrapElevatedCommand("systemctl reboot", ElevationRun0, "root")
	expected := "systemctl reboot"
	if wrapped != expected {
		t.Errorf("expected %q, got %q", expected, wrapped)
	}
}

func TestControlService_WithRun0Elevation(t *testing.T) {
	runner := &mockSSHRunner{}
	session := NewBridgeSession(runner, ssh.Config{Host: "192.168.1.150", Port: 22, User: "core"})
	session.SetElevationMethod(ElevationRun0)

	if err := session.ControlService("nginx.service", "reload"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := "run0 --background=no systemctl reload nginx.service"
	if runner.lastCmd != expected {
		t.Errorf("expected %q, got %q", expected, runner.lastCmd)
	}
}

func TestControlService_WithSudoElevation(t *testing.T) {
	runner := &mockSSHRunner{}
	session := NewBridgeSession(runner, ssh.Config{Host: "192.168.1.150", Port: 22, User: "core"})
	session.SetElevationMethod(ElevationSudo)

	if err := session.ControlService("nginx.service", "reload"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := "sudo -n systemctl reload nginx.service"
	if runner.lastCmd != expected {
		t.Errorf("expected %q, got %q", expected, runner.lastCmd)
	}
}

func TestDetectElevation_PrefersRun0(t *testing.T) {
	runner := &mockSSHRunner{
		cmdFunc: func(cmd string) (string, error) {
			if strings.Contains(cmd, "run0") {
				return "0\n", nil
			}
			if strings.Contains(cmd, "sudo") {
				return "0\n", nil
			}
			return "", errors.New("unknown command")
		},
	}
	session := NewBridgeSession(runner, ssh.Config{Host: "192.168.1.150", Port: 22, User: "core"})
	status, err := session.DetectElevation()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.Method != ElevationRun0 || !status.Active {
		t.Errorf("expected ElevationRun0 active, got %+v", status)
	}
	if session.GetElevationMethod() != ElevationRun0 {
		t.Errorf("expected session elevation to be ElevationRun0, got %s", session.GetElevationMethod())
	}
}

func TestDetectElevation_FallsBackToSudo(t *testing.T) {
	runner := &mockSSHRunner{
		cmdFunc: func(cmd string) (string, error) {
			if strings.Contains(cmd, "run0") {
				return "", errors.New("exit status 1")
			}
			if strings.Contains(cmd, "sudo") {
				return "0\n", nil
			}
			return "", errors.New("unknown command")
		},
	}
	session := NewBridgeSession(runner, ssh.Config{Host: "192.168.1.150", Port: 22, User: "apps"})
	status, err := session.DetectElevation()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.Method != ElevationSudo || !status.Active {
		t.Errorf("expected ElevationSudo active, got %+v", status)
	}
	if session.GetElevationMethod() != ElevationSudo {
		t.Errorf("expected session elevation to be ElevationSudo, got %s", session.GetElevationMethod())
	}
}

func TestWrapElevatedCommand_DoasFallback(t *testing.T) {
	wrapped := WrapElevatedCommand("systemctl stop podman", ElevationDoas, "apps")
	expected := "doas -n systemctl stop podman"
	if wrapped != expected {
		t.Errorf("expected %q, got %q", expected, wrapped)
	}

	// Strips existing doas
	wrappedFromDoas := WrapElevatedCommand("doas -n systemctl restart caddy", ElevationRun0, "core")
	expectedRun0 := "run0 --background=no systemctl restart caddy"
	if wrappedFromDoas != expectedRun0 {
		t.Errorf("expected %q, got %q", expectedRun0, wrappedFromDoas)
	}
}

func TestControlService_WithDoasElevation(t *testing.T) {
	runner := &mockSSHRunner{}
	session := NewBridgeSession(runner, ssh.Config{Host: "192.168.1.150", Port: 22, User: "core"})
	session.SetElevationMethod(ElevationDoas)

	if err := session.ControlService("nginx.service", "reload"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := "doas -n systemctl reload nginx.service"
	if runner.lastCmd != expected {
		t.Errorf("expected %q, got %q", expected, runner.lastCmd)
	}
}

func TestDetectElevation_FallsBackToDoas(t *testing.T) {
	runner := &mockSSHRunner{
		cmdFunc: func(cmd string) (string, error) {
			if strings.Contains(cmd, "run0") {
				return "", errors.New("command not found")
			}
			if strings.Contains(cmd, "sudo") {
				return "", errors.New("command not found")
			}
			if strings.Contains(cmd, "doas") {
				return "0\n", nil
			}
			return "", errors.New("unknown command")
		},
	}
	session := NewBridgeSession(runner, ssh.Config{Host: "192.168.1.150", Port: 22, User: "alpine-user"})
	status, err := session.DetectElevation()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.Method != ElevationDoas || !status.Active {
		t.Errorf("expected ElevationDoas active, got %+v", status)
	}
	if session.GetElevationMethod() != ElevationDoas {
		t.Errorf("expected session elevation to be ElevationDoas, got %s", session.GetElevationMethod())
	}
}

func TestDetectElevation_Unprivileged(t *testing.T) {
	runner := &mockSSHRunner{
		cmdFunc: func(cmd string) (string, error) {
			return "", errors.New("command not found or permission denied")
		},
	}
	session := NewBridgeSession(runner, ssh.Config{Host: "192.168.1.150", Port: 22, User: "guest"})
	status, err := session.DetectElevation()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.Method != ElevationNone || status.Active {
		t.Errorf("expected ElevationNone inactive, got %+v", status)
	}
}

func TestHasCockpitBridge(t *testing.T) {
	presentRunner := &mockSSHRunner{
		output: "/usr/bin/cockpit-bridge\n",
	}
	session := NewBridgeSession(presentRunner, ssh.Config{Host: "192.168.1.150", Port: 22, User: "core"})
	if !session.HasCockpitBridge() {
		t.Errorf("expected HasCockpitBridge to be true when binary is found")
	}

	missingRunner := &mockSSHRunner{
		output: "NO_BRIDGE\n",
	}
	missingSession := NewBridgeSession(missingRunner, ssh.Config{Host: "192.168.1.150", Port: 22, User: "core"})
	if missingSession.HasCockpitBridge() {
		t.Errorf("expected HasCockpitBridge to be false when binary is missing")
	}
}
