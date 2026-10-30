package collections

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"
)

// latestTTL bounds how long a Galaxy version lookup is reused.
const latestTTL = 6 * time.Hour

// statePage is the global_settings page that holds the collection state.
const statePage = "ansible/collections"

// LatestVersion is a cached Galaxy lookup for one collection.
type LatestVersion struct {
	Version   string `json:"version"`
	CheckedAt int64  `json:"checkedAt"`
}

// persistedState is the durable half of the manager: operator opt-outs and the
// version-lookup cache. It survives restarts, which is what makes "Remove"
// stick instead of being undone by the next catalog pass.
type persistedState struct {
	Disabled []string                 `json:"disabled,omitempty"`
	Latest   map[string]LatestVersion `json:"latest,omitempty"`
	// Pinned holds operator version pins. A pinned collection is never moved by
	// an update or the weekly pass; Ensure reinstalls the pinned version if it
	// drifts.
	Pinned map[string]string `json:"pinned,omitempty"`
	// AutoUpdate enables the opt-in weekly `--upgrade` pass.
	AutoUpdate        bool  `json:"autoUpdate,omitempty"`
	AutoUpdateLastRun int64 `json:"autoUpdateLastRun,omitempty"`
}

// StateStore is the durable backing for the collection state. It is satisfied
// by the plugin settings store (global_settings table); a nil store keeps the
// state in memory only, which is what unit tests use.
type StateStore interface {
	Value(pageID, field string) (string, bool)
	Save(pageID string, values map[string]any) error
}

// SetStateStore wires the durable store. It must be called before the manager
// serves requests; later calls reload the state.
func (m *Manager) SetStateStore(store StateStore) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.store = store
	m.stateLoaded = false
	m.state = persistedState{}
}

// loadStateLocked reads the persisted state once. Callers hold m.mu.
func (m *Manager) loadStateLocked() {
	if m.stateLoaded {
		return
	}
	m.stateLoaded = true
	if m.store == nil {
		return
	}
	raw, ok := m.store.Value(statePage, "state")
	if !ok || strings.TrimSpace(raw) == "" {
		return
	}
	// The settings store returns scalar values verbatim but renders objects with
	// fmt.Sprint, so the state is persisted as a JSON *string* and decoded here.
	var decoded persistedState
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		log.Printf("ansible collections: ignoring corrupt state (%v)", err)
		return
	}
	m.state = decoded
}

// saveStateLocked persists the state. Callers hold m.mu.
func (m *Manager) saveStateLocked() {
	if m.store == nil {
		return
	}
	encoded, err := json.Marshal(m.state)
	if err != nil {
		log.Printf("ansible collections: could not encode state: %v", err)
		return
	}
	if err := m.store.Save(statePage, map[string]any{"state": string(encoded)}); err != nil {
		log.Printf("ansible collections: could not persist state: %v", err)
	}
}

// Disabled lists the collections the operator removed on purpose.
func (m *Manager) Disabled() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.loadStateLocked()
	out := make([]string, 0, len(m.state.Disabled))
	out = append(out, m.state.Disabled...)
	sort.Strings(out)
	return out
}

// IsDisabled reports whether a collection is opted out.
func (m *Manager) IsDisabled(name string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.loadStateLocked()
	for _, entry := range m.state.Disabled {
		if entry == name {
			return true
		}
	}
	return false
}

// SetDisabled records (or clears) an operator opt-out. Disabled collections are
// skipped by Ensure and EnsureCatalog until they are enabled again.
func (m *Manager) SetDisabled(name string, disabled bool) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}

	m.mu.Lock()
	m.loadStateLocked()
	changed := false
	next := make([]string, 0, len(m.state.Disabled)+1)
	for _, entry := range m.state.Disabled {
		if entry == name {
			if disabled {
				next = append(next, entry)
			} else {
				changed = true
			}
			continue
		}
		next = append(next, entry)
	}
	if disabled && !changed && !contains(next, name) {
		next = append(next, name)
		changed = true
	}
	if changed {
		m.state.Disabled = next
		sort.Strings(m.state.Disabled)
		m.saveStateLocked()
	}
	m.mu.Unlock()
	return nil
}

// Pinned returns a copy of the version pins.
func (m *Manager) Pinned() map[string]string {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.loadStateLocked()
	out := make(map[string]string, len(m.state.Pinned))
	for name, version := range m.state.Pinned {
		out[name] = version
	}
	return out
}

// PinnedVersion reports the pinned version for a collection, if any.
func (m *Manager) PinnedVersion(name string) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.loadStateLocked()
	version, ok := m.state.Pinned[name]
	return version, ok
}

