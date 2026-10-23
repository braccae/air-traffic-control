package readonlyapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"air-traffic-control/backend/pkg/hostcache"
)

// These tests pin the Phase 1b parsers and the cached-response envelopes. Route
// parity against a real host is verified separately over SSH (see the task
// handoff); nothing here fabricates host data — the fixtures are literal
// command/REST output shapes.

func testAPI(t *testing.T) (*API, *hostcache.Store) {
	t.Helper()
	store, err := hostcache.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	blob := json.RawMessage(`{"id":"node-1","name":"n1","shortName":"n1",` +
		`"ip":"100.64.0.10","tailscaleIp":"100.64.0.10","tailscaleHost":"n1.ts.net"}`)
	if err := store.UpsertHost(hostcache.NodeRecord{
		ID: "node-1", Name: "n1", ShortName: "n1", IP: "100.64.0.10",
		TailscaleIP: "100.64.0.10", TailscaleHost: "n1.ts.net", NodeJSON: blob,
	}); err != nil {
		t.Fatalf("seed host: %v", err)
	}
	return &API{store: store}, store
}

// The podman probe starts with `ps -eo ... args`, so its output can contain the
// script text (markers included) before the real echo output. The split must
// ignore those inline occurrences.
func TestSplitMarkerLineIgnoresScriptEcho(t *testing.T) {
	out := `  1000 core     sh -c ps -eo uid,user:32,args --no-headers; echo "===PASSWD==="; cat /etc/passwd
===PASSWD===
root:x:0:0:root:/root:/bin/bash
===SUBUID===
core:655360:65536
===DOCKER===
abc:::web:::nginx:::Up
`

	_, afterPasswd := splitMarkerLine(out, "===PASSWD===")
	passwd, afterSubuid := splitMarkerLine(afterPasswd, "===SUBUID===")
	if passwd != "\nroot:x:0:0:root:/root:/bin/bash\n" {
		t.Fatalf("passwd section = %q", passwd)
	}
	subuid, _ := splitMarkerLine(afterSubuid, "===DOCKER===")
	if subuid != "\ncore:655360:65536\n" {
		t.Fatalf("subuid section = %q", subuid)
	}
}

func TestParseStatsJSON(t *testing.T) {
	// Shape emitted by `podman stats --no-stream --format json`.
	raw := `[{"ID":"0123456789abcdef","Name":"web","cpu_percent":"1.25","mem_usage":"12.5MB / 1GB"}]`
	stats := parseStatsJSON(raw)

	full, ok := stats["0123456789abcdef"]
	if !ok {
		t.Fatal("full id missing from stats map")
	}
	if full.CPU != "1.25" || full.Memory != "12.5MB" {
		t.Fatalf("stat = %+v", full)
	}
	if short := stats["0123456789ab"]; short.CPU != "1.25" {
		t.Fatalf("12-char id alias = %+v", short)
	}
	if named := stats["web"]; named.Memory != "12.5MB" {
		t.Fatalf("name alias = %+v", named)
	}

	if got := parseStatsJSON("not json"); len(got) != 0 {
		t.Fatalf("non-JSON stats must yield no entries, got %v", got)
	}

	// Shape emitted by Libpod socket API (/v4.0.0/libpod/containers/stats?stream=false).
	sockRaw := `{"Error":null,"Stats":[{"ContainerID":"e377a55482d2c038","Name":"systemd-caddy","CPU":0.18,"MemUsage":182583296,"MemPerc":0.56}]}`
	sockStats := parseStatsJSON(sockRaw)
	if s, ok := sockStats["e377a55482d2"]; !ok || s.CPU != "0.18%" || s.Memory != "182.6MB" {
		t.Fatalf("unexpected socket stats parse result: %+v", sockStats)
	}
}

