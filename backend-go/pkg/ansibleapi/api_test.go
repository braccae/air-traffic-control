package ansibleapi

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"air-traffic-control/backend/pkg/ansqueue"
	"air-traffic-control/backend/pkg/hostcache"
)

type testEnv struct {
	server       *httptest.Server
	store        *hostcache.Store
	queueStore   *ansqueue.Store
	api          *API
	events       *[]map[string]any
	tempDir      string
	cfgPath      string
	playbooksDir string
}

func setupAnsibleTestEnv(t *testing.T) *testEnv {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "hosts-cache.sqlite")
	store, err := hostcache.Open(dbPath)
	if err != nil {
		t.Fatalf("hostcache.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	qStore := ansqueue.New(store.SQLDB())

	events := &[]map[string]any{}
	broadcast := func(ev map[string]any) {
		*events = append(*events, ev)
	}

	cfgPath := filepath.Join(tempDir, "ansible.cfg")
	playbooksDir := filepath.Join(tempDir, "playbooks")
	_ = os.MkdirAll(playbooksDir, 0o755)

	api, err := NewAPI(APIOptions{
		Store:          store,
		QueueStore:     qStore,
		RepoRoot:       tempDir,
		AnsibleCfgPath: cfgPath,
		PlaybooksDir:   playbooksDir,
		Broadcast:      broadcast,
	})
	if err != nil {
		t.Fatalf("NewAPI: %v", err)
	}

	mux := http.NewServeMux()
	api.Register(mux)

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	return &testEnv{
		server:       srv,
		store:        store,
		queueStore:   qStore,
		api:          api,
		events:       events,
		tempDir:      tempDir,
		cfgPath:      cfgPath,
		playbooksDir: playbooksDir,
	}
}

func doJSON(t *testing.T, srv *httptest.Server, method, path string, body any) (int, map[string]any) {
	var bodyReader io.Reader
	if body != nil {
		switch b := body.(type) {
		case string:
			bodyReader = bytes.NewBufferString(b)
		default:
			bytesData, _ := json.Marshal(b)
			bodyReader = bytes.NewBuffer(bytesData)
		}
	}
	req, err := http.NewRequest(method, srv.URL+path, bodyReader)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}

	var parsed map[string]any
	if len(respBytes) > 0 && (respBytes[0] == '{' || respBytes[0] == '[') {
		if respBytes[0] == '{' {
			_ = json.Unmarshal(respBytes, &parsed)
		} else {
			var arr []any
			_ = json.Unmarshal(respBytes, &arr)
			parsed = map[string]any{"_array": arr}
		}
	}

	return resp.StatusCode, parsed
}

