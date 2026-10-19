package ssh_test

import (
	"context"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"crypto/rand"
	"crypto/rsa"

	atc_ssh "air-traffic-control/backend/pkg/ssh"
	"air-traffic-control/backend/pkg/types"
)

func generateTestSigner(t *testing.T) ssh.Signer {
	t.Helper()
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate server private key: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(privateKey)
	if err != nil {
		t.Fatalf("failed to create signer: %v", err)
	}
	return signer
}

// mockExecSSHServer runs an in-memory SSH server capable of handling session channels, exec requests, and keepalive requests.
type mockExecSSHServer struct {
	listener     net.Listener
	serverConfig *ssh.ServerConfig
	activeSessions int32
	maxSessions    int32
	keepaliveCount int32
}

func newMockExecSSHServer(t *testing.T, maxSessions int32) *mockExecSSHServer {
	t.Helper()
	signer := generateTestSigner(t)

	s := &mockExecSSHServer{
		maxSessions: maxSessions,
	}

	config := &ssh.ServerConfig{
		NoClientAuth: true,
	}
	config.AddHostKey(signer)
	s.serverConfig = config

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	s.listener = l

	go s.serve()
	return s
}

func (s *mockExecSSHServer) Addr() (string, int) {
	host, portStr, _ := net.SplitHostPort(s.listener.Addr().String())
	var port int
	fmt.Sscanf(portStr, "%d", &port)
	return host, port
}

func (s *mockExecSSHServer) Close() error {
	return s.listener.Close()
}

func (s *mockExecSSHServer) serve() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		go s.handleConn(conn)
	}
}

func (s *mockExecSSHServer) handleConn(c net.Conn) {
	defer c.Close()
	sConn, chans, reqs, err := ssh.NewServerConn(c, s.serverConfig)
	if err != nil {
		return
	}
	defer sConn.Close()

	// Handle global connection requests like keepalive@openssh.com
	go func() {
		for req := range reqs {
			if req.Type == "keepalive@openssh.com" {
				atomic.AddInt32(&s.keepaliveCount, 1)
				if req.WantReply {
					req.Reply(true, nil)
				}
			} else if req.WantReply {
				req.Reply(false, nil)
			}
		}
	}()

	for newChannel := range chans {
		if newChannel.ChannelType() != "session" && newChannel.ChannelType() != "direct-streamlocal@openssh.com" {
			newChannel.Reject(ssh.UnknownChannelType, "unsupported channel type")
			continue
		}

		current := atomic.AddInt32(&s.activeSessions, 1)
		if s.maxSessions > 0 && current > s.maxSessions {
			atomic.AddInt32(&s.activeSessions, -1)
			newChannel.Reject(ssh.ResourceShortage, "too many concurrent sessions")
			continue
		}

		channel, requests, err := newChannel.Accept()
		if err != nil {
			atomic.AddInt32(&s.activeSessions, -1)
			continue
		}

		go func(ch ssh.Channel, inReqs <-chan *ssh.Request) {
			defer func() {
				atomic.AddInt32(&s.activeSessions, -1)
				ch.Close()
			}()

			for req := range inReqs {
				switch req.Type {
				case "exec":
					// Payload format: 4-byte uint32 length prefix followed by command string
					if len(req.Payload) >= 4 {
						cmd := string(req.Payload[4:])
						if req.WantReply {
							req.Reply(true, nil)
						}
						handleCommand(ch, cmd)
						return
					}
					if req.WantReply {
						req.Reply(false, nil)
					}
				default:
					if req.WantReply {
						req.Reply(false, nil)
					}
				}
			}
		}(channel, requests)
	}
}

func handleCommand(ch ssh.Channel, cmd string) {
	if strings.HasPrefix(cmd, "sleep ") {
		var ms int
		fmt.Sscanf(cmd, "sleep %d", &ms)
		time.Sleep(time.Duration(ms) * time.Millisecond)
		io.WriteString(ch, "slept\n")
		sendExitStatus(ch, 0)
		return
	}

	if cmd == "echo test" {
		io.WriteString(ch, "test\n")
		sendExitStatus(ch, 0)
		return
	}

	if cmd == "fail" {
		io.WriteString(ch.Stderr(), "command error\n")
		sendExitStatus(ch, 1)
		return
	}

	io.WriteString(ch, fmt.Sprintf("executed: %s\n", cmd))
	sendExitStatus(ch, 0)
}

