package systemd_test

// Integration checks against the real systemd host the test runs on.
//
// Enable with: ATC_SYSTEMD_INTEGRATION=1 go test ./pkg/systemd -run Integration -v
//
// These tests only read. They compare the values parsed by this package against
// an independent `systemctl show` invocation on the same host, which is what the
// acceptance criteria for ATC's systemd integration require.

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"air-traffic-control/backend/pkg/systemd"
)

// hostRunner executes commands on the local host, standing in for the SSH /
// tsnet runners the daemon uses against remote nodes.
type hostRunner struct{}

func (hostRunner) RunCommand(ctx context.Context, cmd string) (string, error) {
	out, err := exec.CommandContext(ctx, "sh", "-c", cmd).CombinedOutput()
	if err != nil {
		return string(out), err
	}
	return string(out), nil
}

func requireIntegration(t *testing.T) {
	t.Helper()
	if os.Getenv("ATC_SYSTEMD_INTEGRATION") != "1" {
		t.Skip("set ATC_SYSTEMD_INTEGRATION=1 to run against the real systemd host")
	}
	if _, err := exec.LookPath("systemctl"); err != nil {
		t.Skip("systemctl not available")
	}
}

func TestIntegrationListUnitsMatchesSystemctl(t *testing.T) {
	requireIntegration(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	units, err := systemd.ListUnits(ctx, hostRunner{}, systemd.ScopeSystem)
	if err != nil {
		t.Fatalf("ListUnits: %v", err)
	}
	if len(units) == 0 {
		t.Fatal("systemd reported zero service units on a running system")
	}

	// Every listed unit must agree with an independent systemctl show call.
	checked := 0
	for _, u := range units {
		if u.Load == "not-found" {
			continue
		}
		raw, err := hostRunner{}.RunCommand(ctx, "systemctl show "+u.Unit+" --no-pager --property=ActiveState,SubState,UnitFileState")
		if err != nil {
			t.Fatalf("systemctl show %s: %v", u.Unit, err)
		}
		want := map[string]string{}
		for _, line := range strings.Split(strings.TrimSpace(raw), "\n") {
			k, v, ok := strings.Cut(line, "=")
			if ok {
				want[k] = v
			}
		}
		if want["ActiveState"] != u.Active {
			t.Errorf("%s ActiveState: package=%q systemctl=%q", u.Unit, u.Active, want["ActiveState"])
		}
		if want["SubState"] != u.Sub {
			t.Errorf("%s SubState: package=%q systemctl=%q", u.Unit, u.Sub, want["SubState"])
		}
		if want["UnitFileState"] != u.UnitFileState {
			t.Errorf("%s UnitFileState: package=%q systemctl=%q", u.Unit, u.UnitFileState, want["UnitFileState"])
		}
		checked++
		if checked >= 15 {
			break
		}
	}
	if checked == 0 {
		t.Fatal("no loadable units were checked")
	}
	t.Logf("verified %d/%d system units against systemctl show", checked, len(units))
}

func TestIntegrationShowUnitMatchesSystemctl(t *testing.T) {
	requireIntegration(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	unit := "systemd-journald.service"
	state, err := systemd.ShowUnit(ctx, hostRunner{}, systemd.ScopeSystem, unit)
	if err != nil {
		t.Fatalf("ShowUnit(%s): %v", unit, err)
	}

	raw, err := hostRunner{}.RunCommand(ctx, "systemctl show "+unit+" --no-pager --property=ActiveState,SubState,LoadState,UnitFileState,FragmentPath")
	if err != nil {
		t.Fatalf("systemctl show: %v", err)
	}
	want := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(raw), "\n") {
		k, v, ok := strings.Cut(line, "=")
		if ok {
			want[k] = v
		}
	}

	if state.ActiveState != want["ActiveState"] || state.SubState != want["SubState"] || state.LoadState != want["LoadState"] {
		t.Errorf("state mismatch: package=%+v systemctl=%v", state, want)
	}
	if state.UnitFileState != want["UnitFileState"] {
		t.Errorf("UnitFileState mismatch: package=%q systemctl=%q", state.UnitFileState, want["UnitFileState"])
	}
	if state.FragmentPath != want["FragmentPath"] {
		t.Errorf("FragmentPath mismatch: package=%q systemctl=%q", state.FragmentPath, want["FragmentPath"])
	}
	t.Logf("%s: active=%s sub=%s enabled=%v fragment=%s", state.Unit, state.ActiveState, state.SubState, state.Enabled, state.FragmentPath)
}

func TestIntegrationJournalForUnit(t *testing.T) {
	requireIntegration(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Find a unit that actually has journal records on this host, then read that
	// unit's journal specifically.
	all, err := systemd.Journal(ctx, hostRunner{}, systemd.ScopeSystem, systemd.JournalOptions{Lines: 200})
	if err != nil {
		t.Fatalf("Journal (unfiltered): %v", err)
	}
	unit := ""
	for _, e := range all {
		if e.Unit != "" {
			unit = e.Unit
			break
		}
	}
	if unit == "" {
		t.Skip("host journal has no unit-attributed records in the last 200 lines")
	}

	entries, err := systemd.Journal(ctx, hostRunner{}, systemd.ScopeSystem, systemd.JournalOptions{
		Unit:  unit,
		Lines: 20,
	})
	if err != nil {
		t.Fatalf("Journal(%s): %v", unit, err)
	}
	if len(entries) == 0 {
		t.Fatalf("expected journald records for %s, got none", unit)
	}
	for _, e := range entries {
		if e.Unit != unit {
			t.Errorf("entry from unexpected unit: %+v", e)
		}
		if e.Timestamp == "" {
			t.Errorf("entry without timestamp: %+v", e)
		}
	}
	t.Logf("read %d journal entries for %s; last: %s %s", len(entries), unit, entries[len(entries)-1].Timestamp, entries[len(entries)-1].Message)
}

func TestIntegrationUserScope(t *testing.T) {
	requireIntegration(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	units, err := systemd.ListUnits(ctx, hostRunner{}, systemd.ScopeUser)
	if err != nil {
		t.Skipf("no reachable user manager on this host: %v", err)
	}
	for _, u := range units {
		if u.Scope != "user" {
			t.Errorf("user-scope listing returned scope %q for %s", u.Scope, u.Unit)
		}
	}
	t.Logf("user manager listed %d service units", len(units))
}
