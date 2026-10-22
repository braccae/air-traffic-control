package sql_test

import (
	"context"
	"strings"
	"testing"

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

func TestSQLPlugin_V2InterfacesAndCapabilities(t *testing.T) {
	p, ok := plugin.Get("database/sql")
	if !ok {
		t.Fatalf("database/sql not registered")
	}

	// 1. Verify DataCollectionPlugin
	dc, ok := p.(newapi.DataCollectionPlugin)
	if !ok {
		t.Fatal("expected database/sql to implement newapi.DataCollectionPlugin")
	}
	sched := dc.CollectionSchedule()
	if sched.Interval <= 0 || sched.TTL <= 0 {
		t.Errorf("expected positive collection schedule interval and TTL, got %+v", sched)
	}
	schema := dc.CollectionSchema()
	if schema.Version != "v2" || schema.RootKey != "engines" {
		t.Errorf("unexpected collection schema: %+v", schema)
	}

	// 2. Verify CategorizedSettingsProvider
	csp, ok := p.(newapi.CategorizedSettingsProvider)
	if !ok {
		t.Fatal("expected database/sql to implement newapi.CategorizedSettingsProvider")
	}
	catPage := csp.CategorizedSettingsPage()
	if catPage.ID != "database/sql" || catPage.Category != "database" || catPage.Scope != newapi.SettingsScopeDatacenter {
		t.Errorf("unexpected categorized settings page: %+v", catPage)
	}
	if err := settingsframework.ValidateSchema(catPage.SettingsPage); err != nil {
		t.Errorf("ValidateSchema failed: %v", err)
	}
	if err := newapi.ValidateCategorizedSettingsPage(catPage); err != nil {
		t.Errorf("ValidateCategorizedSettingsPage failed: %v", err)
	}
	if _, ok := plugin.GetCategorizedSettingsPage("database/sql"); !ok {
		t.Error("expected database/sql categorized settings page to be registered in global registry")
	}

	// 3. Verify ManagementPlugin
	mgmt, ok := p.(newapi.ManagementPlugin)
	if !ok {
		t.Fatal("expected database/sql to implement newapi.ManagementPlugin")
	}
	mgmtActions := mgmt.ManagementActions()
	if len(mgmtActions) != 5 {
		t.Fatalf("expected 5 management actions, got %d", len(mgmtActions))
	}
	if len(mgmt.Actions()) != len(mgmtActions) {
		t.Errorf("Actions() length %d does not match ManagementActions() length %d", len(mgmt.Actions()), len(mgmtActions))
	}

	linuxFacts := plugin.HostFacts{NodeID: "node-1", OSFamily: "linux"}
	if err := mgmt.ValidateAction(linuxFacts, "query", map[string]any{
		"engine": "postgres",
		"query":  "SELECT 1;",
	}); err != nil {
		t.Errorf("expected valid query options to pass validation, got: %v", err)
	}
	if err := mgmt.ValidateAction(linuxFacts, "user_delete", map[string]any{
		"engine":   "mariadb",
		"username": "old_user",
	}); err != nil {
		t.Errorf("expected valid user_delete alias options to pass validation, got: %v", err)
	}
	if err := mgmt.ValidateAction(linuxFacts, "database_create", map[string]any{
		"engine": "postgres",
	}); err == nil {
		t.Error("expected missing required name option to fail validation")
	}
	if err := mgmt.ValidateAction(linuxFacts, "database_create", map[string]any{
		"engine": "sqlite",
		"name":   "testdb",
	}); err == nil {
		t.Error("expected invalid engine enum to fail validation")
	}
	if err := mgmt.ValidateAction(linuxFacts, "unknown_action", map[string]any{}); err == nil {
		t.Error("expected unknown action to fail validation")
	}

	// 4. Verify ContextMenuProvider
	cmp, ok := p.(newapi.ContextMenuProvider)
	if !ok {
		t.Fatal("expected database/sql to implement newapi.ContextMenuProvider")
	}
	entries := cmp.ContextMenuEntries(context.Background(), linuxFacts)
	if len(entries) != 5 {
		t.Fatalf("expected 5 context menu entries for compatible linux host, got %d", len(entries))
	}
	for _, entry := range entries {
		if entry.PluginID != "database/sql" || entry.Target != newapi.TargetDatabase {
			t.Errorf("unexpected context menu entry metadata: %+v", entry)
		}
	}
	if !entries[len(entries)-1].Danger || entries[len(entries)-1].ConfirmPrompt == "" {
		t.Errorf("expected last context menu entry to be a danger action with confirmation prompt, got %+v", entries[len(entries)-1])
	}
	if routerEntries := cmp.ContextMenuEntries(context.Background(), plugin.HostFacts{OSFamily: "router_switch"}); len(routerEntries) != 0 {
		t.Errorf("expected no context menu entries for incompatible router_switch host, got %+v", routerEntries)
	}

	// 5. Verify TaskCompileProvider
	tcp, ok := p.(newapi.TaskCompileProvider)
	if !ok {
		t.Fatal("expected database/sql to implement newapi.TaskCompileProvider")
	}
	compiled, err := tcp.CompileTask(context.Background(), linuxFacts, newapi.TaskRequest{
		ID:       "req-sql-1",
		Type:     newapi.TaskTypePluginAction,
		NodeID:   "node-1",
		PluginID: "database/sql",
		Action:   "database_drop",
		Params: map[string]any{
			"engine": "postgres",
			"name":   "legacy_db",
		},
	})
	if err != nil {
		t.Fatalf("CompileTask failed: %v", err)
	}
	if len(compiled.Steps) != 1 || compiled.Steps[0].Module != "community.postgresql.postgresql_db" || compiled.Steps[0].Params["state"] != "absent" {
		t.Errorf("unexpected compiled task steps: %+v", compiled.Steps)
	}
}
