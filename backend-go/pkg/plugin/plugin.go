package plugin

import (
	"context"
)

// Plugin is the base interface that every compile-time plugin must implement.
type Plugin interface {
	// Meta returns the display, kind, and unique identification metadata.
	Meta() Meta

	// Compatible performs a static eligibility check against the target host facts
	// (e.g. matching OSFamily, connection protocol, architecture).
	Compatible(h HostFacts) bool

	// Requirements returns required host packages and controller Galaxy collections.
	Requirements() Requirements

	// Probe determines whether the plugin capability is present and active on the host.
	Probe(ctx context.Context, r Runner, h HostFacts) (Detection, error)
}

// Reader is an optional interface implemented by plugins that produce live inspection data
// for their frontend tab or dashboard views.
type Reader interface {
	// Read retrieves current operational state and resource hierarchies from the target host.
	Read(ctx context.Context, r Runner, h HostFacts) (any, error)
}

// Actor is an optional interface implemented by plugins that compile declarative host mutations
// into Ansible playbooks or task sequences.
type Actor interface {
	// Actions enumerates all supported mutation actions and their option schemas.
	Actions() []ActionSpec

	// Compile transforms action arguments into a declarative TaskSpec.
	Compile(h HostFacts, action string, opts map[string]any) (TaskSpec, error)
}