// Pin freezes a collection at a version. An empty version pins whatever is
// installed right now.
func (m *Manager) Pin(ctx context.Context, name, version string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || name == "ansible.builtin" {
		return "", fmt.Errorf("cannot pin %q", name)
	}
	version = strings.TrimSpace(version)
	if version == "" {
		installed, err := m.Installed(ctx)
		if err != nil {
			return "", err
		}
		current, ok := installed[name]
		if !ok {
			return "", fmt.Errorf("%s is not installed, so there is no version to pin", name)
		}
		version = current
	}

	m.mu.Lock()
	m.loadStateLocked()
	if m.state.Pinned == nil {
		m.state.Pinned = map[string]string{}
	}
	m.state.Pinned[name] = version
	m.saveStateLocked()
	m.mu.Unlock()
	return version, nil
}

// Unpin releases a version pin.
func (m *Manager) Unpin(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.loadStateLocked()
	if _, ok := m.state.Pinned[strings.TrimSpace(name)]; !ok {
		return nil
	}
	delete(m.state.Pinned, strings.TrimSpace(name))
	m.saveStateLocked()
	return nil
}

// AutoUpdateEnabled reports whether the weekly upgrade pass is on.
func (m *Manager) AutoUpdateEnabled() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.loadStateLocked()
	return m.state.AutoUpdate
}

// SetAutoUpdate toggles the weekly upgrade pass. Enabling starts the clock, so
// the first automatic pass happens a week later rather than immediately.
func (m *Manager) SetAutoUpdate(enabled bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.loadStateLocked()
	m.state.AutoUpdate = enabled
	if enabled && m.state.AutoUpdateLastRun == 0 {
		m.state.AutoUpdateLastRun = time.Now().UnixMilli()
	}
	m.saveStateLocked()
	return nil
}

// autoUpdateInterval is the minimum spacing between automatic upgrade passes.
const autoUpdateInterval = 7 * 24 * time.Hour

// MaybeAutoUpdate runs the weekly upgrade pass when it is enabled and due.
// Pinned and disabled collections are left alone. It reports whether a pass ran.
func (m *Manager) MaybeAutoUpdate(ctx context.Context) (bool, []UpdateResult) {
	m.mu.Lock()
	m.loadStateLocked()
	enabled := m.state.AutoUpdate
	last := m.state.AutoUpdateLastRun
	pinned := make([]string, 0, len(m.state.Pinned))
	for name := range m.state.Pinned {
		pinned = append(pinned, name)
	}
	m.mu.Unlock()

	if !enabled {
		return false, nil
	}
	if last > 0 && time.Since(time.UnixMilli(last)) < autoUpdateInterval {
		return false, nil
	}

	installed, err := m.Installed(ctx)
	if err != nil {
		log.Printf("ansible collections: weekly update skipped: %v", err)
		return false, nil
	}
	names := make([]string, 0, len(installed))
	for name := range installed {
		if contains(pinned, name) || m.IsDisabled(name) {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)

	results := m.Update(ctx, names)

	m.mu.Lock()
	m.loadStateLocked()
	m.state.AutoUpdateLastRun = time.Now().UnixMilli()
	m.saveStateLocked()
	m.mu.Unlock()

	updated := 0
	for _, result := range results {
		if result.Changed {
			updated++
		}
	}
	log.Printf("ansible collections: weekly update pass finished (%d upgraded, %d checked)", updated, len(results))
	return true, results
}

// Required reports whether ATC's own mutation pipeline depends on a collection.
// Removing these breaks privilege escalation or firewall/service modules.
func Required(name string) bool {
	switch strings.TrimSpace(name) {
	case CommunityGeneral, AnsiblePosix:
		return true
	}
	return false
}

// LatestVersionFor returns the newest published version, cached for the lookup
// TTL. It never invents a version: an offline lookup returns an error and the
// caller reports "unknown".
func (m *Manager) LatestVersionFor(names []string) (map[string]LatestVersion, map[string]error) {
	m.mu.Lock()
	m.loadStateLocked()
	cached := make(map[string]LatestVersion, len(names))
	stale := make([]string, 0, len(names))
	now := time.Now()
	for _, name := range names {
		entry, ok := m.state.Latest[name]
		if ok && now.Sub(time.UnixMilli(entry.CheckedAt)) < latestTTL {
			cached[name] = entry
			continue
		}
		stale = append(stale, name)
	}
	m.mu.Unlock()

	errs := map[string]error{}
	if len(stale) > 0 {
		results, lookupErrs := m.fetchLatest(stale)
		for name, err := range lookupErrs {
			errs[name] = err
		}
		if len(results) > 0 {
			m.mu.Lock()
			m.loadStateLocked()
			if m.state.Latest == nil {
				m.state.Latest = map[string]LatestVersion{}
			}
			for name, entry := range results {
				m.state.Latest[name] = entry
				cached[name] = entry
			}
			m.saveStateLocked()
			m.mu.Unlock()
		}
	}
	return cached, errs
}

// UpdateStatus describes one collection's installed vs published version.
type UpdateStatus struct {
	Name            string `json:"name"`
	Installed       string `json:"installed"`
	Latest          string `json:"latest,omitempty"`
	UpdateAvailable bool   `json:"updateAvailable"`
	Pinned          bool   `json:"pinned,omitempty"`
	CheckError      string `json:"checkError,omitempty"`
}

// CheckUpdates compares installed versions with the published ones.
func (m *Manager) CheckUpdates(ctx context.Context) []UpdateStatus {
	installed, err := m.Installed(ctx)
	names := make([]string, 0, len(installed))
	for name := range installed {
		names = append(names, name)
	}
	sort.Strings(names)

	latest, lookupErrs := m.LatestVersionFor(names)
	out := make([]UpdateStatus, 0, len(names))
	for _, name := range names {
		status := UpdateStatus{Name: name, Installed: installed[name]}
		if pin, pinned := m.PinnedVersion(name); pinned {
			status.Pinned = true
			status.Latest = pin
			status.UpdateAvailable = false
		} else if entry, ok := latest[name]; ok {
			status.Latest = entry.Version
			status.UpdateAvailable = CompareVersions(entry.Version, installed[name]) > 0
		}
		if lookupErr, ok := lookupErrs[name]; ok {
			status.CheckError = lookupErr.Error()
		}
		if err != nil {
			status.CheckError = err.Error()
		}
		out = append(out, status)
	}
	return out
}

// UpdateResult reports one upgrade attempt.
type UpdateResult struct {
	Name    string `json:"name"`
	From    string `json:"from,omitempty"`
	To      string `json:"to,omitempty"`
	Output  string `json:"output,omitempty"`
	Error   string `json:"error,omitempty"`
	Skipped string `json:"skipped,omitempty"`
	Changed bool   `json:"changed"`
}

// Update upgrades the named collections (or every installed collection when
// names is empty) to the newest published version.
func (m *Manager) Update(ctx context.Context, names []string) []UpdateResult {
	before, _ := m.Installed(ctx)

	if len(names) == 0 {
		for name := range before {
			names = append(names, name)
		}
		sort.Strings(names)
	}

	results := make([]UpdateResult, 0, len(names))
	upgraded := false
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" || name == "ansible.builtin" || strings.HasPrefix(name, "ansible._") {
			continue
		}
		if pin, pinned := m.PinnedVersion(name); pinned {
			results = append(results, UpdateResult{
				Name:    name,
				From:    before[name],
				To:      before[name],
				Skipped: "pinned to " + pin,
			})
			continue
		}
		result := UpdateResult{Name: name, From: before[name]}
		out, err := m.galaxy(ctx, "collection", "install", "--upgrade", name)
		result.Output = strings.TrimSpace(out)
		if err != nil {
			result.Error = err.Error()
			results = append(results, result)
			continue
		}
		upgraded = true
		results = append(results, result)
	}

	if upgraded {
		m.invalidate()
		after, _ := m.Installed(ctx)
		for i := range results {
			if results[i].Error != "" {
				continue
			}
			results[i].To = after[results[i].Name]
			results[i].Changed = results[i].From == "" || CompareVersions(results[i].To, results[i].From) > 0
		}
	}
	return results
}

