package unified

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "github.com/microsoft/go-mssqldb"
	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"air-traffic-control/backend/pkg/plugin"
)

// Query executes an ad-hoc statement through the engine's native Go driver.
//
// Driver failures are not returned as errors: they populate QueryResult.Fallback
// with "cli" so the caller can retry through the vendor CLI without treating the
// request as a hard failure. Only malformed requests return a non-nil error.
func (p *Plugin) Query(ctx context.Context, r plugin.Runner, h plugin.HostFacts, req plugin.QueryRequest) (plugin.QueryResult, error) {
	engine := normalizeEngine(req.Engine)
	if engine == "" {
		return plugin.QueryResult{
			Engine:   req.Engine,
			Driver:   DriverName,
			Fallback: "cli",
			Error:    fmt.Sprintf("unsupported database engine %q", req.Engine),
		}, nil
	}
	if strings.TrimSpace(req.Query) == "" {
		return plugin.QueryResult{Engine: engine, Driver: DriverName}, fmt.Errorf("query is required")
	}

	spec := specFor(engine)
	cfg := resolveEngineConfig(spec, req, h)
	start := time.Now()

	var (
		res plugin.QueryResult
		err error
	)
	switch spec.Kind {
	case "sql":
		res, err = querySQL(ctx, cfg, req)
	case "redis":
		res, err = queryRedis(ctx, cfg, req)
	case "mongo":
		res, err = queryMongo(ctx, cfg, req)
	default:
		err = fmt.Errorf("engine %q has no pure-Go driver; use the vendor CLI", engine)
	}
	if err != nil {
		return plugin.QueryResult{
			Engine:     engine,
			Driver:     DriverName,
			Fallback:   "cli",
			Error:      err.Error(),
			DurationMs: time.Since(start).Milliseconds(),
		}, nil
	}

	res.Engine = engine
	if res.Driver == "" {
		res.Driver = DriverName
	}
	res.DurationMs = time.Since(start).Milliseconds()
	return res, nil
}

// ---------------------------------------------------------------
// SQL engines (PostgreSQL, MySQL/MariaDB, MSSQL)
// ---------------------------------------------------------------

func querySQL(ctx context.Context, cfg connConfig, req plugin.QueryRequest) (plugin.QueryResult, error) {
	db, err := openSQL(ctx, cfg)
	if err != nil {
		return plugin.QueryResult{}, err
	}
	defer func() { _ = db.Close() }()

	queryCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	stmt := strings.TrimSpace(req.Query)
	limit := req.Limit
	if limit <= 0 {
		limit = DefaultRowLimit
	}

	if sqlIsRead(stmt) {
		rows, err := db.QueryContext(queryCtx, stmt)
		if err != nil {
			return plugin.QueryResult{}, err
		}
		defer func() { _ = rows.Close() }()

		cols, err := rows.Columns()
		if err != nil {
			return plugin.QueryResult{}, err
		}
		result := plugin.QueryResult{Columns: cols, Rows: make([][]any, 0, 16)}
		for rows.Next() {
			if len(result.Rows) >= limit {
				result.Truncated = true
				break
			}
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				return plugin.QueryResult{}, err
			}
			row := make([]any, len(cols))
			for i, v := range vals {
				row[i] = normalizeValue(v)
			}
			result.Rows = append(result.Rows, row)
		}
		if err := rows.Err(); err != nil {
			return plugin.QueryResult{}, err
		}
		return result, nil
	}

	res, err := db.ExecContext(queryCtx, stmt)
	if err != nil {
		return plugin.QueryResult{}, err
	}
	affected, _ := res.RowsAffected()
	return plugin.QueryResult{Affected: affected, Raw: fmt.Sprintf("%d row(s) affected", affected)}, nil
}

func openSQL(ctx context.Context, cfg connConfig) (*sql.DB, error) {
	var driverName, dsn string
	switch cfg.Engine {
	case "postgres":
		driverName = "pgx"
		dsn = postgresDSN(cfg)
	case "mariadb":
		driverName = "mysql"
		dsn = mysqlDSN(cfg)
	case "mssql":
		driverName = "sqlserver"
		dsn = mssqlDSN(cfg)
	default:
		return nil, fmt.Errorf("engine %q is not an SQL engine", cfg.Engine)
	}

	db, err := sql.Open(driverName, dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(2)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(2 * time.Minute)

	pingCtx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("connect to %s at %s: %w", cfg.Engine, cfg.address(), err)
	}
	return db, nil
}

