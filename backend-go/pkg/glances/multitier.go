package glances

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// MetricPayload is the wire shape of the multi-tier telemetry probe. It matches
// `GlancesMetricPayload` in src/utils/telemetryCollector.ts field-for-field so
// GET /api/nodes/:id/telemetry/glances returns the same JSON from either stack.
type MetricPayload struct {
	CPU    MetricCPU  `json:"cpu"`
	Mem    MetricMem  `json:"mem"`
	Load   MetricLoad `json:"load"`
	Source string     `json:"source"`
}

// MetricCPU is the CPU block (`cpu.total` percent).
type MetricCPU struct {
	Total float64 `json:"total"`
}

// MetricMem is the memory block. used/total are bytes, percent is 0-100.
type MetricMem struct {
	Percent float64 `json:"percent"`
	Used    float64 `json:"used"`
	Total   float64 `json:"total"`
}

// MetricLoad is the load-average block.
type MetricLoad struct {
	Min1  float64 `json:"min1"`
	Min5  float64 `json:"min5"`
	Min15 float64 `json:"min15"`
}

// Metric sources, exactly the literals the TS collector emits.
const (
	SourceHTTPRest         = "http_rest"
	SourceSSHLocalhostCurl = "ssh_localhost_curl"
	SourceCLIStdoutJSON    = "cli_stdout_json"
	SourcePCPFallback      = "pcp_fallback"
)

// CommandRunner is the minimal remote-execution surface the tiers need; every
// plugin.Runner satisfies it.
type CommandRunner interface {
	RunCommand(ctx context.Context, cmd string) (string, error)
}

// CollectOptions configures a multi-tier probe.
type CollectOptions struct {
	// Route is the host to query over HTTP (tailscale IP, LAN IP, DNS name).
	Route string
	// Port is the Glances REST port; 0 means 61208.
	Port int
	// Timeout bounds a single tier's command/HTTP attempt; 0 means 2s.
	Timeout time.Duration
	// Name is used in the exhausted-tiers error message (peer name or id).
	Name string
	// HTTPClient overrides the HTTP client used for the direct REST tier.
	HTTPClient *http.Client
}

