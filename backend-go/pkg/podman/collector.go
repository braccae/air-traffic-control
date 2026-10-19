package podman

import (
	"encoding/json"
	"fmt"
	"strings"

	"air-traffic-control/backend/pkg/types"
)

// ContainerStats holds CPU and memory metrics parsed from podman stats --no-stream --format json
type ContainerStats struct {
	ID       string `json:"ID"`
	Name     string `json:"Name"`
	CPUPerc  string `json:"CPUPerc"`
	MemUsage string `json:"MemUsage"`
	MemPerc  string `json:"MemPerc"`
}

// CollectStats queries live container metrics using podman stats
func (m *Manager) CollectStats(user string, uid int) (map[string]ContainerStats, error) {
	var cmd string
	if user == "" || user == "root" || uid == 0 {
		cmd = "run0 --background=no podman stats --no-stream --format json 2>/dev/null || sudo -n podman stats --no-stream --format json 2>/dev/null || doas -n podman stats --no-stream --format json 2>/dev/null || podman stats --no-stream --format json 2>/dev/null"
	} else {
		cmd = fmt.Sprintf("run0 --background=no --user %s podman stats --no-stream --format json 2>/dev/null || sudo -n -u %s XDG_RUNTIME_DIR=/run/user/%d podman stats --no-stream --format json 2>/dev/null || doas -u %s XDG_RUNTIME_DIR=/run/user/%d podman stats --no-stream --format json 2>/dev/null || XDG_RUNTIME_DIR=/run/user/%d podman stats --no-stream --format json 2>/dev/null", user, user, uid, user, uid, uid)
	}

	out, err := m.sshMgr.RunCommand(m.cfg, cmd)
	if err != nil {
		return nil, err
	}
	return ParseStatsJSON(out)
}

// ParseStatsJSON parses podman stats json output into a map keyed by container ID and container Name
func ParseStatsJSON(out string) (map[string]ContainerStats, error) {
	out = strings.TrimSpace(out)
	if out == "" || out == "null" {
		return make(map[string]ContainerStats), nil
	}

	var statsList []struct {
		ID         string `json:"id"`
		IDUpper    string `json:"ID"`
		IdAlt      string `json:"Id"`
		Name       string `json:"name"`
		NameUpper  string `json:"Name"`
		CPUPerc    string `json:"cpu_percent"`
		CPUPercAlt string `json:"CPUPerc"`
		CPUAlt     string `json:"CPU"`
		MemUsage   string `json:"mem_usage"`
		MemAlt     string `json:"MemUsage"`
		MemoryAlt  string `json:"Memory"`
		MemPerc    string `json:"mem_percent"`
		MemPercAlt string `json:"MemPerc"`
	}

	if err := json.Unmarshal([]byte(out), &statsList); err != nil {
		return nil, fmt.Errorf("failed to parse stats json: %w", err)
	}

	result := make(map[string]ContainerStats)
	for _, s := range statsList {
		id := s.ID
		if id == "" {
			id = s.IDUpper
		}
		if id == "" {
			id = s.IdAlt
		}

		name := s.Name
		if name == "" {
			name = s.NameUpper
		}

		cpu := s.CPUPerc
		if cpu == "" {
			cpu = s.CPUPercAlt
		}
		if cpu == "" {
			cpu = s.CPUAlt
		}

		mem := s.MemUsage
		if mem == "" {
			mem = s.MemAlt
		}
		if mem == "" {
			mem = s.MemoryAlt
		}
		if idx := strings.Index(mem, "/"); idx != -1 {
			mem = strings.TrimSpace(mem[:idx])
		}

		memPerc := s.MemPerc
		if memPerc == "" {
			memPerc = s.MemPercAlt
		}

		stat := ContainerStats{
			ID:       id,
			Name:     name,
			CPUPerc:  cpu,
			MemUsage: mem,
			MemPerc:  memPerc,
		}

		if id != "" {
			result[id] = stat
			if len(id) > 12 {
				result[id[:12]] = stat
			}
		}
		if s.Name != "" {
			result[s.Name] = stat
		}
	}
	return result, nil
}

// EnrichContainersWithStats populates CPU and Memory in containers based on stats
func EnrichContainersWithStats(containers []types.PodmanContainer, stats map[string]ContainerStats) {
	for i := range containers {
		c := &containers[i]
		if stat, ok := stats[c.ID]; ok {
			if stat.CPUPerc != "" {
				c.CPU = stat.CPUPerc
			}
			if stat.MemUsage != "" {
				c.Memory = stat.MemUsage
			}
		} else if stat, ok := stats[c.Name]; ok {
			if stat.CPUPerc != "" {
				c.CPU = stat.CPUPerc
			}
			if stat.MemUsage != "" {
				c.Memory = stat.MemUsage
			}
		}
	}
}
