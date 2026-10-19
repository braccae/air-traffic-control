package interfaces

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"air-traffic-control/backend/pkg/plugin"
)

func init() {
	_ = plugin.Register(New())
}

// Plugin implements network interface and route telemetry and configuration.
type Plugin struct{}

func New() *Plugin {
	return &Plugin{}
}

func (p *Plugin) Meta() plugin.Meta {
	return plugin.Meta{
		ID:          "network/interfaces",
		Kind:        plugin.KindNetwork,
		Name:        "interfaces",
		Label:       "Network Interfaces",
		Icon:        "settings_ethernet",
		Description: "Linux network interfaces, IP addresses, MTU, carrier states, and link telemetry",
		Order:       10,
	}
}

func (p *Plugin) Compatible(h plugin.HostFacts) bool {
	return h.OSFamily != "windows" && h.OSFamily != "router_switch"
}

func (p *Plugin) Requirements() plugin.Requirements {
	return plugin.Requirements{
		HostPackages: map[string][]string{
			"dnf": {"iproute"},
			"apt": {"iproute2"},
			"apk": {"iproute2"},
		},
		ControllerCollections: []string{
			"ansible.builtin",
		},
	}
}

func (p *Plugin) Probe(ctx context.Context, r plugin.Runner, h plugin.HostFacts) (plugin.Detection, error) {
	if r == nil {
		return plugin.Detection{Detected: false, Status: "OFFLINE", Error: "runner is nil"}, nil
	}

	out, err := r.RunCommand(ctx, "command -v ip")
	if err != nil || strings.TrimSpace(out) == "" {
		return plugin.Detection{
			Detected: false,
			Status:   "UNAVAILABLE",
			Error:    "ip command not found",
		}, nil
	}

	// Quick interface count
	ifCountOut, err := r.RunCommand(ctx, "ip -j link 2>/dev/null || true")
	count := 0
	if err == nil && strings.TrimSpace(ifCountOut) != "" {
		var links []map[string]any
		if err := json.Unmarshal([]byte(ifCountOut), &links); err == nil {
			count = len(links)
		}
	}

	return plugin.Detection{
		Detected: true,
		Status:   fmt.Sprintf("%d interface(s)", count),
		Details:  "iproute2 tools active",
	}, nil
}

type ipAddrInfo struct {
	Family    string `json:"family"`
	Local     string `json:"local"`
	PrefixLen int    `json:"prefixlen"`
}

type ipAddrObj struct {
	IfName   string       `json:"ifname"`
	AddrInfo []ipAddrInfo `json:"addr_info"`
}

type ipLinkObj struct {
	IfName    string   `json:"ifname"`
	Address   string   `json:"address"`
	OperState string   `json:"operstate"`
	Flags     []string `json:"flags"`
	MTU       int      `json:"mtu"`
	Stats64   struct {
		Rx struct {
			Bytes float64 `json:"bytes"`
		} `json:"rx"`
		Tx struct {
			Bytes float64 `json:"bytes"`
		} `json:"tx"`
	} `json:"stats64"`
}

func formatBytes(b float64) string {
	if b >= 1073741824 {
		return fmt.Sprintf("%.2f GiB", b/1073741824)
	}
	if b >= 1048576 {
		return fmt.Sprintf("%.1f MiB", b/1048576)
	}
	if b >= 1024 {
		return fmt.Sprintf("%.0f KiB", b/1024)
	}
	return fmt.Sprintf("%.0f B", b)
}

