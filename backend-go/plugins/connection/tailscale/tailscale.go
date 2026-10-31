package tailscale

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"tailscale.com/ipn"
	"tailscale.com/net/netns"
	"tailscale.com/tailcfg"
	"tailscale.com/tsnet"

	"air-traffic-control/backend/pkg/plugin"
	"air-traffic-control/backend/pkg/plugin/newapi"
	"air-traffic-control/backend/pkg/plugin/settingsframework"
	"air-traffic-control/backend/pkg/secrets"
	"air-traffic-control/backend/pkg/tailscale"
)

var (
	_ plugin.Plugin                      = (*Plugin)(nil)
	_ plugin.Dialer                      = (*Plugin)(nil)
	_ plugin.Reader                      = (*Plugin)(nil)
	_ plugin.Actor                       = (*Plugin)(nil)
	_ plugin.SettingsPageProvider        = (*Plugin)(nil)
	_ newapi.DataCollectionPlugin        = (*Plugin)(nil)
	_ newapi.ManagementPlugin            = (*Plugin)(nil)
	_ newapi.CategorizedSettingsProvider = (*Plugin)(nil)
	_ newapi.ContextMenuProvider         = (*Plugin)(nil)
	_ newapi.TaskCompileProvider         = (*Plugin)(nil)
)

func resolveAgentSocketPath() string {
	if sock := os.Getenv("SSH_AUTH_SOCK"); sock != "" {
		if _, err := os.Stat(sock); err == nil {
			return sock
		}
	}
	uid := os.Getuid()
	candidates := []string{
		fmt.Sprintf("/run/user/%d/ssh-agent.socket", uid),
		fmt.Sprintf("/run/user/%d/keyring/ssh", uid),
		fmt.Sprintf("/run/user/%d/gnupg/S.gpg-agent.ssh", uid),
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}

func getSSHAgentAuthMethod() (ssh.AuthMethod, io.Closer) {
	sock := resolveAgentSocketPath()
	if sock == "" {
		return nil, nil
	}
	conn, err := net.Dial("unix", sock)
	if err != nil {
		return nil, nil
	}
	agentClient := agent.NewClient(conn)
	return ssh.PublicKeysCallback(agentClient.Signers), conn
}

func init() {
	_ = plugin.Register(New())
}

// SettingsPageID is the route slug of ATC's own Tailscale settings page. It is
// also the key under which the framework persists the page's values.
const SettingsPageID = "connection/tailscale"

// Defaults for ATC's own tsnet node when nothing has been configured.
const (
	DefaultStateDir = "/tmp/atc-tsnet"
	DefaultHostname = "air-traffic-control-tsnet"
)

// Settings field names, shared between the page schema and the config resolver.
const (
	FieldAuthKey = "tailscale_auth_key"
	// FieldAuthKeyLegacy is the pre-rename persisted field name. It is only read
	// for backward compatibility so existing installs keep their auth key after
	// the settings page field was renamed to tailscale_auth_key.
	FieldAuthKeyLegacy = "authKey"
	// FieldFallbackHostSocket opts in to the local-development host tailscaled
	// socket fallback. Production deployments leave this false so ATC joins the
	// mesh directly with its own auth key instead of silently borrowing the
	// host's Tailscale socket.
	FieldFallbackHostSocket = "tailscale_fallback_host_socket"
	FieldHostname           = "hostname"
	FieldStateDir           = "stateDir"
	FieldControlURL         = "controlURL"
	FieldEphemeral          = "ephemeral"
	// FieldProxyMode controls the userspace SOCKS5/HTTP proxy listeners:
	// auto | socks | http | both | off. "auto" enables both when the daemon
	// cannot set SO_MARK (unprivileged userspace deployment).
	FieldProxyMode        = "proxy_mode"
	FieldProxySocksListen = "proxy_socks_listen"
	FieldProxyHTTPListen  = "proxy_http_listen"
	FieldProxyUsername    = "proxy_username"
	FieldProxyPassword    = "proxy_password"
)

// CategorizedSettingsPage implements newapi.CategorizedSettingsProvider so ATC's
// own Tailscale node settings render in the categorized Datacenter Settings
// sidebar using settingsframework.NewPageBuilder.
func (p *Plugin) CategorizedSettingsPage() newapi.CategorizedSettingsPage {
	return settingsframework.NewPageBuilder(SettingsPageID, "ATC Tailscale").
		WithIcon("hub").
		WithCategory(string(p.Meta().Kind)).
		WithScope(newapi.SettingsScopeDatacenter).
		WithOrder(15).
		WithDescription("ATC's own Tailscale node (embedded tsnet) — the identity ATC uses to reach the mesh. "+
			"The auth key is write-only and masked in every API response.").
		SecretField(
			FieldAuthKey,
			"Tailscale Auth Key",
			settingsframework.WithPlaceholder("tskey-auth-..."),
			settingsframework.WithDescription("Auth key used to register ATC's own node and join the mesh directly. "+
				"Saving a new key re-registers ATC: the previous tsnet state is discarded so the new key is the one that takes effect."),
		).
		BoolField(
			FieldFallbackHostSocket,
			"Allow Host Socket Fallback",
			false,
			settingsframework.WithDescription("Local development only. When enabled, ATC borrows the host's system tailscaled "+
				"socket/network stack instead of running its own tsnet node; the stored auth key is kept for when you disable it. "+
				"Leave disabled in production so ATC joins the mesh with its own auth key."),
		).
		StringField(
			FieldHostname,
			"Node Hostname",
			DefaultHostname,
			settingsframework.WithPlaceholder(DefaultHostname),
			settingsframework.WithDescription("Hostname ATC advertises to the tailnet."),
		).
		StringField(
			FieldStateDir,
			"tsnet State Directory",
			DefaultStateDir,
			settingsframework.WithPlaceholder(DefaultStateDir),
			settingsframework.WithDescription("Directory holding ATC's tsnet node state (file store + logs)."),
		).
		StringField(
			FieldControlURL,
			"Control Server URL",
			"",
			settingsframework.WithPlaceholder("https://controlplane.tailscale.com"),
			settingsframework.WithDescription("Leave blank for the Tailscale-hosted control plane; set it for Headscale or a self-hosted control server."),
		).
		BoolField(
			FieldEphemeral,
			"Ephemeral Node",
			false,
			settingsframework.WithDescription("Requested at registration only; the auth key's own ephemerality decides. Inert once the node is registered."),
		).
		StringField(
			FieldProxyMode,
			"Userspace Proxy Mode",
			"auto",
			settingsframework.WithPlaceholder("auto"),
			settingsframework.WithDescription("auto | socks | http | both | off. ATC always runs Tailscale in userspace (no /dev/net/tun). "+
				"auto enables both proxies when the daemon lacks CAP_NET_ADMIN; loopback on a host, 0.0.0.0 inside a container."),
		).
		StringField(
			FieldProxySocksListen,
			"SOCKS5 Listen Address",
			"",
			settingsframework.WithPlaceholder("127.0.0.1:1055"),
			settingsframework.WithDescription("Explicit SOCKS5 bind address. Blank follows the host/container default (127.0.0.1:1055 or 0.0.0.0:1055)."),
		).
		StringField(
			FieldProxyHTTPListen,
			"HTTP Proxy Listen Address",
			"",
			settingsframework.WithPlaceholder("127.0.0.1:1056"),
			settingsframework.WithDescription("Explicit HTTP CONNECT proxy bind address. Blank follows the host/container default (127.0.0.1:1056 or 0.0.0.0:1056)."),
		).
		StringField(
			FieldProxyUsername,
			"Proxy Username",
			"",
			settingsframework.WithDescription("Optional. When the proxy listens beyond loopback and no credentials are configured, ATC generates and persists a pair."),
		).
		SecretField(
			FieldProxyPassword,
			"Proxy Password",
			settingsframework.WithDescription("Optional SOCKS5/HTTP proxy password. Write-only and masked in every API response."),
		).
		BuildCategorized()
}

// SettingsPage implements plugin.SettingsPageProvider so ATC's own Tailscale
// node settings render in the Datacenter Settings sidebar with no bespoke
// frontend code. The auth key is a write-only secret: the framework masks it in
// every response and only ever persists genuinely new material.
func (p *Plugin) SettingsPage() plugin.SettingsPage {
	return p.CategorizedSettingsPage().SettingsPage
}

// tsnetSettings is the effective configuration for ATC's own tsnet node,
// resolved from explicit plugin options, then persisted settings, then
// environment variables.
type tsnetSettings struct {
	AuthKey            string
	Hostname           string
	StateDir           string
	ControlURL         string
	Ephemeral          bool
	FallbackHostSocket bool
	ProxyMode          string
	ProxySocksListen   string
	ProxyHTTPListen    string
	ProxyUsername      string
	ProxyPassword      string
}

// fingerprint identifies the configuration a cached tsnet.Server was built
// from, so a settings change actually rebuilds it instead of being inert.
func (c tsnetSettings) fingerprint() string {
	return strings.Join([]string{
		c.Hostname,
		c.StateDir,
		c.ControlURL,
		c.AuthKey,
		strconv.FormatBool(c.Ephemeral),
		strconv.FormatBool(c.FallbackHostSocket),
	}, "\x00")
}

// persistedSetting reads a string setting written through the settings page
// via the v2 settingsframework StoreAdapter.
func persistedSetting(field string) string {
	return settingsframework.NewStoreAdapter(nil).StringOrDefault(SettingsPageID, field, "")
}

// effectiveSettings resolves the configuration for ATC's own tsnet node.
// Precedence per field: explicit plugin option, then the persisted settings
// page value, then environment variables (TS_AUTHKEY, TS_CONTROL_URL).
func (p *Plugin) effectiveSettings() tsnetSettings {
	adapter := settingsframework.NewStoreAdapter(nil)
	cfg := tsnetSettings{
		AuthKey:            strings.TrimSpace(p.authKey),
		Hostname:           adapter.StringOrDefault(SettingsPageID, FieldHostname, p.hostname),
		ControlURL:         adapter.StringOrDefault(SettingsPageID, FieldControlURL, ""),
		Ephemeral:          adapter.BoolOrDefault(SettingsPageID, FieldEphemeral, p.ephemeral),
		FallbackHostSocket: adapter.BoolOrDefault(SettingsPageID, FieldFallbackHostSocket, false),
		ProxyMode:          adapter.StringOrDefault(SettingsPageID, FieldProxyMode, "auto"),
		ProxySocksListen:   adapter.StringOrDefault(SettingsPageID, FieldProxySocksListen, ""),
		ProxyHTTPListen:    adapter.StringOrDefault(SettingsPageID, FieldProxyHTTPListen, ""),
		ProxyUsername:      strings.TrimSpace(adapter.StringOrDefault(SettingsPageID, FieldProxyUsername, "")),
	}
	if secret, ok := adapter.SecretValue(SettingsPageID, FieldProxyPassword); ok {
		cfg.ProxyPassword = secret
	}
	// State dir: persisted value wins; otherwise derive from the data store
	// location so new installs keep their node state outside /tmp.
	if sd := adapter.StringOrDefault(SettingsPageID, FieldStateDir, ""); sd != "" {
		// Normalize in memory only; migrateLegacyStateDir persists the absolute
		// form from the startup path. Persisting here would write to the shared
		// settings store on every read (including from tests probing settings).
		cfg.StateDir = absoluteStateDir(sd)
	} else if p.stateDir != "" && p.stateDir != DefaultStateDir {
		// Explicit plugin option (e.g. WithStateDir in tests) wins.
		cfg.StateDir = p.stateDir
	} else {
		dbPath := ""
		if st := plugin.GlobalSettingsStore(); st != nil {
			dbPath = st.DBPath()
		}
		if dbPath == "" {
			dbPath = plugin.ResolveDBPath("")
		}
		if dbPath != "" {
			if abs, err := filepath.Abs(dbPath); err == nil {
				dbPath = abs
			}
			cfg.StateDir = filepath.Join(filepath.Dir(dbPath), "tsnet")
		} else {
			cfg.StateDir = p.stateDir
		}
	}

	if cfg.AuthKey == "" {
		if secret, ok := adapter.SecretValue(SettingsPageID, FieldAuthKey); ok {
			cfg.AuthKey = secret
		}
	}
	// Backward compatibility: rows written before the field rename still carry
	// the secret under "authKey". Read it only when the new field is empty.
	if cfg.AuthKey == "" {
		if secret, ok := adapter.SecretValue(SettingsPageID, FieldAuthKeyLegacy); ok {
			cfg.AuthKey = secret
		}
	}
	if cfg.AuthKey == "" {
		cfg.AuthKey = strings.TrimSpace(os.Getenv("TS_AUTHKEY"))
	}
	if cfg.ControlURL == "" {
		cfg.ControlURL = strings.TrimSpace(os.Getenv("TS_CONTROL_URL"))
	}
	return cfg
}

// absoluteStateDir resolves a configured state directory to an absolute path.
// It returns dir unchanged when it is already absolute or cannot be resolved.
func absoluteStateDir(dir string) string {
	dir = strings.TrimSpace(dir)
	if dir == "" || filepath.IsAbs(dir) {
		return dir
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		log.Printf("tsnet: cannot resolve relative state dir %q: %v", dir, err)
		return dir
	}
	return filepath.Clean(abs)
}

// resetTsnetState removes the persisted tsnet node state so a new auth key
// registers the node afresh instead of being ignored because the node already
// exists in the state store.
func resetTsnetState(stateDir string) error {
	if strings.TrimSpace(stateDir) == "" {
		return nil
	}
	statePath := filepath.Join(stateDir, "tailscaled.state")
	if err := os.Remove(statePath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("reset tsnet state %s: %w", statePath, err)
	}
	return nil
}

// tsnetUpTimeout bounds how long ATC waits for its own node to reach Running.
// tsnet.Up blocks while the backend sits in NeedsLogin (no auth key, or the
// control plane is unreachable), so an unbounded call used to burn the caller's
// whole dial timeout on every host and hang /api/tailscale/self.
const tsnetUpTimeout = 12 * time.Second

// tsnetFailureCooldown short-circuits repeated dials while the node is known to
// be unregistered: a cache warmup over N hosts must not spend N × tsnetUpTimeout
// re-discovering the same failure.
const tsnetFailureCooldown = 15 * time.Second

// hasRegisteredNodeState reports whether stateDir holds an actual tsnet node
// registration rather than only a freshly generated machine key. tsnet writes
// `_machinekey` as soon as it starts, before any login: treating that file as
// "configured" skipped the missing-auth-key guard and left every dial waiting on
// an interactive login that can never complete in a headless daemon.
func hasRegisteredNodeState(stateDir string) bool {
	if strings.TrimSpace(stateDir) == "" {
		return false
	}
	raw, err := os.ReadFile(filepath.Join(stateDir, "tailscaled.state"))
	if err != nil {
		return false
	}
	var stored map[string]json.RawMessage
	if err := json.Unmarshal(raw, &stored); err != nil {
		// Unknown/legacy state format: trust it rather than forcing an already
		// registered node through a needless re-registration.
		return true
	}
	return len(stored[string(ipn.CurrentProfileStateKey)]) > 0
}

// tsnetStartError renders an actionable error for a node that cannot reach
// Running, including the live backend state, Tailscale health warnings, and the
// authorization URL when control is asking for an interactive login.
func tsnetStartError(srv *tsnet.Server, err error) error {
	details := []string{fmt.Sprintf("backend error: %v", err)}
	backendState := ""
	if lc, lerr := srv.LocalClient(); lerr == nil && lc != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if st, serr := lc.Status(ctx); serr == nil && st != nil {
			if st.BackendState != "" {
				backendState = st.BackendState
				details = append(details, "backend state "+st.BackendState)
			}
			for _, h := range st.Health {
				if strings.TrimSpace(h) != "" {
					details = append(details, "health: "+h)
				}
			}
			if st.AuthURL != "" {
				details = append(details, "authorize this node in Tailscale: "+st.AuthURL)
			}
		}
	}
	if hint := unprivilegedControlHint(backendState, err); hint != "" {
		details = append(details, hint)
	}
	return fmt.Errorf("tsnet node is not connected: %s", strings.Join(details, "; "))
}