func postgresDSN(cfg connConfig) string {
	q := url.Values{}
	q.Set("sslmode", "disable")
	q.Set("connect_timeout", "5")
	q.Set("application_name", "air-traffic-control")

	u := url.URL{Scheme: "postgres", Path: "/" + cfg.Database}
	if cfg.User != "" {
		u.User = url.UserPassword(cfg.User, cfg.Password)
	}
	if cfg.Socket != "" {
		q.Set("host", cfg.Socket)
	} else {
		u.Host = net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func mysqlDSN(cfg connConfig) string {
	mc := mysql.NewConfig()
	mc.User = cfg.User
	mc.Passwd = cfg.Password
	mc.DBName = cfg.Database
	mc.Timeout = 5 * time.Second
	mc.ReadTimeout = 60 * time.Second
	mc.WriteTimeout = 60 * time.Second
	mc.ParseTime = true
	mc.Params = map[string]string{"charset": "utf8mb4"}
	if cfg.Socket != "" {
		mc.Net = "unix"
		mc.Addr = cfg.Socket
	} else {
		mc.Net = "tcp"
		mc.Addr = net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	}
	return mc.FormatDSN()
}

func mssqlDSN(cfg connConfig) string {
	q := url.Values{}
	q.Set("database", cfg.Database)
	q.Set("connection timeout", "5")
	q.Set("encrypt", "disable")
	u := url.URL{Scheme: "sqlserver", Host: net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port)), RawQuery: q.Encode()}
	if cfg.User != "" {
		u.User = url.UserPassword(cfg.User, cfg.Password)
	}
	return u.String()
}

func sqlIsRead(stmt string) bool {
	upper := strings.ToUpper(strings.TrimSpace(stmt))
	for _, prefix := range []string{"SELECT", "WITH", "SHOW", "EXPLAIN", "DESCRIBE", "DESC ", "VALUES", "TABLE ", "PRAGMA"} {
		if strings.HasPrefix(upper, prefix) {
			return true
		}
	}
	return false
}

func normalizeValue(v any) any {
	switch t := v.(type) {
	case []byte:
		return string(t)
	case time.Time:
		return t.Format(time.RFC3339Nano)
	default:
		return v
	}
}

// ---------------------------------------------------------------
// Redis
// ---------------------------------------------------------------

func queryRedis(ctx context.Context, cfg connConfig, req plugin.QueryRequest) (plugin.QueryResult, error) {
	opt := &redis.Options{
		Addr:         cfg.address(),
		Username:     cfg.User,
		Password:     cfg.Password,
		DialTimeout:  5 * time.Second,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
	}
	if cfg.Socket != "" {
		opt.Network = "unix"
	}
	if idx, err := strconv.Atoi(strings.TrimSpace(cfg.Database)); err == nil {
		opt.DB = idx
	}
	client := redis.NewClient(opt)
	defer func() { _ = client.Close() }()

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := client.Ping(pingCtx).Err(); err != nil {
		return plugin.QueryResult{}, fmt.Errorf("connect to redis at %s: %w", cfg.address(), err)
	}

	queryCtx, cancel2 := context.WithTimeout(ctx, 30*time.Second)
	defer cancel2()

	limit := req.Limit
	if limit <= 0 {
		limit = 500
	}

	cmdText := strings.TrimSpace(req.Query)
	upper := strings.ToUpper(cmdText)
	if strings.HasPrefix(upper, "SCAN") || strings.HasPrefix(strings.ToLower(cmdText), "--scan") {
		pattern := "*"
		fields := splitRedisArgs(cmdText)
		for i := 0; i < len(fields); i++ {
			f := strings.ToLower(fields[i])
			if (f == "--pattern" || f == "pattern" || f == "match") && i+1 < len(fields) {
				pattern = fields[i+1]
			}
		}
		var (
			keys      []string
			cursor    uint64
			truncated bool
		)
		for {
			batch, next, err := client.Scan(queryCtx, cursor, pattern, 100).Result()
			if err != nil {
				return plugin.QueryResult{}, err
			}
			keys = append(keys, batch...)
			if len(keys) >= limit {
				keys = keys[:limit]
				truncated = true
				break
			}
			cursor = next
			if cursor == 0 {
				break
			}
		}
		return plugin.QueryResult{Raw: strings.Join(keys, "\n"), Truncated: truncated}, nil
	}

	args := splitRedisArgs(cmdText)
	if len(args) == 0 {
		return plugin.QueryResult{}, fmt.Errorf("empty redis command")
	}
	anyArgs := make([]any, len(args))
	for i, a := range args {
		anyArgs[i] = a
	}
	reply, err := client.Do(queryCtx, anyArgs...).Result()
	if err != nil {
		return plugin.QueryResult{}, err
	}
	return plugin.QueryResult{Raw: formatRedisReply(reply)}, nil
}

// splitRedisArgs splits a redis-cli style command string into arguments while
// honoring single quotes, double quotes and backslash escapes.
func splitRedisArgs(s string) []string {
	var (
		out     []string
		b       strings.Builder
		quote   rune
		escaped bool
	)
	flush := func() {
		if b.Len() > 0 {
			out = append(out, b.String())
			b.Reset()
		}
	}
	for _, r := range s {
		if escaped {
			b.WriteRune(r)
			escaped = false
			continue
		}
		if quote != 0 {
			if r == '\\' {
				escaped = true
				continue
			}
			if r == quote {
				quote = 0
				continue
			}
			b.WriteRune(r)
			continue
		}
		switch r {
		case '\'', '"':
			quote = r
		case '\\':
			escaped = true
		case ' ', '\t', '\n', '\r':
			flush()
		default:
			b.WriteRune(r)
		}
	}
	flush()
	return out
}

