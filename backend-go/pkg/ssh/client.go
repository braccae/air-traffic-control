package ssh

import (
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"air-traffic-control/backend/pkg/types"
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

// GetClient retrieves or dials an SSH client
func (m *ClientManager) GetClient(cfg Config) (*ssh.Client, error) {
	port := cfg.Port
	if port == 0 {
		port = 22
	}
	user := cfg.User
	if user == "" {
		user = "core"
	}
	key := fmt.Sprintf("%s@%s:%d", user, cfg.Host, port)

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
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // Configurable in production
		Timeout:         timeout,
	}

	addr := net.JoinHostPort(cfg.Host, fmt.Sprintf("%d", port))
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
