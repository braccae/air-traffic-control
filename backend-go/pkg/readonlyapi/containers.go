package readonlyapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"air-traffic-control/backend/pkg/hostcache"
	"air-traffic-control/backend/pkg/plugin"
	pkgpodman "air-traffic-control/backend/pkg/podman"
	"air-traffic-control/backend/pkg/ssh"
	"air-traffic-control/backend/pkg/types"
)

// podmanProbeScript probes both rootful (/run/podman/podman.sock) and rootless
// (/run/user/<uid>/podman/podman.sock) Podman sockets directly using the
// AGENTS.md §3 privilege escalation hierarchy (run0 -> sudo -n -> doas -n -> none),
// falling back to native podman CLI if a socket is unavailable.
const podmanProbeScript = "ps -eo uid,user:32,args --no-headers" + "; " +
	"echo \"===PASSWD===\"" + "; " +
	"cat /etc/passwd" + "; " +
	"echo \"===SUBUID===\"" + "; " +
	"cat /etc/subuid 2>/dev/null || true" + "; " +
	"echo \"===LINGER===\"" + "; " +
	"ls -1 /var/lib/systemd/linger 2>/dev/null || true" + "; " +
	"echo \"===RUNUSER===\"" + "; " +
	"(run0 --background=no sh -c 'ls -1d /run/user/*/podman/podman.sock /run/user/*/libpod /run/user/*/podman /run/user/*/containers 2>/dev/null' 2>/dev/null || sudo -n sh -c 'ls -1d /run/user/*/podman/podman.sock /run/user/*/libpod /run/user/*/podman /run/user/*/containers 2>/dev/null' 2>/dev/null || doas -n sh -c 'ls -1d /run/user/*/podman/podman.sock /run/user/*/libpod /run/user/*/podman /run/user/*/containers 2>/dev/null' 2>/dev/null || ls -1d /run/user/*/podman/podman.sock /run/user/*/libpod /run/user/*/podman /run/user/*/containers 2>/dev/null) || true" + "; " +
	"echo \"===ROOTPODMAN===\"" + "; " +
	"(run0 --background=no curl -sf --unix-socket /run/podman/podman.sock \"http://d/v4.0.0/libpod/containers/json?all=true\" 2>/dev/null || sudo -n curl -sf --unix-socket /run/podman/podman.sock \"http://d/v4.0.0/libpod/containers/json?all=true\" 2>/dev/null || doas -n curl -sf --unix-socket /run/podman/podman.sock \"http://d/v4.0.0/libpod/containers/json?all=true\" 2>/dev/null || curl -sf --unix-socket /run/podman/podman.sock \"http://d/v4.0.0/libpod/containers/json?all=true\" 2>/dev/null || run0 --background=no podman ps --all --format json 2>/dev/null || sudo -n podman ps --all --format json 2>/dev/null || doas -n podman ps --all --format json 2>/dev/null || podman ps --all --format json 2>/dev/null) || true" + "; " +
	"echo \"===ROOTPODS===\"" + "; " +
	"(run0 --background=no curl -sf --unix-socket /run/podman/podman.sock \"http://d/v4.0.0/libpod/pods/json\" 2>/dev/null || sudo -n curl -sf --unix-socket /run/podman/podman.sock \"http://d/v4.0.0/libpod/pods/json\" 2>/dev/null || doas -n curl -sf --unix-socket /run/podman/podman.sock \"http://d/v4.0.0/libpod/pods/json\" 2>/dev/null || curl -sf --unix-socket /run/podman/podman.sock \"http://d/v4.0.0/libpod/pods/json\" 2>/dev/null || run0 --background=no podman pod ps --format json 2>/dev/null || sudo -n podman pod ps --format json 2>/dev/null || doas -n podman pod ps --format json 2>/dev/null || podman pod ps --format json 2>/dev/null) || true" + "; " +
	"echo \"===ROOTSTATS===\"" + "; " +
	"(run0 --background=no curl -sf --unix-socket /run/podman/podman.sock \"http://d/v4.0.0/libpod/containers/stats?stream=false\" 2>/dev/null || sudo -n curl -sf --unix-socket /run/podman/podman.sock \"http://d/v4.0.0/libpod/containers/stats?stream=false\" 2>/dev/null || doas -n curl -sf --unix-socket /run/podman/podman.sock \"http://d/v4.0.0/libpod/containers/stats?stream=false\" 2>/dev/null || curl -sf --unix-socket /run/podman/podman.sock \"http://d/v4.0.0/libpod/containers/stats?stream=false\" 2>/dev/null || run0 --background=no podman stats --no-stream --format json 2>/dev/null || sudo -n podman stats --no-stream --format json 2>/dev/null || doas -n podman stats --no-stream --format json 2>/dev/null || podman stats --no-stream --format json 2>/dev/null) || true" + "; " +
	"echo \"===USERPROBES===\"" + "; " +
	"USERS=$( ( [ -d /run/user ] && ls -1 /run/user 2>/dev/null | grep -E \"^[0-9]+$\" | while read u; do id -nu \"$u\" 2>/dev/null; done ; ls -1 /var/lib/systemd/linger 2>/dev/null ) | sort -u )" + "; " +
	"for u in $USERS; do uid=$(id -u \"$u\" 2>/dev/null || true); [ -n \"$uid\" ] && [ \"$uid\" -gt 0 ] 2>/dev/null || continue; sock=\"/run/user/$uid/podman/podman.sock\"; echo \"---USER_PS:$u:$uid---\"; (run0 --background=no curl -sf --unix-socket \"$sock\" \"http://d/v4.0.0/libpod/containers/json?all=true\" 2>/dev/null || sudo -n curl -sf --unix-socket \"$sock\" \"http://d/v4.0.0/libpod/containers/json?all=true\" 2>/dev/null || doas -n curl -sf --unix-socket \"$sock\" \"http://d/v4.0.0/libpod/containers/json?all=true\" 2>/dev/null || curl -sf --unix-socket \"$sock\" \"http://d/v4.0.0/libpod/containers/json?all=true\" 2>/dev/null || run0 --background=no --user \"$u\" podman ps --all --format json 2>/dev/null || sudo -n -u \"$u\" XDG_RUNTIME_DIR=/run/user/$uid podman ps --all --format json 2>/dev/null || doas -n -u \"$u\" XDG_RUNTIME_DIR=/run/user/$uid podman ps --all --format json 2>/dev/null || XDG_RUNTIME_DIR=/run/user/$uid podman ps --all --format json 2>/dev/null) || true; echo \"---USER_PODS:$u:$uid---\"; (run0 --background=no curl -sf --unix-socket \"$sock\" \"http://d/v4.0.0/libpod/pods/json\" 2>/dev/null || sudo -n curl -sf --unix-socket \"$sock\" \"http://d/v4.0.0/libpod/pods/json\" 2>/dev/null || doas -n curl -sf --unix-socket \"$sock\" \"http://d/v4.0.0/libpod/pods/json\" 2>/dev/null || curl -sf --unix-socket \"$sock\" \"http://d/v4.0.0/libpod/pods/json\" 2>/dev/null || run0 --background=no --user \"$u\" podman pod ps --format json 2>/dev/null || sudo -n -u \"$u\" XDG_RUNTIME_DIR=/run/user/$uid podman pod ps --format json 2>/dev/null || doas -n -u \"$u\" XDG_RUNTIME_DIR=/run/user/$uid podman pod ps --format json 2>/dev/null || XDG_RUNTIME_DIR=/run/user/$uid podman pod ps --format json 2>/dev/null) || true; echo \"---USER_STATS:$u:$uid---\"; (run0 --background=no curl -sf --unix-socket \"$sock\" \"http://d/v4.0.0/libpod/containers/stats?stream=false\" 2>/dev/null || sudo -n curl -sf --unix-socket \"$sock\" \"http://d/v4.0.0/libpod/containers/stats?stream=false\" 2>/dev/null || doas -n curl -sf --unix-socket \"$sock\" \"http://d/v4.0.0/libpod/containers/stats?stream=false\" 2>/dev/null || curl -sf --unix-socket \"$sock\" \"http://d/v4.0.0/libpod/containers/stats?stream=false\" 2>/dev/null || run0 --background=no --user \"$u\" podman stats --no-stream --format json 2>/dev/null || sudo -n -u \"$u\" XDG_RUNTIME_DIR=/run/user/$uid podman stats --no-stream --format json 2>/dev/null || doas -n -u \"$u\" XDG_RUNTIME_DIR=/run/user/$uid podman stats --no-stream --format json 2>/dev/null || XDG_RUNTIME_DIR=/run/user/$uid podman stats --no-stream --format json 2>/dev/null) || true; done" + "; " +
	"echo \"===DOCKER_CLI===\"" + "; " +
	"(which docker 2>/dev/null && (docker --version 2>&1; readlink /var/run/docker.sock /run/docker.sock 2>/dev/null || true)) || echo \"NO_DOCKER\"" + "; " +
	"echo \"===DOCKER===\"" + "; " +
	"if which docker >/dev/null 2>&1 && ! (docker --version 2>&1; readlink /var/run/docker.sock /run/docker.sock 2>/dev/null) | grep -qi podman; then (docker ps -a --format \"{{.ID}}:::{{.Names}}:::{{.Image}}:::{{.Status}}:::{{.Label \\\"com.docker.compose.project\\\"}}:::{{.Label \\\"com.docker.compose.service\\\"}}:::{{.Label \\\"com.docker.compose.project.config_files\\\"}}\" 2>/dev/null || run0 --background=no docker ps -a --format \"{{.ID}}:::{{.Names}}:::{{.Image}}:::{{.Status}}:::{{.Label \\\"com.docker.compose.project\\\"}}:::{{.Label \\\"com.docker.compose.service\\\"}}:::{{.Label \\\"com.docker.compose.project.config_files\\\"}}\" 2>/dev/null || sudo -n docker ps -a --format \"{{.ID}}:::{{.Names}}:::{{.Image}}:::{{.Status}}:::{{.Label \\\"com.docker.compose.project\\\"}}:::{{.Label \\\"com.docker.compose.service\\\"}}:::{{.Label \\\"com.docker.compose.project.config_files\\\"}}\" 2>/dev/null || doas -n docker ps -a --format \"{{.ID}}:::{{.Names}}:::{{.Image}}:::{{.Status}}:::{{.Label \\\"com.docker.compose.project\\\"}}:::{{.Label \\\"com.docker.compose.service\\\"}}:::{{.Label \\\"com.docker.compose.project.config_files\\\"}}\" 2>/dev/null) || echo \"NO_DOCKER\"; else echo \"NO_DOCKER\"; fi"

