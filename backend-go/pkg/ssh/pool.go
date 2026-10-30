package ssh

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/crypto/ssh"

	"air-traffic-control/backend/pkg/secrets"
	"air-traffic-control/backend/pkg/types"
)

// Default limits and intervals
const (
	DefaultMaxSessionsPerHost = 8
	DefaultHeartbeatInterval  = 25 * time.Second
	DefaultHeartbeatTimeout   = 5 * time.Second
	MaxHeartbeatFailures      = 3
)

// PoolConfig specifies parameters for managing pooled persistent SSH connections.
type PoolConfig struct {
	MaxSessionsPerHost int
	HeartbeatInterval  time.Duration
	HeartbeatTimeout   time.Duration
}

// pooledClient tracks a persistent SSH client, its keepalive lifecycle, and session concurrency.
type pooledClient struct {
	client     *ssh.Client
	config     Config
	key        string
	semaphore  chan struct{}
	cancelKeep context.CancelFunc
	closed     bool
	mu         sync.Mutex
}

// ClientPool manages thread-safe, long-lived, multiplexed SSH connections.
type ClientPool struct {
	mu      sync.RWMutex
	clients map[string]*pooledClient
	cfg     PoolConfig
	closed  bool
}

// NewClientPool instantiates a persistent SSH client pool.
func NewClientPool(cfg PoolConfig) *ClientPool {
	if cfg.MaxSessionsPerHost <= 0 {
		cfg.MaxSessionsPerHost = DefaultMaxSessionsPerHost
	}
	if cfg.HeartbeatInterval <= 0 {
		cfg.HeartbeatInterval = DefaultHeartbeatInterval
	}
	if cfg.HeartbeatTimeout <= 0 {
		cfg.HeartbeatTimeout = DefaultHeartbeatTimeout
	}

	return &ClientPool{
		clients: make(map[string]*pooledClient),
		cfg:     cfg,
	}
}

// ActiveCount returns the number of active client connections in the pool.
func (p *ClientPool) ActiveCount() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.clients)
}

// poolKey constructs a unique cache key for a host/port/user credential
// identity. The credential fingerprint is part of the key so rotating a vault
// secret never reuses a connection authenticated with the previous value.
func poolKey(cfg Config, credential string) string {
	port := cfg.Port
	if port == 0 {
		port = 22
	}
	user := cfg.User
	if user == "" {
		user = "core"
	}
	return fmt.Sprintf("%s@%s:%d#%s", user, cfg.Host, port, credential)
}

// resolvedSecret resolves cfg.VaultSecretID through the process-wide vault
// resolver. A nil result means the config references no vault secret.
func resolvedSecret(ctx context.Context, cfg Config) (*secrets.Secret, error) {
	id := strings.TrimSpace(cfg.VaultSecretID)
	if id == "" {
		return nil, nil
	}
	sec, err := secrets.Resolve(ctx, id)
	if err != nil {
		return nil, err
	}
	return &sec, nil
}

// credentialFingerprint is a non-reversible identity for the credential used by
// a connection. It keeps plaintext out of pool keys and logs.
func credentialFingerprint(cfg Config, sec *secrets.Secret) string {
	if sec != nil {
		sum := sha256.Sum256([]byte(sec.Value))
		return hex.EncodeToString(sum[:8])
	}
	if len(cfg.PrivateKey) > 0 {
		sum := sha256.Sum256(cfg.PrivateKey)
		return "key-" + hex.EncodeToString(sum[:8])
	}
	if cfg.Password != "" {
		sum := sha256.Sum256([]byte(cfg.Password))
		return "pw-" + hex.EncodeToString(sum[:8])
	}
	return "agent"
}

// connFor returns a live pooled client for cfg, dialing on demand.
func (p *ClientPool) connFor(ctx context.Context, cfg Config) (*pooledClient, error) {
	sec, err := resolvedSecret(ctx, cfg)
	if err != nil {
		return nil, err
	}
	key := poolKey(cfg, credentialFingerprint(cfg, sec))

	p.mu.RLock()
	if p.closed {
		p.mu.RUnlock()
		return nil, errors.New("ssh client pool is closed")
	}
	pc, ok := p.clients[key]
	p.mu.RUnlock()

	if ok && !pc.isClosed() {
		return pc, nil
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return nil, errors.New("ssh client pool is closed")
	}

	// Double-check under write lock
	if pc, ok = p.clients[key]; ok && !pc.isClosed() {
		return pc, nil
	}

	// Evict any stale client
	if ok {
		pc.close()
		delete(p.clients, key)
	}

	clientCfg, methods, err := buildClientConfig(cfg, sec)
	if err != nil {
		return nil, err
	}
	client, err := dialSSH(cfg, clientCfg, methods)
	if err != nil {
		return nil, err
	}

	keepCtx, cancelKeep := context.WithCancel(context.Background())
	newPC := &pooledClient{
		client:     client,
		config:     cfg,
		key:        key,
		semaphore:  make(chan struct{}, p.cfg.MaxSessionsPerHost),
		cancelKeep: cancelKeep,
	}

	p.clients[key] = newPC

	// Start background keepalive routine
	go p.runKeepalive(keepCtx, newPC)

	return newPC, nil
}