func TestAnsibleConfigRoutes(t *testing.T) {
	env := setupAnsibleTestEnv(t)

	// 1. GET /api/ansible/config (auto-creates default)
	status, res := doJSON(t, env.server, http.MethodGet, "/api/ansible/config", nil)
	if status != http.StatusOK {
		t.Fatalf("expected 200, got %d: %v", status, res)
	}
	if res["success"] != true {
		t.Fatalf("expected success: true, got %v", res)
	}
	settings, ok := res["settings"].(map[string]any)
	if !ok || settings["forks"] != float64(10) {
		t.Fatalf("expected forks 10, got %v", settings)
	}

	// 2. PUT /api/ansible/config with valid updates
	putPayload := map[string]any{
		"stdoutCallback":    "yaml",
		"forks":             25,
		"timeout":           45,
		"hostKeyChecking":   true,
		"pipelining":        false,
		"becomeMethod":      "sudo",
		"vaultPasswordFile": ".vault-pass",
	}
	status, res = doJSON(t, env.server, http.MethodPut, "/api/ansible/config", putPayload)
	if status != http.StatusOK || res["success"] != true {
		t.Fatalf("expected 200 success, got %d: %v", status, res)
	}

	// Verify persistence in GET
	status, res = doJSON(t, env.server, http.MethodGet, "/api/ansible/config", nil)
	if status != http.StatusOK {
		t.Fatalf("expected 200, got %d", status)
	}
	s := res["settings"].(map[string]any)
	if s["stdoutCallback"] != "yaml" || s["forks"] != float64(25) || s["becomeMethod"] != "sudo" || s["vaultPasswordFile"] != ".vault-pass" {
		t.Fatalf("persisted settings mismatch: %v", s)
	}

	// 3. PUT validation errors
	badCallback := map[string]any{"stdoutCallback": "invalid"}
	status, res = doJSON(t, env.server, http.MethodPut, "/api/ansible/config", badCallback)
	if status != http.StatusBadRequest {
		t.Fatalf("expected 400 for bad stdoutCallback, got %d: %v", status, res)
	}

	badForks := map[string]any{"forks": 1000}
	status, res = doJSON(t, env.server, http.MethodPut, "/api/ansible/config", badForks)
	if status != http.StatusBadRequest {
		t.Fatalf("expected 400 for bad forks, got %d: %v", status, res)
	}

	badVault := map[string]any{"vaultPasswordFile": "pass\n[injected]"}
	status, res = doJSON(t, env.server, http.MethodPut, "/api/ansible/config", badVault)
	if status != http.StatusBadRequest {
		t.Fatalf("expected 400 for newline in vault password file, got %d: %v", status, res)
	}
}

func TestAnsibleEnvironment(t *testing.T) {
	env := setupAnsibleTestEnv(t)

	status, res := doJSON(t, env.server, http.MethodGet, "/api/ansible/environment", nil)
	if status != http.StatusOK {
		t.Fatalf("expected 200, got %d: %v", status, res)
	}
	if res["success"] != true {
		t.Fatalf("expected success: true, got %v", res)
	}
	if _, ok := res["venvPath"]; !ok {
		t.Fatalf("expected venvPath in environment response")
	}
	if _, ok := res["packages"]; !ok {
		t.Fatalf("expected packages in environment response")
	}
}

func TestAnsibleInventoryAndGalaxyInstall(t *testing.T) {
	env := setupAnsibleTestEnv(t)

	// Seed host in store
	_ = env.store.UpsertHost(hostcache.NodeRecord{
		ID:          "node-1",
		Name:        "srv-1",
		ShortName:   "srv-1",
		TailscaleIP: "100.64.0.1",
		Status:      "online",
		NodeJSON:    []byte(`{"id":"node-1","name":"srv-1","shortName":"srv-1","tailscaleIp":"100.64.0.1","status":"online","tags":["tag:host","tag:worker"]}`),
	})
	_ = env.store.UpsertTagChunk(hostcache.TagChunk{
		ID:        "chunk-1",
		Tag:       "tag:worker",
		SSHUser:   "apps",
		CreatedAt: time.Now().UnixMilli(),
	})
	_ = env.store.UpsertGroupVar("group_vars/all.json", `{"global_var":"test"}`)

	// GET /api/ansible/inventory
	status, res := doJSON(t, env.server, http.MethodGet, "/api/ansible/inventory", nil)
	if status != http.StatusOK {
		t.Fatalf("expected 200, got %d: %v", status, res)
	}
	meta := res["_meta"].(map[string]any)
	hostvars := meta["hostvars"].(map[string]any)
	if _, ok := hostvars["srv-1"]; !ok {
		t.Fatalf("expected srv-1 in hostvars: %v", hostvars)
	}
	tagHost := res["tag_host"].(map[string]any)
	if len(tagHost["hosts"].([]any)) == 0 {
		t.Fatalf("expected hosts in tag_host: %v", tagHost)
	}

	// POST /api/ansible/collections/install validation
	badReq := map[string]any{"collection": "invalid-no-dot"}
	status, res = doJSON(t, env.server, http.MethodPost, "/api/ansible/collections/install", badReq)
	if status != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid collection name, got %d: %v", status, res)
	}
}