// socketMarkAvailable is a test seam for netns.UseSocketMark, which probes
// whether the process may set SO_MARK (CAP_NET_ADMIN). When it cannot, tsnet
// binds its sockets to the default interface instead, which breaks control-plane
// reachability on hosts that rely on policy routing.
var socketMarkAvailable = netns.UseSocketMark

// closeTsnetServer is a test seam so a transport switch can be asserted without
// starting a real tailscaled.
var closeTsnetServer = func(srv *tsnet.Server) error { return srv.Close() }

// unprivilegedControlHint explains the most common cause of a control-plane
// timeout on an unprivileged daemon. Tailscale marks its outbound sockets with
// SO_MARK so they bypass its own routes; that needs CAP_NET_ADMIN. Without it,
// tsnet falls back to SO_BINDTODEVICE, which also bypasses *host* policy
// routing (Cloudflare WARP, VPNs), so the control plane can end up pinned to an
// interface that cannot reach it — every dial then sits in SYN-SENT until the
// deadline. Only reported when the backend is stuck at NeedsLogin on a network
// error, so an invalid auth key is not blamed on socket capabilities.
func unprivilegedControlHint(backendState string, err error) string {
	if backendState != "NeedsLogin" || socketMarkAvailable() {
		return ""
	}
	msg := err.Error()
	if !strings.Contains(msg, "deadline exceeded") &&
		!strings.Contains(msg, "network is unreachable") &&
		!strings.Contains(msg, "i/o timeout") {
		return ""
	}
	return "hint: this daemon lacks CAP_NET_ADMIN, so tsnet binds its sockets to the " +
		"default interface and cannot follow policy routing (e.g. Cloudflare WARP or a VPN) " +
		"to the Tailscale control plane; run the daemon with cap_net_admin, or enable the " +
		"host tailscaled fallback in Datacenter Settings"
}

