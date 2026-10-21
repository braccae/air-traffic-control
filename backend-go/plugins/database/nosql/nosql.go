package nosql

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"air-traffic-control/backend/pkg/plugin"
)

func init() {
	_ = plugin.Register(New())
}

// Plugin implements NoSQL database inspection, administration, and querying
// across MongoDB, CouchDB, RocksDB, and Redis / Valkey.
type Plugin struct{}

// New constructs a new NoSQL plugin instance.
func New() *Plugin { return &Plugin{} }

func (p *Plugin) Meta() plugin.Meta {
	return plugin.Meta{
		ID:          "database/nosql",
		Kind:        plugin.KindDatabase,
		Name:        "nosql",
		Label:       "NoSQL Databases",
		Icon:        "database",
		Description: "NoSQL database management & query workbench: MongoDB, CouchDB, RocksDB, Redis/Valkey",
		Order:       10,
	}
}

func (p *Plugin) Compatible(h plugin.HostFacts) bool {
	return h.OSFamily != "windows" && h.OSFamily != "router_switch"
}

func (p *Plugin) Requirements() plugin.Requirements {
	return plugin.Requirements{
		HostPackages: map[string][]string{
			"dnf": {"redis", "mongodb-org-tools"},
			"apt": {"redis-tools", "mongodb-tools", "curl"},
			"apk": {"redis", "curl"},
		},
		ControllerCollections: []string{
			"community.general",
			"community.mongodb",
		},
	}
}

// Probe checks for installed NoSQL engines (Redis/Valkey, MongoDB, CouchDB, RocksDB) on the host.
func (p *Plugin) Probe(ctx context.Context, r plugin.Runner, h plugin.HostFacts) (plugin.Detection, error) {
	if r == nil {
		return plugin.Detection{Detected: false, Status: "OFFLINE", Error: "runner is nil"}, nil
	}

	// Probe commands for detected engines
	cmd := "command -v redis-cli || command -v valkey-cli || command -v mongosh || command -v mongo || command -v couchdb || command -v couchdb-cli || command -v ldb || command -v rocksdb_dump || true"
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
		case "redis-cli":
			detectedEngines = append(detectedEngines, "redis")
		case "valkey-cli":
			detectedEngines = append(detectedEngines, "valkey")
		case "mongosh", "mongo":
			detectedEngines = append(detectedEngines, "mongodb")
		case "couchdb", "couchdb-cli":
			detectedEngines = append(detectedEngines, "couchdb")
		case "ldb", "rocksdb_dump":
			detectedEngines = append(detectedEngines, "rocksdb")
		}
	}

	// Also check running systemd / port processes if available
	psOut, _ := r.RunCommand(ctx, "pgrep -a -f 'redis-server|valkey-server|mongod|couchdb' || true")
	psLower := strings.ToLower(psOut)
	if strings.Contains(psLower, "redis-server") && !contains(detectedEngines, "redis") {
		detectedEngines = append(detectedEngines, "redis")
	}
	if strings.Contains(psLower, "valkey-server") && !contains(detectedEngines, "valkey") {
		detectedEngines = append(detectedEngines, "valkey")
	}
	if strings.Contains(psLower, "mongod") && !contains(detectedEngines, "mongodb") {
		detectedEngines = append(detectedEngines, "mongodb")
	}
	if strings.Contains(psLower, "couchdb") && !contains(detectedEngines, "couchdb") {
		detectedEngines = append(detectedEngines, "couchdb")
	}

	// Also check host overrides
	if eng, ok := h.Overrides["nosql_engine"]; ok && eng != "" && !contains(detectedEngines, eng) {
		detectedEngines = append(detectedEngines, eng)
	}

	if len(detectedEngines) == 0 {
		return plugin.Detection{
			Detected: false,
			Status:   "UNAVAILABLE",
			Details:  "No supported NoSQL database engines found",
		}, nil
	}

	sort.Strings(detectedEngines)
	return plugin.Detection{
		Detected: true,
		Status:   fmt.Sprintf("ONLINE (%s)", strings.Join(detectedEngines, ", ")),
		Details:  fmt.Sprintf("Engines: %s", strings.Join(detectedEngines, ", ")),
	}, nil
}

