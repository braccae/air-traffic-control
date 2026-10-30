package tailscale

import (
	"testing"
	"time"
)

// The exact payload that used to restart tsnet every minute: connected, online,
// 49 peers, and only the benign accept-routes warning.
func TestShouldRepairIgnoresInformationalHealth(t *testing.T) {
	st := SelfStatus{
		Connected:    true,
		Online:       true,
		BackendState: "Running",
		PeerCount:    49,
		Health:       []string{"Some peers are advertising routes but --accept-routes is false"},
	}
	if shouldRepair(st) {
		t.Fatal("a healthy node with only informational warnings must never be repaired")
	}

	unknown := st
	unknown.Health = []string{"some brand new informational message"}
	if shouldRepair(unknown) {
		t.Fatal("unrecognised health strings must be treated as benign")
	}
}

func TestShouldRepairOnRealFaults(t *testing.T) {
	cases := []struct {
		name   string
		status SelfStatus
	}{
		{"not connected", SelfStatus{Connected: false, Online: true}},
		{"offline", SelfStatus{Connected: true, Online: false}},
		{"backend error", SelfStatus{Connected: true, Online: true, Health: []string{"backend error: failed to connect to control"}}},
		{"no derp", SelfStatus{Connected: true, Online: true, Health: []string{"no DERP servers reachable"}}},
		{"no state", SelfStatus{Connected: true, Online: true, BackendState: "NoState"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !shouldRepair(tc.status) {
				t.Fatalf("%s must trigger a repair: %+v", tc.name, tc.status)
			}
		})
	}
}

func TestHealthIsFatalClassification(t *testing.T) {
	benign := []string{
		"Some peers are advertising routes but --accept-routes is false",
		"Tailscale is starting. Please wait.",
		"using exit node \"foo\"",
	}
	for _, entry := range benign {
		if healthIsFatal(entry) {
			t.Errorf("%q must be benign", entry)
		}
	}

	fatal := []string{
		"not connected to the tailnet",
		"backend error: failed",
		"node key has expired",
		"no DERP servers reachable",
	}
	for _, entry := range fatal {
		if !healthIsFatal(entry) {
			t.Errorf("%q must be fatal", entry)
		}
	}
}

func TestRepairPolicyDebouncesAndBacksOff(t *testing.T) {
	now := time.Now()
	p := repairPolicy{}

	// Two bad readings are not enough.
	if p.mayRepair(now) {
		t.Fatal("first bad reading must not repair")
	}
	if p.mayRepair(now.Add(30 * time.Second)) {
		t.Fatal("second bad reading must not repair")
	}
	if !p.mayRepair(now.Add(60 * time.Second)) {
		t.Fatal("third consecutive bad reading must repair")
	}
	p.recordRepair(now.Add(60 * time.Second))

	// Immediately after a repair the cooldown blocks another one.
	p.badStreak = repairBadStreak
	if p.mayRepair(now.Add(90 * time.Second)) {
		t.Fatal("cooldown must block a second repair")
	}

	// After the base cooldown a repair is allowed again.
	if !p.mayRepair(now.Add(11 * time.Minute)) {
		t.Fatal("repair should be allowed after the cooldown")
	}
	p.recordRepair(now.Add(11 * time.Minute))

	// A second repair doubles the backoff.
	if p.backoff != 2*repairBaseBackoff {
		t.Fatalf("backoff = %s, want %s", p.backoff, 2*repairBaseBackoff)
	}
	p.badStreak = repairBadStreak
	if p.mayRepair(now.Add(20 * time.Minute)) {
		t.Fatal("escalated cooldown must block the next repair")
	}

	// A long healthy period resets the backoff.
	p.recordHealthy(now.Add(2 * time.Hour))
	if p.backoff != 0 {
		t.Fatalf("backoff should reset after a sustained healthy period, got %s", p.backoff)
	}
}

func TestRepairBackoffIsCapped(t *testing.T) {
	p := repairPolicy{backoff: repairBaseBackoff}
	for i := 0; i < 10; i++ {
		p.recordRepair(time.Now())
	}
	if p.backoff != repairMaxBackoff {
		t.Fatalf("backoff = %s, want cap %s", p.backoff, repairMaxBackoff)
	}
}
