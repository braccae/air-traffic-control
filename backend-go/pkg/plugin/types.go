package plugin

import (
	"context"
	"net"
)

// Kind identifies the category of a plugin
type Kind string

const (
	KindConnection Kind = "connection"
	KindElevation  Kind = "elevation"
	KindPackage    Kind = "package"
	KindContainer  Kind = "container"
	KindStorage    Kind = "storage"
	KindShare      Kind = "share"
	KindMount      Kind = "mount"
	KindBackup     Kind = "backup"
	KindLogs       Kind = "logs"
	KindNetwork    Kind = "network"
	KindFirewall   Kind = "firewall"
	KindTelemetry  Kind = "telemetry"
	KindDisk       Kind = "disk"
	KindVM         Kind = "vm"
	KindSystem     Kind = "system"
	KindDatabase   Kind = "database"
)

// Meta provides display and identification metadata for a plugin
type Meta struct {
	ID          string `json:"id"`          // Format: <kind>/<name>, e.g. "storage/zfs"
	Kind        Kind   `json:"kind"`        // Plugin kind category
	Name        string `json:"name"`        // Short unique name within the kind, e.g. "zfs"
	Label       string `json:"label"`       // Human-readable title, e.g. "ZFS Storage"
	Icon        string `json:"icon"`        // Icon identifier for the frontend, e.g. "database", "terminal"
	Description string `json:"description"` // Summary description
	Order       int    `json:"order"`       // Ordering priority within the kind or sidebar section
}

// HostFacts contains target node identity and operational runtime attributes
type HostFacts struct {
	NodeID           string            `json:"nodeId"`
	OSFamily         string            `json:"osFamily"` // e.g. "linux", "windows", "ios"
	HostRoute        string            `json:"hostRoute"`
	ActiveConnection string            `json:"activeConnection"` // e.g. "connection/ssh", "connection/tailscale"
	ActiveElevation  string            `json:"activeElevation"`  // e.g. "elevation/run0", "elevation/sudo"
	ActivePackage    string            `json:"activePackage"`    // e.g. "package/dnf", "package/apt"
	Overrides        map[string]string `json:"overrides,omitempty"`
}

// Requirements specifies package requirements on the target host and Galaxy collections on the controller
type Requirements struct {
	// HostPackages maps a package manager plugin name (e.g. "dnf", "apt", "apk") to required package names
	HostPackages map[string][]string `json:"hostPackages,omitempty"`
	// ControllerCollections lists required Ansible Galaxy collections (e.g. ["community.general"])
	ControllerCollections []string `json:"controllerCollections,omitempty"`
}

// Detection conveys probe results for a plugin on a specific host
type Detection struct {
	Detected bool   `json:"detected"`          // True if the tool/subsystem is present on the host
	Status   string `json:"status"`            // Short status badge, e.g. "ONLINE", "3 pools", "INACTIVE"
	Details  string `json:"details,omitempty"` // Contextual details or telemetry summary
	Error    string `json:"error,omitempty"`   // Probe error if detection encountered a failure
}

// OptionSchema defines the typed options for a plugin action
type OptionSchema struct {
	Name        string   `json:"name"`
	Type        string   `json:"type"` // "string", "int", "bool", "select"
	Required    bool     `json:"required"`
	Default     any      `json:"default,omitempty"`
	Enum        []string `json:"enum,omitempty"`
	Description string   `json:"description,omitempty"`
	Secret      bool     `json:"secret,omitempty"` // If true, requires masking in UI & logs
}

// ActionSpec describes a declarative mutation supported by an Actor plugin
type ActionSpec struct {
	Name        string         `json:"name"`
	Label       string         `json:"label"`
	Description string         `json:"description,omitempty"`
	Options     []OptionSchema `json:"options,omitempty"`
}

// TaskStep represents an individual declarative Ansible task step
type TaskStep struct {
	Name   string         `json:"name"`
	Module string         `json:"module"`
	Params map[string]any `json:"params,omitempty"`
}

// TaskSpec represents a complete declarative mutation task produced by Compile
type TaskSpec struct {
	Name          string            `json:"name"`
	Steps         []TaskStep        `json:"steps"`
	SecretsMasked map[string]string `json:"secretsMasked,omitempty"`
}

// Runner provides command execution and network dial capabilities to probes and readers
type Runner interface {
	RunCommand(ctx context.Context, cmd string) (string, error)
}

// QueryRequest describes an ad-hoc database query or command.
//
// Credentials and connection details are resolved server-side from the node's
// pluginOverrides whenever the corresponding field is left empty, so the browser
// does not have to resend secrets on every query. Fields supplied here take
// precedence over the stored overrides.
type QueryRequest struct {
	Engine   string `json:"engine"`             // postgres|postgresql|mysql|mariadb|mssql|redis|mongodb|rocksdb
	Host     string `json:"host,omitempty"`     // TCP host; defaults to <engine>_host override or HostRoute
	Port     int    `json:"port,omitempty"`     // TCP port; defaults to the engine's standard port
	User     string `json:"user,omitempty"`     // defaults to <engine>_user override
	Password string `json:"password,omitempty"` // defaults to <engine>_password override
	Database string `json:"database,omitempty"` // defaults to <engine>_database override
	Socket   string `json:"socket,omitempty"`   // unix socket path; defaults to <engine>_socket override
	Query    string `json:"query"`              // SQL statement or native command text
	Limit    int    `json:"limit,omitempty"`    // optional row cap for read queries
}

// QueryResult is the normalized result returned by a Querier. It is designed to
// be JSON-serializable and to map cleanly onto the frontend table renderers.
type QueryResult struct {
	Engine     string   `json:"engine"`
	Driver     string   `json:"driver"` // "go" when a native driver executed the query
	Columns    []string `json:"columns,omitempty"`
	Rows       [][]any  `json:"rows,omitempty"`
	Raw        string   `json:"raw,omitempty"`
	Affected   int64    `json:"affected,omitempty"`
	DurationMs int64    `json:"durationMs"`
	Truncated  bool     `json:"truncated,omitempty"`
	// Fallback is set to "cli" when the native driver could not connect or does
	// not support the requested command. Callers should then retry through the
	// vendor CLI transport instead of treating this as a hard failure.
	Fallback string `json:"fallback,omitempty"`
	Error    string `json:"error,omitempty"`
}

// Querier is an optional interface implemented by plugins that execute ad-hoc
// queries against database engines through native Go drivers rather than by
// shelling out to vendor CLI tools.
type Querier interface {
	Query(ctx context.Context, r Runner, h HostFacts, req QueryRequest) (QueryResult, error)
}

// Dialer provides optional direct network connection dial capabilities (e.g. for connection plugins)
type Dialer interface {
	Dial(ctx context.Context, network, address string) (net.Conn, error)
}
