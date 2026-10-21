package sql

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"air-traffic-control/backend/pkg/plugin"
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

// Probe checks for installed SQL database engines (PostgreSQL, MariaDB/MySQL, MSSQL) on the host.
func (p *Plugin) Probe(ctx context.Context, r plugin.Runner, h plugin.HostFacts) (plugin.Detection, error) {
	if r == nil {
		return plugin.Detection{Detected: false, Status: "OFFLINE", Error: "runner is nil"}, nil
	}

	cmd := "command -v psql || command -v mysql || command -v mariadb || command -v sqlcmd || true"
	out, err := r.RunCommand(ctx, cmd)
	if err != nil {
		return plugin.Detection{Detected: false, Status: "ERROR", Error: err.Error()}, nil
	}

	lines := strings.Fields(strings.TrimSpace(out))
	detectedEngines := make([]string, 0)
	for _, l := range lines {
		base := l
		if idx := strings.LastIndex(l, "/"); idx != -1 {
			base = l[idx+1:]
		}
		switch base {
		case "psql":
			if !contains(detectedEngines, "postgres") {
				detectedEngines = append(detectedEngines, "postgres")
			}
		case "mysql", "mariadb":
			if !contains(detectedEngines, "mariadb") {
				detectedEngines = append(detectedEngines, "mariadb")
			}
		case "sqlcmd":
			if !contains(detectedEngines, "mssql") {
				detectedEngines = append(detectedEngines, "mssql")
			}
		}
	}

	// Also probe running systemd/process services
	psOut, _ := r.RunCommand(ctx, "pgrep -a -f 'postgres|mysqld|mariadbd|sqlservr' || true")
	psLower := strings.ToLower(psOut)
	if strings.Contains(psLower, "postgres") && !contains(detectedEngines, "postgres") {
		detectedEngines = append(detectedEngines, "postgres")
	}
	if (strings.Contains(psLower, "mysqld") || strings.Contains(psLower, "mariadbd")) && !contains(detectedEngines, "mariadb") {
		detectedEngines = append(detectedEngines, "mariadb")
	}
	if strings.Contains(psLower, "sqlservr") && !contains(detectedEngines, "mssql") {
		detectedEngines = append(detectedEngines, "mssql")
	}

	// Also check host overrides
	if eng, ok := h.Overrides["sql_engine"]; ok && eng != "" && !contains(detectedEngines, eng) {
		detectedEngines = append(detectedEngines, eng)
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

	// 1. Inspect PostgreSQL
	psqlCli, _ := r.RunCommand(ctx, "command -v psql || true")
	if strings.TrimSpace(psqlCli) != "" || h.Overrides["postgres_host"] != "" {
		st := inspectPostgres(ctx, r, strings.TrimSpace(psqlCli), h)
		if st != nil {
			engines = append(engines, *st)
		}
	}

	// 2. Inspect MariaDB / MySQL
	mysqlCli, _ := r.RunCommand(ctx, "command -v mariadb || command -v mysql || true")
	if strings.TrimSpace(mysqlCli) != "" || h.Overrides["mariadb_host"] != "" {
		st := inspectMariaDB(ctx, r, strings.TrimSpace(mysqlCli), h)
		if st != nil {
			engines = append(engines, *st)
		}
	}

	// 3. Inspect MSSQL
	mssqlCli, _ := r.RunCommand(ctx, "command -v sqlcmd || true")
	if strings.TrimSpace(mssqlCli) != "" || h.Overrides["mssql_host"] != "" {
		st := inspectMSSQL(ctx, r, strings.TrimSpace(mssqlCli), h)
		if st != nil {
			engines = append(engines, *st)
		}
	}

	return map[string]any{
		"engines": engines,
		"count":   len(engines),
	}, nil
}

func inspectPostgres(ctx context.Context, r plugin.Runner, cli string, h plugin.HostFacts) *EngineStatus {
	if cli == "" {
		cli = "psql"
	}
	opts := buildPostgresFlags(h)

	// Test connection and query version
	cmd := fmt.Sprintf("%s %s -t -A -c 'SELECT version();' 2>/dev/null || true", cli, opts)
	verOut, err := r.RunCommand(ctx, cmd)
	if err != nil || strings.TrimSpace(verOut) == "" {
		return &EngineStatus{
			Engine:    "postgres",
			Status:    "inactive",
			Databases: []string{},
			Users:     []EngineUser{},
			Metrics:   map[string]any{},
		}
	}

	version := strings.TrimSpace(verOut)
	if idx := strings.Index(version, "\n"); idx != -1 {
		version = version[:idx]
	}

	// List databases
	dbCmd := fmt.Sprintf("%s %s -t -A -c 'SELECT datname FROM pg_database WHERE datistemplate = false;' 2>/dev/null || true", cli, opts)
	dbOut, _ := r.RunCommand(ctx, dbCmd)
	databases := make([]string, 0)
	for _, l := range strings.Split(dbOut, "\n") {
		l = strings.TrimSpace(l)
		if l != "" {
			databases = append(databases, l)
		}
	}
	sort.Strings(databases)

	// List users and attributes
	userCmd := fmt.Sprintf("%s %s -t -A -F'|' -c 'SELECT usename, usesuper, usecreatedb FROM pg_user;' 2>/dev/null || true", cli, opts)
	userOut, _ := r.RunCommand(ctx, userCmd)
	users := make([]EngineUser, 0)
	for _, l := range strings.Split(userOut, "\n") {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		parts := strings.Split(l, "|")
		u := EngineUser{Username: parts[0], Roles: make([]string, 0)}
		if len(parts) > 1 && parts[1] == "t" {
			u.Roles = append(u.Roles, "superuser")
		}
		if len(parts) > 2 && parts[2] == "t" {
			u.Roles = append(u.Roles, "createdb")
		}
		users = append(users, u)
	}

	// Metrics: connections and size
	metrics := make(map[string]any)
	statCmd := fmt.Sprintf("%s %s -t -A -c 'SELECT count(*) FROM pg_stat_activity;' 2>/dev/null || true", cli, opts)
	if statOut, err := r.RunCommand(ctx, statCmd); err == nil && strings.TrimSpace(statOut) != "" {
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

func inspectMariaDB(ctx context.Context, r plugin.Runner, cli string, h plugin.HostFacts) *EngineStatus {
	if cli == "" {
		cli = "mysql"
	}
	opts := buildMariaDBFlags(h)

	cmd := fmt.Sprintf("%s %s -s -N -e 'SELECT VERSION();' 2>/dev/null || true", cli, opts)
	verOut, err := r.RunCommand(ctx, cmd)
	if err != nil || strings.TrimSpace(verOut) == "" {
		return &EngineStatus{
			Engine:    "mariadb",
			Status:    "inactive",
			Databases: []string{},
			Users:     []EngineUser{},
			Metrics:   map[string]any{},
		}
	}

	version := strings.TrimSpace(verOut)

	// List databases
	dbCmd := fmt.Sprintf("%s %s -s -N -e 'SHOW DATABASES;' 2>/dev/null || true", cli, opts)
	dbOut, _ := r.RunCommand(ctx, dbCmd)
	databases := make([]string, 0)
	for _, l := range strings.Split(dbOut, "\n") {
		l = strings.TrimSpace(l)
		if l != "" {
			databases = append(databases, l)
		}
	}
	sort.Strings(databases)

	// List users
	userCmd := fmt.Sprintf("%s %s -s -N -e 'SELECT User, Host FROM mysql.user;' 2>/dev/null || true", cli, opts)
	userOut, _ := r.RunCommand(ctx, userCmd)
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

	metrics := make(map[string]any)
	threadCmd := fmt.Sprintf("%s %s -s -N -e \"SHOW GLOBAL STATUS LIKE 'Threads_connected';\" 2>/dev/null || true", cli, opts)
	if thOut, err := r.RunCommand(ctx, threadCmd); err == nil {
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

func inspectMSSQL(ctx context.Context, r plugin.Runner, cli string, h plugin.HostFacts) *EngineStatus {
	if cli == "" {
		cli = "sqlcmd"
	}
	opts := buildMSSQLFlags(h)

	cmd := fmt.Sprintf("%s %s -h -1 -W -Q 'SELECT @@VERSION;' 2>/dev/null || true", cli, opts)
	verOut, err := r.RunCommand(ctx, cmd)
	if err != nil || strings.TrimSpace(verOut) == "" {
		return &EngineStatus{
			Engine:    "mssql",
			Status:    "inactive",
			Databases: []string{},
			Users:     []EngineUser{},
			Metrics:   map[string]any{},
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
		Metrics:   map[string]any{},
	}
}

func (p *Plugin) Actions() []plugin.ActionSpec {
	return []plugin.ActionSpec{
		{
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
		{
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
		{
			Name:        "database_drop",
			Label:       "Drop Database",
			Description: "Drop an existing database from the SQL server",
			Options: []plugin.OptionSchema{
				{Name: "engine", Type: "select", Required: true, Enum: []string{"postgres", "mariadb", "mssql"}, Description: "Target SQL engine"},
				{Name: "name", Type: "string", Required: true, Description: "Database name to drop"},
			},
		},
		{
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
		{
			Name:        "user_drop",
			Label:       "Drop User",
			Description: "Drop a database user from the SQL server",
			Options: []plugin.OptionSchema{
				{Name: "engine", Type: "select", Required: true, Enum: []string{"postgres", "mariadb", "mssql"}, Description: "Target SQL engine"},
				{Name: "username", Type: "string", Required: true, Description: "Database username to drop"},
			},
		},
	}
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

// Flags builders
func buildPostgresFlags(h plugin.HostFacts) string {
	parts := make([]string, 0)
	if host := h.Overrides["postgres_host"]; host != "" {
		parts = append(parts, fmt.Sprintf("-h %s", escapeArg(host)))
	}
	if port := h.Overrides["postgres_port"]; port != "" {
		parts = append(parts, fmt.Sprintf("-p %s", port))
	}
	if user := h.Overrides["postgres_user"]; user != "" {
		parts = append(parts, fmt.Sprintf("-U %s", escapeArg(user)))
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " ")
}

func buildMariaDBFlags(h plugin.HostFacts) string {
	parts := make([]string, 0)
	if host := h.Overrides["mariadb_host"]; host != "" {
		parts = append(parts, fmt.Sprintf("-h %s", escapeArg(host)))
	}
	if port := h.Overrides["mariadb_port"]; port != "" {
		parts = append(parts, fmt.Sprintf("-P %s", port))
	}
	if user := h.Overrides["mariadb_user"]; user != "" {
		parts = append(parts, fmt.Sprintf("-u %s", escapeArg(user)))
	}
	if pwd := h.Overrides["mariadb_password"]; pwd != "" {
		parts = append(parts, fmt.Sprintf("-p%s", escapeArg(pwd)))
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " ")
}

func buildMSSQLFlags(h plugin.HostFacts) string {
	parts := make([]string, 0)
	server := "localhost"
	if host := h.Overrides["mssql_host"]; host != "" {
		server = host
		if port := h.Overrides["mssql_port"]; port != "" {
			server = fmt.Sprintf("%s,%s", host, port)
		}
	}
	parts = append(parts, fmt.Sprintf("-S %s", escapeArg(server)))
	if user := h.Overrides["mssql_user"]; user != "" {
		parts = append(parts, fmt.Sprintf("-U %s", escapeArg(user)))
	}
	if pwd := h.Overrides["mssql_password"]; pwd != "" {
		parts = append(parts, fmt.Sprintf("-P %s", escapeArg(pwd)))
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
