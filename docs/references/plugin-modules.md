# Database Plugin Modules — Unified Driver Patterns

Reference for the `database/unified` plugin (`backend-go/plugins/database/unified`).
It replaces ad-hoc vendor CLI invocation (`psql`, `mysql`, `sqlcmd`, `redis-cli`,
`mongosh`, `ldb`) with native Go drivers behind one query contract.

## Why

The SQL and NoSQL views previously built a shell command string and ran it over
`/api/terminal/exec`. That required the client binary to exist on the target and
made structured results hard to obtain. The unified plugin keeps the CLI path as
a fallback but prefers a real driver connection, returning normalized JSON.

## Interface

`pkg/plugin/types.go` defines the optional contract:

```go
type Querier interface {
    Query(ctx context.Context, r Runner, h HostFacts, req QueryRequest) (QueryResult, error)
}

type QueryRequest struct {
    Engine   string // postgres|postgresql|mysql|mariadb|mssql|redis|mongodb|rocksdb
    Host     string
    Port     int
    User     string
    Password string
    Database string
    Socket   string
    Query    string
    Limit    int
}

type QueryResult struct {
    Engine     string
    Driver     string // "go"
    Columns    []string
    Rows       [][]any
    Raw        string
    Affected   int64
    DurationMs int64
    Truncated  bool
    Fallback   string // "cli" => retry through the vendor CLI
    Error      string
}
```

Route: `POST /api/nodes/:id/plugins/:kind/:name/query` →
`handlePluginQuery` in `pkg/plugin/api.go`. The handler always returns HTTP 200
with a `QueryResult`; only malformed requests (empty query, bad JSON) return 4xx.

## Drivers compiled into the daemon

| Engine | Go driver | Notes |
|---|---|---|
| PostgreSQL | `github.com/jackc/pgx/v5/stdlib` | DSN via URL, `sslmode=disable`, unix socket through `host=` |
| MySQL / MariaDB | `github.com/go-sql-driver/mysql` | `mysql.Config.FormatDSN()` handles escaping; tcp or unix |
| MSSQL | `github.com/microsoft/go-mssqldb` | `sqlserver://` URL, `encrypt=disable` for homelab |
| Redis | `github.com/redis/go-redis/v9` | `Do` for commands, `Scan` for `SCAN`/`--scan --pattern` |
| MongoDB | `go.mongodb.org/mongo-driver/v2` | collection listing, raw JSON commands, simple find/count |
| RocksDB | none | always `Fallback: "cli"` (requires CGO/`ldb`) |

## Connection resolution

`resolveEngineConfig` in `unified.go` merges request fields over node overrides.
Override prefixes per engine: `postgres_`, `postgresql_`, `mariadb_`, `mysql_`,
`mssql_`, `sqlserver_`, `redis_`, `mongodb_`, `mongo_`, `rocksdb_`.

Recognised suffixes: `_host`, `_port`, `_socket`, `_user`, `_password`,
`_database`, `_path`. Defaults: host `HostRoute`, port per engine, users
`postgres`/`root`/`sa`, databases `postgres`/`admin`.

Security: the browser sends engine/database/query only. Passwords stay in the
node's `pluginOverrides` and are read server-side; they are never included in
`QueryResult`, logs or task specs.

## Query semantics

- SQL read statements (`SELECT`, `WITH`, `SHOW`, `EXPLAIN`, `DESCRIBE`, `VALUES`,
  `TABLE`, `PRAGMA`) return `Columns`/`Rows`; everything else uses `ExecContext`
  and returns `Affected`. Rows are capped at `DefaultRowLimit` (1000) with
  `Truncated: true`.
- Redis returns `Raw` text in redis-cli-ish form (one element per line); hashes
  and sorted sets keep field/value pairs on alternating lines so the existing
  key-value renderer works unchanged.
- MongoDB returns JSON in `Raw`; the document renderer parses it. Supported:
  `db.getCollectionNames()`, raw JSON commands, `db.<coll>.find({...})`,
  `db.<coll>.countDocuments({...})`.
- Any driver/connection error returns `Fallback: "cli"` and `Error` with a nil
  error. Unsupported engines and expressions do the same. Never return synthetic
  rows or empty successes.

## Frontend fallback

`SQLManagementView.executeQueryInternal` and `NoSQLView.runQuery` POST to the
query endpoint first. If the response has `fallback` or `error`, they build the
existing vendor CLI command and call `/api/terminal/exec`. Browser helpers
(`handleBrowseRedisKeys`, `handleLoadMongoCollections`, dumps) still use the CLI
path; extend them the same way when adding driver support.

## Adding an engine

1. Add an `engineSpec` (name, override keys, default port, kind, driver id).
2. Implement `query<Engine>` in `query.go` and its DSN/URI builder.
3. Map errors to `Fallback: "cli"`; cap result sizes.
4. Add tests in `unified_test.go` for config merging, DSN output, argument
   splitting/formatting and fallback behaviour. Use unreachable ports (e.g.
   `postgres_port=1`) to exercise failure without mocks.
5. Run the four gates from `AGENTS.md`.