// GetClient retrieves or lazily establishes a pooled SSH client.
func (p *ClientPool) GetClient(ctx context.Context, cfg Config) (*ssh.Client, error) {
	pc, err := p.connFor(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return pc.client, nil
}

func (pc *pooledClient) isClosed() bool {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	return pc.closed
}

func (pc *pooledClient) close() {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	if pc.closed {
		return
	}
	pc.closed = true
	if pc.cancelKeep != nil {
		pc.cancelKeep()
	}
	if pc.client != nil {
		_ = pc.client.Close()
	}
}

// Evict explicitly evicts and closes a client from the pool.
func (p *ClientPool) Evict(key string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if pc, ok := p.clients[key]; ok {
		pc.close()
		delete(p.clients, key)
	}
}

// runKeepalive sends periodic OpenSSH keepalive packets.
func (p *ClientPool) runKeepalive(ctx context.Context, pc *pooledClient) {
	ticker := time.NewTicker(p.cfg.HeartbeatInterval)
	defer ticker.Stop()

	consecutiveFailures := 0

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if pc.isClosed() {
				return
			}

			// Send keepalive@openssh.com
			_, _, err := pc.client.SendRequest("keepalive@openssh.com", true, nil)
			if err != nil {
				consecutiveFailures++
				if consecutiveFailures >= MaxHeartbeatFailures || isConnectionDead(err) {
					p.Evict(pc.key)
					return
				}
			} else {
				consecutiveFailures = 0
			}
		}
	}
}

// transientTransportMarkers are netstack / OS errors that mean the pooled
// connection is gone but a fresh dial may succeed. Tailscale's gVisor netstack
// reports aborted TCP flows as "operation aborted" (tcpip.ErrAborted), which
// happens whenever ATC's tsnet node is restarted under an open session.
func isConnectionDead(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) ||
		errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.ErrClosedPipe) ||
		errors.Is(err, syscall.ECONNABORTED) || errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.EPIPE) || errors.Is(err, syscall.ECONNREFUSED) {
		return true
	}
	str := strings.ToLower(err.Error())
	if str == "eof" {
		return true
	}
	for _, marker := range []string{
		"operation aborted",
		"connection was refused",
		"connection refused",
		"connection reset by peer",
		"broken pipe",
		"use of closed network connection",
		"unexpected eof",
		"closed pipe",
	} {
		if strings.Contains(str, marker) {
			return true
		}
	}
	return false
}

// acquireSession acquires a slot from the per-host semaphore, bounded by MaxSessionsPerHost.
func (p *ClientPool) acquireSession(ctx context.Context, pc *pooledClient) error {
	select {
	case pc.semaphore <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *ClientPool) releaseSession(pc *pooledClient) {
	select {
	case <-pc.semaphore:
	default:
	}
}

// RunCommand executes a command remotely over a multiplexed channel session.
// Automatically retries once if a stale connection / EOF is detected.
func (p *ClientPool) RunCommand(ctx context.Context, cfg Config, command string) (string, error) {
	out, err := p.runCommandOnce(ctx, cfg, command)
	if err != nil && isConnectionDead(err) {
		// Evict dead client and retry once
		p.evictConfig(ctx, cfg)
		return p.runCommandOnce(ctx, cfg, command)
	}
	return out, err
}

// evictConfig drops the pooled connection bound to cfg's credential identity.
func (p *ClientPool) evictConfig(ctx context.Context, cfg Config) {
	sec, err := resolvedSecret(ctx, cfg)
	if err != nil {
		return
	}
	p.Evict(poolKey(cfg, credentialFingerprint(cfg, sec)))
}

func (p *ClientPool) runCommandOnce(ctx context.Context, cfg Config, command string) (string, error) {
	pc, err := p.connFor(ctx, cfg)
	if err != nil {
		return "", err
	}

	if pc == nil || pc.client == nil {
		return "", errors.New("failed to acquire ssh client from pool")
	}

	// Guard concurrency with per-host semaphore
	if err := p.acquireSession(ctx, pc); err != nil {
		return "", err
	}
	defer p.releaseSession(pc)

	session, err := pc.client.NewSession()
	if err != nil {
		return "", err
	}
	defer session.Close()

	formattedCmd := command
	if cfg.NodeType == types.NodeTypeWindows {
		formattedCmd = fmt.Sprintf("powershell.exe -NoProfile -NonInteractive -Command \"%s\"", command)
	}

	done := make(chan struct{})
	var stdout, stderr bytes.Buffer
	session.Stdout = &stdout
	session.Stderr = &stderr
	var runErr error

	go func() {
		runErr = session.Run(formattedCmd)
		close(done)
	}()

	select {
	case <-ctx.Done():
		_ = session.Signal(ssh.SIGKILL)
		return "", ctx.Err()
	case <-done:
		if runErr != nil {
			combined := stdout.String()
			if stderr.Len() > 0 {
				if combined != "" && !strings.HasSuffix(combined, "\n") {
					combined += "\n"
				}
				combined += stderr.String()
			}
			return combined, fmt.Errorf("ssh command execution failed: %w (output: %s)", runErr, combined)
		}
		return stdout.String(), nil
	}
}

// DialContext establishes a direct UNIX domain socket or network connection through the pooled SSH client.
func (p *ClientPool) DialContext(ctx context.Context, cfg Config, network, addr string) (net.Conn, error) {
	client, err := p.GetClient(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return client.Dial(network, addr)
}

// Close gracefully closes all pooled SSH connections.
func (p *ClientPool) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil
	}
	p.closed = true
	for _, pc := range p.clients {
		pc.close()
	}
	p.clients = make(map[string]*pooledClient)
	return nil
}