// containerItem mirrors ContainerItem in src/types/index.ts (wire keys and
// types). podId/podName are omitted when empty, like `undefined` in Express.
type containerItem struct {
	ID                 string `json:"id"`
	PodID              string `json:"podId,omitempty"`
	PodName            string `json:"podName,omitempty"`
	Name               string `json:"name"`
	Image              string `json:"image"`
	Status             string `json:"status"`
	CPU                string `json:"cpu"`
	Memory             string `json:"memory"`
	User               string `json:"user"`
	UID                int    `json:"uid"`
	Rootless           bool   `json:"rootless"`
	ComposeProject     string `json:"composeProject,omitempty"`
	ComposeService     string `json:"composeService,omitempty"`
	ComposeConfigFiles string `json:"composeConfigFiles,omitempty"`
}

// podItem mirrors PodItem in src/types/index.ts.
type podItem struct {
	ID                     string          `json:"id"`
	Name                   string          `json:"name"`
	ContainersRunningCount int             `json:"containersRunningCount"`
	Ports                  string          `json:"ports"`
	TotalCPU               string          `json:"totalCpu"`
	TotalRam               string          `json:"totalRam"`
	User                   string          `json:"user"`
	UID                    int             `json:"uid"`
	Rootless               bool            `json:"rootless"`
	Containers             []containerItem `json:"containers"`
}

