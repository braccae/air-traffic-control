package sql

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"air-traffic-control/backend/pkg/plugin"
	"air-traffic-control/backend/pkg/plugin/newapi"
	"air-traffic-control/backend/pkg/plugin/settingsframework"
)

const SettingsPageID = "database/sql"

var (
	_ plugin.Plugin                      = (*Plugin)(nil)
	_ plugin.Reader                      = (*Plugin)(nil)
	_ plugin.Actor                       = (*Plugin)(nil)
	_ plugin.SettingsPageProvider        = (*Plugin)(nil)
	_ newapi.DataCollectionPlugin        = (*Plugin)(nil)
	_ newapi.ManagementPlugin            = (*Plugin)(nil)
	_ newapi.CategorizedSettingsProvider = (*Plugin)(nil)
	_ newapi.ContextMenuProvider         = (*Plugin)(nil)
	_ newapi.TaskCompileProvider         = (*Plugin)(nil)
)

func init() {
	_ = plugin.Register(New())
}

// Plugin implements SQL database inspection, administration, and querying
// across PostgreSQL, MariaDB/MySQL, and MSSQL.
type Plugin struct{}

// New constructs a new SQL plugin instance.
func New() *Plugin { return &Plugin{} }

func (p *Plugin) Meta() plugin.Meta {
	return plugin.Meta{
		ID:          "database/sql",
		Kind:        plugin.KindDatabase,
		Name:        "sql",
		Label:       "SQL Databases",
		Icon:        "database",
		Description: "SQL database management & query workbench: PostgreSQL, MariaDB/MySQL, MSSQL",
		Order:       9,
	}
}

func (p *Plugin) Compatible(h plugin.HostFacts) bool {
	return h.OSFamily != "router_switch"
}

func (p *Plugin) Requirements() plugin.Requirements {
	return plugin.Requirements{
		HostPackages: map[string][]string{
			"dnf": {"postgresql", "mariadb", "mysql"},
			"apt": {"postgresql-client", "mariadb-client", "mysql-client"},
			"apk": {"postgresql-client", "mariadb-client"},
		},
		ControllerCollections: []string{
			"community.postgresql",
			"community.mysql",
			"community.general",
		},
	}
}

// CollectionSchedule returns the standard polling and cache TTL schedule for SQL database telemetry.
func (p *Plugin) CollectionSchedule() newapi.CollectionSchedule {
	return newapi.DefaultScheduleForKind(p.Meta().Kind)
}

// CollectionSchema documents the payload returned by Read.
func (p *Plugin) CollectionSchema() newapi.CollectionSchema {
	return newapi.CollectionSchema{
		Version:     "v2",
		RootKey:     "engines",
		Description: p.Meta().Description,
	}
}

// CategorizedSettingsPage declares datacenter-wide SQL database defaults via settingsframework.
func (p *Plugin) CategorizedSettingsPage() newapi.CategorizedSettingsPage {
	meta := p.Meta()
	return settingsframework.NewPageBuilder(meta.ID, "SQL Database Defaults").
		WithIcon(meta.Icon).
		WithCategory(string(meta.Kind)).
		WithScope(newapi.SettingsScopeDatacenter).
		WithOrder(meta.Order).
		WithDescription("Datacenter-wide PostgreSQL, MariaDB/MySQL, and MSSQL query workbench and administration defaults.").
		SelectField(
			"defaultEngine",
			"Default SQL Engine",
			"postgres",
			[]plugin.SettingsFieldOption{
				{Value: "postgres", Label: "PostgreSQL"},
				{Value: "mariadb", Label: "MariaDB / MySQL"},
				{Value: "mssql", Label: "Microsoft SQL Server"},
			},
			settingsframework.WithDescription("Default SQL engine when none is explicitly specified in an action."),
		).
		IntField(
			"defaultQueryLimit",
			"Default Query Row Limit",
			50,
			settingsframework.WithMinMax(1, 10000),
			settingsframework.WithDescription("Default maximum number of rows returned by ad-hoc SQL queries."),
		).
		StringField(
			"defaultEncoding",
			"Default Database Encoding",
			"UTF8",
			settingsframework.WithPlaceholder("UTF8"),
			settingsframework.WithDescription("Default character encoding used when creating new databases."),
		).
		SecretField(
			"adminPasswordVaultVar",
			"Default Admin Password Vault Variable",
			settingsframework.WithPlaceholder("vault_sql_admin_password"),
			settingsframework.WithDescription("Optional Ansible Vault variable name holding the default SQL administrator password."),
		).
		BuildCategorized()
}

// SettingsPage returns the underlying global SettingsPage schema.
func (p *Plugin) SettingsPage() plugin.SettingsPage {
	return p.CategorizedSettingsPage().SettingsPage
}

