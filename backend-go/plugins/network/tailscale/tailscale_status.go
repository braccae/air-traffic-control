package tailscale

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"air-traffic-control/backend/pkg/plugin"
)

func init() {
	_ = plugin.Register(New())
}

// Plugin implements local and peer Tailscale mesh status inspection plus
// declarative `tailscale set` feature configuration for the selected node.
type Plugin struct{}

func New() *Plugin {
	return &Plugin{}
}

func (p *Plugin) Meta() plugin.Meta {
	return plugin.Meta{
		ID:          "network/tailscale",
		Kind:        plugin.KindNetwork,
		Name:        "tailscale",
		Label:       "Tailscale Mesh Status",
		Icon:        "hub",
		Description: "Tailscale mesh peering, MagicDNS status, direct vs DERP relays, peer latency, and feature configuration",
		Order:       20,
	}
}

func (p *Plugin) Compatible(h plugin.HostFacts) bool {
	return true
}

func (p *Plugin) Requirements() plugin.Requirements {
	return plugin.Requirements{
		HostPackages: map[string][]string{
			"dnf": {"tailscale"},
			"apt": {"tailscale"},
			"apk": {"tailscale"},
		},
		ControllerCollections: []string{"ansible.builtin"},
	}
}

func (p *Plugin) Probe(ctx context.Context, r plugin.Runner, h plugin.HostFacts) (plugin.Detection, error) {
	if r == nil {
		return plugin.Detection{Detected: false, Status: "OFFLINE", Error: "runner is nil"}, nil
	}

	out, err := r.RunCommand(ctx, "tailscale status --json 2>/dev/null || true")
	if err != nil || strings.TrimSpace(out) == "" {
		return plugin.Detection{
			Detected: false,
			Status:   "UNAVAILABLE",
			Error:    "tailscale status command failed or tailscale not running",
		}, nil
	}

	var status map[string]any
	if err := json.Unmarshal([]byte(out), &status); err != nil {
		return plugin.Detection{
			Detected: false,
			Status:   "UNAVAILABLE",
			Error:    "failed to parse tailscale status json",
		}, nil
	}

	state, _ := status["BackendState"].(string)
	if state == "" {
		state = "ONLINE"
	}

	peers := 0
	if peerMap, ok := status["Peer"].(map[string]any); ok {
		peers = len(peerMap)
	}

	return plugin.Detection{
		Detected: true,
		Status:   state,
		Details:  fmt.Sprintf("Tailscale mesh active (%d peer(s))", peers),
	}, nil
}

// ---------------------------------------------------------------------------
// Structured read payload
// ---------------------------------------------------------------------------

// SelfInfo describes the selected node's own Tailscale identity.
type SelfInfo struct {
	ID           string   `json:"id"`
	HostName     string   `json:"hostName"`
	DNSName      string   `json:"dnsName"`
	TailscaleIPs []string `json:"tailscaleIPs"`
	Online       bool     `json:"online"`
	OS           string   `json:"os"`
	Tags         []string `json:"tags"`
	ExitNode     bool     `json:"exitNode"`
}

// PeerInfo describes a single peer visible from the selected node.
type PeerInfo struct {
	Key            string   `json:"key"`
	HostName       string   `json:"hostName"`
	DNSName        string   `json:"dnsName"`
	TailscaleIPs   []string `json:"tailscaleIPs"`
	Online         bool     `json:"online"`
	Active         bool     `json:"active"`
	OS             string   `json:"os"`
	Relay          string   `json:"relay"`
	CurAddr        string   `json:"curAddr"`
	RxBytes        float64  `json:"rxBytes"`
	TxBytes        float64  `json:"txBytes"`
	LastSeen       string   `json:"lastSeen"`
	LastHandshake  string   `json:"lastHandshake"`
	ExitNode       bool     `json:"exitNode"`
	ExitNodeOption bool     `json:"exitNodeOption"`
	AllowedIPs     []string `json:"allowedIPs"`
	Tags           []string `json:"tags"`
}

// Prefs reflects the node's current `tailscale set` preferences (best-effort).
type Prefs struct {
	AcceptDNS         bool   `json:"acceptDNS"`
	AcceptRoutes      bool   `json:"acceptRoutes"`
	SSH               bool   `json:"ssh"`
	AdvertiseExitNode bool   `json:"advertiseExitNode"`
	AdvertiseRoutes   string `json:"advertiseRoutes"`
	ExitNode          string `json:"exitNode"`
	Hostname          string `json:"hostname"`
}

// ReadResult is the stable structured payload returned by Read.
type ReadResult struct {
	BackendState   string     `json:"backendState"`
	MagicDNSSuffix string     `json:"magicDNSSuffix"`
	Health         []string   `json:"health"`
	Self           *SelfInfo  `json:"self"`
	Peers          []PeerInfo `json:"peers"`
	Prefs          Prefs      `json:"prefs"`
}

