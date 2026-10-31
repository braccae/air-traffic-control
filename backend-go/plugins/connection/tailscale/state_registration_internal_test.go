package tailscale

import (
	"os"
	"path/filepath"
	"testing"
)

// A state file containing only the machine key is what tsnet writes before it
// ever logs in. It must not be mistaken for a registration: doing so skipped the
// missing-auth-key guard and left every dial waiting on an interactive login.
func TestHasRegisteredNodeStateRejectsMachineKeyOnly(t *testing.T) {
	dir := t.TempDir()
	writeState(t, dir, `{"_machinekey":"cHJpdmtleTpkZWFkYmVlZg=="}`)

	if hasRegisteredNodeState(dir) {
		t.Fatal("machine-key-only state was treated as a registered node")
	}
}

func TestHasRegisteredNodeStateAcceptsProfile(t *testing.T) {
	dir := t.TempDir()
	writeState(t, dir, `{
		"_machinekey":"cHJpdmtleTpkZWFkYmVlZg==",
		"_current-profile":"profile-daemon",
		"profile-daemon":"H4sIAA=="
	}`)

	if !hasRegisteredNodeState(dir) {
		t.Fatal("registered node state was reported as unregistered")
	}
}

// Corrupt or legacy-format state is trusted: re-registering a node that is
// already in the tailnet would create a duplicate.
func TestHasRegisteredNodeStateTrustsUnknownFormat(t *testing.T) {
	dir := t.TempDir()
	writeState(t, dir, "not-json-legacy-state")

	if !hasRegisteredNodeState(dir) {
		t.Fatal("unparseable state was reported as unregistered")
	}
}

func TestHasRegisteredNodeStateMissingDir(t *testing.T) {
	if hasRegisteredNodeState(filepath.Join(t.TempDir(), "absent")) {
		t.Fatal("missing state directory was reported as registered")
	}
	if hasRegisteredNodeState("") {
		t.Fatal("empty state directory was reported as registered")
	}
}

func writeState(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "tailscaled.state"), []byte(content), 0o600); err != nil {
		t.Fatalf("write state: %v", err)
	}
}
