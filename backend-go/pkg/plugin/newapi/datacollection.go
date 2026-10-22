package newapi

import (
	"time"

	"air-traffic-control/backend/pkg/plugin"
)

// CollectionSchedule defines the polling cadence, cache TTL, and execution
// timeout for a DataCollectionPlugin.
type CollectionSchedule struct {
	Interval time.Duration `json:"interval"`
	TTL      time.Duration `json:"ttl"`
	Timeout  time.Duration `json:"timeout,omitempty"`
	Realtime bool          `json:"realtime,omitempty"`
}

// CollectionSchema documents the payload structure returned by a
// DataCollectionPlugin's Read method.
type CollectionSchema struct {
	Version     string `json:"version"`
	RootKey     string `json:"rootKey,omitempty"`
	Description string `json:"description,omitempty"`
}

// CollectionEnvelope wraps raw plugin Read output with provenance and cache
// TTL metadata.
type CollectionEnvelope struct {
	PluginID    string    `json:"pluginId"`
	NodeID      string    `json:"nodeId"`
	CollectedAt time.Time `json:"collectedAt"`
	TTLSeconds  int       `json:"ttlSeconds"`
	Data        any       `json:"data"`
}

// DataCollectionPlugin is the contract for telemetry and live-state inspection
// plugins that expose structured collection schedules and schema metadata in
// addition to plugin.Reader.
type DataCollectionPlugin interface {
	plugin.Plugin
	plugin.Reader
	CollectionSchedule() CollectionSchedule
	CollectionSchema() CollectionSchema
}

// DefaultScheduleForKind returns the standard polling and cache TTL schedule
// for a given plugin.Kind, aligned with AGENTS.md §4 cache TTL tiers.
func DefaultScheduleForKind(kind plugin.Kind) CollectionSchedule {
	ttl := plugin.TTLForKind(kind)
	if kind == plugin.KindTelemetry {
		ttl = 15 * time.Second
	}
	return CollectionSchedule{
		Interval: ttl,
		TTL:      ttl,
		Timeout:  10 * time.Second,
		Realtime: kind == plugin.KindTelemetry || kind == plugin.KindLogs,
	}
}
