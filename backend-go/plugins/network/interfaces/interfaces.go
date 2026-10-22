package interfaces

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"air-traffic-control/backend/pkg/plugin"
	"air-traffic-control/backend/pkg/plugin/newapi"
	"air-traffic-control/backend/pkg/plugin/settingsframework"
)

var (
	_ plugin.Plugin                      = (*Plugin)(nil)
	_ plugin.Reader                      = (*Plugin)(nil)
	_ plugin.Actor                       = (*Plugin)(nil)
	_ plugin.SettingsPageProvider        = (*Plugin)(nil)
	_ newapi.DataCollectionPlugin        = (*Plugin)(nil)
	_ newapi.ManagementPlugin            = (*Plugin)(nil)
	_ newapi.CategorizedSettingsProvider = (*Plugin)(nil)
	_ newapi.ContextMenuProvider         = (*Plugin)(nil)
	_ newapi.TaskCompileProvider         = (*Plugin)(nil)
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

func (p *Plugin) CollectionSchedule() newapi.CollectionSchedule {
	return newapi.DefaultScheduleForKind(p.Meta().Kind)
}

func (p *Plugin) CollectionSchema() newapi.CollectionSchema {
	return newapi.CollectionSchema{
		Version:     "v2",
		RootKey:     "interfaces",
		Description: p.Meta().Description,
	}
}

func (p *Plugin) CategorizedSettingsPage() newapi.CategorizedSettingsPage {
	return settingsframework.NewPageBuilder(p.Meta().ID, "Network Interfaces Defaults").
		WithIcon(p.Meta().Icon).
		WithCategory(string(p.Meta().Kind)).
		WithScope(newapi.SettingsScopeDatacenter).
		WithOrder(p.Meta().Order).
		WithDescription("Datacenter-wide network interface configuration, MTU, and DNS defaults.").
		StringField("defaultInterface", "Default Interface", "eth0",
			settingsframework.WithDescription("Fallback network interface device name when none is specified.")).
		StringField("defaultInterfaceType", "Default Interface Type", "wireguard",
			settingsframework.WithDescription("Default interface type when configuring an interface.")).
		IntField("defaultMTU", "Default MTU", 1500,
			settingsframework.WithMinMax(576, 9216),
			settingsframework.WithDescription("Default MTU applied when link MTU is unreported.")).
		StringField("defaultDNS", "Default DNS Nameserver", "",
			settingsframework.WithDescription("Optional default DNS nameserver IP for configured interfaces.")).
		BuildCategorized()
}

func (p *Plugin) SettingsPage() plugin.SettingsPage {
	return p.CategorizedSettingsPage().SettingsPage
}

func defaultInterfaceName() string {
	return settingsframework.NewStoreAdapter(nil).StringOrDefault("network/interfaces", "defaultInterface", "eth0")
}

func defaultInterfaceType() string {
	return settingsframework.NewStoreAdapter(nil).StringOrDefault("network/interfaces", "defaultInterfaceType", "wireguard")
}

func defaultMTU() int {
	return settingsframework.NewStoreAdapter(nil).IntOrDefault("network/interfaces", "defaultMTU", 1500)
}

func (p *Plugin) ContextMenuEntries(ctx context.Context, h plugin.HostFacts) []newapi.ContextMenuEntry {
	if !p.Compatible(h) {
		return nil
	}
	return newapi.NewContextMenuBuilder(p.Meta().ID).
		WithTarget(newapi.TargetNetwork).
		WithGroup(string(p.Meta().Kind)).
		AddAction("connect_interface", "Configure Interface", "settings_ethernet", "connect_interface", 10).
		AddAction("toggle_interface", "Toggle Interface State", "swap_horiz", "toggle_interface", 20).
		Build()
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
			mtu = defaultMTU()
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

func (p *Plugin) ManagementActions() []newapi.ManagementActionSpec {
	return []newapi.ManagementActionSpec{
		{
			ActionSpec: plugin.ActionSpec{
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
			Category:         newapi.ActionCategoryConfiguration,
			Idempotent:       true,
			Dangerous:        false,
			InvalidatesKinds: []plugin.Kind{plugin.KindNetwork},
		},
		{
			ActionSpec: plugin.ActionSpec{
				Name:        "toggle_interface",
				Label:       "Toggle Interface State",
				Description: "Set network interface up or down",
				Options: []plugin.OptionSchema{
					{Name: "name", Type: "string", Required: true, Description: "Interface device name (e.g. eth0)"},
					{Name: "state", Type: "select", Enum: []string{"up", "down"}, Default: "up", Description: "Interface state"},
				},
			},
			Category:         newapi.ActionCategoryLifecycle,
			Idempotent:       true,
			Dangerous:        false,
			InvalidatesKinds: []plugin.Kind{plugin.KindNetwork},
		},
	}
}

func (p *Plugin) Actions() []plugin.ActionSpec {
	mgmt := p.ManagementActions()
	specs := make([]plugin.ActionSpec, len(mgmt))
	for i, m := range mgmt {
		specs[i] = m.ActionSpec
	}
	return specs
}

func normalizeActionName(action string) string {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "connect_interface", "interface":
		return "connect_interface"
	case "toggle_interface", "toggle":
		return "toggle_interface"
	default:
		return strings.ToLower(strings.TrimSpace(action))
	}
}

func (p *Plugin) ValidateAction(h plugin.HostFacts, action string, opts map[string]any) error {
	canonical := normalizeActionName(action)
	for _, spec := range p.ManagementActions() {
		if strings.EqualFold(spec.Name, canonical) {
			normalizedOpts := make(map[string]any, len(opts)+1)
			for k, v := range opts {
				normalizedOpts[k] = v
			}
			if name, _ := normalizedOpts["name"].(string); strings.TrimSpace(name) == "" {
				if iface, ok := normalizedOpts["interface"].(string); ok && strings.TrimSpace(iface) != "" {
					normalizedOpts["name"] = strings.TrimSpace(iface)
				}
			}
			return newapi.ValidateManagementOptions(spec, normalizedOpts)
		}
	}
	return fmt.Errorf("unsupported interfaces action: %s", action)
}

func (p *Plugin) CompileTask(ctx context.Context, h plugin.HostFacts, req newapi.TaskRequest) (plugin.TaskSpec, error) {
	action := strings.TrimSpace(req.Action)
	if action == "" && req.Params != nil {
		if a, ok := req.Params["action"].(string); ok {
			action = strings.TrimSpace(a)
		}
	}
	if action == "" {
		return plugin.TaskSpec{}, fmt.Errorf("action is required for plugin %s", p.Meta().ID)
	}
	return p.Compile(h, action, req.Params)
}

func (p *Plugin) Compile(h plugin.HostFacts, action string, opts map[string]any) (plugin.TaskSpec, error) {
	norm := strings.ToLower(action)
	if norm == "toggle_interface" || norm == "toggle" {
		ifaceName, _ := opts["name"].(string)
		if ifaceName == "" {
			ifaceName, _ = opts["interface"].(string)
		}
		if ifaceName == "" {
			return plugin.TaskSpec{}, fmt.Errorf("interface name is required")
		}
		state, _ := opts["state"].(string)
		if strings.ToLower(state) != "down" {
			state = "up"
		}
		return plugin.TaskSpec{
			Name: fmt.Sprintf("Set network interface %s %s", ifaceName, state),
			Steps: []plugin.TaskStep{
				{
					Name:   fmt.Sprintf("Set interface %s %s", ifaceName, state),
					Module: "ansible.builtin.command",
					Params: map[string]any{
						"cmd":    fmt.Sprintf("ip link set %s %s", ifaceName, state),
						"become": true,
					},
				},
			},
		}, nil
	}

	if norm != "connect_interface" && norm != "interface" {
		return plugin.TaskSpec{}, fmt.Errorf("unsupported interfaces action: %s", action)
	}

	ifaceName, _ := opts["name"].(string)
	if ifaceName == "" {
		ifaceName, _ = opts["interface"].(string)
	}
	if ifaceName == "" {
		ifaceName = defaultInterfaceName()
	}

	ifaceType, _ := opts["type"].(string)
	if ifaceType == "" {
		ifaceType = defaultInterfaceType()
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
