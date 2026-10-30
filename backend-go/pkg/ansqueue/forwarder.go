package ansqueue

import (
	"context"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"air-traffic-control/backend/pkg/plugin"
)

// loopbackForwarder exposes a target that is only reachable through a plugin
// transport (ATC's tsnet node) on 127.0.0.1, so an external process —
// ansible-playbook — can connect over plain TCP without needing a host-level
// tailscaled route or MagicDNS resolution.
type loopbackForwarder struct {
	listener net.Listener
	port     int
	target   string

	closeOnce sync.Once
	wg        sync.WaitGroup
}

// startLoopbackForwarder opens an ephemeral loopback listener that forwards
// every accepted connection to target via dialer.
func startLoopbackForwarder(dialer plugin.Dialer, target string) (*loopbackForwarder, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	addr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		_ = listener.Close()
		return nil, fmt.Errorf("ansqueue: unexpected listener address %s", listener.Addr())
	}

	f := &loopbackForwarder{listener: listener, port: addr.Port, target: target}
	f.wg.Add(1)
	go f.serve(dialer)
	return f, nil
}

func (f *loopbackForwarder) serve(dialer plugin.Dialer) {
	defer f.wg.Done()
	for {
		conn, err := f.listener.Accept()
		if err != nil {
			return
		}
		f.wg.Add(1)
		go func() {
			defer f.wg.Done()
			defer conn.Close()
			dialCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			upstream, dialErr := dialer.Dial(dialCtx, "tcp", f.target)
			cancel()
			if dialErr != nil {
				return
			}
			defer upstream.Close()

			done := make(chan struct{}, 2)
			go func() {
				_, _ = io.Copy(upstream, conn)
				done <- struct{}{}
			}()
			go func() {
				_, _ = io.Copy(conn, upstream)
				done <- struct{}{}
			}()
			<-done
		}()
	}
}

func (f *loopbackForwarder) Close() {
	f.closeOnce.Do(func() {
		_ = f.listener.Close()
		f.wg.Wait()
	})
}

// isTailnetAddress reports whether host is a Tailscale CGNAT address
// (100.64.0.0/10) or a MagicDNS name.
func isTailnetAddress(host string) bool {
	host = strings.TrimSpace(host)
	if host == "" {
		return false
	}
	if strings.HasSuffix(host, ".ts.net") {
		return true
	}
	parts := strings.Split(host, ".")
	if len(parts) != 4 || parts[0] != "100" {
		return false
	}
	second, err := strconv.Atoi(parts[1])
	if err != nil {
		return false
	}
	return second >= 64 && second <= 127
}