// CompareVersions compares dotted version strings. Non-numeric segments are
// compared lexically; missing segments count as zero. It returns -1, 0 or 1.
func CompareVersions(a, b string) int {
	a = strings.TrimSpace(strings.TrimPrefix(a, "v"))
	b = strings.TrimSpace(strings.TrimPrefix(b, "v"))
	if a == b {
		return 0
	}
	as := strings.FieldsFunc(a, func(r rune) bool { return r == '.' || r == '-' || r == '+' })
	bs := strings.FieldsFunc(b, func(r rune) bool { return r == '.' || r == '-' || r == '+' })
	for i := 0; i < len(as) || i < len(bs); i++ {
		if i >= len(as) {
			// "1.0.0" outranks "1.0.0-rc1": a trailing numeric segment means a
			// newer patch release, a trailing label means a pre-release.
			if _, numeric := atoi(bs[i]); numeric {
				return -1
			}
			return 1
		}
		if i >= len(bs) {
			if _, numeric := atoi(as[i]); numeric {
				return 1
			}
			return -1
		}
		ap, bp := as[i], bs[i]
		if ap == bp {
			continue
		}
		ai, aok := atoi(ap)
		bi, bok := atoi(bp)
		switch {
		case aok && bok:
			if ai < bi {
				return -1
			}
			return 1
		case aok && !bok:
			// A numeric segment outranks a pre-release label ("1.0.0" > "1.0.0-rc1").
			return 1
		case !aok && bok:
			return -1
		default:
			if ap < bp {
				return -1
			}
			return 1
		}
	}
	return 0
}

func atoi(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, false
		}
		n = n*10 + int(r-'0')
	}
	return n, true
}

func contains(list []string, value string) bool {
	for _, entry := range list {
		if entry == value {
			return true
		}
	}
	return false
}
