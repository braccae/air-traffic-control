package ssh

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"fmt"
	"net"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// mockSSHServer runs an in-memory SSH server for testing auth methods
type mockSSHServer struct {
	listener       net.Listener
	serverConfig   *ssh.ServerConfig
	authLog        []string
	allowNone      bool
	allowPublicKey bool
	serverKey      ssh.Signer
}

func newMockSSHServer(t *testing.T, allowNone, allowPublicKey bool) *mockSSHServer {
	t.Helper()
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate server private key: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(privateKey)
	if err != nil {
		t.Fatalf("failed to create signer: %v", err)
	}

	m := &mockSSHServer{
		allowNone:      allowNone,
		allowPublicKey: allowPublicKey,
		serverKey:      signer,
		authLog:        make([]string, 0),
	}

	config := &ssh.ServerConfig{
		NoClientAuth: allowNone,
	}
	if allowPublicKey {
		config.PublicKeyCallback = func(conn ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			m.authLog = append(m.authLog, fmt.Sprintf("publickey:%s", conn.User()))
			return nil, nil
		}
	}
	config.AddHostKey(signer)
	m.serverConfig = config

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen on 127.0.0.1: %v", err)
	}
	m.listener = l

	go m.serve()
	return m
}

func (m *mockSSHServer) Addr() string {
	return m.listener.Addr().String()
}

func (m *mockSSHServer) Close() error {
	return m.listener.Close()
}

func (m *mockSSHServer) serve() {
	for {
		conn, err := m.listener.Accept()
		if err != nil {
			return
		}
		go func(c net.Conn) {
			defer c.Close()
			sConn, chans, reqs, err := ssh.NewServerConn(c, m.serverConfig)
			if err != nil {
				return
			}
			defer sConn.Close()
			go ssh.DiscardRequests(reqs)
			for newChannel := range chans {
				channel, reqs, err := newChannel.Accept()
				if err != nil {
					continue
				}
				go ssh.DiscardRequests(reqs)
				channel.Close()
			}
		}(conn)
	}
}

// TestSSHAuth_NoneAuthVsPublicKeyFallback verifies client authentication negotiation:
// 1. If server allows NoClientAuth (simulating Tailscale SSH node with none-auth), client succeeds without keys.
// 2. If server rejects none-auth, client successfully falls back to public key auth without hanging or crashing.
func TestSSHAuth_NoneAuthVsPublicKeyFallback(t *testing.T) {
	// Generate a test client key
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate client key: %v", err)
	}
	clientSigner, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatalf("failed to create client signer: %v", err)
	}

	t.Run("ServerAllowsNoneAuth_TailscaleSSHBehavior", func(t *testing.T) {
		server := newMockSSHServer(t, true, false)
		defer server.Close()

		authOrder := []ssh.AuthMethod{
			ssh.PublicKeys(clientSigner),
		}

		clientConfig := &ssh.ClientConfig{
			User:            "root",
			Auth:            authOrder,
			HostKeyCallback: ssh.InsecureIgnoreHostKey(),
			Timeout:         2 * time.Second,
		}

		conn, err := net.Dial("tcp", server.Addr())
		if err != nil {
			t.Fatalf("failed to dial server: %v", err)
		}
		defer conn.Close()

		c, chans, reqs, err := ssh.NewClientConn(conn, server.Addr(), clientConfig)
		if err != nil {
			t.Fatalf("SSH handshake failed on none-auth server: %v", err)
		}
		sshClient := ssh.NewClient(c, chans, reqs)
		defer sshClient.Close()
	})

	t.Run("ServerRequiresPublicKey_FallbackBehavior", func(t *testing.T) {
		server := newMockSSHServer(t, false, true)
		defer server.Close()

		clientConfig := &ssh.ClientConfig{
			User:            "core",
			Auth:            []ssh.AuthMethod{ssh.PublicKeys(clientSigner)},
			HostKeyCallback: ssh.InsecureIgnoreHostKey(),
			Timeout:         2 * time.Second,
		}

		conn, err := net.Dial("tcp", server.Addr())
		if err != nil {
			t.Fatalf("failed to dial server: %v", err)
		}
		defer conn.Close()

		c, chans, reqs, err := ssh.NewClientConn(conn, server.Addr(), clientConfig)
		if err != nil {
			t.Fatalf("SSH handshake with public key auth failed: %v", err)
		}
		sshClient := ssh.NewClient(c, chans, reqs)
		defer sshClient.Close()

		if len(server.authLog) == 0 {
			t.Fatalf("expected server authLog to record publickey auth")
		}
	})

	t.Run("ServerRejectsAll_GracefulFailureWithoutPanic", func(t *testing.T) {
		server := newMockSSHServer(t, false, false)
		defer server.Close()

		clientConfig := &ssh.ClientConfig{
			User:            "core",
			Auth:            []ssh.AuthMethod{ssh.PublicKeys(clientSigner)},
			HostKeyCallback: ssh.InsecureIgnoreHostKey(),
			Timeout:         1 * time.Second,
		}

		conn, err := net.Dial("tcp", server.Addr())
		if err != nil {
			t.Fatalf("failed to dial server: %v", err)
		}
		defer conn.Close()

		_, _, _, err = ssh.NewClientConn(conn, server.Addr(), clientConfig)
		if err == nil {
			t.Fatalf("expected handshake error on unauthenticated server, got nil")
		}
	})
}

// TestTsnetDialerInterface_MockAndIntegration verifies tsnet dialer semantics
func TestTsnetDialerInterface_Contract(t *testing.T) {
	// DialContext / Dial interface compatibility
	type tsDialer interface {
		Dial(ctx context.Context, network, address string) (net.Conn, error)
	}

	// Verify custom dialer can pipe to ssh.NewClientConn
	server := newMockSSHServer(t, true, false)
	defer server.Close()

	dialerFunc := func(ctx context.Context, network, address string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, network, address)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	rawConn, err := dialerFunc(ctx, "tcp", server.Addr())
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer rawConn.Close()

	clientConfig := &ssh.ClientConfig{
		User:            "test-user",
		Auth:            []ssh.AuthMethod{},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         2 * time.Second,
	}

	c, chans, reqs, err := ssh.NewClientConn(rawConn, server.Addr(), clientConfig)
	if err != nil {
		t.Fatalf("handshake over dialer failed: %v", err)
	}
	client := ssh.NewClient(c, chans, reqs)
	client.Close()
}
