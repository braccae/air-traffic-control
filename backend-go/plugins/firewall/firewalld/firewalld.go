package firewalld

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"air-traffic-control/backend/pkg/plugin"
)

func init() {
	_ = plugin.Register(New())
}

// Plugin implements firewalld zone and rules inspection and packet filter mutation.
type Plugin struct{}

func New() *Plugin {
	return &Plugin{}
}

func (p *Plugin) Meta() plugin.Meta {
	return plugin.Meta{
		ID:          "firewall/firewalld",
		Kind:        plugin.KindFirewall,
		Name:        "firewalld",
		Label:       "Firewall (firewalld / nftables)",
		Icon:        "security",
		Description: "Linux packet filter, active zones, open services, allowed ports, and nftables backend rules",
		Order:       10,
	}
}

func (p *Plugin) Compatible(h plugin.HostFacts) bool {
	return h.OSFamily != "windows" && h.OSFamily != "router_switch"
}

func (p *Plugin) Requirements() plugin.Requirements {
	return plugin.Requirements{
		HostPackages: map[string][]string{
			"dnf": {"firewalld"},
			"apt": {"firewalld"},
		},
		ControllerCollections: []string{
			"ansible.posix",
		},
	}
}

func (p *Plugin) Probe(ctx context.Context, r plugin.Runner, h plugin.HostFacts) (plugin.Detection, error) {
	if r == nil {
		return plugin.Detection{Detected: false, Status: "OFFLINE", Error: "runner is nil"}, nil
	}

	out, err := r.RunCommand(ctx, "sudo -n firewall-cmd --state 2>&1 || firewall-cmd --state 2>&1 || true")
	if err == nil && strings.Contains(strings.TrimSpace(out), "running") {
		return plugin.Detection{
			Detected: true,
			Status:   "running",
			Details:  "firewalld daemon active",
		}, nil
	}

	cmdOut, err := r.RunCommand(ctx, "command -v firewall-cmd")
	if err == nil && strings.TrimSpace(cmdOut) != "" {
		return plugin.Detection{
			Detected: true,
			Status:   "inactive",
			Details:  "firewalld installed but not running",
		}, nil
	}

	return plugin.Detection{
		Detected: false,
		Status:   "UNAVAILABLE",
		Error:    "firewall-cmd not found",
	}, nil
}

type ParsedFirewallRule struct {
	ID        string `json:"id"`
	Chain     string `json:"chain"`
	Policy    string `json:"policy"`
	Proto     string `json:"proto"`
	Port      string `json:"port"`
	Source    string `json:"source"`
	Interface string `json:"interface"`
	Comment   string `json:"comment"`
}

var zoneHeaderRegex = regexp.MustCompile(`^([a-zA-Z0-9_\-]+)`)