// serviceUser mirrors ServiceUser in src/types/index.ts.
type serviceUser struct {
	User          string `json:"user"`
	UIDGid        string `json:"uidGid"`
	RolesGroups   string `json:"rolesGroups"`
	AuthMechanism string `json:"authMechanism"`
	LingerSubUID  string `json:"lingerSubUid"`
	Icon          string `json:"icon"`
}

// containerProbe holds the JSON blobs that go into host_containers_cache.
type containerProbe struct {
	ProbedUsers      json.RawMessage
	Identities       json.RawMessage
	RootContainers   json.RawMessage
	RootPods         json.RawMessage
	HasDocker        bool
	DockerContainers json.RawMessage
}

// handleNodeContainers ports GET /api/nodes/:id/containers (server.ts).
func (a *API) handleNodeContainers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	a.serveContainers(w, r, r.PathValue("id"))
}

// handlePodmanContainers serves GET /api/podman/containers?host=…, the
// host-parameter form of the same read-only route. It shares the handler and
// envelope so both spellings return identical JSON for the same host.
func (a *API) handlePodmanContainers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	a.serveContainers(w, r, r.URL.Query().Get("host"))
}

func (a *API) serveContainers(w http.ResponseWriter, r *http.Request, query string) {
	node, err := a.findNode(query)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"success": false, "error": err.Error()})
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

	nodeJSON := json.RawMessage(rec.NodeJSON)

	if r.URL.Query().Get("refresh") != "true" {
		if entry, found, cErr := a.store.GetHostContainers(node.ID); cErr == nil && found &&
			hostcache.Fresh(entry.UpdatedAt, hostcache.ContainerTTL) {
			writeJSON(w, http.StatusOK, containersEnvelope(
				nodeJSON,
				entry.ProbedUsersJSON,
				concatJSONArrays(entry.CustomIdentitiesJSON, entry.IdentitiesJSON),
				entry.RootContainersJSON,
				entry.RootPodsJSON,
				entry.HasDocker,
				entry.DockerContainersJSON,
				true,
				entry.UpdatedAt,
			))
			return
		}
	}

	probe, err := a.probeContainers(r.Context(), rec)
	if err != nil {
		// Zero-mock: an unreachable host is reported honestly instead of
		// returning an empty-but-successful container list.
		writeJSON(w, http.StatusBadGateway, map[string]any{"success": false, "error": err.Error()})
		return
	}

	if err := a.store.UpsertHostContainers(node.ID, hostcache.ContainersUpdate{
		ProbedUsersJSON:      probe.ProbedUsers,
		IdentitiesJSON:       probe.Identities,
		RootContainersJSON:   probe.RootContainers,
		RootPodsJSON:         probe.RootPods,
		DockerContainersJSON: probe.DockerContainers,
		HasDocker:            &probe.HasDocker,
	}); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
		return
	}

	// Re-read so the envelope carries the persisted custom identities, exactly
	// like the Express route's `updatedCache` read-back.
	custom := json.RawMessage(nil)
	if entry, found, cErr := a.store.GetHostContainers(node.ID); cErr == nil && found {
		custom = entry.CustomIdentitiesJSON
	}

	writeJSON(w, http.StatusOK, containersEnvelope(
		nodeJSON,
		probe.ProbedUsers,
		concatJSONArrays(custom, probe.Identities),
		probe.RootContainers,
		probe.RootPods,
		probe.HasDocker,
		probe.DockerContainers,
		false,
		time.Now().UnixMilli(),
	))
}

