package tailscale

import (
	"path/filepath"
	"testing"
)

func TestAbsoluteStateDirAnchorsRelativePath(t *testing.T) {
	got := absoluteStateDir("../data/tsnet")
	if !filepath.IsAbs(got) {
		t.Fatalf("absoluteStateDir(%q) = %q, want an absolute path", "../data/tsnet", got)
	}
	if filepath.Base(got) != "tsnet" {
		t.Fatalf("absoluteStateDir(%q) = %q, want it to keep the tsnet base", "../data/tsnet", got)
	}
}

func TestAbsoluteStateDirLeavesAbsoluteAndEmptyAlone(t *testing.T) {
	if got := absoluteStateDir("/var/lib/atc/tsnet"); got != "/var/lib/atc/tsnet" {
		t.Fatalf("absolute path rewritten to %q", got)
	}
	if got := absoluteStateDir("  "); got != "" {
		t.Fatalf("blank state dir = %q, want empty", got)
	}
}

// An explicit host-socket opt-in must win over a stored auth key, otherwise
// switching modes requires destroying the key first.
func TestShouldUseHostSocketWinsOverAuthKey(t *testing.T) {
	orig := tailscaledActive
	t.Cleanup(func() { tailscaledActive = orig })
	tailscaledActive = func() bool { return true }

	cfg := tsnetSettings{AuthKey: "tskey-auth-present", FallbackHostSocket: true}
	if !shouldUseHostSocket(cfg) {
		t.Fatal("enabled host-socket fallback was ignored because an auth key is configured")
	}
	if shouldUseHostSocket(tsnetSettings{AuthKey: "tskey-auth-present"}) {
		t.Fatal("host socket must stay off unless explicitly enabled")
	}
}

func TestShouldUseHostSocketRequiresActiveHost(t *testing.T) {
	orig := tailscaledActive
	t.Cleanup(func() { tailscaledActive = orig })
	tailscaledActive = func() bool { return false }

	cfg := tsnetSettings{FallbackHostSocket: true}
	if shouldUseHostSocket(cfg) {
		t.Fatal("host-socket fallback was selected while the host tailscaled is inactive")
	}
}
