package readonlyapi

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"air-traffic-control/backend/pkg/glances"
	"air-traffic-control/backend/pkg/hostcache"
	"air-traffic-control/backend/pkg/plugin"
)

// telemetryScript is the exact probe script src/utils/liveCluster.ts runs in
// fetchLiveNodeTelemetry, joined the same way (&& + ---SPLIT--- markers).
const telemetryScript = "cat /proc/loadavg" +
	" && echo \"---SPLIT---\" && " +
	"free -m | awk '/^Mem:/ {print $2, $3, $6}'" +
	" && echo \"---SPLIT---\" && " +
	"nproc" +
	" && echo \"---SPLIT---\" && " +
	"lscpu | awk -F: '/Model name/ {gsub(/^[ \\t]+/, \"\", $2); print $2; exit}'" +
	" && echo \"---SPLIT---\" && " +
	"uptime -p 2>/dev/null || uptime" +
	" && echo \"---SPLIT---\" && " +
	". /etc/os-release 2>/dev/null && echo \"$PRETTY_NAME\"" +
	" && echo \"---SPLIT---\" && " +
	"df -BG / | awk 'NR==2 {gsub(/G/, \"\", $2); gsub(/G/, \"\", $3); print $2, $3}'" +
	" && echo \"---SPLIT---\" && " +
	"which glances 2>/dev/null || echo \"NO_GLANCES\"" +
	" && echo \"---SPLIT---\" && " +
	"which pcp 2>/dev/null || echo \"NO_PCP\"" +
	" && echo \"---SPLIT---\" && " +
	"which cockpit-bridge 2>/dev/null || echo \"NO_COCKPIT\"" +
	" && echo \"---SPLIT---\" && " +
	"cat /proc/spl/kstat/zfs/arcstats 2>/dev/null | awk '/^size/ {print $3}' || echo \"NO_ARC\"" +
	" && echo \"---SPLIT---\" && " +
	"zpool list -H -o name,size,alloc,free,cap,health 2>/dev/null | head -n 1 || echo \"NO_POOL\""

// nodeTelemetryInfo is the subset of the NodeItem JSON blob the telemetry probe
// needs (route selection + Glances port).
type nodeTelemetryInfo struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	ShortName     string `json:"shortName"`
	IP            string `json:"ip"`
	TailscaleIP   string `json:"tailscaleIp"`
	TailscaleHost string `json:"tailscaleHost"`
	FallbackIP    string `json:"fallbackIp"`
	APIPort       int    `json:"apiPort"`
}

func parseTelemetryInfo(rec hostcache.NodeRecord) nodeTelemetryInfo {
	var info nodeTelemetryInfo
	if len(rec.NodeJSON) > 0 {
		_ = json.Unmarshal(rec.NodeJSON, &info)
	}
	if info.ID == "" {
		info.ID = rec.ID
	}
	if info.Name == "" {
		info.Name = rec.Name
	}
	if info.TailscaleIP == "" {
		info.TailscaleIP = rec.TailscaleIP
	}
	if info.IP == "" {
		info.IP = rec.IP
	}
	if info.TailscaleHost == "" {
		info.TailscaleHost = rec.TailscaleHost
	}
	return info
}

// glanceRoute mirrors the TS route preference: tailscaleIp || ip || tailscaleHost || fallbackIp || 127.0.0.1.
func (i nodeTelemetryInfo) glanceRoute() string {
	for _, candidate := range []string{i.TailscaleIP, i.IP, i.TailscaleHost, i.FallbackIP} {
		if strings.TrimSpace(candidate) != "" {
			return strings.TrimSpace(candidate)
		}
	}
	return "127.0.0.1"
}

