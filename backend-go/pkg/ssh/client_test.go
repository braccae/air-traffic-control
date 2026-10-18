package ssh

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/crypto/ssh/agent"
)

func TestResolveSSHAgentAuthMethod_ConnectsToAgentSocket(t *testing.T) {
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "ssh-agent.socket")

	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("failed to listen on temp unix socket: %v", err)
	}
	defer ln.Close()

	keyring := agent.NewKeyring()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go agent.ServeAgent(keyring, conn)
		}
	}()

	t.Setenv("SSH_AUTH_SOCK", sockPath)

	resolved := resolveAgentSocketPath()
	if resolved != sockPath {
		t.Fatalf("expected resolved socket %q, got %q", sockPath, resolved)
	}

	authMethod, closer := getSSHAgentAuthMethod()
	if closer != nil {
		defer closer.Close()
	}
	if authMethod == nil {
		t.Fatalf("expected non-nil ssh.AuthMethod from ssh-agent socket %s", sockPath)
	}
}

func TestResolveSSHAgentSocketPath_FallbackToRunUserSocket(t *testing.T) {
	t.Setenv("SSH_AUTH_SOCK", "")
	uid := os.Getuid()
	expected := fmt.Sprintf("/run/user/%d/ssh-agent.socket", uid)
	if _, err := os.Stat(expected); err != nil {
		t.Skipf("no live %s on this test runner", expected)
	}

	resolved := resolveAgentSocketPath()
	if resolved != expected {
		t.Fatalf("expected fallback socket %q, got %q", expected, resolved)
	}
}
