package plugin

import (
	"fmt"
	"strings"

	"golang.org/x/crypto/ssh"
)

// SSHClientProvider is implemented by connection plugins that can hand out a
// fully-authenticated interactive SSH client. Interactive PTY sessions need
// raw session channels that the Runner interface deliberately does not
// expose, so the terminal layer asks the connection plugin for a client
// instead of re-dialing over bare TCP.
//
// The returned client is owned by the plugin's internal pool: callers MUST
// NOT close it. Close only the sessions created from it.
type SSHClientProvider interface {
	GetClient(h HostFacts) (*ssh.Client, error)
}

// ResolveSSHClient selects the connection plugin for the given host facts and
// returns an authenticated SSH client dialed over that plugin's transport
// (tsnet mesh, plain TCP, ...). Selection mirrors DefaultRunnerResolver so an
// interactive session takes exactly the same route as a command runner: a
// tailnet address goes over ATC's own tsnet node, never over the host's bare
// TCP stack.
func ResolveSSHClient(facts HostFacts) (*ssh.Client, error) {
	connPluginID := facts.ActiveConnection
	if connPluginID == "" {
		route := strings.TrimSpace(facts.HostRoute)
		switch {
		case route == "" || route == "127.0.0.1" || route == "localhost" || route == "::1":
			return nil, fmt.Errorf("no ssh transport for local route %q", route)
		case strings.Contains(route, ".ts.net") || strings.HasPrefix(route, "100."):
			connPluginID = "connection/tailscale"
		default:
			connPluginID = "connection/ssh"
		}
	}

	p, ok := Get(connPluginID)
	if !ok {
		return nil, fmt.Errorf("connection plugin %s is not registered", connPluginID)
	}
	scp, ok := p.(SSHClientProvider)
	if !ok {
		return nil, fmt.Errorf("connection plugin %s cannot provide interactive ssh clients", connPluginID)
	}
	return scp.GetClient(facts)
}

// ResolveDialer returns a plugin.Dialer for the given facts, using the same
// connection-plugin selection as ResolveRunner/ResolveSSHClient. It returns a
// Dialer that can open raw TCP connections over the right transport (tsnet
// mesh for tailnet hosts). Returns an error when no connection plugin can be
// resolved; callers should fall back to a direct net.Dial.
func ResolveDialer(facts HostFacts) (Dialer, error) {
	connPluginID := facts.ActiveConnection
	if connPluginID == "" {
		route := strings.TrimSpace(facts.HostRoute)
		switch {
		case route == "" || route == "127.0.0.1" || route == "localhost" || route == "::1":
			connPluginID = "connection/local"
		case strings.Contains(route, ".ts.net") || strings.HasPrefix(route, "100."):
			connPluginID = "connection/tailscale"
		default:
			connPluginID = "connection/ssh"
		}
	}
	p, ok := Get(connPluginID)
	if !ok {
		return nil, fmt.Errorf("connection plugin %s is not registered", connPluginID)
	}
	d, ok := p.(Dialer)
	if !ok {
		return nil, fmt.Errorf("connection plugin %s does not provide a dialer", connPluginID)
	}
	return d, nil
}

// EvictSSHClient removes a cached interactive SSH client for the given facts so
// the next ResolveSSHClient call re-dials. Used by callers that discover the
// client is dead only after attempting a session (NewSession failure).
func EvictSSHClient(facts HostFacts) {
	connPluginID := facts.ActiveConnection
	if connPluginID == "" {
		route := strings.TrimSpace(facts.HostRoute)
		switch {
		case route == "" || route == "127.0.0.1" || route == "localhost" || route == "::1":
			return
		case strings.Contains(route, ".ts.net") || strings.HasPrefix(route, "100."):
			connPluginID = "connection/tailscale"
		default:
			connPluginID = "connection/ssh"
		}
	}
	p, ok := Get(connPluginID)
	if !ok {
		return
	}
	type evictor interface {
		EvictClient(h HostFacts)
	}
	if e, ok := p.(evictor); ok {
		e.EvictClient(facts)
	}
}