func sendExitStatus(ch ssh.Channel, code uint32) {
	payload := []byte{
		byte(code >> 24),
		byte(code >> 16),
		byte(code >> 8),
		byte(code),
	}
	ch.SendRequest("exit-status", false, payload)
}

// 1. Test ClientPool Lazy Connection Instantiation
func TestClientPool_LazyConnection(t *testing.T) {
	server := newMockExecSSHServer(t, 10)
	defer server.Close()

	host, port := server.Addr()
	cfg := atc_ssh.Config{
		Host:     host,
		Port:     port,
		User:     "core",
		Timeout:  3 * time.Second,
		NodeType: types.NodeTypeLinuxLinux,
	}

	pool := atc_ssh.NewClientPool(atc_ssh.PoolConfig{
		MaxSessionsPerHost: 8,
		HeartbeatInterval:  1 * time.Second,
	})
	defer pool.Close()

	// Before any request, pool count should be 0
	if pool.ActiveCount() != 0 {
		t.Fatalf("expected 0 active connections initially, got %d", pool.ActiveCount())
	}

	// First command should lazily establish the connection
	ctx := context.Background()
	out, err := pool.RunCommand(ctx, cfg, "echo test")
	if err != nil {
		t.Fatalf("unexpected error on first command: %v", err)
	}
	if strings.TrimSpace(out) != "test" {
		t.Fatalf("expected 'test', got %q", out)
	}

	// Now pool count must be 1
	if pool.ActiveCount() != 1 {
		t.Fatalf("expected 1 active connection in pool, got %d", pool.ActiveCount())
	}

	// Subsequent command reuses existing client
	out2, err := pool.RunCommand(ctx, cfg, "echo test")
	if err != nil {
		t.Fatalf("unexpected error on second command: %v", err)
	}
	if strings.TrimSpace(out2) != "test" {
		t.Fatalf("expected 'test', got %q", out2)
	}
	if pool.ActiveCount() != 1 {
		t.Fatalf("expected active connection count to remain 1, got %d", pool.ActiveCount())
	}
}

// 2. Test Concurrency Limiter (8-session limit per host)
func TestClientPool_MaxSessionsLimit(t *testing.T) {
	// Server rejects >8 concurrent channels
	server := newMockExecSSHServer(t, 8)
	defer server.Close()

	host, port := server.Addr()
	cfg := atc_ssh.Config{
		Host:     host,
		Port:     port,
		User:     "core",
		Timeout:  3 * time.Second,
		NodeType: types.NodeTypeLinuxLinux,
	}

	pool := atc_ssh.NewClientPool(atc_ssh.PoolConfig{
		MaxSessionsPerHost: 8,
	})
	defer pool.Close()

	var wg sync.WaitGroup
	// Run 16 concurrent requests on the same host with a sleep
	const concurrent = 16
	errChan := make(chan error, concurrent)

	for i := 0; i < concurrent; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, err := pool.RunCommand(ctx, cfg, "sleep 50")
			if err != nil {
				errChan <- err
			}
		}()
	}

	wg.Wait()
	close(errChan)

	for err := range errChan {
		t.Errorf("concurrent command failed: %v", err)
	}
}

// 3. Test Context Cancellation on Blocked Semaphore
func TestClientPool_ConcurrencyContextCancellation(t *testing.T) {
	server := newMockExecSSHServer(t, 8)
	defer server.Close()

	host, port := server.Addr()
	cfg := atc_ssh.Config{
		Host:     host,
		Port:     port,
		User:     "core",
		Timeout:  3 * time.Second,
		NodeType: types.NodeTypeLinuxLinux,
	}

	pool := atc_ssh.NewClientPool(atc_ssh.PoolConfig{
		MaxSessionsPerHost: 2, // low limit for quick test
	})
	defer pool.Close()

	ctxBlocker, cancelBlocker := context.WithCancel(context.Background())
	defer cancelBlocker()

	// Fill both available sessions
	for i := 0; i < 2; i++ {
		go func() {
			_, _ = pool.RunCommand(ctxBlocker, cfg, "sleep 1000")
		}()
	}

	// Give goroutines time to acquire slots
	time.Sleep(50 * time.Millisecond)

	// 3rd command should block and respect context timeout
	ctxTimeout, cancelTimeout := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancelTimeout()

	_, err := pool.RunCommand(ctxTimeout, cfg, "echo test")
	if err == nil {
		t.Fatalf("expected context timeout error on 3rd command, got nil")
	}
	if !strings.Contains(err.Error(), "context deadline exceeded") {
		t.Fatalf("expected context deadline exceeded, got: %v", err)
	}
}

