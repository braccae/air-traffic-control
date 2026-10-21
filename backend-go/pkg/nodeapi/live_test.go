package nodeapi

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"air-traffic-control/backend/pkg/ansqueue"
	"air-traffic-control/backend/pkg/hostcache"
	"air-traffic-control/backend/pkg/ssh"
)

// TestLiveHardwareProbe is skipped unless ATC_NODEAPI_LIVE_HOST is set.
// It executes a REAL hardware discovery probe over SSH against the target host
// and asserts parsed findings and real stdout execution.
func TestLiveHardwareProbe(t *testing.T) {
	host := os.Getenv("ATC_NODEAPI_LIVE_HOST")
	if host == "" {
		t.Skip("set ATC_NODEAPI_LIVE_HOST to run the live hardware probe test")
	}
	user := os.Getenv("ATC_NODEAPI_LIVE_USER")
	if user == "" {
		user = "core"
	}
	port := 22
	if raw := os.Getenv("ATC_NODEAPI_LIVE_PORT"); raw != "" {
		p, err := strconv.Atoi(raw)
		if err != nil {
			t.Fatalf("ATC_NODEAPI_LIVE_PORT: %v", err)
		}
		port = p
	}

	sshMgr := ssh.NewClientManager()
	runner := NewSSHRunner(sshMgr.Pool())

	node := NewObject()
	node.SetString("id", "live-probe-node")
	node.SetString("name", host)
	node.SetString("ip", host)
	node.SetJSON("port", port)
	node.SetString("sshUser", user)

	cmd := ProbeCommand()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	rawOutput, err := runner.RunNodeCommand(ctx, node, cmd, 15*time.Second)
	if err != nil {
		t.Fatalf("live RunNodeCommand failed on host %s: %v", host, err)
	}

	if strings.TrimSpace(rawOutput) == "" {
		t.Fatalf("expected non-empty real stdout from live host")
	}
	if !strings.Contains(rawOutput, "===ARCH===") {
		t.Errorf("expected rawOutput to contain ===ARCH=== marker, got %q", rawOutput)
	}

	hardware := ParseHardwareProbeOutput(rawOutput)
	if hardware.CPU == "" {
		t.Errorf("expected non-empty hardware.cpu from live host")
	}
	if hardware.VCPUs < 1 {
		t.Errorf("expected vcpus >= 1, got %d", hardware.VCPUs)
	}
	if hardware.RamGiB < 1 {
		t.Errorf("expected ramGiB >= 1, got %d", hardware.RamGiB)
	}
}

