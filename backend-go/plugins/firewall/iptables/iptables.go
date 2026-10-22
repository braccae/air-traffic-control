package iptables

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"air-traffic-control/backend/pkg/plugin"
)

func init() {
	_ = plugin.Register(New())
}

// Plugin implements raw iptables / ip6tables inspection and packet filter mutation.
type Plugin struct{}

func New() *Plugin {
	return &Plugin{}
}

func (p *Plugin) Meta() plugin.Meta {
	return plugin.Meta{
		ID:          "firewall/iptables",
		Kind:        plugin.KindFirewall,
		Name:        "iptables",
		Label:       "iptables",
		Icon:        "security",
		Description: "Raw Linux iptables / ip6tables netfilter packet filtering chains and rules",
		Order:       11,
	}
}

func (p *Plugin) Compatible(h plugin.HostFacts) bool {
	return h.OSFamily != "windows" && h.OSFamily != "router_switch"
}

func (p *Plugin) Requirements() plugin.Requirements {
	return plugin.Requirements{
		HostPackages: map[string][]string{
			"dnf": {"iptables"},
			"apt": {"iptables"},
			"apk": {"iptables"},
		},
		ControllerCollections: []string{},
	}
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

func (p *Plugin) Probe(ctx context.Context, r plugin.Runner, h plugin.HostFacts) (plugin.Detection, error) {
	if r == nil {
		return plugin.Detection{Detected: false, Status: "OFFLINE", Error: "runner is nil"}, nil
	}

	binOut, err := r.RunCommand(ctx, "command -v iptables || command -v ip6tables || ls /usr/sbin/iptables /sbin/iptables 2>/dev/null || true")
	if err != nil {
		return plugin.Detection{Detected: false, Status: "ERROR", Error: err.Error()}, err
	}
	if strings.TrimSpace(binOut) == "" {
		return plugin.Detection{
			Detected: false,
			Status:   "unavailable",
			Details:  "iptables binary not found on host",
		}, nil
	}

	stateCmd := "sudo -n firewall-cmd --state 2>/dev/null || firewall-cmd --state 2>/dev/null || echo 'NO_FIREWALLD'; " +
		"echo '===RULES==='; " +
		"(sudo -n iptables -S 2>/dev/null || iptables -S 2>/dev/null || /usr/sbin/iptables -S 2>/dev/null || /sbin/iptables -S 2>/dev/null || echo 'NO_RULES'); " +
		"echo '===MODS==='; " +
		"(grep -E '^(ip_tables|iptable_filter|x_tables)\\b' /proc/modules 2>/dev/null || true)"
	stateOut, err := r.RunCommand(ctx, stateCmd)
	if err != nil {
		return plugin.Detection{Detected: false, Status: "ERROR", Error: err.Error()}, err
	}

	parts := strings.Split(stateOut, "===RULES===")
	fwState := strings.TrimSpace(parts[0])
	var rulesRaw, modsRaw string
	if len(parts) > 1 {
		rest := strings.Split(parts[1], "===MODS===")
		rulesRaw = strings.TrimSpace(rest[0])
		if len(rest) > 1 {
			modsRaw = strings.TrimSpace(rest[1])
		}
	}

	// Do not claim active host firewall ownership when firewalld is running.
	if strings.Contains(fwState, "running") {
		return plugin.Detection{
			Detected: true,
			Status:   "inactive",
			Details:  "firewalld owns packet filtering on host",
		}, nil
	}

	parsedRules := ParseIptablesSave(rulesRaw)
	hasChainDefs := strings.Contains(rulesRaw, "-P ") || strings.Contains(rulesRaw, "-A ")
	hasKernelMods := strings.TrimSpace(modsRaw) != ""

	if len(parsedRules) > 0 || (hasChainDefs && !strings.Contains(rulesRaw, "NO_RULES")) || hasKernelMods {
		detail := fmt.Sprintf("iptables active (%d rules)", len(parsedRules))
		if len(parsedRules) == 0 && hasKernelMods {
			detail = "iptables kernel modules loaded"
		}
		return plugin.Detection{
			Detected: true,
			Status:   "active",
			Details:  detail,
		}, nil
	}

	return plugin.Detection{
		Detected: true,
		Status:   "inactive",
		Details:  "iptables installed but no active rules or kernel modules loaded",
	}, nil
}

var commentQuotedRe = regexp.MustCompile(`--comment\s+(?:"([^"]+)"|'([^']+)'|(\S+))`)

// ParseIptablesSave parses `iptables -S` or `iptables-save` output into ParsedFirewallRule entries.
func ParseIptablesSave(raw string) []ParsedFirewallRule {
	rules := []ParsedFirewallRule{}
	lines := strings.Split(raw, "\n")
	idx := 1

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "*") || trimmed == "COMMIT" || strings.HasPrefix(trimmed, ":") {
			continue
		}
		if !strings.HasPrefix(trimmed, "-A ") {
			continue
		}

		fields := tokenizeIptablesLine(trimmed)
		chain := "INPUT"
		policy := "ACCEPT"
		proto := "all"
		port := "all"
		source := "0.0.0.0/0"
		iface := "all"
		comment := ""

		for i := 0; i < len(fields); i++ {
			tok := fields[i]
			switch tok {
			case "-A":
				if i+1 < len(fields) {
					chain = fields[i+1]
					i++
				}
			case "-j", "-g":
				if i+1 < len(fields) {
					policy = strings.ToUpper(fields[i+1])
					i++
				}
			case "-p":
				if i+1 < len(fields) {
					proto = strings.ToLower(fields[i+1])
					i++
				}
			case "--dport", "--dports":
				if i+1 < len(fields) {
					port = strings.ReplaceAll(fields[i+1], ":", "-")
					i++
				}
			case "-s", "--src", "--source":
				if i+1 < len(fields) {
					source = fields[i+1]
					i++
				}
			case "-i", "--in-interface":
				if i+1 < len(fields) {
					iface = fields[i+1]
					i++
				}
			}
		}

		if m := commentQuotedRe.FindStringSubmatch(trimmed); len(m) > 0 {
			for _, g := range m[1:] {
				if g != "" {
					comment = g
					break
				}
			}
		}
		if comment == "" {
			comment = trimmed
		}

		rules = append(rules, ParsedFirewallRule{
			ID:        fmt.Sprintf("ipt-%d", idx),
			Chain:     chain,
			Policy:    policy,
			Proto:     proto,
			Port:      port,
			Source:    source,
			Interface: iface,
			Comment:   comment,
		})
		idx++
	}

	return rules
}