// Plugin provides connectivity over the Tailscale mesh using embedded tsnet or custom dialer.
type Plugin struct {
	mu          sync.Mutex
	tsServer    *tsnet.Server
	tsDialer    *tsnetDialer
	dialer      plugin.Dialer
	clients     map[string]*ssh.Client
	stateDir    string
	hostname    string
	authKey     string
	ephemeral   bool
	tsnetFP     string
	tsnetAuth   string
	initialized bool

	// Userspace proxy listeners (SOCKS5 + HTTP CONNECT), bound to the active
	// dialer. Guarded by mu.
	proxyMode      string
	proxySocksLn   net.Listener
	proxyHTTPLn    net.Listener
	proxyHTTPsrv   *http.Server
	proxySocksAddr string
	proxyHTTPAddr  string
	proxyAuth      bool
	proxyCredsPath string
	proxyErr       string
	proxyFP        string

	// upSince records when the backend last became Running, so `lastUpAt`
	// reports a real "up since" instead of the time of the last status read.
	upMu    sync.Mutex
	upSince time.Time
}

// Option configures the Tailscale plugin.
type Option func(*Plugin)

// WithStateDir sets the directory for tsnet state.
func WithStateDir(dir string) Option {
	return func(p *Plugin) {
		p.stateDir = dir
	}
}

// WithHostname sets the tsnet advertised hostname.
func WithHostname(hostname string) Option {
	return func(p *Plugin) {
		p.hostname = hostname
	}
}

// WithAuthKey sets the Tailscale auth key.
func WithAuthKey(key string) Option {
	return func(p *Plugin) {
		p.authKey = key
	}
}

