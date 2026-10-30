package terminal

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strconv"
	"sync"

	"github.com/creack/pty"
	"golang.org/x/crypto/ssh"

	"air-traffic-control/backend/pkg/nodeapi"
	sshclient "air-traffic-control/backend/pkg/ssh"
)

// PTYSession abstracts an interactive terminal session (SSH or local).
type PTYSession interface {
	io.ReadWriteCloser
	Resize(cols, rows int) error
}

type localPTYSession struct {
	cmd  *exec.Cmd
	ptmx *os.File
	once sync.Once
}

func (s *localPTYSession) Read(p []byte) (int, error) {
	return s.ptmx.Read(p)
}

func (s *localPTYSession) Write(p []byte) (int, error) {
	return s.ptmx.Write(p)
}

func (s *localPTYSession) Resize(cols, rows int) error {
	if s.ptmx == nil {
		return fmt.Errorf("ptmx is nil")
	}
	return pty.Setsize(s.ptmx, &pty.Winsize{
		Rows: uint16(rows),
		Cols: uint16(cols),
	})
}

func (s *localPTYSession) Close() error {
	var err error
	s.once.Do(func() {
		if s.ptmx != nil {
			_ = s.ptmx.Close()
		}
		if s.cmd != nil && s.cmd.Process != nil {
			_ = s.cmd.Process.Kill()
			_, _ = s.cmd.Process.Wait()
		}
	})
	return err
}

type sshPTYSession struct {
	session *ssh.Session
	stdin   io.WriteCloser
	stdout  io.Reader
	once    sync.Once
}

func (s *sshPTYSession) Read(p []byte) (int, error) {
	return s.stdout.Read(p)
}

func (s *sshPTYSession) Write(p []byte) (int, error) {
	return s.stdin.Write(p)
}

func (s *sshPTYSession) Resize(cols, rows int) error {
	if s.session == nil {
		return fmt.Errorf("ssh session is nil")
	}
	return s.session.WindowChange(rows, cols)
}

func (s *sshPTYSession) Close() error {
	var err error
	s.once.Do(func() {
		if s.stdin != nil {
			_ = s.stdin.Close()
		}
		if s.session != nil {
			err = s.session.Close()
		}
	})
	return err
}

// isLocalNode checks if a node represents the local machine (localhost).
// Mirrors the logic in websocket hub for consistency.
func isLocalNode(node *nodeapi.Object) bool {
	if node == nil {
		return false
	}
	ip, _ := node.GetString("ip")
	shortName, _ := node.GetString("shortName")
	name, _ := node.GetString("name")
	return ip == "127.0.0.1" ||
		ip == "localhost" ||
		ip == "::1" ||
		shortName == "localhost" ||
		name == "localhost"
}

// OpenLocalPTY spawns a local PTY session running bash/sh.
func OpenLocalPTY(cols, rows int) (PTYSession, error) {
	log.Printf("pty: opening local PTY cols=%d rows=%d", cols, rows)
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/bash"
	}
	if _, err := exec.LookPath(shell); err != nil {
		shell = "/bin/sh"
		log.Printf("pty: bash not found, falling back to %s", shell)
	}

	cmd := exec.Command(shell)
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")

	ws := &pty.Winsize{
		Rows: uint16(rows),
		Cols: uint16(cols),
	}
	if ws.Rows == 0 {
		ws.Rows = 24
	}
	if ws.Cols == 0 {
		ws.Cols = 80
	}

	ptmx, err := pty.StartWithSize(cmd, ws)
	if err != nil {
		log.Printf("pty: failed to start pty: %v", err)
		return nil, fmt.Errorf("failed to start pty: %w", err)
	}

	log.Printf("pty: local PTY allocated successfully (fd=%d)", ptmx.Fd())
	return &localPTYSession{
		cmd:  cmd,
		ptmx: ptmx,
	}, nil
}

// OpenSSHPTY opens an interactive SSH PTY session on a remote node via SSH client pool.
func OpenSSHPTY(ctx context.Context, pool *sshclient.ClientPool, node *nodeapi.Object, cols, rows int) (PTYSession, error) {
	if node == nil {
		return nil, fmt.Errorf("node is nil")
	}

	ip, _ := node.GetString("ip")
	tailscaleHost, _ := node.GetString("tailscaleHost")
	tailscaleIp, _ := node.GetString("tailscaleIp")
	sshUser, _ := node.GetString("sshUser")
	port := 22
	if rawPort, ok := node.Get("port"); ok {
		var p int
		if err := json.Unmarshal(rawPort, &p); err == nil && p > 0 {
			port = p
		} else {
			var s string
			if err := json.Unmarshal(rawPort, &s); err == nil {
				if parsed, err := strconv.Atoi(s); err == nil && parsed > 0 {
					port = parsed
				}
			}
		}
	}

	effectiveUser := sshUser
	if effectiveUser == "" {
		effectiveUser = "core"
	}

	isLocal := isLocalNode(node)

	if isLocal || pool == nil {
		return OpenLocalPTY(cols, rows)
	}

	targetHost := tailscaleIp
	if targetHost == "" {
		targetHost = ip
	}
	if targetHost == "" {
		targetHost = tailscaleHost
	}
	if targetHost == "" {
		return nil, fmt.Errorf("no host address available for node")
	}

	vaultSecretID, _ := node.GetString("vaultSecretId")
	authMethod, _ := node.GetString("authMethod")

	cfg := sshclient.Config{
		Host:          targetHost,
		Port:          port,
		User:          effectiveUser,
		VaultSecretID: vaultSecretID,
		AuthMethod:    authMethod,
	}

	client, err := pool.GetClient(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("ssh get client failed: %w", err)
	}

	return OpenSSHPTYWithClient(client, cols, rows)
}

// OpenSSHPTYWithClient opens an interactive PTY shell over an
// already-authenticated SSH client. The caller owns session lifecycle only:
// the client itself is typically pooled by a connection plugin and must NOT
// be closed here.
func OpenSSHPTYWithClient(client *ssh.Client, cols, rows int) (PTYSession, error) {
	session, err := client.NewSession()
	if err != nil {
		return nil, fmt.Errorf("ssh new session failed: %w", err)
	}

	modes := ssh.TerminalModes{
		ssh.ECHO:          1,
		ssh.TTY_OP_ISPEED: 14400,
		ssh.TTY_OP_OSPEED: 14400,
	}

	if rows <= 0 {
		rows = 24
	}
	if cols <= 0 {
		cols = 80
	}

	if err := session.RequestPty("xterm-256color", rows, cols, modes); err != nil {
		session.Close()
		return nil, fmt.Errorf("request pty failed: %w", err)
	}

	stdin, err := session.StdinPipe()
	if err != nil {
		session.Close()
		return nil, fmt.Errorf("stdin pipe failed: %w", err)
	}

	stdout, err := session.StdoutPipe()
	if err != nil {
		session.Close()
		return nil, fmt.Errorf("stdout pipe failed: %w", err)
	}

	session.Stderr = session.Stdout

	if err := session.Shell(); err != nil {
		session.Close()
		return nil, fmt.Errorf("ssh shell failed: %w", err)
	}

	return &sshPTYSession{
		session: session,
		stdin:   stdin,
		stdout:  stdout,
	}, nil
}
