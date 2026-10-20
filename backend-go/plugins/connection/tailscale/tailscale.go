package tailscale

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"tailscale.com/tsnet"

	"air-traffic-control/backend/pkg/plugin"
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
	FieldAuthKey    = "authKey"
	FieldHostname   = "hostname"
	FieldStateDir   = "stateDir"
	FieldControlURL = "controlURL"
	FieldEphemeral  = "ephemeral"
)

// SettingsPage implements plugin.SettingsPageProvider so ATC's own Tailscale
// node settings render in the Datacenter Settings sidebar with no bespoke
// frontend code. The auth key is a write-only secret: the framework masks it in
// every response and only ever persists genuinely new material.
func (p *Plugin) SettingsPage() plugin.SettingsPage {
	return plugin.SettingsPage{
		ID:    SettingsPageID,
		Title: "ATC Tailscale",
		Icon:  "hub",
		Description: "ATC's own Tailscale node (embedded tsnet) — the identity ATC uses to reach the mesh. " +
			"The auth key is write-only and masked in every API response.",
		Order: 15,
		Fields: []plugin.SettingsField{
			{
				Name:        FieldAuthKey,
				Label:       "Tailscale Auth Key",
				Type:        plugin.SettingsFieldSecret,
				Secret:      true,
				Placeholder: "tskey-auth-...",
				Description: "Auth key used to register ATC's own node. Saving a new key re-registers ATC: " +
					"the previous tsnet state is discarded so the new key is the one that takes effect.",
			},
			{
				Name:        FieldHostname,
				Label:       "Node Hostname",
				Type:        plugin.SettingsFieldString,
				Default:     DefaultHostname,
				Placeholder: DefaultHostname,
				Description: "Hostname ATC advertises to the tailnet.",
			},
			{
				Name:        FieldStateDir,
				Label:       "tsnet State Directory",
				Type:        plugin.SettingsFieldString,
				Default:     DefaultStateDir,
				Placeholder: DefaultStateDir,
				Description: "Directory holding ATC's tsnet node state (file store + logs).",
			},
			{
				Name:        FieldControlURL,
				Label:       "Control Server URL",
				Type:        plugin.SettingsFieldString,
				Placeholder: "https://controlplane.tailscale.com",
				Description: "Leave blank for the Tailscale-hosted control plane; set it for Headscale or a self-hosted control server.",
			},
			{
				Name:        FieldEphemeral,
				Label:       "Ephemeral Node",
				Type:        plugin.SettingsFieldBool,
				Default:     true,
				Description: "Register ATC as an ephemeral node so the tailnet drops it when ATC goes offline.",
			},
		},
	}
}

// tsnetSettings is the effective configuration for ATC's own tsnet node,
// resolved from explicit plugin options, then persisted settings, then
// environment variables.
type tsnetSettings struct {
	AuthKey    string
	Hostname   string
	StateDir   string
	ControlURL string
	Ephemeral  bool
}

// fingerprint identifies the configuration a cached tsnet.Server was built
// from, so a settings change actually rebuilds it instead of being inert.
func (c tsnetSettings) fingerprint() string {
	return strings.Join([]string{c.Hostname, c.StateDir, c.ControlURL, c.AuthKey, strconv.FormatBool(c.Ephemeral)}, "\x00")
}

// persistedSetting reads a string setting written through the settings page.
func persistedSetting(field string) string {
	store := plugin.GlobalSettingsStore()
	if store == nil {
		return ""
	}
	v, ok := store.Value(SettingsPageID, field)
	if !ok {
		return ""
	}
	return strings.TrimSpace(v)
}