// containersEnvelope builds the Express response body for
// GET /api/nodes/:id/containers with identical keys, nesting and types.
func containersEnvelope(
	nodeJSON, probedUsers, identities, rootContainers, rootPods json.RawMessage,
	hasDocker bool, dockerContainers json.RawMessage, fromCache bool, cachedAt int64,
) map[string]any {
	return map[string]any{
		"node":             nodeJSON,
		"probedUsers":      orEmptyArray(probedUsers),
		"identities":       orEmptyArray(identities),
		"rootContainers":   orEmptyArray(rootContainers),
		"rootPods":         orEmptyArray(rootPods),
		"hasDocker":        hasDocker,
		"dockerContainers": orEmptyArray(dockerContainers),
		"fromCache":        fromCache,
		"cachedAt":         cachedAt,
	}
}

// orEmptyArray returns the blob when it holds JSON, otherwise an empty array.
func orEmptyArray(raw json.RawMessage) json.RawMessage {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return json.RawMessage("[]")
	}
	return raw
}

// concatJSONArrays concatenates two JSON arrays (used for
// `[...customIdentities, ...identities]`).
func concatJSONArrays(first, second json.RawMessage) json.RawMessage {
	var a, b []json.RawMessage
	_ = json.Unmarshal(orEmptyArray(first), &a)
	_ = json.Unmarshal(orEmptyArray(second), &b)
	combined := append(a, b...)
	if combined == nil {
		combined = []json.RawMessage{}
	}
	raw, err := json.Marshal(combined)
	if err != nil {
		return json.RawMessage("[]")
	}
	return raw
}

// podmanRunnerAdapter adapts plugin.Runner to pkg/podman's CommandRunner.
type podmanRunnerAdapter struct {
	ctx    context.Context
	runner plugin.Runner
}

func (p *podmanRunnerAdapter) RunCommand(_ ssh.Config, command string) (string, error) {
	return p.runner.RunCommand(p.ctx, command)
}

// probeContainers runs the podman/identity probe against the real host.
//
// The rootless-user discovery is NOT re-implemented here: it reuses
// pkg/podman.ProbeUnprivilegedUsers (the single Go implementation, also served
// by GET /api/podman/probe). The remaining blocks of the Express script —
// /etc/passwd identities, root containers/pods, docker — are parsed here.
func (a *API) probeContainers(ctx context.Context, rec hostcache.NodeRecord) (*containerProbe, error) {
	_, runner, err := a.factsAndRunner(ctx, rec.ID)
	if err != nil {
		return nil, err
	}

	probeCtx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()

	probed := []types.UnprivilegedPodmanUser{}
	mgr := pkgpodman.NewManager(&podmanRunnerAdapter{ctx: probeCtx, runner: runner}, ssh.Config{})
	if users, pErr := mgr.ProbeUnprivilegedUsers(); pErr == nil && len(users) > 0 {
		probed = users
	}
	probedJSON, err := json.Marshal(probed)
	if err != nil {
		return nil, err
	}

	out, scriptErr := runNodeCommandTimeout(probeCtx, runner, podmanProbeScript, 15*time.Second)
	if scriptErr != nil && !strings.Contains(out, "===PASSWD===") {
		return nil, fmt.Errorf("podman probe failed for %s: %w", rec.Name, scriptErr)
	}

	// Line-anchored splits: the script's own first command is `ps -eo ... args`,
	// whose output contains the script text (and therefore the markers) when the
	// SSH transport allocates a wide pty. Only the echo output puts a marker on
	// a line of its own, so anchoring keeps the sections intact on every
	// transport.
	_, rest1 := splitMarkerLine(out, "===PASSWD===")
	passwdPart, rest2 := splitMarkerLine(rest1, "===SUBUID===")
	subuidPart, rest3 := splitMarkerLine(rest2, "===LINGER===")
	lingerPart, rest4 := splitMarkerLine(rest3, "===RUNUSER===")
	_, rest5 := splitMarkerLine(rest4, "===ROOTPODMAN===")
	rootPodmanPart, rest6 := splitMarkerLine(rest5, "===ROOTPODS===")
	rootPodsPart, rest7 := splitMarkerLine(rest6, "===ROOTSTATS===")
	rootStatsPart, rest8 := splitMarkerLine(rest7, "===USERPROBES===")
	_, rest9 := splitMarkerLine(rest8, "===DOCKER_CLI===")
	dockerCLIPart, dockerPart := splitMarkerLine(rest9, "===DOCKER===")

	info := parseTelemetryInfo(rec)
	hostLabel := info.TailscaleHost
	if hostLabel == "" {
		hostLabel = "local"
	}

	statsMap := parseStatsJSON(rootStatsPart)
	rootPods := parseRootPods(rootPodsPart, hostLabel)
	rootContainers, mergedPods := separatePodsAndStandalone(parseRootContainers(rootPodmanPart, statsMap), rootPods)

	hasDocker, dockerContainers := parseDockerPsOutput(dockerPart, dockerCLIPart)

	identities := parseIdentities(passwdPart, subuidPart, lingerPart)

	rootContainersJSON, _ := json.Marshal(rootContainers)
	rootPodsJSON, _ := json.Marshal(mergedPods)
	dockerJSON, _ := json.Marshal(dockerContainers)
	identitiesJSON, _ := json.Marshal(identities)

	return &containerProbe{
		ProbedUsers:      probedJSON,
		Identities:       identitiesJSON,
		RootContainers:   rootContainersJSON,
		RootPods:         rootPodsJSON,
		HasDocker:        hasDocker,
		DockerContainers: dockerJSON,
	}, nil
}