func TestParseRootContainersAndPods(t *testing.T) {
	stats := map[string]statEntry{"aaaaaaaaaaaa": {CPU: "2.00%", Memory: "64 MiB"}}
	raw := `[
		{"Id":"aaaaaaaaaaaabbbb","Names":["web"],"Image":"nginx:latest","Status":"Up 2 hours","Pod":"","PodName":""},
		{"Id":"ccccccccccccdddd","Names":["db"],"Image":"postgres:16","Status":"Up 3 hours","Pod":"111122223333","PodName":"app"},
		{"Id":"eeeeeeeeeeeeffff","Names":["app-infra"],"Image":"localhost/podman-pause:4.9","Status":"Up 3 hours","Pod":"111122223333","PodName":"app"},
		{"Id":"9999999999990000","Names":["old"],"Image":"busybox","Status":"Exited (0) 1 day ago"}
	]`

	containers := parseRootContainers(raw, stats)
	if len(containers) != 3 {
		t.Fatalf("containers = %d, want 3 (infra container filtered)", len(containers))
	}
	if containers[0].ID != "aaaaaaaaaaaa" {
		t.Fatalf("id must be truncated to 12 chars, got %q", containers[0].ID)
	}
	if containers[0].CPU != "2.00%" || containers[0].Memory != "64 MiB" {
		t.Fatalf("stats not applied: %+v", containers[0])
	}
	if containers[0].User != "root" || containers[0].Rootless {
		t.Fatalf("root container flags wrong: %+v", containers[0])
	}

	podsRaw := `[{"Id":"1111222233334444","Name":"app","Status":"Running","PortMappings":"8080:80"}]`
	pods := parseRootPods(podsRaw, "n1.ts.net")
	if len(pods) != 1 || pods[0].Name != "pod: app" || pods[0].Ports != "8080:80" {
		t.Fatalf("pods = %+v", pods)
	}

	standalone, merged := separatePodsAndStandalone(containers, pods)
	if len(standalone) != 2 {
		t.Fatalf("standalone = %d, want 2", len(standalone))
	}
	if len(merged) != 1 {
		t.Fatalf("pods = %d, want 1", len(merged))
	}
	if len(merged[0].Containers) != 1 || merged[0].Containers[0].Name != "db" {
		t.Fatalf("pod membership = %+v", merged[0].Containers)
	}
	if merged[0].ContainersRunningCount != 1 {
		t.Fatalf("running count = %d", merged[0].ContainersRunningCount)
	}
}

func TestCalculatePodTotals(t *testing.T) {
	cpu, ram := calculatePodTotals([]containerItem{
		{CPU: "1.5%", Memory: "512 MiB"},
		{CPU: "0.5%", Memory: "1 GiB"},
	})
	if cpu != "2.00%" {
		t.Fatalf("cpu = %q", cpu)
	}
	if ram != "1.50 GiB" {
		t.Fatalf("ram = %q", ram)
	}

	if cpu, ram := calculatePodTotals(nil); cpu != "" || ram != "" {
		t.Fatalf("empty totals = (%q, %q)", cpu, ram)
	}
}

func TestParseMemoryStringToBytes(t *testing.T) {
	cases := map[string]float64{
		"512 MiB":      512 * 1024 * 1024,
		"1.5GiB":       1.5 * 1024 * 1024 * 1024,
		"900 kB":       900 * 1024,
		"12 MB / 1 GB": 12 * 1024 * 1024,
		"":             0,
	}
	for input, want := range cases {
		if got := parseMemoryStringToBytes(input); got != want {
			t.Fatalf("parseMemoryStringToBytes(%q) = %v, want %v", input, got, want)
		}
	}
}