func tokenizeIptablesLine(line string) []string {
	var tokens []string
	var cur strings.Builder
	inQuote := rune(0)

	for _, r := range line {
		switch {
		case inQuote != 0:
			if r == inQuote {
				inQuote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '"' || r == '\'':
			inQuote = r
		case r == ' ' || r == '\t':
			if cur.Len() > 0 {
				tokens = append(tokens, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteRune(r)
		}
	}
	if cur.Len() > 0 {
		tokens = append(tokens, cur.String())
	}
	return tokens
}

func (p *Plugin) Read(ctx context.Context, r plugin.Runner, h plugin.HostFacts) (any, error) {
	if r == nil {
		return nil, fmt.Errorf("runner is nil")
	}

	cmd := "(sudo -n iptables -S 2>&1 || iptables -S 2>&1 || /usr/sbin/iptables -S 2>&1 || /sbin/iptables -S 2>&1 || echo 'NO_IPTABLES'); " +
		"echo '===IP6==='; " +
		"(sudo -n ip6tables -S 2>&1 || ip6tables -S 2>&1 || /usr/sbin/ip6tables -S 2>&1 || /sbin/ip6tables -S 2>&1 || echo 'NO_IP6TABLES')"
	out, err := r.RunCommand(ctx, cmd)
	if err != nil {
		return nil, err
	}

	parts := strings.Split(out, "===IP6===")
	v4Raw := strings.TrimSpace(parts[0])
	var v6Raw string
	if len(parts) > 1 {
		v6Raw = strings.TrimSpace(parts[1])
	}

	hasV4 := !strings.Contains(v4Raw, "NO_IPTABLES") && (strings.Contains(v4Raw, "-P ") || strings.Contains(v4Raw, "-A "))
	hasV6 := !strings.Contains(v6Raw, "NO_IP6TABLES") && (strings.Contains(v6Raw, "-P ") || strings.Contains(v6Raw, "-A "))

	var combined strings.Builder
	if hasV4 {
		combined.WriteString(v4Raw)
	}
	if hasV6 {
		if combined.Len() > 0 {
			combined.WriteString("\n")
		}
		combined.WriteString(v6Raw)
	}

	rules := ParseIptablesSave(combined.String())
	ports := []string{}
	for _, rule := range rules {
		if rule.Policy == "ACCEPT" && rule.Port != "" && rule.Port != "all" {
			ports = append(ports, fmt.Sprintf("%s/%s", rule.Port, rule.Proto))
		}
	}

	status := "inactive"
	if hasV4 || hasV6 || len(rules) > 0 {
		status = "active"
	}

	return map[string]any{
		"status":   status,
		"backend":  "iptables",
		"zones":    map[string]any{},
		"services": []string{},
		"ports":    ports,
		"rules":    rules,
	}, nil
}

func (p *Plugin) Actions() []plugin.ActionSpec {
	return []plugin.ActionSpec{
		{
			Name:        "allow_port",
			Label:       "Allow Port",
			Description: "Allow an incoming TCP/UDP port via ansible.builtin.iptables",
			Options: []plugin.OptionSchema{
				{Name: "port", Type: "string", Required: true, Description: "Destination port or range (e.g. 80, 443, 8000:8080)"},
				{Name: "proto", Type: "string", Required: false, Default: "tcp", Description: "Protocol: tcp or udp"},
				{Name: "chain", Type: "string", Required: false, Default: "INPUT", Description: "Target chain (e.g. INPUT, FORWARD)"},
				{Name: "source", Type: "string", Required: false, Description: "Optional source IP or CIDR"},
				{Name: "comment", Type: "string", Required: false, Description: "Optional rule comment"},
			},
		},
		{
			Name:        "deny_port",
			Label:       "Deny Port",
			Description: "Drop or reject traffic on a TCP/UDP port via ansible.builtin.iptables",
			Options: []plugin.OptionSchema{
				{Name: "port", Type: "string", Required: true, Description: "Destination port or range (e.g. 23, 8080)"},
				{Name: "proto", Type: "string", Required: false, Default: "tcp", Description: "Protocol: tcp or udp"},
				{Name: "chain", Type: "string", Required: false, Default: "INPUT", Description: "Target chain (e.g. INPUT)"},
				{Name: "jump", Type: "select", Enum: []string{"DROP", "REJECT"}, Default: "DROP", Description: "Target jump policy"},
				{Name: "source", Type: "string", Required: false, Description: "Optional source IP or CIDR"},
				{Name: "comment", Type: "string", Required: false, Description: "Optional rule comment"},
			},
		},
		{
			Name:        "delete_port",
			Label:       "Delete Port Rule",
			Description: "Remove an existing port rule from iptables via ansible.builtin.iptables",
			Options: []plugin.OptionSchema{
				{Name: "port", Type: "string", Required: true, Description: "Destination port or range to remove"},
				{Name: "proto", Type: "string", Required: false, Default: "tcp", Description: "Protocol: tcp or udp"},
				{Name: "chain", Type: "string", Required: false, Default: "INPUT", Description: "Target chain (e.g. INPUT)"},
				{Name: "jump", Type: "select", Enum: []string{"ACCEPT", "DROP", "REJECT"}, Default: "ACCEPT", Description: "Rule jump target to match"},
			},
		},
	}
}

func normalizePortSpec(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if idx := strings.Index(s, "/"); idx >= 0 {
		s = strings.TrimSpace(s[:idx])
	}
	if s == "" {
		return "", fmt.Errorf("port is required")
	}
	sep := ""
	if strings.Contains(s, "-") {
		sep = "-"
	} else if strings.Contains(s, ":") {
		sep = ":"
	}
	if sep != "" {
		parts := strings.Split(s, sep)
		if len(parts) != 2 {
			return "", fmt.Errorf("invalid port range %q", raw)
		}
		start, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
		end, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
		if err1 != nil || err2 != nil || start < 1 || start > 65535 || end < 1 || end > 65535 || start >= end {
			return "", fmt.Errorf("invalid port range %q", raw)
		}
		return fmt.Sprintf("%d:%d", start, end), nil
	}
	p, err := strconv.Atoi(s)
	if err != nil || p < 1 || p > 65535 {
		return "", fmt.Errorf("invalid port %q: must be 1-65535", raw)
	}
	return strconv.Itoa(p), nil
}

func (p *Plugin) Compile(h plugin.HostFacts, action string, opts map[string]any) (plugin.TaskSpec, error) {
	norm := strings.ToLower(strings.TrimSpace(action))
	switch norm {
	case "allow_port", "port", "deny_port", "delete_port":
		rawPort, _ := opts["port"].(string)
		portSpec, err := normalizePortSpec(rawPort)
		if err != nil {
			return plugin.TaskSpec{}, err
		}

		proto, _ := opts["proto"].(string)
		proto = strings.ToLower(strings.TrimSpace(proto))
		if proto == "" {
			if idx := strings.Index(rawPort, "/"); idx >= 0 {
				proto = strings.ToLower(strings.TrimSpace(rawPort[idx+1:]))
			}
		}
		if proto == "" {
			proto = "tcp"
		}
		if proto != "tcp" && proto != "udp" && proto != "sctp" {
			return plugin.TaskSpec{}, fmt.Errorf("invalid protocol %q: must be tcp, udp, or sctp", proto)
		}

		chain, _ := opts["chain"].(string)
		chain = strings.ToUpper(strings.TrimSpace(chain))
		if chain == "" {
			chain = "INPUT"
		}

		jump := "ACCEPT"
		state := "present"
		if norm == "deny_port" {
			jump = "DROP"
			if j, ok := opts["jump"].(string); ok && strings.TrimSpace(j) != "" {
				jump = strings.ToUpper(strings.TrimSpace(j))
			}
		} else if norm == "delete_port" {
			state = "absent"
			if j, ok := opts["jump"].(string); ok && strings.TrimSpace(j) != "" {
				jump = strings.ToUpper(strings.TrimSpace(j))
			}
		} else if st, ok := opts["state"].(string); ok && strings.EqualFold(strings.TrimSpace(st), "disabled") {
			state = "absent"
		}

		params := map[string]any{
			"chain":            chain,
			"protocol":         proto,
			"destination_port": portSpec,
			"jump":             jump,
			"state":            state,
			"become":           true,
		}
		if src, ok := opts["source"].(string); ok && strings.TrimSpace(src) != "" && !strings.EqualFold(strings.TrimSpace(src), "any") {
			params["source"] = strings.TrimSpace(src)
		}
		if comment, ok := opts["comment"].(string); ok && strings.TrimSpace(comment) != "" {
			params["comment"] = strings.TrimSpace(comment)
		}

		title := fmt.Sprintf("Configure iptables %s %s/%s on %s", jump, portSpec, proto, chain)
		if state == "absent" {
			title = fmt.Sprintf("Remove iptables rule %s/%s from %s", portSpec, proto, chain)
		}

		return plugin.TaskSpec{
			Name: title,
			Steps: []plugin.TaskStep{
				{
					Name:   title,
					Module: "ansible.builtin.iptables",
					Params: params,
				},
			},
		}, nil

	default:
		return plugin.TaskSpec{}, fmt.Errorf("unsupported iptables action: %s", action)
	}
}