// statEntry is the CPU/memory pair resolved from `podman stats` output.
type statEntry struct {
	CPU    string
	Memory string
}

// parseStatsJSON mirrors the parseStatsJson closure in liveCluster.ts and also
// supports the Libpod socket API envelope {"Error":null,"Stats":[...]}.
func parseStatsJSON(raw string) map[string]statEntry {
	stats := map[string]statEntry{}
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return stats
	}

	if strings.HasPrefix(trimmed, "{") {
		if parsedSocket, err := pkgpodman.ParseStatsJSON(trimmed); err == nil && len(parsedSocket) > 0 {
			for k, v := range parsedSocket {
				stats[k] = statEntry{CPU: v.CPUPerc, Memory: v.MemUsage}
			}
			return stats
		}
		return stats
	}

	if !strings.HasPrefix(trimmed, "[") {
		return stats
	}

	var parsed []map[string]any
	if err := json.Unmarshal([]byte(trimmed), &parsed); err != nil {
		return stats
	}

	for _, s := range parsed {
		id := firstString(s, "ID", "Id", "id", "Container")
		name := firstString(s, "Name", "name")
		cpu := firstString(s, "cpu_percent", "CPUPerc", "CPU", "cpu")
		memRaw := firstString(s, "mem_usage", "MemUsage", "Memory", "memory")
		mem := memRaw
		if idx := strings.Index(memRaw, "/"); idx >= 0 {
			mem = strings.TrimSpace(memRaw[:idx])
		}
		entry := statEntry{CPU: cpu, Memory: mem}
		if id != "" {
			stats[id] = entry
			if len(id) > 12 {
				stats[id[:12]] = entry
			}
		}
		if name != "" {
			stats[name] = entry
		}
	}
	return stats
}

func firstString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			switch t := v.(type) {
			case string:
				return strings.TrimSpace(t)
			case float64:
				return strconv.FormatFloat(t, 'f', -1, 64)
			}
		}
	}
	return ""
}

// rawPodmanContainer covers the field aliases podman emits across versions.
type rawPodmanContainer struct {
	ID      string      `json:"Id"`
	IDUpper string      `json:"ID"`
	IDLower string      `json:"id"`
	Names   interface{} `json:"Names"`
	Image   string      `json:"Image"`
	State   string      `json:"State"`
	Status  string      `json:"Status"`
	Pod     string      `json:"Pod"`
	PodName string      `json:"PodName"`
	PodID   string      `json:"PodId"`
	IsInfra bool        `json:"IsInfra"`
}

func (rc rawPodmanContainer) id() string {
	for _, candidate := range []string{rc.ID, rc.IDUpper, rc.IDLower} {
		if candidate != "" {
			return candidate
		}
	}
	return ""
}

func (rc rawPodmanContainer) name() string {
	switch v := rc.Names.(type) {
	case []interface{}:
		if len(v) > 0 {
			if s, ok := v[0].(string); ok {
				return s
			}
		}
	case string:
		return v
	}
	return rc.id()
}

