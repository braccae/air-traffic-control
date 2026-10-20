package nodeapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"air-traffic-control/backend/pkg/hostcache"
)

type stubRunner struct {
	runFn func(ctx context.Context, node *Object, command string, timeout time.Duration) (string, error)
}

func (s *stubRunner) RunNodeCommand(ctx context.Context, node *Object, command string, timeout time.Duration) (string, error) {
	if s.runFn != nil {
		return s.runFn(ctx, node, command, timeout)
	}
	return "", errors.New("stubRunner: not implemented")
}

func setupTestServer(t *testing.T, runner CommandRunner) (*httptest.Server, *hostcache.Store, *API, *[]map[string]any) {
	dbPath := filepath.Join(t.TempDir(), "hosts-cache.sqlite")
	store, err := hostcache.Open(dbPath)
	if err != nil {
		t.Fatalf("hostcache.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	events := &[]map[string]any{}
	broadcast := func(ev map[string]any) {
		*events = append(*events, ev)
	}

	api, err := New(Options{
		Store:     store,
		Runner:    runner,
		Broadcast: broadcast,
		Now: func() time.Time {
			return time.UnixMilli(1700000000000)
		},
	})
	if err != nil {
		t.Fatalf("New API: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/nodes", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			api.HandleCreate(w, r)
			return
		}
		api.notFound(w, r)
	})
	api.Register(mux)

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	return server, store, api, events
}

func TestAPICreate(t *testing.T) {
	server, store, _, events := setupTestServer(t, nil)

	payload := `{
		"id": "node-custom-01",
		"name": "Custom Server Host",
		"port": "not-numeric",
		"hasGlances": false,
		"specs": {
			"customCpu": "Custom Silicon 1",
			"vcpus": 12
		},
		"folder": "Core Infrastructure",
		"unknownField": "should-not-persist"
	}`

	resp, err := http.Post(server.URL+"/api/nodes", "application/json", strings.NewReader(payload))
	if err != nil {
		t.Fatalf("POST /api/nodes failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}

	respObj, err := ParseObject(bodyBytes)
	if err != nil {
		t.Fatalf("parse response json: %v", err)
	}

	successRaw, ok := respObj.Get("success")
	if !ok || string(successRaw) != "true" {
		t.Errorf("expected success: true, got %s", string(successRaw))
	}

	nodeRaw, ok := respObj.Get("node")
	if !ok {
		t.Fatalf("expected node object in response")
	}

	nodeObj, err := ParseObject(nodeRaw)
	if err != nil {
		t.Fatalf("parse node json: %v", err)
	}

	// 1. Verify defaults and coerced values
	if s, _ := nodeObj.GetString("id"); s != "node-custom-01" {
		t.Errorf("expected id node-custom-01, got %q", s)
	}
	if s, _ := nodeObj.GetString("name"); s != "Custom Server Host" {
		t.Errorf("expected name Custom Server Host, got %q", s)
	}
	if s, _ := nodeObj.GetString("shortName"); s != "Custom" {
		t.Errorf("expected shortName Custom, got %q", s)
	}
	if s, _ := nodeObj.GetString("group"); s != "tag:host" {
		t.Errorf("expected default group tag:host, got %q", s)
	}
	if s, _ := nodeObj.GetString("datacenter"); s != "leo-banjo.ts.net" {
		t.Errorf("expected datacenter leo-banjo.ts.net, got %q", s)
	}
	if s, _ := nodeObj.GetString("nodeType"); s != "linux" {
		t.Errorf("expected nodeType linux, got %q", s)
	}
	if s, _ := nodeObj.GetString("ip"); s != "192.168.8.200" {
		t.Errorf("expected default ip 192.168.8.200, got %q", s)
	}
	if s, _ := nodeObj.GetString("sshUser"); s != "core" {
		t.Errorf("expected default sshUser core, got %q", s)
	}
	if s, _ := nodeObj.GetString("authMethod"); s != "ssh-ed25519 (ssh-agent)" {
		t.Errorf("expected default authMethod, got %q", s)
	}
	if n := parsePositiveInt(nodeObj.vals["port"]); n != 22 {
		t.Errorf("expected port coerced to 22, got %d", n)
	}

	// 2. Explicit false on hasGlances preserved; other flags default to true
	if string(nodeObj.vals["hasGlances"]) != "false" {
		t.Errorf("expected explicit false on hasGlances, got %s", string(nodeObj.vals["hasGlances"]))
	}
	if string(nodeObj.vals["hasCockpit"]) != "true" || string(nodeObj.vals["hasPodman"]) != "true" || string(nodeObj.vals["hasRestic"]) != "true" {
		t.Errorf("expected true defaults on cockpit/podman/restic")
	}

	// 3. Body specs honoured verbatim
	specsRaw, _ := nodeObj.Get("specs")
	specsObj, err := ParseObject(specsRaw)
	if err != nil {
		t.Fatalf("parse specs: %v", err)
	}
	if s, _ := specsObj.GetString("customCpu"); s != "Custom Silicon 1" {
		t.Errorf("expected customCpu 'Custom Silicon 1', got %q", s)
	}
	if n := parsePositiveInt(specsObj.vals["vcpus"]); n != 12 {
		t.Errorf("expected vcpus 12, got %d", n)
	}

	// 4. Unknown body key NOT persisted
	if nodeObj.Has("unknownField") {
		t.Errorf("unknownField should not be persisted on node")
	}

	// 5. Response key order matches Express order
	expectedOrder := []string{
		"id", "name", "shortName", "group", "datacenter", "nodeType", "badge", "badgeColor", "ip", "port",
		"sshUser", "authMethod", "tags", "sshConnected", "rttMs", "tailscaleIp", "tailscaleHost",
		"osDescription", "status", "uptime", "hasGlances", "hasCockpit", "hasPodman", "hasRestic",
		"specs", "folder",
	}
	if !reflect.DeepEqual(nodeObj.Keys(), expectedOrder) {
		t.Errorf("expected key order %v, got %v", expectedOrder, nodeObj.Keys())
	}

	// 6. Verify row in SQLite store
	rec, found, err := store.GetHostByID("node-custom-01")
	if err != nil || !found {
		t.Fatalf("expected host persisted in store, found: %v, err: %v", found, err)
	}
	if rec.Name != "Custom Server Host" || rec.Port != 22 {
		t.Errorf("store record mismatch: %+v", rec)
	}

	// 7. Verify WebSocket broadcast
	if len(*events) != 1 {
		t.Fatalf("expected 1 websocket event, got %d", len(*events))
	}
	if (*events)[0]["type"] != "node_added" {
		t.Errorf("expected event type node_added, got %v", (*events)[0]["type"])
	}
}

func TestAPIPutUpdate(t *testing.T) {
	var runnerCmd string
	runner := &stubRunner{
		runFn: func(ctx context.Context, node *Object, command string, timeout time.Duration) (string, error) {
			runnerCmd = command
			if strings.Contains(command, "uptime") {
				return "dev-node\nup 4 days, 2 hours\n", nil
			}
			return "", nil
		},
	}

	server, store, _, events := setupTestServer(t, runner)

	// Seed an existing host
	initialNode := `{
		"id": "node-target-01",
		"name": "Target Initial",
		"shortName": "Target",
		"group": "tag:worker",
		"ip": "192.168.1.50",
		"port": 22,
		"specs": {
			"cpu": "Intel Xeon",
			"vcpus": 4,
			"ramGiB": 16
		},
		"status": "offline",
		"sshConnected": false
	}`
	parsedInitial, _ := ParseObject([]byte(initialNode))
	rec, _ := nodeRecordFromObject(parsedInitial)
	if err := store.UpsertHost(rec); err != nil {
		t.Fatalf("seed host failed: %v", err)
	}

	t.Run("404 on unknown node ID", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodPut, server.URL+"/api/nodes/non-existent-id", strings.NewReader(`{"name":"New"}`))
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("PUT failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("expected 404, got %d", resp.StatusCode)
		}

		body, _ := io.ReadAll(resp.Body)
		var m map[string]any
		_ = json.Unmarshal(body, &m)
		if m["error"] != "Node not found" {
			t.Errorf("expected error 'Node not found', got %v", m["error"])
		}
		if _, hasSuccess := m["success"]; hasSuccess {
			t.Errorf("PUT 404 response must not have success key")
		}
	})

	t.Run("partial update with empty name keeps name and shallow-merges specs and runs testSsh", func(t *testing.T) {
		updatePayload := `{
			"name": "",
			"specs": {
				"vcpus": 32,
				"customTag": "accelerated"
			},
			"testSsh": true,
			"extraField": "carriedOver"
		}`

		req, _ := http.NewRequest(http.MethodPut, server.URL+"/api/nodes/node-target-01", strings.NewReader(updatePayload))
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("PUT failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}

		bodyBytes, _ := io.ReadAll(resp.Body)
		respObj, _ := ParseObject(bodyBytes)
		nodeRaw, _ := respObj.Get("node")
		nodeObj, _ := ParseObject(nodeRaw)

		// Name kept untouched because update.name was empty
		if s, _ := nodeObj.GetString("name"); s != "Target Initial" {
			t.Errorf("expected name 'Target Initial', got %q", s)
		}
		if s, _ := nodeObj.GetString("shortName"); s != "Target" {
			t.Errorf("expected shortName 'Target', got %q", s)
		}

		// Specs shallow merged: cpu and ramGiB kept from initial, vcpus updated, customTag added
		specsRaw, _ := nodeObj.Get("specs")
		specsObj, _ := ParseObject(specsRaw)
		if s, _ := specsObj.GetString("cpu"); s != "Intel Xeon" {
			t.Errorf("expected cpu 'Intel Xeon', got %q", s)
		}
		if n := parsePositiveInt(specsObj.vals["vcpus"]); n != 32 {
			t.Errorf("expected vcpus 32, got %d", n)
		}
		if n := parsePositiveInt(specsObj.vals["ramGiB"]); n != 16 {
			t.Errorf("expected ramGiB 16, got %d", n)
		}
		if s, _ := specsObj.GetString("customTag"); s != "accelerated" {
			t.Errorf("expected customTag 'accelerated', got %q", s)
		}

		// testSsh ran and updated sshConnected, status, and uptime from second line
		if string(nodeObj.vals["sshConnected"]) != "true" {
			t.Errorf("expected sshConnected true")
		}
		if s, _ := nodeObj.GetString("status"); s != "online" {
			t.Errorf("expected status online, got %q", s)
		}
		if s, _ := nodeObj.GetString("uptime"); s != "4 days, 2 hours" {
			t.Errorf("expected uptime '4 days, 2 hours', got %q", s)
		}

		// Unknown/extra fields spread into object
		if s, _ := nodeObj.GetString("extraField"); s != "carriedOver" {
			t.Errorf("expected extraField 'carriedOver', got %q", s)
		}

		// Broadcast emitted
		if len(*events) == 0 || (*events)[len(*events)-1]["type"] != "node_updated" {
			t.Errorf("expected node_updated event broadcast")
		}
	})

	t.Run("testSsh failure sets sshConnected false only", func(t *testing.T) {
		runner.runFn = func(ctx context.Context, node *Object, command string, timeout time.Duration) (string, error) {
			return "", errors.New("ssh connection refused")
		}

		req, _ := http.NewRequest(http.MethodPut, server.URL+"/api/nodes/node-target-01", strings.NewReader(`{"testSsh":true}`))
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("PUT failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200 on testSsh failure, got %d", resp.StatusCode)
		}

		bodyBytes, _ := io.ReadAll(resp.Body)
		respObj, _ := ParseObject(bodyBytes)
		nodeRaw, _ := respObj.Get("node")
		nodeObj, _ := ParseObject(nodeRaw)

		if string(nodeObj.vals["sshConnected"]) != "false" {
			t.Errorf("expected sshConnected false on failed probe")
		}
		if s, _ := nodeObj.GetString("status"); s != "online" {
			t.Errorf("status must remain untouched on probe failure, got %q", s)
		}
	})
	_ = runnerCmd
}

func TestAPIDelete(t *testing.T) {
	server, store, _, events := setupTestServer(t, nil)

	initialNode := `{
		"id": "node-del-01",
		"name": "To Be Deleted",
		"shortName": "DeleteMe",
		"ip": "192.168.1.99"
	}`
	parsed, _ := ParseObject([]byte(initialNode))
	rec, _ := nodeRecordFromObject(parsed)
	if err := store.UpsertHost(rec); err != nil {
		t.Fatalf("seed host failed: %v", err)
	}

	// Insert rows in dependent cache tables
	if err := store.UpsertHostTelemetry("node-del-01", json.RawMessage(`{"load": 0.5}`)); err != nil {
		t.Fatalf("upsert telemetry failed: %v", err)
	}
	if err := store.UpsertHostContainers("node-del-01", hostcache.ContainersUpdate{ProbedUsersJSON: json.RawMessage(`[{"id":"c1"}]`)}); err != nil {
		t.Fatalf("upsert containers failed: %v", err)
	}
	if err := store.UpsertHostSection("node-del-01", "disk", json.RawMessage(`{"free": 100}`)); err != nil {
		t.Fatalf("upsert section failed: %v", err)
	}

	// Verify rows exist before delete
	if _, found, _ := store.GetHostTelemetry("node-del-01"); !found {
		t.Fatalf("expected telemetry row present")
	}
	if _, found, _ := store.GetHostContainers("node-del-01"); !found {
		t.Fatalf("expected containers row present")
	}
	if _, found, _ := store.GetHostSection("node-del-01", "disk"); !found {
		t.Fatalf("expected section row present")
	}

	// 1. DELETE returns 200 and removed node object
	req, _ := http.NewRequest(http.MethodDelete, server.URL+"/api/nodes/node-del-01", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	bodyBytes, _ := io.ReadAll(resp.Body)
	respObj, _ := ParseObject(bodyBytes)
	if string(respObj.vals["success"]) != "true" {
		t.Errorf("expected success: true")
	}
	removedRaw, ok := respObj.Get("removed")
	if !ok {
		t.Fatalf("missing removed object in response")
	}
	removedObj, _ := ParseObject(removedRaw)
	if s, _ := removedObj.GetString("id"); s != "node-del-01" {
		t.Errorf("expected removed.id node-del-01, got %q", s)
	}

	// 2. Assert hosts row and all 3 cache tables are cascaded
	if _, found, _ := store.GetHostByID("node-del-01"); found {
		t.Errorf("expected host row deleted")
	}
	if _, found, _ := store.GetHostTelemetry("node-del-01"); found {
		t.Errorf("expected telemetry row deleted in cascade")
	}
	if _, found, _ := store.GetHostContainers("node-del-01"); found {
		t.Errorf("expected containers row deleted in cascade")
	}
	if _, found, _ := store.GetHostSection("node-del-01", "disk"); found {
		t.Errorf("expected section row deleted in cascade")
	}

	// 3. Broadcast emitted
	if len(*events) == 0 || (*events)[len(*events)-1]["type"] != "node_deleted" {
		t.Errorf("expected node_deleted event broadcast")
	}

	// 4. Second DELETE returns 404
	req2, _ := http.NewRequest(http.MethodDelete, server.URL+"/api/nodes/node-del-01", nil)
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatalf("second DELETE failed: %v", err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 for second delete, got %d", resp2.StatusCode)
	}
	b2, _ := io.ReadAll(resp2.Body)
	var m2 map[string]any
	_ = json.Unmarshal(b2, &m2)
	if m2["error"] != "Node not found" {
		t.Errorf("expected 'Node not found', got %v", m2["error"])
	}
	if _, hasSuccess := m2["success"]; hasSuccess {
		t.Errorf("DELETE 404 response must not have success key")
	}
}

func TestAPIProbeHostUnbound(t *testing.T) {
	cannedOutput := `
===ARCH===
x86_64
===OS_RELEASE===
PRETTY_NAME="AlmaLinux 10.2"
===CPUINFO===
processor	: 0
model name	: AMD Ryzen 9 7950X
===MEMINFO===
MemTotal:       32724484 kB
===DMI===
ASUSTeK
---
PRIME X670
---
---
AMI
---
0805
---
2023-01-01
===EFI===
efi
===IOMMU===
iommu_group
`

	var capturedTimeout time.Duration
	runner := &stubRunner{
		runFn: func(ctx context.Context, node *Object, command string, timeout time.Duration) (string, error) {
			capturedTimeout = timeout
			return cannedOutput, nil
		},
	}

	server, store, _, _ := setupTestServer(t, runner)

	t.Run("400 when host is empty", func(t *testing.T) {
		resp, err := http.Post(server.URL+"/api/nodes/probe-host", "application/json", strings.NewReader(`{}`))
		if err != nil {
			t.Fatalf("POST failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", resp.StatusCode)
		}
		b, _ := io.ReadAll(resp.Body)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		if m["error"] != "host is required" || m["success"] != false {
			t.Errorf("unexpected 400 response: %s", string(b))
		}
	})

	t.Run("200 on successful probe with parsed hardware and rawOutput, no DB row written", func(t *testing.T) {
		resp, err := http.Post(server.URL+"/api/nodes/probe-host", "application/json", strings.NewReader(`{"host":"192.168.1.120"}`))
		if err != nil {
			t.Fatalf("POST failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
		if capturedTimeout != 8*time.Second {
			t.Errorf("expected 8s timeout, got %v", capturedTimeout)
		}

		bodyBytes, _ := io.ReadAll(resp.Body)
		var respData probeHostResponse
		if err := json.Unmarshal(bodyBytes, &respData); err != nil {
			t.Fatalf("decode probeHostResponse: %v", err)
		}

		if !respData.Success {
			t.Errorf("expected success: true")
		}
		if respData.Hardware.CPU != "AMD Ryzen 9 7950X" {
			t.Errorf("expected cpu 'AMD Ryzen 9 7950X', got %q", respData.Hardware.CPU)
		}
		if respData.Hardware.RamGiB != 31 {
			t.Errorf("expected ramGiB 31, got %d", respData.Hardware.RamGiB)
		}
		if respData.RawOutput != cannedOutput {
			t.Errorf("rawOutput mismatch")
		}

		// Assert no row written to store
		hosts, _ := store.GetAllHosts()
		if len(hosts) != 0 {
			t.Errorf("unbound probe must not write to SQLite store, found %d hosts", len(hosts))
		}
	})

	t.Run("502 on runner error", func(t *testing.T) {
		runner.runFn = func(ctx context.Context, node *Object, command string, timeout time.Duration) (string, error) {
			return "", errors.New("SSH connection to 192.168.1.120:22 refused")
		}

		resp, err := http.Post(server.URL+"/api/nodes/probe-host", "application/json", strings.NewReader(`{"host":"192.168.1.120"}`))
		if err != nil {
			t.Fatalf("POST failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusBadGateway {
			t.Errorf("expected 502, got %d", resp.StatusCode)
		}

		b, _ := io.ReadAll(resp.Body)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		if m["success"] != false || !strings.Contains(m["error"].(string), "refused") {
			t.Errorf("unexpected error payload: %s", string(b))
		}
	})
}

func TestAPIProbeHostBound(t *testing.T) {
	cannedOutput := `
===ARCH===
x86_64
===OS_RELEASE===
PRETTY_NAME="Proxmox VE 8.2"
===CPUINFO===
processor	: 0
model name	: AMD EPYC 7002
processor	: 1
model name	: AMD EPYC 7002
===MEMINFO===
MemTotal:       65792000 kB
===DMI===
Supermicro
---
H11SSL-i
---
---
Supermicro
---
2.5b
---
2023-11-01
===EFI===
efi
===IOMMU===
iommu_group
`

	runner := &stubRunner{
		runFn: func(ctx context.Context, node *Object, command string, timeout time.Duration) (string, error) {
			return cannedOutput, nil
		},
	}

	server, store, _, events := setupTestServer(t, runner)

	t.Run("404 on unknown node ID", func(t *testing.T) {
		resp, err := http.Post(server.URL+"/api/nodes/non-existent-id/probe-host", "application/json", nil)
		if err != nil {
			t.Fatalf("POST failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("expected 404, got %d", resp.StatusCode)
		}

		b, _ := io.ReadAll(resp.Body)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		if m["success"] != false || m["error"] != "Node non-existent-id not found" {
			t.Errorf("unexpected 404 error payload: %s", string(b))
		}
	})

	t.Run("200 on bound probe updates node and store and broadcasts node_updated", func(t *testing.T) {
		// Seed host
		initialNode := `{
			"id": "node-pve-02",
			"name": "Proxmox Two",
			"osDescription": "Debian 12",
			"specs": {
				"cpu": "Generic",
				"vcpus": 1,
				"ramGiB": 2,
				"ipmi": "192.168.1.200",
				"ecc": "Registered ECC"
			}
		}`
		parsed, _ := ParseObject([]byte(initialNode))
		rec, _ := nodeRecordFromObject(parsed)
		if err := store.UpsertHost(rec); err != nil {
			t.Fatalf("seed host failed: %v", err)
		}

		resp, err := http.Post(server.URL+"/api/nodes/node-pve-02/probe-host", "application/json", nil)
		if err != nil {
			t.Fatalf("POST failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}

		bodyBytes, _ := io.ReadAll(resp.Body)
		respObj, err := ParseObject(bodyBytes)
		if err != nil {
			t.Fatalf("parse response json: %v", err)
		}

		if string(respObj.vals["success"]) != "true" {
			t.Errorf("expected success: true")
		}

		nodeRaw, ok := respObj.Get("node")
		if !ok {
			t.Fatalf("missing node in response")
		}
		nodeObj, _ := ParseObject(nodeRaw)

		// Assert updated specs
		if s, _ := nodeObj.GetString("osDescription"); s != "Proxmox VE 8.2" {
			t.Errorf("expected osDescription 'Proxmox VE 8.2', got %q", s)
		}
		specsRaw, _ := nodeObj.Get("specs")
		specsObj, _ := ParseObject(specsRaw)
		if s, _ := specsObj.GetString("cpu"); s != "AMD EPYC 7002" {
			t.Errorf("expected cpu 'AMD EPYC 7002', got %q", s)
		}
		if n := parsePositiveInt(specsObj.vals["vcpus"]); n != 2 {
			t.Errorf("expected vcpus 2, got %d", n)
		}
		if n := parsePositiveInt(specsObj.vals["ramGiB"]); n != 63 {
			t.Errorf("expected ramGiB 63, got %d", n)
		}
		if s, _ := specsObj.GetString("motherboard"); s != "Supermicro H11SSL-i" {
			t.Errorf("expected motherboard 'Supermicro H11SSL-i', got %q", s)
		}
		if s, _ := specsObj.GetString("ipmi"); s != "192.168.1.200" {
			t.Errorf("ipmi should be preserved: %q", s)
		}
		if s, _ := specsObj.GetString("ecc"); s != "Registered ECC" {
			t.Errorf("ecc should be preserved: %q", s)
		}

		// Verify stored in DB
		storedRec, found, _ := store.GetHostByID("node-pve-02")
		if !found {
			t.Fatalf("host not found in store")
		}
		storedNode, _ := ParseObject(storedRec.NodeJSON)
		storedSpecsRaw, _ := storedNode.Get("specs")
		storedSpecs, _ := ParseObject(storedSpecsRaw)
		if s, _ := storedSpecs.GetString("cpu"); s != "AMD EPYC 7002" {
			t.Errorf("expected stored cpu 'AMD EPYC 7002', got %q", s)
		}

		// Verify broadcast
		if len(*events) == 0 || (*events)[len(*events)-1]["type"] != "node_updated" {
			t.Errorf("expected node_updated event broadcast")
		}
	})

	t.Run("502 on runner error", func(t *testing.T) {
		runner.runFn = func(ctx context.Context, node *Object, command string, timeout time.Duration) (string, error) {
			return "", errors.New("timeout connecting to node")
		}

		resp, err := http.Post(server.URL+"/api/nodes/node-pve-02/probe-host", "application/json", nil)
		if err != nil {
			t.Fatalf("POST failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusBadGateway {
			t.Errorf("expected 502, got %d", resp.StatusCode)
		}
		b, _ := io.ReadAll(resp.Body)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		if m["success"] != false || !strings.Contains(m["error"].(string), "timeout") {
			t.Errorf("unexpected 502 payload: %s", string(b))
		}
	})
}