func (p *Plugin) Read(ctx context.Context, r plugin.Runner, h plugin.HostFacts) (any, error) {
	if r == nil {
		return nil, fmt.Errorf("runner is nil")
	}

	statusOut, err := r.RunCommand(ctx, "tailscale status --json")
	if err != nil {
		return nil, err
	}

	var status map[string]any
	if err := json.Unmarshal([]byte(statusOut), &status); err != nil {
		return nil, fmt.Errorf("invalid tailscale status json: %w", err)
	}

	result := ReadResult{Peers: []PeerInfo{}}
	result.BackendState, _ = status["BackendState"].(string)
	result.MagicDNSSuffix, _ = status["MagicDNSSuffix"].(string)
	result.Health = stringSlice(status["Health"])

	if self, ok := status["Self"].(map[string]any); ok {
		result.Self = &SelfInfo{
			ID:           asString(self["ID"]),
			HostName:     asString(self["HostName"]),
			DNSName:      asString(self["DNSName"]),
			TailscaleIPs: stringSlice(self["TailscaleIPs"]),
			Online:       asBool(self["Online"]),
			OS:           asString(self["OS"]),
			Tags:         stringSlice(self["Tags"]),
			ExitNode:     asBool(self["ExitNode"]),
		}
	}

	if peerMap, ok := status["Peer"].(map[string]any); ok {
		keys := make([]string, 0, len(peerMap))
		for k := range peerMap {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			pm, ok := peerMap[k].(map[string]any)
			if !ok {
				continue
			}
			result.Peers = append(result.Peers, PeerInfo{
				Key:            k,
				HostName:       asString(pm["HostName"]),
				DNSName:        asString(pm["DNSName"]),
				TailscaleIPs:   stringSlice(pm["TailscaleIPs"]),
				Online:         asBool(pm["Online"]),
				Active:         asBool(pm["Active"]),
				OS:             asString(pm["OS"]),
				Relay:          asString(pm["Relay"]),
				CurAddr:        asString(pm["CurAddr"]),
				RxBytes:        asFloat(pm["RxBytes"]),
				TxBytes:        asFloat(pm["TxBytes"]),
				LastSeen:       asString(pm["LastSeen"]),
				LastHandshake:  asString(pm["LastHandshake"]),
				ExitNode:       asBool(pm["ExitNode"]),
				ExitNodeOption: asBool(pm["ExitNodeOption"]),
				AllowedIPs:     stringSlice(pm["AllowedIPs"]),
				Tags:           stringSlice(pm["Tags"]),
			})
		}
	}

	// Prefs are best-effort: `tailscale debug prefs` may require operator
	// privileges on the node. Failure leaves the zero-value Prefs, never faked.
	if prefsOut, perr := r.RunCommand(ctx, "tailscale debug prefs 2>/dev/null || true"); perr == nil && strings.TrimSpace(prefsOut) != "" {
		var raw map[string]any
		if json.Unmarshal([]byte(prefsOut), &raw) == nil {
			result.Prefs = parsePrefs(raw)
		}
	}

	return result, nil
}

func parsePrefs(m map[string]any) Prefs {
	p := Prefs{
		AcceptDNS:         asBool(m["CorpDNS"]),
		AcceptRoutes:      asBool(m["RouteAll"]),
		SSH:               asBool(m["RunSSH"]),
		AdvertiseExitNode: asBool(m["AdvertiseExitNode"]),
		Hostname:          asString(m["Hostname"]),
	}
	if routes := stringSlice(m["AdvertiseRoutes"]); len(routes) > 0 {
		p.AdvertiseRoutes = strings.Join(routes, ",")
	}
	if ip := asString(m["ExitNodeIP"]); ip != "" {
		p.ExitNode = ip
	} else {
		p.ExitNode = asString(m["ExitNodeID"])
	}
	return p
}

// ---------------------------------------------------------------------------
// Actor: declarative `tailscale set` feature configuration
// ---------------------------------------------------------------------------

// tailscaleBoolActions maps an action name to its boolean `tailscale set` flag.
var tailscaleBoolActions = map[string]string{
	"set_accept_dns":          "--accept-dns",
	"set_accept_routes":       "--accept-routes",
	"set_ssh":                 "--ssh",
	"set_advertise_exit_node": "--advertise-exit-node",
}

// tailscaleStringActions maps an action name to its option name and flag.
var tailscaleStringActions = map[string]struct {
	Option string
	Flag   string
}{
	"set_advertise_routes": {"routes", "--advertise-routes"},
	"set_exit_node":        {"exitNode", "--exit-node"},
	"set_hostname":         {"hostname", "--hostname"},
}