// New creates a new Tailscale connection plugin.
func New(opts ...Option) *Plugin {
	p := &Plugin{
		clients:   make(map[string]*ssh.Client),
		stateDir:  DefaultStateDir,
		hostname:  DefaultHostname,
		ephemeral: false,
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// SetDialer allows injecting a custom dialer (useful for unit testing without full tsnet handshake).
func (p *Plugin) SetDialer(d plugin.Dialer) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.dialer = d
}

// Meta returns plugin metadata.
func (p *Plugin) Meta() plugin.Meta {
	return plugin.Meta{
		ID:          "connection/tailscale",
		Kind:        plugin.KindConnection,
		Name:        "tailscale",
		Label:       "Tailscale Mesh (tsnet)",
		Icon:        "shield",
		Description: "Peer-to-peer Tailscale mesh transport using embedded tsnet and keyless Tailscale SSH",
		Order:       5,
	}
}

// Compatible returns true if the host is a Tailscale IP (100.64.0.0/10) or domain (.ts.net).
func (p *Plugin) Compatible(h plugin.HostFacts) bool {
	if h.ActiveConnection == "connection/tailscale" {
		return true
	}
	if h.ActiveConnection != "" && h.ActiveConnection != "connection/tailscale" {
		return false
	}
	route := strings.TrimSpace(h.HostRoute)
	if strings.HasSuffix(route, ".ts.net") {
		return true
	}
	// Check if IP starts with 100.
	ip := net.ParseIP(route)
	if ip != nil {
		ip4 := ip.To4()
		if ip4 != nil && ip4[0] == 100 {
			// CGNAT / Tailscale range 100.64.0.0/10: 100.64.0.0 - 100.127.255.255
			if ip4[1] >= 64 && ip4[1] <= 127 {
				return true
			}
		}
	}
	return false
}

// Requirements specifies required packages.
func (p *Plugin) Requirements() plugin.Requirements {
	return plugin.Requirements{
		HostPackages: map[string][]string{
			"dnf": {"tailscale"},
			"apt": {"tailscale"},
			"apk": {"tailscale"},
		},
		ControllerCollections: []string{
			"ansible.builtin",
		},
	}
}

// CollectionSchedule returns the standard connection polling and cache TTL schedule (60s).
func (p *Plugin) CollectionSchedule() newapi.CollectionSchedule {
	return newapi.DefaultScheduleForKind(p.Meta().Kind)
}

// CollectionSchema documents the v2 payload structure emitted by Read.
func (p *Plugin) CollectionSchema() newapi.CollectionSchema {
	return newapi.CollectionSchema{
		Version:     "v2",
		RootKey:     "connection",
		Description: p.Meta().Description,
	}
}

// Read inspects live Tailscale SSH transport state for the target node without
// ever exposing the controller's tsnet auth key.
func (p *Plugin) Read(ctx context.Context, r plugin.Runner, h plugin.HostFacts) (any, error) {
	if r == nil {
		var err error
		r, err = p.NewRunner(h)
		if err != nil {
			return nil, err
		}
	}

	out, err := r.RunCommand(ctx, "uname -s -r")
	if err != nil {
		return nil, err
	}

	cfg := p.effectiveSettings()
	port := 22
	if pStr, ok := h.Overrides["port"]; ok && pStr != "" {
		if pVal, perr := strconv.Atoi(pStr); perr == nil && pVal > 0 {
			port = pVal
		}
	}
	user := "root"
	if u, ok := h.Overrides["user"]; ok && u != "" {
		user = u
	}

	return map[string]any{
		"connection": map[string]any{
			"plugin":      p.Meta().ID,
			"hostRoute":   h.HostRoute,
			"port":        port,
			"user":        user,
			"hostname":    cfg.Hostname,
			"stateDir":    cfg.StateDir,
			"controlURL":  cfg.ControlURL,
			"ephemeral":   cfg.Ephemeral,
			"probeOutput": strings.TrimSpace(out),
		},
	}, nil
}

// ContextMenuEntries provides node context menu actions for Tailscale-connected hosts.
func (p *Plugin) ContextMenuEntries(ctx context.Context, h plugin.HostFacts) []newapi.ContextMenuEntry {
	if !p.Compatible(h) {
		return nil
	}
	meta := p.Meta()
	return newapi.NewContextMenuBuilder(meta.ID).
		WithTarget(newapi.TargetNode).
		WithGroup(string(meta.Kind)).
		AddAction("verify_connection", "Verify Tailscale Connection", "network_check", "verify_connection", 10).
		AddAction("enable_tailscale_ssh", "Enable Tailscale SSH", "vpn_key", "enable_tailscale_ssh", 20).
		AddAction("restart_tailscaled", "Restart Tailscale Daemon", "restart_alt", "restart_tailscaled", 30).
		Build()
}

// ManagementActions returns rich v2 action specifications for Tailscale transport management.
func (p *Plugin) ManagementActions() []newapi.ManagementActionSpec {
	return []newapi.ManagementActionSpec{
		{
			ActionSpec: plugin.ActionSpec{
				Name:        "verify_connection",
				Label:       "Verify Tailscale Connection",
				Description: "Verify Ansible connectivity over the Tailscale mesh transport",
				Options:     []plugin.OptionSchema{},
			},
			Category:         newapi.ActionCategoryDiagnostic,
			Idempotent:       true,
			InvalidatesKinds: []plugin.Kind{plugin.KindConnection},
		},
		{
			ActionSpec: plugin.ActionSpec{
				Name:        "enable_tailscale_ssh",
				Label:       "Enable Tailscale SSH",
				Description: "Enable or disable keyless Tailscale SSH on the remote node (tailscale set --ssh)",
				Options: []plugin.OptionSchema{
					{
						Name:        "enabled",
						Type:        "bool",
						Required:    false,
						Default:     true,
						Description: "Enable or disable the Tailscale SSH server on the node",
					},
				},
			},
			Category:         newapi.ActionCategorySecurity,
			Idempotent:       true,
			InvalidatesKinds: []plugin.Kind{plugin.KindConnection, plugin.KindNetwork},
		},
		{
			ActionSpec: plugin.ActionSpec{
				Name:        "restart_tailscaled",
				Label:       "Restart Tailscale Daemon",
				Description: "Restart the tailscaled systemd service on the remote node",
				Options: []plugin.OptionSchema{
					{
						Name:        "service",
						Type:        "string",
						Required:    false,
						Default:     "tailscaled.service",
						Description: "Systemd unit name for the Tailscale daemon",
					},
				},
			},
			Category:         newapi.ActionCategoryLifecycle,
			Idempotent:       true,
			InvalidatesKinds: []plugin.Kind{plugin.KindConnection, plugin.KindNetwork},
		},
	}
}

// Actions derives legacy plugin.ActionSpec items from ManagementActions().
func (p *Plugin) Actions() []plugin.ActionSpec {
	mgmt := p.ManagementActions()
	specs := make([]plugin.ActionSpec, len(mgmt))
	for i, m := range mgmt {
		specs[i] = m.ActionSpec
	}
	return specs
}

// ValidateAction validates options against the declared ManagementActionSpec.
func (p *Plugin) ValidateAction(h plugin.HostFacts, action string, opts map[string]any) error {
	norm := strings.TrimSpace(action)
	for _, spec := range p.ManagementActions() {
		if strings.EqualFold(spec.Name, norm) {
			if err := newapi.ValidateManagementOptions(spec, opts); err != nil {
				return err
			}
			if strings.EqualFold(spec.Name, "enable_tailscale_ssh") && opts != nil {
				if raw, exists := opts["enabled"]; exists && raw != nil {
					if _, ok := raw.(bool); !ok {
						return fmt.Errorf("option \"enabled\" must be a boolean for action %s", spec.Name)
					}
				}
			}
			if strings.EqualFold(spec.Name, "restart_tailscaled") && opts != nil {
				if rawSvc, ok := opts["service"].(string); ok && strings.TrimSpace(rawSvc) != "" {
					if err := validateServiceName(strings.TrimSpace(rawSvc)); err != nil {
						return err
					}
				}
			}
			return nil
		}
	}
	return fmt.Errorf("unsupported tailscale connection action: %s", action)
}

// CompileTask adapts a typed v2 TaskRequest to the plugin's declarative Ansible TaskSpec compiler.
func (p *Plugin) CompileTask(ctx context.Context, h plugin.HostFacts, req newapi.TaskRequest) (plugin.TaskSpec, error) {
	action := strings.TrimSpace(req.Action)
	if action == "" && req.Params != nil {
		if a, ok := req.Params["action"].(string); ok {
			action = strings.TrimSpace(a)
		}
	}
	if action == "" {
		return plugin.TaskSpec{}, fmt.Errorf("action is required for plugin %s", p.Meta().ID)
	}
	return p.Compile(h, action, req.Params)
}

// Compile turns a Tailscale connection management action into a declarative Ansible TaskSpec.
func (p *Plugin) Compile(h plugin.HostFacts, action string, opts map[string]any) (plugin.TaskSpec, error) {
	if err := p.ValidateAction(h, action, opts); err != nil {
		return plugin.TaskSpec{}, err
	}

	norm := strings.ToLower(strings.TrimSpace(action))
	switch norm {
	case "verify_connection":
		return plugin.TaskSpec{
			Name: "Verify Tailscale connection",
			Steps: []plugin.TaskStep{
				{
					Name:   "Verify Tailscale connection via ansible.builtin.ping",
					Module: "ansible.builtin.ping",
					Params: map[string]any{},
				},
			},
		}, nil
	case "enable_tailscale_ssh":
		enabled := true
		if opts != nil {
			if b, ok := opts["enabled"].(bool); ok {
				enabled = b
			}
		}
		cmd := fmt.Sprintf("tailscale set --ssh=%t", enabled)
		return plugin.TaskSpec{
			Name: cmd,
			Steps: []plugin.TaskStep{
				{
					Name:   cmd,
					Module: "ansible.builtin.command",
					Params: map[string]any{
						"cmd":    cmd,
						"become": true,
					},
				},
			},
		}, nil
	case "restart_tailscaled":
		svc := "tailscaled.service"
		if opts != nil {
			if raw, ok := opts["service"].(string); ok && strings.TrimSpace(raw) != "" {
				svc = strings.TrimSpace(raw)
			}
		}
		label := fmt.Sprintf("Restart %s", svc)
		return plugin.TaskSpec{
			Name: label,
			Steps: []plugin.TaskStep{
				{
					Name:   label,
					Module: "ansible.builtin.systemd_service",
					Params: map[string]any{
						"name":   svc,
						"state":  "restarted",
						"become": true,
					},
				},
			},
		}, nil
	default:
		return plugin.TaskSpec{}, fmt.Errorf("unsupported tailscale connection action: %s", action)
	}
}

func validateServiceName(name string) error {
	if strings.HasPrefix(name, "-") {
		return fmt.Errorf("service name must not start with '-'")
	}
	if strings.ContainsAny(name, " \t\r\n;|&$`\"'\\/") {
		return fmt.Errorf("service name contains disallowed characters")
	}
	return nil
}

func isSystemTailscaledActive() bool {
	candidates := []string{
		"/run/tailscale/tailscaled.sock",
		"/var/run/tailscale/tailscaled.sock",
	}
	for _, c := range candidates {
		if fi, err := os.Stat(c); err == nil && (fi.Mode()&os.ModeSocket != 0) {
			return true
		}
	}
	return false
}

// tailscaledActive is a test seam for the host-socket availability probe.
var tailscaledActive = isSystemTailscaledActive

// shouldUseHostSocket reports whether the operator opted into borrowing the
// host's tailscaled. An explicit opt-in wins over a configured auth key so the
// two modes can be switched without deleting the stored key.
func shouldUseHostSocket(cfg tsnetSettings) bool {
	return cfg.FallbackHostSocket && tailscaledActive()
}

func (p *Plugin) ensureTsnet() (plugin.Dialer, error) {
	// Resolve settings before taking the lock: the settings store has its own
	// locking and must never be held while dialing.
	cfg := p.effectiveSettings()

	// One-time migration of node state out of /tmp so a reboot no longer
	// wipes the registration. Existing /tmp state is copied, never moved, so a
	// failure leaves the old state intact.
	p.migrateLegacyStateDir(&cfg)

	p.mu.Lock()
	defer p.mu.Unlock()

	if p.dialer != nil {
		return p.dialer, nil
	}

	// Host-socket fallback is opt-in and intended for local development only.
	// When enabled it wins over the auth key: the operator explicitly asked ATC
	// to borrow the host's tailscaled, and the key is kept for when they switch
	// back to ATC's own tsnet node. Requiring an empty key made the two modes
	// mutually exclusive in the UI (switching to the host socket meant
	// destroying the stored key first).
	if shouldUseHostSocket(cfg) {
		// Switching to the host socket must stop ATC's own node. Leaving it
		// running kept a logged-out tsnet backend retrying the old/invalid auth
		// key in the background forever, flooding the daemon log and hammering
		// the control plane long after the operator switched modes.
		if p.tsServer != nil {
			_ = closeTsnetServer(p.tsServer)
			p.tsServer = nil
		}
		p.tsDialer = nil
		p.tsnetFP = ""
		// Rebind the userspace proxies to the borrowed host network.
		dialer := plugin.Dialer(&systemNetworkDialer{})
		p.closeProxiesLocked()
		p.ensureProxiesLocked(cfg, dialer)
		return dialer, nil
	}

	// Fail loudly instead of hanging on an interactive login when there is no
	// auth key and the fallback is disabled — unless prior registered node
	// state exists that tsnet can reuse without a fresh registration. A state
	// file holding only the machine key is not a registration: tsnet writes it
	// before it ever logs in, so checking existence alone let the daemon spin on
	// a login prompt instead of reporting the missing key.
	if cfg.AuthKey == "" && !hasRegisteredNodeState(cfg.StateDir) {
		return nil, fmt.Errorf("tailscale: no auth key configured and host socket fallback disabled; " +
			"set the Tailscale auth key in Datacenter Settings or enable the local-dev host-socket fallback")
	}

	// A settings change must reach the live node, not just the database: the
	// cached server is rebuilt whenever the resolved configuration changes, and
	// a replaced auth key also discards the node state so the new key is the one
	// that registers.
	if p.tsServer != nil && p.tsnetFP != cfg.fingerprint() {
		// Discard node state only when the auth key itself changed, so editing
		// hostname or the control URL does not force a re-registration.
		if cfg.AuthKey != p.tsnetAuth {
			if err := resetTsnetState(cfg.StateDir); err != nil {
				return nil, err
			}
		}
		_ = p.tsServer.Close()
		p.tsServer = nil
		p.tsDialer = nil
		p.tsnetFP = ""
		// The old dialer is gone; force the proxies to rebind to the new one.
		p.closeProxiesLocked()
	}

	if p.tsServer == nil {
		srv := &tsnet.Server{
			Dir:        cfg.StateDir,
			Hostname:   cfg.Hostname,
			AuthKey:    cfg.AuthKey,
			ControlURL: cfg.ControlURL,
			Ephemeral:  cfg.Ephemeral,
			// tsnet discs all logs by default (Logf unset), which is why
			// tailscaled.log1.txt stayed empty. Surface them, filtered to
			// state/derp/netmap/health lines so the daemon log isn't flooded.
			Logf: func(format string, args ...any) {
				msg := fmt.Sprintf(format, args...)
				if isNoisyTsnetLine(msg) {
					return
				}
				log.Printf("tsnet: %s", msg)
			},
		}
		p.tsServer = srv
		p.tsDialer = &tsnetDialer{srv: srv}
		p.tsnetFP = cfg.fingerprint()
		p.tsnetAuth = cfg.AuthKey
	}

	// Reuse one dialer so its "node is up" / "node failed" state survives
	// across calls: a fresh dialer per dial re-ran tsnet.Up for every host.
	if p.tsDialer == nil {
		p.tsDialer = &tsnetDialer{srv: p.tsServer}
	}

	// Keep the userspace SOCKS5/HTTP proxy listeners bound to the live node.
	p.ensureProxiesLocked(cfg, p.tsDialer)

	return p.tsDialer, nil
}

// SelfStatus is the live view of ATC's own tsnet node. It is derived from the
// tsnet local API (never from the host's tailscaled) and never contains the
// auth key. When the node cannot be brought up, the real error is returned so
// the UI can show it instead of pretending the mesh is connected.
type SelfStatus struct {
	Configured bool `json:"configured"`
	Connected  bool `json:"connected"`
	// Online is what the control plane reports for this node. It is distinct
	// from Connected (which is only "BackendState == Running"): a node can
	// have a running backend while control says it is offline.
	Online   bool   `json:"online"`
	Mode     string `json:"mode"` // "tsnet" | "host-socket" | "unconfigured"
	Hostname string `json:"hostname"`
	// RequestedHostname is the name ATC asked Tailscale for. Hostname is the
	// name the control plane actually assigned; they differ ("-1" suffix) when
	// a stale device still holds the requested name.
	RequestedHostname string   `json:"requestedHostname,omitempty"`
	NodeID            string   `json:"nodeId,omitempty"`
	DNSName           string   `json:"dnsName,omitempty"`
	TailscaleIPs      []string `json:"tailscaleIps"`
	BackendState      string   `json:"backendState,omitempty"`
	AuthURL           string   `json:"authUrl,omitempty"`
	Health            []string `json:"health,omitempty"`
	KeyExpiry         string   `json:"keyExpiry,omitempty"`
	PeerCount         int      `json:"peerCount"`
	LastUpAt          string   `json:"lastUpAt,omitempty"`
	StateDir          string   `json:"stateDir"`
	Ephemeral         bool     `json:"ephemeral"`
	// Proxy is the userspace SOCKS5/HTTP listener state. It is never secret.
	Proxy ProxyStatus `json:"proxy"`
	Error string      `json:"error,omitempty"`
}

// SelfStatus brings ATC's own tsnet node up (idempotently) and reports its real
// registration state. It is safe to call while other operations are dialing:
// ensureTsnet reuses a cached server and rebuilds it only when the resolved
// settings changed.
func (p *Plugin) SelfStatus(ctx context.Context) (status SelfStatus) {
	cfg := p.effectiveSettings()
	status = SelfStatus{
		Mode:              "unconfigured",
		Hostname:          cfg.Hostname,
		RequestedHostname: cfg.Hostname,
		StateDir:          cfg.StateDir,
		Ephemeral:         cfg.Ephemeral,
		TailscaleIPs:      []string{},
	}
	defer func() { status.Proxy = p.proxyStatus() }()
	if cfg.AuthKey != "" {
		status.Configured = true
	} else if hasRegisteredNodeState(cfg.StateDir) {
		// Prior registration state lets tsnet come up without a fresh key.
		status.Configured = true
	}

	dialer, err := p.ensureTsnet()
	if err != nil {
		status.Error = err.Error()
		return status
	}

	td, ok := dialer.(*tsnetDialer)
	if !ok {
		// Explicit local-dev host-socket fallback: ATC borrows the host's
		// tailscaled and therefore is not its own mesh node. Report the
		// borrowed node's real identity: returning an empty shell here made a
		// healthy host mesh read as "offline, no addresses, 0 peers".
		status.Mode = "host-socket"
		p.fillHostSocketStatus(&status)
		return status
	}

	status.Mode = "tsnet"
	// Never wait on tsnet.Up forever: it blocks while the backend is in
	// NeedsLogin, which is exactly the state an unregistered node sits in.
	// The caller's deadline is clamped rather than replaced: the HTTP handlers
	// pass 30s, which used to let a NeedsLogin node stall the whole request
	// instead of failing at tsnetUpTimeout. WithTimeout already inherits the
	// earlier of the two deadlines.
	upCtx, cancelUp := context.WithTimeout(ctx, tsnetUpTimeout)
	defer cancelUp()
	st, err := td.srv.Up(upCtx)
	if err != nil {
		status.Error = tsnetStartError(td.srv, err).Error()
		// Still report what the node knows about itself so the UI can show the
		// real backend state (NeedsLogin, health warnings, auth URL) instead of
		// only the timeout.
		if lc, lerr := td.srv.LocalClient(); lerr == nil && lc != nil {
			sctx, scancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer scancel()
			if s, serr := lc.Status(sctx); serr == nil && s != nil {
				status.BackendState = s.BackendState
				status.AuthURL = s.AuthURL
				status.Health = s.Health
				status.PeerCount = len(s.Peer)
			}
		}
		return status
	}
	status.BackendState = st.BackendState
	status.AuthURL = st.AuthURL
	status.Connected = st.BackendState == "Running"
	if st.Self != nil {
		requested := cfg.Hostname
		if st.Self.HostName != "" {
			requested = st.Self.HostName
		}
		status.RequestedHostname = requested
		// Tailscale assigns the MagicDNS name; HostName is only the request.
		status.Hostname = tailnetHostname(st.Self.DNSName, requested)
		status.NodeID = string(st.Self.ID)
		status.DNSName = st.Self.DNSName
		status.TailscaleIPs = make([]string, 0, len(st.Self.TailscaleIPs))
		for _, ip := range st.Self.TailscaleIPs {
			status.TailscaleIPs = append(status.TailscaleIPs, ip.String())
		}
	}
	status.Online = st.BackendState == "Running" && (st.Self == nil || st.Self.Online)
	status.LastUpAt = p.markUp(status.Connected)

	// Kick off the self-heal watchdog the first time the node comes up. It is
	// guarded by sync.Once so repeated SelfStatus calls do not spawn workers.
	p.startWatchdog()

	// Enrich from the tsnet local API: health problems, node-key expiry, and
	// the real control-plane view of this node's online state. /api/tailscale/self
	// used to report "connected" purely from BackendState==Running, which lied
	// while control reported online=False.
	if lc, err := td.srv.LocalClient(); err == nil && lc != nil {
		if s, err := lc.Status(ctx); err == nil {
			status.Health = s.Health
			status.PeerCount = len(s.Peer)
			if s.Self != nil {
				status.Online = s.Self.Online
				if s.Self.HostName != "" {
					status.RequestedHostname = s.Self.HostName
				}
				status.Hostname = tailnetHostname(s.Self.DNSName, status.RequestedHostname)
				status.NodeID = string(s.Self.ID)
				if s.Self.KeyExpiry != nil {
					status.KeyExpiry = s.Self.KeyExpiry.UTC().Format(time.RFC3339)
				}
			}
		}
	}
	return status
}

// tailnetHostname returns the device name the control plane assigned, derived
// from its MagicDNS name (first label). Tailscale appends -1, -2, ... when the
// requested hostname is already held by another device, and admin consoles show
// that assigned name rather than the requested one.
func tailnetHostname(dnsName, requested string) string {
	dnsName = strings.TrimSuffix(strings.TrimSpace(dnsName), ".")
	if dnsName != "" {
		if i := strings.IndexByte(dnsName, '.'); i > 0 {
			return dnsName[:i]
		}
		return dnsName
	}
	return strings.TrimSpace(requested)
}

// fillHostSocketStatus populates an unreported self view from the host's
// tailscaled. The fallback has no tsnet node of its own, so the borrowed node is
// the only truthful identity to show; a failure is surfaced in Error instead of
// leaving a blank-but-successful-looking payload.
func (p *Plugin) fillHostSocketStatus(status *SelfStatus) {
	host, err := tailscale.GetLocalStatus()
	if err != nil {
		status.Error = fmt.Sprintf("host tailscaled status unavailable: %v", err)
		return
	}
	status.BackendState = host.BackendState
	status.Connected = host.BackendState == "Running"
	status.Online = host.Self.Online
	if host.Self.HostName != "" {
		status.RequestedHostname = host.Self.HostName
	}
	status.Hostname = tailnetHostname(host.Self.DNSName, status.RequestedHostname)
	status.NodeID = host.Self.ID
	status.DNSName = host.Self.DNSName
	if len(host.Self.TailscaleIPs) > 0 {
		status.TailscaleIPs = append([]string{}, host.Self.TailscaleIPs...)
	}
	status.PeerCount = len(host.Peer)
	status.LastUpAt = p.markUp(status.Connected)
}

// markUp records the moment the backend became Running and returns it as an
// RFC3339 timestamp. It returns "" while disconnected.
func (p *Plugin) markUp(connected bool) string {
	p.upMu.Lock()
	defer p.upMu.Unlock()
	if !connected {
		p.upSince = time.Time{}
		return ""
	}
	if p.upSince.IsZero() {
		p.upSince = time.Now().UTC()
	}
	return p.upSince.Format(time.RFC3339)
}

var watchdogOnce sync.Once

// startWatchdog launches the background self-heal loop at most once per plugin.
func (p *Plugin) startWatchdog() {
	watchdogOnce.Do(func() {
		go p.watchdogLoop(context.Background())
	})
}

// watchdogLoop runs every 30s and drives the repair ladder
// (force-netmap-update -> rebind -> close+Up) only when the mesh is genuinely
// broken. Informational health warnings are ignored: Tailscale reports them for
// healthy nodes (e.g. "--accept-routes is false"), and treating those as faults
// restarted tsnet every minute, aborting every in-flight connection.
func (p *Plugin) watchdogLoop(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	policy := repairPolicy{}
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			st := p.SelfStatus(ctx)
			if !shouldRepair(st) {
				policy.recordHealthy(now)
				continue
			}
			if !policy.mayRepair(now) {
				continue
			}
			log.Printf("tsnet watchdog: repairing mesh (%s); this drops in-flight SSH/PTY/DB sessions", repairReason(st))
			_, _, _ = p.Repair(ctx)
			policy.recordRepair(now)
		}
	}
}