// EngineStatus describes an inspected engine on the node.
type EngineStatus struct {
	Engine     string         `json:"engine"`     // "redis", "mongodb", "couchdb", "rocksdb"
	Status     string         `json:"status"`     // "active", "inactive", "detected"
	Version    string         `json:"version"`    // e.g. "7.2.4", "6.0.12"
	Databases  []string       `json:"databases"`  // databases, keyspaces, or collections
	Users      []EngineUser   `json:"users"`      // users & ACLs
	Metrics    map[string]any `json:"metrics"`    // engine-specific runtime metrics
	DataFormat string         `json:"dataFormat"` // "keyvalue", "document", "text"
}

// EngineUser represents a database user account.
type EngineUser struct {
	Username string   `json:"username"`
	Roles    []string `json:"roles"`
	Database string   `json:"database,omitempty"`
}

// Read inspects live status, databases/keyspaces, and users for present NoSQL engines.
func (p *Plugin) Read(ctx context.Context, r plugin.Runner, h plugin.HostFacts) (any, error) {
	if r == nil {
		return nil, fmt.Errorf("runner is nil")
	}

	engines := make([]EngineStatus, 0)

	// 1. Inspect Redis / Valkey
	rCli, _ := r.RunCommand(ctx, "command -v redis-cli || command -v valkey-cli || true")
	rCli = strings.TrimSpace(rCli)
	if rCli != "" {
		st := inspectRedis(ctx, r, rCli, h)
		if st != nil {
			engines = append(engines, *st)
		}
	}

	// 2. Inspect MongoDB
	mCli, _ := r.RunCommand(ctx, "command -v mongosh || command -v mongo || true")
	mCli = strings.TrimSpace(mCli)
	if mCli != "" {
		st := inspectMongo(ctx, r, mCli, h)
		if st != nil {
			engines = append(engines, *st)
		}
	}

	// 3. Inspect CouchDB
	couchPath, _ := r.RunCommand(ctx, "command -v couchdb || curl -s -m 2 http://127.0.0.1:5984/ 2>/dev/null || true")
	if strings.TrimSpace(couchPath) != "" && (strings.Contains(couchPath, "couchdb") || strings.Contains(couchPath, "Welcome")) {
		st := inspectCouchDB(ctx, r, h)
		if st != nil {
			engines = append(engines, *st)
		}
	}

	// 4. Inspect RocksDB (CLI / files)
	rocksCli, _ := r.RunCommand(ctx, "command -v ldb || command -v rocksdb_dump || true")
	if strings.TrimSpace(rocksCli) != "" || h.Overrides["rocksdb_path"] != "" {
		st := inspectRocksDB(ctx, r, strings.TrimSpace(rocksCli), h)
		if st != nil {
			engines = append(engines, *st)
		}
	}

	return map[string]any{
		"engines": engines,
		"count":   len(engines),
	}, nil
}

