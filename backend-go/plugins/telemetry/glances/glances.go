package glances

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"air-traffic-control/backend/pkg/plugin"
)

func init() {
	_ = plugin.Register(New())
}

// Plugin implements 4-tier telemetry collection (Glances REST API -> loopback -> CLI -> proc/sysfs).
type Plugin struct{}

func New() *Plugin {
	return &Plugin{}
}

func (p *Plugin) Meta() plugin.Meta {
	return plugin.Meta{
		ID:          "telemetry/glances",
		Kind:        plugin.KindTelemetry,
		Name:        "glances",
		Label:       "Glances Telemetry & Metrics",
		Icon:        "monitoring",
		Description: "Multi-tier real-time host metrics: CPU, memory, load averages, sensors, and thermal stats",
		Order:       10,
	}
}

func (p *Plugin) Compatible(h plugin.HostFacts) bool {
	return h.OSFamily != "windows" && h.OSFamily != "router_switch"
}

func (p *Plugin) Requirements() plugin.Requirements {
	return plugin.Requirements{
		HostPackages: map[string][]string{
			"dnf": {"glances", "python3-psutil"},
			"apt": {"glances", "python3-psutil"},
			"apk": {"glances", "py3-psutil"},
		},
	}
}

func (p *Plugin) Probe(ctx context.Context, r plugin.Runner, h plugin.HostFacts) (plugin.Detection, error) {
	if r == nil {
		return plugin.Detection{Detected: false, Status: "OFFLINE", Error: "runner is nil"}, nil
	}

	// Tier 1 / 2 check: curl glances daemon port 61208
	out, err := r.RunCommand(ctx, "curl -s -m 2 http://127.0.0.1:61208/api/4/status 2>/dev/null || curl -s -m 2 http://127.0.0.1:61208/api/3/status 2>/dev/null || true")
	if err == nil && strings.TrimSpace(out) != "" {
		return plugin.Detection{
			Detected: true,
			Status:   "ONLINE",
			Details:  "Glances REST daemon active (port 61208)",
		}, nil
	}

	// Tier 3 check: glances CLI
	cliOut, err := r.RunCommand(ctx, "command -v glances")
	if err == nil && strings.TrimSpace(cliOut) != "" {
		return plugin.Detection{
			Detected: true,
			Status:   "ONLINE",
			Details:  "Glances CLI available",
		}, nil
	}

	// Tier 4 check: /proc/stat fallback
	procOut, err := r.RunCommand(ctx, "test -r /proc/stat && echo 'PROC_OK' || true")
	if err == nil && strings.Contains(procOut, "PROC_OK") {
		return plugin.Detection{
			Detected: true,
			Status:   "ONLINE",
			Details:  "Proc / sysfs fallback telemetry active",
		}, nil
	}

	return plugin.Detection{
		Detected: false,
		Status:   "UNAVAILABLE",
		Error:    "neither glances nor readable /proc/stat found",
	}, nil
}

func (p *Plugin) Read(ctx context.Context, r plugin.Runner, h plugin.HostFacts) (any, error) {
	if r == nil {
		return nil, fmt.Errorf("runner is nil")
	}

	// Tier 1/2: Query local Glances REST API
	restCmd := "curl -s -m 2 http://127.0.0.1:61208/api/4/all 2>/dev/null || curl -s -m 2 http://127.0.0.1:61208/api/3/all 2>/dev/null || true"
	restOut, err := r.RunCommand(ctx, restCmd)
	if err == nil && strings.TrimSpace(restOut) != "" && strings.HasPrefix(strings.TrimSpace(restOut), "{") {
		var payload map[string]any
		if err := json.Unmarshal([]byte(restOut), &payload); err == nil {
			return payload, nil
		}
	}

	// Tier 3: Execute glances CLI with stdout JSON
	cliCmd := "glances --stdout-json cpu,mem,load,sensors 2>/dev/null || true"
	cliOut, err := r.RunCommand(ctx, cliCmd)
	if err == nil && strings.TrimSpace(cliOut) != "" && strings.HasPrefix(strings.TrimSpace(cliOut), "{") {
		var payload map[string]any
		if err := json.Unmarshal([]byte(cliOut), &payload); err == nil {
			return payload, nil
		}
	}

	// Tier 4: /proc/stat and /proc/meminfo fallback
	procCmd := "cat /proc/loadavg /proc/meminfo 2>/dev/null || true"
	procOut, err := r.RunCommand(ctx, procCmd)
	if err != nil {
		return nil, err
	}

	var load1, load5, load15 float64
	var memTotalKb, memAvailKb float64

	lines := strings.Split(procOut, "\n")
	for _, l := range lines {
		fields := strings.Fields(l)
		if len(fields) >= 3 && load1 == 0 && strings.Contains(fields[0], ".") {
			load1, _ = strconv.ParseFloat(fields[0], 64)
			load5, _ = strconv.ParseFloat(fields[1], 64)
			load15, _ = strconv.ParseFloat(fields[2], 64)
		} else if strings.HasPrefix(l, "MemTotal:") && len(fields) >= 2 {
			memTotalKb, _ = strconv.ParseFloat(fields[1], 64)
		} else if strings.HasPrefix(l, "MemAvailable:") && len(fields) >= 2 {
			memAvailKb, _ = strconv.ParseFloat(fields[1], 64)
		}
	}

	memUsedKb := memTotalKb - memAvailKb
	memPercent := 0.0
	if memTotalKb > 0 {
		memPercent = (memUsedKb / memTotalKb) * 100.0
	}

	return map[string]any{
		"load": map[string]any{
			"min1":  load1,
			"min5":  load5,
			"min15": load15,
		},
		"mem": map[string]any{
			"total": memTotalKb * 1024,
			"used":  memUsedKb * 1024,
			"free":  memAvailKb * 1024,
			"percent": memPercent,
		},
		"fallback": true,
	}, nil
}
