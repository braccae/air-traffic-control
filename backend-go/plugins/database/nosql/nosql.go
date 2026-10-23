package nosql

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"air-traffic-control/backend/pkg/plugin"
	"air-traffic-control/backend/pkg/plugin/newapi"
	"air-traffic-control/backend/pkg/plugin/settingsframework"
)

// SettingsPageID is the canonical settings page route slug for the NoSQL plugin.
const SettingsPageID = "database/nosql"

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

// CollectionSchedule returns the standard polling and cache TTL schedule for NoSQL database inspection.
func (p *Plugin) CollectionSchedule() newapi.CollectionSchedule {
	return newapi.DefaultScheduleForKind(p.Meta().Kind)
}

// CollectionSchema documents the v2 payload structure emitted by Read.
func (p *Plugin) CollectionSchema() newapi.CollectionSchema {
	return newapi.CollectionSchema{
		Version:     "v2",
		RootKey:     "engines",
		Description: p.Meta().Description,
	}
}

// CategorizedSettingsPage declares datacenter-wide NoSQL database defaults via settingsframework.
func (p *Plugin) CategorizedSettingsPage() newapi.CategorizedSettingsPage {
	meta := p.Meta()
	return settingsframework.NewPageBuilder(meta.ID, "NoSQL Database Defaults").
		WithIcon(meta.Icon).
		WithCategory(string(meta.Kind)).
		WithScope(newapi.SettingsScopeDatacenter).
		WithOrder(meta.Order).
		WithDescription("Datacenter-wide NoSQL engine ports, query limits, and Ansible Vault credential defaults for Redis/Valkey, MongoDB, CouchDB, and RocksDB.").
		SelectField("defaultEngine", "Default NoSQL Engine", "redis", []plugin.SettingsFieldOption{
			{Value: "redis", Label: "Redis"},
			{Value: "valkey", Label: "Valkey"},
			{Value: "mongodb", Label: "MongoDB"},
			{Value: "couchdb", Label: "CouchDB"},
			{Value: "rocksdb", Label: "RocksDB"},
		}, settingsframework.WithDescription("Default NoSQL engine targeted when compiling database actions without an explicit engine.")).
		IntField("redisDefaultPort", "Default Redis / Valkey Port", 6379,
			settingsframework.WithMinMax(1, 65535),
			settingsframework.WithDescription("Default TCP port used when probing or inspecting Redis and Valkey instances.")).
		IntField("mongoDefaultPort", "Default MongoDB Port", 27017,
			settingsframework.WithMinMax(1, 65535),
			settingsframework.WithDescription("Default TCP port used when probing or inspecting MongoDB instances.")).
		IntField("couchDefaultPort", "Default CouchDB Port", 5984,
			settingsframework.WithMinMax(1, 65535),
			settingsframework.WithDescription("Default HTTP port used when probing or inspecting CouchDB instances.")).
		IntField("defaultQueryLimit", "Default Query Row / Key Limit", 50,
			settingsframework.WithMinMax(1, 10000),
			settingsframework.WithDescription("Default maximum number of rows or keys returned by raw NoSQL queries.")).
		SecretField("defaultPasswordVaultVar", "Default Password Vault Variable",
			settingsframework.WithPlaceholder("vault_nosql_password"),
			settingsframework.WithDescription("Default Ansible Vault variable name used for NoSQL user and ACL credentials.")).
		BuildCategorized()
}

// SettingsPage returns the underlying plugin.SettingsPage for legacy consumers.
func (p *Plugin) SettingsPage() plugin.SettingsPage {
	return p.CategorizedSettingsPage().SettingsPage
}

func defaultNoSQLEngine() string {
	return settingsframework.NewStoreAdapter(nil).StringOrDefault(SettingsPageID, "defaultEngine", "redis")
}

func defaultNoSQLQueryLimit() int {
	return settingsframework.NewStoreAdapter(nil).IntOrDefault(SettingsPageID, "defaultQueryLimit", 50)
}