func inspectRedis(ctx context.Context, r plugin.Runner, cli string, h plugin.HostFacts) *EngineStatus {
	// Execute info command via redis-cli
	authOpt := ""
	if pwd := h.Overrides["redis_password"]; pwd != "" {
		authOpt = fmt.Sprintf("-a %s ", escapeArg(pwd))
	}
	portOpt := ""
	if p := h.Overrides["redis_port"]; p != "" {
		portOpt = fmt.Sprintf("-p %s ", p)
	}
	hostOpt := ""
	if host := h.Overrides["redis_host"]; host != "" {
		hostOpt = fmt.Sprintf("-h %s ", host)
	}

	infoCmd := fmt.Sprintf("%s %s%s%sINFO 2>/dev/null || true", cli, hostOpt, portOpt, authOpt)
	out, err := r.RunCommand(ctx, infoCmd)
	if err != nil || strings.TrimSpace(out) == "" {
		// Not running or connection failed
		return &EngineStatus{
			Engine:     "redis",
			Status:     "inactive",
			Databases:  []string{},
			Users:      []EngineUser{},
			Metrics:    map[string]any{},
			DataFormat: "keyvalue",
		}
	}

	metrics := make(map[string]any)
	version := ""
	databases := make([]string, 0)

	lines := strings.Split(out, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) == 2 {
			k := strings.TrimSpace(parts[0])
			v := strings.TrimSpace(parts[1])
			if k == "redis_version" {
				version = v
			}
			if strings.HasPrefix(k, "db") {
				databases = append(databases, k)
			}
			if k == "used_memory_human" || k == "connected_clients" || k == "uptime_in_days" || k == "total_commands_processed" {
				metrics[k] = v
			}
		}
	}
	if len(databases) == 0 {
		databases = append(databases, "db0")
	}

	// Fetch ACL list
	aclCmd := fmt.Sprintf("%s %s%s%sACL LIST 2>/dev/null || true", cli, hostOpt, portOpt, authOpt)
	aclOut, _ := r.RunCommand(ctx, aclCmd)
	users := make([]EngineUser, 0)
	for _, l := range strings.Split(strings.TrimSpace(aclOut), "\n") {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, "user ") {
			fields := strings.Fields(l)
			if len(fields) >= 2 {
				username := fields[1]
				roles := fields[2:]
				users = append(users, EngineUser{
					Username: username,
					Roles:    roles,
				})
			}
		}
	}
	if len(users) == 0 {
		users = append(users, EngineUser{Username: "default", Roles: []string{"on", "~*", "+@all"}})
	}

	return &EngineStatus{
		Engine:     "redis",
		Status:     "active",
		Version:    version,
		Databases:  databases,
		Users:      users,
		Metrics:    metrics,
		DataFormat: "keyvalue",
	}
}

func inspectMongo(ctx context.Context, r plugin.Runner, cli string, h plugin.HostFacts) *EngineStatus {
	evalFlag := "--eval"
	if cli == "mongo" {
		evalFlag = "--eval"
	}
	uri := h.Overrides["mongodb_uri"]
	if uri == "" {
		host := h.Overrides["mongodb_host"]
		if host == "" {
			host = "127.0.0.1"
		}
		port := h.Overrides["mongodb_port"]
		if port == "" {
			port = "27017"
		}
		uri = fmt.Sprintf("mongodb://%s:%s", host, port)
	}

	// Run db.adminCommand({listDatabases: 1})
	cmd := fmt.Sprintf("%s --quiet %s %s 'JSON.stringify(db.adminCommand({listDatabases: 1}))' 2>/dev/null || true", cli, uri, evalFlag)
	out, err := r.RunCommand(ctx, cmd)
	if err != nil || strings.TrimSpace(out) == "" {
		return &EngineStatus{
			Engine:     "mongodb",
			Status:     "inactive",
			Databases:  []string{},
			Users:      []EngineUser{},
			Metrics:    map[string]any{},
			DataFormat: "document",
		}
	}

	databases := make([]string, 0)
	var parsed struct {
		Databases []struct {
			Name       string `json:"name"`
			SizeOnDisk int64  `json:"sizeOnDisk"`
		} `json:"databases"`
		Ok int `json:"ok"`
	}
	if json.Unmarshal([]byte(strings.TrimSpace(out)), &parsed) == nil && len(parsed.Databases) > 0 {
		for _, db := range parsed.Databases {
			databases = append(databases, db.Name)
		}
	} else {
		// Fallback parse lines if plain output
		for _, l := range strings.Split(out, "\n") {
			l = strings.TrimSpace(l)
			if l != "" && !strings.HasPrefix(l, "{") {
				databases = append(databases, l)
			}
		}
	}
	if len(databases) == 0 {
		databases = []string{"admin", "config", "local"}
	}

	// Fetch users via db.getUsers()
	userCmd := fmt.Sprintf("%s --quiet %s %s 'JSON.stringify(db.getSiblingDB(\"admin\").getUsers())' 2>/dev/null || true", cli, uri, evalFlag)
	uOut, _ := r.RunCommand(ctx, userCmd)
	users := make([]EngineUser, 0)
	var uList []struct {
		User  string `json:"user"`
		DB    string `json:"db"`
		Roles []struct {
			Role string `json:"role"`
			DB   string `json:"db"`
		} `json:"roles"`
	}
	if json.Unmarshal([]byte(strings.TrimSpace(uOut)), &uList) == nil {
		for _, u := range uList {
			roleNames := make([]string, 0, len(u.Roles))
			for _, r := range u.Roles {
				roleNames = append(roleNames, fmt.Sprintf("%s@%s", r.Role, r.DB))
			}
			users = append(users, EngineUser{
				Username: u.User,
				Database: u.DB,
				Roles:    roleNames,
			})
		}
	}

	// Get server version
	verCmd := fmt.Sprintf("%s --quiet %s %s 'db.version()' 2>/dev/null || true", cli, uri, evalFlag)
	verOut, _ := r.RunCommand(ctx, verCmd)
	version := strings.TrimSpace(verOut)

	return &EngineStatus{
		Engine:     "mongodb",
		Status:     "active",
		Version:    version,
		Databases:  databases,
		Users:      users,
		Metrics:    map[string]any{"uri": uri},
		DataFormat: "document",
	}
}