func parseFirewallListAll(listAllRaw, activeZonesRaw string) (map[string]any, []string, []string, []string, []ParsedFirewallRule) {
	zones := make(map[string]map[string][]string)
	zoneLines := strings.Split(strings.TrimSpace(activeZonesRaw), "\n")
	currentZone := ""

	for _, line := range zoneLines {
		if match := zoneHeaderRegex.FindStringSubmatch(line); len(match) > 0 && !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
			currentZone = match[1]
			zones[currentZone] = map[string][]string{
				"interfaces": {},
				"sources":    {},
			}
		} else if currentZone != "" && strings.Contains(line, "interfaces:") {
			val := strings.TrimSpace(strings.Replace(line, "interfaces:", "", 1))
			zones[currentZone]["interfaces"] = strings.Fields(val)
		} else if currentZone != "" && strings.Contains(line, "sources:") {
			val := strings.TrimSpace(strings.Replace(line, "sources:", "", 1))
			zones[currentZone]["sources"] = strings.Fields(val)
		}
	}

	var services []string
	var ports []string
	var richRules []string
	inRichRules := false

	for _, line := range strings.Split(listAllRaw, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "services:") {
			val := strings.TrimSpace(strings.Replace(trimmed, "services:", "", 1))
			services = strings.Fields(val)
			inRichRules = false
		} else if strings.HasPrefix(trimmed, "ports:") {
			val := strings.TrimSpace(strings.Replace(trimmed, "ports:", "", 1))
			ports = strings.Fields(val)
			inRichRules = false
		} else if strings.HasPrefix(trimmed, "rich rules:") {
			inRichRules = true
		} else if inRichRules {
			if strings.HasPrefix(trimmed, "rule ") {
				richRules = append(richRules, trimmed)
			} else if trimmed != "" && !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
				inRichRules = false
			}
		}
	}

	var rules []ParsedFirewallRule

	// Tailscale trusted rule
	isTailscaleTrusted := false
	if trusted, ok := zones["trusted"]; ok {
		for _, iface := range trusted["interfaces"] {
			if iface == "tailscale0" {
				isTailscaleTrusted = true
				break
			}
		}
	}
	if !isTailscaleTrusted {
		if internal, ok := zones["internal"]; ok {
			for _, iface := range internal["interfaces"] {
				if iface == "tailscale0" {
					isTailscaleTrusted = true
					break
				}
			}
		}
	}

	if isTailscaleTrusted {
		rules = append(rules, ParsedFirewallRule{
			ID:        "fw-ts",
			Chain:     "INPUT",
			Policy:    "ACCEPT",
			Proto:     "all",
			Port:      "all",
			Source:    "100.64.0.0/10",
			Interface: "tailscale0",
			Comment:   "Tailscale mesh trusted traffic",
		})
	}

	idx := 1
	for _, s := range services {
		rules = append(rules, ParsedFirewallRule{
			ID:        fmt.Sprintf("fw-svc-%d", idx),
			Chain:     "INPUT",
			Policy:    "ACCEPT",
			Proto:     "tcp/udp",
			Port:      s,
			Source:    "0.0.0.0/0",
			Interface: "all",
			Comment:   fmt.Sprintf("Allowed service: %s", s),
		})
		idx++
	}

	for _, p := range ports {
		tokens := strings.Split(p, "/")
		portNum := tokens[0]
		proto := "tcp"
		if len(tokens) > 1 {
			proto = tokens[1]
		}
		rules = append(rules, ParsedFirewallRule{
			ID:        fmt.Sprintf("fw-port-%d", idx),
			Chain:     "INPUT",
			Policy:    "ACCEPT",
			Proto:     proto,
			Port:      portNum,
			Source:    "0.0.0.0/0",
			Interface: "all",
			Comment:   fmt.Sprintf("Explicit port allow: %s", p),
		})
		idx++
	}

	convertedZones := make(map[string]any)
	for k, v := range zones {
		convertedZones[k] = v
	}

	return convertedZones, services, ports, richRules, rules
}

func (p *Plugin) Read(ctx context.Context, r plugin.Runner, h plugin.HostFacts) (any, error) {
	if r == nil {
		return nil, fmt.Errorf("runner is nil")
	}

	cmd := "sudo -n firewall-cmd --state 2>&1 || firewall-cmd --state 2>&1 || echo 'NO_FIREWALLD'; echo '===ZONES==='; sudo -n firewall-cmd --get-active-zones 2>&1 || true; echo '===LIST_ALL==='; sudo -n firewall-cmd --list-all 2>&1 || true; echo '===NFT==='; sudo -n nft list tables 2>&1 || echo 'NO_NFT'"
	out, err := r.RunCommand(ctx, cmd)
	if err != nil {
		return nil, err
	}

	parts := strings.Split(out, "===ZONES===")
	stateRaw := strings.TrimSpace(parts[0])
	var zonesRaw, listAllRaw, nftRaw string
	if len(parts) > 1 {
		rest1 := strings.Split(parts[1], "===LIST_ALL===")
		zonesRaw = strings.TrimSpace(rest1[0])
		if len(rest1) > 1 {
			rest2 := strings.Split(rest1[1], "===NFT===")
			listAllRaw = strings.TrimSpace(rest2[0])
			if len(rest2) > 1 {
				nftRaw = strings.TrimSpace(rest2[1])
			}
		}
	}

	isRunning := strings.Contains(stateRaw, "running")
	hasNft := !strings.Contains(nftRaw, "NO_NFT") && strings.Contains(nftRaw, "table")

	zones, services, ports, _, rules := parseFirewallListAll(listAllRaw, zonesRaw)

	if !isRunning {
		rules = []ParsedFirewallRule{}
	}

	status := "inactive"
	if isRunning {
		status = "running"
	}

	return map[string]any{
		"status":         status,
		"nftablesActive": hasNft,
		"backend":        "nftables (firewalld)",
		"zones":          zones,
		"services":       services,
		"ports":          ports,
		"rules":          rules,
	}, nil
}

