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

// Dialer provides optional direct network connection dial capabilities (e.g. for connection plugins)
type Dialer interface {
	Dial(ctx context.Context, network, address string) (net.Conn, error)
}