// 4. Test Auto-Reconnect & Stale Socket Eviction
func TestClientPool_AutoReconnectOnDisconnect(t *testing.T) {
	server := newMockExecSSHServer(t, 10)

	host, port := server.Addr()
	cfg := atc_ssh.Config{
		Host:     host,
		Port:     port,
		User:     "core",
		Timeout:  3 * time.Second,
		NodeType: types.NodeTypeLinuxLinux,
	}

	pool := atc_ssh.NewClientPool(atc_ssh.PoolConfig{
		MaxSessionsPerHost: 8,
		HeartbeatInterval:  500 * time.Millisecond,
	})
	defer pool.Close()

	ctx := context.Background()
	out, err := pool.RunCommand(ctx, cfg, "echo test")
	if err != nil || strings.TrimSpace(out) != "test" {
		t.Fatalf("initial run failed: %v", err)
	}

	// Kill server to simulate connection drop / host restart
	server.Close()

	// Create a new server on the same host/port after slight delay
	time.Sleep(50 * time.Millisecond)
	newServer := newMockExecSSHServerAt(t, host, port)
	defer newServer.Close()

	// Next command must detect EOF / stale connection, evict, and automatically reconnect once
	out2, err := pool.RunCommand(ctx, cfg, "echo test")
	if err != nil {
		t.Fatalf("expected auto-reconnect to succeed, got error: %v", err)
	}
	if strings.TrimSpace(out2) != "test" {
		t.Fatalf("expected 'test', got %q", out2)
	}
}

// 5. Test Background Keepalive and Eviction on Failed Heartbeat
func TestClientPool_KeepaliveHeartbeat(t *testing.T) {
	server := newMockExecSSHServer(t, 10)
	defer server.Close()

	host, port := server.Addr()
	cfg := atc_ssh.Config{
		Host:     host,
		Port:     port,
		User:     "core",
		Timeout:  3 * time.Second,
		NodeType: types.NodeTypeLinuxLinux,
	}

	pool := atc_ssh.NewClientPool(atc_ssh.PoolConfig{
		MaxSessionsPerHost: 8,
		HeartbeatInterval:  100 * time.Millisecond,
	})
	defer pool.Close()

	ctx := context.Background()
	_, err := pool.RunCommand(ctx, cfg, "echo test")
	if err != nil {
		t.Fatalf("failed command: %v", err)
	}

	// Wait for several keep-alives to trigger
	time.Sleep(350 * time.Millisecond)

	count := atomic.LoadInt32(&server.keepaliveCount)
	if count < 2 {
		t.Fatalf("expected at least 2 keepalive requests, got %d", count)
	}
}

// 6. Test DialContext on ClientPool (e.g. for remote UNIX domain sockets or TCP forwarding)
func TestClientPool_DialContext(t *testing.T) {
	server := newMockExecSSHServer(t, 10)
	defer server.Close()

	host, port := server.Addr()
	cfg := atc_ssh.Config{
		Host:     host,
		Port:     port,
		User:     "core",
		Timeout:  3 * time.Second,
		NodeType: types.NodeTypeLinuxLinux,
	}

	pool := atc_ssh.NewClientPool(atc_ssh.PoolConfig{
		MaxSessionsPerHost: 8,
	})
	defer pool.Close()

	ctx := context.Background()
	// Dialing through SSH client
	conn, err := pool.DialContext(ctx, cfg, "tcp", fmt.Sprintf("127.0.0.1:%d", port))
	// In mock server direct-tcpip channel is handled or rejected cleanly
	if err != nil && !strings.Contains(err.Error(), "channel") && !strings.Contains(err.Error(), "rejected") && !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("unexpected dial error: %v", err)
	}
	if conn != nil {
		conn.Close()
	}
}

func newMockExecSSHServerAt(t *testing.T, host string, port int) *mockExecSSHServer {
	t.Helper()
	signer := generateTestSigner(t)

	s := &mockExecSSHServer{
		maxSessions: 10,
	}

	config := &ssh.ServerConfig{
		NoClientAuth: true,
	}
	config.AddHostKey(signer)
	s.serverConfig = config

	addr := net.JoinHostPort(host, fmt.Sprintf("%d", port))
	l, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("failed to listen on %s: %v", addr, err)
	}
	s.listener = l

	go s.serve()
	return s
}