func formatRedisReply(v any) string {
	switch t := v.(type) {
	case nil:
		return "(nil)"
	case string:
		return t
	case []byte:
		return string(t)
	case int64:
		return strconv.FormatInt(t, 10)
	case []any:
		parts := make([]string, 0, len(t))
		for _, item := range t {
			parts = append(parts, formatRedisReply(item))
		}
		return strings.Join(parts, "\n")
	default:
		return fmt.Sprint(t)
	}
}

// ---------------------------------------------------------------
// MongoDB
// ---------------------------------------------------------------

var mongoCallPattern = regexp.MustCompile(`(?s)^db\.([A-Za-z0-9_\-]+)\.(find|countDocuments|count)\((.*?)\)\s*;?$`)

func queryMongo(ctx context.Context, cfg connConfig, req plugin.QueryRequest) (plugin.QueryResult, error) {
	client, err := mongo.Connect(options.Client().
		ApplyURI(mongoURI(cfg)).
		SetConnectTimeout(5 * time.Second).
		SetServerSelectionTimeout(5 * time.Second))
	if err != nil {
		return plugin.QueryResult{}, err
	}
	defer func() {
		dctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = client.Disconnect(dctx)
	}()

	pingCtx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	if err := client.Ping(pingCtx, nil); err != nil {
		return plugin.QueryResult{}, fmt.Errorf("connect to mongodb at %s: %w", cfg.address(), err)
	}

	queryCtx, cancel2 := context.WithTimeout(ctx, 30*time.Second)
	defer cancel2()

	dbName := cfg.Database
	if dbName == "" {
		dbName = "admin"
	}
	db := client.Database(dbName)

	q := strings.TrimSpace(req.Query)
	lower := strings.ToLower(q)

	if strings.HasPrefix(lower, "db.getcollectionnames(") {
		names, err := db.ListCollectionNames(queryCtx, bson.M{})
		if err != nil {
			return plugin.QueryResult{}, err
		}
		encoded, _ := json.Marshal(names)
		return plugin.QueryResult{Raw: string(encoded)}, nil
	}

	if strings.HasPrefix(q, "{") {
		var cmd bson.M
		if err := json.Unmarshal([]byte(q), &cmd); err != nil {
			return plugin.QueryResult{}, fmt.Errorf("invalid mongo command JSON: %w", err)
		}
		var out bson.M
		if err := db.RunCommand(queryCtx, cmd).Decode(&out); err != nil {
			return plugin.QueryResult{}, err
		}
		if cursor, ok := out["cursor"].(bson.M); ok {
			if batch, ok := cursor["firstBatch"].(bson.A); ok {
				encoded, _ := json.Marshal(batch)
				return plugin.QueryResult{Raw: string(encoded)}, nil
			}
		}
		encoded, _ := json.Marshal(out)
		return plugin.QueryResult{Raw: string(encoded)}, nil
	}

	if matches := mongoCallPattern.FindStringSubmatch(q); matches != nil {
		coll := db.Collection(matches[1])
		op := matches[2]
		arg := strings.TrimSpace(matches[3])
		switch op {
		case "find":
			filter := bson.M{}
			if arg != "" {
				if err := json.Unmarshal([]byte(arg), &filter); err != nil {
					return plugin.QueryResult{}, fmt.Errorf("invalid mongo find filter: %w", err)
				}
			}
			limit := int64(req.Limit)
			if limit <= 0 {
				limit = DefaultRowLimit
			}
			cur, err := coll.Find(queryCtx, filter, options.Find().SetLimit(limit))
			if err != nil {
				return plugin.QueryResult{}, err
			}
			var docs []bson.M
			if err := cur.All(queryCtx, &docs); err != nil {
				return plugin.QueryResult{}, err
			}
			encoded, _ := json.Marshal(docs)
			return plugin.QueryResult{Raw: string(encoded), Truncated: int64(len(docs)) >= limit}, nil
		case "countDocuments", "count":
			filter := bson.M{}
			if arg != "" {
				_ = json.Unmarshal([]byte(arg), &filter)
			}
			n, err := coll.CountDocuments(queryCtx, filter)
			if err != nil {
				return plugin.QueryResult{}, err
			}
			return plugin.QueryResult{Raw: strconv.FormatInt(n, 10)}, nil
		}
	}

	return plugin.QueryResult{}, fmt.Errorf("mongo expression not supported by the Go driver; falling back to CLI")
}

func mongoURI(cfg connConfig) string {
	q := url.Values{}
	q.Set("connectTimeoutMS", "5000")
	q.Set("serverSelectionTimeoutMS", "5000")

	dbName := cfg.Database
	if dbName == "" {
		dbName = "admin"
	}
	u := url.URL{Scheme: "mongodb", Path: "/" + dbName}
	if cfg.User != "" {
		u.User = url.UserPassword(cfg.User, cfg.Password)
	}
	if cfg.Socket != "" {
		// MongoDB supports unix sockets through percent-encoded socket paths in
		// the host component; keep the URL form so the driver can parse it.
		u.Host = url.PathEscape(cfg.Socket)
	} else {
		u.Host = net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	}
	u.RawQuery = q.Encode()
	return u.String()
}