func TestAnsibleModules(t *testing.T) {
	env := setupAnsibleTestEnv(t)

	// GET /api/ansible/modules (if ansible-doc is not found, should return 503 error cleanly)
	status, res := doJSON(t, env.server, http.MethodGet, "/api/ansible/modules", nil)
	if status != http.StatusOK && status != http.StatusServiceUnavailable {
		t.Fatalf("expected 200 or 503, got %d: %v", status, res)
	}

	// GET /api/ansible/modules/:name/spec bad module name
	status, res = doJSON(t, env.server, http.MethodGet, "/api/ansible/modules/bad..name/spec", nil)
	if status != http.StatusBadRequest {
		t.Fatalf("expected 400 for bad module name, got %d: %v", status, res)
	}
}

func TestPlaybookCRUDAndSyntax(t *testing.T) {
	env := setupAnsibleTestEnv(t)

	// 1. Initial list
	status, res := doJSON(t, env.server, http.MethodGet, "/api/playbooks", nil)
	if status != http.StatusOK {
		t.Fatalf("expected 200, got %d: %v", status, res)
	}
	playbooks, ok := res["playbooks"].([]any)
	if !ok {
		t.Fatalf("expected playbooks array in response: %v", res)
	}
	if len(playbooks) != 0 {
		t.Fatalf("expected 0 playbooks initially, got %d", len(playbooks))
	}

	// 2. Create new playbook
	createPayload := map[string]any{
		"name":    "custom-audit.yml",
		"content": "---\n- name: Custom Audit\n  hosts: all\n  tasks:\n    - ping:\n",
	}
	status, res = doJSON(t, env.server, http.MethodPost, "/api/playbooks", createPayload)
	if status != http.StatusCreated || res["success"] != true {
		t.Fatalf("expected 201 Created, got %d: %v", status, res)
	}

	// 3. Read back
	status, res = doJSON(t, env.server, http.MethodGet, "/api/playbooks/custom-audit.yml", nil)
	if status != http.StatusOK || res["success"] != true {
		t.Fatalf("expected 200, got %d: %v", status, res)
	}
	if res["content"] != createPayload["content"] {
		t.Fatalf("content mismatch: got %v", res["content"])
	}

	// 4. Update playbook
	updatePayload := map[string]any{
		"content": "---\n- name: Updated Custom Audit\n  hosts: all\n  tasks:\n    - debug:\n        msg: hello\n",
	}
	status, res = doJSON(t, env.server, http.MethodPut, "/api/playbooks/custom-audit.yml", updatePayload)
	if status != http.StatusOK || res["success"] != true {
		t.Fatalf("expected 200, got %d: %v", status, res)
	}

	// 5. Parse YAML document
	parsePayload := map[string]any{"content": updatePayload["content"]}
	status, res = doJSON(t, env.server, http.MethodPost, "/api/playbooks/parse", parsePayload)
	if status != http.StatusOK || res["success"] != true {
		t.Fatalf("expected 200 parse success, got %d: %v", status, res)
	}
	if res["document"] == nil {
		t.Fatalf("expected document in parse response")
	}

	// Invalid YAML
	badParse := map[string]any{"content": ": - bad yaml [["}
	status, res = doJSON(t, env.server, http.MethodPost, "/api/playbooks/parse", badParse)
	if status != http.StatusBadRequest || res["success"] == true {
		t.Fatalf("expected 400 for bad yaml, got %d: %v", status, res)
	}

	// 6. Delete protected baseline playbook should fail
	status, res = doJSON(t, env.server, http.MethodDelete, "/api/playbooks/site.yml", nil)
	if status != http.StatusForbidden {
		t.Fatalf("expected 403 for protected site.yml deletion, got %d: %v", status, res)
	}

	// 7. Delete custom playbook
	status, res = doJSON(t, env.server, http.MethodDelete, "/api/playbooks/custom-audit.yml", nil)
	if status != http.StatusOK || res["success"] != true {
		t.Fatalf("expected 200, got %d: %v", status, res)
	}

	// Read deleted -> 404
	status, _ = doJSON(t, env.server, http.MethodGet, "/api/playbooks/custom-audit.yml", nil)
	if status != http.StatusNotFound {
		t.Fatalf("expected 404 for deleted playbook, got %d", status)
	}
}