// Probe checks for active SQL database engines (PostgreSQL, MariaDB/MySQL, MSSQL) on the host.
func (p *Plugin) Probe(ctx context.Context, r plugin.Runner, h plugin.HostFacts) (plugin.Detection, error) {
	if r == nil {
		return plugin.Detection{Detected: false, Status: "OFFLINE", Error: "runner is nil"}, nil
	}

	pgPort := firstNonEmpty(h.Overrides["postgres_port"], "5432")
	pgSocket := h.Overrides["postgres_socket"]
	mariaPort := firstNonEmpty(h.Overrides["mariadb_port"], h.Overrides["mysql_port"], "3306")
	mariaSocket := firstNonEmpty(h.Overrides["mariadb_socket"], h.Overrides["mysql_socket"])
	mssqlPort := firstNonEmpty(h.Overrides["mssql_port"], "1433")
	mssqlSocket := h.Overrides["mssql_socket"]

	detectedEngines := make([]string, 0)

	// 1. Probe running SQL server processes (without -f or -a so the subshell never matches itself)
	psOut, err := r.RunCommand(ctx, "pgrep -l '^(postgres|postmaster|mysqld|mariadbd|sqlservr)$' 2>/dev/null || true")
	if err != nil {
		return plugin.Detection{Detected: false, Status: "ERROR", Error: err.Error()}, nil
	}
	psLower := strings.ToLower(psOut)
	if strings.Contains(psLower, "postgres") || strings.Contains(psLower, "postmaster") {
		if !contains(detectedEngines, "postgres") {
			detectedEngines = append(detectedEngines, "postgres")
		}
	}
	hasMysqld := strings.Contains(psLower, "mysqld")
	hasMariadbd := strings.Contains(psLower, "mariadbd")
	if hasMysqld && !contains(detectedEngines, "mariadb") {
		detectedEngines = append(detectedEngines, "mariadb")
	} else if hasMariadbd && !contains(detectedEngines, "mariadb") {
		// Verify mariadbd is not solely KDE Akonadi private user session
		akOut, _ := r.RunCommand(ctx, "pgrep -a '^mariadbd$' 2>/dev/null || true")
		nonAkonadi := false
		if strings.TrimSpace(akOut) == "" {
			// Mock runner or system where pgrep -a is empty -> accept the pgrep -l match
			nonAkonadi = true
		} else {
			for _, line := range strings.Split(strings.TrimSpace(akOut), "\n") {
				if strings.TrimSpace(line) != "" && !strings.Contains(strings.ToLower(line), "akonadi") {
					nonAkonadi = true
					break
				}
			}
		}
		if nonAkonadi {
			detectedEngines = append(detectedEngines, "mariadb")
		}
	}
	if strings.Contains(psLower, "sqlservr") && !contains(detectedEngines, "mssql") {
		detectedEngines = append(detectedEngines, "mssql")
	}

	// 2. Probe Unix sockets and listening TCP ports
	sockProbeCmd := fmt.Sprintf(
		"for s in %s /run/postgresql/.s.PGSQL.%s /var/run/postgresql/.s.PGSQL.%s /run/postgres/.s.PGSQL.%s /tmp/.s.PGSQL.%s; do [ -n \"$s\" ] && [ -S \"$s\" ] && echo \"PG_SOCK:$s\" && break; done; "+
			"for s in %s /run/mysqld/mysqld.sock /var/run/mysqld/mysqld.sock /var/lib/mysql/mysql.sock /run/mariadb/mariadb.sock /tmp/mysql.sock; do [ -n \"$s\" ] && [ -S \"$s\" ] && echo \"MARIA_SOCK:$s\" && break; done; "+
			"for s in %s /var/opt/mssql/.system/mssql.sock; do [ -n \"$s\" ] && [ -S \"$s\" ] && echo \"MSSQL_SOCK:$s\" && break; done; "+
			"echo '===SS==='; ss -ltn 2>/dev/null || true",
		escapeArg(pgSocket), pgPort, pgPort, pgPort, pgPort,
		escapeArg(mariaSocket),
		escapeArg(mssqlSocket),
	)
	sockOut, _ := r.RunCommand(ctx, sockProbeCmd)
	parts := strings.Split(sockOut, "===SS===")
	sockPart := parts[0]
	ssPart := ""
	if len(parts) > 1 {
		ssPart = parts[1]
	}

	if strings.Contains(sockPart, "PG_SOCK:") || hasListeningPort(ssPart, pgPort) {
		if !contains(detectedEngines, "postgres") {
			detectedEngines = append(detectedEngines, "postgres")
		}
	}
	if strings.Contains(sockPart, "MARIA_SOCK:") || hasListeningPort(ssPart, mariaPort) {
		if !contains(detectedEngines, "mariadb") {
			detectedEngines = append(detectedEngines, "mariadb")
		}
	}
	if strings.Contains(sockPart, "MSSQL_SOCK:") || hasListeningPort(ssPart, mssqlPort) {
		if !contains(detectedEngines, "mssql") {
			detectedEngines = append(detectedEngines, "mssql")
		}
	}

	// 3. Also check explicit host overrides
	if eng, ok := h.Overrides["sql_engine"]; ok && eng != "" && !contains(detectedEngines, eng) {
		detectedEngines = append(detectedEngines, eng)
	}
	if (h.Overrides["postgres_host"] != "" || h.Overrides["postgres_port"] != "" || h.Overrides["postgres_socket"] != "") && !contains(detectedEngines, "postgres") {
		detectedEngines = append(detectedEngines, "postgres")
	}
	if (h.Overrides["mariadb_host"] != "" || h.Overrides["mariadb_port"] != "" || h.Overrides["mariadb_socket"] != "" || h.Overrides["mysql_port"] != "" || h.Overrides["mysql_socket"] != "") && !contains(detectedEngines, "mariadb") {
		detectedEngines = append(detectedEngines, "mariadb")
	}
	if (h.Overrides["mssql_host"] != "" || h.Overrides["mssql_port"] != "" || h.Overrides["mssql_socket"] != "") && !contains(detectedEngines, "mssql") {
		detectedEngines = append(detectedEngines, "mssql")
	}

	if len(detectedEngines) == 0 {
		return plugin.Detection{
			Detected: false,
			Status:   "UNAVAILABLE",
			Details:  "No supported SQL database engines found (PostgreSQL, MariaDB/MySQL, MSSQL)",
		}, nil
	}

	sort.Strings(detectedEngines)
	return plugin.Detection{
		Detected: true,
		Status:   fmt.Sprintf("ONLINE (%s)", strings.Join(detectedEngines, ", ")),
		Details:  fmt.Sprintf("Engines: %s", strings.Join(detectedEngines, ", ")),
	}, nil
}

// EngineStatus describes an inspected SQL engine on the host.
type EngineStatus struct {
	Engine    string         `json:"engine"`    // "postgres", "mariadb", "mssql"
	Status    string         `json:"status"`    // "active", "inactive", "detected"
	Version   string         `json:"version"`   // version string
	Databases []string       `json:"databases"` // database names
	Users     []EngineUser   `json:"users"`     // database users
	Metrics   map[string]any `json:"metrics"`   // runtime stats/metrics
}

// EngineUser represents a database user account.
type EngineUser struct {
	Username string   `json:"username"`
	Roles    []string `json:"roles"`
	Database string   `json:"database,omitempty"`
}

// Read inspects live status, databases, and users for present SQL engines.
func (p *Plugin) Read(ctx context.Context, r plugin.Runner, h plugin.HostFacts) (any, error) {
	if r == nil {
		return nil, fmt.Errorf("runner is nil")
	}

	engines := make([]EngineStatus, 0)

	det, _ := p.Probe(ctx, r, h)
	detLower := strings.ToLower(det.Status + " " + det.Details)

	// 1. Inspect PostgreSQL
	psqlCli, _ := r.RunCommand(ctx, "command -v psql || true")
	psqlCli = strings.TrimSpace(psqlCli)
	if psqlCli != "" || strings.Contains(detLower, "postgres") || h.Overrides["postgres_host"] != "" || h.Overrides["postgres_port"] != "" || h.Overrides["postgres_socket"] != "" || h.Overrides["postgres_user"] != "" || h.Overrides["postgres_database"] != "" {
		st := inspectPostgres(ctx, r, psqlCli, h, strings.Contains(detLower, "postgres"))
		if st != nil {
			engines = append(engines, *st)
		}
	}

	// 2. Inspect MariaDB / MySQL
	mysqlCli, _ := r.RunCommand(ctx, "command -v mariadb || command -v mysql || true")
	mysqlCli = strings.TrimSpace(mysqlCli)
	if mysqlCli != "" || strings.Contains(detLower, "mariadb") || h.Overrides["mariadb_host"] != "" || h.Overrides["mariadb_port"] != "" || h.Overrides["mariadb_socket"] != "" || h.Overrides["mysql_port"] != "" || h.Overrides["mysql_socket"] != "" || h.Overrides["mariadb_user"] != "" || h.Overrides["mariadb_database"] != "" {
		st := inspectMariaDB(ctx, r, mysqlCli, h, strings.Contains(detLower, "mariadb"))
		if st != nil {
			engines = append(engines, *st)
		}
	}

	// 3. Inspect MSSQL
	mssqlCli, _ := r.RunCommand(ctx, "command -v sqlcmd || true")
	mssqlCli = strings.TrimSpace(mssqlCli)
	if mssqlCli != "" || strings.Contains(detLower, "mssql") || h.Overrides["mssql_host"] != "" || h.Overrides["mssql_port"] != "" || h.Overrides["mssql_socket"] != "" || h.Overrides["mssql_user"] != "" || h.Overrides["mssql_database"] != "" {
		st := inspectMSSQL(ctx, r, mssqlCli, h, strings.Contains(detLower, "mssql"))
		if st != nil {
			engines = append(engines, *st)
		}
	}

	return map[string]any{
		"engines": engines,
		"count":   len(engines),
	}, nil
}