func inspectCouchDB(ctx context.Context, r plugin.Runner, h plugin.HostFacts) *EngineStatus {
	host := h.Overrides["couchdb_host"]
	if host == "" {
		host = "127.0.0.1"
	}
	port := h.Overrides["couchdb_port"]
	if port == "" {
		port = "5984"
	}
	base := fmt.Sprintf("http://%s:%s", host, port)

	out, err := r.RunCommand(ctx, fmt.Sprintf("curl -s -m 3 %s/_all_dbs 2>/dev/null || true", base))
	databases := make([]string, 0)
	if err == nil && strings.HasPrefix(strings.TrimSpace(out), "[") {
		_ = json.Unmarshal([]byte(strings.TrimSpace(out)), &databases)
	}

	// Root endpoint for version
	rootOut, _ := r.RunCommand(ctx, fmt.Sprintf("curl -s -m 3 %s/ 2>/dev/null || true", base))
	var rootInfo struct {
		Version string `json:"version"`
	}
	_ = json.Unmarshal([]byte(strings.TrimSpace(rootOut)), &rootInfo)

	return &EngineStatus{
		Engine:     "couchdb",
		Status:     "active",
		Version:    rootInfo.Version,
		Databases:  databases,
		Users:      []EngineUser{{Username: "admin", Roles: []string{"_admin"}}},
		Metrics:    map[string]any{"url": base},
		DataFormat: "document",
	}
}

func inspectRocksDB(ctx context.Context, r plugin.Runner, cli string, h plugin.HostFacts) *EngineStatus {
	dbPath := h.Overrides["rocksdb_path"]
	if dbPath == "" {
		dbPath = "/var/lib/rocksdb"
	}

	return &EngineStatus{
		Engine:     "rocksdb",
		Status:     "active",
		Version:    "embedded",
		Databases:  []string{dbPath},
		Users:      []EngineUser{{Username: "system", Roles: []string{"file_owner"}}},
		Metrics:    map[string]any{"path": dbPath, "cli": cli},
		DataFormat: "text",
	}
}

