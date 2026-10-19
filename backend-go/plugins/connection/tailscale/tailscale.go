package tailscale

import (
	"context"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
	"tailscale.com/tsnet"

	"air-traffic-control/backend/pkg/plugin"
)

func init() {
	_ = plugin.Register(New())
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
		clients:  make(map[string]*ssh.Client),
		stateDir: "/tmp/atc-tsnet",
		hostname: "air-traffic-control-tsnet",
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

func (p *Plugin) ensureTsnet() (plugin.Dialer, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.dialer != nil {
		return p.dialer, nil
	}

	if p.tsServer == nil {
		authKey := p.authKey
		if authKey == "" {
			authKey = os.Getenv("TS_AUTHKEY")
		}

		srv := &tsnet.Server{
			Dir:       p.stateDir,
			Hostname:  p.hostname,
			AuthKey:   authKey,
			Ephemeral: true,
		}
		p.tsServer = srv
	}

	return &tsnetDialer{srv: p.tsServer}, nil
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
		return err
	}
	return nil
}