func inspectPostgres(ctx context.Context, r plugin.Runner, cli string, h plugin.HostFacts, detected bool) *EngineStatus {
	port := firstNonEmpty(h.Overrides["postgres_port"], "5432")
	socketOverride := h.Overrides["postgres_socket"]
	metrics := map[string]any{"port": port}
	if socketOverride != "" {
		metrics["socket"] = socketOverride
	}
	if u := h.Overrides["postgres_user"]; u != "" {
		metrics["user"] = u
	}
	if db := h.Overrides["postgres_database"]; db != "" {
		metrics["database"] = db
	}

	pgPwdEnv := ""
	if pwd := h.Overrides["postgres_password"]; pwd != "" {
		pgPwdEnv = fmt.Sprintf("PGPASSWORD=%s ", escapeArg(pwd))
	}

	runPsql := func(sqlQuery string) string {
		if cli != "" {
			opts := buildPostgresFlags(h)
			cmd := fmt.Sprintf("%s%s %s -t -A -c %s 2>/dev/null || true", pgPwdEnv, cli, opts, escapeArg(sqlQuery))
			if out, err := r.RunCommand(ctx, cmd); err == nil && strings.TrimSpace(out) != "" {
				return out
			}
			// Try common Unix socket directories or 127.0.0.1 fallback if default failed
			sockDirs := []string{"/run/postgresql", "/var/run/postgresql", "/run/postgres", "/tmp"}
			if socketOverride != "" {
				if idx := strings.LastIndex(socketOverride, "/"); idx > 0 {
					sockDirs = append([]string{socketOverride[:idx]}, sockDirs...)
				}
			}
			for _, dir := range sockDirs {
				tryCmd := fmt.Sprintf("[ -S %s/.s.PGSQL.%s ] && (sudo -n -u postgres %s%s -h %s -p %s -t -A -c %s 2>/dev/null || %s%s -h %s -p %s -t -A -c %s 2>/dev/null) || true",
					escapeArg(dir), port, pgPwdEnv, cli, escapeArg(dir), port, escapeArg(sqlQuery), pgPwdEnv, cli, escapeArg(dir), port, escapeArg(sqlQuery))
				if out, err := r.RunCommand(ctx, tryCmd); err == nil && strings.TrimSpace(out) != "" {
					metrics["socket"] = fmt.Sprintf("%s/.s.PGSQL.%s", dir, port)
					return out
				}
			}
		}
		// Container exec fallback (Podman / Docker)
		ctrCmd := fmt.Sprintf(
			"for rt in 'sudo -n podman' podman 'sudo -n docker' docker; do "+
				"cid=$($rt ps --format '{{.Names}} {{.Image}}' 2>/dev/null | grep -iE 'postgres|postgis|timescale|pgvector' | awk '{print $1}' | head -n1); "+
				"if [ -n \"$cid\" ]; then $rt exec \"$cid\" sh -c \"psql -U \\${POSTGRES_USER:-postgres} -t -A -c %s\" 2>/dev/null && break; fi; "+
				"done || true",
			escapeArg(sqlQuery),
		)
		out, _ := r.RunCommand(ctx, ctrCmd)
		return out
	}

	verOut := runPsql("SELECT version();")
	if strings.TrimSpace(verOut) == "" {
		status := "inactive"
		if detected {
			status = "active"
		}
		return &EngineStatus{
			Engine:    "postgres",
			Status:    status,
			Databases: []string{},
			Users:     []EngineUser{},
			Metrics:   metrics,
		}
	}

	version := strings.TrimSpace(verOut)
	if idx := strings.Index(version, "\n"); idx != -1 {
		version = version[:idx]
	}

	// List databases
	dbOut := runPsql("SELECT datname FROM pg_database WHERE datistemplate = false;")
	databases := make([]string, 0)
	for _, l := range strings.Split(dbOut, "\n") {
		l = strings.TrimSpace(l)
		if l != "" {
			databases = append(databases, l)
		}
	}
	sort.Strings(databases)

	// List users and attributes
	var userOut string
	if cli != "" {
		opts := buildPostgresFlags(h)
		userCmd := fmt.Sprintf("%s%s %s -t -A -F'|' -c 'SELECT usename, usesuper, usecreatedb FROM pg_user;' 2>/dev/null || true", pgPwdEnv, cli, opts)
		userOut, _ = r.RunCommand(ctx, userCmd)
	}
	if strings.TrimSpace(userOut) == "" {
		userOut = runPsql("SELECT usename || '|' || usesuper || '|' || usecreatedb FROM pg_user;")
	}
	users := make([]EngineUser, 0)
	for _, l := range strings.Split(userOut, "\n") {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		parts := strings.Split(l, "|")
		u := EngineUser{Username: parts[0], Roles: make([]string, 0)}
		if len(parts) > 1 && (parts[1] == "t" || parts[1] == "true") {
			u.Roles = append(u.Roles, "superuser")
		}
		if len(parts) > 2 && (parts[2] == "t" || parts[2] == "true") {
			u.Roles = append(u.Roles, "createdb")
		}
		users = append(users, u)
	}

	// Metrics: connections
	if statOut := runPsql("SELECT count(*) FROM pg_stat_activity;"); strings.TrimSpace(statOut) != "" {
		metrics["active_connections"] = strings.TrimSpace(statOut)
	}

	return &EngineStatus{
		Engine:    "postgres",
		Status:    "active",
		Version:   version,
		Databases: databases,
		Users:     users,
		Metrics:   metrics,
	}
}