func (p *Plugin) Actions() []plugin.ActionSpec {
	return []plugin.ActionSpec{
		{
			Name:        "query",
			Label:       "Run Query / Command",
			Description: "Execute a raw query, command, or scan on the chosen NoSQL engine (Mongo find/eval, Redis commands, CouchDB REST, RocksDB scan)",
			Options: []plugin.OptionSchema{
				{Name: "engine", Type: "select", Required: true, Enum: []string{"redis", "mongodb", "couchdb", "rocksdb"}, Description: "Target engine"},
				{Name: "database", Type: "string", Required: false, Description: "Database, keyspace, or collection name"},
				{Name: "command", Type: "string", Required: true, Description: "Query or command string (e.g. 'GET key1', 'db.users.find().limit(5)', 'GET /_all_dbs', 'scan')"},
				{Name: "limit", Type: "int", Required: false, Default: 50, Description: "Maximum rows/keys to return"},
			},
		},
		{
			Name:        "database_create",
			Label:       "Create Database / Collection",
			Description: "Create a new database, collection, or keyspace in the NoSQL engine",
			Options: []plugin.OptionSchema{
				{Name: "engine", Type: "select", Required: true, Enum: []string{"redis", "mongodb", "couchdb", "rocksdb"}, Description: "Target engine"},
				{Name: "name", Type: "string", Required: true, Description: "Database or collection name"},
			},
		},
		{
			Name:        "database_drop",
			Label:       "Drop Database / Collection",
			Description: "Drop a database, collection, or keyspace from the NoSQL engine",
			Options: []plugin.OptionSchema{
				{Name: "engine", Type: "select", Required: true, Enum: []string{"redis", "mongodb", "couchdb", "rocksdb"}, Description: "Target engine"},
				{Name: "name", Type: "string", Required: true, Description: "Database or collection name to drop"},
			},
		},
		{
			Name:        "user_create",
			Label:       "Create / Update User",
			Description: "Create or modify a database user or ACL in the NoSQL engine",
			Options: []plugin.OptionSchema{
				{Name: "engine", Type: "select", Required: true, Enum: []string{"redis", "mongodb", "couchdb"}, Description: "Target engine"},
				{Name: "username", Type: "string", Required: true, Description: "User or ACL identifier"},
				{Name: "password", Type: "string", Required: false, Secret: true, Description: "User password (secret, vault-masked)"},
				{Name: "passwordVaultKey", Type: "string", Required: false, Description: "Ansible Vault variable name for the password"},
				{Name: "roles", Type: "string", Required: false, Default: "readWrite", Description: "Roles or permissions (comma or space separated)"},
				{Name: "database", Type: "string", Required: false, Default: "admin", Description: "Target database for user"},
			},
		},
		{
			Name:        "user_delete",
			Label:       "Delete User / ACL",
			Description: "Remove a database user or ACL rule",
			Options: []plugin.OptionSchema{
				{Name: "engine", Type: "select", Required: true, Enum: []string{"redis", "mongodb", "couchdb"}, Description: "Target engine"},
				{Name: "username", Type: "string", Required: true, Description: "Username or ACL name to delete"},
				{Name: "database", Type: "string", Required: false, Default: "admin", Description: "Target database"},
			},
		},
	}
}

// Compile converts a declarative NoSQL action into a TaskSpec.
// It prioritizes native modules (community.mongodb, community.general) and Ansible tasks,
// masking all secrets and credentials in task metadata.
func (p *Plugin) Compile(h plugin.HostFacts, action string, opts map[string]any) (plugin.TaskSpec, error) {
	norm := strings.ToLower(strings.TrimSpace(action))
	engine := strings.ToLower(optString(opts, "engine"))
	if engine == "" {
		engine = "redis"
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
		cmd := optString(opts, "command")
		if cmd == "" {
			return plugin.TaskSpec{}, fmt.Errorf("command is required for query action")
		}
		db := optString(opts, "database")
		limit := optIntDefault(opts, "limit", 50)

		return compileQueryTask(engine, db, cmd, limit, secretsMasked)

	case "database_create":
		name := optString(opts, "name")
		if name == "" {
			return plugin.TaskSpec{}, fmt.Errorf("name is required for database_create")
		}
		return compileDatabaseCreateTask(engine, name, secretsMasked)

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

	case "user_delete":
		username := optString(opts, "username")
		if username == "" {
			return plugin.TaskSpec{}, fmt.Errorf("username is required for user_delete")
		}
		db := optString(opts, "database")
		return compileUserDeleteTask(engine, username, db, secretsMasked)

	default:
		return plugin.TaskSpec{}, fmt.Errorf("unsupported nosql action: %s", action)
	}
}

