// Package collections keeps the controller's Ansible Galaxy collections in
// sync with what the coordinator actually needs.
//
// Two triggers install a collection:
//   - the baseline catalog (everything advertised in Datacenter → Settings →
//     Ansible Engine → Galaxy Collections) is ensured on startup;
//   - a host feature that needs a specific module tree (a detected PostgreSQL
//     server, a Podman host, a Windows node, …) ensures that collection the
//     moment the probe reports it.
package collections

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Entry describes one collection in the baseline catalog.
type Entry struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// Well-known collection names used across the codebase.
const (
	CommunityGeneral   = "community.general"
	CommunityDocker    = "community.docker"
	CommunityPostgres  = "community.postgresql"
	CommunityMySQL     = "community.mysql"
	CommunityMongoDB   = "community.mongodb"
	CommunityProxmox   = "community.proxmox"
	CommunityLibvirt   = "community.libvirt"
	AnsiblePosix       = "ansible.posix"
	AnsibleWindows     = "ansible.windows"
	ContainersPodman   = "containers.podman"
	extraInstallTTL    = 10 * time.Minute
	galaxyInstallLimit = 8 * time.Minute
)

// Catalog is the baseline set installed on startup. It is also the list the
// datacenter settings page renders, so "everything in the tab" is exactly what
// the coordinator keeps installed.
var Catalog = []Entry{
	{Name: CommunityGeneral, Description: "Core community modules & become plugins (community.general.run0, doas, systemd, filesystems)"},
	{Name: AnsiblePosix, Description: "POSIX system modules (firewalld, mount, sysctl, synchronize, acl)"},
	{Name: AnsibleWindows, Description: "Windows host automation over WinRM (win_* modules, PowerShell remoting)"},
	{Name: CommunityDocker, Description: "Docker Engine & Docker Compose lifecycle modules"},
	{Name: ContainersPodman, Description: "Rootless and rootful Podman container & pod management"},
	{Name: CommunityPostgres, Description: "PostgreSQL databases, roles, privileges, and extensions"},
	{Name: CommunityMySQL, Description: "MariaDB / MySQL databases, users, and replication"},
	{Name: CommunityMongoDB, Description: "MongoDB users, roles, replicasets, and sharding"},
	{Name: CommunityProxmox, Description: "Proxmox VE KVM/QEMU and LXC container automation"},
}

// pluginCollections maps a detected capability plugin to the collections its
// Ansible modules live in.
var pluginCollections = map[string][]string{
	"container/docker": {CommunityDocker},
	"container/podman": {ContainersPodman},
	"vm/proxmox":       {CommunityProxmox},
	"vm/libvirt":       {CommunityLibvirt},
}

// engineCollections maps a database engine reported by a probe to its modules.
var engineCollections = map[string]string{
	"postgres":   CommunityPostgres,
	"postgresql": CommunityPostgres,
	"mariadb":    CommunityMySQL,
	"mysql":      CommunityMySQL,
	// No standalone community.mssql collection exists; the MSSQL modules ship
	// in community.general.
	"mssql":   CommunityGeneral,
	"mongodb": CommunityMongoDB,
	"mongo":   CommunityMongoDB,
	"redis":   CommunityGeneral,
	"valkey":  CommunityGeneral,
	"couchdb": CommunityGeneral,
}

// CollectionsForPlugin returns the collections a detected plugin implies.
func CollectionsForPlugin(pluginID string, details string) []string {
	out := []string{}
	seen := map[string]bool{}
	add := func(name string) {
		if name == "" || name == "ansible.builtin" || seen[name] {
			return
		}
		seen[name] = true
		out = append(out, name)
	}

	for _, name := range pluginCollections[pluginID] {
		add(name)
	}
	for _, engine := range parseEngines(details) {
		add(engineCollections[engine])
	}
	return out
}