// Repair timing: three consecutive bad readings (~90s) before acting, then at
// most one repair per 10 minutes, backing off to 60 minutes while it keeps
// failing. A sustained healthy period resets the backoff.
const (
	repairBadStreak   = 3
	repairBaseBackoff = 10 * time.Minute
	repairMaxBackoff  = 60 * time.Minute
)

// repairPolicy debounces the self-heal ladder.
type repairPolicy struct {
	badStreak  int
	lastRepair time.Time
	backoff    time.Duration
}

func (p *repairPolicy) mayRepair(now time.Time) bool {
	p.badStreak++
	if p.badStreak < repairBadStreak {
		return false
	}
	if !p.lastRepair.IsZero() && now.Sub(p.lastRepair) < p.cooldown() {
		return false
	}
	return true
}

func (p *repairPolicy) cooldown() time.Duration {
	if p.backoff > 0 {
		return p.backoff
	}
	return repairBaseBackoff
}

func (p *repairPolicy) recordRepair(now time.Time) {
	p.badStreak = 0
	p.lastRepair = now
	if p.backoff == 0 {
		p.backoff = repairBaseBackoff
		return
	}
	p.backoff *= 2
	if p.backoff > repairMaxBackoff {
		p.backoff = repairMaxBackoff
	}
}

