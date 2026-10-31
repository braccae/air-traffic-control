package tailscale

import (
	"errors"
	"strings"
	"testing"
)

// stubSocketMark pins the SO_MARK probe so the hint is deterministic regardless
// of whether the test process happens to hold CAP_NET_ADMIN.
func stubSocketMark(t *testing.T, available bool) {
	t.Helper()
	prev := socketMarkAvailable
	socketMarkAvailable = func() bool { return available }
	t.Cleanup(func() { socketMarkAvailable = prev })
}

func TestControlHintExplainsUnprivilegedBindOnTimeout(t *testing.T) {
	stubSocketMark(t, false)

	hint := unprivilegedControlHint("NeedsLogin", errors.New("tsnet.Up: context deadline exceeded"))
	if hint == "" {
		t.Fatal("expected a capability hint for a NeedsLogin timeout without SO_MARK")
	}
	for _, want := range []string{"CAP_NET_ADMIN", "host tailscaled fallback"} {
		if !strings.Contains(hint, want) {
			t.Fatalf("hint %q is missing %q", hint, want)
		}
	}
}

func TestControlHintAcceptsUnreachableAndIOTimeout(t *testing.T) {
	stubSocketMark(t, false)

	for _, msg := range []string{
		"dial tcp 192.200.0.107:443: connect: network is unreachable",
		"dial tcp 192.200.0.107:443: i/o timeout",
	} {
		if unprivilegedControlHint("NeedsLogin", errors.New(msg)) == "" {
			t.Fatalf("expected a hint for %q", msg)
		}
	}
}

// A node that reaches control but is rejected (bad or expired key) must not be
// told to change socket capabilities: that would send the operator down the
// wrong path.
func TestControlHintIgnoresNonNetworkFailures(t *testing.T) {
	stubSocketMark(t, false)

	if hint := unprivilegedControlHint("NeedsLogin", errors.New("register request: http 401: invalid key")); hint != "" {
		t.Fatalf("invalid-key failure produced a network hint: %q", hint)
	}
	if hint := unprivilegedControlHint("Running", errors.New("context deadline exceeded")); hint != "" {
		t.Fatalf("running backend produced a NeedsLogin hint: %q", hint)
	}
}

func TestControlHintSilentWhenSocketMarkWorks(t *testing.T) {
	stubSocketMark(t, true)

	if hint := unprivilegedControlHint("NeedsLogin", errors.New("context deadline exceeded")); hint != "" {
		t.Fatalf("hint emitted although SO_MARK is available: %q", hint)
	}
}
