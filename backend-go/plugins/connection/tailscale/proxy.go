package tailscale

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"tailscale.com/net/socks5"

	"air-traffic-control/backend/pkg/plugin"
)

// Userspace proxy defaults. They mirror Tailscale's own userspace-networking
// ports so tooling and docs line up.
const (
	DefaultSocksProxyPort = 1055
	DefaultHTTPProxyPort  = 1056
)

// inContainer is a test seam for the container probe.
var inContainer = runningInContainer

// runningInContainer reports whether ATC is running inside a container, where
// a proxy listener has to reach sibling containers rather than just the host.
func runningInContainer() bool {
	if strings.TrimSpace(os.Getenv("container")) != "" {
		return true
	}
	if strings.TrimSpace(os.Getenv("KUBERNETES_SERVICE_HOST")) != "" {
		return true
	}
	for _, marker := range []string{"/.dockerenv", "/run/.containerenv"} {
		if _, err := os.Stat(marker); err == nil {
			return true
		}
	}
	if raw, err := os.ReadFile("/proc/1/cgroup"); err == nil {
		cgroup := string(raw)
		for _, marker := range []string{"docker", "kubepods", "libpod", "containerd", "lxc"} {
			if strings.Contains(cgroup, marker) {
				return true
			}
		}
	}
	return false
}

// normalizeProxyMode maps operator input to one of auto/socks/http/both/off.
// Unknown values fall back to auto rather than guessing a listener.
func normalizeProxyMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "off", "none", "disabled":
		return "off"
	case "socks", "socks5":
		return "socks"
	case "http", "https":
		return "http"
	case "both", "all", "on", "enabled":
		return "both"
	default:
		return "auto"
	}
}

// proxyEnabled resolves the effective listeners. "auto" turns the proxies on
// when the daemon cannot set SO_MARK: that is exactly the unprivileged,
// userspace-only deployment, where ATC cannot offer kernel-style tailnet
// routing but can still hand a SOCKS5/HTTP proxy to sibling services.
func (c tsnetSettings) proxyEnabled() (socks, httpProxy bool) {
	switch normalizeProxyMode(c.ProxyMode) {
	case "off":
		return false, false
	case "socks":
		return true, false
	case "http":
		return false, true
	case "both":
		return true, true
	default:
		if socketMarkAvailable() {
			return false, false
		}
		return true, true
	}
}

// resolveProxyListen prefers an explicit address, else loopback for a normal
// host and 0.0.0.0 inside a container.
func (c tsnetSettings) resolveProxyListen(configured string, port int) string {
	if addr := strings.TrimSpace(configured); addr != "" {
		return addr
	}
	host := "127.0.0.1"
	if inContainer() {
		host = "0.0.0.0"
	}
	return net.JoinHostPort(host, strconv.Itoa(port))
}

// listenExposesBeyondLoopback reports whether an address is reachable from
// outside this host, which is when the proxy must require credentials.
func listenExposesBeyondLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return true
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		return true
	}
	if strings.EqualFold(host, "localhost") {
		return false
	}
	ip := net.ParseIP(host)
	return ip == nil || !ip.IsLoopback()
}

type proxyCredentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// proxyCredentialsPath keeps generated credentials next to the tsnet state
// (data/proxy-credentials.json) so a restart reuses them.
func proxyCredentialsPath(stateDir string) string {
	stateDir = strings.TrimSpace(stateDir)
	if stateDir == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(stateDir), "proxy-credentials.json")
}

// resolveProxyCredentials returns operator-configured credentials, or
// generates and persists a pair when the proxy is exposed beyond loopback.
// An exposed listener never starts unauthenticated: a failure here is fatal
// for the proxy (it fails closed) rather than silently open.
func resolveProxyCredentials(cfg tsnetSettings, exposed bool) (*proxyCredentials, string, error) {
	if cfg.ProxyUsername != "" || cfg.ProxyPassword != "" {
		return &proxyCredentials{Username: cfg.ProxyUsername, Password: cfg.ProxyPassword}, "", nil
	}
	if !exposed {
		return nil, "", nil
	}
	path := proxyCredentialsPath(cfg.StateDir)
	if path != "" {
		if raw, err := os.ReadFile(path); err == nil {
			var stored proxyCredentials
			if err := json.Unmarshal(raw, &stored); err == nil && stored.Username != "" && stored.Password != "" {
				return &stored, path, nil
			}
		}
	}
	creds, err := newProxyCredentials()
	if err != nil {
		return nil, path, err
	}
	if path == "" {
		// No state dir: credentials live for this process only.
		return creds, "", nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, path, err
	}
	raw, err := json.Marshal(creds)
	if err != nil {
		return nil, path, err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return nil, path, err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return nil, path, err
	}
	return creds, path, nil
}