func TestParseDockerPsOutput(t *testing.T) {
	raw := "abc123def456:::web:::nginx:latest:::Up 2 hours\n" +
		"def456abc123:::app-infra:::localhost/podman-pause:4.9:::Up 2 hours\n" +
		"{{.ID}}:::{{.Names}}:::{{.Image}}:::{{.Status}}\n"

	hasDocker, containers := parseDockerPsOutput(raw, "/usr/bin/docker")
	if !hasDocker {
		t.Fatal("hasDocker should be true when the docker CLI is present")
	}
	if len(containers) != 1 || containers[0].Name != "web" {
		t.Fatalf("containers = %+v", containers)
	}
	// Zero-mock: docker ps reports no usage numbers, so these stay empty
	// instead of Express's fabricated 0.2% / 48 MiB.
	if containers[0].CPU != "" || containers[0].Memory != "" {
		t.Fatalf("fabricated docker usage: %+v", containers[0])
	}

	hasDocker, containers = parseDockerPsOutput("NO_DOCKER", "NO_DOCKER")
	if hasDocker || len(containers) != 0 {
		t.Fatalf("empty host reported docker: %v %+v", hasDocker, containers)
	}

	// Docker Engine HTTP API JSON array (/containers/json?all=1) with Compose labels
	socketJSON := `[
		{
			"Id": "11223344556677889900",
			"Names": ["/traefik-reverse-proxy"],
			"Image": "traefik:v3.1",
			"State": "running",
			"Status": "Up 5 hours",
			"Ports": [{"IP": "0.0.0.0", "PrivatePort": 80, "PublicPort": 80, "Type": "tcp"}],
			"Labels": {
				"com.docker.compose.project": "ingress",
				"com.docker.compose.service": "traefik",
				"com.docker.compose.project.config_files": "/srv/ingress/compose.yaml"
			}
		}
	]`
	hasDocker, sockContainers := parseDockerPsOutput(socketJSON, `{"Version":"27.3.1","Platform":{"Name":"Docker Engine"}}`)
	if !hasDocker || len(sockContainers) != 1 {
		t.Fatalf("expected 1 container from Docker socket JSON, got hasDocker=%v containers=%+v", hasDocker, sockContainers)
	}
	if sockContainers[0].ID != "112233445566" || sockContainers[0].Name != "traefik-reverse-proxy" {
		t.Fatalf("unexpected container ID/Name: %+v", sockContainers[0])
	}
	if sockContainers[0].ComposeProject != "ingress" || sockContainers[0].ComposeService != "traefik" || sockContainers[0].ComposeConfigFiles != "/srv/ingress/compose.yaml" {
		t.Fatalf("expected compose labels from socket JSON, got %+v", sockContainers[0])
	}
}

func TestParseIdentities(t *testing.T) {
	passwd := "root:x:0:0:root:/root:/bin/bash\n" +
		"core:x:1000:1000:Core:/home/core:/bin/bash\n" +
		"svc-app:x:2900:2900:Service:/home/svc-app:/usr/sbin/nologin\n" +
		"nobody:x:65534:65534:Nobody:/:/sbin/nologin\n"
	subuid := "core:655360:65536\nsvc-app:720896:65536\n"
	linger := "core\n"

	identities := parseIdentities(passwd, subuid, linger)
	if len(identities) != 2 {
		t.Fatalf("identities = %d, want 2 (uid 900-65533 only): %+v", len(identities), identities)
	}
	if identities[0].User != "core" || identities[1].User != "svc-app" {
		t.Fatalf("identity order = %q, %q", identities[0].User, identities[1].User)
	}
	if identities[0].UIDGid != "1000:1000" {
		t.Fatalf("uidGid = %q", identities[0].UIDGid)
	}
	if identities[0].LingerSubUID != "Linger: Enabled (655360:65536)" {
		t.Fatalf("linger = %q", identities[0].LingerSubUID)
	}
	if identities[1].RolesGroups != "podman, service-identity" {
		t.Fatalf("roles = %q", identities[1].RolesGroups)
	}
}

func TestNodeGlancesUnknownID(t *testing.T) {
	api, _ := testAPI(t)

	req := httptest.NewRequest(http.MethodGet, "/api/nodes/nope/telemetry/glances", nil)
	req.SetPathValue("id", "nope")
	rr := httptest.NewRecorder()
	api.handleNodeGlances(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (Express matches the id strictly)", rr.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["success"] != false || body["error"] != "Node not found" {
		t.Fatalf("body = %v", body)
	}
}