func inspectMariaDB(ctx context.Context, r plugin.Runner, cli string, h plugin.HostFacts, detected bool) *EngineStatus {
	port := firstNonEmpty(h.Overrides["mariadb_port"], h.Overrides["mysql_port"], "3306")
	socketOverride := firstNonEmpty(h.Overrides["mariadb_socket"], h.Overrides["mysql_socket"])
	metrics := map[string]any{"port": port}
	if socketOverride != "" {
		metrics["socket"] = socketOverride
	}
	if u := firstNonEmpty(h.Overrides["mariadb_user"], h.Overrides["mysql_user"]); u != "" {
		metrics["user"] = u
	}
	if db := firstNonEmpty(h.Overrides["mariadb_database"], h.Overrides["mysql_database"]); db != "" {
		metrics["database"] = db
	}

	runMysql := func(sqlQuery string) string {
		if cli != "" {
			opts := buildMariaDBFlags(h)
			cmd := fmt.Sprintf("%s %s -s -N -e %s 2>/dev/null || true", cli, opts, escapeArg(sqlQuery))
			if out, err := r.RunCommand(ctx, cmd); err == nil && strings.TrimSpace(out) != "" {
				return out
			}
			// Try common Unix sockets with sudo/direct fallback
			sockets := []string{"/run/mysqld/mysqld.sock", "/var/run/mysqld/mysqld.sock", "/var/lib/mysql/mysql.sock", "/run/mariadb/mariadb.sock", "/tmp/mysql.sock"}
			if socketOverride != "" {
				sockets = append([]string{socketOverride}, sockets...)
			}
			for _, sock := range sockets {
				tryCmd := fmt.Sprintf("[ -S %s ] && (sudo -n %s -S %s -s -N -e %s 2>/dev/null || %s -S %s -s -N -e %s 2>/dev/null) || true",
					escapeArg(sock), cli, escapeArg(sock), escapeArg(sqlQuery), cli, escapeArg(sock), escapeArg(sqlQuery))
				if out, err := r.RunCommand(ctx, tryCmd); err == nil && strings.TrimSpace(out) != "" {
					metrics["socket"] = sock
					return out
				}
			}
		}
		// Container exec fallback
		ctrCmd := fmt.Sprintf(
			"for rt in 'sudo -n podman' podman 'sudo -n docker' docker; do "+
				"cid=$($rt ps --format '{{.Names}} {{.Image}}' 2>/dev/null | grep -iE 'mariadb|mysql' | awk '{print $1}' | head -n1); "+
				"if [ -n \"$cid\" ]; then $rt exec \"$cid\" sh -c \"(mariadb -u root -p\\\"\\${MARIADB_ROOT_PASSWORD:-\\${MYSQL_ROOT_PASSWORD}}\\\" -s -N -e %s || mysql -u root -p\\\"\\${MARIADB_ROOT_PASSWORD:-\\${MYSQL_ROOT_PASSWORD}}\\\" -s -N -e %s)\" 2>/dev/null && break; fi; "+
				"done || true",
			escapeArg(sqlQuery), escapeArg(sqlQuery),
		)
		out, _ := r.RunCommand(ctx, ctrCmd)
		return out
	}

	verOut := runMysql("SELECT VERSION();")
	if strings.TrimSpace(verOut) == "" {
		status := "inactive"
		if detected {
			status = "active"
		}
		return &EngineStatus{
			Engine:    "mariadb",
			Status:    status,
			Databases: []string{},
			Users:     []EngineUser{},
			Metrics:   metrics,
		}
	}

	version := strings.TrimSpace(verOut)

	// List databases
	dbOut := runMysql("SHOW DATABASES;")
	databases := make([]string, 0)
	for _, l := range strings.Split(dbOut, "\n") {
		l = strings.TrimSpace(l)
		if l != "" {
			databases = append(databases, l)
		}
	}
	sort.Strings(databases)

	// List users
	userOut := runMysql("SELECT User, Host FROM mysql.user;")
	users := make([]EngineUser, 0)
	for _, l := range strings.Split(userOut, "\n") {
		fields := strings.Fields(l)
		if len(fields) >= 1 {
			u := EngineUser{Username: fields[0], Roles: []string{}}
			if len(fields) > 1 {
				u.Roles = append(u.Roles, fmt.Sprintf("host:%s", fields[1]))
			}
			users = append(users, u)
		}
	}

	if thOut := runMysql("SHOW GLOBAL STATUS LIKE 'Threads_connected';"); strings.TrimSpace(thOut) != "" {
		thParts := strings.Fields(thOut)
		if len(thParts) >= 2 {
			metrics["threads_connected"] = thParts[1]
		}
	}

	return &EngineStatus{
		Engine:    "mariadb",
		Status:    "active",
		Version:   version,
		Databases: databases,
		Users:     users,
		Metrics:   metrics,
	}
}

func inspectMSSQL(ctx context.Context, r plugin.Runner, cli string, h plugin.HostFacts, detected bool) *EngineStatus {
	if cli == "" {
		cli = "sqlcmd"
	}
	port := firstNonEmpty(h.Overrides["mssql_port"], "1433")
	socketOverride := h.Overrides["mssql_socket"]
	metrics := map[string]any{"port": port}
	if socketOverride != "" {
		metrics["socket"] = socketOverride
	}
	if u := h.Overrides["mssql_user"]; u != "" {
		metrics["user"] = u
	}
	if db := h.Overrides["mssql_database"]; db != "" {
		metrics["database"] = db
	}
	opts := buildMSSQLFlags(h)

	cmd := fmt.Sprintf("%s %s -h -1 -W -Q 'SELECT @@VERSION;' 2>/dev/null || true", cli, opts)
	verOut, err := r.RunCommand(ctx, cmd)
	if err != nil || strings.TrimSpace(verOut) == "" {
		status := "inactive"
		if detected {
			status = "active"
		}
		return &EngineStatus{
			Engine:    "mssql",
			Status:    status,
			Databases: []string{},
			Users:     []EngineUser{},
			Metrics:   metrics,
		}
	}

	version := strings.TrimSpace(verOut)
	if idx := strings.Index(version, "\n"); idx != -1 {
		version = version[:idx]
	}

	// List databases
	dbCmd := fmt.Sprintf("%s %s -h -1 -W -Q 'SELECT name FROM sys.databases;' 2>/dev/null || true", cli, opts)
	dbOut, _ := r.RunCommand(ctx, dbCmd)
	databases := make([]string, 0)
	for _, l := range strings.Split(dbOut, "\n") {
		l = strings.TrimSpace(l)
		if l != "" && !strings.HasPrefix(l, "-") {
			databases = append(databases, l)
		}
	}
	sort.Strings(databases)

	// List logins / users
	userCmd := fmt.Sprintf("%s %s -h -1 -W -Q 'SELECT name, type_desc FROM sys.server_principals WHERE type IN (\"S\", \"U\");' 2>/dev/null || true", cli, opts)
	userOut, _ := r.RunCommand(ctx, userCmd)
	users := make([]EngineUser, 0)
	for _, l := range strings.Split(userOut, "\n") {
		fields := strings.Fields(l)
		if len(fields) >= 1 && !strings.HasPrefix(fields[0], "-") {
			u := EngineUser{Username: fields[0], Roles: []string{}}
			if len(fields) > 1 {
				u.Roles = append(u.Roles, fields[1])
			}
			users = append(users, u)
		}
	}

	return &EngineStatus{
		Engine:    "mssql",
		Status:    "active",
		Version:   version,
		Databases: databases,
		Users:     users,
		Metrics:   metrics,
	}
}

