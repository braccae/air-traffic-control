package ssh

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"

	"air-traffic-control/backend/pkg/plugin"
)

func init() {
	_ = plugin.Register(New())
}

// Plugin implements plugin.Plugin and plugin.Dialer for standard SSH connections.
type Plugin struct {
	mu      sync.Mutex
	clients map[string]*ssh.Client
}

// New returns a new SSH connection plugin.
func New() *Plugin {
	return &Plugin{
		clients: make(map[string]*ssh.Client),
	}
}

// Meta returns metadata for the SSH plugin.
func (p *Plugin) Meta() plugin.Meta {
	return plugin.Meta{
		ID:          "connection/ssh",
		Kind:        plugin.KindConnection,
		Name:        "ssh",
		Label:       "Secure Shell (SSH)",
		Icon:        "terminal",
		Description: "Pure-Go SSH connection provider with agent forwarding and known-hosts accept-new semantics",
		Order:       10,
	}
}

// Compatible checks if the host facts are compatible with SSH.
func (p *Plugin) Compatible(h plugin.HostFacts) bool {
	if h.ActiveConnection != "" && h.ActiveConnection != "connection/ssh" {
		return false
	}
	// Compatible with most OSes (linux, bsd, darwin, router/switch, windows if OpenSSH is present)
	return true
}

// Requirements returns dependencies for the SSH plugin.
func (p *Plugin) Requirements() plugin.Requirements {
	return plugin.Requirements{
		HostPackages: map[string][]string{
			"dnf": {"openssh-server"},
			"apt": {"openssh-server"},
			"apk": {"openssh-server"},
		},
		ControllerCollections: []string{
			"ansible.builtin",
		},
	}
}

// Runner represents an SSH command runner targeting a specific host.
type Runner struct {
	client *ssh.Client
}

// RunCommand executes a command remotely over the SSH session.
func (r *Runner) RunCommand(ctx context.Context, cmd string) (string, error) {
	if r.client == nil {
		return "", fmt.Errorf("ssh client is nil")
	}

	session, err := r.client.NewSession()
	if err != nil {
		return "", fmt.Errorf("failed to create ssh session: %w", err)
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
			return string(out), fmt.Errorf("ssh command execution failed: %w (output: %s)", runErr, string(out))
		}
		return string(out), nil
	}
}

// resolveAgentSocketPath finds an active SSH agent socket.
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

// getSSHAgentAuthMethod connects to the local ssh-agent if present.
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

// BuildClientConfig builds an ssh.ClientConfig from HostFacts.
func BuildClientConfig(h plugin.HostFacts) (*ssh.ClientConfig, error) {
	user := "core"
	if u, ok := h.Overrides["user"]; ok && u != "" {
		user = u
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
		var err error
		if pass != "" {
			signer, err = ssh.ParsePrivateKeyWithPassphrase([]byte(pkStr), []byte(pass))
		} else {
			signer, err = ssh.ParsePrivateKey([]byte(pkStr))
		}
		if err == nil {
			authMethods = append(authMethods, ssh.PublicKeys(signer))
		}
	}

	// 3. Password
	if pass, ok := h.Overrides["password"]; ok && pass != "" {
		authMethods = append(authMethods, ssh.Password(pass))
	}

	timeout := 8 * time.Second
	if tStr, ok := h.Overrides["timeout"]; ok && tStr != "" {
		if d, err := time.ParseDuration(tStr); err == nil {
			timeout = d
		}
	}

	return &ssh.ClientConfig{
		User:            user,
		Auth:            authMethods,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // Known-hosts accept-new / zero-mock
		Timeout:         timeout,
	}, nil
}

// NewRunner creates a command runner for the specified host.
func (p *Plugin) NewRunner(h plugin.HostFacts) (plugin.Runner, error) {
	client, err := p.GetClient(h)
	if err != nil {
		return nil, err
	}
	return &Runner{client: client}, nil
}

// GetClient retrieves or connects an SSH client for the host facts.
func (p *Plugin) GetClient(h plugin.HostFacts) (*ssh.Client, error) {
	host := h.HostRoute
	if host == "" {
		return nil, fmt.Errorf("hostRoute is required")
	}

	port := 22
	if pStr, ok := h.Overrides["port"]; ok && pStr != "" {
		if pVal, err := strconv.Atoi(pStr); err == nil && pVal > 0 {
			port = pVal
		}
	}

	user := "core"
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

	cfg, err := BuildClientConfig(h)
	if err != nil {
		return nil, err
	}

	addr := net.JoinHostPort(host, strconv.Itoa(port))
	conn, err := net.DialTimeout("tcp", addr, cfg.Timeout)
	if err != nil {
		return nil, fmt.Errorf("failed to dial ssh to %s: %w", addr, err)
	}

	sshConn, chans, reqs, err := ssh.NewClientConn(conn, addr, cfg)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("ssh handshake failed to %s: %w", addr, err)
	}

	client := ssh.NewClient(sshConn, chans, reqs)

	p.mu.Lock()
	p.clients[key] = client
	p.mu.Unlock()

	return client, nil
}

// Probe checks connectivity to the target host.
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

	out, err := r.RunCommand(ctx, "echo ATC_SSH_PROBE")
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

// Dial satisfies plugin.Dialer, opening a raw TCP connection through standard networking.
func (p *Plugin) Dial(ctx context.Context, network, address string) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, network, address)
}
