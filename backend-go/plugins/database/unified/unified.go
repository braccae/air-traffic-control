// Package unified implements a database plugin that executes ad-hoc queries
// through native Go drivers instead of shelling out to vendor CLI tools.
//
// It is the "SQLAlchemy-style" single API for many database engines requested
// by the database views: one Querier contract (database/unified/query) resolves
// connection settings from node pluginOverrides and dispatches to PostgreSQL,
// MySQL/MariaDB, MSSQL, Redis or MongoDB drivers compiled into the daemon.
// Engines without a pure-Go driver (RocksDB/ldb) report Fallback="cli" so the
// existing CLI transport keeps working.
package unified

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"air-traffic-control/backend/pkg/plugin"
)

const (
	// PluginID is the registry id of the unified database driver plugin.
	PluginID = "database/unified"
	// DriverName is the stable name reported in QueryResult.Driver.
	DriverName = "go"
	// DefaultRowLimit caps read queries when the request does not specify a limit.
	DefaultRowLimit = 1000
)

// Compile-time interface assertions.
var (
	_ plugin.Plugin  = (*Plugin)(nil)
	_ plugin.Reader  = (*Plugin)(nil)
	_ plugin.Actor   = (*Plugin)(nil)
	_ plugin.Querier = (*Plugin)(nil)
)

// engineSpec describes one supported engine and how its connection overrides
// are named on the node record.
type engineSpec struct {
	Name         string
	OverrideKeys []string
	DefaultPort  int
	Kind         string // "sql", "redis", "mongo" or "none"
	Driver       string // Go driver identifier reported to the UI
}

var engineSpecs = []engineSpec{
	{Name: "postgres", OverrideKeys: []string{"postgres", "postgresql"}, DefaultPort: 5432, Kind: "sql", Driver: "pgx/v5"},
	{Name: "mariadb", OverrideKeys: []string{"mariadb", "mysql"}, DefaultPort: 3306, Kind: "sql", Driver: "go-sql-driver/mysql"},
	{Name: "mssql", OverrideKeys: []string{"mssql", "sqlserver"}, DefaultPort: 1433, Kind: "sql", Driver: "microsoft/go-mssqldb"},
	{Name: "redis", OverrideKeys: []string{"redis"}, DefaultPort: 6379, Kind: "redis", Driver: "redis/go-redis/v9"},
	{Name: "mongodb", OverrideKeys: []string{"mongodb", "mongo"}, DefaultPort: 27017, Kind: "mongo", Driver: "mongo-driver/v2"},
	{Name: "rocksdb", OverrideKeys: []string{"rocksdb"}, DefaultPort: 0, Kind: "none", Driver: ""},
}

func init() {
	_ = plugin.Register(New())
}

// New constructs a new unified database driver plugin instance.
func New() *Plugin { return &Plugin{} }

// Plugin implements plugin.Plugin, plugin.Reader, plugin.Actor and plugin.Querier.
type Plugin struct{}

// Meta returns plugin registry metadata.
func (p *Plugin) Meta() plugin.Meta {
	return plugin.Meta{
		ID:          PluginID,
		Kind:        plugin.KindDatabase,
		Name:        "unified",
		Label:       "Unified Database Drivers",
		Icon:        "database",
		Description: "Executes queries through native Go drivers (PostgreSQL, MySQL/MariaDB, MSSQL, Redis, MongoDB) with automatic CLI fallback.",
		Order:       1,
	}
}

// Compatible reports that the driver layer works on any host OS reachable over
// TCP or a local unix socket.
func (p *Plugin) Compatible(h plugin.HostFacts) bool {
	return true
}

// Requirements returns no host packages: the drivers are compiled into the
// daemon and require nothing on the target node. CLI clients are optional and
// only installed on demand through the install_cli_client action.
func (p *Plugin) Requirements() plugin.Requirements {
	return plugin.Requirements{}
}

