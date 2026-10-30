// Package systemd queries and validates systemd units through systemd's own
// machine-readable interfaces instead of scraping human-readable tables.
//
// All reads run through the plugin.Runner (SSH / tsnet / local), so this works
// against agentless remote targets: nothing is installed on the target beyond
// systemd itself. The commands used are:
//
//	systemctl [--user] list-units --type=service --all --output=json
//	systemctl [--user] list-unit-files --type=service --output=json
//	systemctl [--user] show <unit> --property=<key>,...
//	journalctl [--user] -u <unit> -n <N> -o json
//
// `systemctl show --property=` is the documented CLI projection of the D-Bus
// properties of org.freedesktop.systemd1.Unit; every value is emitted as
// Key=Value with no locale-dependent formatting, so it is machine-readable.
// A direct D-Bus client is not used because the daemon is agentless: a D-Bus
// connection can only be established to the local bus, never over SSH.
//
// Nothing here fabricates state. If systemd cannot answer, the caller gets an
// error carrying the command's own stderr.
package systemd

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"air-traffic-control/backend/pkg/plugin"
)

// Scope selects which systemd manager instance is queried.
type Scope string

const (
	// ScopeSystem is the host-wide manager (PID 1).
	ScopeSystem Scope = "system"
	// ScopeUser is the per-user manager instance (systemctl --user).
	ScopeUser Scope = "user"
)

// ParseScope normalises a scope string. An empty string means ScopeSystem.
func ParseScope(raw string) (Scope, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", string(ScopeSystem):
		return ScopeSystem, nil
	case string(ScopeUser):
		return ScopeUser, nil
	}
	return "", fmt.Errorf("invalid systemd scope %q (expected %q or %q)", raw, ScopeSystem, ScopeUser)
}

// flag returns the systemctl/journalctl flag for the scope.
func (s Scope) flag() string {
	if s == ScopeUser {
		return " --user"
	}
	return ""
}

// String implements fmt.Stringer.
func (s Scope) String() string { return string(s) }

// Unit is a single service unit merged from list-units and list-unit-files.
type Unit struct {
	Unit          string `json:"unit"`
	Load          string `json:"load"`
	Active        string `json:"active"`
	Sub           string `json:"sub"`
	Description   string `json:"description"`
	UnitFileState string `json:"unitFileState,omitempty"`
	Enabled       bool   `json:"enabled"`
	Scope         string `json:"scope"`
}

// State is the per-unit property projection returned by ShowUnit.
type State struct {
	Unit                   string   `json:"unit"`
	Scope                  string   `json:"scope"`
	Description            string   `json:"description,omitempty"`
	LoadState              string   `json:"loadState"`
	ActiveState            string   `json:"activeState"`
	SubState               string   `json:"subState"`
	UnitFileState          string   `json:"unitFileState,omitempty"`
	Enabled                bool     `json:"enabled"`
	Result                 string   `json:"result,omitempty"`
	FragmentPath           string   `json:"fragmentPath,omitempty"`
	DropInPaths            []string `json:"dropInPaths,omitempty"`
	MainPID                int      `json:"mainPid,omitempty"`
	NRestarts              int      `json:"nRestarts,omitempty"`
	ActiveEnterTimestamp   string   `json:"activeEnterTimestamp,omitempty"`
	StateChangeTimestamp   string   `json:"stateChangeTimestamp,omitempty"`
	InactiveEnterTimestamp string   `json:"inactiveEnterTimestamp,omitempty"`
}

// JournalEntry is one structured journald record.
type JournalEntry struct {
	Timestamp  string `json:"timestamp"`
	Priority   int    `json:"priority"`
	Level      string `json:"level"`
	Unit       string `json:"unit,omitempty"`
	Identifier string `json:"identifier,omitempty"`
	PID        string `json:"pid,omitempty"`
	Message    string `json:"message"`
}

