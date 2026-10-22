package taskapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"air-traffic-control/backend/pkg/plugin"
	"air-traffic-control/backend/pkg/plugin/newapi"
	"air-traffic-control/backend/pkg/taskapi"
)

type testStoragePlugin struct{}

func (p *testStoragePlugin) Meta() plugin.Meta {
	return plugin.Meta{
		ID:    "storage/zfs-test",
		Kind:  plugin.KindStorage,
		Name:  "zfs-test",
		Label: "ZFS Test",
		Icon:  "database",
		Order: 10,
	}
}

func (p *testStoragePlugin) Compatible(h plugin.HostFacts) bool { return true }
func (p *testStoragePlugin) Requirements() plugin.Requirements  { return plugin.Requirements{} }
func (p *testStoragePlugin) Probe(ctx context.Context, r plugin.Runner, h plugin.HostFacts) (plugin.Detection, error) {
	return plugin.Detection{Detected: true, Status: "ONLINE"}, nil
}

func (p *testStoragePlugin) Actions() []plugin.ActionSpec {
	return []plugin.ActionSpec{
		{
			Name:  "scrub",
			Label: "Scrub Pool",
			Options: []plugin.OptionSchema{
				{Name: "pool", Type: "string", Required: true},
			},
		},
	}
}

func (p *testStoragePlugin) Compile(h plugin.HostFacts, action string, opts map[string]any) (plugin.TaskSpec, error) {
	pool, _ := opts["pool"].(string)
	return plugin.TaskSpec{
		Name: "Scrub " + pool,
		Steps: []plugin.TaskStep{
			{
				Name:   "Run zpool scrub",
				Module: "ansible.builtin.command",
				Params: map[string]any{"cmd": "zpool scrub " + pool},
			},
		},
	}, nil
}

func (p *testStoragePlugin) TaskTypes() []newapi.TaskTypeSpec {
	return []newapi.TaskTypeSpec{
		{
			Type:           taskapi.TaskTypeStorageOp,
			Label:          "ZFS Storage Operation",
			PluginID:       p.Meta().ID,
			SupportsDryRun: true,
			SupportsCancel: true,
			Parameters: []plugin.OptionSchema{
				{Name: "pool", Type: "string", Required: true},
				{Name: "operation", Type: "select", Required: false, Default: "scrub", Enum: []string{"scrub", "trim"}},
				{Name: "passphrase", Type: "string", Required: false, Secret: true},
			},
		},
	}
}

func (p *testStoragePlugin) CompileTask(ctx context.Context, h plugin.HostFacts, req newapi.TaskRequest) (plugin.TaskSpec, error) {
	pool, _ := req.Params["pool"].(string)
	op, _ := req.Params["operation"].(string)
	return plugin.TaskSpec{
		Name: "ZFS " + op + " on " + pool,
		Steps: []plugin.TaskStep{
			{
				Name:   "Execute ZFS " + op,
				Module: "community.general.zpool_facts",
				Params: map[string]any{"name": pool},
			},
		},
	}, nil
}

func TestTaskTypeEnumAndDefaults(t *testing.T) {
	all := taskapi.AllTaskTypes()
	if len(all) != 7 {
		t.Fatalf("expected 7 canonical task types, got %d", len(all))
	}
	for _, tt := range all {
		if err := taskapi.ValidateTaskType(tt); err != nil {
			t.Errorf("expected built-in task type %q to validate, got: %v", tt, err)
		}
	}
	if err := taskapi.ValidateTaskType(""); err == nil {
		t.Error("expected empty task type to fail validation")
	}
	if err := taskapi.ValidateTaskType("nonexistent"); err == nil {
		t.Error("expected unknown task type to fail validation")
	}

	defaults := taskapi.DefaultTaskTypes()
	if len(defaults) != 7 {
		t.Fatalf("expected 7 default task type specs, got %d", len(defaults))
	}
}

func TestTaskParamsHelpersAndDefaults(t *testing.T) {
	pReg := plugin.NewRegistry()
	tp := &testStoragePlugin{}
	if err := pReg.Register(tp); err != nil {
		t.Fatalf("register plugin: %v", err)
	}

	reg := taskapi.NewRegistry(pReg)
	spec, ok := reg.GetType(taskapi.TaskTypeStorageOp, "storage/zfs-test")
	if !ok {
		t.Fatal("expected plugin-contributed storage_op TaskTypeSpec to be found")
	}

	params := taskapi.TaskParams{
		ID:       "task-101",
		Type:     taskapi.TaskTypeStorageOp,
		NodeID:   "node-alpha",
		PluginID: "storage/zfs-test",
		Options: map[string]any{
			"pool":       "tank",
			"count":      "42",
			"force":      "true",
			"passphrase": "super-secret-value",
		},
	}

	params.ApplyDefaults(spec)

	if op, ok := params.StringOption("operation"); !ok || op != "scrub" {
		t.Errorf("expected default operation 'scrub', got %q (ok=%v)", op, ok)
	}
	if n, ok := params.IntOption("count"); !ok || n != 42 {
		t.Errorf("expected IntOption('count') == 42, got %d (ok=%v)", n, ok)
	}
	if b, ok := params.BoolOption("force"); !ok || !b {
		t.Errorf("expected BoolOption('force') == true, got %v (ok=%v)", b, ok)
	}
	if masked := params.SecretsMasked["passphrase"]; masked == "" || masked == "super-secret-value" {
		t.Errorf("expected passphrase to be masked in SecretsMasked, got %q", masked)
	}

	if err := taskapi.ValidateTaskParams(spec, params); err != nil {
		t.Fatalf("expected valid TaskParams, got: %v", err)
	}

	cloned := params.Clone()
	cloned.Options["pool"] = "mutated"
	if s, _ := params.StringOption("pool"); s != "tank" {
		t.Errorf("expected Clone to deep-copy Options map, got %q", s)
	}
}

