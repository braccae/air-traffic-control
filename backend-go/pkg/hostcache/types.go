// Package hostcache is the Go-side cache/store for data/hosts-cache.sqlite.
//
// During the node→pure-Go migration the TypeScript coordinator
// (src/utils/hostCacheDb.ts) and the Go daemon share the same database file, so
// this package reproduces the TS schema byte-for-byte: identical table names,
// column names/order, primary keys, indices, PRAGMAs and Unix-millisecond
// timestamps. The TS implementation remains the source of truth until the
// migration completes.
//
// Ownership during the transition:
//   - The TS coordinator (src/utils/hostCacheDb.ts) owns the *parsing* of
//     telemetry/container/section blobs. This package never parses those blobs:
//     every *_json value is treated as an opaque json.RawMessage and only
//     stored/read back verbatim. This avoids duplicating the TS parser logic.
//   - This package owns *persistence* of every table, including the telemetry,
//     container and section tables that the TS store currently keeps only in
//     RAM. Because the column layout matches exactly, either side may read what
//     the other wrote without stomping entries.
//
// Open mode: a read-only open cannot create WAL sidecar files, so the write
// owner must open first. Open() therefore opens read-write-create and creates
// the schema plus WAL/-shm sidecars; OpenReadOnly() is provided for read-only
// consumers (the daemon's SQLiteNodeResolver already opens read-only).
package hostcache

import "encoding/json"

// NodeRecord mirrors the `hosts` row plus the decoded node_json blob. The JSON
// tags use the same camelCase keys as the TS NodeItem so callers can
// json.Unmarshal(nodeJSON, &rec) without re-implementing the TS parser. Fields
// present only in the JSON blob (folder, chassis, plugin flags, ...) are carried
// through NodeJSON untouched; Folder is surfaced as a convenience.
type NodeRecord struct {
	ID            string          `json:"id"`
	Name          string          `json:"name"`
	ShortName     string          `json:"shortName"`
	Group         string          `json:"group"`
	IP            string          `json:"ip"`
	Port          int             `json:"port"`
	SSHUser       string          `json:"sshUser"`
	TailscaleIP   string          `json:"tailscaleIp"`
	TailscaleHost string          `json:"tailscaleHost"`
	Status        string          `json:"status"`
	Folder        string          `json:"folder,omitempty"`
	NodeJSON      json.RawMessage `json:"-"`
	SortOrder     int             `json:"-"`
	UpdatedAt     int64           `json:"-"`
}

// TelemetryEntry is a row of host_telemetry_cache. TelemetryJSON is the raw
// blob written by the TS parser; this package never inspects it.
type TelemetryEntry struct {
	TelemetryJSON json.RawMessage `json:"telemetry"`
	UpdatedAt     int64           `json:"updatedAt"`
}

// ContainersEntry is a row of host_containers_cache. Every blob is opaque to
// this package. A nil blob on read means the column held SQL NULL.
type ContainersEntry struct {
	ProbedUsersJSON      json.RawMessage `json:"probedUsers"`
	IdentitiesJSON       json.RawMessage `json:"identities"`
	CustomIdentitiesJSON json.RawMessage `json:"customIdentities"`
	RootContainersJSON   json.RawMessage `json:"rootContainers"`
	RootPodsJSON         json.RawMessage `json:"rootPods"`
	DockerContainersJSON json.RawMessage `json:"dockerContainers"`
	HasDocker            bool            `json:"hasDocker"`
	UpdatedAt            int64           `json:"updatedAt"`
}

// ContainersUpdate is the write payload for UpsertHostContainers. It mirrors the
// TS payload, where absent optional fields are preserved from the existing row:
//   - RootPodsJSON / DockerContainersJSON: nil preserves the stored value.
//   - HasDocker: nil preserves the stored value; non-nil always writes it.
//
// CustomIdentitiesJSON is intentionally absent: like TS, custom identities are
// always preserved and only changed via AddCustomIdentity.
type ContainersUpdate struct {
	ProbedUsersJSON      json.RawMessage
	IdentitiesJSON       json.RawMessage
	RootContainersJSON   json.RawMessage
	RootPodsJSON         json.RawMessage
	DockerContainersJSON json.RawMessage
	HasDocker            *bool
}

