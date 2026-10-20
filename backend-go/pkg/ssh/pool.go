package ssh

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

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

// poolKey constructs a unique cache key for a host/port/user credential.
func poolKey(cfg Config) string {
	port := cfg.Port
	if port == 0 {
		port = 22
	}
	user := cfg.User
	if user == "" {
		user = "core"
	}
	return fmt.Sprintf("%s@%s:%d", user, cfg.Host, port)
}

// GetClient retrieves or lazily establishes a pooled SSH client.
func (p *ClientPool) GetClient(ctx context.Context, cfg Config) (*ssh.Client, error) {
	p.mu.RLock()
	if p.closed {
		p.mu.RUnlock()
		return nil, errors.New("ssh client pool is closed")
	}
	key := poolKey(cfg)
	pc, ok := p.clients[key]
	p.mu.RUnlock()

	if ok && !pc.isClosed() {
		return pc.client, nil
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return nil, errors.New("ssh client pool is closed")
	}

	// Double-check under write lock
	if pc, ok = p.clients[key]; ok && !pc.isClosed() {
		return pc.client, nil
	}

	// Evict any stale client
	if ok {
		pc.close()
		delete(p.clients, key)
	}

	// Lazily dial fresh client
	client, err := dialSSH(cfg)
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

	return client, nil
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

func isConnectionDead(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
		return true
	}
	str := err.Error()
	return str == "EOF" ||
		errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, io.ErrClosedPipe)
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
		key := poolKey(cfg)
		p.Evict(key)
		return p.runCommandOnce(ctx, cfg, command)
	}
	return out, err
}

func (p *ClientPool) runCommandOnce(ctx context.Context, cfg Config, command string) (string, error) {
	key := poolKey(cfg)

	// Retrieve pooled entry
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return "", errors.New("ssh client pool is closed")
	}
	pc, ok := p.clients[key]
	if !ok || pc.isClosed() {
		p.mu.Unlock()
		_, err := p.GetClient(ctx, cfg)
		if err != nil {
			return "", err
		}
		p.mu.RLock()
		pc = p.clients[key]
		p.mu.RUnlock()
	} else {
		p.mu.Unlock()
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

// dialSSH builds the client config and dials TCP.
func dialSSH(cfg Config) (*ssh.Client, error) {
	port := cfg.Port
	if port == 0 {
		port = 22
	}
	user := cfg.User
	if user == "" {
		user = "core"
	}

	authMethods := []ssh.AuthMethod{}
	if agentAuth, _ := getSSHAgentAuthMethod(); agentAuth != nil {
		authMethods = append(authMethods, agentAuth)
	}
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
		}
	}
	if cfg.Password != "" {
		authMethods = append(authMethods, ssh.Password(cfg.Password))
	}

	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 8 * time.Second
	}

	sshConfig := &ssh.ClientConfig{
		User:            user,
		Auth:            authMethods,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         timeout,
	}

	addr := net.JoinHostPort(cfg.Host, fmt.Sprintf("%d", port))
	return ssh.Dial("tcp", addr, sshConfig)
}