// Collect runs the same four-tier degradation chain as
// fetchNodeTelemetryMultiTier in src/utils/telemetryCollector.ts:
//
//	Tier 1 — direct Glances REST (http://<route>:<port>/api/3/all)
//	Tier 2 — SSH localhost curl against 127.0.0.1:<port>
//	Tier 3 — `glances --stdout-json now,cpu,mem,load`
//	Tier 4 — /proc/loadavg + /proc/meminfo + /proc/stat
//
// It never fabricates metrics: when every tier fails it returns an error that
// names the node and lists each tier's failure.
func Collect(ctx context.Context, runner CommandRunner, opts CollectOptions) (*MetricPayload, error) {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	port := opts.Port
	if port <= 0 {
		port = 61208
	}
	route := strings.TrimSpace(opts.Route)
	if route == "" {
		route = "127.0.0.1"
	}

	var errs []string

	// Tier 1: direct HTTP REST.
	httpCtx, cancel := context.WithTimeout(ctx, timeout)
	req, err := http.NewRequestWithContext(httpCtx, http.MethodGet, fmt.Sprintf("http://%s:%d/api/3/all", route, port), nil)
	if err != nil {
		errs = append(errs, "Tier 1 HTTP failed: "+err.Error())
	} else {
		client := opts.HTTPClient
		if client == nil {
			client = &http.Client{}
		}
		resp, hErr := client.Do(req)
		if hErr != nil {
			errs = append(errs, "Tier 1 HTTP failed: "+hErr.Error())
		} else {
			if resp.StatusCode == http.StatusOK {
				var payload map[string]any
				decErr := json.NewDecoder(resp.Body).Decode(&payload)
				_ = resp.Body.Close()
				if decErr == nil {
					cancel()
					parsed := ParseRestData(payload)
					return &parsed, nil
				}
				errs = append(errs, "Tier 1 HTTP decode failed: "+decErr.Error())
			} else {
				_ = resp.Body.Close()
				errs = append(errs, fmt.Sprintf("Tier 1 HTTP status %d", resp.StatusCode))
			}
		}
	}
	cancel()

	if runner != nil {
		// Tier 2: SSH localhost curl.
		curlCmd := fmt.Sprintf("curl -s --connect-timeout 2 http://127.0.0.1:%d/api/3/all", port)
		if out, err := runTier(ctx, runner, curlCmd, timeout+time.Second); err != nil {
			errs = append(errs, "Tier 2 SSH localhost curl failed: "+err.Error())
		} else if trimmed := strings.TrimSpace(out); strings.HasPrefix(trimmed, "{") {
			var payload map[string]any
			if decErr := json.Unmarshal([]byte(trimmed), &payload); decErr == nil {
				parsed := ParseRestData(payload)
				parsed.Source = SourceSSHLocalhostCurl
				return &parsed, nil
			}
			errs = append(errs, "Tier 2 SSH curl returned malformed JSON")
		} else {
			errs = append(errs, "Tier 2 SSH curl returned non-JSON output")
		}

		// Tier 3: Glances CLI.
		cliCmd := "glances --stdout-json now,cpu,mem,load 2>/dev/null"
		if out, err := runTier(ctx, runner, cliCmd, timeout+1500*time.Millisecond); err != nil {
			errs = append(errs, "Tier 3 Glances CLI failed: "+err.Error())
		} else if strings.Contains(out, "{") {
			if parsed, pErr := ParseCLIJSON(out); pErr == nil {
				return parsed, nil
			} else {
				errs = append(errs, "Tier 3 Glances CLI failed: "+pErr.Error())
			}
		} else {
			errs = append(errs, "Tier 3 Glances CLI returned non-JSON output")
		}

		// Tier 4: native /proc fallback.
		sysfsCmd := "cat /proc/loadavg 2>/dev/null; cat /proc/meminfo 2>/dev/null; cat /proc/stat 2>/dev/null"
		if out, err := runTier(ctx, runner, sysfsCmd, timeout+time.Second); err != nil {
			errs = append(errs, "Tier 4 sysfs fallback failed: "+err.Error())
		} else if strings.Contains(out, "MemTotal") || strings.Contains(out, "cpu ") {
			parsed := ParseSysfs(out)
			return &parsed, nil
		} else {
			errs = append(errs, "Tier 4 sysfs commands returned insufficient data")
		}
	}

	name := opts.Name
	if name == "" {
		name = route
	}
	// Zero-mock: never return synthetic metrics when every tier is exhausted.
	return nil, fmt.Errorf("failed to collect telemetry for node %s: all probe tiers exhausted (%s)", name, strings.Join(errs, "; "))
}

func runTier(ctx context.Context, runner CommandRunner, cmd string, timeout time.Duration) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return runner.RunCommand(cctx, cmd)
}

// ParseRestData mirrors parseGlancesRestData: it reads cpu.total, mem.* and
// load.* from a Glances /api/3/all payload (or its JSON string form).
func ParseRestData(data any) MetricPayload {
	parsed := map[string]any{}
	switch v := data.(type) {
	case string:
		_ = json.Unmarshal([]byte(v), &parsed)
	case map[string]any:
		parsed = v
	}

	cpu := mapAt(parsed, "cpu")
	mem := mapAt(parsed, "mem")
	load := mapAt(parsed, "load")

	cpuTotal := toFloat(cpu["total"])
	if cpuTotal == 0 {
		cpuTotal = toFloat(parsed["cpu"])
	}

	return MetricPayload{
		CPU: MetricCPU{Total: cpuTotal},
		Mem: MetricMem{
			Percent: toFloat(mem["percent"]),
			Used:    toFloat(mem["used"]),
			Total:   toFloat(mem["total"]),
		},
		Load: MetricLoad{
			Min1:  toFloat(load["min1"]),
			Min5:  toFloat(load["min5"]),
			Min15: toFloat(load["min15"]),
		},
		Source: SourceHTTPRest,
	}
}