// ManagementActions returns rich action specifications for SQL database mutations and queries.
func (p *Plugin) ManagementActions() []newapi.ManagementActionSpec {
	return []newapi.ManagementActionSpec{
		{
			ActionSpec: plugin.ActionSpec{
				Name:        "query",
				Label:       "Execute SQL Query",
				Description: "Run raw or parameterized SQL against a chosen database",
				Options: []plugin.OptionSchema{
					{Name: "engine", Type: "select", Required: true, Enum: []string{"postgres", "mariadb", "mssql"}, Description: "Target SQL engine"},
					{Name: "database", Type: "string", Required: false, Description: "Target database"},
					{Name: "query", Type: "string", Required: true, Description: "SQL statement(s) to execute"},
					{Name: "limit", Type: "int", Required: false, Default: 50, Description: "Maximum rows to return"},
				},
			},
			Category:   newapi.ActionCategoryDiagnostic,
			Idempotent: true,
		},
		{
			ActionSpec: plugin.ActionSpec{
				Name:        "database_create",
				Label:       "Create Database",
				Description: "Create a new database on the SQL server",
				Options: []plugin.OptionSchema{
					{Name: "engine", Type: "select", Required: true, Enum: []string{"postgres", "mariadb", "mssql"}, Description: "Target SQL engine"},
					{Name: "name", Type: "string", Required: true, Description: "Database name"},
					{Name: "owner", Type: "string", Required: false, Description: "Database owner user"},
					{Name: "encoding", Type: "string", Required: false, Default: "UTF8", Description: "Character encoding"},
				},
			},
			Category:         newapi.ActionCategoryConfiguration,
			Idempotent:       true,
			InvalidatesKinds: []plugin.Kind{plugin.KindDatabase},
		},
		{
			ActionSpec: plugin.ActionSpec{
				Name:        "database_drop",
				Label:       "Drop Database",
				Description: "Drop an existing database from the SQL server",
				Options: []plugin.OptionSchema{
					{Name: "engine", Type: "select", Required: true, Enum: []string{"postgres", "mariadb", "mssql"}, Description: "Target SQL engine"},
					{Name: "name", Type: "string", Required: true, Description: "Database name to drop"},
				},
			},
			Category:         newapi.ActionCategoryLifecycle,
			Idempotent:       true,
			Dangerous:        true,
			ConfirmPrompt:    "Permanently drop this SQL database and all its data?",
			InvalidatesKinds: []plugin.Kind{plugin.KindDatabase},
		},
		{
			ActionSpec: plugin.ActionSpec{
				Name:        "user_create",
				Label:       "Create / Alter User",
				Description: "Create or alter a database user and assign roles/privileges",
				Options: []plugin.OptionSchema{
					{Name: "engine", Type: "select", Required: true, Enum: []string{"postgres", "mariadb", "mssql"}, Description: "Target SQL engine"},
					{Name: "username", Type: "string", Required: true, Description: "Database username"},
					{Name: "password", Type: "string", Required: false, Secret: true, Description: "User password (secret, vault-masked)"},
					{Name: "passwordVaultKey", Type: "string", Required: false, Description: "Ansible Vault variable name for the password"},
					{Name: "roles", Type: "string", Required: false, Description: "Roles, privileges, or grants (comma-separated)"},
					{Name: "database", Type: "string", Required: false, Description: "Database on which to grant privileges"},
				},
			},
			Category:         newapi.ActionCategorySecurity,
			Idempotent:       true,
			InvalidatesKinds: []plugin.Kind{plugin.KindDatabase},
		},
		{
			ActionSpec: plugin.ActionSpec{
				Name:        "user_drop",
				Label:       "Drop User",
				Description: "Drop a database user from the SQL server",
				Options: []plugin.OptionSchema{
					{Name: "engine", Type: "select", Required: true, Enum: []string{"postgres", "mariadb", "mssql"}, Description: "Target SQL engine"},
					{Name: "username", Type: "string", Required: true, Description: "Database username to drop"},
				},
			},
			Category:         newapi.ActionCategorySecurity,
			Idempotent:       true,
			Dangerous:        true,
			ConfirmPrompt:    "Permanently drop this database user account?",
			InvalidatesKinds: []plugin.Kind{plugin.KindDatabase},
		},
		{
			ActionSpec: plugin.ActionSpec{
				Name:        "database_dump",
				Label:       "Dump Database",
				Description: "Dump an SQL database schema and/or data to a file on the target host",
				Options: []plugin.OptionSchema{
					{Name: "engine", Type: "select", Required: true, Enum: []string{"postgres", "mariadb", "mssql"}, Description: "Target SQL engine"},
					{Name: "database", Type: "string", Required: true, Description: "Database name to dump"},
					{Name: "format", Type: "string", Required: false, Default: "plain", Description: "Dump format (plain, custom, directory, tar, sql, single-transaction, bak)"},
					{Name: "scope", Type: "string", Required: false, Default: "full", Description: "Dump scope (full, schema_only, data_only)"},
					{Name: "clean", Type: "bool", Required: false, Default: false, Description: "Include DROP statements before CREATE"},
					{Name: "compress", Type: "bool", Required: false, Default: false, Description: "Compress output with gzip (.gz)"},
					{Name: "output_path", Type: "string", Required: false, Description: "Destination file path on the target host"},
				},
			},
			Category:         newapi.ActionCategoryLifecycle,
			Idempotent:       false,
			InvalidatesKinds: []plugin.Kind{plugin.KindDatabase},
		},
	}
}

func (p *Plugin) Actions() []plugin.ActionSpec {
	mgmt := p.ManagementActions()
	specs := make([]plugin.ActionSpec, len(mgmt))
	for i, m := range mgmt {
		specs[i] = m.ActionSpec
	}
	return specs
}

// ValidateAction validates options against the declared ManagementActionSpec.
func (p *Plugin) ValidateAction(h plugin.HostFacts, action string, opts map[string]any) error {
	norm := strings.ToLower(strings.TrimSpace(action))
	if norm == "user_delete" {
		norm = "user_drop"
	}
	for _, spec := range p.ManagementActions() {
		if strings.EqualFold(spec.Name, norm) {
			return newapi.ValidateManagementOptions(spec, opts)
		}
	}
	return fmt.Errorf("unsupported sql action: %s", action)
}

// ContextMenuEntries provides database context menu items for compatible hosts.
func (p *Plugin) ContextMenuEntries(ctx context.Context, h plugin.HostFacts) []newapi.ContextMenuEntry {
	if !p.Compatible(h) {
		return nil
	}
	return newapi.NewContextMenuBuilder(p.Meta().ID).
		WithTarget(newapi.TargetDatabase).
		WithGroup(string(p.Meta().Kind)).
		AddAction("sql-query", "Execute SQL Query", "terminal", "query", 10).
		AddAction("sql-database-create", "Create Database", "add_circle", "database_create", 20).
		AddAction("sql-user-create", "Create / Alter User", "person_add", "user_create", 30).
		AddDangerAction(
			"sql-user-drop",
			"Drop User",
			"person_remove",
			"user_drop",
			"Permanently drop this database user account?",
			80,
		).
		AddDangerAction(
			"sql-database-drop",
			"Drop Database",
			"delete_forever",
			"database_drop",
			"Permanently drop this SQL database and all its data?",
			90,
		).
		Build()
}

// CompileTask compiles a typed TaskRequest into a declarative Ansible TaskSpec.
func (p *Plugin) CompileTask(ctx context.Context, h plugin.HostFacts, req newapi.TaskRequest) (plugin.TaskSpec, error) {
	action := strings.TrimSpace(req.Action)
	if action == "" && req.Params != nil {
		if a, ok := req.Params["action"].(string); ok {
			action = strings.TrimSpace(a)
		}
	}
	if action == "" {
		return plugin.TaskSpec{}, fmt.Errorf("action is required for plugin %s", p.Meta().ID)
	}
	return p.Compile(h, action, req.Params)
}

// Compile converts a declarative SQL action into a TaskSpec.
func (p *Plugin) Compile(h plugin.HostFacts, action string, opts map[string]any) (plugin.TaskSpec, error) {
	norm := strings.ToLower(strings.TrimSpace(action))
	engine := strings.ToLower(optString(opts, "engine"))
	if engine == "" {
		engine = "postgres"
	}

	secretsMasked := make(map[string]string)
	vaultKey := optString(opts, "passwordVaultKey")
	if vaultKey != "" {
		secretsMasked["db_password"] = fmt.Sprintf("•••••••• (vault: %s)", vaultKey)
	} else if pwd := optString(opts, "password"); pwd != "" {
		secretsMasked["db_password"] = "••••••••"
	}

	switch norm {
	case "query":
		query := optString(opts, "query")
		if query == "" {
			return plugin.TaskSpec{}, fmt.Errorf("query is required for query action")
		}
		db := optString(opts, "database")
		limit := optIntDefault(opts, "limit", 50)
		return compileQueryTask(engine, db, query, limit, secretsMasked)

	case "database_create":
		name := optString(opts, "name")
		if name == "" {
			return plugin.TaskSpec{}, fmt.Errorf("name is required for database_create")
		}
		owner := optString(opts, "owner")
		encoding := optString(opts, "encoding")
		return compileDatabaseCreateTask(engine, name, owner, encoding, secretsMasked)

	case "database_drop":
		name := optString(opts, "name")
		if name == "" {
			return plugin.TaskSpec{}, fmt.Errorf("name is required for database_drop")
		}
		return compileDatabaseDropTask(engine, name, secretsMasked)

	case "user_create":
		username := optString(opts, "username")
		if username == "" {
			return plugin.TaskSpec{}, fmt.Errorf("username is required for user_create")
		}
		roles := optString(opts, "roles")
		db := optString(opts, "database")
		return compileUserCreateTask(engine, username, roles, db, opts, secretsMasked)

	case "user_drop", "user_delete":
		username := optString(opts, "username")
		if username == "" {
			return plugin.TaskSpec{}, fmt.Errorf("username is required for user_drop")
		}
		return compileUserDropTask(engine, username, secretsMasked)

	case "database_dump":
		db := firstNonEmpty(optString(opts, "database"), optString(opts, "name"))
		if db == "" {
			return plugin.TaskSpec{}, fmt.Errorf("database is required for database_dump")
		}
		return compileDatabaseDumpTask(h, engine, db, opts, secretsMasked)

	default:
		return plugin.TaskSpec{}, fmt.Errorf("unsupported sql action: %s", action)
	}
}

