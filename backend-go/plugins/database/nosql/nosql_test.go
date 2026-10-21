package nosql_test

import (
	"context"
	"strings"
	"testing"

	"air-traffic-control/backend/pkg/plugin"
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
