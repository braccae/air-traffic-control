package proxy

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"strings"

	"air-traffic-control/backend/pkg/plugin"
	tsplugin "air-traffic-control/backend/plugins/connection/tailscale"
)

// ForwardStreams dials target host:port via dialer and proxies stdin and stdout bidirectionally.
func ForwardStreams(ctx context.Context, dialer plugin.Dialer, host, port string, r io.Reader, w io.Writer) error {
	targetAddr := net.JoinHostPort(host, port)
	conn, err := dialer.Dial(ctx, "tcp", targetAddr)
	if err != nil {
		return fmt.Errorf("failed to dial target %s: %w", targetAddr, err)
	}
	defer conn.Close()

	errChan := make(chan error, 2)

	// Stream stdin -> conn
	go func() {
		_, err := io.Copy(conn, r)
		errChan <- err
	}()

	// Stream conn -> stdout
	go func() {
		_, err := io.Copy(w, conn)
		errChan <- err
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-errChan:
		if err == io.EOF {
			return nil
		}
		return err
	}
}

// Run executes the proxy command with CLI arguments and connects os.Stdin and os.Stdout to target.
func Run(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: proxy <host> <port>")
	}
	host := args[0]
	port := args[1]

	// Determine if target is tailscale or standard ssh
	var dialer plugin.Dialer

	// Check if registered plugins provide dialer
	if p, ok := plugin.Get("connection/tailscale"); ok {
		if d, ok := p.(plugin.Dialer); ok && p.Compatible(plugin.HostFacts{HostRoute: host}) {
			dialer = d
		}
	}

	if dialer == nil {
		// Fallback to tailscale plugin instance if it matches tailnet or explicitly requested
		if strings.HasSuffix(host, ".ts.net") || strings.HasPrefix(host, "100.") {
			dialer = tsplugin.New()
		} else if p, ok := plugin.Get("connection/ssh"); ok {
			if d, ok := p.(plugin.Dialer); ok {
				dialer = d
			}
		}
	}

	if dialer == nil {
		// Default standard network dialer
		dialer = &stdDialer{}
	}

	return ForwardStreams(context.Background(), dialer, host, port, os.Stdin, os.Stdout)
}

type stdDialer struct{}

func (s *stdDialer) Dial(ctx context.Context, network, address string) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, network, address)
}