// buildClientConfig assembles SSH auth methods from a resolved vault secret,
// literal credentials, and the local ssh-agent (in that order).
func buildClientConfig(cfg Config, sec *secrets.Secret) (*ssh.ClientConfig, []string, error) {
	port := cfg.Port
	if port == 0 {
		port = 22
	}
	user := cfg.User
	if user == "" {
		user = "core"
	}

	authMethods := []ssh.AuthMethod{}
	methods := []string{}

	if sec != nil {
		switch sec.Kind {
		case secrets.KindPrivateKey:
			var signer ssh.Signer
			var err error
			if cfg.Passphrase != "" {
				signer, err = ssh.ParsePrivateKeyWithPassphrase([]byte(sec.Value), []byte(cfg.Passphrase))
			} else {
				signer, err = ssh.ParsePrivateKey([]byte(sec.Value))
			}
			if err != nil {
				return nil, nil, fmt.Errorf("vault secret %q is not a usable SSH private key: %w", sec.Name, err)
			}
			authMethods = append(authMethods, ssh.PublicKeys(signer))
			methods = append(methods, "vault-private-key:"+sec.Name)
		case secrets.KindPassword:
			authMethods = append(authMethods, ssh.Password(sec.Value))
			methods = append(methods, "vault-password:"+sec.Name)
		}
	} else {
		if len(cfg.PrivateKey) > 0 {
			var signer ssh.Signer
			var err error
			if cfg.Passphrase != "" {
				signer, err = ssh.ParsePrivateKeyWithPassphrase(cfg.PrivateKey, []byte(cfg.Passphrase))
			} else {
				signer, err = ssh.ParsePrivateKey(cfg.PrivateKey)
			}
			if err == nil {
				authMethods = append(authMethods, ssh.PublicKeys(signer))
				methods = append(methods, "private-key")
			}
		}
		if cfg.Password != "" {
			authMethods = append(authMethods, ssh.Password(cfg.Password))
			methods = append(methods, "password")
		}
	}

	if agentAuth, _ := getSSHAgentAuthMethod(); agentAuth != nil {
		authMethods = append(authMethods, agentAuth)
		methods = append(methods, "ssh-agent")
	}

	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 8 * time.Second
	}

	return &ssh.ClientConfig{
		User:            user,
		Auth:            authMethods,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         timeout,
	}, methods, nil
}

// OfferAuthMethods reports the authentication methods a connection attempt
// would offer, and validates that any referenced vault secret resolves. It does
// not open a connection, so it is safe to call from a dry-run/test endpoint.
func OfferAuthMethods(cfg Config) ([]string, error) {
	sec, err := resolvedSecret(context.Background(), cfg)
	if err != nil {
		return nil, err
	}
	_, methods, err := buildClientConfig(cfg, sec)
	if err != nil {
		return nil, err
	}
	return methods, nil
}

// dialSSH dials TCP and completes the SSH handshake. When cfg.Dialer is set the
// TCP hop is carried by that transport (tsnet mesh) instead of the host stack.
func dialSSH(cfg Config, clientCfg *ssh.ClientConfig, methods []string) (*ssh.Client, error) {
	port := cfg.Port
	if port == 0 {
		port = 22
	}
	addr := net.JoinHostPort(cfg.Host, fmt.Sprintf("%d", port))

	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 8 * time.Second
	}

	dial := func() (net.Conn, error) {
		if cfg.Dialer != nil {
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			return cfg.Dialer(ctx, "tcp", addr)
		}
		return net.DialTimeout("tcp", addr, timeout)
	}

	conn, err := dial()
	if err == nil {
		_ = conn.SetDeadline(time.Now().Add(timeout))
		clientConn, chans, reqs, handshakeErr := ssh.NewClientConn(conn, addr, clientCfg)
		if handshakeErr != nil {
			_ = conn.Close()
			err = handshakeErr
		} else {
			_ = conn.SetDeadline(time.Time{})
			return ssh.NewClient(clientConn, chans, reqs), nil
		}
	}

	if len(methods) == 0 {
		return nil, fmt.Errorf(
			"ssh handshake failed to %s: %w (no authentication method available: no ssh-agent socket and no vaultSecretId configured)",
			addr, err,
		)
	}
	return nil, fmt.Errorf("ssh handshake failed to %s using %s: %w", addr, strings.Join(methods, ", "), err)
}
