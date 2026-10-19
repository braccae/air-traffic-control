package ssh_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"air-traffic-control/backend/pkg/plugin"
	sshplugin "air-traffic-control/backend/plugins/connection/ssh"
)

// startMockSSHServer starts an in-memory SSH server for testing connection plugin.
func startMockSSHServer(t *testing.T, noClientAuth bool) (string, func()) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate RSA key: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatalf("failed to create signer: %v", err)
	}

	srvConfig := &ssh.ServerConfig{
		NoClientAuth: noClientAuth,
	}
	if !noClientAuth {
		srvConfig.PasswordCallback = func(c ssh.ConnMetadata, pass []byte) (*ssh.Permissions, error) {
			if c.User() == "testuser" && string(pass) == "secret" {
				return nil, nil
			}
			return nil, fmt.Errorf("invalid password")
		}
	}
	srvConfig.AddHostKey(signer)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}

	stopCh := make(chan struct{})
	go func() {
		for {
			c, err := listener.Accept()
			if err != nil {
				select {
				case <-stopCh:
					return
				default:
					return
				}
			}
			go func(conn net.Conn) {
				defer conn.Close()
				sConn, chans, reqs, err := ssh.NewServerConn(conn, srvConfig)
				if err != nil {
					return
				}
				defer sConn.Close()
				go ssh.DiscardRequests(reqs)
				for ch := range chans {
					channel, reqs, err := ch.Accept()
					if err != nil {
						continue
					}
					go func() {
						for r := range reqs {
							if r.Type == "exec" {
								cmd := string(r.Payload[4:]) // 4 bytes uint32 length prefix
								r.Reply(true, nil)
								if strings.Contains(cmd, "echo") {
									channel.Write([]byte("echo-output\n"))
								} else {
									channel.Write([]byte("mock-output\n"))
								}
								channel.SendRequest("exit-status", false, []byte{0, 0, 0, 0})
								channel.Close()
								return
							}
							r.Reply(false, nil)
						}
					}()
				}
			}(c)
		}
	}()

	addr := listener.Addr().String()
	cleanup := func() {
		close(stopCh)
		listener.Close()
	}
	return addr, cleanup
}

func TestSSHPlugin_MetaAndCompatibility(t *testing.T) {
	p := sshplugin.New()

	meta := p.Meta()
	if meta.ID != "connection/ssh" {
		t.Errorf("expected ID 'connection/ssh', got %q", meta.ID)
	}
	if meta.Kind != plugin.KindConnection {
		t.Errorf("expected Kind 'connection', got %q", meta.Kind)
	}

	// Should be compatible with linux, freebsd, etc. when ActiveConnection matches or not restricted
	linuxFacts := plugin.HostFacts{OSFamily: "linux", HostRoute: "192.168.1.100"}
	if !p.Compatible(linuxFacts) {
		t.Errorf("expected SSH plugin to be compatible with linux facts")
	}

	// Incompatible if ActiveConnection explicitly set to something else e.g. "connection/winrm"
	winrmFacts := plugin.HostFacts{OSFamily: "windows", ActiveConnection: "connection/winrm"}
	if p.Compatible(winrmFacts) {
		t.Errorf("expected SSH plugin to be incompatible when ActiveConnection is connection/winrm")
	}
}

func TestSSHPlugin_ProbeAndRunCommand(t *testing.T) {
	addr, cleanup := startMockSSHServer(t, false)
	defer cleanup()

	host, portStr, _ := net.SplitHostPort(addr)
	p := sshplugin.New()

	facts := plugin.HostFacts{
		NodeID:    "test-node",
		OSFamily:  "linux",
		HostRoute: host,
		Overrides: map[string]string{
			"port":     portStr,
			"user":     "testuser",
			"password": "secret",
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Direct runner retrieval or through probe
	runner, err := p.NewRunner(facts)
	if err != nil {
		t.Fatalf("failed to create runner: %v", err)
	}

	out, err := runner.RunCommand(ctx, "echo hello")
	if err != nil {
		t.Fatalf("RunCommand failed: %v", err)
	}
	if !strings.Contains(out, "echo-output") {
		t.Errorf("expected 'echo-output', got %q", out)
	}

	// Probe
	detection, err := p.Probe(ctx, runner, facts)
	if err != nil {
		t.Fatalf("Probe failed: %v", err)
	}
	if !detection.Detected {
		t.Errorf("expected probe to detect SSH connection, got %+v", detection)
	}
}

func TestSSHPlugin_Dialer(t *testing.T) {
	addr, cleanup := startMockSSHServer(t, true)
	defer cleanup()

	p := sshplugin.New()
	dialer, ok := any(p).(plugin.Dialer)
	if !ok {
		t.Fatalf("expected SSH plugin to implement plugin.Dialer")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := dialer.Dial(ctx, "tcp", addr)
	if err != nil {
		t.Fatalf("Dial failed: %v", err)
	}
	defer conn.Close()

	if conn.RemoteAddr().String() != addr {
		t.Errorf("expected remote addr %q, got %q", addr, conn.RemoteAddr().String())
	}
}
