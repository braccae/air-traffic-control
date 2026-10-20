package nodeapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"air-traffic-control/backend/pkg/ansqueue"
	"air-traffic-control/backend/pkg/hostcache"
	"air-traffic-control/backend/pkg/nodeapi"
	_ "air-traffic-control/backend/plugins/all"
)

type mutationStubRunner struct {
	mu       sync.Mutex
	lastCmd  string
	lastNode *nodeapi.Object
	runFn    func(ctx context.Context, node *nodeapi.Object, cmd string, timeout time.Duration) (string, error)
}

func (m *mutationStubRunner) RunNodeCommand(ctx context.Context, node *nodeapi.Object, command string, timeout time.Duration) (string, error) {
	m.mu.Lock()
	m.lastCmd = command
	m.lastNode = node
	fn := m.runFn
	m.mu.Unlock()

	if fn != nil {
		return fn(ctx, node, command, timeout)
	}
	return "ok", nil
}

type testHarness struct {
	ts        *httptest.Server
	store     *hostcache.Store
	qStore    *ansqueue.Store
	api       *nodeapi.API
	runner    *mutationStubRunner
	events    *[]map[string]any
	eventsMu  *sync.Mutex
}

func setupMutationTestHarness(t *testing.T) *testHarness {
	dbPath := filepath.Join(t.TempDir(), "hosts-cache.sqlite")
	store, err := hostcache.Open(dbPath)
	if err != nil {
		t.Fatalf("hostcache.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	qStore := ansqueue.New(store.SQLDB())

	runner := &mutationStubRunner{}

	events := &[]map[string]any{}
	var eventsMu sync.Mutex
	broadcast := func(ev map[string]any) {
		eventsMu.Lock()
		*events = append(*events, ev)
		eventsMu.Unlock()
	}

	api, err := nodeapi.New(nodeapi.Options{
		Store:      store,
		QueueStore: qStore,
		Runner:     runner,
		Broadcast:  broadcast,
		Now: func() time.Time {
			return time.UnixMilli(1700000000000)
		},
	})
	if err != nil {
		t.Fatalf("New nodeapi: %v", err)
	}

	// Seed one baseline test node
	nodeObj := nodeapi.NewObject()
	nodeObj.SetString("id", "node-1")
	nodeObj.SetString("name", "test-host")
	nodeObj.SetString("shortName", "test-host")
	nodeObj.SetString("ip", "192.168.1.100")
	nodeObj.SetString("tailscaleIp", "100.64.0.1")
	nodeObj.SetString("tailscaleHost", "test-host.leo-banjo.ts.net")
	nodeObj.SetString("sshUser", "core")
	nodeObj.Set("port", json.RawMessage("22"))
	nodeObj.SetString("privilegeMethod", "run0")
	nodeObj.SetString("nodeType", "linux")

	nodeBytes, _ := nodeObj.MarshalJSON()
	if err := store.UpsertHost(hostcache.NodeRecord{
		ID:        "node-1",
		Name:      "test-host",
		IP:        "192.168.1.100",
		NodeJSON:  nodeBytes,
		UpdatedAt: time.Now().UnixMilli(),
	}); err != nil {
		t.Fatalf("UpsertHost: %v", err)
	}

	mux := http.NewServeMux()
	api.Register(mux)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	return &testHarness{
		ts:       ts,
		store:    store,
		qStore:   qStore,
		api:      api,
		runner:   runner,
		events:   events,
		eventsMu: &eventsMu,
	}
}

func doJSONRequest(t *testing.T, url, method string, body any) (*http.Response, map[string]any) {
	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		bodyReader = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, url, bodyReader)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()
	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read resp: %v", err)
	}
	var res map[string]any
	if len(respBytes) > 0 {
		_ = json.Unmarshal(respBytes, &res)
	}
	return resp, res
}