// baseTelemetry returns the TelemetryData key set with zero values. Keys match
// src/types/index.ts TelemetryData so the response shape is identical to
// Express; values are only ever real measurements or an explicit zero/empty
// "not measured" (no synthetic jitter).
func baseTelemetry() map[string]any {
	return map[string]any{
		"cpuUsage":       0.0,
		"cpuModel":       "",
		"vCpus":          0,
		"memoryUsedGiB":  0.0,
		"memoryTotalGiB": 0.0,
		"arcCacheGiB":    0.0,
		"zfsTankUsedTb":  0.0,
		"zfsTankTotalTb": 0.0,
		"zfsRaidType":    "",
		"zfsScrubStatus": "",
		"loadAvg1m":      0.0,
		"loadAvg5m":      0.0,
		"loadAvg15m":     0.0,
		"ioDelayPercent": 0.0,
		"cpuTempC":       0.0,
		"fanSpeedRpm":    0.0,
		"packageWatts":   0.0,
		"sparkline":      []float64{},
		"metricsSource":  "kernel",
		"buffCacheGiB":   0.0,
	}
}

// telemetryState is the process-level last-known telemetry, mirroring the
// `telemetry` singleton in server.ts (minus its hard-coded seed values).
var telemetryState = struct {
	sync.RWMutex
	values map[string]any
}{values: baseTelemetry()}

func currentTelemetry() map[string]any {
	telemetryState.RLock()
	defer telemetryState.RUnlock()
	out := make(map[string]any, len(telemetryState.values))
	for k, v := range telemetryState.values {
		out[k] = v
	}
	return out
}

// mergeTelemetry returns base < current < override, matching the TS
// `{...telemetry, ...live}` / `{...telemetry, ...cached.telemetry}` spread.
func mergeTelemetry(override map[string]any) map[string]any {
	merged := baseTelemetry()
	for k, v := range currentTelemetry() {
		merged[k] = v
	}
	for k, v := range override {
		merged[k] = v
	}
	return merged
}

// storeTelemetry records the fresh measurement as the process-level state and
// maintains a real sparkline history (appended, never synthesised).
func storeTelemetry(live map[string]any) map[string]any {
	merged := mergeTelemetry(live)

	history := []float64{}
	if prev, ok := merged["sparkline"].([]float64); ok {
		history = append(history, prev...)
	}
	if usage, ok := merged["cpuUsage"].(float64); ok {
		history = append(history, usage)
	}
	if len(history) > 20 {
		history = history[len(history)-20:]
	}
	merged["sparkline"] = history

	telemetryState.Lock()
	telemetryState.values = merged
	telemetryState.Unlock()

	return merged
}

// handleTelemetry ports GET /api/telemetry (server.ts).
//
// Cached path: 15s host_telemetry_cache TTL, response spread over the
// process-level last telemetry with fromCache:true.
// Fresh path: multi-tier Glances probe + native probe script over the real SSH
// runner, persisted to the telemetry cache and to the host row's uptime /
// osDescription.
//
// Zero-mock deviation from Express: when the host cannot be reached this route
// returns 502 with an honest error instead of fabricating node.specs-derived
// metrics.
func (a *API) handleTelemetry(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	q := r.URL.Query()
	query := q.Get("host")
	if query == "" {
		query = q.Get("node")
	}
	if query == "" {
		query = q.Get("id")
	}

	node, err := a.findNode(query)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	rec, ok, err := a.store.GetHostByID(node.ID)
	if err != nil || !ok {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"success": false,
			"error":   fmt.Sprintf("failed to load node %s", node.ID),
		})
		return
	}

	if q.Get("refresh") != "true" {
		if entry, found, cErr := a.store.GetHostTelemetry(node.ID); cErr == nil && found &&
			hostcache.Fresh(entry.UpdatedAt, hostcache.TelemetryTTL) {
			var cached map[string]any
			if json.Unmarshal(entry.TelemetryJSON, &cached) == nil {
				resp := mergeTelemetry(cached)
				resp["node"] = json.RawMessage(rec.NodeJSON)
				resp["fromCache"] = true
				resp["cachedAt"] = entry.UpdatedAt
				writeJSON(w, http.StatusOK, resp)
				return
			}
		}
	}

	live, err := a.collectLiveTelemetry(r.Context(), node.ID, parseTelemetryInfo(rec))
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	if raw, mErr := json.Marshal(live); mErr == nil {
		_ = a.store.UpsertHostTelemetry(node.ID, raw)
	}
	resp := storeTelemetry(live)

	// Persist real uptime/osDescription onto the host row exactly like Express.
	fields := map[string]any{}
	if up, ok := live["uptime"].(string); ok && up != "" {
		fields["uptime"] = up
	}
	if osDesc, ok := live["osDescription"].(string); ok && osDesc != "" {
		fields["osDescription"] = osDesc
	}
	if len(fields) > 0 {
		if updated, uErr := a.updateNodeBlob(rec, fields); uErr == nil {
			rec.NodeJSON = updated
		}
	}

	resp["node"] = json.RawMessage(rec.NodeJSON)
	resp["fromCache"] = false
	resp["cachedAt"] = time.Now().UnixMilli()
	writeJSON(w, http.StatusOK, resp)
}