// parseRootContainers mirrors the root-container block in
// probeNodePodmanAndIdentities, including the podman-pause infra filter.
func parseRootContainers(raw string, stats map[string]statEntry) []containerItem {
	containers := []containerItem{}
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || !strings.HasPrefix(trimmed, "[") {
		return containers
	}

	var parsed []rawPodmanContainer
	if err := json.Unmarshal([]byte(trimmed), &parsed); err != nil {
		return containers
	}

	for _, rc := range parsed {
		name := rc.name()
		nameLower := strings.ToLower(name)
		imgLower := strings.ToLower(rc.Image)
		if rc.IsInfra ||
			strings.Contains(nameLower, "pause") ||
			strings.HasSuffix(nameLower, "-infra") ||
			strings.Contains(imgLower, "pause") ||
			strings.Contains(imgLower, "podman-pause") {
			continue
		}

		fullID := rc.id()
		if fullID == "" {
			fullID = name
		}
		shortID := fullID
		if len(shortID) > 12 {
			shortID = shortID[:12]
		}

		status := rc.Status
		if status == "" {
			status = rc.State
		} else if rc.State != "" && !strings.Contains(strings.ToLower(status), strings.ToLower(rc.State)) && !strings.HasPrefix(strings.ToLower(status), "up ") && !strings.HasPrefix(strings.ToLower(status), "exited") {
			status = fmt.Sprintf("%s (%s)", rc.State, status)
		}
		if status == "" {
			status = "Running"
		}

		image := rc.Image
		if image == "" {
			image = name + ":latest"
		}

		podID := rc.Pod
		if podID == "" {
			podID = rc.PodID
		}

		cpu, mem := "", ""
		if stat, ok := stats[fullID]; ok {
			cpu, mem = stat.CPU, stat.Memory
		} else if stat, ok := stats[shortID]; ok {
			cpu, mem = stat.CPU, stat.Memory
		} else if stat, ok := stats[name]; ok {
			cpu, mem = stat.CPU, stat.Memory
		}

		containers = append(containers, containerItem{
			ID:       shortID,
			PodID:    podID,
			PodName:  rc.PodName,
			Name:     name,
			Image:    image,
			Status:   status,
			CPU:      cpu,
			Memory:   mem,
			User:     "root",
			UID:      0,
			Rootless: false,
		})
	}
	return containers
}

// parseRootPods mirrors the parsedRootPods block. Totals and running counts are
// filled in by separatePodsAndStandalone.
func parseRootPods(raw, hostLabel string) []podItem {
	pods := []podItem{}
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || !strings.HasPrefix(trimmed, "[") {
		return pods
	}

	var parsed []map[string]any
	if err := json.Unmarshal([]byte(trimmed), &parsed); err != nil {
		return pods
	}

	for _, rp := range parsed {
		name := firstString(rp, "Name", "Id", "ID", "id")
		if name == "" {
			name = "system-pod"
		}
		id := firstString(rp, "Id", "ID", "id")
		if id == "" {
			id = name
		}
		ports := ""
		if mapping := firstString(rp, "PortMappings", "Ports"); mapping != "" {
			ports = mapping
		} else {
			ports = "Host: " + hostLabel
		}
		if !strings.HasPrefix(name, "pod:") {
			name = "pod: " + name
		}
		pods = append(pods, podItem{
			ID:         id,
			Name:       name,
			Ports:      ports,
			TotalCPU:   "",
			TotalRam:   "",
			User:       "root",
			UID:        0,
			Rootless:   false,
			Containers: []containerItem{},
		})
	}
	return pods
}