// SectionEntry is a row of host_section_cache. DataJSON is opaque to this package.
type SectionEntry struct {
	DataJSON  json.RawMessage `json:"data"`
	UpdatedAt int64           `json:"updatedAt"`
}

// Task mirrors a row of the `tasks` table using TS camelCase keys. Logs is the
// raw log_json blob (TS stores JSON.stringify(task.logs)).
type Task struct {
	ID          string          `json:"id"`
	StartTime   string          `json:"startTime"`
	NodeTarget  string          `json:"nodeTarget"`
	User        string          `json:"user"`
	Description string          `json:"description"`
	Progress    int             `json:"progress"`
	ProgressEta string          `json:"progressEta"`
	Status      string          `json:"status"`
	Category    string          `json:"category"`
	Logs        json.RawMessage `json:"logs"`
	UpdatedAt   int64           `json:"-"`
}

// TaskUpdate carries the mutable subset of a task. Nil pointers / nil Logs mean
// "leave unchanged", matching the TS `?? existing` merge.
type TaskUpdate struct {
	Progress    *int
	ProgressEta *string
	Status      *string
	Logs        json.RawMessage
}

// CustomFolder mirrors a row of inventory_folders.
type CustomFolder struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	CreatedAt int64  `json:"createdAt"`
}

// TagChunk mirrors a row of tag_inventory_chunks.
type TagChunk struct {
	ID            string `json:"id"`
	Tag           string `json:"tag"`
	SSHUser       string `json:"sshUser,omitempty"`
	VaultSecretID string `json:"vaultSecretId,omitempty"`
	AuthMethod    string `json:"authMethod"`
	CreatedAt     int64  `json:"createdAt"`
}

// VaultVariable mirrors a row of vault_variables.
type VaultVariable struct {
	Name       string `json:"name"`
	VaultBlock string `json:"vaultBlock"`
	CreatedAt  int64  `json:"createdAt"`
	UpdatedAt  int64  `json:"updatedAt"`
}

// VaultAuditEvent mirrors a row of vault_audit_log.
type VaultAuditEvent struct {
	ID             string `json:"id"`
	Timestamp      int64  `json:"timestamp"`
	VariableName   string `json:"variableName"`
	Actor          string `json:"actor"`
	Host           string `json:"host"`
	Action         string `json:"action"`
	PasswordSource string `json:"passwordSource"`
}

// RcloneRemote mirrors a row of rclone_remotes.
type RcloneRemote struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Provider   string `json:"provider"`
	Type       string `json:"type"`
	ConfigJSON string `json:"configJson"`
	CreatedAt  int64  `json:"createdAt"`
	UpdatedAt  int64  `json:"updatedAt"`
}

// RcloneMount mirrors a row of rclone_mounts.
type RcloneMount struct {
	NodeID      string  `json:"nodeId"`
	RemoteID    string  `json:"remoteId"`
	RemoteName  string  `json:"remoteName"`
	MountTarget string  `json:"mountTarget"`
	Policy      string  `json:"policy"`
	Status      string  `json:"status"`
	Error       *string `json:"error,omitempty"`
	InstalledAt int64   `json:"installedAt"`
	UpdatedAt   int64   `json:"updatedAt"`
}

// CronSchedule mirrors a row of cron_schedules.
type CronSchedule struct {
	ID             string          `json:"id"`
	Name           string          `json:"name"`
	CronExpression string          `json:"cronExpression"`
	Enabled        bool            `json:"enabled"`
	ScopeKind      string          `json:"scopeKind"`
	ScopeTarget    string          `json:"scopeTarget"`
	TaskKind       string          `json:"taskKind"`
	PayloadJSON    json.RawMessage `json:"payloadJson"`
	CreatedAt      int64           `json:"createdAt"`
	UpdatedAt      int64           `json:"updatedAt"`
	NextRunAt      *int64          `json:"nextRunAt,omitempty"`
	LastRunAt      *int64          `json:"lastRunAt,omitempty"`
	LastRunStatus  *string         `json:"lastRunStatus,omitempty"`
	LastRunTaskID  *string         `json:"lastRunTaskId,omitempty"`
	LastRunError   *string         `json:"lastRunError,omitempty"`
}