// EngineRead describes per-engine connectivity as seen by the Go driver layer.
type EngineRead struct {
	Engine         string `json:"engine"`
	Driver         string `json:"driver,omitempty"`
	Host           string `json:"host"`
	Port           int    `json:"port,omitempty"`
	Socket         string `json:"socket,omitempty"`
	Reachable      bool   `json:"reachable"`
	AuthConfigured bool   `json:"authConfigured"`
	Database       string `json:"database,omitempty"`
	CLI            string `json:"cli,omitempty"`
	Error          string `json:"error,omitempty"`
}

// ReadData is returned by Read for the plugin's diagnostics tab.
type ReadData struct {
	Drivers []string     `json:"drivers"`
	Engines []EngineRead `json:"engines"`
}

// Probe checks whether at least one configured engine is reachable through a Go
// driver or has a vendor CLI installed for fallback.
func (p *Plugin) Probe(ctx context.Context, r plugin.Runner, h plugin.HostFacts) (plugin.Detection, error) {
	configured := 0
	reachable := 0
	cliPresent := 0
	var detailParts []string

	for _, spec := range engineSpecs {
		cfg, ok := configuredEngine(spec, h)
		if !ok {
			continue
		}
		configured++
		okReach, _ := checkReachable(cfg, 800*time.Millisecond)
		if okReach {
			reachable++
			detailParts = append(detailParts, fmt.Sprintf("%s driver reachable at %s", spec.Name, cfg.address()))
		}
		if cli := detectCLI(ctx, r, spec.Name); cli != "" {
			cliPresent++
		}
	}

	if configured == 0 {
		return plugin.Detection{
			Detected: false,
			Status:   "NO CONFIG",
			Details:  "No database engine overrides configured; set <engine>_host/_port/_user/_database in Edit Node.",
		}, nil
	}

	det := plugin.Detection{
		Detected: reachable > 0 || cliPresent > 0,
		Status:   fmt.Sprintf("%d/%d reachable", reachable, configured),
		Details:  strings.Join(detailParts, "; "),
	}
	if det.Details == "" {
		det.Details = "Configured engines are not reachable over TCP; queries will use CLI fallback."
	}
	return det, nil
}

// Read returns per-engine driver connectivity for diagnostics.
func (p *Plugin) Read(ctx context.Context, r plugin.Runner, h plugin.HostFacts) (any, error) {
	out := ReadData{Drivers: make([]string, 0, len(engineSpecs))}
	for _, spec := range engineSpecs {
		if spec.Driver == "" {
			continue
		}
		out.Drivers = append(out.Drivers, fmt.Sprintf("%s (%s)", spec.Name, spec.Driver))
	}
	for _, spec := range engineSpecs {
		cfg, ok := configuredEngine(spec, h)
		if !ok {
			continue
		}
		reachable, connErr := checkReachable(cfg, time.Second)
		entry := EngineRead{
			Engine:         spec.Name,
			Driver:         spec.Driver,
			Host:           cfg.Host,
			Port:           cfg.Port,
			Socket:         cfg.Socket,
			Reachable:      reachable,
			AuthConfigured: cfg.User != "",
			Database:       cfg.Database,
			CLI:            detectCLI(ctx, r, spec.Name),
			Error:          connErr,
		}
		out.Engines = append(out.Engines, entry)
	}
	return out, nil
}

// Actions exposes the optional CLI fallback client installation.
func (p *Plugin) Actions() []plugin.ActionSpec {
	return []plugin.ActionSpec{{
		Name:        "install_cli_client",
		Label:       "Install CLI client (fallback)",
		Description: "Installs the vendor command-line client used when the native Go driver cannot reach the engine.",
		Options: []plugin.OptionSchema{{
			Name:        "engine",
			Type:        "select",
			Required:    true,
			Enum:        []string{"postgres", "mariadb", "mssql", "redis", "mongodb", "rocksdb"},
			Description: "Database engine whose CLI client should be installed.",
		}},
	}}
}