// TestLiveMutationsAndCheckpointHalt runs against a real host when ATC_NODEAPI_LIVE_HOST is set.
// It verifies live runner execution and proves checkpoint halting against real hosts.
func TestLiveMutationsAndCheckpointHalt(t *testing.T) {
	host := os.Getenv("ATC_NODEAPI_LIVE_HOST")
	if host == "" {
		t.Skip("set ATC_NODEAPI_LIVE_HOST to run the live mutations and checkpoint halting test")
	}
	user := os.Getenv("ATC_NODEAPI_LIVE_USER")
	if user == "" {
		user = "core"
	}
	port := 22
	if raw := os.Getenv("ATC_NODEAPI_LIVE_PORT"); raw != "" {
		p, err := strconv.Atoi(raw)
		if err != nil {
			t.Fatalf("ATC_NODEAPI_LIVE_PORT: %v", err)
		}
		port = p
	}

	sshMgr := ssh.NewClientManager()
	runner := NewSSHRunner(sshMgr.Pool())

	node := NewObject()
	node.SetString("id", "live-node-1")
	node.SetString("name", host)
	node.SetString("ip", host)
	node.SetJSON("port", port)
	node.SetString("sshUser", user)

	// Live non-destructive command execution check
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := runner.RunNodeCommand(ctx, node, "uname -s", 10*time.Second)
	if err != nil {
		t.Fatalf("live command failed on %s: %v", host, err)
	}
	if strings.TrimSpace(out) != "Linux" {
		t.Logf("live node OS: %s", strings.TrimSpace(out))
	}

	// End-to-end live HTTP route + ansqueue execution + checkpoint halt on real host
	dbPath := t.TempDir() + "/live-p2c.sqlite"
	store, err := hostcache.Open(dbPath)
	if err != nil {
		t.Fatalf("hostcache.Open: %v", err)
	}
	defer store.Close()

	node.SetString("shortName", "live-node-1")
	node.SetString("tailscaleIp", host)
	node.SetString("tailscaleHost", host)
	node.SetString("privilegeMethod", "sudo")
	node.SetString("nodeType", "linux")
	nodeBytes, _ := node.MarshalJSON()
	if err := store.UpsertHost(hostcache.NodeRecord{
		ID:            "live-node-1",
		Name:          host,
		ShortName:     "live-node-1",
		IP:            host,
		Port:          port,
		SSHUser:       user,
		TailscaleIP:   host,
		TailscaleHost: host,
		Status:        "online",
		NodeJSON:      nodeBytes,
		UpdatedAt:     time.Now().UnixMilli(),
	}); err != nil {
		t.Fatalf("UpsertHost: %v", err)
	}

	qStore := ansqueue.New(store.SQLDB())
	api, err := New(Options{
		Store:      store,
		QueueStore: qStore,
		Runner:     runner,
	})
	if err != nil {
		t.Fatalf("New API: %v", err)
	}

	mux := http.NewServeMux()
	api.Register(mux)
	runOpts := ansqueue.Options{
		WorkDir:     t.TempDir(),
		Invalidator: ansqueue.NewCacheInvalidator(store),
		Timeout:     2 * time.Minute,
	}
	qAPI, err := ansqueue.NewAPI(ansqueue.APIOptions{
		Store:      qStore,
		RunOptions: runOpts,
	})
	if err != nil {
		t.Fatalf("NewAPI ansqueue: %v", err)
	}
	qAPI.Register(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	postJSON := func(path, body string) (int, string) {
		req, err := http.NewRequest(http.MethodPost, srv.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatalf("NewRequest: %v", err)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("Do(%s): %v", path, err)
		}
		defer resp.Body.Close()
		var buf strings.Builder
		_, _ = io.Copy(&buf, resp.Body)
		return resp.StatusCode, strings.TrimSpace(buf.String())
	}

	// Stage Task 1 via ported route: POST /api/nodes/live-node-1/zfs/nonexistent_pool_halt_p2c/scrub
	// Direct execution fails with 500 (since zpool is not present / pool doesn't exist), AND stages Task 1 in queue!
	code1, body1 := postJSON("/api/nodes/live-node-1/zfs/nonexistent_pool_halt_p2c/scrub", "{}")
	t.Logf("Stage 1 (ZFS scrub nonexistent pool): HTTP %d -> %s", code1, body1)

	// Stage Task 2 via ported route: POST /api/nodes/live-node-1/interfaces/lo/toggle {"state":"up"}
	// Direct execution succeeds with 200 on real host, AND stages Task 2 in queue!
	code2, body2 := postJSON("/api/nodes/live-node-1/interfaces/lo/toggle", `{"state":"up"}`)
	t.Logf("Stage 2 (Interface lo toggle up): HTTP %d -> %s", code2, body2)
	if code2 != http.StatusOK {
		t.Fatalf("expected 200 on live lo toggle up, got %d: %s", code2, body2)
	}

	snapBefore := qStore.Snapshot()
	if len(snapBefore.Tasks) != 2 {
		t.Fatalf("expected 2 staged tasks in queue, got %d", len(snapBefore.Tasks))
	}
	t.Logf("Queue before execute: Task[0]=%q (%s), Task[1]=%q (%s)",
		snapBefore.Tasks[0].Title, snapBefore.Tasks[0].Status,
		snapBefore.Tasks[1].Title, snapBefore.Tasks[1].Status)

	// Execute queue via POST /api/queue/execute
	codeExec, bodyExec := postJSON("/api/queue/execute", "{}")
	t.Logf("POST /api/queue/execute: HTTP %d -> %s", codeExec, bodyExec)
	if codeExec != http.StatusOK {
		t.Fatalf("expected 200 from /api/queue/execute, got %d: %s", codeExec, bodyExec)
	}
	if !strings.Contains(bodyExec, `"haltedOnError":true`) {
		t.Fatalf("expected haltedOnError:true in execute response, got: %s", bodyExec)
	}

	snapAfter := qStore.Snapshot()
	t.Logf("Queue after execute: Task[0]=%q (status=%s, error=%q), Task[1]=%q (status=%s)",
		snapAfter.Tasks[0].Title, snapAfter.Tasks[0].Status, snapAfter.Tasks[0].Error,
		snapAfter.Tasks[1].Title, snapAfter.Tasks[1].Status)
	if snapAfter.Tasks[0].Status != ansqueue.StatusFailed {
		t.Fatalf("expected Task[0] status failed, got %s", snapAfter.Tasks[0].Status)
	}
	if snapAfter.Tasks[1].Status != ansqueue.StatusStaged {
		t.Fatalf("expected Task[1] to remain staged after checkpoint halt, got %s", snapAfter.Tasks[1].Status)
	}
}