func (p *repairPolicy) recordHealthy(now time.Time) {
	p.badStreak = 0
	if p.backoff > 0 && !p.lastRepair.IsZero() && now.Sub(p.lastRepair) > repairMaxBackoff {
		p.backoff = 0
	}
}

// benignHealthMarkers are Tailscale health strings that describe a healthy node.
func benignHealthMarkers() []string {
	return []string{
		"--accept-routes is false",
		"advertising routes",
		"exit node",
		"is starting",
		"warming-up",
	}
}

// fatalHealthMarkers are health strings that indicate the node cannot reach the
// tailnet and self-healing may help. Anything unmatched is treated as benign so
// a new informational string can never reintroduce a repair loop.
func fatalHealthMarkers() []string {
	return []string{
		"not connected",
		"backend error",
		"backend failed",
		"failed to connect",
		"no derp",
		"unreachable",
		"has expired",
		"key expired",
		"shut down",
		"state is stopped",
	}
}

// healthIsFatal classifies one Tailscale health entry.
func healthIsFatal(entry string) bool {
	lower := strings.ToLower(strings.TrimSpace(entry))
	if lower == "" {
		return false
	}
	for _, marker := range benignHealthMarkers() {
		if strings.Contains(lower, marker) {
			return false
		}
	}
	for _, marker := range fatalHealthMarkers() {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// shouldRepair reports whether a status reading means the mesh needs the repair
// ladder. A connected, online node with only informational warnings is healthy.
func shouldRepair(st SelfStatus) bool {
	if !st.Connected || !st.Online {
		return true
	}
	if strings.EqualFold(strings.TrimSpace(st.BackendState), "NoState") {
		return true
	}
	for _, entry := range st.Health {
		if healthIsFatal(entry) {
			return true
		}
	}
	return false
}

// repairReason renders a short cause for the watchdog log.
func repairReason(st SelfStatus) string {
	switch {
	case !st.Connected:
		return "backend not connected"
	case !st.Online:
		return "control plane reports offline"
	}
	for _, entry := range st.Health {
		if healthIsFatal(entry) {
			return entry
		}
	}
	if st.BackendState != "" {
		return "backend state " + st.BackendState
	}
	return "degraded"
}

// TsnetPing pings a target through ATC's own tsnet node (not the host's).
// Returns (output, isDirect, rtt, err). When tsnet is not the active transport
// it returns an error so the caller can fall back to the host tailscale CLI.
func (p *Plugin) TsnetPing(ctx context.Context, target string) (string, bool, *string, error) {
	td, err := p.ensureTsnet()
	if err != nil {
		return "", false, nil, err
	}
	d, ok := td.(*tsnetDialer)
	if !ok {
		return "", false, nil, fmt.Errorf("host-socket mode: use host ping")
	}
	lc, err := d.srv.LocalClient()
	if err != nil || lc == nil {
		return "", false, nil, fmt.Errorf("tsnet local client unavailable: %v", err)
	}

	// If the target is an IP, ping it directly through tsnet.
	if ip, err := netip.ParseAddr(strings.TrimSpace(target)); err == nil {
		res, err := lc.Ping(ctx, ip, tailcfg.PingDisco)
		if err != nil {
			return "", false, nil, err
		}
		isDirect := res.Endpoint != "" && !strings.Contains(strings.ToLower(res.Endpoint), "derp")
		var rtt *string
		if res.LatencySeconds > 0 {
			s := fmt.Sprintf("%.1fms", res.LatencySeconds*1000)
			rtt = &s
		}
		out := fmt.Sprintf("via %s in %.1fms", res.Endpoint, res.LatencySeconds*1000)
		if res.Err != "" {
			return out, isDirect, rtt, fmt.Errorf("%s", res.Err)
		}
		return out, isDirect, rtt, nil
	}

	// Otherwise resolve by hostname against tsnet's own netmap.
	st, err := lc.Status(ctx)
	if err != nil {
		return "", false, nil, err
	}
	for _, peer := range st.Peer {
		if strings.EqualFold(peer.HostName, target) || strings.EqualFold(strings.TrimSuffix(peer.DNSName, "."), target) {
			if !peer.Online {
				return fmt.Sprintf("%s: offline", peer.HostName), false, nil, fmt.Errorf("peer offline")
			}
			ips := make([]string, 0, len(peer.TailscaleIPs))
			for _, ip := range peer.TailscaleIPs {
				ips = append(ips, ip.String())
			}
			out := fmt.Sprintf("%s online (%s)", peer.HostName, strings.Join(ips, ","))
			return out, false, nil, nil
		}
	}
	return "", false, nil, fmt.Errorf("peer %q not found in tsnet netmap", target)
}

// TsnetPeers returns the peers from ATC's own tsnet node's netmap. It returns
// (nil, nil) when tsnet is not the active transport (host-socket mode), so the
// caller can fall back to the host tailscaled view.
func (p *Plugin) TsnetPeers(ctx context.Context) ([]tailscale.PeerStatus, error) {
	dialer, err := p.ensureTsnet()
	if err != nil {
		return nil, err
	}
	td, ok := dialer.(*tsnetDialer)
	if !ok {
		return nil, nil
	}
	lc, err := td.srv.LocalClient()
	if err != nil || lc == nil {
		return nil, fmt.Errorf("tsnet local client unavailable: %v", err)
	}
	st, err := lc.Status(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]tailscale.PeerStatus, 0, len(st.Peer))
	for _, peer := range st.Peer {
		ps := tailscale.PeerStatus{
			HostName: peer.HostName,
			DNSName:  strings.TrimSuffix(peer.DNSName, "."),
			OS:       peer.OS,
			Online:   peer.Online,
			Active:   peer.Online,
		}
		if peer.ID != "" {
			ps.ID = string(peer.ID)
		}
		if peer.TailscaleIPs != nil {
			for _, ip := range peer.TailscaleIPs {
				ps.TailscaleIPs = append(ps.TailscaleIPs, ip.String())
			}
		}
		ps.Addrs = peer.Addrs
		if peer.Tags != nil {
			ps.Tags = peer.Tags.AsSlice()
		}
		out = append(out, ps)
	}
	return out, nil
}

// Repair runs the self-heal ladder once and returns the resulting status plus
// the steps taken. It is safe to call concurrently with normal operations.
func (p *Plugin) Repair(ctx context.Context) (SelfStatus, []string, error) {
	var steps []string
	st := p.SelfStatus(ctx)
	if st.Connected && st.Online && len(st.Health) == 0 {
		steps = append(steps, "already healthy: no repair needed")
		return st, steps, nil
	}

	// Step 1: force a netmap replay.
	if td, err := p.ensureTsnet(); err == nil {
		if d, ok := td.(*tsnetDialer); ok {
			if lc, err := d.srv.LocalClient(); err == nil && lc != nil {
				if err := lc.DebugAction(ctx, "force-netmap-update"); err != nil {
					steps = append(steps, "force-netmap-update failed: "+err.Error())
				} else {
					steps = append(steps, "force-netmap-update")
				}
			}
		}
	}
	time.Sleep(3 * time.Second)
	st = p.SelfStatus(ctx)
	if st.Connected && st.Online && len(st.Health) == 0 {
		return st, steps, nil
	}

	// Step 2: rebind the magicsock.
	if td, err := p.ensureTsnet(); err == nil {
		if d, ok := td.(*tsnetDialer); ok {
			if lc, err := d.srv.LocalClient(); err == nil && lc != nil {
				if err := lc.DebugAction(ctx, "rebind"); err != nil {
					steps = append(steps, "rebind failed: "+err.Error())
				} else {
					steps = append(steps, "rebind")
				}
			}
		}
	}
	time.Sleep(3 * time.Second)
	st = p.SelfStatus(ctx)
	if st.Connected && st.Online && len(st.Health) == 0 {
		return st, steps, nil
	}

	// Step 3: close and bring the node back up from scratch.
	p.mu.Lock()
	if p.tsServer != nil {
		_ = p.tsServer.Close()
		p.tsServer = nil
	}
	p.tsDialer = nil
	p.dialer = nil
	p.tsnetFP = ""
	p.tsnetAuth = ""
	p.closeProxiesLocked()
	p.mu.Unlock()
	time.Sleep(1 * time.Second)
	st = p.SelfStatus(ctx)
	steps = append(steps, "close+Up restart")
	return st, steps, nil
}

type systemNetworkDialer struct{}

func (s *systemNetworkDialer) Dial(ctx context.Context, network, address string) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, network, address)
}

