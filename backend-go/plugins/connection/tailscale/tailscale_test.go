package tailscale_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"net"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"air-traffic-control/backend/pkg/plugin"
	tsplugin "air-traffic-control/backend/plugins/connection/tailscale"
)

// startMockTailscaleSSHServer starts an in-memory SSH server with NoClientAuth simulating Tailscale SSH
func startMockTailscaleSSHServer(t *testing.T) (string, func()) {
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
		NoClientAuth: true,
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
								r.Reply(true, nil)
								channel.Write([]byte("tailscale-ssh-success\n"))
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

type mockDialer struct {
	dialFn func(ctx context.Context, network, address string) (net.Conn, error)
}

func (m *mockDialer) Dial(ctx context.Context, network, address string) (net.Conn, error) {
	if m.dialFn != nil {
		return m.dialFn(ctx, network, address)
	}
	var d net.Dialer
	return d.DialContext(ctx, network, address)
}

func TestTailscalePlugin_MetaAndCompatibility(t *testing.T) {
	p := tsplugin.New()
	meta := p.Meta()

	if meta.ID != "connection/tailscale" {
		t.Errorf("expected ID 'connection/tailscale', got %q", meta.ID)
	}
	if meta.Kind != plugin.KindConnection {
		t.Errorf("expected Kind 'connection', got %q", meta.Kind)
	}

	// Compatible with 100.x.y.z addresses or .ts.net hostnames
	if !p.Compatible(plugin.HostFacts{HostRoute: "100.105.71.49"}) {
		t.Errorf("expected compatibility with 100.x.y.z Tailscale IP")
	}
	if !p.Compatible(plugin.HostFacts{HostRoute: "likes-it-cold.leo-banjo.ts.net"}) {
		t.Errorf("expected compatibility with .ts.net domain")
	}
	if !p.Compatible(plugin.HostFacts{ActiveConnection: "connection/tailscale"}) {
		t.Errorf("expected compatibility with activeConnection connection/tailscale")
	}
	if p.Compatible(plugin.HostFacts{HostRoute: "192.168.1.100"}) {
		t.Errorf("expected incompatible with non-Tailscale host by default")
	}
}

func TestTailscalePlugin_RunWithMockDialer(t *testing.T) {
	addr, cleanup := startMockTailscaleSSHServer(t)
	defer cleanup()

	host, portStr, _ := net.SplitHostPort(addr)
	p := tsplugin.New()
	p.SetDialer(&mockDialer{}) // uses standard dialer to reach 127.0.0.1 mock

	facts := plugin.HostFacts{
		NodeID:           "ts-test-node",
		OSFamily:         "linux",
		HostRoute:        host,
		ActiveConnection: "connection/tailscale",
		Overrides: map[string]string{
			"port": portStr,
			"user": "root",
		},
	}

	runner, err := p.NewRunner(facts)
	if err != nil {
		t.Fatalf("failed to create runner: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	out, err := runner.RunCommand(ctx, "echo test")
	if err != nil {
		t.Fatalf("RunCommand failed: %v", err)
	}
	if !strings.Contains(out, "tailscale-ssh-success") {
		t.Errorf("expected 'tailscale-ssh-success', got %q", out)
	}

	detection, err := p.Probe(ctx, runner, facts)
	if err != nil {
		t.Fatalf("Probe failed: %v", err)
	}
	if !detection.Detected || detection.Status != "ONLINE" {
		t.Errorf("expected probe ONLINE, got %+v", detection)
	}
}
