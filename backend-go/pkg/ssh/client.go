package ssh

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
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
	pool *ClientPool
}

// NewClientManager initializes a thread-safe connection pool
func NewClientManager() *ClientManager {
	return &ClientManager{
		pool: NewClientPool(PoolConfig{
			MaxSessionsPerHost: DefaultMaxSessionsPerHost,
			HeartbeatInterval:  DefaultHeartbeatInterval,
		}),
	}
}

// Pool returns the underlying ClientPool.
func (m *ClientManager) Pool() *ClientPool {
	return m.pool
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
	return m.pool.GetClient(context.Background(), cfg)
}

// RunCommand executes a command remotely and returns output using channel multiplexing
func (m *ClientManager) RunCommand(cfg Config, command string) (string, error) {
	return m.pool.RunCommand(context.Background(), cfg, command)
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