func (p *Plugin) Read(ctx context.Context, r plugin.Runner, h plugin.HostFacts) (any, error) {
	if r == nil {
		return nil, fmt.Errorf("runner is nil")
	}

	cmd := "ip -j addr 2>/dev/null; echo '===LINK==='; ip -j -s link 2>/dev/null"
	out, err := r.RunCommand(ctx, cmd)
	if err != nil {
		return nil, err
	}

	parts := strings.Split(out, "===LINK===")
	addrRaw := strings.TrimSpace(parts[0])
	linkRaw := ""
	if len(parts) > 1 {
		linkRaw = strings.TrimSpace(parts[1])
	}

	var addrs []ipAddrObj
	var links []ipLinkObj

	_ = json.Unmarshal([]byte(addrRaw), &addrs)
	_ = json.Unmarshal([]byte(linkRaw), &links)

	var ifaces []map[string]any
	for _, link := range links {
		name := link.IfName
		var addrMatch *ipAddrObj
		for i := range addrs {
			if addrs[i].IfName == name {
				addrMatch = &addrs[i]
				break
			}
		}

		ipStr := "No IP assigned"
		if addrMatch != nil {
			for _, info := range addrMatch.AddrInfo {
				if info.Family == "inet" {
					ipStr = fmt.Sprintf("%s/%d", info.Local, info.PrefixLen)
					break
				}
			}
			if ipStr == "No IP assigned" {
				for _, info := range addrMatch.AddrInfo {
					if info.Family == "inet6" {
						ipStr = fmt.Sprintf("%s/%d", info.Local, info.PrefixLen)
						break
					}
				}
			}
		}

		rxBytes := link.Stats64.Rx.Bytes
		txBytes := link.Stats64.Tx.Bytes

		ifaceType := "Physical Ethernet"
		speed := "1000 Mbps (Full Duplex)"
		if name == "lo" {
			ifaceType = "Loopback"
			speed = "Virtual"
		} else if strings.HasPrefix(name, "tailscale") {
			ifaceType = "WireGuard Tunnel"
			speed = "10 Gbps (Mesh)"
		} else if strings.Contains(name, ".") {
			ifaceType = "802.1Q VLAN"
			speed = "Sub-interface"
		} else if strings.HasPrefix(name, "docker") || strings.HasPrefix(name, "br-") || strings.HasPrefix(name, "podman") {
			ifaceType = "Container Bridge"
			speed = "Virtual"
		} else if strings.HasPrefix(name, "veth") {
			ifaceType = "Container veth peer"
			speed = "Virtual"
		}

		isUp := false
		for _, f := range link.Flags {
			if f == "UP" {
				isUp = true
				break
			}
		}

		isCarrier := link.OperState == "UP" || link.OperState == "UNKNOWN"
		status := "DOWN"
		if isUp {
			if isCarrier {
				status = "UP / CARRIER"
			} else {
				status = "UP / NO-CARRIER"
			}
		}

		mac := link.Address
		if mac == "" {
			mac = "N/A"
		}

		mtu := link.MTU
		if mtu == 0 {
			mtu = 1500
		}

		ifaces = append(ifaces, map[string]any{
			"name":   name,
			"type":   ifaceType,
			"ip":     ipStr,
			"mac":    mac,
			"speed":  speed,
			"rx":     formatBytes(rxBytes),
			"tx":     formatBytes(txBytes),
			"status": status,
			"mtu":    mtu,
		})
	}

	return map[string]any{
		"interfaces": ifaces,
	}, nil
}

func (p *Plugin) Actions() []plugin.ActionSpec {
	return []plugin.ActionSpec{
		{
			Name:        "connect_interface",
			Label:       "Configure Interface",
			Description: "Configure network interface settings",
			Options: []plugin.OptionSchema{
				{Name: "name", Type: "string", Required: true, Description: "Interface device name (e.g. eth0, wg0)"},
				{Name: "type", Type: "string", Required: false, Default: "wireguard", Description: "Interface type"},
				{Name: "cidr", Type: "string", Required: false, Description: "IPv4 CIDR address"},
				{Name: "gateway", Type: "string", Required: false, Description: "Default gateway IPv4"},
				{Name: "dns", Type: "string", Required: false, Description: "DNS nameserver"},
			},
		},
	}
}

func (p *Plugin) Compile(h plugin.HostFacts, action string, opts map[string]any) (plugin.TaskSpec, error) {
	norm := strings.ToLower(action)
	if norm != "connect_interface" && norm != "interface" {
		return plugin.TaskSpec{}, fmt.Errorf("unsupported interfaces action: %s", action)
	}

	ifaceName, _ := opts["name"].(string)
	if ifaceName == "" {
		ifaceName, _ = opts["interface"].(string)
	}
	if ifaceName == "" {
		ifaceName = "eth0"
	}

	ifaceType, _ := opts["type"].(string)
	if ifaceType == "" {
		ifaceType = "wireguard"
	}

	templateParams := map[string]any{
		"interface": ifaceName,
		"type":      ifaceType,
		"become":    true,
	}

	if cidr, ok := opts["cidr"].(string); ok && cidr != "" {
		templateParams["cidr"] = cidr
	}
	if gw, ok := opts["gateway"].(string); ok && gw != "" {
		templateParams["gateway"] = gw
	}
	if dns, ok := opts["dns"].(string); ok && dns != "" {
		templateParams["dns"] = dns
	}

	step := plugin.TaskStep{
		Name:   fmt.Sprintf("Configure network interface %s (%s)", ifaceName, ifaceType),
		Module: "ansible.builtin.template",
		Params: templateParams,
	}

	return plugin.TaskSpec{
		Name:  fmt.Sprintf("Configure network interface %s", ifaceName),
		Steps: []plugin.TaskStep{step},
	}, nil
}