// effectiveSettings resolves the configuration for ATC's own tsnet node.
// Precedence per field: explicit plugin option, then the persisted settings
// page value, then environment variables (TS_AUTHKEY, TS_CONTROL_URL).
func (p *Plugin) effectiveSettings() tsnetSettings {
	cfg := tsnetSettings{
		AuthKey:   strings.TrimSpace(p.authKey),
		Hostname:  p.hostname,
		StateDir:  p.stateDir,
		Ephemeral: p.ephemeral,
	}

	if v := persistedSetting(FieldHostname); v != "" {
		cfg.Hostname = v
	}
	if v := persistedSetting(FieldStateDir); v != "" {
		cfg.StateDir = v
	}
	if v := persistedSetting(FieldControlURL); v != "" {
		cfg.ControlURL = v
	}
	if v := persistedSetting(FieldEphemeral); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.Ephemeral = b
		}
	}
	if cfg.AuthKey == "" {
		if v := persistedSetting(FieldAuthKey); v != "" && !plugin.IsMaskedSecretValue(v) {
			cfg.AuthKey = v
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

// Plugin provides connectivity over the Tailscale mesh using embedded tsnet or custom dialer.
type Plugin struct {
	mu          sync.Mutex
	tsServer    *tsnet.Server
	dialer      plugin.Dialer
	clients     map[string]*ssh.Client
	stateDir    string
	hostname    string
	authKey     string
	ephemeral   bool
	tsnetFP     string
	tsnetAuth   string
	initialized bool
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
		ephemeral: true,
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
		ControllerCollections: []string{
			"ansible.builtin",
		},
	}
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

func (p *Plugin) ensureTsnet() (plugin.Dialer, error) {
	// Resolve settings before taking the lock: the settings store has its own
	// locking and must never be held while dialing.
	cfg := p.effectiveSettings()

	p.mu.Lock()
	defer p.mu.Unlock()

	if p.dialer != nil {
		return p.dialer, nil
	}

	// If a host-level system tailscaled is active and no auth key is configured,
	// direct dial through standard network stack which automatically routes 100.x / MagicDNS.
	if isSystemTailscaledActive() && cfg.AuthKey == "" {
		return &systemNetworkDialer{}, nil
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
		p.tsnetFP = ""
	}

	if p.tsServer == nil {
		srv := &tsnet.Server{
			Dir:        cfg.StateDir,
			Hostname:   cfg.Hostname,
			AuthKey:    cfg.AuthKey,
			ControlURL: cfg.ControlURL,
			Ephemeral:  cfg.Ephemeral,
		}
		p.tsServer = srv
		p.tsnetFP = cfg.fingerprint()
		p.tsnetAuth = cfg.AuthKey
	}

	return &tsnetDialer{srv: p.tsServer}, nil
}

type systemNetworkDialer struct{}

func (s *systemNetworkDialer) Dial(ctx context.Context, network, address string) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, network, address)
}

type tsnetDialer struct {
	srv *tsnet.Server
}

func (td *tsnetDialer) Dial(ctx context.Context, network, address string) (net.Conn, error) {
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

	key := fmt.Sprintf("%s@%s:%d", user, host, port)

	p.mu.Lock()
	if client, ok := p.clients[key]; ok {
		p.mu.Unlock()
		return client, nil
	}
	p.mu.Unlock()

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

	var authMethods []ssh.AuthMethod
	// 1. Agent forwarding
	if agentAuth, _ := getSSHAgentAuthMethod(); agentAuth != nil {
		authMethods = append(authMethods, agentAuth)
	}

	// 2. Private Key
	if pkStr, ok := h.Overrides["private_key"]; ok && strings.TrimSpace(pkStr) != "" {
		pass := h.Overrides["passphrase"]
		var signer ssh.Signer
		var parseErr error
		if pass != "" {
			signer, parseErr = ssh.ParsePrivateKeyWithPassphrase([]byte(pkStr), []byte(pass))
		} else {
			signer, parseErr = ssh.ParsePrivateKey([]byte(pkStr))
		}
		if parseErr == nil {
			authMethods = append(authMethods, ssh.PublicKeys(signer))
		}
	}
	if pass, ok := h.Overrides["password"]; ok && pass != "" {
		authMethods = append(authMethods, ssh.Password(pass))
	}

	// Automatic none-auth negotiation (verified in Task 1 spike)
	clientConfig := &ssh.ClientConfig{
		User:            user,
		Auth:            authMethods,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         timeout,
	}

	sshConn, chans, reqs, err := ssh.NewClientConn(conn, targetAddr, clientConfig)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("tailscale ssh handshake failed: %w", err)
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

	if p.tsServer != nil {
		err := p.tsServer.Close()
		p.tsServer = nil
		p.tsnetFP = ""
		p.tsnetAuth = ""
		return err
	}
	return nil
}
