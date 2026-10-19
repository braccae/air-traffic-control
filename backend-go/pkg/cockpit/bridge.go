package cockpit

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"

	"prox-orch/backend/pkg/ssh"
)

// ElevationMethod represents privilege escalation mechanism
type ElevationMethod string

const (
	ElevationNone ElevationMethod = "none"
	ElevationRun0 ElevationMethod = "run0"
	ElevationSudo ElevationMethod = "sudo"
	ElevationDoas ElevationMethod = "doas"
)

// PrivilegeStatus describes remote host privilege elevation status
type PrivilegeStatus struct {
	Method     ElevationMethod `json:"method"`
	Active     bool            `json:"active"`
	User       string          `json:"user"`
	CanElevate bool            `json:"canElevate"`
	Error      string          `json:"error,omitempty"`
}

// CommandRunner abstracts remote command execution
type CommandRunner interface {
	RunCommand(cfg ssh.Config, command string) (string, error)
}

// BridgeSession wraps an interactive cockpit-bridge process over SSH
type BridgeSession struct {
	mu              sync.Mutex
	sshMgr          CommandRunner
	cfg             ssh.Config
	elevationMethod ElevationMethod
	stdin           io.WriteCloser
	stdout          io.ReadCloser
}

// LogEntry represents a journald or dmesg line parsed via Cockpit
type LogEntry struct {
	Timestamp string `json:"timestamp"`
	Priority  string `json:"priority"`
	Service   string `json:"service"`
	Message   string `json:"message"`
}

// ServiceStatus represents a systemd service managed through Cockpit
type ServiceStatus struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	LoadState   string `json:"loadState"`
	ActiveState string `json:"activeState"`
	SubState    string `json:"subState"`
}

// StripLeadingPrivilege strips existing sudo, run0, or doas prefix from a command
func StripLeadingPrivilege(cmd string) string {
	trimmed := strings.TrimSpace(cmd)
	if strings.HasPrefix(trimmed, "sudo -n ") {
		return strings.TrimSpace(strings.TrimPrefix(trimmed, "sudo -n "))
	}
	if strings.HasPrefix(trimmed, "sudo ") {
		return strings.TrimSpace(strings.TrimPrefix(trimmed, "sudo "))
	}
	if strings.HasPrefix(trimmed, "run0 --background=no ") {
		return strings.TrimSpace(strings.TrimPrefix(trimmed, "run0 --background=no "))
	}
	if strings.HasPrefix(trimmed, "run0 ") {
		return strings.TrimSpace(strings.TrimPrefix(trimmed, "run0 "))
	}
	if strings.HasPrefix(trimmed, "doas -n ") {
		return strings.TrimSpace(strings.TrimPrefix(trimmed, "doas -n "))
	}
	if strings.HasPrefix(trimmed, "doas ") {
		return strings.TrimSpace(strings.TrimPrefix(trimmed, "doas "))
	}
	return trimmed
}

// WrapElevatedCommand wraps command using run0 (preferred), sudo, or doas
func WrapElevatedCommand(cmd string, method ElevationMethod, user string) string {
	cleanCmd := StripLeadingPrivilege(cmd)
	if cleanCmd == "" {
		return ""
	}
	if user == "root" || method == ElevationNone {
		return cleanCmd
	}
	switch method {
	case ElevationRun0:
		return fmt.Sprintf("run0 --background=no %s", cleanCmd)
	case ElevationSudo:
		return fmt.Sprintf("sudo -n %s", cleanCmd)
	case ElevationDoas:
		return fmt.Sprintf("doas -n %s", cleanCmd)
	default:
		return cleanCmd
	}
}

// NewBridgeSession initializes connection to remote cockpit-bridge
func NewBridgeSession(sshMgr CommandRunner, cfg ssh.Config) *BridgeSession {
	return &BridgeSession{
		sshMgr:          sshMgr,
		cfg:             cfg,
		elevationMethod: ElevationNone,
	}
}

// SetElevationMethod sets the elevation mechanism
func (b *BridgeSession) SetElevationMethod(method ElevationMethod) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.elevationMethod = method
}

// GetElevationMethod returns current elevation mechanism
func (b *BridgeSession) GetElevationMethod() ElevationMethod {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.elevationMethod
}

// WrapElevated wraps a command using the session's configured elevation method and user
func (b *BridgeSession) WrapElevated(cmd string) string {
	b.mu.Lock()
	method := b.elevationMethod
	b.mu.Unlock()
	return WrapElevatedCommand(cmd, method, b.cfg.User)
}

