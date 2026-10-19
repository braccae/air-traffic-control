package restic

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"prox-orch/backend/pkg/ssh"
	"prox-orch/backend/pkg/types"
)

// CommandRunner abstracts remote command execution
type CommandRunner interface {
	RunCommand(cfg ssh.Config, command string) (string, error)
}

type Manager struct {
	sshMgr CommandRunner
	cfg    ssh.Config
	repo   string
}

func escapeSingleQuotes(s string) string {
	return strings.ReplaceAll(s, "'", "'\\''")
}

func NewManager(sshMgr CommandRunner, cfg ssh.Config, repo string) *Manager {
	return &Manager{
		sshMgr: sshMgr,
		cfg:    cfg,
		repo:   repo,
	}
}

// ListSnapshots returns all snapshots stored in the restic repository
func (m *Manager) ListSnapshots(password string) ([]types.ResticSnapshot, error) {
	cmd := fmt.Sprintf("env RESTIC_PASSWORD='%s' restic -r '%s' snapshots --json", escapeSingleQuotes(password), escapeSingleQuotes(m.repo))
	out, err := m.sshMgr.RunCommand(m.cfg, cmd)
	if err != nil {
		return nil, fmt.Errorf("failed to list restic snapshots: %w", err)
	}

	var raw []struct {
		Time     time.Time `json:"time"`
		ID       string    `json:"id"`
		ShortID  string    `json:"short_id"`
		Hostname string    `json:"hostname"`
		Paths    []string  `json:"paths"`
		Tags     []string  `json:"tags"`
		Summary  *struct {
			TotalBytesProcessed int64 `json:"total_bytes_processed"`
		} `json:"summary"`
	}

	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		return nil, fmt.Errorf("failed to parse restic json: %w", err)
	}

	var result []types.ResticSnapshot
	for _, s := range raw {
		sizeStr := ""
		if s.Summary != nil && s.Summary.TotalBytesProcessed > 0 {
			sizeStr = fmt.Sprintf("%.1f GiB", float64(s.Summary.TotalBytesProcessed)/(1024*1024*1024))
		}
		result = append(result, types.ResticSnapshot{
			ID:            s.ID,
			ShortID:       s.ShortID,
			Time:          s.Time,
			Paths:         s.Paths,
			Tags:          s.Tags,
			Hostname:      s.Hostname,
			Size:          sizeStr,
			Deduplication: "verified",
		})
	}
	return result, nil
}

// CreateSnapshot triggers an immediate restic backup of targeted directories
func (m *Manager) CreateSnapshot(password string, paths []string, tags []string) (string, error) {
	tagArgs := ""
	for _, t := range tags {
		tagArgs += fmt.Sprintf(" --tag %s", escapeSingleQuotes(t))
	}
	pathArgs := ""
	for _, p := range paths {
		pathArgs += fmt.Sprintf(" '%s'", escapeSingleQuotes(p))
	}

	cmd := fmt.Sprintf("env RESTIC_PASSWORD='%s' restic -r '%s' backup%s%s --json", escapeSingleQuotes(password), escapeSingleQuotes(m.repo), tagArgs, pathArgs)
	return m.sshMgr.RunCommand(m.cfg, cmd)
}

// IntegrityCheck runs restic check to verify repository index and pack hashes
func (m *Manager) IntegrityCheck(password string) (string, error) {
	cmd := fmt.Sprintf("env RESTIC_PASSWORD='%s' restic -r '%s' check", escapeSingleQuotes(password), escapeSingleQuotes(m.repo))
	return m.sshMgr.RunCommand(m.cfg, cmd)
}