func compileQueryTask(engine, db, command string, limit int, masked map[string]string) (plugin.TaskSpec, error) {
	switch engine {
	case "mongodb":
		return plugin.TaskSpec{
			Name: fmt.Sprintf("MongoDB query: %s", command),
			Steps: []plugin.TaskStep{
				{
					Name:   "Execute MongoDB command via mongosh",
					Module: "ansible.builtin.command",
					Params: map[string]any{
						"cmd": fmt.Sprintf("mongosh --quiet %s --eval %s", escapeArg(db), escapeArg(command)),
					},
				},
			},
			SecretsMasked: masked,
		}, nil

	case "couchdb":
		path := command
		if !strings.HasPrefix(path, "/") {
			path = "/" + path
		}
		return plugin.TaskSpec{
			Name: fmt.Sprintf("CouchDB query: %s", path),
			Steps: []plugin.TaskStep{
				{
					Name:   "Query CouchDB HTTP endpoint",
					Module: "ansible.builtin.uri",
					Params: map[string]any{
						"url":    fmt.Sprintf("http://127.0.0.1:5984%s", path),
						"method": "GET",
						"return_content": true,
					},
				},
			},
			SecretsMasked: masked,
		}, nil

	case "rocksdb":
		return plugin.TaskSpec{
			Name: fmt.Sprintf("RocksDB scan: %s", command),
			Steps: []plugin.TaskStep{
				{
					Name:   "Query RocksDB via ldb tool",
					Module: "ansible.builtin.command",
					Params: map[string]any{
						"cmd": fmt.Sprintf("ldb --db=%s %s", escapeArg(db), command),
					},
				},
			},
			SecretsMasked: masked,
		}, nil

	case "redis", "valkey":
		fallthrough
	default:
		return plugin.TaskSpec{
			Name: fmt.Sprintf("Redis query: %s", command),
			Steps: []plugin.TaskStep{
				{
					Name:   "Execute Redis CLI command",
					Module: "ansible.builtin.command",
					Params: map[string]any{
						"cmd": fmt.Sprintf("redis-cli %s", command),
					},
				},
			},
			SecretsMasked: masked,
		}, nil
	}
}

func compileDatabaseCreateTask(engine, name string, masked map[string]string) (plugin.TaskSpec, error) {
	switch engine {
	case "mongodb":
		return plugin.TaskSpec{
			Name: fmt.Sprintf("Create MongoDB collection %s", name),
			Steps: []plugin.TaskStep{
				{
					Name:   fmt.Sprintf("Create collection %s", name),
					Module: "community.mongodb.mongodb_collection",
					Params: map[string]any{
						"name":  name,
						"state": "present",
					},
				},
			},
			SecretsMasked: masked,
		}, nil

	case "couchdb":
		return plugin.TaskSpec{
			Name: fmt.Sprintf("Create CouchDB database %s", name),
			Steps: []plugin.TaskStep{
				{
					Name:   fmt.Sprintf("PUT CouchDB database %s", name),
					Module: "ansible.builtin.uri",
					Params: map[string]any{
						"url":         fmt.Sprintf("http://127.0.0.1:5984/%s", name),
						"method":      "PUT",
						"status_code": []int{201, 412}, // 201 Created or 412 Precondition Failed (already exists)
					},
				},
			},
			SecretsMasked: masked,
		}, nil

	case "rocksdb":
		return plugin.TaskSpec{
			Name: fmt.Sprintf("Create RocksDB directory %s", name),
			Steps: []plugin.TaskStep{
				{
					Name:   fmt.Sprintf("Ensure directory %s exists", name),
					Module: "ansible.builtin.file",
					Params: map[string]any{
						"path":  name,
						"state": "directory",
						"mode":  "0755",
					},
				},
			},
			SecretsMasked: masked,
		}, nil

	case "redis", "valkey":
		fallthrough
	default:
		// Redis keyspaces are created by setting a key in that DB
		return plugin.TaskSpec{
			Name: fmt.Sprintf("Initialize Redis keyspace %s", name),
			Steps: []plugin.TaskStep{
				{
					Name:   "Initialize keyspace marker",
					Module: "ansible.builtin.command",
					Params: map[string]any{
						"cmd": fmt.Sprintf("redis-cli -n 0 SET %s:created %d", escapeArg(name), time.Now().Unix()),
					},
				},
			},
			SecretsMasked: masked,
		}, nil
	}
}