func newProxyCredentials() (*proxyCredentials, error) {
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return nil, fmt.Errorf("generate proxy credentials: %w", err)
	}
	return &proxyCredentials{
		Username: "atc-" + hex.EncodeToString(buf[:4]),
		Password: hex.EncodeToString(buf[4:]),
	}, nil
}

// ProxyStatus is the live, non-secret view of the userspace proxy listeners.
type ProxyStatus struct {
	Mode            string `json:"mode"` // off | socks | http | both
	Socks           string `json:"socks,omitempty"`
	HTTP            string `json:"http,omitempty"`
	Authenticated   bool   `json:"authenticated"`
	CredentialsFile string `json:"credentialsFile,omitempty"`
	Error           string `json:"error,omitempty"`
}

// ensureProxiesLocked reconciles the listeners with the resolved settings. The
// caller must hold p.mu. Listeners are bound to the active dialer (tsnet or the
// borrowed host socket), so a transport switch restarts them.
func (p *Plugin) ensureProxiesLocked(cfg tsnetSettings, dialer plugin.Dialer) {
	socksOn, httpOn := cfg.proxyEnabled()
	mode := "off"
	switch {
	case socksOn && httpOn:
		mode = "both"
	case socksOn:
		mode = "socks"
	case httpOn:
		mode = "http"
	}

	socksAddr, httpAddr := "", ""
	if socksOn {
		socksAddr = cfg.resolveProxyListen(cfg.ProxySocksListen, DefaultSocksProxyPort)
	}
	if httpOn {
		httpAddr = cfg.resolveProxyListen(cfg.ProxyHTTPListen, DefaultHTTPProxyPort)
	}

	exposed := (socksOn && listenExposesBeyondLoopback(socksAddr)) ||
		(httpOn && listenExposesBeyondLoopback(httpAddr))

	creds, credsPath, credErr := resolveProxyCredentials(cfg, exposed)
	fp := strings.Join([]string{mode, socksAddr, httpAddr, credentialFingerprint(creds)}, "\x00")
	if fp == p.proxyFP {
		return
	}

	p.closeProxiesLocked()
	p.proxyMode = mode
	p.proxyFP = fp
	if mode == "off" {
		return
	}
	if credErr != nil {
		p.proxyErr = fmt.Sprintf("proxy credentials unavailable: %v", credErr)
		log.Printf("tailscale proxy: %s", p.proxyErr)
		return
	}
	if creds != nil {
		p.proxyAuth = true
		p.proxyCredsPath = credsPath
	}

	if socksOn {
		ln, err := net.Listen("tcp", socksAddr)
		if err != nil {
			p.proxyErr = fmt.Sprintf("SOCKS5 listener %s: %v", socksAddr, err)
			log.Printf("tailscale proxy: %s", p.proxyErr)
		} else {
			p.proxySocksLn = ln
			p.proxySocksAddr = ln.Addr().String()
			server := &socks5.Server{
				Logf: func(format string, args ...any) {
					log.Printf("tailscale socks5: "+format, args...)
				},
				Dialer: dialer.Dial,
			}
			if creds != nil {
				server.Username = creds.Username
				server.Password = creds.Password
			}
			go func() {
				if err := server.Serve(ln); err != nil && !errors.Is(err, net.ErrClosed) {
					log.Printf("tailscale socks5: serve %s: %v", ln.Addr(), err)
				}
			}()
			log.Printf("tailscale socks5 proxy listening on %s", ln.Addr())
		}
	}

	if httpOn {
		ln, err := net.Listen("tcp", httpAddr)
		if err != nil {
			if p.proxyErr == "" {
				p.proxyErr = fmt.Sprintf("HTTP proxy listener %s: %v", httpAddr, err)
			}
			log.Printf("tailscale proxy: HTTP listener %s: %v", httpAddr, err)
		} else {
			srv := &http.Server{
				Handler:           newHTTPProxyHandler(dialer.Dial, creds),
				ReadHeaderTimeout: 15 * time.Second,
			}
			p.proxyHTTPLn = ln
			p.proxyHTTPsrv = srv
			p.proxyHTTPAddr = ln.Addr().String()
			go func() {
				if err := srv.Serve(ln); err != nil &&
					!errors.Is(err, http.ErrServerClosed) &&
					!errors.Is(err, net.ErrClosed) {
					log.Printf("tailscale http proxy: serve %s: %v", ln.Addr(), err)
				}
			}()
			log.Printf("tailscale http proxy listening on %s", ln.Addr())
		}
	}
}