type tsnetDialer struct {
	srv *tsnet.Server

	mu    sync.Mutex
	ready bool

	// lastErr/lastErrAt short-circuit repeated dials while the node is known to
	// be unregistered (see tsnetFailureCooldown).
	lastErr   error
	lastErrAt time.Time
}

// ensureRunning brings the tsnet node up before the first dial. Without this,
// the very first connections raced registration and failed with "tsnet: backend
// in state NoState" (which the startup cache warmup hit for every host).
func (td *tsnetDialer) ensureRunning(ctx context.Context) error {
	td.mu.Lock()
	if td.ready {
		td.mu.Unlock()
		return nil
	}
	// The node is known to be unregistered: fail immediately instead of letting
	// every caller pay the full Up timeout again.
	if td.lastErr != nil && time.Since(td.lastErrAt) < tsnetFailureCooldown {
		err := td.lastErr
		td.mu.Unlock()
		return err
	}
	td.mu.Unlock()

	upCtx, cancel := context.WithTimeout(ctx, tsnetUpTimeout)
	defer cancel()
	if _, err := td.srv.Up(upCtx); err != nil {
		wrapped := tsnetStartError(td.srv, err)
		td.mu.Lock()
		td.lastErr = wrapped
		td.lastErrAt = time.Now()
		td.mu.Unlock()
		return wrapped
	}

	td.mu.Lock()
	defer td.mu.Unlock()
	// Another goroutine may have completed the same handshake meanwhile.
	td.ready = true
	td.lastErr = nil
	return nil
}

func (td *tsnetDialer) Dial(ctx context.Context, network, address string) (net.Conn, error) {
	if err := td.ensureRunning(ctx); err != nil {
		return nil, fmt.Errorf("tsnet not ready: %w", err)
	}
	return td.srv.Dial(ctx, network, address)
}

// Runner provides command execution over Tailscale SSH.
type Runner struct {
	client *ssh.Client
}

// RunCommand executes a command remotely over the SSH session.
func (r *Runner) RunCommand(ctx context.Context, cmd string) (string, error) {
	if r.client == nil {
		return "", fmt.Errorf("tailscale ssh client is nil")
	}

	session, err := r.client.NewSession()
	if err != nil {
		return "", fmt.Errorf("failed to create tailscale ssh session: %w", err)
	}
	defer session.Close()

	done := make(chan struct{})
	var out []byte
	var runErr error

	go func() {
		out, runErr = session.CombinedOutput(cmd)
		close(done)
	}()

	select {
	case <-ctx.Done():
		_ = session.Signal(ssh.SIGKILL)
		return "", ctx.Err()
	case <-done:
		if runErr != nil {
			return string(out), fmt.Errorf("tailscale ssh execution failed: %w (output: %s)", runErr, string(out))
		}
		return string(out), nil
	}
}

// Dial satisfies plugin.Dialer.
func (p *Plugin) Dial(ctx context.Context, network, address string) (net.Conn, error) {
	dialer, err := p.ensureTsnet()
	if err != nil {
		return nil, err
	}
	return dialer.Dial(ctx, network, address)
}

// NewRunner creates a command runner targeting a Tailscale peer.
func (p *Plugin) NewRunner(h plugin.HostFacts) (plugin.Runner, error) {
	client, err := p.GetClient(h)
	if err != nil {
		return nil, err
	}
	return &Runner{client: client}, nil
}

// EvictClient removes a cached client for the given facts.
func (p *Plugin) EvictClient(h plugin.HostFacts) {
	host := h.HostRoute
	if host == "" {
		return
	}
	user := "root"
	if u, ok := h.Overrides["user"]; ok && u != "" {
		user = u
	}
	port := 22
	if pStr, ok := h.Overrides["port"]; ok && pStr != "" {
		if pVal, err := strconv.Atoi(pStr); err == nil && pVal > 0 {
			port = pVal
		}
	}
	authMethods, credentialID, err := authMethodsForHost(h)
	if err != nil {
		return
	}
	key := fmt.Sprintf("%s@%s:%d#%s", user, host, port, credentialID)
	p.mu.Lock()
	if c, ok := p.clients[key]; ok {
		delete(p.clients, key)
		p.mu.Unlock()
		_ = c.Close()
	} else {
		p.mu.Unlock()
	}
	_ = authMethods
}

// migrateLegacyStateDir moves a /tmp-based state directory to the data store's
// directory the first time it runs, preserving the registration. It is a no-op
// when the state dir is already outside /tmp or the target already exists.
func (p *Plugin) migrateLegacyStateDir(cfg *tsnetSettings) {
	if !strings.HasPrefix(cfg.StateDir, "/tmp/") {
		// Persist the normalized absolute form once. A relative value (written
		// when the daemon ran from backend-go/) is only meaningful to the
		// process that wrote it; anchoring it stops a later launch from
		// silently creating a second, unregistered node elsewhere.
		if raw := persistedSetting(FieldStateDir); raw != "" && raw != cfg.StateDir {
			persistStateDir(cfg.StateDir)
		}
		return
	}
	log.Printf("tsnet migrate: moving legacy state dir %q out of /tmp", cfg.StateDir)
	dbPath := ""
	if st := plugin.GlobalSettingsStore(); st != nil {
		dbPath = st.DBPath()
	}
	if dbPath == "" {
		dbPath = plugin.ResolveDBPath("")
	}
	if dbPath == "" {
		return
	}
	if abs, err := filepath.Abs(dbPath); err == nil {
		dbPath = abs
	}
	newDir := filepath.Join(filepath.Dir(dbPath), "tsnet")
	if _, err := os.Stat(newDir); err == nil {
		// Already migrated (or created): just switch over.
		cfg.StateDir = newDir
		persistStateDir(newDir)
		return
	}
	if _, err := os.Stat(cfg.StateDir); err == nil {
		if err := copyDir(cfg.StateDir, newDir); err != nil {
			log.Printf("tsnet: failed to migrate state dir %s -> %s: %v", cfg.StateDir, newDir, err)
			return
		}
	} else {
		if err := os.MkdirAll(newDir, 0700); err != nil {
			log.Printf("tsnet: failed to create state dir %s: %v", newDir, err)
			return
		}
	}
	cfg.StateDir = newDir
	persistStateDir(newDir)
}

// persistStateDir stores the normalized state directory in the settings page.
func persistStateDir(dir string) {
	adapter := settingsframework.NewStoreAdapter(nil)
	if err := adapter.Save(SettingsPageID, map[string]any{FieldStateDir: dir}); err != nil {
		log.Printf("tsnet: failed to persist state dir %q: %v", dir, err)
	}
}