// ParseCLIJSON mirrors parseGlancesCliJson: it slices the first {...} block out
// of `glances --stdout-json` output before decoding.
func ParseCLIJSON(stdout string) (*MetricPayload, error) {
	start := strings.Index(stdout, "{")
	end := strings.LastIndex(stdout, "}")
	if start == -1 || end == -1 || end < start {
		return nil, fmt.Errorf("invalid JSON in Glances CLI output: no JSON object found")
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(stdout[start:end+1]), &parsed); err != nil {
		return nil, err
	}
	cpu := mapAt(parsed, "cpu")
	mem := mapAt(parsed, "mem")
	load := mapAt(parsed, "load")
	payload := &MetricPayload{
		CPU: MetricCPU{Total: toFloat(cpu["total"])},
		Mem: MetricMem{
			Percent: toFloat(mem["percent"]),
			Used:    toFloat(mem["used"]),
			Total:   toFloat(mem["total"]),
		},
		Load: MetricLoad{
			Min1:  toFloat(load["min1"]),
			Min5:  toFloat(load["min5"]),
			Min15: toFloat(load["min15"]),
		},
		Source: SourceCLIStdoutJSON,
	}
	return payload, nil
}

var (
	loadAvgRe = regexp.MustCompile(`(?m)(?:^|\n)\s*(\d+\.\d+)\s+(\d+\.\d+)\s+(\d+\.\d+)`)
	memInfoRe = regexp.MustCompile(`(MemTotal|MemFree|MemAvailable|Buffers|Cached):\s*(\d+)\s*kB`)
	cpuLineRe = regexp.MustCompile(`(?m)(?:^|\n)cpu\s+(\d+)\s+(\d+)\s+(\d+)\s+(\d+)\s+(\d+)\s+(\d+)\s+(\d+)(?:\s+(\d+))?`)
)

// ParseSysfs mirrors parseSysfsTelemetry: it derives CPU% and memory from the
// combined /proc/loadavg + /proc/meminfo + /proc/stat output. No values are
// invented — anything absent stays zero.
func ParseSysfs(rawOutput string) MetricPayload {
	payload := MetricPayload{Source: SourcePCPFallback}

	if m := loadAvgRe.FindStringSubmatch(rawOutput); m != nil {
		payload.Load.Min1 = parseFloat(m[1])
		payload.Load.Min5 = parseFloat(m[2])
		payload.Load.Min15 = parseFloat(m[3])
	}

	mem := map[string]float64{}
	for _, m := range memInfoRe.FindAllStringSubmatch(rawOutput, -1) {
		mem[m[1]] = parseFloat(m[2])
	}
	memTotalKb := mem["MemTotal"]
	availKb, hasAvail := mem["MemAvailable"]
	if !hasAvail && memTotalKb > 0 {
		availKb = mem["MemFree"] + mem["Buffers"] + mem["Cached"]
	}
	usedKb := math.Max(0, memTotalKb-availKb)
	if memTotalKb > 0 {
		payload.Mem.Percent = round1(usedKb / memTotalKb * 100)
	}
	payload.Mem.Used = usedKb * 1024
	payload.Mem.Total = memTotalKb * 1024

	if m := cpuLineRe.FindStringSubmatch(rawOutput); m != nil {
		var ticks [8]float64
		for i := 0; i < 8; i++ {
			ticks[i] = parseFloat(m[i+1])
		}
		total := ticks[0] + ticks[1] + ticks[2] + ticks[3] + ticks[4] + ticks[5] + ticks[6] + ticks[7]
		idle := ticks[3] + ticks[4]
		if total > 0 {
			payload.CPU.Total = round1((total - idle) / total * 100)
		}
	}

	return payload
}

func mapAt(m map[string]any, key string) map[string]any {
	if v, ok := m[key].(map[string]any); ok {
		return v
	}
	return map[string]any{}
}

func toFloat(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case float32:
		return float64(n)
	case int:
		return float64(n)
	case int64:
		return float64(n)
	case json.Number:
		f, _ := n.Float64()
		return f
	case string:
		f, _ := strconv.ParseFloat(strings.TrimSpace(n), 64)
		return f
	default:
		return 0
	}
}

func parseFloat(s string) float64 {
	f, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return f
}

func round1(v float64) float64 {
	return math.Round(v*10) / 10
}