func TestNodeContainersCachedEnvelope(t *testing.T) {
	api, store := testAPI(t)

	if err := store.UpsertHostContainers("node-1", hostcache.ContainersUpdate{
		ProbedUsersJSON:      json.RawMessage(`[{"user":"core","uid":1000,"linger":true,"containers":[]}]`),
		IdentitiesJSON:       json.RawMessage(`[{"user":"core","uidGid":"1000:1000"}]`),
		RootContainersJSON:   json.RawMessage(`[{"id":"aaaa","name":"web"}]`),
		RootPodsJSON:         json.RawMessage(`[{"id":"pppp","name":"pod: app"}]`),
		DockerContainersJSON: json.RawMessage(`[]`),
		HasDocker:            boolPtr(true),
	}); err != nil {
		t.Fatalf("seed containers: %v", err)
	}
	if err := store.AddCustomIdentity("node-1", json.RawMessage(`{"user":"svc-app","uidGid":"2900:2900"}`)); err != nil {
		t.Fatalf("add custom identity: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/nodes/node-1/containers", nil)
	req.SetPathValue("id", "node-1")
	rr := httptest.NewRecorder()
	api.handleNodeContainers(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}

	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	for _, key := range []string{
		"node", "probedUsers", "identities", "rootContainers", "rootPods",
		"hasDocker", "dockerContainers", "fromCache", "cachedAt",
	} {
		if _, ok := body[key]; !ok {
			t.Fatalf("missing key %q in response: %v", key, body)
		}
	}
	if body["fromCache"] != true {
		t.Fatalf("fromCache = %v, want true", body["fromCache"])
	}
	if body["hasDocker"] != true {
		t.Fatalf("hasDocker = %v", body["hasDocker"])
	}

	identities, ok := body["identities"].([]any)
	if !ok || len(identities) != 2 {
		t.Fatalf("identities = %v, want custom first then discovered", body["identities"])
	}
	first, _ := identities[0].(map[string]any)
	if first["user"] != "svc-app" {
		t.Fatalf("custom identity must be prepended, got %v", first["user"])
	}

	node, _ := body["node"].(map[string]any)
	if node["id"] != "node-1" || node["tailscaleHost"] != "n1.ts.net" {
		t.Fatalf("node blob = %v", node)
	}
}

func TestTelemetryCachedEnvelope(t *testing.T) {
	api, store := testAPI(t)

	live := map[string]any{
		"cpuUsage":       12.5,
		"cpuModel":       "AMD EPYC",
		"vCpus":          8,
		"memoryUsedGiB":  4.0,
		"memoryTotalGiB": 16.0,
		"uptime":         "3 days",
	}
	raw, _ := json.Marshal(live)
	if err := store.UpsertHostTelemetry("node-1", raw); err != nil {
		t.Fatalf("seed telemetry: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/telemetry?host=node-1", nil)
	rr := httptest.NewRecorder()
	api.handleTelemetry(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}

	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	for _, key := range []string{
		"cpuUsage", "cpuModel", "vCpus", "memoryUsedGiB", "memoryTotalGiB",
		"arcCacheGiB", "zfsTankUsedTb", "zfsTankTotalTb", "zfsRaidType",
		"zfsScrubStatus", "loadAvg1m", "loadAvg5m", "loadAvg15m",
		"ioDelayPercent", "cpuTempC", "fanSpeedRpm", "packageWatts",
		"sparkline", "metricsSource", "buffCacheGiB",
		"node", "fromCache", "cachedAt",
	} {
		if _, ok := body[key]; !ok {
			t.Fatalf("missing key %q in telemetry response", key)
		}
	}
	if body["fromCache"] != true {
		t.Fatalf("fromCache = %v, want true", body["fromCache"])
	}
	if body["cpuUsage"] != 12.5 {
		t.Fatalf("cached cpuUsage = %v", body["cpuUsage"])
	}
	if _, ok := body["sparkline"].([]any); !ok {
		t.Fatalf("sparkline must be an array, got %T", body["sparkline"])
	}
	node, _ := body["node"].(map[string]any)
	if node["id"] != "node-1" {
		t.Fatalf("node blob = %v", node)
	}
}

func boolPtr(b bool) *bool { return &b }
