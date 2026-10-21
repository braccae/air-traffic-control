package sql_test

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

func TestSQLPlugin_Registered(t *testing.T) {
	p, ok := plugin.Get("database/sql")
	if !ok {
		t.Fatal("database/sql plugin not registered")
	}
	meta := p.Meta()
	if meta.Kind != plugin.KindDatabase || meta.Name != "sql" {
		t.Errorf("unexpected meta: %+v", meta)
	}
	if _, ok := p.(plugin.Reader); !ok {
		t.Error("database/sql must implement plugin.Reader")
	}
	if _, ok := p.(plugin.Actor); !ok {
		t.Error("database/sql must implement plugin.Actor")
	}
}

func TestSQLPlugin_Probe(t *testing.T) {
	p, _ := plugin.Get("database/sql")

	// 1. None detected
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

	// 2. PostgreSQL and MariaDB detected
	runner = &mockRunner{
		responses: map[string]string{
			"command -v": "/usr/bin/psql\n/usr/bin/mysql",
			"pgrep":      "1024 postgres\n2048 mysqld",
		},
	}
	det, err = p.Probe(context.Background(), runner, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !det.Detected {
		t.Fatalf("expected detected=true")
	}
	if !strings.Contains(det.Status, "postgres") || !strings.Contains(det.Status, "mariadb") {
		t.Errorf("expected status to mention detected engines, got: %s", det.Status)
	}
}

func TestSQLPlugin_Read(t *testing.T) {
	p, _ := plugin.Get("database/sql")
	reader := p.(plugin.Reader)

	runner := &mockRunner{
		responses: map[string]string{
			"command -v psql": "/usr/bin/psql",
			"command -v mariadb": "/usr/bin/mariadb",
			"SELECT version();": "PostgreSQL 16.2 on x86_64-pc-linux-gnu",
			"SELECT datname FROM pg_database": "postgres\napp_db\ntemplate0",
			"SELECT usename, usesuper, usecreatedb": "postgres|t|t\napp_user|f|t",
			"SELECT count(*) FROM pg_stat_activity": "5",
			"SELECT VERSION();": "11.2.2-MariaDB",
			"SHOW DATABASES;": "information_schema\nmysql\nproduction_db",
			"SELECT User, Host FROM mysql.user;": "root localhost\napp %",
			"Threads_connected": "Threads_connected 12",
		},
	}

	res, err := reader.Read(context.Background(), runner, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}

	data, ok := res.(map[string]any)
	if !ok {
		t.Fatalf("unexpected read result format: %+v", res)
	}
	if data["count"].(int) < 2 {
		t.Errorf("expected at least 2 engines in read result, got: %d", data["count"])
	}
}

func TestSQLPlugin_Actor_Compile(t *testing.T) {
	p, _ := plugin.Get("database/sql")
	actor := p.(plugin.Actor)

	// Test 1: Postgres Query
	task, err := actor.Compile(plugin.HostFacts{OSFamily: "linux"}, "query", map[string]any{
		"engine":   "postgres",
		"database": "app_db",
		"query":    "SELECT * FROM users;",
	})
	if err != nil {
		t.Fatalf("compile query failed: %v", err)
	}
	if len(task.Steps) != 1 || task.Steps[0].Module != "community.postgresql.postgresql_query" {
		t.Errorf("unexpected task step for postgres query: %+v", task)
	}

	// Test 2: MariaDB Database Create
	task, err = actor.Compile(plugin.HostFacts{OSFamily: "linux"}, "database_create", map[string]any{
		"engine": "mariadb",
		"name":   "analytics_db",
	})
	if err != nil {
		t.Fatalf("compile database_create failed: %v", err)
	}
	if len(task.Steps) != 1 || task.Steps[0].Module != "community.mysql.mysql_db" {
		t.Errorf("unexpected task step for mariadb database_create: %+v", task)
	}

	// Test 3: User create with secrets masking
	task, err = actor.Compile(plugin.HostFacts{OSFamily: "linux"}, "user_create", map[string]any{
		"engine":           "postgres",
		"username":         "dbadmin",
		"password":         "supersecret123",
		"passwordVaultKey": "vault_pg_pw",
		"roles":            "SUPERUSER,CREATEDB",
	})
	if err != nil {
		t.Fatalf("compile user_create failed: %v", err)
	}
	if !strings.Contains(task.SecretsMasked["db_password"], "vault_pg_pw") {
		t.Errorf("expected vault key in masked secrets, got: %+v", task.SecretsMasked)
	}
}