// Actions enumerates the supported feature-configuration mutations.
func (p *Plugin) Actions() []plugin.ActionSpec {
	boolOpt := func(name, desc string) plugin.OptionSchema {
		return plugin.OptionSchema{Name: name, Type: "bool", Required: true, Description: desc}
	}
	strOpt := func(name, desc string) plugin.OptionSchema {
		return plugin.OptionSchema{Name: name, Type: "string", Required: true, Description: desc}
	}

	return []plugin.ActionSpec{
		{
			Name:        "set_accept_dns",
			Label:       "Accept DNS",
			Description: "Accept DNS configuration from the admin panel (tailscale set --accept-dns)",
			Options:     []plugin.OptionSchema{boolOpt("enabled", "Enable or disable accepting DNS configuration")},
		},
		{
			Name:        "set_accept_routes",
			Label:       "Accept Routes",
			Description: "Accept routes advertised by other Tailscale nodes (tailscale set --accept-routes)",
			Options:     []plugin.OptionSchema{boolOpt("enabled", "Enable or disable accepting advertised routes")},
		},
		{
			Name:        "set_ssh",
			Label:       "Tailscale SSH",
			Description: "Run the Tailscale SSH server on this node (tailscale set --ssh)",
			Options:     []plugin.OptionSchema{boolOpt("enabled", "Enable or disable the Tailscale SSH server")},
		},
		{
			Name:        "set_advertise_exit_node",
			Label:       "Advertise Exit Node",
			Description: "Offer to be an exit node for internet traffic (tailscale set --advertise-exit-node)",
			Options:     []plugin.OptionSchema{boolOpt("enabled", "Enable or disable advertising as an exit node")},
		},
		{
			Name:        "set_advertise_routes",
			Label:       "Advertise Routes",
			Description: "Routes to advertise to other nodes, comma-separated (tailscale set --advertise-routes)",
			Options:     []plugin.OptionSchema{strOpt("routes", "Comma-separated CIDRs, e.g. 10.0.0.0/8,192.168.0.0/24")},
		},
		{
			Name:        "set_exit_node",
			Label:       "Exit Node",
			Description: "Tailscale exit node (IP, base name, or auto:any) for internet traffic (tailscale set --exit-node)",
			Options:     []plugin.OptionSchema{strOpt("exitNode", "Exit node IP, base name, or auto:any")},
		},
		{
			Name:        "set_hostname",
			Label:       "Hostname",
			Description: "Hostname to use instead of the OS-provided one (tailscale set --hostname)",
			Options:     []plugin.OptionSchema{strOpt("hostname", "Tailscale hostname")},
		},
	}
}

// Compile turns a feature action into a declarative ansible.builtin.command task.
func (p *Plugin) Compile(h plugin.HostFacts, action string, opts map[string]any) (plugin.TaskSpec, error) {
	var cmd string

	if flag, ok := tailscaleBoolActions[action]; ok {
		enabled, ok := opts["enabled"].(bool)
		if !ok {
			return plugin.TaskSpec{}, fmt.Errorf("option \"enabled\" must be a boolean for action %s", action)
		}
		cmd = fmt.Sprintf("tailscale set %s=%t", flag, enabled)
	} else if spec, ok := tailscaleStringActions[action]; ok {
		val, _ := opts[spec.Option].(string)
		val = strings.TrimSpace(val)
		if val == "" {
			return plugin.TaskSpec{}, fmt.Errorf("option %q must not be empty for action %s", spec.Option, action)
		}
		if err := validateTailscaleValue(val); err != nil {
			return plugin.TaskSpec{}, fmt.Errorf("invalid %s: %w", spec.Option, err)
		}
		cmd = fmt.Sprintf("tailscale set %s=%s", spec.Flag, val)
	} else {
		return plugin.TaskSpec{}, fmt.Errorf("unsupported tailscale action: %s", action)
	}

	return plugin.TaskSpec{
		Name: cmd,
		Steps: []plugin.TaskStep{
			{
				Name:   cmd,
				Module: "ansible.builtin.command",
				Params: map[string]any{"cmd": cmd},
			},
		},
	}, nil
}

// validateTailscaleValue rejects values that could break out of the single
// `tailscale set --flag=<value>` argument (command-injection guard).
func validateTailscaleValue(v string) error {
	if strings.HasPrefix(v, "-") {
		return fmt.Errorf("value must not start with '-'")
	}
	if strings.ContainsAny(v, " \t\r\n;|&$`\"'\\") {
		return fmt.Errorf("value contains disallowed characters")
	}
	return nil
}

// ---------------------------------------------------------------------------
// JSON coercion helpers
// ---------------------------------------------------------------------------

func asString(v any) string {
	s, _ := v.(string)
	return s
}

func asBool(v any) bool {
	b, _ := v.(bool)
	return b
}

func asFloat(v any) float64 {
	f, _ := v.(float64)
	return f
}

func stringSlice(v any) []string {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, item := range arr {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
