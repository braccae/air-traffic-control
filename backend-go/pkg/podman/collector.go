package podman

import (
	"encoding/json"
	"fmt"
	"strings"

	"air-traffic-control/backend/pkg/types"
)

// ContainerStats holds CPU and memory metrics parsed from podman stats --no-stream --format json
// or from the Podman socket API (/v4.0.0/libpod/containers/stats?stream=false).
type ContainerStats struct {
	ID       string `json:"ID"`
	Name     string `json:"Name"`
	CPUPerc  string `json:"CPUPerc"`
	MemUsage string `json:"MemUsage"`
	MemPerc  string `json:"MemPerc"`
}

// CollectStats queries live container metrics using the Podman socket API first
// (escalating with run0 -> sudo -n -> doas -n -> none), falling back to native podman stats CLI.
func (m *Manager) CollectStats(user string, uid int) (map[string]ContainerStats, error) {
	var cmd string
	if user == "" || user == "root" || uid == 0 {
		cmd = RootSocketOrCLICommand("/v4.0.0/libpod/containers/stats?stream=false", "podman stats --no-stream --format json")
	} else {
		cmd = UserSocketOrCLICommand(user, uid, "/v4.0.0/libpod/containers/stats?stream=false", "podman stats --no-stream --format json")
	}

	out, err := m.sshMgr.RunCommand(m.cfg, cmd)
	if err != nil {
		return nil, err
	}
	return ParseStatsJSON(out)
}

func formatBytesShort(b uint64) string {
	const (
		kb = 1000
		mb = 1000 * kb
		gb = 1000 * mb
	)
	switch {
	case b >= gb:
		return fmt.Sprintf("%.2fGB", float64(b)/float64(gb))
	case b >= mb:
		return fmt.Sprintf("%.1fMB", float64(b)/float64(mb))
	case b >= kb:
		return fmt.Sprintf("%.1fkB", float64(b)/float64(kb))
	default:
		return fmt.Sprintf("%dB", b)
	}
}

	// ParseStatsJSON parses podman stats json output (either CLI array or Libpod socket {"Stats":[...]})
// into a map keyed by container ID (full and 12-char short) and container Name.
func ParseStatsJSON(out string) (map[string]ContainerStats, error) {
	out = strings.TrimSpace(out)
	if out == "" || out == "null" {
		return make(map[string]ContainerStats), nil
	}

	// Handle Libpod socket API envelope: {"Error":null,"Stats":[...]}
	if strings.HasPrefix(out, "{") {
		var socketEnvelope struct {
			Stats []struct {
				ContainerID string  `json:"ContainerID"`
				Name        string  `json:"Name"`
				CPU         float64 `json:"CPU"`
				AvgCPU      float64 `json:"AvgCPU"`
				MemUsage    uint64  `json:"MemUsage"`
				MemPerc     float64 `json:"MemPerc"`
			} `json:"Stats"`
		}
		if err := json.Unmarshal([]byte(out), &socketEnvelope); err != nil {
			return nil, fmt.Errorf("failed to parse socket stats json: %w", err)
		}
		result := make(map[string]ContainerStats)
		for _, s := range socketEnvelope.Stats {
			cpuVal := s.CPU
			if cpuVal == 0 && s.AvgCPU > 0 {
				cpuVal = s.AvgCPU
			}
			cpuStr := fmt.Sprintf("%.2f%%", cpuVal)
			memStr := ""
			if s.MemUsage > 0 {
				memStr = formatBytesShort(s.MemUsage)
			}
			memPercStr := fmt.Sprintf("%.2f%%", s.MemPerc)
			stat := ContainerStats{
				ID:       s.ContainerID,
				Name:     s.Name,
				CPUPerc:  cpuStr,
				MemUsage: memStr,
				MemPerc:  memPercStr,
			}
			if s.ContainerID != "" {
				result[s.ContainerID] = stat
				if len(s.ContainerID) > 12 {
					result[s.ContainerID[:12]] = stat
				}
			}
			if s.Name != "" {
				result[s.Name] = stat
			}
		}
		return result, nil
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
		var stat ContainerStats
		var ok bool
		if stat, ok = stats[c.ID]; !ok {
			if len(c.ID) > 12 {
				stat, ok = stats[c.ID[:12]]
			}
		}
		if !ok {
			stat, ok = stats[c.Name]
		}
		if ok {
			if stat.CPUPerc != "" {
				c.CPU = stat.CPUPerc
			}
			if stat.MemUsage != "" {
				c.Memory = stat.MemUsage
			}
		}
	}
}
