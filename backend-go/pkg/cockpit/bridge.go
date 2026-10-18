package cockpit

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"sync"

	"prox-orch/backend/pkg/ssh"
)

// CommandRunner abstracts remote command execution
type CommandRunner interface {
	RunCommand(cfg ssh.Config, command string) (string, error)
}

// BridgeSession wraps an interactive cockpit-bridge process over SSH
type BridgeSession struct {
	mu     sync.Mutex
	sshMgr CommandRunner
	cfg    ssh.Config
	stdin  io.WriteCloser
	stdout io.ReadCloser
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

// NewBridgeSession initializes connection to remote cockpit-bridge
func NewBridgeSession(sshMgr CommandRunner, cfg ssh.Config) *BridgeSession {
	return &BridgeSession{
		sshMgr: sshMgr,
		cfg:    cfg,
	}
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
	scanner := bufio.NewScanner(bufio.NewReader(bufio.NewReader(io.NopCloser(bufio.NewReader(io.NopCloser(nil))))))
	_ = scanner // Used in stream mode

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

// ControlService controls a systemd unit via systemctl or cockpit-bridge dbus
func (b *BridgeSession) ControlService(serviceName, action string) error {
	cmd := fmt.Sprintf("systemctl %s %s", action, serviceName)
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