// copyDir recursively copies src into dst.
func copyDir(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dst, info.Mode()); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		s := filepath.Join(src, e.Name())
		d := filepath.Join(dst, e.Name())
		if e.IsDir() {
			if err := copyDir(s, d); err != nil {
				return err
			}
		} else {
			info, err := os.Stat(s)
			if err != nil {
				return err
			}
			data, err := os.ReadFile(s)
			if err != nil {
				return err
			}
			mode := info.Mode().Perm()
			if mode == 0 {
				mode = 0600
			}
			if err := os.WriteFile(d, data, mode); err != nil {
				return err
			}
		}
	}
	return nil
}

// isNoisyTsnetLine filters the tsnet internal log stream: keep state, derp,
// netmap, and health lines, drop the per-packet/keepalive noise that would
// otherwise flood the daemon log.
func isNoisyTsnetLine(msg string) bool {
	lower := strings.ToLower(msg)
	noisy := []string{
		"magicsock:", "portmap:", "udp", "keepalive", "disco ping",
		"received packet", "sending packet", "stun", "netcheck:",
		"wireguard", "noise_handshake", "sendmsg", "recvmsg",
	}
	for _, n := range noisy {
		if strings.Contains(lower, n) {
			return true
		}
	}
	return false
}

// authMethodsForHost assembles the SSH auth methods a peer offers, in priority
// order: the vault secret referenced by the node or tag chunk, legacy
// private_key/password overrides, then the local ssh-agent.
//
// A referenced secret that cannot be decrypted or parsed fails loudly: silently
// continuing with no methods produced the opaque "attempted methods [none]"
// handshake error this transport used to emit. The returned credentialID is a
// non-reversible fingerprint used only in the client cache key, so rotating a
// secret never reuses a connection authenticated with the old value.
func authMethodsForHost(h plugin.HostFacts) ([]ssh.AuthMethod, string, error) {
	var methods []ssh.AuthMethod
	fingerprint := ""

	if vaultID := strings.TrimSpace(h.Overrides["vaultSecretId"]); vaultID != "" {
		sec, err := secrets.Resolve(context.Background(), vaultID)
		if err != nil {
			return nil, "", err
		}
		switch sec.Kind {
		case secrets.KindPrivateKey:
			var signer ssh.Signer
			if pass := h.Overrides["passphrase"]; pass != "" {
				signer, err = ssh.ParsePrivateKeyWithPassphrase([]byte(sec.Value), []byte(pass))
			} else {
				signer, err = ssh.ParsePrivateKey([]byte(sec.Value))
			}
			if err != nil {
				return nil, "", fmt.Errorf("vault secret %q is not a usable SSH private key: %w", vaultID, err)
			}
			methods = append(methods, ssh.PublicKeys(signer))
		case secrets.KindPassword:
			methods = append(methods, ssh.Password(sec.Value))
		default:
			return nil, "", fmt.Errorf("vault secret %q has unsupported kind %q", vaultID, sec.Kind)
		}
		sum := sha256.Sum256([]byte(sec.Value))
		fingerprint = "vault:" + vaultID + ":" + hex.EncodeToString(sum[:8])
	} else {
		if pkStr, ok := h.Overrides["private_key"]; ok && strings.TrimSpace(pkStr) != "" {
			var signer ssh.Signer
			var err error
			if pass := h.Overrides["passphrase"]; pass != "" {
				signer, err = ssh.ParsePrivateKeyWithPassphrase([]byte(pkStr), []byte(pass))
			} else {
				signer, err = ssh.ParsePrivateKey([]byte(pkStr))
			}
			if err != nil {
				return nil, "", fmt.Errorf("private_key override is not a usable SSH private key: %w", err)
			}
			methods = append(methods, ssh.PublicKeys(signer))
			sum := sha256.Sum256([]byte(pkStr))
			fingerprint = "key:" + hex.EncodeToString(sum[:8])
		}
		if pass, ok := h.Overrides["password"]; ok && pass != "" {
			methods = append(methods, ssh.Password(pass))
			if fingerprint == "" {
				sum := sha256.Sum256([]byte(pass))
				fingerprint = "pw:" + hex.EncodeToString(sum[:8])
			}
		}
	}

	if agentAuth, _ := getSSHAgentAuthMethod(); agentAuth != nil {
		methods = append(methods, agentAuth)
		if fingerprint == "" {
			fingerprint = "agent"
		}
	}

	if fingerprint == "" {
		fingerprint = "none"
	}
	return methods, fingerprint, nil
}

// GetClient connects or retrieves a pooled SSH client over Tailscale.
func (p *Plugin) GetClient(h plugin.HostFacts) (*ssh.Client, error) {
	host := h.HostRoute
	if host == "" {
		return nil, fmt.Errorf("hostRoute is required for tailscale connection")
	}

	port := 22
	if pStr, ok := h.Overrides["port"]; ok && pStr != "" {
		if pVal, err := strconv.Atoi(pStr); err == nil && pVal > 0 {
			port = pVal
		}
	}

	user := "root"
	if u, ok := h.Overrides["user"]; ok && u != "" {
		user = u
	}

	// Resolve the node/tag vault credential before touching the client cache so
	// the cache key reflects the credential actually offered (rotating the
	// secret must not reuse a connection authenticated with the old value).
	authMethods, credentialID, err := authMethodsForHost(h)
	if err != nil {
		return nil, err
	}

	key := fmt.Sprintf("%s@%s:%d#%s", user, host, port, credentialID)

	p.mu.Lock()
	if client, ok := p.clients[key]; ok {
		p.mu.Unlock()
		// Liveness check: a cached client whose underlying connection died
		// (e.g. after a tsnet re-registration) must not be handed out again.
		if _, _, err := client.SendRequest("keepalive@openssh.com", false, nil); err == nil {
			return client, nil
		}
		p.mu.Lock()
		delete(p.clients, key)
		p.mu.Unlock()
		_ = client.Close()
	} else {
		p.mu.Unlock()
	}

	dialer, err := p.ensureTsnet()
	if err != nil {
		return nil, fmt.Errorf("failed to initialize tsnet dialer: %w", err)
	}

	timeout := 10 * time.Second
	if tStr, ok := h.Overrides["timeout"]; ok && tStr != "" {
		if d, err := time.ParseDuration(tStr); err == nil {
			timeout = d
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	targetAddr := net.JoinHostPort(host, strconv.Itoa(port))
	conn, err := dialer.Dial(ctx, "tcp", targetAddr)
	if err != nil {
		return nil, fmt.Errorf("tailscale dial to %s failed: %w", targetAddr, err)
	}

	// Automatic none-auth negotiation (verified in Task 1 spike) stays enabled:
	// hosts enrolled in Tailscale SSH authenticate at the control layer, so an
	// empty Auth list is valid for them.
	clientConfig := &ssh.ClientConfig{
		User:            user,
		Auth:            authMethods,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         timeout,
	}

	sshConn, chans, reqs, err := ssh.NewClientConn(conn, targetAddr, clientConfig)
	if err != nil {
		conn.Close()
		if len(authMethods) == 0 {
			return nil, fmt.Errorf(
				"tailscale ssh handshake failed to %s@%s: %w (no credential offered: node has no vaultSecretId, no private_key/password override, and no ssh-agent socket; hosts that rely on Tailscale SSH must use authMethod=tailscale-ssh)",
				user, targetAddr, err,
			)
		}
		return nil, fmt.Errorf("tailscale ssh handshake failed to %s@%s: %w", user, targetAddr, err)
	}

	client := ssh.NewClient(sshConn, chans, reqs)

	p.mu.Lock()
	p.clients[key] = client
	p.mu.Unlock()

	return client, nil
}

// Probe verifies reachability over Tailscale.
func (p *Plugin) Probe(ctx context.Context, r plugin.Runner, h plugin.HostFacts) (plugin.Detection, error) {
	if r == nil {
		var err error
		r, err = p.NewRunner(h)
		if err != nil {
			return plugin.Detection{
				Detected: false,
				Status:   "OFFLINE",
				Error:    err.Error(),
			}, nil
		}
	}

	out, err := r.RunCommand(ctx, "echo ATC_TAILSCALE_PROBE")
	if err != nil {
		return plugin.Detection{
			Detected: false,
			Status:   "UNREACHABLE",
			Error:    err.Error(),
		}, nil
	}

	return plugin.Detection{
		Detected: true,
		Status:   "ONLINE",
		Details:  strings.TrimSpace(out),
	}, nil
}

// Close closes the tsnet server and all active SSH connections.
func (p *Plugin) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	for _, client := range p.clients {
		_ = client.Close()
	}
	p.clients = make(map[string]*ssh.Client)
	p.closeProxiesLocked()

	if p.tsServer != nil {
		err := p.tsServer.Close()
		p.tsServer = nil
		p.tsnetFP = ""
		p.tsnetAuth = ""
		return err
	}
	return nil
}
