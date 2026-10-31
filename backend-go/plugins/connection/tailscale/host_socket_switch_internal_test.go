package tailscale

import (
	"testing"

	"tailscale.com/tsnet"
)

// Switching to the host socket must stop ATC's own tsnet node. A logged-out
// backend left running keeps retrying the old/invalid auth key in the
// background forever, flooding the daemon log and the control plane after the
// operator has already switched modes.
func TestHostSocketSwitchClosesRunningTsnetServer(t *testing.T) {
	store := withSettingsStore(t)
	t.Setenv("TS_AUTHKEY", "")
	t.Setenv("TS_CONTROL_URL", "")

	if err := store.Save(SettingsPageID, map[string]any{
		FieldFallbackHostSocket: true,
		FieldStateDir:           t.TempDir(),
		FieldProxyMode:          "off",
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	origActive := tailscaledActive
	tailscaledActive = func() bool { return true }
	t.Cleanup(func() { tailscaledActive = origActive })

	closed := 0
	origClose := closeTsnetServer
	closeTsnetServer = func(*tsnet.Server) error {
		closed++
		return nil
	}
	t.Cleanup(func() { closeTsnetServer = origClose })

	p := New()
	p.tsServer = &tsnet.Server{}
	p.tsnetFP = "stale-tsnet-config"
	p.tsnetAuth = "tskey-auth-original"

	dialer, err := p.ensureTsnet()
	if err != nil {
		t.Fatalf("ensureTsnet: %v", err)
	}
	if _, ok := dialer.(*systemNetworkDialer); !ok {
		t.Fatalf("dialer = %T, want *systemNetworkDialer", dialer)
	}
	if closed != 1 {
		t.Fatalf("closed %d tsnet servers, want 1", closed)
	}
	if p.tsServer != nil {
		t.Fatal("tsnet server kept running after switching to the host socket")
	}
	// The key is kept so switching back to ATC's own node does not force a
	// needless re-registration.
	if p.tsnetAuth != "tskey-auth-original" {
		t.Fatalf("tsnetAuth = %q, want the key preserved", p.tsnetAuth)
	}
}

// A daemon without CAP_NET_ADMIN must fail loudly, never silently borrow the
// host's tailscaled: enabling the passthrough is an operator action only.
func TestEnsureTsnetDoesNotAutoEnableHostSocket(t *testing.T) {
	store := withSettingsStore(t)
	t.Setenv("TS_AUTHKEY", "")
	t.Setenv("TS_CONTROL_URL", "")

	// The host tailscaled is available, so an auto-enable bug would succeed.
	origActive := tailscaledActive
	tailscaledActive = func() bool { return true }
	t.Cleanup(func() { tailscaledActive = origActive })

	p := New()
	if _, err := p.ensureTsnet(); err == nil {
		t.Fatal("ensureTsnet succeeded without an auth key or host-socket opt-in")
	}
	if raw, ok := store.Value(SettingsPageID, FieldFallbackHostSocket); ok && raw != "false" {
		t.Fatalf("host-socket fallback was written automatically: %q", raw)
	}
	if p.tsServer != nil {
		t.Fatal("tsnet server started despite the missing key")
	}
}