// parseEngines reads "Engines: postgres, mariadb" style probe details.
func parseEngines(details string) []string {
	lower := strings.ToLower(details)
	idx := strings.Index(lower, "engines:")
	if idx < 0 {
		return nil
	}
	value := details[idx+len("engines:"):]
	if nl := strings.IndexByte(value, '\n'); nl >= 0 {
		value = value[:nl]
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		cleaned := strings.TrimSpace(strings.ToLower(part))
		if cleaned != "" {
			out = append(out, cleaned)
		}
	}
	return out
}

// Manager installs and tracks Galaxy collections. All installs are serialized:
// ansible-galaxy is not safe to run concurrently against one collection tree.
type Manager struct {
	repoRoot string

	mu       sync.Mutex
	known    map[string]bool
	knownAt  time.Time
	pending  map[string]bool
	lastErrs map[string]error

	// state is the durable operator state (opt-outs + version cache).
	store       StateStore
	state       persistedState
	stateLoaded bool

	// galaxyRun is the exec seam (tests inject a fake).
	galaxyRun func(ctx context.Context, args ...string) (string, error)
}

// defaultManager is the process-wide manager installed at startup.
var (
	defaultMu      sync.RWMutex
	defaultManager *Manager
)

// SetDefault publishes the process-wide manager.
func SetDefault(m *Manager) {
	defaultMu.Lock()
	defer defaultMu.Unlock()
	defaultManager = m
}

// Default returns the process-wide manager (nil before startup wiring).
func Default() *Manager {
	defaultMu.RLock()
	defer defaultMu.RUnlock()
	return defaultManager
}

// New builds a Manager rooted at the repository (used to locate the managed venv).
func New(repoRoot string) *Manager {
	return &Manager{
		repoRoot: repoRoot,
		known:    map[string]bool{},
		pending:  map[string]bool{},
		lastErrs: map[string]error{},
	}
}

// Catalog returns the baseline catalog (for the settings UI).
func (m *Manager) Catalog() []Entry {
	out := make([]Entry, len(Catalog))
	copy(out, Catalog)
	return out
}

// Installed lists the collections present on the controller, keyed by name.
func (m *Manager) Installed(ctx context.Context) (map[string]string, error) {
	out, err := m.galaxy(ctx, "collection", "list", "--format", "json")
	if err != nil {
		return nil, fmt.Errorf("ansible-galaxy collection list: %w", err)
	}

	var raw map[string]map[string]struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		return nil, fmt.Errorf("parse ansible-galaxy collection list: %w", err)
	}

	byName := map[string]string{}
	for _, cols := range raw {
		for name, meta := range cols {
			if name == "" || name == "ansible.builtin" || strings.HasPrefix(name, "ansible._") {
				continue
			}
			version := meta.Version
			if version == "" || version == "*" {
				version = "installed"
			}
			byName[name] = version
		}
	}
	return byName, nil
}

// EnsureCatalog installs every baseline collection that is missing.
func (m *Manager) EnsureCatalog(ctx context.Context) []string {
	names := make([]string, 0, len(Catalog))
	for _, entry := range Catalog {
		names = append(names, entry.Name)
	}
	return m.Ensure(ctx, names, "baseline catalog")
}

// Ensure installs the given collections when they are not present yet. It is
// synchronous (callers run it off the request path) and reports the names it
// installed. Failures are logged and returned separately.
func (m *Manager) Ensure(ctx context.Context, names []string, reason string) []string {
	installed := m.refresh(ctx)
	var versions map[string]string
	installedNow := []string{}
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" || name == "ansible.builtin" || m.IsDisabled(name) {
			continue
		}
		// A pinned collection is held at its exact version, even if a broader
		// install already put a different one on disk.
		if pin, pinned := m.PinnedVersion(name); pinned {
			if versions == nil {
				versions, _ = m.Installed(ctx)
			}
			if versions[name] == pin {
				continue
			}
			if m.installSpec(ctx, name+":"+pin, reason+" (pinned "+pin+")", true) {
				installedNow = append(installedNow, name)
			}
			continue
		}
		if installed[name] {
			continue
		}
		if m.install(ctx, name, reason) {
			installedNow = append(installedNow, name)
		}
	}
	if len(installedNow) > 0 {
		m.invalidate()
	}
	return installedNow
}