func compileQueryTask(engine, db, query string, limit int, masked map[string]string) (plugin.TaskSpec, error) {
	switch engine {
	case "mariadb", "mysql":
		return plugin.TaskSpec{
			Name: fmt.Sprintf("MariaDB query: %s", truncate(query, 40)),
			Steps: []plugin.TaskStep{
				{
					Name:   "Run MariaDB query",
					Module: "community.mysql.mysql_query",
					Params: map[string]any{
						"login_db": db,
						"query":    query,
					},
				},
			},
			SecretsMasked: masked,
		}, nil

	case "mssql":
		return plugin.TaskSpec{
			Name: fmt.Sprintf("MSSQL query: %s", truncate(query, 40)),
			Steps: []plugin.TaskStep{
				{
					Name:   "Execute MSSQL query via sqlcmd",
					Module: "ansible.builtin.command",
					Params: map[string]any{
						"cmd": fmt.Sprintf("sqlcmd -d %s -Q %s", escapeArg(db), escapeArg(query)),
					},
				},
			},
			SecretsMasked: masked,
		}, nil

	case "postgres":
		fallthrough
	default:
		return plugin.TaskSpec{
			Name: fmt.Sprintf("PostgreSQL query: %s", truncate(query, 40)),
			Steps: []plugin.TaskStep{
				{
					Name:   "Run PostgreSQL query",
					Module: "community.postgresql.postgresql_query",
					Params: map[string]any{
						"db":    db,
						"query": query,
					},
				},
			},
			SecretsMasked: masked,
		}, nil
	}
}

func compileDatabaseCreateTask(engine, name, owner, encoding string, masked map[string]string) (plugin.TaskSpec, error) {
	if encoding == "" {
		encoding = "UTF8"
	}
	switch engine {
	case "mariadb", "mysql":
		return plugin.TaskSpec{
			Name: fmt.Sprintf("Create MariaDB database %s", name),
			Steps: []plugin.TaskStep{
				{
					Name:   fmt.Sprintf("Ensure database %s is present", name),
					Module: "community.mysql.mysql_db",
					Params: map[string]any{
						"name":     name,
						"encoding": encoding,
						"state":    "present",
					},
				},
			},
			SecretsMasked: masked,
		}, nil

	case "mssql":
		return plugin.TaskSpec{
			Name: fmt.Sprintf("Create MSSQL database %s", name),
			Steps: []plugin.TaskStep{
				{
					Name:   fmt.Sprintf("Create database %s via sqlcmd", name),
					Module: "ansible.builtin.command",
					Params: map[string]any{
						"cmd": fmt.Sprintf("sqlcmd -Q %s", escapeArg(fmt.Sprintf("CREATE DATABASE [%s];", name))),
					},
				},
			},
			SecretsMasked: masked,
		}, nil

	case "postgres":
		fallthrough
	default:
		params := map[string]any{
			"name":     name,
			"encoding": encoding,
			"state":    "present",
		}
		if owner != "" {
			params["owner"] = owner
		}
		return plugin.TaskSpec{
			Name: fmt.Sprintf("Create PostgreSQL database %s", name),
			Steps: []plugin.TaskStep{
				{
					Name:   fmt.Sprintf("Ensure database %s is present", name),
					Module: "community.postgresql.postgresql_db",
					Params: params,
				},
			},
			SecretsMasked: masked,
		}, nil
	}
}

func compileDatabaseDropTask(engine, name string, masked map[string]string) (plugin.TaskSpec, error) {
	switch engine {
	case "mariadb", "mysql":
		return plugin.TaskSpec{
			Name: fmt.Sprintf("Drop MariaDB database %s", name),
			Steps: []plugin.TaskStep{
				{
					Name:   fmt.Sprintf("Drop database %s", name),
					Module: "community.mysql.mysql_db",
					Params: map[string]any{
						"name":  name,
						"state": "absent",
					},
				},
			},
			SecretsMasked: masked,
		}, nil

	case "mssql":
		return plugin.TaskSpec{
			Name: fmt.Sprintf("Drop MSSQL database %s", name),
			Steps: []plugin.TaskStep{
				{
					Name:   fmt.Sprintf("Drop database %s via sqlcmd", name),
					Module: "ansible.builtin.command",
					Params: map[string]any{
						"cmd": fmt.Sprintf("sqlcmd -Q %s", escapeArg(fmt.Sprintf("DROP DATABASE [%s];", name))),
					},
				},
			},
			SecretsMasked: masked,
		}, nil

	case "postgres":
		fallthrough
	default:
		return plugin.TaskSpec{
			Name: fmt.Sprintf("Drop PostgreSQL database %s", name),
			Steps: []plugin.TaskStep{
				{
					Name:   fmt.Sprintf("Drop database %s", name),
					Module: "community.postgresql.postgresql_db",
					Params: map[string]any{
						"name":  name,
						"state": "absent",
					},
				},
			},
			SecretsMasked: masked,
		}, nil
	}
}

func compileUserCreateTask(engine, username, roles, database string, opts map[string]any, masked map[string]string) (plugin.TaskSpec, error) {
	password := optString(opts, "password")
	vaultKey := optString(opts, "passwordVaultKey")

	var pwdVal any
	if vaultKey != "" {
		pwdVal = fmt.Sprintf("{{ %s }}", vaultKey)
	} else if password != "" {
		pwdVal = password
	}

	switch engine {
	case "mariadb", "mysql":
		params := map[string]any{
			"name":  username,
			"state": "present",
		}
		if pwdVal != nil {
			params["password"] = pwdVal
		}
		if roles != "" {
			priv := roles
			if !strings.Contains(priv, ":") && database != "" {
				priv = fmt.Sprintf("%s.*:%s", database, roles)
			}
			params["priv"] = priv
		}
		return plugin.TaskSpec{
			Name: fmt.Sprintf("Create/Update MariaDB user %s", username),
			Steps: []plugin.TaskStep{
				{
					Name:   fmt.Sprintf("Ensure user %s exists", username),
					Module: "community.mysql.mysql_user",
					Params: params,
				},
			},
			SecretsMasked: masked,
		}, nil

	case "mssql":
		sql := fmt.Sprintf("IF NOT EXISTS (SELECT * FROM sys.server_principals WHERE name = N'%s') CREATE LOGIN [%s] ", username, username)
		if password != "" {
			sql += fmt.Sprintf("WITH PASSWORD = N'%s'; ", password)
		} else {
			sql += "WITH PASSWORD = N''; "
		}
		if database != "" {
			sql += fmt.Sprintf("USE [%s]; CREATE USER [%s] FOR LOGIN [%s]; ", database, username, username)
		}
		return plugin.TaskSpec{
			Name: fmt.Sprintf("Create MSSQL login %s", username),
			Steps: []plugin.TaskStep{
				{
					Name:   fmt.Sprintf("Create login %s via sqlcmd", username),
					Module: "ansible.builtin.command",
					Params: map[string]any{
						"cmd": fmt.Sprintf("sqlcmd -Q %s", escapeArg(sql)),
					},
				},
			},
			SecretsMasked: masked,
		}, nil

	case "postgres":
		fallthrough
	default:
		params := map[string]any{
			"name":  username,
			"state": "present",
		}
		if pwdVal != nil {
			params["password"] = pwdVal
		}
		if roles != "" {
			params["role_attr_flags"] = roles
		}
		if database != "" {
			params["db"] = database
		}
		return plugin.TaskSpec{
			Name: fmt.Sprintf("Create/Update PostgreSQL user %s", username),
			Steps: []plugin.TaskStep{
				{
					Name:   fmt.Sprintf("Ensure user %s exists", username),
					Module: "community.postgresql.postgresql_user",
					Params: params,
				},
			},
			SecretsMasked: masked,
		}, nil
	}
}

