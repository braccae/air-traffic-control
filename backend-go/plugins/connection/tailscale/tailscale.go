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
	"air-traffic-control/backend/pkg/plugin/newapi"
	"air-traffic-control/backend/pkg/plugin/settingsframework"
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
	FieldAuthKey    = "authKey"
	FieldHostname   = "hostname"
	FieldStateDir   = "stateDir"
	FieldControlURL = "controlURL"
	FieldEphemeral  = "ephemeral"
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
		WithDescription("ATC's own Tailscale node (embedded tsnet) — the identity ATC uses to reach the mesh. " +
			"The auth key is write-only and masked in every API response.").
		SecretField(
			FieldAuthKey,
			"Tailscale Auth Key",
			settingsframework.WithPlaceholder("tskey-auth-..."),
			settingsframework.WithDescription("Auth key used to register ATC's own node. Saving a new key re-registers ATC: "+
				"the previous tsnet state is discarded so the new key is the one that takes effect."),
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
			true,
			settingsframework.WithDescription("Register ATC as an ephemeral node so the tailnet drops it when ATC goes offline."),
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
		AuthKey:    strings.TrimSpace(p.authKey),
		Hostname:   adapter.StringOrDefault(SettingsPageID, FieldHostname, p.hostname),
		StateDir:   adapter.StringOrDefault(SettingsPageID, FieldStateDir, p.stateDir),
		ControlURL: adapter.StringOrDefault(SettingsPageID, FieldControlURL, ""),
		Ephemeral:  adapter.BoolOrDefault(SettingsPageID, FieldEphemeral, p.ephemeral),
	}

	if cfg.AuthKey == "" {
		if secret, ok := adapter.SecretValue(SettingsPageID, FieldAuthKey); ok {
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