func TestPlaybookRunnerAndQueueMode(t *testing.T) {
	env := setupAnsibleTestEnv(t)

	// Create a dummy playbook
	_, err := CreatePlaybook(env.playbooksDir, "test-run.yml", "---\n- name: Test Run\n  hosts: all\n  tasks: []\n")
	if err != nil {
		t.Fatalf("CreatePlaybook: %v", err)
	}

	// Run with mode: "queue" -> stages in ansqueue
	queueRunPayload := map[string]any{
		"playbook": "test-run.yml",
		"target":   "srv-1",
		"mode":     "queue",
	}
	status, res := doJSON(t, env.server, http.MethodPost, "/api/playbook/run", queueRunPayload)
	if status != http.StatusOK || res["success"] != true {
		t.Fatalf("expected 200 for queue run, got %d: %v", status, res)
	}
	if env.queueStore.StagedCount() != 1 {
		t.Fatalf("expected 1 task staged in queue, got %d", env.queueStore.StagedCount())
	}

	// Cancel non-existent task
	cancelPayload := map[string]any{"taskId": "task-not-running"}
	status, res = doJSON(t, env.server, http.MethodPost, "/api/playbook/cancel", cancelPayload)
	if status != http.StatusNotFound {
		t.Fatalf("expected 404 for cancelling inactive task, got %d: %v", status, res)
	}

	// Test live stdout streaming via mock ansible-playbook binary
	binDir := filepath.Join(env.tempDir, "data", "ansible-venv", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("MkdirAll binDir: %v", err)
	}
	mockBin := filepath.Join(binDir, "ansible-playbook")
	script := "#!/bin/sh\necho \"PLAY [Test Live Play] ***\"\necho \"TASK [Gathering Facts] ***\"\necho \"ok: [srv-1]\"\necho \"PLAY RECAP ***\"\nexit 0\n"
	if err := os.WriteFile(mockBin, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile mockBin: %v", err)
	}

	liveRunPayload := map[string]any{
		"playbook": "test-run.yml",
		"target":   "srv-1",
	}
	status, res = doJSON(t, env.server, http.MethodPost, "/api/playbook/run", liveRunPayload)
	if status != http.StatusOK || res["success"] != true {
		t.Fatalf("expected 200 for live run, got %d: %v", status, res)
	}
	taskId, _ := res["taskId"].(string)
	if taskId == "" {
		t.Fatalf("expected taskId in response: %v", res)
	}

	// Wait for background goroutine to finish streaming and persisting logs
	deadline := time.Now().Add(3 * time.Second)
	var savedTask *hostcache.Task
	for time.Now().Before(deadline) {
		tk, _ := env.store.GetTask(taskId)
		if tk != nil && tk.Status == "OK" {
			savedTask = tk
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if savedTask == nil || savedTask.Status != "OK" {
		t.Fatalf("expected task %s to finish with OK status, got %+v", taskId, savedTask)
	}

	var savedLogs []string
	_ = json.Unmarshal(savedTask.Logs, &savedLogs)
	if len(savedLogs) < 4 {
		t.Fatalf("expected incremental logs persisted in SQLite, got %v", savedLogs)
	}

	var taskOutputCount int
	for _, ev := range *env.events {
		if ev["type"] == "task_output" && ev["taskId"] == taskId {
			taskOutputCount++
		}
	}
	if taskOutputCount < 3 {
		t.Fatalf("expected at least 3 live task_output broadcast events, got %d", taskOutputCount)
	}
}

func TestGroupVarsStore(t *testing.T) {
	env := setupAnsibleTestEnv(t)

	// 1. Initial GET
	status, res := doJSON(t, env.server, http.MethodGet, "/api/group-vars", nil)
	if status != http.StatusOK {
		t.Fatalf("expected 200, got %d: %v", status, res)
	}

	// 2. PUT valid group var
	putPayload := map[string]any{
		"file":    "group_vars/all.yml",
		"content": "datacenter_region: us-east-1\n",
	}
	status, res = doJSON(t, env.server, http.MethodPut, "/api/group-vars", putPayload)
	if status != http.StatusOK || res["success"] != true {
		t.Fatalf("expected 200, got %d: %v", status, res)
	}

	// 3. PUT invalid path traversal
	badPathPayload := map[string]any{
		"file":    "../secrets.yml",
		"content": "secret: bad",
	}
	status, res = doJSON(t, env.server, http.MethodPut, "/api/group-vars", badPathPayload)
	if status != http.StatusBadRequest {
		t.Fatalf("expected 400 for path traversal, got %d: %v", status, res)
	}

	// 4. Verify persisted in GET
	status, res = doJSON(t, env.server, http.MethodGet, "/api/group-vars", nil)
	if status != http.StatusOK {
		t.Fatalf("expected 200, got %d: %v", status, res)
	}
	if res["group_vars/all.yml"] != "datacenter_region: us-east-1\n" {
		t.Fatalf("group var content mismatch: %v", res)
	}

	// 5. DELETE group var
	status, res = doJSON(t, env.server, http.MethodDelete, "/api/group-vars?file=group_vars/all.yml", nil)
	if status != http.StatusOK || res["success"] != true {
		t.Fatalf("expected 200, got %d: %v", status, res)
	}

	// 6. DELETE missing group var -> 404
	status, _ = doJSON(t, env.server, http.MethodDelete, "/api/group-vars?file=group_vars/missing.yml", nil)
	if status != http.StatusNotFound {
		t.Fatalf("expected 404 for missing group var delete, got %d", status)
	}
}

func TestPlaybookQueueEndpoint(t *testing.T) {
	env := setupAnsibleTestEnv(t)

	// Create a playbook first
	createPayload := map[string]any{
		"name":    "queue-me.yml",
		"content": "---\n- hosts: all\n  tasks: []\n",
	}
	status, res := doJSON(t, env.server, http.MethodPost, "/api/playbooks", createPayload)
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("failed to create playbook: %d %v", status, res)
	}

	// Stage playbook via POST /api/playbooks/queue-me.yml/queue
	queuePayload := map[string]any{
		"target":    "tag_host",
		"checkMode": true,
		"tags":      "deploy",
		"execute":   false,
	}
	status, res = doJSON(t, env.server, http.MethodPost, "/api/playbooks/queue-me.yml/queue", queuePayload)
	if status != http.StatusOK || res["success"] != true {
		t.Fatalf("expected 200 for POST /api/playbooks/queue-me.yml/queue, got %d: %v", status, res)
	}

	snap := env.queueStore.Snapshot()
	if len(snap.Tasks) != 1 {
		t.Fatalf("expected 1 queued playbook task, got %d", len(snap.Tasks))
	}
	if snap.Tasks[0].PlaybookRun == nil || snap.Tasks[0].PlaybookRun.Playbook != "queue-me.yml" {
		t.Errorf("expected PlaybookRun for queue-me.yml, got %+v", snap.Tasks[0].PlaybookRun)
	}
	if snap.Tasks[0].PlaybookRun.Target != "tag_host" || !snap.Tasks[0].PlaybookRun.CheckMode {
		t.Errorf("unexpected PlaybookRun fields: %+v", snap.Tasks[0].PlaybookRun)
	}
}
