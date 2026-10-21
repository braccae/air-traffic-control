package systemd_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"air-traffic-control/backend/pkg/plugin"
	"air-traffic-control/backend/pkg/systemd"
)

// recordingRunner answers from a command-substring map and records every command
// it was asked to run.
type recordingRunner struct {
	responses map[string]string
	errs      map[string]error
	commands  []string
}

func (r *recordingRunner) RunCommand(_ context.Context, cmd string) (string, error) {
	r.commands = append(r.commands, cmd)
	for pattern, err := range r.errs {
		if strings.Contains(cmd, pattern) {
			return r.responses[pattern], err
		}
	}
	for pattern, resp := range r.responses {
		if strings.Contains(cmd, pattern) {
			return resp, nil
		}
	}
	return "", fmt.Errorf("unexpected command: %s", cmd)
}

func (r *recordingRunner) lastCommand() string { return r.commands[len(r.commands)-1] }

func TestParseScope(t *testing.T) {
	for _, tc := range []struct {
		in      string
		want    systemd.Scope
		wantErr bool
	}{
		{"", systemd.ScopeSystem, false},
		{"system", systemd.ScopeSystem, false},
		{"USER", systemd.ScopeUser, false},
		{" user ", systemd.ScopeUser, false},
		{"global", "", true},
	} {
		got, err := systemd.ParseScope(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("ParseScope(%q) expected error, got %q", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseScope(%q) unexpected error: %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("ParseScope(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestNormalizeUnitName(t *testing.T) {
	valid := map[string]string{
		"sshd":               "sshd.service",
		"sshd.service":       "sshd.service",
		"getty@tty1.service": "getty@tty1.service",
		"nginx.socket":       "nginx.socket",
		"foo-bar_baz.timer":  "foo-bar_baz.timer",
	}
	for in, want := range valid {
		got, err := systemd.NormalizeUnitName(in)
		if err != nil {
			t.Errorf("NormalizeUnitName(%q) unexpected error: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("NormalizeUnitName(%q) = %q, want %q", in, got, want)
		}
	}

	for _, bad := range []string{
		"",
		"  ",
		"../../etc/passwd",
		"sshd.service; rm -rf /",
		"sshd service",
		"/etc/systemd/system/x.service",
		"$(id)",
	} {
		if _, err := systemd.NormalizeUnitName(bad); err == nil {
			t.Errorf("NormalizeUnitName(%q) expected error, got nil", bad)
		}
	}
}

const sampleListUnits = `[{"unit":"sshd.service","load":"loaded","active":"active","sub":"running","description":"OpenSSH Daemon"},
{"unit":"caddy.service","load":"loaded","active":"failed","sub":"failed","description":"Caddy Web Server"},
{"unit":"apparmor.service","load":"not-found","active":"inactive","sub":"dead","description":"apparmor.service"}]`

const sampleListUnitFiles = `[{"unit_file":"sshd.service","state":"enabled","preset":"disabled"},
{"unit_file":"caddy.service","state":"disabled","preset":null},
{"unit_file":"apparmor.service","state":"masked","preset":null}]`

func TestListUnitsMergesUnitFileState(t *testing.T) {
	r := &recordingRunner{responses: map[string]string{
		"list-units":      sampleListUnits,
		"list-unit-files": sampleListUnitFiles,
	}}

	units, err := systemd.ListUnits(context.Background(), r, systemd.ScopeSystem)
	if err != nil {
		t.Fatalf("ListUnits error: %v", err)
	}
	if len(units) != 3 {
		t.Fatalf("expected 3 units, got %d", len(units))
	}

	byName := map[string]systemd.Unit{}
	for _, u := range units {
		byName[u.Unit] = u
	}
	if !byName["sshd.service"].Enabled || byName["sshd.service"].UnitFileState != "enabled" {
		t.Errorf("sshd should be enabled: %+v", byName["sshd.service"])
	}
	if byName["caddy.service"].Enabled {
		t.Errorf("caddy should not be enabled: %+v", byName["caddy.service"])
	}
	if byName["caddy.service"].Active != "failed" || byName["caddy.service"].Sub != "failed" {
		t.Errorf("caddy state mismatch: %+v", byName["caddy.service"])
	}
	if byName["apparmor.service"].Enabled {
		t.Errorf("masked unit must not report enabled")
	}
	if units[0].Scope != "system" {
		t.Errorf("scope = %q, want system", units[0].Scope)
	}
	// Sorted by unit name.
	if units[0].Unit != "apparmor.service" || units[2].Unit != "sshd.service" {
		t.Errorf("units not sorted: %v", []string{units[0].Unit, units[1].Unit, units[2].Unit})
	}
}

func TestListUnitsUserScopeFlagsCommand(t *testing.T) {
	r := &recordingRunner{responses: map[string]string{
		"list-units":      `[{"unit":"gpg-agent.service","load":"loaded","active":"active","sub":"running","description":"GnuPG agent"}]`,
		"list-unit-files": `[]`,
	}}
	units, err := systemd.ListUnits(context.Background(), r, systemd.ScopeUser)
	if err != nil {
		t.Fatalf("ListUnits error: %v", err)
	}
	if len(units) != 1 || units[0].Scope != "user" {
		t.Fatalf("unexpected units: %+v", units)
	}
	for _, cmd := range r.commands {
		if !strings.HasPrefix(cmd, "systemctl --user ") {
			t.Errorf("user scope command missing --user: %q", cmd)
		}
	}
}

func TestListUnitsRejectsNonJSON(t *testing.T) {
	r := &recordingRunner{responses: map[string]string{
		"list-units": "sshd.service loaded active running OpenSSH Daemon",
	}}
	if _, err := systemd.ListUnits(context.Background(), r, systemd.ScopeSystem); err == nil {
		t.Fatal("expected error for non-JSON output (caller must fall back)")
	}
}

func TestListAllScopesToleratesMissingUserManager(t *testing.T) {
	r := &recordingRunner{
		responses: map[string]string{
			"list-units":      sampleListUnits,
			"list-unit-files": sampleListUnitFiles,
		},
		errs: map[string]error{
			"systemctl --user list-units": fmt.Errorf("Failed to connect to bus: No medium found"),
		},
	}

	units, warnings, err := systemd.ListAllScopes(context.Background(), r)
	if err != nil {
		t.Fatalf("ListAllScopes error: %v", err)
	}
	if len(units) != 3 {
		t.Fatalf("expected system units to survive, got %d", len(units))
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "user manager unavailable") {
		t.Fatalf("expected user manager warning, got %v", warnings)
	}
}

const sampleShow = `Id=sshd.service
Description=OpenSSH Daemon
LoadState=loaded
ActiveState=active
SubState=running
UnitFileState=enabled
FragmentPath=/usr/lib/systemd/system/sshd.service
DropInPaths=/etc/systemd/system/sshd.service.d/atc-override.conf
MainPID=903
NRestarts=2
Result=success
ActiveEnterTimestamp=Thu 2026-10-15 17:17:04 EDT
StateChangeTimestamp=Thu 2026-10-15 17:17:04 EDT
InactiveEnterTimestamp=
`

func TestShowUnitParsesProperties(t *testing.T) {
	r := &recordingRunner{responses: map[string]string{"show": sampleShow}}

	state, err := systemd.ShowUnit(context.Background(), r, systemd.ScopeSystem, "sshd")
	if err != nil {
		t.Fatalf("ShowUnit error: %v", err)
	}
	if state.Unit != "sshd.service" || state.ActiveState != "active" || state.SubState != "running" {
		t.Errorf("unexpected state: %+v", state)
	}
	if !state.Enabled || state.UnitFileState != "enabled" {
		t.Errorf("expected enabled state: %+v", state)
	}
	if state.MainPID != 903 || state.NRestarts != 2 {
		t.Errorf("unexpected pid/restarts: %+v", state)
	}
	if len(state.DropInPaths) != 1 || state.DropInPaths[0] != "/etc/systemd/system/sshd.service.d/atc-override.conf" {
		t.Errorf("unexpected drop-ins: %v", state.DropInPaths)
	}
	if !strings.Contains(r.lastCommand(), "show 'sshd.service' --no-pager --property=") {
		t.Errorf("unexpected command: %q", r.lastCommand())
	}
}

func TestShowUnitRejectsBadName(t *testing.T) {
	r := &recordingRunner{}
	if _, err := systemd.ShowUnit(context.Background(), r, systemd.ScopeSystem, "sshd; reboot"); err == nil {
		t.Fatal("expected validation error")
	}
	if len(r.commands) != 0 {
		t.Fatalf("no command should run for an invalid unit, got %v", r.commands)
	}
}

const sampleJournal = `{"__REALTIME_TIMESTAMP":"1792480000123456","PRIORITY":"6","MESSAGE":"Started OpenSSH Daemon.","SYSLOG_IDENTIFIER":"systemd","_PID":"1","_SYSTEMD_UNIT":"sshd.service"}
{"__REALTIME_TIMESTAMP":"1792480001123456","PRIORITY":"3","MESSAGE":[83,116,97,114,116,105,110,103,32,102,97,105,108,101,100],"SYSLOG_IDENTIFIER":"sshd","_PID":"1200","_SYSTEMD_UNIT":"sshd.service"}
{"__REALTIME_TIMESTAMP":"1792480002123456","PRIORITY":"4","MESSAGE":"No host key","SYSLOG_IDENTIFIER":"sshd","_PID":"1200","_SYSTEMD_USER_UNIT":"gpg-agent.service"}
not json`

func TestJournalParsesStructuredEntries(t *testing.T) {
	r := &recordingRunner{responses: map[string]string{"journalctl": sampleJournal}}

	entries, err := systemd.Journal(context.Background(), r, systemd.ScopeSystem, systemd.JournalOptions{
		Unit:  "sshd",
		Lines: 50,
	})
	if err != nil {
		t.Fatalf("Journal error: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("expected 3 parsed entries, got %d", len(entries))
	}
	if entries[0].Message != "Started OpenSSH Daemon." || entries[0].Level != "info" {
		t.Errorf("unexpected first entry: %+v", entries[0])
	}
	if entries[1].Message != "Starting failed" {
		t.Errorf("byte-array message not decoded: %q", entries[1].Message)
	}
	if entries[1].Priority != 3 || entries[1].Level != "err" {
		t.Errorf("unexpected priority mapping: %+v", entries[1])
	}
	if entries[2].Unit != "gpg-agent.service" {
		t.Errorf("user unit not folded into Unit: %+v", entries[2])
	}
	if entries[0].Timestamp == "" || !strings.HasPrefix(entries[0].Timestamp, "2026-") {
		t.Errorf("unexpected timestamp: %q", entries[0].Timestamp)
	}
	cmd := r.lastCommand()
	if !strings.Contains(cmd, "-u 'sshd.service'") || !strings.Contains(cmd, "-n 50") || !strings.Contains(cmd, "-o json") {
		t.Errorf("unexpected journal command: %q", cmd)
	}
}

func TestJournalUserScopeAndPriority(t *testing.T) {
	r := &recordingRunner{responses: map[string]string{"journalctl": ""}}
	if _, err := systemd.Journal(context.Background(), r, systemd.ScopeUser, systemd.JournalOptions{
		Unit:     "gpg-agent.service",
		Lines:    10,
		Priority: "err",
	}); err != nil {
		t.Fatalf("Journal error: %v", err)
	}
	cmd := r.lastCommand()
	if !strings.HasPrefix(cmd, "journalctl --user ") || !strings.Contains(cmd, "-p err") {
		t.Errorf("unexpected command: %q", cmd)
	}
}

func TestJournalRejectsInjection(t *testing.T) {
	r := &recordingRunner{responses: map[string]string{"journalctl": ""}}
	if _, err := systemd.Journal(context.Background(), r, systemd.ScopeSystem, systemd.JournalOptions{
		Unit: "sshd.service'; reboot; #",
	}); err == nil {
		t.Fatal("expected validation error for injected unit name")
	}
	if len(r.commands) != 0 {
		t.Fatalf("no command should run, got %v", r.commands)
	}
}

// ensure the package still satisfies the runner contract it depends on.
var _ plugin.Runner = (*recordingRunner)(nil)