func TestMutationRoutes_16RoutesParityAndQueueStaging(t *testing.T) {
	h := setupMutationTestHarness(t)

	// 1. POST /api/nodes/:id/interfaces/:name/toggle
	t.Run("Route1_InterfaceToggle", func(t *testing.T) {
		h.runner.runFn = func(ctx context.Context, node *nodeapi.Object, cmd string, timeout time.Duration) (string, error) {
			if !strings.Contains(cmd, "ip link set eth0 down") {
				return "", fmt.Errorf("bad cmd: %s", cmd)
			}
			return "", nil
		}
		resp, data := doJSONRequest(t, h.ts.URL+"/api/nodes/node-1/interfaces/eth0/toggle", http.MethodPost, map[string]any{"state": "down"})
		if resp.StatusCode != http.StatusOK || data["success"] != true || data["state"] != "down" || data["interface"] != "eth0" {
			t.Errorf("Toggle failed: %d, %+v", resp.StatusCode, data)
		}

		// Error path
		h.runner.runFn = func(ctx context.Context, node *nodeapi.Object, cmd string, timeout time.Duration) (string, error) {
			return "", errors.New("device busy")
		}
		respErr, dataErr := doJSONRequest(t, h.ts.URL+"/api/nodes/node-1/interfaces/eth0/toggle", http.MethodPost, map[string]any{"state": "up"})
		if respErr.StatusCode != http.StatusInternalServerError || dataErr["success"] != false {
			t.Errorf("expected 500 on error: %d, %+v", respErr.StatusCode, dataErr)
		}
	})

	// 2. POST /api/nodes/:id/interfaces/connect
	t.Run("Route2_InterfaceConnect", func(t *testing.T) {
		h.runner.runFn = func(ctx context.Context, node *nodeapi.Object, cmd string, timeout time.Duration) (string, error) {
			return "Interface connected", nil
		}
		resp, data := doJSONRequest(t, h.ts.URL+"/api/nodes/node-1/interfaces/connect", http.MethodPost, map[string]any{
			"type": "wireguard",
			"name": "wg0",
			"cidr": "10.0.0.2/24",
		})
		if resp.StatusCode != http.StatusOK || data["success"] != true {
			t.Errorf("Connect failed: %d, %+v", resp.StatusCode, data)
		}

		// Validation error returns 500
		respBad, dataBad := doJSONRequest(t, h.ts.URL+"/api/nodes/node-1/interfaces/connect", http.MethodPost, map[string]any{
			"type": "unsupported_type",
			"name": "wg0",
		})
		if respBad.StatusCode != http.StatusInternalServerError || dataBad["success"] != false {
			t.Errorf("expected 500 on validation error: %d, %+v", respBad.StatusCode, dataBad)
		}
	})

	// 3. POST /api/nodes/:id/firewall/rules
	t.Run("Route3_FirewallAddRule", func(t *testing.T) {
		h.runner.runFn = func(ctx context.Context, node *nodeapi.Object, cmd string, timeout time.Duration) (string, error) {
			return "success", nil
		}
		resp, data := doJSONRequest(t, h.ts.URL+"/api/nodes/node-1/firewall/rules", http.MethodPost, map[string]any{
			"port":     "8080",
			"protocol": "tcp",
			"zone":     "public",
		})
		if resp.StatusCode != http.StatusOK || data["success"] != true {
			t.Errorf("Firewall add failed: %d, %+v", resp.StatusCode, data)
		}
	})

	// 4. DELETE /api/nodes/:id/firewall/rules/:ruleId
	t.Run("Route4_FirewallDeleteRule", func(t *testing.T) {
		h.runner.runFn = func(ctx context.Context, node *nodeapi.Object, cmd string, timeout time.Duration) (string, error) {
			if !strings.Contains(cmd, "--remove-port=8080/tcp") {
				return "", fmt.Errorf("unexpected cmd: %s", cmd)
			}
			return "success", nil
		}
		// ruleId containing slash
		resp, data := doJSONRequest(t, h.ts.URL+"/api/nodes/node-1/firewall/rules/8080/tcp", http.MethodDelete, nil)
		if resp.StatusCode != http.StatusOK || data["success"] != true {
			t.Errorf("Firewall delete failed: %d, %+v", resp.StatusCode, data)
		}
	})

	// 5. POST /api/nodes/:id/disks/:name/smart-test
	t.Run("Route5_DiskSmartTest", func(t *testing.T) {
		h.runner.runFn = func(ctx context.Context, node *nodeapi.Object, cmd string, timeout time.Duration) (string, error) {
			return "Testing has begun", nil
		}
		resp, data := doJSONRequest(t, h.ts.URL+"/api/nodes/node-1/disks/sda/smart-test", http.MethodPost, nil)
		if resp.StatusCode != http.StatusOK || data["success"] != true || data["disk"] != "sda" {
			t.Errorf("Smart test failed: %d, %+v", resp.StatusCode, data)
		}
	})

	// 6. POST /api/nodes/:id/zfs/:pool/scrub
	t.Run("Route6_ZfsScrub", func(t *testing.T) {
		h.runner.runFn = func(ctx context.Context, node *nodeapi.Object, cmd string, timeout time.Duration) (string, error) {
			return "", nil
		}
		resp, data := doJSONRequest(t, h.ts.URL+"/api/nodes/node-1/zfs/tank/scrub", http.MethodPost, nil)
		if resp.StatusCode != http.StatusOK || data["success"] != true || data["pool"] != "tank" {
			t.Errorf("ZFS scrub failed: %d, %+v", resp.StatusCode, data)
		}
	})

	// 7. POST /api/nodes/:id/vms/:name/action
	t.Run("Route7_VmAction", func(t *testing.T) {
		h.runner.runFn = func(ctx context.Context, node *nodeapi.Object, cmd string, timeout time.Duration) (string, error) {
			return "Domain started", nil
		}
		resp, data := doJSONRequest(t, h.ts.URL+"/api/nodes/node-1/vms/my-vm/action", http.MethodPost, map[string]any{"action": "start"})
		if resp.StatusCode != http.StatusOK || data["success"] != true || data["vm"] != "my-vm" || data["action"] != "start" {
			t.Errorf("VM action failed: %d, %+v", resp.StatusCode, data)
		}

		// Invalid action 400
		respBad, _ := doJSONRequest(t, h.ts.URL+"/api/nodes/node-1/vms/my-vm/action", http.MethodPost, map[string]any{"action": "explode"})
		if respBad.StatusCode != http.StatusBadRequest {
			t.Errorf("expected 400 for bad action, got %d", respBad.StatusCode)
		}

		// Invalid VM name 400
		respBadName, _ := doJSONRequest(t, h.ts.URL+"/api/nodes/node-1/vms/bad$name/action", http.MethodPost, map[string]any{"action": "start"})
		if respBadName.StatusCode != http.StatusBadRequest {
			t.Errorf("expected 400 for bad VM name, got %d", respBadName.StatusCode)
		}
	})

	// 8. POST /api/nodes/:id/vms/create
	t.Run("Route8_VmCreate", func(t *testing.T) {
		h.runner.runFn = func(ctx context.Context, node *nodeapi.Object, cmd string, timeout time.Duration) (string, error) {
			return "Domain defined", nil
		}
		resp, data := doJSONRequest(t, h.ts.URL+"/api/nodes/node-1/vms/create", http.MethodPost, map[string]any{
			"name":   "test-vm",
			"vcpus":  2,
			"ramGiB": 4,
		})
		if resp.StatusCode != http.StatusOK || data["success"] != true || data["vm"] == nil {
			t.Errorf("VM create failed: %d, %+v", resp.StatusCode, data)
		}

		// Missing name 400
		respBad, _ := doJSONRequest(t, h.ts.URL+"/api/nodes/node-1/vms/create", http.MethodPost, map[string]any{})
		if respBad.StatusCode != http.StatusBadRequest {
			t.Errorf("expected 400 for missing VM name, got %d", respBad.StatusCode)
		}
	})

	// 9. POST /api/nodes/:id/shares/reload
	t.Run("Route9_SharesReload", func(t *testing.T) {
		h.runner.runFn = func(ctx context.Context, node *nodeapi.Object, cmd string, timeout time.Duration) (string, error) {
			return "Reloaded", nil
		}
		resp, data := doJSONRequest(t, h.ts.URL+"/api/nodes/node-1/shares/reload", http.MethodPost, nil)
		if resp.StatusCode != http.StatusOK || data["success"] != true {
			t.Errorf("Shares reload failed: %d, %+v", resp.StatusCode, data)
		}
	})

	// 10. POST /api/nodes/:id/shares/nfs
	t.Run("Route10_SharesNfs", func(t *testing.T) {
		h.runner.runFn = func(ctx context.Context, node *nodeapi.Object, cmd string, timeout time.Duration) (string, error) {
			return "Exported", nil
		}
		resp, data := doJSONRequest(t, h.ts.URL+"/api/nodes/node-1/shares/nfs", http.MethodPost, map[string]any{
			"path":   "/srv/nfs/data",
			"client": "192.168.1.0/24",
		})
		if resp.StatusCode != http.StatusOK || data["success"] != true {
			t.Errorf("Shares NFS failed: %d, %+v", resp.StatusCode, data)
		}

		// Missing path 400
		respBad, _ := doJSONRequest(t, h.ts.URL+"/api/nodes/node-1/shares/nfs", http.MethodPost, map[string]any{})
		if respBad.StatusCode != http.StatusBadRequest {
			t.Errorf("expected 400 for missing path, got %d", respBad.StatusCode)
		}
	})

	// 11. POST /api/nodes/:id/shares/smb
	t.Run("Route11_SharesSmb", func(t *testing.T) {
		h.runner.runFn = func(ctx context.Context, node *nodeapi.Object, cmd string, timeout time.Duration) (string, error) {
			return "Configured", nil
		}
		resp, data := doJSONRequest(t, h.ts.URL+"/api/nodes/node-1/shares/smb", http.MethodPost, map[string]any{
			"name": "media",
			"path": "/srv/smb/media",
		})
		if resp.StatusCode != http.StatusOK || data["success"] != true {
			t.Errorf("Shares SMB failed: %d, %+v", resp.StatusCode, data)
		}

		// Missing name/path 400
		respBad, _ := doJSONRequest(t, h.ts.URL+"/api/nodes/node-1/shares/smb", http.MethodPost, map[string]any{"name": "media"})
		if respBad.StatusCode != http.StatusBadRequest {
			t.Errorf("expected 400 for missing path, got %d", respBad.StatusCode)
		}
	})

	// 12. POST /api/nodes/:id/users/create
	t.Run("Route12_UsersCreate", func(t *testing.T) {
		h.runner.runFn = func(ctx context.Context, node *nodeapi.Object, cmd string, timeout time.Duration) (string, error) {
			return "", nil
		}
		resp, data := doJSONRequest(t, h.ts.URL+"/api/nodes/node-1/users/create", http.MethodPost, map[string]any{"username": "testuser"})
		if resp.StatusCode != http.StatusOK || data["success"] != true || data["username"] != "testuser" {
			t.Errorf("Users create failed: %d, %+v", resp.StatusCode, data)
		}

		// Missing username 400
		respBad, _ := doJSONRequest(t, h.ts.URL+"/api/nodes/node-1/users/create", http.MethodPost, map[string]any{})
		if respBad.StatusCode != http.StatusBadRequest {
			t.Errorf("expected 400 for missing username, got %d", respBad.StatusCode)
		}
	})

	// 13. POST /api/nodes/:id/users/:user/password
	t.Run("Route13_UsersPassword_SecretMasking", func(t *testing.T) {
		h.runner.runFn = func(ctx context.Context, node *nodeapi.Object, cmd string, timeout time.Duration) (string, error) {
			return "password changed", nil
		}
		plainPassword := "sUpErS3cr3tP@ssword!"
		resp, data := doJSONRequest(t, h.ts.URL+"/api/nodes/node-1/users/testuser/password", http.MethodPost, map[string]any{
			"password": plainPassword,
		})
		if resp.StatusCode != http.StatusOK || data["success"] != true {
			t.Errorf("Users password failed: %d, %+v", resp.StatusCode, data)
		}

		// Missing password 400
		respBad, _ := doJSONRequest(t, h.ts.URL+"/api/nodes/node-1/users/testuser/password", http.MethodPost, map[string]any{})
		if respBad.StatusCode != http.StatusBadRequest {
			t.Errorf("expected 400 for missing password, got %d", respBad.StatusCode)
		}

		// Verify secret masking in staged queue task
		snap := h.qStore.Snapshot()
		var pwdTask *ansqueue.Task
		for i := len(snap.Tasks) - 1; i >= 0; i-- {
			if strings.Contains(snap.Tasks[i].Title, "Update password for user testuser") {
				pwdTask = &snap.Tasks[i]
				break
			}
		}
		if pwdTask == nil {
			t.Fatalf("staged password task not found in queue")
		}
		if pwdTask.SecretsMasked["password"] != ansqueue.MaskedSecretValue("user_password_testuser") {
			t.Errorf("expected masked secret in SecretsMasked, got: %+v", pwdTask.SecretsMasked)
		}
		// Confirm plaintext NEVER stored in parameters
		if paramPwd := fmt.Sprintf("%v", pwdTask.Steps[0].Parameters["password"]); paramPwd == plainPassword || strings.Contains(paramPwd, plainPassword) {
			t.Fatalf("CRITICAL SECRET LEAK: plaintext password found in queued task parameters: %s", paramPwd)
		}
	})

	// 14. POST /api/nodes/:id/users/:user/smbpassword
	t.Run("Route14_UsersSmbPassword_SecretMasking", func(t *testing.T) {
		h.runner.runFn = func(ctx context.Context, node *nodeapi.Object, cmd string, timeout time.Duration) (string, error) {
			return "smb password changed", nil
		}
		plainPassword := "sambaP@ssword999!"
		resp, data := doJSONRequest(t, h.ts.URL+"/api/nodes/node-1/users/testuser/smbpassword", http.MethodPost, map[string]any{
			"password": plainPassword,
		})
		if resp.StatusCode != http.StatusOK || data["success"] != true {
			t.Errorf("Users smbpassword failed: %d, %+v", resp.StatusCode, data)
		}

		// Missing password 400
		respBad, _ := doJSONRequest(t, h.ts.URL+"/api/nodes/node-1/users/testuser/smbpassword", http.MethodPost, map[string]any{})
		if respBad.StatusCode != http.StatusBadRequest {
			t.Errorf("expected 400 for missing samba password, got %d", respBad.StatusCode)
		}

		// Verify secret masking in staged queue task
		snap := h.qStore.Snapshot()
		var smbTask *ansqueue.Task
		for i := len(snap.Tasks) - 1; i >= 0; i-- {
			if strings.Contains(snap.Tasks[i].Title, "Update Samba password for user testuser") {
				smbTask = &snap.Tasks[i]
				break
			}
		}
		if smbTask == nil {
			t.Fatalf("staged smbpassword task not found in queue")
		}
		if smbTask.SecretsMasked["smb_password"] != ansqueue.MaskedSecretValue("smb_password_testuser") {
			t.Errorf("expected masked secret in SecretsMasked, got: %+v", smbTask.SecretsMasked)
		}
		// Confirm plaintext NEVER stored in parameters
		if paramPwd := fmt.Sprintf("%v", smbTask.Steps[0].Parameters["smb_password"]); paramPwd == plainPassword || strings.Contains(paramPwd, plainPassword) {
			t.Fatalf("CRITICAL SECRET LEAK: plaintext samba password found in queued task parameters: %s", paramPwd)
		}
	})

	// 15. POST /api/nodes/:id/reboot
	t.Run("Route15_Reboot", func(t *testing.T) {
		h.runner.runFn = func(ctx context.Context, node *nodeapi.Object, cmd string, timeout time.Duration) (string, error) {
			return "", nil
		}
		resp, data := doJSONRequest(t, h.ts.URL+"/api/nodes/node-1/reboot", http.MethodPost, nil)
		if resp.StatusCode != http.StatusOK || data["success"] != true || data["task"] == nil {
			t.Errorf("Reboot failed: %d, %+v", resp.StatusCode, data)
		}
	})

	// 16. POST /api/podman/action
	t.Run("Route16_PodmanAction", func(t *testing.T) {
		h.runner.runFn = func(ctx context.Context, node *nodeapi.Object, cmd string, timeout time.Duration) (string, error) {
			if !strings.Contains(cmd, "podman restart my-container") {
				return "", fmt.Errorf("unexpected cmd: %s", cmd)
			}
			return "Container restarted", nil
		}
		resp, data := doJSONRequest(t, h.ts.URL+"/api/podman/action", http.MethodPost, map[string]any{
			"host":      "node-1",
			"container": "my-container",
			"action":    "restart",
		})
		if resp.StatusCode != http.StatusOK || data["success"] != true || data["output"] != "Container restarted" {
			t.Errorf("Podman action failed: %d, %+v", resp.StatusCode, data)
		}

		// Rootless user command construction
		h.runner.runFn = func(ctx context.Context, node *nodeapi.Object, cmd string, timeout time.Duration) (string, error) {
			if !strings.Contains(cmd, "run0 --background=no --user apps podman stop app-box") ||
				!strings.Contains(cmd, "XDG_RUNTIME_DIR=/run/user/1001") {
				return "", fmt.Errorf("unexpected rootless cmd: %s", cmd)
			}
			return "Stopped", nil
		}
		uid := 1001
		resp2, data2 := doJSONRequest(t, h.ts.URL+"/api/podman/action", http.MethodPost, map[string]any{
			"host":      "node-1",
			"container": "app-box",
			"action":    "stop",
			"user":      "apps",
			"uid":       uid,
		})
		if resp2.StatusCode != http.StatusOK || data2["success"] != true {
			t.Errorf("Rootless podman action failed: %d, %+v", resp2.StatusCode, data2)
		}

		// Error path returns status 200 with {success: false, error: ...} (matching Express server.ts:1295)
		h.runner.runFn = func(ctx context.Context, node *nodeapi.Object, cmd string, timeout time.Duration) (string, error) {
			return "", errors.New("container not found")
		}
		respErr, dataErr := doJSONRequest(t, h.ts.URL+"/api/podman/action", http.MethodPost, map[string]any{
			"host":      "node-1",
			"container": "missing",
			"action":    "stop",
		})
		if respErr.StatusCode != http.StatusOK || dataErr["success"] != false || !strings.Contains(fmt.Sprint(dataErr["error"]), "container not found") {
			t.Errorf("expected 200 with success: false on podman error: %d, %+v", respErr.StatusCode, dataErr)
		}
	})

	// Checkpoint Halting Test
	t.Run("CheckpointHalting", func(t *testing.T) {
		// Stage two tasks
		task1 := ansqueue.Task{
			Title:           "Failing Task",
			TargetNodeID:    "node-1",
			TargetNodeName:  "test-host",
			TargetHostRoute: "192.168.1.100",
			Category:        "system",
			PrimaryModule:   "ansible.builtin.command",
			Steps: []ansqueue.Step{
				{
					Name:       "Fail deliberately",
					Module:     "ansible.builtin.command",
					Parameters: map[string]any{"cmd": "false"},
				},
			},
			Status:    ansqueue.StatusStaged,
			CreatedAt: time.Now().UnixMilli(),
		}
		task2 := ansqueue.Task{
			Title:           "Subsequent Staged Task",
			TargetNodeID:    "node-1",
			TargetNodeName:  "test-host",
			TargetHostRoute: "192.168.1.100",
			Category:        "system",
			PrimaryModule:   "ansible.builtin.command",
			Steps: []ansqueue.Step{
				{
					Name:       "Should never run",
					Module:     "ansible.builtin.command",
					Parameters: map[string]any{"cmd": "echo should_not_run"},
				},
			},
			Status:    ansqueue.StatusStaged,
			CreatedAt: time.Now().UnixMilli() + 100,
		}

		t1, err := h.qStore.Enqueue(task1)
		if err != nil {
			t.Fatalf("enqueue t1: %v", err)
		}
		t2, err := h.qStore.Enqueue(task2)
		if err != nil {
			t.Fatalf("enqueue t2: %v", err)
		}

		// Execute next task with a runner that simulates failure
		runnerOpts := ansqueue.Options{
			WorkDir:     t.TempDir(),
			Invalidator: ansqueue.NewCacheInvalidator(h.store),
			AnsibleBinary: "/bin/false", // Guaranteed to fail execution
		}
		execResult, err := h.qStore.ExecuteNextTask(context.Background(), runnerOpts)
		if err != nil {
			t.Fatalf("ExecuteNextTask returned error: %v", err)
		}

		// Verify checkpoint halting
		if !execResult.HaltedOnError {
			t.Errorf("expected HaltedOnError to be true, got false")
		}
		if execResult.Result == nil || !execResult.Result.Failed {
			t.Errorf("expected executed task result to be failed, got %+v", execResult.Result)
		}

		// Verify subsequent task t2 remains staged in SQLite!
		snap := h.qStore.Snapshot()
		var foundT2 *ansqueue.Task
		for _, tk := range snap.Tasks {
			if tk.ID == t2.ID {
				foundT2 = &tk
				break
			}
		}
		if foundT2 == nil {
			t.Fatalf("subsequent task t2 was dropped from queue")
		}
		if foundT2.Status != ansqueue.StatusStaged {
			t.Errorf("subsequent task t2 status = %q; want %q (checkpoint halt invariant violated)", foundT2.Status, ansqueue.StatusStaged)
		}

		_ = t1
	})
}
