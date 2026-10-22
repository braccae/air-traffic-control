package nosql_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"air-traffic-control/backend/pkg/plugin"
	"air-traffic-control/backend/pkg/plugin/newapi"
	"air-traffic-control/backend/pkg/plugin/settingsframework"
	_ "air-traffic-control/backend/plugins/all"
)

type mockRunner struct {
	responses map[string]string
}

func (m *mockRunner) RunCommand(ctx context.Context, cmd string) (string, error) {
	for pattern, resp := range m.responses {
		if strings.Contains(cmd, pattern) {
			return resp, nil
		}
	}
	return "", nil
}

func TestNoSQLPlugin_Registered(t *testing.T) {
	p, ok := plugin.Get("database/nosql")
	if !ok {
		t.Fatal("database/nosql plugin not registered")
	}
	meta := p.Meta()
	if meta.Kind != plugin.KindDatabase || meta.Name != "nosql" {
		t.Errorf("unexpected meta: %+v", meta)
	}
	if _, ok := p.(plugin.Reader); !ok {
		t.Error("database/nosql must implement plugin.Reader")
	}
	if _, ok := p.(plugin.Actor); !ok {
		t.Error("database/nosql must implement plugin.Actor")
	}
}

func TestNoSQLPlugin_Probe(t *testing.T) {
	p, _ := plugin.Get("database/nosql")

	// 1. Neither engine present
	runner := &mockRunner{
		responses: map[string]string{
			"command -v": "",
			"pgrep":      "",
		},
	}
	det, err := p.Probe(context.Background(), runner, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if det.Detected {
		t.Errorf("expected detected=false when no engines present")
	}

	// 2. Redis & MongoDB detected
	runner = &mockRunner{
		responses: map[string]string{
			"command -v": "/usr/bin/redis-cli\n/usr/bin/mongosh",
			"pgrep":      "1024 redis-server\n2048 mongod",
		},
	}
	det, err = p.Probe(context.Background(), runner, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !det.Detected {
		t.Fatalf("expected detected=true")
	}
	if !strings.Contains(det.Status, "mongodb") || !strings.Contains(det.Status, "redis") {
		t.Errorf("expected status to mention detected engines, got: %s", det.Status)
	}
}

func TestNoSQLPlugin_Read(t *testing.T) {
	p, _ := plugin.Get("database/nosql")
	reader := p.(plugin.Reader)

	runner := &mockRunner{
		responses: map[string]string{
			"command -v redis-cli": "/usr/bin/redis-cli",
			"command -v mongosh":   "/usr/bin/mongosh",
			"INFO": "redis_version:7.2.4\nused_memory_human:14.2M\nconnected_clients:5\ndb0:keys=100\ndb1:keys=20",
			"ACL LIST": "user default on #hash ~* +@all\nuser app on #hash ~app:* +@read",
			"listDatabases": `{"databases":[{"name":"admin","sizeOnDisk":1048576},{"name":"app_data","sizeOnDisk":20971520}],"ok":1}`,
			"getUsers": `[{"user":"admin","db":"admin","roles":[{"role":"root","db":"admin"}]},{"user":"app","db":"app_data","roles":[{"role":"readWrite","db":"app_data"}]}]`,
			"db.version()": "7.0.5",
		},
	}

	res, err := reader.Read(context.Background(), runner, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}

	data, ok := res.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any, got %T", data)
	}
	count, _ := data["count"].(int)
	if count < 2 {
		t.Errorf("expected at least 2 engines inspected, got %d", count)
	}
}

func TestNoSQLPlugin_ActionsAndCompile(t *testing.T) {
	p, _ := plugin.Get("database/nosql")
	actor := p.(plugin.Actor)

	actions := actor.Actions()
	if len(actions) < 5 {
		t.Errorf("expected at least 5 actions, got %d", len(actions))
	}

	// 1. Query Redis
	task, err := actor.Compile(plugin.HostFacts{OSFamily: "linux"}, "query", map[string]any{
		"engine":  "redis",
		"command": "GET test_key",
	})
	if err != nil {
		t.Fatalf("compile redis query failed: %v", err)
	}
	if len(task.Steps) != 1 || !strings.Contains(task.Steps[0].Params["cmd"].(string), "redis-cli GET test_key") {
		t.Errorf("unexpected task step: %+v", task.Steps)
	}

	// 2. Query MongoDB
	task, err = actor.Compile(plugin.HostFacts{OSFamily: "linux"}, "query", map[string]any{
		"engine":   "mongodb",
		"database": "app_data",
		"command":  "db.users.find().limit(5)",
	})
	if err != nil {
		t.Fatalf("compile mongo query failed: %v", err)
	}
	if len(task.Steps) != 1 || !strings.Contains(task.Steps[0].Params["cmd"].(string), "mongosh") {
		t.Errorf("unexpected task step: %+v", task.Steps)
	}

	// 3. Query CouchDB
	task, err = actor.Compile(plugin.HostFacts{OSFamily: "linux"}, "query", map[string]any{
		"engine":  "couchdb",
		"command": "/_all_dbs",
	})
	if err != nil {
		t.Fatalf("compile couchdb query failed: %v", err)
	}
	if task.Steps[0].Module != "ansible.builtin.uri" {
		t.Errorf("expected ansible.builtin.uri for CouchDB, got %s", task.Steps[0].Module)
	}

	// 4. User Create with Password Vault Masking
	task, err = actor.Compile(plugin.HostFacts{OSFamily: "linux"}, "user_create", map[string]any{
		"engine":           "mongodb",
		"username":         "dba_user",
		"database":         "admin",
		"roles":            "root",
		"passwordVaultKey": "mongo_dba_password",
	})
	if err != nil {
		t.Fatalf("compile user_create failed: %v", err)
	}
	if task.Steps[0].Module != "community.mongodb.mongodb_user" {
		t.Errorf("expected community.mongodb.mongodb_user, got %s", task.Steps[0].Module)
	}
	if task.SecretsMasked["db_password"] != "•••••••• (vault: mongo_dba_password)" {
		t.Errorf("expected masked secret for db_password, got %q", task.SecretsMasked["db_password"])
	}

	// 5. Redis ACL delete
	task, err = actor.Compile(plugin.HostFacts{OSFamily: "linux"}, "user_delete", map[string]any{
		"engine":   "redis",
		"username": "temp_user",
	})
	if err != nil {
		t.Fatalf("compile user_delete failed: %v", err)
	}
	if !strings.Contains(task.Steps[0].Params["cmd"].(string), "ACL DELUSER") {
		t.Errorf("expected ACL DELUSER in command, got %v", task.Steps[0].Params["cmd"])
	}
}

func TestNoSQLPlugin_NewAPIInterfacesAndSchemas(t *testing.T) {
	p, ok := plugin.Get("database/nosql")
	if !ok {
		t.Fatalf("database/nosql not registered in DefaultRegistry")
	}

	// 1. DataCollectionPlugin
	dc, ok := p.(newapi.DataCollectionPlugin)
	if !ok {
		t.Fatalf("database/nosql does not implement newapi.DataCollectionPlugin")
	}
	sched := dc.CollectionSchedule()
	if sched.Interval != 60*time.Second || sched.TTL != 60*time.Second {
		t.Errorf("unexpected collection schedule for database/nosql: %+v", sched)
	}
	schema := dc.CollectionSchema()
	if schema.Version != "v2" || schema.RootKey != "engines" {
		t.Errorf("unexpected collection schema: %+v", schema)
	}

	// 2. ManagementPlugin & action parity
	mgmt, ok := p.(newapi.ManagementPlugin)
	if !ok {
		t.Fatalf("database/nosql does not implement newapi.ManagementPlugin")
	}
	mgmtActions := mgmt.ManagementActions()
	legacyActions := p.(plugin.Actor).Actions()
	if len(mgmtActions) != 5 || len(legacyActions) != len(mgmtActions) {
		t.Fatalf("expected 5 management actions matching legacy Actions(), got mgmt=%d legacy=%d", len(mgmtActions), len(legacyActions))
	}
	for i, ma := range mgmtActions {
		if ma.Name != legacyActions[i].Name {
			t.Errorf("action[%d] name mismatch: %q vs %q", i, ma.Name, legacyActions[i].Name)
		}
		if (ma.Name == "database_drop" || ma.Name == "user_delete") && (!ma.Dangerous || ma.ConfirmPrompt == "") {
			t.Errorf("expected %s to be marked dangerous with a confirmation prompt: %+v", ma.Name, ma)
		}
	}

	facts := plugin.HostFacts{NodeID: "node1", OSFamily: "linux"}
	if err := mgmt.ValidateAction(facts, "query", map[string]any{
		"engine":  "redis",
		"command": "GET key1",
	}); err != nil {
		t.Errorf("expected valid query options to pass ValidateAction, got %v", err)
	}
	if err := mgmt.ValidateAction(facts, "query", map[string]any{
		"engine": "redis",
	}); err == nil {
		t.Errorf("expected missing required command option to fail ValidateAction")
	}
	if err := mgmt.ValidateAction(facts, "database_create", map[string]any{
		"engine": "invalid_engine",
		"name":   "testdb",
	}); err == nil {
		t.Errorf("expected invalid engine enum to fail ValidateAction")
	}
	if err := mgmt.ValidateAction(facts, "unknown_action", nil); err == nil {
		t.Errorf("expected unknown action to fail ValidateAction")
	}

	// 3. CategorizedSettingsProvider & settingsframework validation
	csp, ok := p.(newapi.CategorizedSettingsProvider)
	if !ok {
		t.Fatalf("database/nosql does not implement newapi.CategorizedSettingsProvider")
	}
	catPage := csp.CategorizedSettingsPage()
	if err := settingsframework.ValidateSchema(catPage.SettingsPage); err != nil {
		t.Fatalf("ValidateSchema failed for database/nosql settings page: %v", err)
	}
	if err := newapi.ValidateCategorizedSettingsPage(catPage); err != nil {
		t.Fatalf("ValidateCategorizedSettingsPage failed for database/nosql: %v", err)
	}
	if catPage.ID != "database/nosql" || catPage.Scope != newapi.SettingsScopeDatacenter || catPage.Category != "database" {
		t.Errorf("unexpected categorized settings page metadata: %+v", catPage)
	}
	if _, ok := plugin.DefaultRegistry().GetSettingsPage("database/nosql"); !ok {
		t.Errorf("expected database/nosql settings page to be indexed in DefaultRegistry")
	}

	// 4. ContextMenuProvider
	cmp, ok := p.(newapi.ContextMenuProvider)
	if !ok {
		t.Fatalf("database/nosql does not implement newapi.ContextMenuProvider")
	}
	entries := cmp.ContextMenuEntries(context.Background(), facts)
	if len(entries) != 5 {
		t.Fatalf("expected 5 context menu entries for compatible linux host, got %d (%+v)", len(entries), entries)
	}
	for _, entry := range entries {
		if entry.PluginID != "database/nosql" || entry.Target != newapi.TargetDatabase {
			t.Errorf("unexpected context menu entry metadata: %+v", entry)
		}
	}
	if winEntries := cmp.ContextMenuEntries(context.Background(), plugin.HostFacts{OSFamily: "windows"}); len(winEntries) != 0 {
		t.Errorf("expected nil/empty context menu entries for incompatible windows host, got %+v", winEntries)
	}

	// 5. TaskCompileProvider
	tcp, ok := p.(newapi.TaskCompileProvider)
	if !ok {
		t.Fatalf("database/nosql does not implement newapi.TaskCompileProvider")
	}
	compiled, err := tcp.CompileTask(context.Background(), facts, newapi.TaskRequest{
		Type:     newapi.TaskTypePluginAction,
		PluginID: "database/nosql",
		Action:   "database_create",
		NodeID:   "node1",
		Params: map[string]any{
			"engine": "mongodb",
			"name":   "events",
		},
	})
	if err != nil {
		t.Fatalf("CompileTask failed: %v", err)
	}
	if len(compiled.Steps) != 1 || compiled.Steps[0].Module != "community.mongodb.mongodb_collection" {
		t.Errorf("unexpected CompileTask steps: %+v", compiled.Steps)
	}
	if _, err := tcp.CompileTask(context.Background(), facts, newapi.TaskRequest{
		Type:     newapi.TaskTypePluginAction,
		PluginID: "database/nosql",
		NodeID:   "node1",
	}); err == nil {
		t.Errorf("expected CompileTask with empty action to return an error")
	}
}