func TestRegistryExecuteBuiltInAndPluginTasks(t *testing.T) {
	pReg := plugin.NewRegistry()
	tp := &testStoragePlugin{}
	if err := pReg.Register(tp); err != nil {
		t.Fatalf("register plugin: %v", err)
	}

	reg := taskapi.NewRegistry(pReg)
	facts := plugin.HostFacts{NodeID: "node-1", OSFamily: "linux", HostRoute: "100.64.0.1"}

	// 1. Built-in ServiceControl task
	svcRes, err := reg.Execute(context.Background(), facts, taskapi.TaskParams{
		ID:     "svc-1",
		Type:   taskapi.TaskTypeServiceControl,
		NodeID: "node-1",
		Options: map[string]any{
			"service": "sshd.service",
			"state":   "restarted",
		},
	})
	if err != nil || !svcRes.Success || svcRes.Compiled == nil {
		t.Fatalf("expected service_control execution to succeed, got res=%+v err=%v", svcRes, err)
	}
	if len(svcRes.Compiled.Steps) != 1 || svcRes.Compiled.Steps[0].Module != "ansible.builtin.systemd" {
		t.Errorf("unexpected compiled steps for service_control: %+v", svcRes.Compiled.Steps)
	}

	// 2. Plugin-backed StorageOp via TaskCompileProvider
	zfsRes, err := reg.Execute(context.Background(), facts, taskapi.TaskParams{
		ID:       "zfs-1",
		Type:     taskapi.TaskTypeStorageOp,
		NodeID:   "node-1",
		PluginID: "storage/zfs-test",
		Options: map[string]any{
			"pool": "rpool",
		},
	})
	if err != nil || !zfsRes.Success || zfsRes.Compiled == nil {
		t.Fatalf("expected plugin storage_op execution to succeed, got res=%+v err=%v", zfsRes, err)
	}
	if zfsRes.Compiled.Name != "ZFS scrub on rpool" {
		t.Errorf("unexpected compiled task name: %q", zfsRes.Compiled.Name)
	}

	// 3. PluginAction via Actor.Compile
	actRes, err := reg.Execute(context.Background(), facts, taskapi.TaskParams{
		ID:       "act-1",
		Type:     taskapi.TaskTypePluginAction,
		NodeID:   "node-1",
		PluginID: "storage/zfs-test",
		Action:   "scrub",
		Options: map[string]any{
			"pool": "datapool",
		},
	})
	if err != nil || !actRes.Success || actRes.Compiled == nil {
		t.Fatalf("expected plugin_action execution to succeed, got res=%+v err=%v", actRes, err)
	}
	if actRes.Compiled.Name != "Scrub datapool" {
		t.Errorf("unexpected compiled task name: %q", actRes.Compiled.Name)
	}
}

func TestHTTPHandlers(t *testing.T) {
	pReg := plugin.NewRegistry()
	_ = pReg.Register(&testStoragePlugin{})

	api := taskapi.NewAPI(taskapi.APIOptions{
		PluginRegistry: pReg,
	})
	mux := http.NewServeMux()
	api.Register(mux)

	// 1. GET /api/task-types
	req := httptest.NewRequest(http.MethodGet, "/api/task-types", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/task-types returned %d: %s", rec.Code, rec.Body.String())
	}
	var specs []taskapi.TaskTypeSpec
	if err := json.Unmarshal(rec.Body.Bytes(), &specs); err != nil {
		t.Fatalf("decode task types: %v", err)
	}
	if len(specs) < 8 {
		t.Fatalf("expected at least 8 task type specs (7 built-in + 1 plugin), got %d", len(specs))
	}

	// 2. GET /api/task-types/service_control
	reqSingle := httptest.NewRequest(http.MethodGet, "/api/task-types/service_control", nil)
	recSingle := httptest.NewRecorder()
	mux.ServeHTTP(recSingle, reqSingle)
	if recSingle.Code != http.StatusOK {
		t.Fatalf("GET /api/task-types/service_control returned %d", recSingle.Code)
	}

	// 3. POST /api/task-types/execute
	payload, _ := json.Marshal(map[string]any{
		"id":     "exec-http-1",
		"type":   "ansible_playbook",
		"nodeId": "node-web-1",
		"options": map[string]any{
			"playbook": "site.yml",
			"tags":     "nginx",
		},
	})
	reqExec := httptest.NewRequest(http.MethodPost, "/api/task-types/execute", bytes.NewReader(payload))
	recExec := httptest.NewRecorder()
	mux.ServeHTTP(recExec, reqExec)
	if recExec.Code != http.StatusOK {
		t.Fatalf("POST /api/task-types/execute returned %d: %s", recExec.Code, recExec.Body.String())
	}
	var result taskapi.TaskResult
	if err := json.Unmarshal(recExec.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode TaskResult: %v", err)
	}
	if !result.Success || result.Compiled == nil || result.Compiled.Name != "Run playbook site.yml" {
		t.Errorf("unexpected execution result: %+v", result)
	}

	// 4. POST /api/task-types/execute with missing required param -> 400
	badPayload, _ := json.Marshal(map[string]any{
		"type":   "ansible_playbook",
		"nodeId": "node-web-1",
	})
	reqBad := httptest.NewRequest(http.MethodPost, "/api/task-types/execute", bytes.NewReader(badPayload))
	recBad := httptest.NewRecorder()
	mux.ServeHTTP(recBad, reqBad)
	if recBad.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing required param, got %d", recBad.Code)
	}
}