func compileUserDropTask(engine, username string, masked map[string]string) (plugin.TaskSpec, error) {
	switch engine {
	case "mariadb", "mysql":
		return plugin.TaskSpec{
			Name: fmt.Sprintf("Drop MariaDB user %s", username),
			Steps: []plugin.TaskStep{
				{
					Name:   fmt.Sprintf("Drop user %s", username),
					Module: "community.mysql.mysql_user",
					Params: map[string]any{
						"name":  username,
						"state": "absent",
					},
				},
			},
			SecretsMasked: masked,
		}, nil

	case "mssql":
		sql := fmt.Sprintf("DROP LOGIN [%s];", username)
		return plugin.TaskSpec{
			Name: fmt.Sprintf("Drop MSSQL login %s", username),
			Steps: []plugin.TaskStep{
				{
					Name:   fmt.Sprintf("Drop login %s via sqlcmd", username),
					Module: "ansible.builtin.command",
					Params: map[string]any{
						"cmd": fmt.Sprintf("sqlcmd -Q %s", escapeArg(sql)),
					},
				},
			},
			SecretsMasked: masked,
		}, nil

	case "postgres":
		fallthrough
	default:
		return plugin.TaskSpec{
			Name: fmt.Sprintf("Drop PostgreSQL user %s", username),
			Steps: []plugin.TaskStep{
				{
					Name:   fmt.Sprintf("Drop user %s", username),
					Module: "community.postgresql.postgresql_user",
					Params: map[string]any{
						"name":  username,
						"state": "absent",
					},
				},
			},
			SecretsMasked: masked,
		}, nil
	}
}

func compileDatabaseDumpTask(h plugin.HostFacts, engine, database string, opts map[string]any, masked map[string]string) (plugin.TaskSpec, error) {
	format := strings.ToLower(optString(opts, "format"))
	scope := strings.ToLower(optString(opts, "scope"))
	if scope == "" {
		scope = "full"
	}
	clean := optBool(opts, "clean")
	compress := optBool(opts, "compress")
	outPath := optString(opts, "output_path")

	if outPath == "" {
		ext := ".sql"
		if engine == "postgres" {
			switch format {
			case "custom":
				ext = ".dump"
			case "directory":
				ext = "_dir"
			case "tar":
				ext = ".tar"
			}
		} else if engine == "mssql" {
			ext = ".bak"
		}
		if compress && ext == ".sql" {
			ext = ".sql.gz"
		}
		outPath = fmt.Sprintf("/tmp/%s_dump%s", database, ext)
	}

	switch engine {
	case "mariadb", "mysql":
		flags := make([]string, 0)
		if sock := firstNonEmpty(h.Overrides["mariadb_socket"], h.Overrides["mysql_socket"]); sock != "" {
			flags = append(flags, fmt.Sprintf("-S %s", escapeArg(sock)))
		} else if host := firstNonEmpty(h.Overrides["mariadb_host"], h.Overrides["mysql_host"]); host != "" {
			flags = append(flags, fmt.Sprintf("-h %s", escapeArg(host)))
		}
		if port := firstNonEmpty(h.Overrides["mariadb_port"], h.Overrides["mysql_port"]); port != "" {
			flags = append(flags, fmt.Sprintf("-P %s", port))
		}
		if user := firstNonEmpty(h.Overrides["mariadb_user"], h.Overrides["mysql_user"]); user != "" {
			flags = append(flags, fmt.Sprintf("-u %s", escapeArg(user)))
		}
		if pwd := firstNonEmpty(h.Overrides["mariadb_password"], h.Overrides["mysql_password"]); pwd != "" {
			flags = append(flags, fmt.Sprintf("-p%s", escapeArg(pwd)))
			if masked["db_password"] == "" {
				masked["db_password"] = "••••••••"
			}
		}
		if format == "single-transaction" {
			flags = append(flags, "--single-transaction")
		}
		if scope == "schema_only" {
			flags = append(flags, "--no-data")
		} else if scope == "data_only" {
			flags = append(flags, "--no-create-info")
		}
		if clean {
			flags = append(flags, "--add-drop-table")
		}
		flagStr := strings.Join(flags, " ")
		if flagStr != "" {
			flagStr += " "
		}
		var cmd string
		if compress {
			cmd = fmt.Sprintf("(mariadb-dump %s%s || mysqldump %s%s) | gzip > %s", flagStr, escapeArg(database), flagStr, escapeArg(database), escapeArg(outPath))
		} else {
			cmd = fmt.Sprintf("(mariadb-dump %s%s || mysqldump %s%s) > %s", flagStr, escapeArg(database), flagStr, escapeArg(database), escapeArg(outPath))
		}
		return plugin.TaskSpec{
			Name: fmt.Sprintf("Dump MariaDB database %s to %s", database, outPath),
			Steps: []plugin.TaskStep{
				{
					Name:   fmt.Sprintf("Dump database %s to %s", database, outPath),
					Module: "ansible.builtin.shell",
					Params: map[string]any{
						"cmd": cmd,
					},
				},
			},
			SecretsMasked: masked,
		}, nil

	case "mssql":
		flags := buildMSSQLFlags(h)
		backupSQL := fmt.Sprintf("BACKUP DATABASE [%s] TO DISK = N'%s' WITH FORMAT, INIT;", database, outPath)
		cmd := fmt.Sprintf("sqlcmd %s -Q %s", flags, escapeArg(backupSQL))
		return plugin.TaskSpec{
			Name: fmt.Sprintf("Backup MSSQL database %s to %s", database, outPath),
			Steps: []plugin.TaskStep{
				{
					Name:   fmt.Sprintf("Backup database %s via sqlcmd", database),
					Module: "ansible.builtin.command",
					Params: map[string]any{
						"cmd": cmd,
					},
				},
			},
			SecretsMasked: masked,
		}, nil

	case "postgres":
		fallthrough
	default:
		pgFlags := make([]string, 0)
		if sock := h.Overrides["postgres_socket"]; sock != "" {
			hostDir := sock
			if idx := strings.LastIndex(sock, "/"); idx > 0 && strings.Contains(sock[idx+1:], ".s.PGSQL.") {
				hostDir = sock[:idx]
			}
			pgFlags = append(pgFlags, fmt.Sprintf("-h %s", escapeArg(hostDir)))
		} else if host := h.Overrides["postgres_host"]; host != "" {
			pgFlags = append(pgFlags, fmt.Sprintf("-h %s", escapeArg(host)))
		}
		if port := h.Overrides["postgres_port"]; port != "" {
			pgFlags = append(pgFlags, fmt.Sprintf("-p %s", port))
		}
		if user := h.Overrides["postgres_user"]; user != "" {
			pgFlags = append(pgFlags, fmt.Sprintf("-U %s", escapeArg(user)))
		}
		switch format {
		case "custom":
			pgFlags = append(pgFlags, "-Fc")
		case "directory":
			pgFlags = append(pgFlags, "-Fd")
		case "tar":
			pgFlags = append(pgFlags, "-Ft")
		default:
			pgFlags = append(pgFlags, "-Fp")
		}
		if scope == "schema_only" {
			pgFlags = append(pgFlags, "--schema-only")
		} else if scope == "data_only" {
			pgFlags = append(pgFlags, "--data-only")
		}
		if clean {
			pgFlags = append(pgFlags, "--clean")
		}
		pgPwdEnv := ""
		if pwd := h.Overrides["postgres_password"]; pwd != "" {
			pgPwdEnv = fmt.Sprintf("PGPASSWORD=%s ", escapeArg(pwd))
			if masked["db_password"] == "" {
				masked["db_password"] = "••••••••"
			}
		}
		flagStr := strings.Join(pgFlags, " ")
		if flagStr != "" {
			flagStr += " "
		}
		var cmd string
		if compress && (format == "" || format == "plain") {
			cmd = fmt.Sprintf("%spg_dump %s%s | gzip > %s", pgPwdEnv, flagStr, escapeArg(database), escapeArg(outPath))
		} else {
			cmd = fmt.Sprintf("%spg_dump %s-f %s %s", pgPwdEnv, flagStr, escapeArg(outPath), escapeArg(database))
		}
		return plugin.TaskSpec{
			Name: fmt.Sprintf("Dump PostgreSQL database %s to %s", database, outPath),
			Steps: []plugin.TaskStep{
				{
					Name:   fmt.Sprintf("Dump database %s to %s", database, outPath),
					Module: "ansible.builtin.shell",
					Params: map[string]any{
						"cmd": cmd,
					},
				},
			},
			SecretsMasked: masked,
		}, nil
	}
}

