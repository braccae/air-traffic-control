package glances

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"prox-orch/backend/pkg/types"
)

// Client interfaces with Glances REST API (typically on port 61208) or local CLI fallback
type Client struct {
	host       string
	port       int
	httpClient *http.Client
}

func NewClient(host string, port int) *Client {
	return &Client{
		host: host,
		port: port,
		httpClient: &http.Client{
			Timeout: 3 * time.Second,
		},
	}
}

// FetchTelemetry queries the Glances API (or local CLI) for CPU, mem, sensors, and load
func (c *Client) FetchTelemetry(nodeID string) (*types.Telemetry, error) {
	// Try REST API endpoints (/api/4/all then /api/3/all)
	endpoints := []string{
		fmt.Sprintf("http://%s:%d/api/4/all", c.host, c.port),
		fmt.Sprintf("http://%s:%d/api/3/all", c.host, c.port),
	}

	for _, endpoint := range endpoints {
		resp, err := c.httpClient.Get(endpoint)
		if err == nil && resp.StatusCode == http.StatusOK {
			defer resp.Body.Close()
			var payload map[string]interface{}
			if err := json.NewDecoder(resp.Body).Decode(&payload); err == nil {
				return parseGlancesPayload(nodeID, payload)
			}
		}
		if resp != nil {
			resp.Body.Close()
		}
	}

	// If REST API fails or is unavailable, check if target is local to fall back to glances CLI
	isLocal := c.host == "127.0.0.1" || c.host == "localhost" || c.host == "dev-pc" || c.host == "" || strings.HasPrefix(c.host, "127.")
	if isLocal {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, "glances", "--stdout-json", "now,cpu,mem,load,sensors").Output()
		if err == nil && len(out) > 0 {
			var payload map[string]interface{}
			if err := json.Unmarshal(out, &payload); err == nil {
				return parseGlancesPayload(nodeID, payload)
			}
		}
	}

	return nil, fmt.Errorf("failed to query glances api or cli for host %s", c.host)
}

func parseGlancesPayload(nodeID string, payload map[string]interface{}) (*types.Telemetry, error) {
	cpuMap, _ := payload["cpu"].(map[string]interface{})
	memMap, _ := payload["mem"].(map[string]interface{})
	loadMap, _ := payload["load"].(map[string]interface{})

	cpuTotal, _ := cpuMap["total"].(float64)
	memUsed, _ := memMap["used"].(float64)
	memTotal, _ := memMap["total"].(float64)
	min1, _ := loadMap["min1"].(float64)
	min5, _ := loadMap["min5"].(float64)
	min15, _ := loadMap["min15"].(float64)

	var cpuTemp float64
	var fanSpeed int
	if sensors, ok := payload["sensors"].([]interface{}); ok {
		for _, s := range sensors {
			if sMap, ok := s.(map[string]interface{}); ok {
				sType, _ := sMap["type"].(string)
				val, _ := sMap["value"].(float64)
				label, _ := sMap["label"].(string)

				if val > 0 {
					if (sType == "temperature_core" || strings.Contains(strings.ToLower(label), "cpu")) && cpuTemp == 0 {
						cpuTemp = val
					}
					if (sType == "fan_speed" || strings.Contains(strings.ToLower(label), "fan")) && fanSpeed == 0 {
						fanSpeed = int(val)
					}
				}
			}
		}
	}

	var cpuModel string
	var vcpus int
	if coreMap, ok := payload["core"].(map[string]interface{}); ok {
		if c, ok := coreMap["phys"].(float64); ok {
			vcpus = int(c)
		} else if c, ok := coreMap["log"].(float64); ok {
			vcpus = int(c)
		}
	}
	if vcpus == 0 {
		if c, ok := cpuMap["cpucore"].(float64); ok {
			vcpus = int(c)
		}
	}
	if sysMap, ok := payload["system"].(map[string]interface{}); ok {
		if m, ok := sysMap["cpu_name"].(string); ok {
			cpuModel = m
		}
	}

	return &types.Telemetry{
		NodeID:         nodeID,
		Timestamp:      time.Now(),
		CPUUsage:       cpuTotal,
		CPUModel:       cpuModel,
		VCPUs:          vcpus,
		MemoryUsedGiB:  memUsed / (1024 * 1024 * 1024),
		MemoryTotalGiB: memTotal / (1024 * 1024 * 1024),
		LoadAvg1m:      min1,
		LoadAvg5m:      min5,
		LoadAvg15m:     min15,
		CPUTempC:       cpuTemp,
		FanSpeedRPM:    fanSpeed,
	}, nil
}