func (p *Plugin) Actions() []plugin.ActionSpec {
	return []plugin.ActionSpec{
		{
			Name:        "allow_port",
			Label:       "Allow Port",
			Description: "Allow an incoming TCP/UDP port through firewalld",
			Options: []plugin.OptionSchema{
				{Name: "port", Type: "string", Required: true, Description: "Port number or port/protocol (e.g. 80, 443/tcp)"},
				{Name: "proto", Type: "string", Required: false, Default: "tcp", Description: "Protocol: tcp or udp"},
				{Name: "zone", Type: "string", Required: false, Description: "Target firewall zone"},
				{Name: "state", Type: "select", Enum: []string{"enabled", "disabled"}, Default: "enabled", Description: "Rule state"},
			},
		},
		{
			Name:        "allow_service",
			Label:       "Allow Service",
			Description: "Allow a named service through firewalld",
			Options: []plugin.OptionSchema{
				{Name: "service", Type: "string", Required: true, Description: "Service name (e.g. http, ssh)"},
				{Name: "zone", Type: "string", Required: false, Description: "Target firewall zone"},
				{Name: "state", Type: "select", Enum: []string{"enabled", "disabled"}, Default: "enabled", Description: "Rule state"},
			},
		},
	}
}

func (p *Plugin) Compile(h plugin.HostFacts, action string, opts map[string]any) (plugin.TaskSpec, error) {
	norm := strings.ToLower(action)
	switch norm {
	case "allow_port", "port":
		port, _ := opts["port"].(string)
		if port == "" {
			port = "80"
		}
		proto, _ := opts["proto"].(string)
		if proto == "" {
			proto = "tcp"
		}
		portProto := port
		if !strings.Contains(port, "/") {
			portProto = fmt.Sprintf("%s/%s", port, proto)
		}

		state, _ := opts["state"].(string)
		if state == "" {
			state = "enabled"
		}

		params := map[string]any{
			"port":      portProto,
			"permanent": true,
			"state":     state,
			"immediate": true,
			"become":    true,
		}
		if zone, ok := opts["zone"].(string); ok && zone != "" {
			params["zone"] = zone
		}

		return plugin.TaskSpec{
			Name: fmt.Sprintf("Allow port %s through firewall", portProto),
			Steps: []plugin.TaskStep{
				{
					Name:   fmt.Sprintf("Allow port %s in firewall", portProto),
					Module: "ansible.posix.firewalld",
					Params: params,
				},
			},
		}, nil

	case "allow_service", "service":
		svc, _ := opts["service"].(string)
		if svc == "" {
			return plugin.TaskSpec{}, fmt.Errorf("service name is required")
		}
		state, _ := opts["state"].(string)
		if state == "" {
			state = "enabled"
		}

		params := map[string]any{
			"service":   svc,
			"permanent": true,
			"state":     state,
			"immediate": true,
			"become":    true,
		}
		if zone, ok := opts["zone"].(string); ok && zone != "" {
			params["zone"] = zone
		}

		return plugin.TaskSpec{
			Name: fmt.Sprintf("Allow service %s through firewall", svc),
			Steps: []plugin.TaskStep{
				{
					Name:   fmt.Sprintf("Allow service %s in firewall", svc),
					Module: "ansible.posix.firewalld",
					Params: params,
				},
			},
		}, nil

	default:
		return plugin.TaskSpec{}, fmt.Errorf("unsupported firewalld action: %s", action)
	}
}