// updateNodeBlob merges fields into the host's node_json blob and persists it.
func (a *API) updateNodeBlob(rec hostcache.NodeRecord, fields map[string]any) (json.RawMessage, error) {
	blob := map[string]any{}
	if len(rec.NodeJSON) > 0 {
		if err := json.Unmarshal(rec.NodeJSON, &blob); err != nil {
			return rec.NodeJSON, err
		}
	}
	for k, v := range fields {
		blob[k] = v
	}
	raw, err := json.Marshal(blob)
	if err != nil {
		return rec.NodeJSON, err
	}
	rec.NodeJSON = raw
	if err := a.store.UpsertHost(rec); err != nil {
		return raw, err
	}
	return raw, nil
}

// collectLiveTelemetry ports fetchLiveNodeTelemetry: a best-effort multi-tier
// Glances probe folded into the native probe script results.
func (a *API) collectLiveTelemetry(ctx context.Context, nodeID string, info nodeTelemetryInfo) (map[string]any, error) {
	_, runner, err := a.factsAndRunner(ctx, nodeID)
	if err != nil {
		return nil, err
	}

	var metrics *glances.MetricPayload
	if payload, gErr := glances.Collect(ctx, runner, glances.CollectOptions{
		Route: info.glanceRoute(),
		Port:  info.APIPort,
		Name:  info.Name,
	}); gErr == nil {
		metrics = payload
	}

	out, scriptErr := runNodeCommandTimeout(ctx, runner, telemetryScript, 5*time.Second)
	if scriptErr != nil && !strings.Contains(out, "---SPLIT---") {
		return nil, fmt.Errorf("telemetry probe failed for %s: %w", info.Name, scriptErr)
	}

	sections := strings.Split(out, "---SPLIT---")
	section := func(i int) string {
		if i < 0 || i >= len(sections) {
			return ""
		}
		return strings.TrimSpace(sections[i])
	}

	loadParts := strings.Fields(section(0))
	load1 := floatAt(loadParts, 0)
	load5 := floatAt(loadParts, 1)
	load15 := floatAt(loadParts, 2)
	if metrics != nil {
		load1 = metrics.Load.Min1
		load5 = metrics.Load.Min5
		load15 = metrics.Load.Min15
	}

	memParts := strings.Fields(section(1))
	memTotalMb := floatAt(memParts, 0)
	memUsedMb := floatAt(memParts, 1)
	memCacheMb := floatAt(memParts, 2)
	if metrics != nil && metrics.Mem.Total > 0 {
		memTotalMb = math.Round(metrics.Mem.Total / (1024 * 1024))
	}
	if metrics != nil && metrics.Mem.Used > 0 {
		memUsedMb = math.Round(metrics.Mem.Used / (1024 * 1024))
	}

	vCpus, _ := strconv.Atoi(section(2))
	cpuModel := section(3)
	uptimeRaw := strings.TrimSpace(strings.TrimPrefix(section(4), "up"))
	osPretty := section(5)

	hasGlances := section(7) != "" && !strings.Contains(section(7), "NO_GLANCES")
	hasPcp := section(8) != "" && !strings.Contains(section(8), "NO_PCP")
	hasCockpit := section(9) != "" && !strings.Contains(section(9), "NO_COCKPIT")

	arcBytes, arcErr := strconv.ParseInt(section(10), 10, 64)
	hasArc := arcErr == nil && arcBytes > 0
	arcCacheGiB := 0.0
	if hasArc {
		arcCacheGiB = roundTo(float64(arcBytes)/(1024*1024*1024), 1)
	}
	buffCacheGiB := roundTo(memCacheMb/1024, 1)

	poolParts := strings.Fields(section(11))
	zfsTotalTb := 0.0
	zfsUsedTb := 0.0
	zfsRaidType := ""
	zfsScrubStatus := ""
	if len(poolParts) >= 6 && poolParts[0] != "NO_POOL" && !strings.Contains(poolParts[0], "no pools") {
		zfsTotalTb = parseFloatPrefix(poolParts[1])
		zfsUsedTb = parseFloatPrefix(poolParts[2])
		zfsRaidType = fmt.Sprintf("ZFS Pool (%s)", poolParts[0])
		zfsScrubStatus = fmt.Sprintf("ZFS: %s (%s allocated)", poolParts[5], poolParts[4])
	}

	metricsSource := "kernel"
	switch {
	case metrics != nil && (metrics.Source == glances.SourceHTTPRest ||
		metrics.Source == glances.SourceSSHLocalhostCurl ||
		metrics.Source == glances.SourceCLIStdoutJSON):
		metricsSource = "glances"
	case hasGlances:
		metricsSource = "glances"
	case hasPcp || (metrics != nil && metrics.Source == glances.SourcePCPFallback):
		metricsSource = "pcp"
	case hasCockpit:
		metricsSource = "cockpit-bridge"
	}

	cpuUsage := 0.0
	if metrics != nil {
		cpuUsage = metrics.CPU.Total
	} else if vCpus > 0 {
		cpuUsage = math.Max(0.5, math.Min(99.0, roundTo(load1/float64(vCpus)*100, 1)))
	}

	return map[string]any{
		"cpuUsage":       cpuUsage,
		"cpuModel":       cpuModel,
		"vCpus":          vCpus,
		"memoryTotalGiB": roundTo(memTotalMb/1024, 1),
		"memoryUsedGiB":  roundTo(memUsedMb/1024, 1),
		"arcCacheGiB":    arcCacheGiB,
		"buffCacheGiB":   buffCacheGiB,
		"zfsTankTotalTb": zfsTotalTb,
		"zfsTankUsedTb":  zfsUsedTb,
		"zfsRaidType":    zfsRaidType,
		"zfsScrubStatus": zfsScrubStatus,
		"loadAvg1m":      load1,
		"loadAvg5m":      load5,
		"loadAvg15m":     load15,
		"uptime":         uptimeRaw,
		"osDescription":  osPretty,
		"metricsSource":  metricsSource,
	}, nil
}

// runNodeCommandTimeout is runNodeCommand with a caller-chosen budget.
func runNodeCommandTimeout(ctx context.Context, runner plugin.Runner, cmd string, timeout time.Duration) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return runner.RunCommand(cctx, cmd)
}

func floatAt(parts []string, i int) float64 {
	if i < 0 || i >= len(parts) {
		return 0
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(parts[i]), 64)
	if err != nil {
		return 0
	}
	return v
}

// parseFloatPrefix parses a leading number such as "7.27T" from a zpool column.
func parseFloatPrefix(s string) float64 {
	end := 0
	for end < len(s) && ((s[end] >= '0' && s[end] <= '9') || s[end] == '.') {
		end++
	}
	if end == 0 {
		return 0
	}
	v, err := strconv.ParseFloat(s[:end], 64)
	if err != nil {
		return 0
	}
	return v
}

func roundTo(v float64, places int) float64 {
	factor := math.Pow(10, float64(places))
	return math.Round(v*factor) / factor
}
