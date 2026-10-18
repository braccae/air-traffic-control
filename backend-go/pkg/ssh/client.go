package ssh

import (
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
	"prox-orch/backend/pkg/types"
)

// Config represents credentials for SSH connections
type Config struct {
	Host       string
	Port       int
	User       string
	Password   string
	PrivateKey []byte
	Passphrase string
	NodeType   types.NodeType
	Timeout    time.Duration
}

// ClientManager handles pooling and lifecycle of SSH sessions
type ClientManager struct {
	mu      sync.RWMutex
	clients map[string]*ssh.Client
}

// NewClientManager initializes a thread-safe connection pool
func NewClientManager() *ClientManager {
	return &ClientManager{
		clients: make(map[string]*ssh.Client),
	}
}

// GetClient retrieves or dials an SSH client
func (m *ClientManager) GetClient(cfg Config) (*ssh.Client, error) {
	key := fmt.Sprintf("%s@%s:%d", cfg.User, cfg.Host, cfg.Port)

	m.mu.RLock()
	if client, ok := m.clients[key]; ok {
		m.mu.RUnlock()
		return client, nil
	}
	m.mu.RUnlock()

	m.mu.Lock()
	defer m.mu.Unlock()

	// Double-check under write lock
	if client, ok := m.clients[key]; ok {
		return client, nil
	}

	authMethods := []ssh.AuthMethod{}
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

	sshConfig := &ssh.ClientConfig{
		User:            cfg.User,
		Auth:            authMethods,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // Configurable in production
		Timeout:         cfg.Timeout,
	}

	addr := net.JoinHostPort(cfg.Host, fmt.Sprintf("%d", cfg.Port))
	client, err := ssh.Dial("tcp", addr, sshConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to dial ssh to %s: %w", addr, err)
	}

	m.clients[key] = client
	return client, nil
}

// RunCommand executes a command remotely and returns output
func (m *ClientManager) RunCommand(cfg Config, command string) (string, error) {
	client, err := m.GetClient(cfg)
	if err != nil {
		return "", err
	}

	session, err := client.NewSession()
	if err != nil {
		return "", err
	}
	defer session.Close()

	// Adapt command execution depending on node type
	formattedCmd := command
	if cfg.NodeType == types.NodeTypeWindows {
		// Target powershell
		formattedCmd = fmt.Sprintf("powershell.exe -NoProfile -NonInteractive -Command \"%s\"", command)
	}

	out, err := session.CombinedOutput(formattedCmd)
	if err != nil {
		return string(out), fmt.Errorf("ssh command execution failed: %w (output: %s)", err, string(out))
	}
	return string(out), nil
}

// OpenPTY opens an interactive pseudo-terminal for web sockets / xterm.js
func (m *ClientManager) OpenPTY(cfg Config, termType string, rows, cols int, stdin io.Reader, stdout io.Writer) error {
	client, err := m.GetClient(cfg)
	if err != nil {
		return err
	}

	session, err := client.NewSession()
	if err != nil {
		return err
	}
	defer session.Close()

	modes := ssh.TerminalModes{
		ssh.ECHO:          1,
		ssh.TTY_OP_ISPEED: 14400,
		ssh.TTY_OP_OSPEED: 14400,
	}

	if err := session.RequestPty(termType, rows, cols, modes); err != nil {
		return fmt.Errorf("request for pseudo terminal failed: %w", err)
	}

	session.Stdin = stdin
	session.Stdout = stdout
	session.Stderr = stdout

	// For Linux: bash or cockpit-bridge session
	// For Windows: powershell or cmd
	// For Switch: default shell
	shell := "/bin/bash"
	if cfg.NodeType == types.NodeTypeWindows {
		shell = "powershell.exe"
	} else if cfg.NodeType == types.NodeTypeRouterSwitch {
		shell = "" // default cli shell
	}

	if shell != "" {
		if err := session.Start(shell); err != nil {
			return err
		}
	} else {
		if err := session.Shell(); err != nil {
			return err
		}
	}

	return session.Wait()
}