// ContextMenuEntries provides database context menu actions for compatible hosts.
func (p *Plugin) ContextMenuEntries(ctx context.Context, h plugin.HostFacts) []newapi.ContextMenuEntry {
	if !p.Compatible(h) {
		return nil
	}
	meta := p.Meta()
	return newapi.NewContextMenuBuilder(meta.ID).
		WithTarget(newapi.TargetDatabase).
		WithGroup(string(meta.Kind)).
		AddAction("query", "Run Query / Command", "terminal", "query", 10).
		AddAction("database_create", "Create Database / Collection", "add_box", "database_create", 20).
		AddAction("user_create", "Create / Update User", "person_add", "user_create", 30).
		AddDangerAction("database_drop", "Drop Database / Collection", "delete_forever", "database_drop",
			"Permanently drop this NoSQL database, collection, or keyspace?", 80).
		AddDangerAction("user_delete", "Delete User / ACL", "person_remove", "user_delete",
			"Permanently delete this NoSQL database user or ACL rule?", 90).
		Build()
}

// Probe checks for active NoSQL engines (Redis/Valkey, MongoDB, CouchDB, RocksDB) on the host.
func (p *Plugin) Probe(ctx context.Context, r plugin.Runner, h plugin.HostFacts) (plugin.Detection, error) {
	if r == nil {
		return plugin.Detection{Detected: false, Status: "OFFLINE", Error: "runner is nil"}, nil
	}

	redisPort := firstNonEmpty(h.Overrides["redis_port"], "6379")
	redisSocket := h.Overrides["redis_socket"]
	valkeyPort := firstNonEmpty(h.Overrides["valkey_port"], "6379")
	valkeySocket := h.Overrides["valkey_socket"]
	mongoPort := firstNonEmpty(h.Overrides["mongodb_port"], "27017")
	mongoSocket := h.Overrides["mongodb_socket"]
	couchPort := firstNonEmpty(h.Overrides["couchdb_port"], "5984")
	couchSocket := h.Overrides["couchdb_socket"]

	detectedEngines := make([]string, 0)

	// 1. Probe running server processes (without -f or -a so the subshell never matches itself)
	psOut, err := r.RunCommand(ctx, "pgrep -l '^(redis-server|valkey-server|mongod|couchdb)$' 2>/dev/null || true")
	if err != nil {
		return plugin.Detection{Detected: false, Status: "ERROR", Error: err.Error()}, nil
	}
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

	// 2. Probe Unix sockets, listening TCP ports, and RocksDB directory
	probeCmd := fmt.Sprintf(
		"for s in %s /run/redis/redis.sock /var/run/redis/redis.sock /run/redis/redis-server.sock /tmp/redis.sock; do [ -n \"$s\" ] && [ -S \"$s\" ] && echo \"REDIS_SOCK:$s\" && break; done; "+
			"for s in %s /run/valkey/valkey.sock /var/run/valkey/valkey.sock; do [ -n \"$s\" ] && [ -S \"$s\" ] && echo \"VALKEY_SOCK:$s\" && break; done; "+
			"for s in %s /tmp/mongodb-%s.sock /tmp/mongodb-27017.sock /run/mongodb/mongod.sock /var/run/mongodb/mongod.sock; do [ -n \"$s\" ] && [ -S \"$s\" ] && echo \"MONGO_SOCK:$s\" && break; done; "+
			"for s in %s /run/couchdb/couchdb.sock; do [ -n \"$s\" ] && [ -S \"$s\" ] && echo \"COUCH_SOCK:$s\" && break; done; "+
			"[ -d /var/lib/rocksdb ] && echo \"ROCKSDB_DIR:/var/lib/rocksdb\"; "+
			"echo '===SS==='; ss -ltn 2>/dev/null || true",
		escapeArg(redisSocket),
		escapeArg(valkeySocket),
		escapeArg(mongoSocket), mongoPort,
		escapeArg(couchSocket),
	)
	probeOut, _ := r.RunCommand(ctx, probeCmd)
	parts := strings.Split(probeOut, "===SS===")
	sockPart := parts[0]
	ssPart := ""
	if len(parts) > 1 {
		ssPart = parts[1]
	}

	if strings.Contains(sockPart, "REDIS_SOCK:") || hasListeningPort(ssPart, redisPort) {
		if !contains(detectedEngines, "redis") && !contains(detectedEngines, "valkey") {
			detectedEngines = append(detectedEngines, "redis")
		}
	}
	if strings.Contains(sockPart, "VALKEY_SOCK:") || (valkeyPort != redisPort && hasListeningPort(ssPart, valkeyPort)) {
		if !contains(detectedEngines, "valkey") {
			detectedEngines = append(detectedEngines, "valkey")
		}
	}
	if strings.Contains(sockPart, "MONGO_SOCK:") {
		if !contains(detectedEngines, "mongodb") {
			detectedEngines = append(detectedEngines, "mongodb")
		}
	} else if !contains(detectedEngines, "mongodb") && hasListeningPort(ssPart, mongoPort) {
		if h.Overrides["mongodb_port"] != "" {
			detectedEngines = append(detectedEngines, "mongodb")
		} else {
			// Verify port is backed by an active listener (not a dead pod port-forward)
			chkOut, _ := r.RunCommand(ctx, fmt.Sprintf("curl -s -m 1 http://127.0.0.1:%s/ >/dev/null 2>&1; rc=$?; [ \"$rc\" -ne 7 ] && echo \"MONGO_ALIVE\" || true", mongoPort))
			if strings.Contains(chkOut, "MONGO_ALIVE") {
				detectedEngines = append(detectedEngines, "mongodb")
			}
		}
	}
	if strings.Contains(sockPart, "COUCH_SOCK:") {
		if !contains(detectedEngines, "couchdb") {
			detectedEngines = append(detectedEngines, "couchdb")
		}
	} else if !contains(detectedEngines, "couchdb") && hasListeningPort(ssPart, couchPort) {
		if h.Overrides["couchdb_port"] != "" {
			detectedEngines = append(detectedEngines, "couchdb")
		} else {
			chkOut, _ := r.RunCommand(ctx, fmt.Sprintf("curl -s -m 1 http://127.0.0.1:%s/ >/dev/null 2>&1; rc=$?; [ \"$rc\" -ne 7 ] && echo \"COUCH_ALIVE\" || true", couchPort))
			if strings.Contains(chkOut, "COUCH_ALIVE") {
				detectedEngines = append(detectedEngines, "couchdb")
			}
		}
	}
	if strings.Contains(sockPart, "ROCKSDB_DIR:") && !contains(detectedEngines, "rocksdb") {
		detectedEngines = append(detectedEngines, "rocksdb")
	}

	// 3. Also check host overrides
	if eng, ok := h.Overrides["nosql_engine"]; ok && eng != "" && !contains(detectedEngines, eng) {
		detectedEngines = append(detectedEngines, eng)
	}
	if (h.Overrides["redis_host"] != "" || h.Overrides["redis_port"] != "" || h.Overrides["redis_socket"] != "" || h.Overrides["redis_user"] != "" || h.Overrides["redis_database"] != "") && !contains(detectedEngines, "redis") {
		detectedEngines = append(detectedEngines, "redis")
	}
	if (h.Overrides["valkey_host"] != "" || h.Overrides["valkey_port"] != "" || h.Overrides["valkey_socket"] != "" || h.Overrides["valkey_user"] != "" || h.Overrides["valkey_database"] != "") && !contains(detectedEngines, "valkey") {
		detectedEngines = append(detectedEngines, "valkey")
	}
	if (h.Overrides["mongodb_uri"] != "" || h.Overrides["mongodb_host"] != "" || h.Overrides["mongodb_port"] != "" || h.Overrides["mongodb_socket"] != "" || h.Overrides["mongodb_user"] != "" || h.Overrides["mongodb_database"] != "") && !contains(detectedEngines, "mongodb") {
		detectedEngines = append(detectedEngines, "mongodb")
	}
	if (h.Overrides["couchdb_host"] != "" || h.Overrides["couchdb_port"] != "" || h.Overrides["couchdb_socket"] != "" || h.Overrides["couchdb_user"] != "" || h.Overrides["couchdb_database"] != "") && !contains(detectedEngines, "couchdb") {
		detectedEngines = append(detectedEngines, "couchdb")
	}
	if (h.Overrides["rocksdb_path"] != "" || h.Overrides["rocksdb_database"] != "") && !contains(detectedEngines, "rocksdb") {
		detectedEngines = append(detectedEngines, "rocksdb")
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

	det, _ := p.Probe(ctx, r, h)
	detLower := strings.ToLower(det.Status + " " + det.Details)

	// 1. Inspect Redis / Valkey
	rCli, _ := r.RunCommand(ctx, "command -v redis-cli || command -v valkey-cli || true")
	rCli = strings.TrimSpace(rCli)
	isRedisDet := strings.Contains(detLower, "redis") || strings.Contains(detLower, "valkey")
	if rCli != "" || isRedisDet || h.Overrides["redis_host"] != "" || h.Overrides["redis_port"] != "" || h.Overrides["redis_socket"] != "" || h.Overrides["valkey_port"] != "" || h.Overrides["valkey_socket"] != "" {
		st := inspectRedis(ctx, r, rCli, h, isRedisDet)
		if st != nil {
			engines = append(engines, *st)
		}
	}

	// 2. Inspect MongoDB
	mCli, _ := r.RunCommand(ctx, "command -v mongosh || command -v mongo || true")
	mCli = strings.TrimSpace(mCli)
	isMongoDet := strings.Contains(detLower, "mongodb")
	if mCli != "" || isMongoDet || h.Overrides["mongodb_uri"] != "" || h.Overrides["mongodb_host"] != "" || h.Overrides["mongodb_port"] != "" || h.Overrides["mongodb_socket"] != "" {
		st := inspectMongo(ctx, r, mCli, h, isMongoDet)
		if st != nil {
			engines = append(engines, *st)
		}
	}

	// 3. Inspect CouchDB
	couchPath, _ := r.RunCommand(ctx, "command -v couchdb || curl -s -m 2 http://127.0.0.1:5984/ 2>/dev/null || true")
	isCouchDet := strings.Contains(detLower, "couchdb")
	if isCouchDet || h.Overrides["couchdb_host"] != "" || h.Overrides["couchdb_port"] != "" || h.Overrides["couchdb_socket"] != "" || (strings.TrimSpace(couchPath) != "" && (strings.Contains(couchPath, "couchdb") || strings.Contains(couchPath, "Welcome"))) {
		st := inspectCouchDB(ctx, r, h, isCouchDet)
		if st != nil {
			engines = append(engines, *st)
		}
	}

	// 4. Inspect RocksDB (CLI / files)
	rocksCli, _ := r.RunCommand(ctx, "command -v ldb || command -v rocksdb_dump || true")
	isRocksDet := strings.Contains(detLower, "rocksdb")
	if isRocksDet || strings.TrimSpace(rocksCli) != "" || h.Overrides["rocksdb_path"] != "" {
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

func inspectRedis(ctx context.Context, r plugin.Runner, cli string, h plugin.HostFacts, detected bool) *EngineStatus {
	port := firstNonEmpty(h.Overrides["redis_port"], h.Overrides["valkey_port"], "6379")
	socketOverride := firstNonEmpty(h.Overrides["redis_socket"], h.Overrides["valkey_socket"])
	metrics := map[string]any{"port": port}
	if socketOverride != "" {
		metrics["socket"] = socketOverride
	}
	if host := firstNonEmpty(h.Overrides["redis_host"], h.Overrides["valkey_host"]); host != "" {
		metrics["host"] = host
	}
	if u := firstNonEmpty(h.Overrides["redis_user"], h.Overrides["valkey_user"]); u != "" {
		metrics["user"] = u
	}
	if db := firstNonEmpty(h.Overrides["redis_database"], h.Overrides["valkey_database"]); db != "" {
		metrics["database"] = db
	}

	userOpt := ""
	if u := firstNonEmpty(h.Overrides["redis_user"], h.Overrides["valkey_user"]); u != "" {
		userOpt = fmt.Sprintf("--user %s ", escapeArg(u))
	}
	authOpt := ""
	if pwd := firstNonEmpty(h.Overrides["redis_password"], h.Overrides["valkey_password"]); pwd != "" {
		authOpt = fmt.Sprintf("-a %s ", escapeArg(pwd))
	}
	portOpt := ""
	if p := firstNonEmpty(h.Overrides["redis_port"], h.Overrides["valkey_port"]); p != "" {
		portOpt = fmt.Sprintf("-p %s ", p)
	}
	hostOpt := ""
	if host := firstNonEmpty(h.Overrides["redis_host"], h.Overrides["valkey_host"]); host != "" {
		hostOpt = fmt.Sprintf("-h %s ", host)
	}
	dbOpt := ""
	if db := firstNonEmpty(h.Overrides["redis_database"], h.Overrides["valkey_database"]); db != "" {
		dbNum := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(db)), "db")
		if dbNum != "" {
			dbOpt = fmt.Sprintf("-n %s ", dbNum)
		}
	}

	runRedis := func(redisArgs string) string {
		if cli != "" {
			if socketOverride != "" {
				sockCmd := fmt.Sprintf("%s -s %s %s%s%s%s 2>/dev/null || true", cli, escapeArg(socketOverride), userOpt, authOpt, dbOpt, redisArgs)
				if out, err := r.RunCommand(ctx, sockCmd); err == nil && strings.TrimSpace(out) != "" {
					return out
				}
			}
			cmd := fmt.Sprintf("%s %s%s%s%s%s%s 2>/dev/null || true", cli, hostOpt, portOpt, userOpt, authOpt, dbOpt, redisArgs)
			if out, err := r.RunCommand(ctx, cmd); err == nil && strings.TrimSpace(out) != "" {
				return out
			}
			// Try common Unix sockets
			for _, sock := range []string{"/run/redis/redis.sock", "/var/run/redis/redis.sock", "/run/redis/redis-server.sock", "/run/valkey/valkey.sock", "/var/run/valkey/valkey.sock", "/tmp/redis.sock"} {
				tryCmd := fmt.Sprintf("[ -S %s ] && (sudo -n %s -s %s %s%s%s%s 2>/dev/null || %s -s %s %s%s%s%s 2>/dev/null) || true",
					escapeArg(sock), cli, escapeArg(sock), userOpt, authOpt, dbOpt, redisArgs, cli, escapeArg(sock), userOpt, authOpt, dbOpt, redisArgs)
				if out, err := r.RunCommand(ctx, tryCmd); err == nil && strings.TrimSpace(out) != "" {
					metrics["socket"] = sock
					return out
				}
			}
		}
		// Container exec fallback
		ctrCmd := fmt.Sprintf(
			"for rt in 'sudo -n podman' podman 'sudo -n docker' docker; do "+
				"cid=$($rt ps --format '{{.Names}} {{.Image}}' 2>/dev/null | grep -iE 'redis|valkey|keydb|dragonfly' | awk '{print $1}' | head -n1); "+
				"if [ -n \"$cid\" ]; then $rt exec \"$cid\" sh -c \"(redis-cli %s || valkey-cli %s)\" 2>/dev/null && break; fi; "+
				"done || true",
			redisArgs, redisArgs,
		)
		out, _ := r.RunCommand(ctx, ctrCmd)
		return out
	}

	out := runRedis("INFO")
	if strings.TrimSpace(out) == "" {
		status := "inactive"
		if detected {
			status = "active"
		}
		return &EngineStatus{
			Engine:     "redis",
			Status:     status,
			Databases:  []string{},
			Users:      []EngineUser{},
			Metrics:    metrics,
			DataFormat: "keyvalue",
		}
	}

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
			if k == "redis_version" || k == "valkey_version" {
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
	aclOut := runRedis("ACL LIST")
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

func inspectMongo(ctx context.Context, r plugin.Runner, cli string, h plugin.HostFacts, detected bool) *EngineStatus {
	evalFlag := "--eval"
	if cli == "mongo" {
		evalFlag = "--eval"
	}
	port := firstNonEmpty(h.Overrides["mongodb_port"], "27017")
	socketOverride := h.Overrides["mongodb_socket"]
	host := firstNonEmpty(h.Overrides["mongodb_host"], "127.0.0.1")
	defaultDB := h.Overrides["mongodb_database"]
	uri := h.Overrides["mongodb_uri"]
	if uri == "" {
		if defaultDB != "" {
			uri = fmt.Sprintf("mongodb://%s:%s/%s", host, port, defaultDB)
		} else {
			uri = fmt.Sprintf("mongodb://%s:%s", host, port)
		}
	}
	metrics := map[string]any{"uri": uri, "port": port, "host": host}
	if socketOverride != "" {
		metrics["socket"] = socketOverride
	}
	if u := h.Overrides["mongodb_user"]; u != "" {
		metrics["user"] = u
	}
	if defaultDB != "" {
		metrics["database"] = defaultDB
	}

	authFlags := ""
	if u := h.Overrides["mongodb_user"]; u != "" {
		authFlags += fmt.Sprintf(" -u %s", escapeArg(u))
	}
	if pwd := h.Overrides["mongodb_password"]; pwd != "" {
		authFlags += fmt.Sprintf(" -p %s", escapeArg(pwd))
	}

	runMongo := func(jsExpr string) string {
		if cli != "" {
			target := uri
			if socketOverride != "" && h.Overrides["mongodb_uri"] == "" {
				target = fmt.Sprintf("--host %s", escapeArg(socketOverride))
				if defaultDB != "" {
					target += " " + escapeArg(defaultDB)
				}
			}
			cmd := fmt.Sprintf("%s --quiet %s%s %s %s 2>/dev/null || true", cli, target, authFlags, evalFlag, escapeArg(jsExpr))
			if out, err := r.RunCommand(ctx, cmd); err == nil && strings.TrimSpace(out) != "" {
				return out
			}
		}
		// Container exec fallback
		ctrCmd := fmt.Sprintf(
			"for rt in 'sudo -n podman' podman 'sudo -n docker' docker; do "+
				"cid=$($rt ps --format '{{.Names}} {{.Image}}' 2>/dev/null | grep -iE 'mongo' | awk '{print $1}' | head -n1); "+
				"if [ -n \"$cid\" ]; then $rt exec \"$cid\" sh -c \"(mongosh --quiet --eval %s || mongo --quiet --eval %s)\" 2>/dev/null && break; fi; "+
				"done || true",
			escapeArg(jsExpr), escapeArg(jsExpr),
		)
		out, _ := r.RunCommand(ctx, ctrCmd)
		return out
	}

	// Run db.adminCommand({listDatabases: 1})
	out := runMongo("JSON.stringify(db.adminCommand({listDatabases: 1}))")
	if strings.TrimSpace(out) == "" {
		status := "inactive"
		if detected {
			status = "active"
		}
		return &EngineStatus{
			Engine:     "mongodb",
			Status:     status,
			Databases:  []string{},
			Users:      []EngineUser{},
			Metrics:    metrics,
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
	uOut := runMongo("JSON.stringify(db.getSiblingDB(\"admin\").getUsers())")
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
	verOut := runMongo("db.version()")
	version := strings.TrimSpace(verOut)

	return &EngineStatus{
		Engine:     "mongodb",
		Status:     "active",
		Version:    version,
		Databases:  databases,
		Users:      users,
		Metrics:    metrics,
		DataFormat: "document",
	}
}

func inspectCouchDB(ctx context.Context, r plugin.Runner, h plugin.HostFacts, detected bool) *EngineStatus {
	host := h.Overrides["couchdb_host"]
	if host == "" {
		host = "127.0.0.1"
	}
	port := firstNonEmpty(h.Overrides["couchdb_port"], "5984")
	socketOverride := h.Overrides["couchdb_socket"]
	base := fmt.Sprintf("http://%s:%s", host, port)
	metrics := map[string]any{"url": base, "port": port, "host": host}
	if socketOverride != "" {
		metrics["socket"] = socketOverride
	}
	if u := h.Overrides["couchdb_user"]; u != "" {
		metrics["user"] = u
	}
	if db := h.Overrides["couchdb_database"]; db != "" {
		metrics["database"] = db
	}

	curlExtra := ""
	if socketOverride != "" {
		curlExtra += fmt.Sprintf(" --unix-socket %s", escapeArg(socketOverride))
	}
	if u := h.Overrides["couchdb_user"]; u != "" {
		pwd := h.Overrides["couchdb_password"]
		curlExtra += fmt.Sprintf(" -u %s", escapeArg(fmt.Sprintf("%s:%s", u, pwd)))
	}

	out, err := r.RunCommand(ctx, fmt.Sprintf("curl -s -m 3%s %s/_all_dbs 2>/dev/null || true", curlExtra, base))
	databases := make([]string, 0)
	if err == nil && strings.HasPrefix(strings.TrimSpace(out), "[") {
		_ = json.Unmarshal([]byte(strings.TrimSpace(out)), &databases)
	}

	// Root endpoint for version
	rootOut, _ := r.RunCommand(ctx, fmt.Sprintf("curl -s -m 3%s %s/ 2>/dev/null || true", curlExtra, base))
	var rootInfo struct {
		Version string `json:"version"`
	}
	_ = json.Unmarshal([]byte(strings.TrimSpace(rootOut)), &rootInfo)

	status := "active"
	if rootInfo.Version == "" && len(databases) == 0 && !detected {
		status = "inactive"
	}

	return &EngineStatus{
		Engine:     "couchdb",
		Status:     status,
		Version:    rootInfo.Version,
		Databases:  databases,
		Users:      []EngineUser{{Username: "admin", Roles: []string{"_admin"}}},
		Metrics:    metrics,
		DataFormat: "document",
	}
}

func inspectRocksDB(ctx context.Context, r plugin.Runner, cli string, h plugin.HostFacts) *EngineStatus {
	dbPath := firstNonEmpty(h.Overrides["rocksdb_path"], h.Overrides["rocksdb_database"], "/var/lib/rocksdb")
	metrics := map[string]any{"path": dbPath, "cli": cli}
	if u := h.Overrides["rocksdb_user"]; u != "" {
		metrics["user"] = u
	}

	return &EngineStatus{
		Engine:     "rocksdb",
		Status:     "active",
		Version:    "embedded",
		Databases:  []string{dbPath},
		Users:      []EngineUser{{Username: "system", Roles: []string{"file_owner"}}},
		Metrics:    metrics,
		DataFormat: "text",
	}
}

// ManagementActions returns the v2 management action specifications for NoSQL databases.
func (p *Plugin) ManagementActions() []newapi.ManagementActionSpec {
	return []newapi.ManagementActionSpec{
		{
			ActionSpec: plugin.ActionSpec{
				Name:        "query",
				Label:       "Run Query / Command",
				Description: "Execute a raw query, command, or scan on the chosen NoSQL engine (Mongo find/eval, Redis commands, CouchDB REST, RocksDB scan)",
				Options: []plugin.OptionSchema{
					{Name: "engine", Type: "select", Required: true, Enum: []string{"redis", "valkey", "mongodb", "couchdb", "rocksdb"}, Description: "Target engine"},
					{Name: "database", Type: "string", Required: false, Description: "Database, keyspace, or collection name"},
					{Name: "command", Type: "string", Required: true, Description: "Query or command string (e.g. 'GET key1', 'db.users.find().limit(5)', 'GET /_all_dbs', 'scan')"},
					{Name: "limit", Type: "int", Required: false, Default: 50, Description: "Maximum rows/keys to return"},
				},
			},
			Category:         newapi.ActionCategoryDiagnostic,
			Idempotent:       false,
			Dangerous:        false,
			InvalidatesKinds: []plugin.Kind{plugin.KindDatabase},
		},
		{
			ActionSpec: plugin.ActionSpec{
				Name:        "database_create",
				Label:       "Create Database / Collection",
				Description: "Create a new database, collection, or keyspace in the NoSQL engine",
				Options: []plugin.OptionSchema{
					{Name: "engine", Type: "select", Required: true, Enum: []string{"redis", "valkey", "mongodb", "couchdb", "rocksdb"}, Description: "Target engine"},
					{Name: "name", Type: "string", Required: true, Description: "Database or collection name"},
				},
			},
			Category:         newapi.ActionCategoryLifecycle,
			Idempotent:       true,
			Dangerous:        false,
			InvalidatesKinds: []plugin.Kind{plugin.KindDatabase},
		},
		{
			ActionSpec: plugin.ActionSpec{
				Name:        "database_drop",
				Label:       "Drop Database / Collection",
				Description: "Drop a database, collection, or keyspace from the NoSQL engine",
				Options: []plugin.OptionSchema{
					{Name: "engine", Type: "select", Required: true, Enum: []string{"redis", "valkey", "mongodb", "couchdb", "rocksdb"}, Description: "Target engine"},
					{Name: "name", Type: "string", Required: true, Description: "Database or collection name to drop"},
				},
			},
			Category:         newapi.ActionCategoryLifecycle,
			Idempotent:       true,
			Dangerous:        true,
			ConfirmPrompt:    "Permanently drop this NoSQL database, collection, or keyspace?",
			InvalidatesKinds: []plugin.Kind{plugin.KindDatabase},
		},
		{
			ActionSpec: plugin.ActionSpec{
				Name:        "user_create",
				Label:       "Create / Update User",
				Description: "Create or modify a database user or ACL in the NoSQL engine",
				Options: []plugin.OptionSchema{
					{Name: "engine", Type: "select", Required: true, Enum: []string{"redis", "valkey", "mongodb", "couchdb"}, Description: "Target engine"},
					{Name: "username", Type: "string", Required: true, Description: "User or ACL identifier"},
					{Name: "password", Type: "string", Required: false, Secret: true, Description: "User password (secret, vault-masked)"},
					{Name: "passwordVaultKey", Type: "string", Required: false, Description: "Ansible Vault variable name for the password"},
					{Name: "roles", Type: "string", Required: false, Default: "readWrite", Description: "Roles or permissions (comma or space separated)"},
					{Name: "database", Type: "string", Required: false, Default: "admin", Description: "Target database for user"},
				},
			},
			Category:         newapi.ActionCategorySecurity,
			Idempotent:       true,
			Dangerous:        false,
			InvalidatesKinds: []plugin.Kind{plugin.KindDatabase},
		},
		{
			ActionSpec: plugin.ActionSpec{
				Name:        "user_delete",
				Label:       "Delete User / ACL",
				Description: "Remove a database user or ACL rule",
				Options: []plugin.OptionSchema{
					{Name: "engine", Type: "select", Required: true, Enum: []string{"redis", "valkey", "mongodb", "couchdb"}, Description: "Target engine"},
					{Name: "username", Type: "string", Required: true, Description: "Username or ACL name to delete"},
					{Name: "database", Type: "string", Required: false, Default: "admin", Description: "Target database"},
				},
			},
			Category:         newapi.ActionCategorySecurity,
			Idempotent:       true,
			Dangerous:        true,
			ConfirmPrompt:    "Permanently delete this NoSQL database user or ACL rule?",
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
	for _, spec := range p.ManagementActions() {
		if strings.EqualFold(spec.Name, norm) {
			return newapi.ValidateManagementOptions(spec, opts)
		}
	}
	return fmt.Errorf("unsupported nosql action: %s", action)
}

// CompileTask adapts a typed v2 TaskRequest to the plugin's declarative Ansible TaskSpec compiler.
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

// Compile converts a declarative NoSQL action into a TaskSpec.
// It prioritizes native modules (community.mongodb, community.general) and Ansible tasks,
// masking all secrets and credentials in task metadata.
func (p *Plugin) Compile(h plugin.HostFacts, action string, opts map[string]any) (plugin.TaskSpec, error) {
	norm := strings.ToLower(strings.TrimSpace(action))
	engine := strings.ToLower(optString(opts, "engine"))
	if engine == "" {
		engine = defaultNoSQLEngine()
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
		limit := optIntDefault(opts, "limit", defaultNoSQLQueryLimit())

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