// separatePodsAndStandalone ports separatePodsAndStandaloneContainers from
// src/utils/podmanViewModel.ts.
func separatePodsAndStandalone(containers []containerItem, existingPods []podItem) ([]containerItem, []podItem) {
	podMap := map[string]*podItem{}
	order := []string{}

	addKey := func(key string, p *podItem) {
		if key == "" {
			return
		}
		if _, exists := podMap[key]; !exists {
			order = append(order, key)
		}
		podMap[key] = p
	}

	for i := range existingPods {
		p := existingPods[i]
		if p.Containers == nil {
			p.Containers = []containerItem{}
		}
		addKey(p.ID, &p)
		addKey(p.Name, &p)
		cleanName := strings.TrimPrefix(p.Name, "pod: ")
		cleanName = strings.TrimSpace(cleanName)
		addKey(cleanName, &p)
	}

	standalone := []containerItem{}

	for _, c := range containers {
		podIdentifier := c.PodID
		if strings.TrimSpace(podIdentifier) == "" {
			podIdentifier = c.PodName
		}

		if strings.TrimSpace(podIdentifier) == "" {
			standalone = append(standalone, c)
			continue
		}

		pid := strings.TrimSpace(c.PodID)
		pname := strings.TrimSpace(c.PodName)
		key := pid
		if key == "" {
			key = pname
		}

		matched := podMap[key]
		if matched == nil {
			matched = podMap[pid]
		}
		if matched == nil {
			matched = podMap[pname]
		}
		if matched == nil {
			for _, existing := range podMap {
				matchID := pid != "" && (strings.HasPrefix(existing.ID, pid) || strings.HasPrefix(pid, existing.ID))
				matchName := pname != "" && (existing.Name == pname ||
					existing.Name == "pod: "+pname ||
					strings.TrimSpace(strings.TrimPrefix(existing.Name, "pod:")) == strings.TrimSpace(strings.TrimPrefix(pname, "pod:")))
				if matchID || matchName {
					matched = existing
					break
				}
			}
		}

		if matched == nil {
			name := pname
			if name == "" {
				name = pid
			}
			displayName := name
			if !strings.HasPrefix(displayName, "pod:") {
				displayName = "pod: " + displayName
			}
			ports := "Host Mesh"
			user := c.User
			if user == "" {
				user = "root"
			}
			matched = &podItem{
				ID:         key,
				Name:       displayName,
				Ports:      ports,
				TotalCPU:   c.CPU,
				TotalRam:   c.Memory,
				User:       user,
				UID:        c.UID,
				Rootless:   c.Rootless,
				Containers: []containerItem{},
			}
			addKey(pid, matched)
			addKey(pname, matched)
		}

		duplicate := false
		for _, existing := range matched.Containers {
			if existing.ID == c.ID || existing.Name == c.Name {
				duplicate = true
				break
			}
		}
		if !duplicate {
			matched.Containers = append(matched.Containers, c)
			status := strings.ToLower(c.Status)
			if strings.Contains(status, "running") || strings.HasPrefix(status, "up ") {
				matched.ContainersRunningCount++
			}
		}
	}

	// De-duplicate the pointer map (keys alias the same pods).
	seen := map[*podItem]bool{}
	pods := []podItem{}
	for _, key := range order {
		p := podMap[key]
		if p == nil || seen[p] {
			continue
		}
		seen[p] = true
		pods = append(pods, *p)
	}

	for i := range pods {
		totalCPU, totalRam := calculatePodTotals(pods[i].Containers)
		if pods[i].TotalCPU == "" && totalCPU != "" {
			pods[i].TotalCPU = totalCPU
		}
		if pods[i].TotalRam == "" && totalRam != "" {
			pods[i].TotalRam = totalRam
		}
	}

	return standalone, pods
}

// calculatePodTotals ports calculatePodTotals from src/utils/podmanViewModel.ts.
func calculatePodTotals(containers []containerItem) (string, string) {
	cpuSum := 0.0
	hasCPU := false
	memBytes := 0.0
	hasMem := false

	for _, c := range containers {
		trimmedCPU := strings.TrimSpace(c.CPU)
		if trimmedCPU != "" && trimmedCPU != "-" {
			value := strings.TrimSuffix(trimmedCPU, "%")
			if parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64); err == nil {
				cpuSum += parsed
				hasCPU = true
			}
		}
		trimmedMem := strings.TrimSpace(c.Memory)
		if trimmedMem != "" && trimmedMem != "-" {
			if b := parseMemoryStringToBytes(trimmedMem); b > 0 {
				memBytes += b
				hasMem = true
			}
		}
	}

	totalCPU := ""
	if hasCPU {
		totalCPU = fmt.Sprintf("%.2f%%", cpuSum)
	}

	totalRam := ""
	if hasMem {
		switch {
		case memBytes >= 1024*1024*1024:
			totalRam = fmt.Sprintf("%.2f GiB", memBytes/(1024*1024*1024))
		case memBytes >= 1024*1024:
			totalRam = fmt.Sprintf("%.1f MiB", memBytes/(1024*1024))
		case memBytes >= 1024:
			totalRam = fmt.Sprintf("%.0f KiB", memBytes/1024)
		default:
			totalRam = fmt.Sprintf("%.0f B", memBytes)
		}
	}

	return totalCPU, totalRam
}

// parseMemoryStringToBytes ports parseMemoryStringToBytes (binary units).
func parseMemoryStringToBytes(mem string) float64 {
	clean := mem
	if idx := strings.Index(clean, "/"); idx >= 0 {
		clean = strings.TrimSpace(clean[:idx])
	}
	clean = strings.TrimSpace(clean)

	end := 0
	for end < len(clean) && ((clean[end] >= '0' && clean[end] <= '9') || clean[end] == '.') {
		end++
	}
	if end == 0 {
		return 0
	}
	value, err := strconv.ParseFloat(clean[:end], 64)
	if err != nil {
		return 0
	}
	unit := strings.ToLower(strings.TrimSpace(clean[end:]))
	switch {
	case strings.HasPrefix(unit, "g"):
		return value * 1024 * 1024 * 1024
	case strings.HasPrefix(unit, "m"):
		return value * 1024 * 1024
	case strings.HasPrefix(unit, "k"):
		return value * 1024
	default:
		return value
	}
}

