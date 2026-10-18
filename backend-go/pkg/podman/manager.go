package podman

import (
	"encoding/json"
	"fmt"

	"prox-orch/backend/pkg/ssh"
	"prox-orch/backend/pkg/types"
)

type Manager struct {
	sshMgr *ssh.ClientManager
	cfg    ssh.Config
}

func NewManager(sshMgr *ssh.ClientManager, cfg ssh.Config) *Manager {
	return &Manager{
		sshMgr: sshMgr,
		cfg:    cfg,
	}
}

// ListPods queries podman pod ps --format json
func (m *Manager) ListPods() ([]types.PodmanPod, error) {
	cmd := "podman pod ps --format json"
	out, err := m.sshMgr.RunCommand(m.cfg, cmd)
	if err != nil {
		return nil, fmt.Errorf("failed to list pods: %w", err)
	}

	var rawPods []struct {
		ID     string `json:"Id"`
		Name   string `json:"Name"`
		Status string `json:"Status"`
	}

	if err := json.Unmarshal([]byte(out), &rawPods); err != nil {
		return nil, fmt.Errorf("failed to parse pods json: %w", err)
	}

	var pods []types.PodmanPod
	for _, rp := range rawPods {
		pods = append(pods, types.PodmanPod{
			ID:     rp.ID,
			Name:   rp.Name,
			Status: rp.Status,
		})
	}
	return pods, nil
}

// RestartPod restarts a pod
func (m *Manager) RestartPod(podName string) error {
	cmd := fmt.Sprintf("podman pod restart %s", podName)
	_, err := m.sshMgr.RunCommand(m.cfg, cmd)
	return err
}

// ContainerAction performs start, stop, restart, or inspect
func (m *Manager) ContainerAction(containerName, action string) error {
	cmd := fmt.Sprintf("podman %s %s", action, containerName)
	_, err := m.sshMgr.RunCommand(m.cfg, cmd)
	return err
}

// GetContainerLogs returns recent logs
func (m *Manager) GetContainerLogs(containerName string, tail int) (string, error) {
	cmd := fmt.Sprintf("podman logs --tail %d %s", tail, containerName)
	return m.sshMgr.RunCommand(m.cfg, cmd)
}