// DetectElevation probes remote host for run0, sudo, and doas support, preferring run0 -> sudo -> doas -> none
func (b *BridgeSession) DetectElevation() (PrivilegeStatus, error) {
	if b.cfg.User == "root" {
		b.SetElevationMethod(ElevationNone)
		return PrivilegeStatus{
			Method:     ElevationNone,
			Active:     true,
			User:       "root",
			CanElevate: true,
		}, nil
	}

	// Probe run0 first (systemd 256+ polkit session)
	run0Check := "which run0 >/dev/null 2>&1 && run0 --background=no id -u"
	out, err := b.sshMgr.RunCommand(b.cfg, run0Check)
	if err == nil && strings.TrimSpace(out) == "0" {
		b.SetElevationMethod(ElevationRun0)
		return PrivilegeStatus{
			Method:     ElevationRun0,
			Active:     true,
			User:       b.cfg.User,
			CanElevate: true,
		}, nil
	}

	// Probe sudo fallback (passwordless sudo -n)
	sudoCheck := "sudo -n id -u"
	out, err = b.sshMgr.RunCommand(b.cfg, sudoCheck)
	if err == nil && strings.TrimSpace(out) == "0" {
		b.SetElevationMethod(ElevationSudo)
		return PrivilegeStatus{
			Method:     ElevationSudo,
			Active:     true,
			User:       b.cfg.User,
			CanElevate: true,
		}, nil
	}

	// Probe doas fallback (OpenBSD / Alpine / custom Linux)
	doasCheck := "which doas >/dev/null 2>&1 && doas -n id -u"
	out, err = b.sshMgr.RunCommand(b.cfg, doasCheck)
	if err == nil && strings.TrimSpace(out) == "0" {
		b.SetElevationMethod(ElevationDoas)
		return PrivilegeStatus{
			Method:     ElevationDoas,
			Active:     true,
			User:       b.cfg.User,
			CanElevate: true,
		}, nil
	}

	b.SetElevationMethod(ElevationNone)
	return PrivilegeStatus{
		Method:     ElevationNone,
		Active:     false,
		User:       b.cfg.User,
		CanElevate: false,
	}, nil
}

// HasCockpitBridge checks if the optional cockpit-bridge binary is available on the remote host.
// The system operates fully agentless over SSH; cockpit-bridge is never a mandatory requirement.
func (b *BridgeSession) HasCockpitBridge() bool {
	out, err := b.sshMgr.RunCommand(b.cfg, "which cockpit-bridge 2>/dev/null || echo NO_BRIDGE")
	if err != nil {
		return false
	}
	return !strings.Contains(out, "NO_BRIDGE") && strings.TrimSpace(out) != ""
}

// FetchRecentLogs fetches recent system journal logs via journalctl/cockpit-bridge
func (b *BridgeSession) FetchRecentLogs(lines int, filter string) ([]LogEntry, error) {
	cmd := fmt.Sprintf("journalctl -n %d --output=json", lines)
	if filter != "" {
		cmd += fmt.Sprintf(" -u %s", filter)
	}

	out, err := b.sshMgr.RunCommand(b.cfg, cmd)
	if err != nil {
		return nil, fmt.Errorf("failed to read journal logs: %w", err)
	}

	var results []LogEntry

	// Parse JSON output from journalctl
	linesArr := splitLines(out)
	for _, l := range linesArr {
		if len(l) == 0 {
			continue
		}
		var parsed map[string]interface{}
		if err := json.Unmarshal([]byte(l), &parsed); err == nil {
			msg, _ := parsed["MESSAGE"].(string)
			unit, _ := parsed["_SYSTEMD_UNIT"].(string)
			ts, _ := parsed["__REALTIME_TIMESTAMP"].(string)
			results = append(results, LogEntry{
				Timestamp: ts,
				Service:   unit,
				Message:   msg,
				Priority:  "info",
			})
		}
	}
	return results, nil
}

// ControlService controls a systemd unit via systemctl or cockpit-bridge dbus with privilege elevation
func (b *BridgeSession) ControlService(serviceName, action string) error {
	rawCmd := fmt.Sprintf("systemctl %s %s", action, serviceName)
	cmd := b.WrapElevated(rawCmd)
	_, err := b.sshMgr.RunCommand(b.cfg, cmd)
	return err
}

func splitLines(s string) []string {
	var lines []string
	curr := ""
	for _, ch := range s {
		if ch == '\n' {
			lines = append(lines, curr)
			curr = ""
		} else {
			curr += string(ch)
		}
	}
	if curr != "" {
		lines = append(lines, curr)
	}
	return lines
}
