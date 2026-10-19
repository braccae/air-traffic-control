package glances

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"prox-orch/backend/pkg/types"
)

// Client interfaces with Glances REST API (typically on port 61208)
type Client struct {
	baseURL    string
	httpClient *http.Client
}

func NewClient(host string, port int) *Client {
	return &Client{
		baseURL: fmt.Sprintf("http://%s:%d/api/3", host, port),
		httpClient: &http.Client{
			Timeout: 3 * time.Second,
		},
	}
}

// FetchTelemetry queries the Glances API for CPU, mem, sensors, and disk
func (c *Client) FetchTelemetry(nodeID string) (*types.Telemetry, error) {
	resp, err := c.httpClient.Get(fmt.Sprintf("%s/all", c.baseURL))
	if err != nil {
		return nil, fmt.Errorf("failed to query glances api: %w", err)
	}
	defer resp.Body.Close()

	var payload map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("failed to decode glances payload: %w", err)
	}

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
	if sensors, ok := payload["sensors"].([]interface{}); ok {
		for _, s := range sensors {
			if sMap, ok := s.(map[string]interface{}); ok {
				if val, ok := sMap["value"].(float64); ok && val > 0 {
					cpuTemp = val
					break
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
	}, nil
}