// refresh returns the cached installed set, refreshing it when stale.
func (m *Manager) refresh(ctx context.Context) map[string]bool {
	m.mu.Lock()
	if m.knownAt.After(time.Time{}) && time.Since(m.knownAt) < extraInstallTTL {
		known := m.known
		m.mu.Unlock()
		return known
	}
	m.mu.Unlock()

	list, err := m.Installed(ctx)
	if err != nil {
		log.Printf("ansible collections: could not list installed collections: %v", err)
		m.mu.Lock()
		known := m.known
		m.mu.Unlock()
		return known
	}

	m.mu.Lock()
	m.known = make(map[string]bool, len(list))
	for name := range list {
		m.known[name] = true
	}
	m.knownAt = time.Now()
	known := m.known
	m.mu.Unlock()
	return known
}

func (m *Manager) invalidate() {
	m.mu.Lock()
	m.knownAt = time.Time{}
	m.mu.Unlock()
}

// install runs ansible-galaxy for one collection, serialized across the process.
func (m *Manager) install(ctx context.Context, name, reason string) bool {
	return m.installSpec(ctx, name, reason, false)
}

// installSpec installs an exact requirement ("ns.name" or "ns.name:1.2.3").
func (m *Manager) installSpec(ctx context.Context, spec, reason string, force bool) bool {
	name := strings.SplitN(spec, ":", 2)[0]
	m.mu.Lock()
	if m.pending[name] {
		m.mu.Unlock()
		return false
	}
	m.pending[name] = true
	m.mu.Unlock()

	defer func() {
		m.mu.Lock()
		delete(m.pending, name)
		m.mu.Unlock()
	}()

	installCtx, cancel := context.WithTimeout(ctx, galaxyInstallLimit)
	defer cancel()

	args := []string{"collection", "install", spec}
	if force {
		args = append(args, "--force")
	}
	out, err := m.galaxy(installCtx, args...)
	if err != nil {
		if out == "" {
			out = err.Error()
		}
	}

	m.mu.Lock()
	if err != nil {
		m.lastErrs[name] = err
	} else {
		delete(m.lastErrs, name)
	}
	m.mu.Unlock()

	if err != nil {
		log.Printf("ansible collections: install %s failed (trigger: %s): %v: %s", name, reason, err, strings.TrimSpace(out))
		return false
	}
	log.Printf("ansible collections: installed %s (trigger: %s)", spec, reason)
	return true
}

// galaxy runs ansible-galaxy with the manager's binary, honouring the test seam.
func (m *Manager) galaxy(ctx context.Context, args ...string) (string, error) {
	if m.galaxyRun != nil {
		return m.galaxyRun(ctx, args...)
	}
	bin, err := galaxyBin(m.repoRoot)
	if err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = m.repoRoot
	cmd.Env = append(os.Environ(), "ANSIBLE_FORCE_COLOR=0")
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// SetGalaxyRunner overrides command execution (tests only).
func (m *Manager) SetGalaxyRunner(run func(ctx context.Context, args ...string) (string, error)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.galaxyRun = run
}

// galaxyBin prefers the managed venv binary so installs land in the same
// interpreter the runner uses.
func galaxyBin(repoRoot string) (string, error) {
	candidates := []string{}
	if repoRoot != "" {
		candidates = append(candidates, filepath.Join(repoRoot, "data", "ansible-venv", "bin", "ansible-galaxy"))
	}
	if env := strings.TrimSpace(os.Getenv("ANSIBLE_VENV_PATH")); env != "" {
		candidates = append(candidates, filepath.Join(env, "bin", "ansible-galaxy"))
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	if path, err := exec.LookPath("ansible-galaxy"); err == nil {
		return path, nil
	}
	return "", errors.New("ansible-galaxy not found (managed venv missing)")
}

// SortedNames returns the catalog names in a stable order (for logging/tests).
func SortedNames() []string {
	names := make([]string, 0, len(Catalog))
	for _, entry := range Catalog {
		names = append(names, entry.Name)
	}
	sort.Strings(names)
	return names
}