// parseDockerPsOutput ports parseDockerPsOutput. Deviation from Express: CPU and
// memory are left empty instead of the fabricated "0.2%"/"48 MiB" placeholders
// (zero-mock policy — docker ps does not report usage).
func parseDockerPsOutput(rawPsOutput, cliCheckOutput string) (bool, []containerItem) {
	containers := []containerItem{}

	if strings.Contains(strings.ToLower(cliCheckOutput), "podman") || strings.Contains(strings.ToLower(rawPsOutput), "emulate docker cli using podman") {
		return false, containers
	}

	cliFound := false
	if cliCheckOutput != "" {
		trimmed := strings.TrimSpace(cliCheckOutput)
		cliFound = trimmed != "" && !strings.Contains(cliCheckOutput, "NO_DOCKER")
	} else {
		trimmed := strings.TrimSpace(rawPsOutput)
		cliFound = trimmed != "" && !strings.Contains(rawPsOutput, "NO_DOCKER")
	}

	for _, rawLine := range strings.Split(rawPsOutput, "\n") {
		line := strings.TrimSpace(rawLine)
		if !strings.Contains(line, ":::") || strings.Contains(line, "{{") {
			continue
		}
		parts := strings.Split(line, ":::")
		if len(parts) < 4 {
			continue
		}
		id, name, image, status := parts[0], parts[1], parts[2], parts[3]
		var composeProject, composeService, composeConfigFiles string
		if len(parts) > 4 {
			composeProject = strings.TrimSpace(parts[4])
		}
		if len(parts) > 5 {
			composeService = strings.TrimSpace(parts[5])
		}
		if len(parts) > 6 {
			composeConfigFiles = strings.TrimSpace(parts[6])
		}
		if id == "" || name == "" {
			continue
		}
		nameLower := strings.ToLower(name)
		imgLower := strings.ToLower(image)
		if strings.Contains(nameLower, "pause") ||
			strings.HasSuffix(nameLower, "-infra") ||
			strings.Contains(imgLower, "pause") ||
			strings.Contains(imgLower, "podman-pause") {
			continue
		}
		shortID := id
		if len(shortID) > 12 {
			shortID = shortID[:12]
		}
		if image == "" {
			image = "docker/image:latest"
		}
		if status == "" {
			status = "Running"
		}
		containers = append(containers, containerItem{
			ID:                 shortID,
			Name:               name,
			Image:              image,
			Status:             status,
			CPU:                "",
			Memory:             "",
			User:               "docker",
			UID:                0,
			Rootless:           false,
			ComposeProject:     composeProject,
			ComposeService:     composeService,
			ComposeConfigFiles: composeConfigFiles,
		})
	}

	return cliFound || len(containers) > 0, containers
}

// parseIdentities mirrors the ServiceUser block in
// probeNodePodmanAndIdentities (uid 900-65533 passwd entries).
func parseIdentities(passwdPart, subuidPart, lingerPart string) []serviceUser {
	type passwdEntry struct {
		uid   int
		gid   int
		home  string
		shell string
	}

	passwd := map[string]passwdEntry{}
	for _, line := range strings.Split(passwdPart, "\n") {
		parts := strings.Split(strings.TrimSpace(line), ":")
		if len(parts) < 7 {
			continue
		}
		uid, uidErr := strconv.Atoi(parts[2])
		gid, _ := strconv.Atoi(parts[3])
		if uidErr != nil || uid < 900 || uid >= 65534 {
			continue
		}
		passwd[parts[0]] = passwdEntry{uid: uid, gid: gid, home: parts[5], shell: parts[6]}
	}

	linger := map[string]bool{}
	for _, line := range strings.Split(lingerPart, "\n") {
		if user := strings.TrimSpace(line); user != "" {
			linger[user] = true
		}
	}

	subuid := map[string]string{}
	for _, line := range strings.Split(subuidPart, "\n") {
		parts := strings.Split(strings.TrimSpace(line), ":")
		if len(parts) >= 3 {
			subuid[parts[0]] = parts[1] + ":" + parts[2]
		}
	}

	users := make([]string, 0, len(passwd))
	for user := range passwd {
		users = append(users, user)
	}
	sort.Strings(users)

	identities := []serviceUser{}
	for _, user := range users {
		pw := passwd[user]
		subRange := subuid[user]
		hasLinger := linger[user]

		roles := "podman, service-identity"
		icon := "deployed_code"
		if user == "core" || user == "pants" {
			roles = "wheel, sudo, systemd-journal"
			icon = "badge"
		}

		lingerSubUID := "Linger: No"
		if hasLinger {
			if subRange != "" {
				lingerSubUID = "Linger: Enabled (" + subRange + ")"
			} else {
				lingerSubUID = "Linger: Enabled (uid)"
			}
		} else if subRange != "" {
			lingerSubUID = "SubUID: " + subRange
		}

		auth := pw.shell
		if subRange != "" {
			auth = "Rootless SubUID " + subRange
		}

		identities = append(identities, serviceUser{
			User:          user,
			UIDGid:        strconv.Itoa(pw.uid) + ":" + strconv.Itoa(pw.gid),
			RolesGroups:   roles,
			AuthMechanism: auth,
			LingerSubUID:  lingerSubUID,
			Icon:          icon,
		})
	}

	return identities
}