// Compile builds a declarative package-install task for the selected CLI client.
func (p *Plugin) Compile(h plugin.HostFacts, action string, opts map[string]any) (plugin.TaskSpec, error) {
	if action != "install_cli_client" {
		return plugin.TaskSpec{}, fmt.Errorf("unsupported action %q", action)
	}
	engine, _ := opts["engine"].(string)
	engine = normalizeEngine(engine)
	if engine == "" {
		return plugin.TaskSpec{}, fmt.Errorf("option %q is required", "engine")
	}
	pkgs := cliPackages(engine, h.ActivePackage)
	if len(pkgs) == 0 {
		return plugin.TaskSpec{}, fmt.Errorf("no CLI client packages known for engine %q", engine)
	}
	return plugin.TaskSpec{
		Name: fmt.Sprintf("Install %s CLI client (fallback)", engine),
		Steps: []plugin.TaskStep{{
			Name:   "Install vendor CLI packages",
			Module: "ansible.builtin.package",
			Params: map[string]any{"name": pkgs, "state": "present"},
		}},
	}, nil
}

// connConfig is the resolved connection configuration for one engine.
type connConfig struct {
	Engine   string
	Kind     string
	Host     string
	Port     int
	User     string
	Password string
	Database string
	Socket   string
}

func (c connConfig) address() string {
	if c.Socket != "" {
		return "unix:" + c.Socket
	}
	return net.JoinHostPort(c.Host, strconv.Itoa(c.Port))
}

func specFor(engine string) engineSpec {
	for _, s := range engineSpecs {
		if s.Name == engine {
			return s
		}
	}
	return engineSpec{}
}

func normalizeEngine(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "postgres", "postgresql", "pgsql":
		return "postgres"
	case "mysql", "mariadb":
		return "mariadb"
	case "mssql", "sqlserver":
		return "mssql"
	case "redis":
		return "redis"
	case "mongodb", "mongo":
		return "mongodb"
	case "rocksdb":
		return "rocksdb"
	}
	return ""
}

// configuredEngine resolves connection settings for a spec from node overrides.
// It reports ok=false when no override for that engine is present.
func configuredEngine(spec engineSpec, h plugin.HostFacts) (connConfig, bool) {
	cfg := resolveEngineConfig(spec, plugin.QueryRequest{}, h)
	if cfg.Host == "" {
		cfg.Host = h.HostRoute
	}
	if cfg.Host == "" {
		cfg.Host = "127.0.0.1"
	}
	if cfg.Port == 0 {
		cfg.Port = spec.DefaultPort
	}
	configured := false
	for _, key := range spec.OverrideKeys {
		for _, suffix := range []string{"_host", "_port", "_socket", "_user", "_database", "_path"} {
			if strings.TrimSpace(h.Overrides[key+suffix]) != "" {
				configured = true
				break
			}
		}
		if configured {
			break
		}
	}
	return cfg, configured
}

// resolveEngineConfig merges the request with node overrides. Request fields
// take precedence; stored overrides fill the gaps so secrets stay server-side.
func resolveEngineConfig(spec engineSpec, req plugin.QueryRequest, h plugin.HostFacts) connConfig {
	cfg := connConfig{Engine: spec.Name, Kind: spec.Kind}
	cfg.Host = strings.TrimSpace(req.Host)
	cfg.User = strings.TrimSpace(req.User)
	cfg.Password = req.Password
	cfg.Database = strings.TrimSpace(req.Database)
	cfg.Socket = strings.TrimSpace(req.Socket)
	cfg.Port = req.Port

	if cfg.Host == "" {
		cfg.Host = firstOverride(h.Overrides, spec.OverrideKeys, "_host")
	}
	if cfg.Host == "" {
		cfg.Host = strings.TrimSpace(h.HostRoute)
	}
	if cfg.Host == "" {
		cfg.Host = "127.0.0.1"
	}
	if cfg.Port == 0 {
		if raw := firstOverride(h.Overrides, spec.OverrideKeys, "_port"); raw != "" {
			if n, err := strconv.Atoi(raw); err == nil {
				cfg.Port = n
			}
		}
	}
	if cfg.Port == 0 {
		cfg.Port = spec.DefaultPort
	}
	if cfg.Socket == "" {
		cfg.Socket = firstOverride(h.Overrides, spec.OverrideKeys, "_socket")
	}
	if cfg.User == "" {
		cfg.User = firstOverride(h.Overrides, spec.OverrideKeys, "_user")
	}
	if cfg.User == "" {
		cfg.User = defaultUser(spec.Name)
	}
	if cfg.Password == "" {
		cfg.Password = firstOverride(h.Overrides, spec.OverrideKeys, "_password")
	}
	if cfg.Database == "" {
		cfg.Database = firstOverride(h.Overrides, spec.OverrideKeys, "_database")
	}
	if cfg.Database == "" {
		cfg.Database = defaultDatabase(spec.Name)
	}
	return cfg
}