// closeProxiesLocked stops every listener and clears the cached fingerprint so
// the next reconcile rebinds against the current dialer. Caller holds p.mu.
func (p *Plugin) closeProxiesLocked() {
	if p.proxyHTTPsrv != nil {
		_ = p.proxyHTTPsrv.Close()
		p.proxyHTTPsrv = nil
	} else if p.proxyHTTPLn != nil {
		_ = p.proxyHTTPLn.Close()
	}
	p.proxyHTTPLn = nil
	if p.proxySocksLn != nil {
		_ = p.proxySocksLn.Close()
	}
	p.proxySocksLn = nil
	p.proxyMode = "off"
	p.proxySocksAddr = ""
	p.proxyHTTPAddr = ""
	p.proxyAuth = false
	p.proxyCredsPath = ""
	p.proxyErr = ""
	p.proxyFP = ""
}

// proxyStatus snapshots the listeners for SelfStatus without leaking secrets.
func (p *Plugin) proxyStatus() ProxyStatus {
	p.mu.Lock()
	defer p.mu.Unlock()
	return ProxyStatus{
		Mode:            p.proxyMode,
		Socks:           p.proxySocksAddr,
		HTTP:            p.proxyHTTPAddr,
		Authenticated:   p.proxyAuth,
		CredentialsFile: p.proxyCredsPath,
		Error:           p.proxyErr,
	}
}

func credentialFingerprint(creds *proxyCredentials) string {
	if creds == nil {
		return ""
	}
	return creds.Username + ":" + creds.Password
}

// httpProxyHandler forwards plain HTTP and tunnels CONNECT requests through a
// dialer (the tsnet node or the borrowed host socket).
type httpProxyHandler struct {
	user, pass string
	reverse    *httputil.ReverseProxy
}

func newHTTPProxyHandler(dial func(context.Context, string, string) (net.Conn, error), creds *proxyCredentials) *httpProxyHandler {
	h := &httpProxyHandler{}
	if creds != nil {
		h.user, h.pass = creds.Username, creds.Password
	}
	h.reverse = &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			if req.URL.Scheme == "" {
				req.URL.Scheme = "http"
			}
			if req.URL.Host == "" {
				req.URL.Host = req.Host
			}
			req.Header.Del("Proxy-Connection")
		},
		Transport: &http.Transport{
			Proxy:              nil,
			DialContext:        dial,
			ForceAttemptHTTP2:  false,
			MaxIdleConns:       16,
			IdleConnTimeout:    30 * time.Second,
			DisableCompression: true,
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			http.Error(w, err.Error(), http.StatusBadGateway)
		},
	}
	return h
}

func (h *httpProxyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if (h.user != "" || h.pass != "") && !h.authorized(r) {
		w.Header().Set("Proxy-Authenticate", `Basic realm="Air Traffic Control"`)
		http.Error(w, "proxy authentication required", http.StatusProxyAuthRequired)
		return
	}
	if r.Method == http.MethodConnect {
		h.connect(w, r)
		return
	}
	h.reverse.ServeHTTP(w, r)
}

func (h *httpProxyHandler) authorized(r *http.Request) bool {
	user, pass, ok := r.BasicAuth()
	if !ok {
		return false
	}
	userOK := subtle.ConstantTimeCompare([]byte(user), []byte(h.user)) == 1
	passOK := subtle.ConstantTimeCompare([]byte(pass), []byte(h.pass)) == 1
	return userOK && passOK
}

func (h *httpProxyHandler) connect(w http.ResponseWriter, r *http.Request) {
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "connection hijacking unsupported", http.StatusInternalServerError)
		return
	}
	client, buffered, err := hijacker.Hijack()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer client.Close()

	target := r.Host
	if _, _, err := net.SplitHostPort(target); err != nil {
		target = net.JoinHostPort(target, "443")
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	upstream, err := h.reverse.Transport.(*http.Transport).DialContext(ctx, "tcp", target)
	if err != nil {
		_, _ = fmt.Fprintf(client, "HTTP/1.1 502 Bad Gateway\r\nContent-Type: text/plain\r\n\r\n%s", err)
		return
	}
	defer upstream.Close()

	if _, err := fmt.Fprint(client, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		return
	}
	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(upstream, buffered)
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(client, upstream)
		done <- struct{}{}
	}()
	<-done
}