// unitNameRe matches a systemd unit name, including templated instances.
var unitNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9:_.@-]*\.(service|socket|timer|target|mount|automount|path|slice|scope|swap|device)$`)

// NormalizeUnitName validates a unit name and returns it with its type suffix.
// A bare name such as "sshd" is normalised to "sshd.service". Anything that is
// not a plain unit name is rejected, so a name can never break out of the shell
// command or of a drop-in path.
func NormalizeUnitName(name string) (string, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return "", fmt.Errorf("unit name is required")
	}
	if strings.Contains(trimmed, "..") || strings.ContainsAny(trimmed, "/ 	\n") {
		return "", fmt.Errorf("invalid systemd unit name %q", name)
	}
	if unitNameRe.MatchString(trimmed) {
		return trimmed, nil
	}
	if candidate := trimmed + ".service"; unitNameRe.MatchString(candidate) {
		return candidate, nil
	}
	return "", fmt.Errorf("invalid systemd unit name %q", name)
}

// ValidateUnitName reports whether name is a valid (optionally unsuffixed) unit.
func ValidateUnitName(name string) error {
	_, err := NormalizeUnitName(name)
	return err
}

// quote single-quotes a value for safe interpolation into the shell command the
// Runner executes. Unit names are validated first; this is defence in depth.
func quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

type rawUnit struct {
	Unit        string `json:"unit"`
	Load        string `json:"load"`
	Active      string `json:"active"`
	Sub         string `json:"sub"`
	Description string `json:"description"`
}

type rawUnitFile struct {
	UnitFile string  `json:"unit_file"`
	State    string  `json:"state"`
	Preset   *string `json:"preset"`
}

// isEnabledState maps a systemd unit file state to "will start at boot".
func isEnabledState(state string) bool {
	switch state {
	case "enabled", "enabled-runtime":
		return true
	}
	return false
}

// ListUnits returns every service unit known to the given manager, with the
// unit-file state (enabled/disabled/static/masked) merged in from
// list-unit-files. A failure of the second query is tolerated: units are still
// returned, just without UnitFileState.
func ListUnits(ctx context.Context, r plugin.Runner, scope Scope) ([]Unit, error) {
	if r == nil {
		return nil, fmt.Errorf("runner is required")
	}

	listCmd := fmt.Sprintf("systemctl%s list-units --type=service --all --no-pager --output=json", scope.flag())
	out, err := r.RunCommand(ctx, listCmd)
	if err != nil {
		return nil, fmt.Errorf("systemctl list-units failed: %w (output: %s)", err, strings.TrimSpace(out))
	}
	trimmed := strings.TrimSpace(out)
	if !strings.HasPrefix(trimmed, "[") {
		return nil, fmt.Errorf("systemctl list-units did not return JSON (systemd too old?): %s", truncate(trimmed, 200))
	}

	var raw []rawUnit
	if err := json.Unmarshal([]byte(trimmed), &raw); err != nil {
		return nil, fmt.Errorf("failed to parse systemctl list-units JSON: %w", err)
	}

	files := map[string]string{}
	fileCmd := fmt.Sprintf("systemctl%s list-unit-files --type=service --no-pager --output=json", scope.flag())
	if fileOut, fileErr := r.RunCommand(ctx, fileCmd); fileErr == nil {
		fileTrimmed := strings.TrimSpace(fileOut)
		if strings.HasPrefix(fileTrimmed, "[") {
			var rawFiles []rawUnitFile
			if jsonErr := json.Unmarshal([]byte(fileTrimmed), &rawFiles); jsonErr == nil {
				for _, f := range rawFiles {
					files[f.UnitFile] = f.State
				}
			}
		}
	}

	units := make([]Unit, 0, len(raw))
	for _, u := range raw {
		state := files[u.Unit]
		units = append(units, Unit{
			Unit:          u.Unit,
			Load:          u.Load,
			Active:        u.Active,
			Sub:           u.Sub,
			Description:   u.Description,
			UnitFileState: state,
			Enabled:       isEnabledState(state),
			Scope:         scope.String(),
		})
	}

	sort.Slice(units, func(i, j int) bool { return units[i].Unit < units[j].Unit })
	return units, nil
}

// ListAllScopes returns system units followed by user units. A user manager
// that cannot be reached (no user session / no lingering) is reported in the
// warnings rather than failing the whole read.
func ListAllScopes(ctx context.Context, r plugin.Runner) ([]Unit, []string, error) {
	systemUnits, err := ListUnits(ctx, r, ScopeSystem)
	if err != nil {
		return nil, nil, err
	}

	units := systemUnits
	var warnings []string
	userUnits, userErr := ListUnits(ctx, r, ScopeUser)
	if userErr != nil {
		warnings = append(warnings, fmt.Sprintf("user manager unavailable: %v", userErr))
	} else {
		units = append(units, userUnits...)
	}
	return units, warnings, nil
}

// showProperties lists the D-Bus properties projected for a unit state read.
var showProperties = []string{
	"Id",
	"Description",
	"LoadState",
	"ActiveState",
	"SubState",
	"UnitFileState",
	"FragmentPath",
	"DropInPaths",
	"MainPID",
	"NRestarts",
	"Result",
	"ActiveEnterTimestamp",
	"StateChangeTimestamp",
	"InactiveEnterTimestamp",
}

// ShowUnit reads one unit's state from systemd's property interface.
func ShowUnit(ctx context.Context, r plugin.Runner, scope Scope, unit string) (State, error) {
	if r == nil {
		return State{}, fmt.Errorf("runner is required")
	}
	normalized, err := NormalizeUnitName(unit)
	if err != nil {
		return State{}, err
	}
	unit = normalized

	cmd := fmt.Sprintf("systemctl%s show %s --no-pager --property=%s",
		scope.flag(), quote(unit), strings.Join(showProperties, ","))
	out, err := r.RunCommand(ctx, cmd)
	if err != nil {
		return State{}, fmt.Errorf("systemctl show %s failed: %w (output: %s)", unit, err, strings.TrimSpace(out))
	}

	props := parseProperties(out)
	if props["Id"] == "" && props["LoadState"] == "" {
		return State{}, fmt.Errorf("systemd returned no state for unit %s", unit)
	}

	state := State{
		Unit:                   props["Id"],
		Scope:                  scope.String(),
		Description:            props["Description"],
		LoadState:              props["LoadState"],
		ActiveState:            props["ActiveState"],
		SubState:               props["SubState"],
		UnitFileState:          props["UnitFileState"],
		Enabled:                isEnabledState(props["UnitFileState"]),
		Result:                 props["Result"],
		FragmentPath:           props["FragmentPath"],
		ActiveEnterTimestamp:   props["ActiveEnterTimestamp"],
		StateChangeTimestamp:   props["StateChangeTimestamp"],
		InactiveEnterTimestamp: props["InactiveEnterTimestamp"],
	}
	if state.Unit == "" {
		state.Unit = unit
	}
	if p := strings.TrimSpace(props["DropInPaths"]); p != "" {
		state.DropInPaths = strings.Fields(p)
	}
	if pid, convErr := strconv.Atoi(strings.TrimSpace(props["MainPID"])); convErr == nil {
		state.MainPID = pid
	}
	if n, convErr := strconv.Atoi(strings.TrimSpace(props["NRestarts"])); convErr == nil {
		state.NRestarts = n
	}
	return state, nil
}

// parseProperties parses systemctl show Key=Value output. Repeated keys (none of
// the properties above repeat) would keep the last value.
func parseProperties(out string) map[string]string {
	props := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		props[strings.TrimSpace(key)] = value
	}
	return props
}

var priorityNames = map[int]string{
	0: "emerg", 1: "alert", 2: "crit", 3: "err", 4: "warning", 5: "notice", 6: "info", 7: "debug",
}

var validPriorityRe = regexp.MustCompile(`^[0-7]$|^(emerg|emergency|alert|crit|critical|err|error|warn|warning|notice|info|debug)$`)

// JournalOptions narrows a journal query.
type JournalOptions struct {
	// Unit limits output to one unit. Empty means "no unit filter".
	Unit string
	// Lines is the tail length; clamped to [1, 5000], default 200.
	Lines int
	// Priority is a journald priority filter (name or 0-7). Optional.
	Priority string
}

// Journal reads structured journald records, optionally scoped to one unit.
func Journal(ctx context.Context, r plugin.Runner, scope Scope, opts JournalOptions) ([]JournalEntry, error) {
	if r == nil {
		return nil, fmt.Errorf("runner is required")
	}

	lines := opts.Lines
	if lines <= 0 {
		lines = 200
	}
	if lines > 5000 {
		lines = 5000
	}

	cmd := fmt.Sprintf("journalctl%s -n %d -o json --no-pager", scope.flag(), lines)
	if strings.TrimSpace(opts.Unit) != "" {
		normalized, err := NormalizeUnitName(opts.Unit)
		if err != nil {
			return nil, err
		}
		cmd += " -u " + quote(normalized)
	}
	if p := strings.ToLower(strings.TrimSpace(opts.Priority)); p != "" {
		if !validPriorityRe.MatchString(p) {
			return nil, fmt.Errorf("invalid journal priority %q", opts.Priority)
		}
		cmd += " -p " + p
	}

	out, err := r.RunCommand(ctx, cmd)
	if err != nil {
		// journalctl exits non-zero on hosts whose journal is unreadable for the
		// connecting user; surface that verbatim rather than pretending it is empty.
		return nil, fmt.Errorf("journalctl failed: %w (output: %s)", err, truncate(strings.TrimSpace(out), 300))
	}

	entries := make([]JournalEntry, 0, 32)
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.HasPrefix(line, "{") {
			continue
		}
		entry, ok := parseJournalEntry(line)
		if !ok {
			continue
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

// rawJournal matches the subset of `journalctl -o json` fields ATC consumes.
type rawJournal struct {
	RealtimeUsec json.RawMessage `json:"__REALTIME_TIMESTAMP"`
	Priority     json.RawMessage `json:"PRIORITY"`
	Message      json.RawMessage `json:"MESSAGE"`
	Identifier   string          `json:"SYSLOG_IDENTIFIER"`
	PID          json.RawMessage `json:"_PID"`
	Unit         string          `json:"_SYSTEMD_UNIT"`
	UserUnit     string          `json:"_SYSTEMD_USER_UNIT"`
}

func parseJournalEntry(line string) (JournalEntry, bool) {
	var raw rawJournal
	if err := json.Unmarshal([]byte(line), &raw); err != nil {
		return JournalEntry{}, false
	}

	entry := JournalEntry{
		Identifier: raw.Identifier,
		Unit:       raw.Unit,
		PID:        jsonScalar(raw.PID),
		Message:    jsonScalar(raw.Message),
	}
	if entry.Unit == "" {
		entry.Unit = raw.UserUnit
	}

	if usec, err := strconv.ParseInt(jsonScalar(raw.RealtimeUsec), 10, 64); err == nil && usec > 0 {
		entry.Timestamp = time.UnixMicro(usec).Format(time.RFC3339)
	}
	if p, err := strconv.Atoi(jsonScalar(raw.Priority)); err == nil {
		entry.Priority = p
		entry.Level = priorityNames[p]
	}
	return entry, true
}

// jsonScalar decodes a journald field that may be a JSON string, a JSON number,
// or an array of byte values (journald emits MESSAGE that way for non-UTF-8
// payloads).
func jsonScalar(raw json.RawMessage) string {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return ""
	}
	if strings.HasPrefix(trimmed, "[") {
		var bytes []int
		if err := json.Unmarshal(raw, &bytes); err == nil {
			out := make([]byte, 0, len(bytes))
			for _, b := range bytes {
				if b >= 0 && b <= 255 {
					out = append(out, byte(b))
				}
			}
			return strings.ToValidUTF8(string(out), "\uFFFD")
		}
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var n json.Number
	if err := json.Unmarshal(raw, &n); err == nil {
		return n.String()
	}
	return ""
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

// UserManager identifies a per-user systemd manager reachable through
// /run/user/<uid>.
type UserManager struct {
	UID  string `json:"uid"`
	Name string `json:"name"`
}

// ListUserManagers discovers the user managers present on a host by scanning
// /run/user for live session runtime directories. It never invents a user: a
// UID without a resolvable name is still reported with its numeric UID.
func ListUserManagers(ctx context.Context, r plugin.Runner) ([]UserManager, error) {
	if r == nil {
		return nil, fmt.Errorf("runner is required")
	}
	cmd := `for d in /run/user/*; do [ -d "$d" ] || continue; uid=${d##*/}; ` +
		`case "$uid" in *[!0-9]*) continue;; esac; ` +
		`name=$(id -nu "$uid" 2>/dev/null || echo "$uid"); echo "$uid $name"; done`
	out, err := r.RunCommand(ctx, cmd)
	if err != nil {
		return nil, fmt.Errorf("failed to enumerate user managers: %w (output: %s)", err, strings.TrimSpace(out))
	}

	managers := make([]UserManager, 0, 4)
	seen := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 1 || !isNumeric(fields[0]) || seen[fields[0]] {
			continue
		}
		name := fields[0]
		if len(fields) > 1 {
			name = fields[1]
		}
		seen[fields[0]] = true
		managers = append(managers, UserManager{UID: fields[0], Name: name})
	}
	sort.Slice(managers, func(i, j int) bool { return managers[i].UID < managers[j].UID })
	return managers, nil
}

func isNumeric(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// UserRunner prefixes every command so `systemctl --user` / `journalctl --user`
// target the selected user's manager instead of the SSH login user's.
type UserRunner struct {
	Base plugin.Runner
	UID  string
}

// RunCommand implements plugin.Runner.
func (u UserRunner) RunCommand(ctx context.Context, cmd string) (string, error) {
	if strings.TrimSpace(u.UID) == "" {
		return u.Base.RunCommand(ctx, cmd)
	}
	uid := strings.TrimSpace(u.UID)
	prefix := fmt.Sprintf(`runuser -u "$(id -nu %s)" -- env XDG_RUNTIME_DIR=/run/user/%s DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/%s/bus `, uid, uid, uid)
	return u.Base.RunCommand(ctx, prefix+cmd)
}