// Flags builders
func buildPostgresFlags(h plugin.HostFacts) string {
	parts := make([]string, 0)
	if sock := h.Overrides["postgres_socket"]; sock != "" {
		hostDir := sock
		if idx := strings.LastIndex(sock, "/"); idx > 0 && strings.Contains(sock[idx+1:], ".s.PGSQL.") {
			hostDir = sock[:idx]
		}
		parts = append(parts, fmt.Sprintf("-h %s", escapeArg(hostDir)))
	} else if host := h.Overrides["postgres_host"]; host != "" {
		parts = append(parts, fmt.Sprintf("-h %s", escapeArg(host)))
	}
	if port := h.Overrides["postgres_port"]; port != "" {
		parts = append(parts, fmt.Sprintf("-p %s", port))
	}
	if user := h.Overrides["postgres_user"]; user != "" {
		parts = append(parts, fmt.Sprintf("-U %s", escapeArg(user)))
	}
	if db := h.Overrides["postgres_database"]; db != "" {
		parts = append(parts, fmt.Sprintf("-d %s", escapeArg(db)))
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " ")
}

func buildMariaDBFlags(h plugin.HostFacts) string {
	parts := make([]string, 0)
	if sock := firstNonEmpty(h.Overrides["mariadb_socket"], h.Overrides["mysql_socket"]); sock != "" {
		parts = append(parts, fmt.Sprintf("-S %s", escapeArg(sock)))
	} else if host := firstNonEmpty(h.Overrides["mariadb_host"], h.Overrides["mysql_host"]); host != "" {
		parts = append(parts, fmt.Sprintf("-h %s", escapeArg(host)))
	}
	if port := firstNonEmpty(h.Overrides["mariadb_port"], h.Overrides["mysql_port"]); port != "" {
		parts = append(parts, fmt.Sprintf("-P %s", port))
	}
	if user := firstNonEmpty(h.Overrides["mariadb_user"], h.Overrides["mysql_user"]); user != "" {
		parts = append(parts, fmt.Sprintf("-u %s", escapeArg(user)))
	}
	if pwd := firstNonEmpty(h.Overrides["mariadb_password"], h.Overrides["mysql_password"]); pwd != "" {
		parts = append(parts, fmt.Sprintf("-p%s", escapeArg(pwd)))
	}
	if db := firstNonEmpty(h.Overrides["mariadb_database"], h.Overrides["mysql_database"]); db != "" {
		parts = append(parts, fmt.Sprintf("-D %s", escapeArg(db)))
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " ")
}

func buildMSSQLFlags(h plugin.HostFacts) string {
	parts := make([]string, 0)
	server := "localhost"
	if sock := h.Overrides["mssql_socket"]; sock != "" {
		server = sock
	} else if host := h.Overrides["mssql_host"]; host != "" {
		server = host
		if port := h.Overrides["mssql_port"]; port != "" {
			server = fmt.Sprintf("%s,%s", host, port)
		}
	} else if port := h.Overrides["mssql_port"]; port != "" && port != "1433" {
		server = fmt.Sprintf("localhost,%s", port)
	}
	parts = append(parts, fmt.Sprintf("-S %s", escapeArg(server)))
	if user := h.Overrides["mssql_user"]; user != "" {
		parts = append(parts, fmt.Sprintf("-U %s", escapeArg(user)))
	}
	if pwd := h.Overrides["mssql_password"]; pwd != "" {
		parts = append(parts, fmt.Sprintf("-P %s", escapeArg(pwd)))
	}
	if db := h.Overrides["mssql_database"]; db != "" {
		parts = append(parts, fmt.Sprintf("-d %s", escapeArg(db)))
	}
	return strings.Join(parts, " ")
}

// Helpers
func contains(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}

func optString(opts map[string]any, key string) string {
	if opts == nil {
		return ""
	}
	if v, ok := opts[key].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

func optBool(opts map[string]any, key string) bool {
	if opts == nil {
		return false
	}
	switch v := opts[key].(type) {
	case bool:
		return v
	case string:
		low := strings.ToLower(strings.TrimSpace(v))
		return low == "true" || low == "1" || low == "yes"
	}
	return false
}

func optIntDefault(opts map[string]any, key string, def int) int {
	if opts == nil {
		return def
	}
	switch v := opts[key].(type) {
	case int:
		return v
	case float64:
		return int(v)
	case string:
		var i int
		if _, err := fmt.Sscanf(v, "%d", &i); err == nil {
			return i
		}
	}
	return def
}

func truncate(s string, maxLen int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.TrimSpace(s)
	if len(s) > maxLen {
		return s[:maxLen] + "..."
	}
	return s
}

func escapeArg(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func hasListeningPort(ssOut, port string) bool {
	port = strings.TrimSpace(port)
	if port == "" || strings.TrimSpace(ssOut) == "" {
		return false
	}
	suffix := ":" + port
	for _, line := range strings.Split(ssOut, "\n") {
		fields := strings.Fields(line)
		for _, f := range fields {
			if strings.HasSuffix(f, suffix) {
				return true
			}
		}
	}
	return false
}