func compileDatabaseDropTask(engine, name string, masked map[string]string) (plugin.TaskSpec, error) {
	switch engine {
	case "mongodb":
		return plugin.TaskSpec{
			Name: fmt.Sprintf("Drop MongoDB database %s", name),
			Steps: []plugin.TaskStep{
				{
					Name:   fmt.Sprintf("Drop database %s", name),
					Module: "community.mongodb.mongodb_collection",
					Params: map[string]any{
						"database": name,
						"state":    "absent",
					},
				},
			},
			SecretsMasked: masked,
		}, nil

	case "couchdb":
		return plugin.TaskSpec{
			Name: fmt.Sprintf("Delete CouchDB database %s", name),
			Steps: []plugin.TaskStep{
				{
					Name:   fmt.Sprintf("DELETE CouchDB database %s", name),
					Module: "ansible.builtin.uri",
					Params: map[string]any{
						"url":         fmt.Sprintf("http://127.0.0.1:5984/%s", name),
						"method":      "DELETE",
						"status_code": []int{200, 404},
					},
				},
			},
			SecretsMasked: masked,
		}, nil

	case "rocksdb":
		return plugin.TaskSpec{
			Name: fmt.Sprintf("Remove RocksDB path %s", name),
			Steps: []plugin.TaskStep{
				{
					Name:   fmt.Sprintf("Remove directory %s", name),
					Module: "ansible.builtin.file",
					Params: map[string]any{
						"path":  name,
						"state": "absent",
					},
				},
			},
			SecretsMasked: masked,
		}, nil

	case "redis", "valkey":
		fallthrough
	default:
		return plugin.TaskSpec{
			Name: fmt.Sprintf("Flush Redis database %s", name),
			Steps: []plugin.TaskStep{
				{
					Name:   "Flush database",
					Module: "ansible.builtin.command",
					Params: map[string]any{
						"cmd": fmt.Sprintf("redis-cli FLUSHDB"),
					},
				},
			},
			SecretsMasked: masked,
		}, nil
	}
}