func firstOverride(overrides map[string]string, keys []string, suffix string) string {
	if overrides == nil {
		return ""
	}
	for _, key := range keys {
		if v := strings.TrimSpace(overrides[key+suffix]); v != "" {
			return v
		}
	}
	return ""
}

func defaultUser(engine string) string {
	switch engine {
	case "postgres":
		return "postgres"
	case "mariadb":
		return "root"
	case "mssql":
		return "sa"
	}
	return ""
}

func defaultDatabase(engine string) string {
	switch engine {
	case "postgres":
		return "postgres"
	case "mongodb":
		return "admin"
	}
	return ""
}

func cliPackages(engine, pkgMgr string) []string {
	switch engine {
	case "postgres":
		switch pkgMgr {
		case "apt":
			return []string{"postgresql-client"}
		case "apk":
			return []string{"postgresql-client"}
		default:
			return []string{"postgresql"}
		}
	case "mariadb":
		switch pkgMgr {
		case "apt":
			return []string{"default-mysql-client"}
		case "apk":
			return []string{"mariadb-client"}
		default:
			return []string{"mariadb"}
		}
	case "mssql":
		return []string{"mssql-tools18"}
	case "redis":
		switch pkgMgr {
		case "apt":
			return []string{"redis-tools"}
		case "apk":
			return []string{"redis"}
		default:
			return []string{"redis"}
		}
	case "mongodb":
		return []string{"mongodb-mongosh"}
	case "rocksdb":
		return []string{"rocksdb-tools"}
	}
	return nil
}

// detectCLI returns the path of the vendor client binary, or "" when absent.
func detectCLI(ctx context.Context, r plugin.Runner, engine string) string {
	if r == nil {
		return ""
	}
	var cmd string
	switch engine {
	case "postgres":
		cmd = "command -v psql 2>/dev/null || true"
	case "mariadb":
		cmd = "command -v mariadb 2>/dev/null || command -v mysql 2>/dev/null || true"
	case "mssql":
		cmd = "command -v sqlcmd 2>/dev/null || command -v /opt/mssql-tools18/bin/sqlcmd 2>/dev/null || true"
	case "redis":
		cmd = "command -v redis-cli 2>/dev/null || true"
	case "mongodb":
		cmd = "command -v mongosh 2>/dev/null || command -v mongo 2>/dev/null || true"
	case "rocksdb":
		cmd = "command -v ldb 2>/dev/null || true"
	}
	if cmd == "" {
		return ""
	}
	out, err := r.RunCommand(ctx, cmd)
	if err != nil {
		return ""
	}
	first := strings.Split(strings.TrimSpace(out), "\n")[0]
	return strings.TrimSpace(first)
}

// checkReachable performs a cheap TCP or unix-socket dial to verify connectivity.
func checkReachable(cfg connConfig, timeout time.Duration) (bool, string) {
	network := "tcp"
	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	if cfg.Socket != "" {
		network = "unix"
		addr = cfg.Socket
	}
	conn, err := net.DialTimeout(network, addr, timeout)
	if err != nil {
		return false, err.Error()
	}
	_ = conn.Close()
	return true, ""
}
