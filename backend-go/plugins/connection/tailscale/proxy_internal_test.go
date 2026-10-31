package tailscale

import (
	"bufio"
	"context"
	"encoding/binary"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

type directDialer struct{}

func (directDialer) Dial(ctx context.Context, network, address string) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, network, address)
}

func startEchoServer(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("echo listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				_, _ = io.Copy(c, c)
			}(conn)
		}
	}()
	return ln.Addr().String()
}

func socks5Connect(t *testing.T, proxyAddr, target string) net.Conn {
	t.Helper()
	conn, err := net.Dial("tcp", proxyAddr)
	if err != nil {
		t.Fatalf("dial socks proxy: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	if _, err := conn.Write([]byte{5, 1, 0}); err != nil {
		t.Fatalf("socks greeting: %v", err)
	}
	reply := make([]byte, 2)
	if _, err := io.ReadFull(conn, reply); err != nil {
		t.Fatalf("socks greeting reply: %v", err)
	}
	if reply[0] != 5 || reply[1] != 0 {
		t.Fatalf("socks greeting reply = %v, want no-auth", reply)
	}

	host, portStr, err := net.SplitHostPort(target)
	if err != nil {
		t.Fatalf("split target: %v", err)
	}
	ip := net.ParseIP(host).To4()
	if ip == nil {
		t.Fatalf("target %q is not IPv4", target)
	}
	port, _ := strconv.Atoi(portStr)
	req := []byte{5, 1, 0, 1, ip[0], ip[1], ip[2], ip[3], 0, 0}
	binary.BigEndian.PutUint16(req[8:], uint16(port))
	if _, err := conn.Write(req); err != nil {
		t.Fatalf("socks connect: %v", err)
	}
	head := make([]byte, 4)
	if _, err := io.ReadFull(conn, head); err != nil {
		t.Fatalf("socks connect reply: %v", err)
	}
	if head[1] != 0 {
		t.Fatalf("socks connect status = %d, want success", head[1])
	}
	// Consume BND.ADDR/BND.PORT for the three possible address types.
	switch head[3] {
	case 1:
		_, _ = io.ReadFull(conn, make([]byte, 4+2))
	case 4:
		_, _ = io.ReadFull(conn, make([]byte, 16+2))
	case 3:
		l := make([]byte, 1)
		_, _ = io.ReadFull(conn, l)
		_, _ = io.ReadFull(conn, make([]byte, int(l[0])+2))
	}
	return conn
}

func TestProxyModeAutoFollowsCapability(t *testing.T) {
	orig := socketMarkAvailable
	t.Cleanup(func() { socketMarkAvailable = orig })

	socketMarkAvailable = func() bool { return true }
	if socks, httpProxy := (tsnetSettings{ProxyMode: "auto"}).proxyEnabled(); socks || httpProxy {
		t.Fatal("auto enabled proxies while SO_MARK is available")
	}

	socketMarkAvailable = func() bool { return false }
	socks, httpProxy := (tsnetSettings{ProxyMode: "auto"}).proxyEnabled()
	if !socks || !httpProxy {
		t.Fatal("auto did not enable both proxies without SO_MARK")
	}

	if socks, httpProxy := (tsnetSettings{ProxyMode: "off"}).proxyEnabled(); socks || httpProxy {
		t.Fatal("off mode enabled a proxy")
	}
	if socks, httpProxy := (tsnetSettings{ProxyMode: "socks"}).proxyEnabled(); !socks || httpProxy {
		t.Fatal("socks mode must enable only SOCKS5")
	}
}

func TestProxyListenDefaultsFollowContainer(t *testing.T) {
	orig := inContainer
	t.Cleanup(func() { inContainer = orig })

	inContainer = func() bool { return false }
	cfg := tsnetSettings{}
	if got := cfg.resolveProxyListen("", DefaultSocksProxyPort); got != "127.0.0.1:1055" {
		t.Fatalf("host SOCKS bind = %q", got)
	}
	if got := cfg.resolveProxyListen("", DefaultHTTPProxyPort); got != "127.0.0.1:1056" {
		t.Fatalf("host HTTP bind = %q", got)
	}

	inContainer = func() bool { return true }
	if got := cfg.resolveProxyListen("", DefaultSocksProxyPort); got != "0.0.0.0:1055" {
		t.Fatalf("container SOCKS bind = %q", got)
	}
	if got := cfg.resolveProxyListen("", DefaultHTTPProxyPort); got != "0.0.0.0:1056" {
		t.Fatalf("container HTTP bind = %q", got)
	}
	if got := cfg.resolveProxyListen("10.0.0.5:9000", DefaultSocksProxyPort); got != "10.0.0.5:9000" {
		t.Fatalf("explicit bind was not honoured: %q", got)
	}
}

func TestLoopbackProxiesNeedNoCredentials(t *testing.T) {
	echoAddr := startEchoServer(t)
	p := New()
	cfg := tsnetSettings{
		ProxyMode:        "both",
		ProxySocksListen: "127.0.0.1:0",
		ProxyHTTPListen:  "127.0.0.1:0",
		StateDir:         filepath.Join(t.TempDir(), "tsnet"),
	}
	p.mu.Lock()
	p.ensureProxiesLocked(cfg, directDialer{})
	p.mu.Unlock()
	t.Cleanup(func() {
		p.mu.Lock()
		p.closeProxiesLocked()
		p.mu.Unlock()
	})

	st := p.proxyStatus()
	if st.Mode != "both" || st.Socks == "" || st.HTTP == "" {
		t.Fatalf("proxy status = %+v", st)
	}
	if st.Authenticated {
		t.Fatal("loopback proxy unexpectedly requires credentials")
	}

	conn := socks5Connect(t, st.Socks, echoAddr)
	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatalf("socks write: %v", err)
	}
	got := make([]byte, 4)
	if _, err := io.ReadFull(conn, got); err != nil {
		t.Fatalf("socks read: %v", err)
	}
	if string(got) != "ping" {
		t.Fatalf("socks echo = %q", got)
	}

	httpConn, err := net.Dial("tcp", st.HTTP)
	if err != nil {
		t.Fatalf("dial http proxy: %v", err)
	}
	defer httpConn.Close()
	if _, err := io.WriteString(httpConn, "CONNECT "+echoAddr+" HTTP/1.1\r\nHost: "+echoAddr+"\r\n\r\n"); err != nil {
		t.Fatalf("http connect request: %v", err)
	}
	reader := bufio.NewReader(httpConn)
	statusLine, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("http connect response: %v", err)
	}
	if !strings.Contains(statusLine, "200") {
		t.Fatalf("http CONNECT status = %q", strings.TrimSpace(statusLine))
	}
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("http headers: %v", err)
		}
		if line == "\r\n" {
			break
		}
	}
	if _, err := httpConn.Write([]byte("pong")); err != nil {
		t.Fatalf("http tunnel write: %v", err)
	}
	got = make([]byte, 4)
	if _, err := io.ReadFull(reader, got); err != nil {
		t.Fatalf("http tunnel read: %v", err)
	}
	if string(got) != "pong" {
		t.Fatalf("http echo = %q", got)
	}
}

func TestExposedProxyGeneratesCredentialsFile(t *testing.T) {
	orig := inContainer
	inContainer = func() bool { return false }
	t.Cleanup(func() { inContainer = orig })

	stateDir := filepath.Join(t.TempDir(), "tsnet")
	p := New()
	cfg := tsnetSettings{
		ProxyMode:        "socks",
		ProxySocksListen: "0.0.0.0:0",
		StateDir:         stateDir,
	}
	p.mu.Lock()
	p.ensureProxiesLocked(cfg, directDialer{})
	p.mu.Unlock()
	t.Cleanup(func() {
		p.mu.Lock()
		p.closeProxiesLocked()
		p.mu.Unlock()
	})

	st := p.proxyStatus()
	if !st.Authenticated {
		t.Fatal("exposed proxy did not enable authentication")
	}
	if st.CredentialsFile == "" {
		t.Fatal("exposed proxy did not report a credential file")
	}
	info, err := os.Stat(st.CredentialsFile)
	if err != nil {
		t.Fatalf("credentials file: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("credentials mode = %o, want 600", info.Mode().Perm())
	}
}