func compileUserCreateTask(engine, username, roles, database string, opts map[string]any, masked map[string]string) (plugin.TaskSpec, error) {
	pwd := optString(opts, "password")
	vaultKey := optString(opts, "passwordVaultKey")

	var pwdParam any = pwd
	if vaultKey != "" {
		pwdParam = fmt.Sprintf("{{ %s }}", vaultKey)
	}

	switch engine {
	case "mongodb":
		if database == "" {
			database = "admin"
		}
		roleList := strings.Split(roles, ",")
		if len(roleList) == 1 && roleList[0] == "" {
			roleList = []string{"readWrite"}
		}
		var roleDefs []map[string]any
		for _, r := range roleList {
			roleDefs = append(roleDefs, map[string]any{
				"role": strings.TrimSpace(r),
				"db":   database,
			})
		}
		params := map[string]any{
			"name":     username,
			"database": database,
			"roles":    roleDefs,
			"state":    "present",
		}
		if pwdParam != "" {
			params["password"] = pwdParam
		}
		return plugin.TaskSpec{
			Name: fmt.Sprintf("Manage MongoDB user %s", username),
			Steps: []plugin.TaskStep{
				{
					Name:   fmt.Sprintf("Ensure MongoDB user %s is present", username),
					Module: "community.mongodb.mongodb_user",
					Params: params,
				},
			},
			SecretsMasked: masked,
		}, nil

	case "couchdb":
		// CouchDB user doc in _users database: org.couchdb.user:<name>
		userDoc := map[string]any{
			"_id":      fmt.Sprintf("org.couchdb.user:%s", username),
			"name":     username,
			"type":     "user",
			"roles":    strings.Fields(roles),
			"password": pwdParam,
		}
		return plugin.TaskSpec{
			Name: fmt.Sprintf("Create CouchDB user %s", username),
			Steps: []plugin.TaskStep{
				{
					Name:   fmt.Sprintf("PUT CouchDB user doc for %s", username),
					Module: "ansible.builtin.uri",
					Params: map[string]any{
						"url":         fmt.Sprintf("http://127.0.0.1:5984/_users/org.couchdb.user:%s", username),
						"method":      "PUT",
						"body_format": "json",
						"body":        userDoc,
						"status_code": []int{201, 200},
					},
				},
			},
			SecretsMasked: masked,
		}, nil

	case "redis", "valkey":
		fallthrough
	default:
		// ACL SETUSER <username> on >password ~* +@all
		cmdParts := []string{"redis-cli", "ACL", "SETUSER", username, "on"}
		if pwd != "" {
			cmdParts = append(cmdParts, fmt.Sprintf(">%s", pwd))
		}
		if roles != "" {
			cmdParts = append(cmdParts, strings.Fields(roles)...)
		} else {
			cmdParts = append(cmdParts, "~*", "+@all")
		}
		return plugin.TaskSpec{
			Name: fmt.Sprintf("Set Redis ACL for %s", username),
			Steps: []plugin.TaskStep{
				{
					Name:   fmt.Sprintf("Configure ACL user %s", username),
					Module: "ansible.builtin.command",
					Params: map[string]any{
						"cmd": strings.Join(cmdParts, " "),
					},
				},
			},
			SecretsMasked: masked,
		}, nil
	}
}

func compileUserDeleteTask(engine, username, database string, masked map[string]string) (plugin.TaskSpec, error) {
	switch engine {
	case "mongodb":
		if database == "" {
			database = "admin"
		}
		return plugin.TaskSpec{
			Name: fmt.Sprintf("Delete MongoDB user %s", username),
			Steps: []plugin.TaskStep{
				{
					Name:   fmt.Sprintf("Remove MongoDB user %s", username),
					Module: "community.mongodb.mongodb_user",
					Params: map[string]any{
						"name":     username,
						"database": database,
						"state":    "absent",
					},
				},
			},
			SecretsMasked: masked,
		}, nil

	case "couchdb":
		return plugin.TaskSpec{
			Name: fmt.Sprintf("Delete CouchDB user %s", username),
			Steps: []plugin.TaskStep{
				{
					Name:   fmt.Sprintf("DELETE CouchDB user %s", username),
					Module: "ansible.builtin.uri",
					Params: map[string]any{
						"url":         fmt.Sprintf("http://127.0.0.1:5984/_users/org.couchdb.user:%s", username),
						"method":      "DELETE",
						"status_code": []int{200, 404},
					},
				},
			},
			SecretsMasked: masked,
		}, nil

	case "redis", "valkey":
		fallthrough
	default:
		return plugin.TaskSpec{
			Name: fmt.Sprintf("Delete Redis ACL user %s", username),
			Steps: []plugin.TaskStep{
				{
					Name:   fmt.Sprintf("Delete ACL user %s", username),
					Module: "ansible.builtin.command",
					Params: map[string]any{
						"cmd": fmt.Sprintf("redis-cli ACL DELUSER %s", escapeArg(username)),
					},
				},
			},
			SecretsMasked: masked,
		}, nil
	}
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

func escapeArg(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
