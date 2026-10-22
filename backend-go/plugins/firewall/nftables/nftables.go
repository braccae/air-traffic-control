package nftables

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"air-traffic-control/backend/pkg/plugin"
)

func init() {
	_ = plugin.Register(New())
}

// Plugin implements raw nftables rule set inspection and mutation.
type Plugin struct{}

func New() *Plugin {
	return &Plugin{}
}

func (p *Plugin) Meta() plugin.Meta {
	return plugin.Meta{
		ID:          "firewall/nftables",
		Kind:        plugin.KindFirewall,
		Name:        "nftables",
		Label:       "nftables",
		Icon:        "security",
		Description: "Linux nftables packet classification tables, chains, and rule sets",
		Order:       12,
	}
}

func (p *Plugin) Compatible(h plugin.HostFacts) bool {
	return h.OSFamily != "windows" && h.OSFamily != "router_switch"
}

func (p *Plugin) Requirements() plugin.Requirements {
	return plugin.Requirements{
		HostPackages: map[string][]string{
			"dnf": {"nftables"},
			"apt": {"nftables"},
			"apk": {"nftables"},
		},
		ControllerCollections: []string{
			"ansible.posix",
		},
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

	binOut, err := r.RunCommand(ctx, "command -v nft || ls /usr/sbin/nft /sbin/nft 2>/dev/null || true")
	if err != nil {
		return plugin.Detection{Detected: false, Status: "ERROR", Error: err.Error()}, err
	}
	if strings.TrimSpace(binOut) == "" {
		return plugin.Detection{
			Detected: false,
			Status:   "unavailable",
			Details:  "nft binary not found on host",
		}, nil
	}

	tablesOut, err := r.RunCommand(ctx, "sudo -n nft list tables 2>/dev/null || nft list tables 2>/dev/null || true")
	if err != nil {
		return plugin.Detection{Detected: false, Status: "ERROR", Error: err.Error()}, err
	}

	tableCount := 0
	for _, line := range strings.Split(strings.TrimSpace(tablesOut), "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "table ") {
			tableCount++
		}
	}

	if tableCount > 0 {
		return plugin.Detection{
			Detected: true,
			Status:   "active",
			Details:  fmt.Sprintf("nftables rule set active (%d tables)", tableCount),
		}, nil
	}

	return plugin.Detection{
		Detected: true,
		Status:   "inactive",
		Details:  "nftables installed but no tables loaded",
	}, nil
}

var (
	nftTableRe   = regexp.MustCompile(`^table\s+(\S+)\s+(\S+)`)
	nftChainRe   = regexp.MustCompile(`^chain\s+(\S+)`)
	nftProtoPort = regexp.MustCompile(`\b(tcp|udp|sctp)\s+dport\s+(\{[^}]+\}|\S+)`)
	nftPolicyRe  = regexp.MustCompile(`\b(accept|drop|reject)\b`)
	nftSourceRe  = regexp.MustCompile(`\bip6?\s+saddr\s+(\S+)`)
	nftIfaceRe   = regexp.MustCompile(`\biif(?:name)?\s+"?([^"\s]+)"?`)
	nftCommentRe = regexp.MustCompile(`comment\s+"([^"]+)"`)
	nftHandleRe  = regexp.MustCompile(`#\s*handle\s+(\d+)`)
)

// ParseNftRuleset parses `nft list ruleset` or `nft -j list ruleset` output into ParsedFirewallRule entries.
func ParseNftRuleset(raw string) ([]ParsedFirewallRule, []string) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || strings.Contains(trimmed, "NO_NFT") {
		return []ParsedFirewallRule{}, []string{}
	}

	if strings.HasPrefix(trimmed, "{") {
		if rules, tables, ok := parseNftJSON(trimmed); ok {
			return rules, tables
		}
	}

	rules := []ParsedFirewallRule{}
	tables := []string{}
	seenTables := map[string]bool{}

	currentTable := ""
	currentChain := ""
	inChain := false
	idx := 1

	for _, line := range strings.Split(trimmed, "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}

		if m := nftTableRe.FindStringSubmatch(t); len(m) == 3 {
			currentTable = m[1] + " " + m[2]
			if !seenTables[currentTable] {
				seenTables[currentTable] = true
				tables = append(tables, currentTable)
			}
			inChain = false
			continue
		}

		if m := nftChainRe.FindStringSubmatch(t); len(m) == 2 {
			currentChain = m[1]
			inChain = true
			continue
		}

		if t == "}" {
			if inChain {
				inChain = false
				currentChain = ""
			}
			continue
		}

		if !inChain || strings.HasPrefix(t, "type ") || strings.HasPrefix(t, "policy ") {
			continue
		}

		polMatch := nftPolicyRe.FindStringSubmatch(t)
		if len(polMatch) == 0 {
			continue
		}
		policy := strings.ToUpper(polMatch[1])

		proto := "all"
		port := "all"
		if pm := nftProtoPort.FindStringSubmatch(t); len(pm) == 3 {
			proto = strings.ToLower(pm[1])
			port = strings.Trim(pm[2], "{} ")
		} else if strings.Contains(t, "icmp") {
			proto = "icmp"
			port = "icmp"
		}

		source := "0.0.0.0/0"
		if sm := nftSourceRe.FindStringSubmatch(t); len(sm) == 2 {
			source = sm[1]
		}

		iface := "all"
		if im := nftIfaceRe.FindStringSubmatch(t); len(im) == 2 {
			iface = im[1]
		}

		comment := t
		if cm := nftCommentRe.FindStringSubmatch(t); len(cm) == 2 {
			comment = cm[1]
		}

		ruleID := fmt.Sprintf("nft-%d", idx)
		if hm := nftHandleRe.FindStringSubmatch(t); len(hm) == 2 {
			ruleID = fmt.Sprintf("nft-handle-%s", hm[1])
		}

		chainName := strings.ToUpper(currentChain)
		if chainName == "" {
			chainName = "INPUT"
		}

		rules = append(rules, ParsedFirewallRule{
			ID:        ruleID,
			Chain:     chainName,
			Policy:    policy,
			Proto:     proto,
			Port:      port,
			Source:    source,
			Interface: iface,
			Comment:   comment,
		})
		idx++
	}

	return rules, tables
}

func parseNftJSON(raw string) ([]ParsedFirewallRule, []string, bool) {
	var payload struct {
		Nftables []map[string]any `json:"nftables"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil || len(payload.Nftables) == 0 {
		return nil, nil, false
	}

	rules := []ParsedFirewallRule{}
	tables := []string{}
	seenTables := map[string]bool{}
	idx := 1

	for _, item := range payload.Nftables {
		if tbl, ok := item["table"].(map[string]any); ok {
			fam, _ := tbl["family"].(string)
			name, _ := tbl["name"].(string)
			key := strings.TrimSpace(fam + " " + name)
			if key != "" && !seenTables[key] {
				seenTables[key] = true
				tables = append(tables, key)
			}
			continue
		}
		ruleObj, ok := item["rule"].(map[string]any)
		if !ok {
			continue
		}
		chain, _ := ruleObj["chain"].(string)
		if chain == "" {
			chain = "INPUT"
		} else {
			chain = strings.ToUpper(chain)
		}
		comment, _ := ruleObj["comment"].(string)
		policy := "ACCEPT"
		proto := "all"
		port := "all"
		source := "0.0.0.0/0"
		iface := "all"

		if exprs, ok := ruleObj["expr"].([]any); ok {
			for _, ex := range exprs {
				em, ok := ex.(map[string]any)
				if !ok {
					continue
				}
				if _, ok := em["accept"]; ok {
					policy = "ACCEPT"
				} else if _, ok := em["drop"]; ok {
					policy = "DROP"
				} else if _, ok := em["reject"]; ok {
					policy = "REJECT"
				}
				if m, ok := em["match"].(map[string]any); ok {
					left, _ := m["left"].(map[string]any)
					right := m["right"]
					if payloadMap, ok := left["payload"].(map[string]any); ok {
						protocol, _ := payloadMap["protocol"].(string)
						field, _ := payloadMap["field"].(string)
						if field == "dport" {
							proto = strings.ToLower(protocol)
							port = fmt.Sprint(right)
						} else if field == "saddr" {
							source = fmt.Sprint(right)
						}
					}
					if metaMap, ok := left["meta"].(map[string]any); ok {
						if k, _ := metaMap["key"].(string); k == "iifname" || k == "iif" {
							iface = fmt.Sprint(right)
						}
					}
				}
			}
		}

		ruleID := fmt.Sprintf("nft-%d", idx)
		if h, ok := ruleObj["handle"].(float64); ok {
			ruleID = fmt.Sprintf("nft-handle-%d", int(h))
		}
		if comment == "" {
			comment = fmt.Sprintf("%s %s %s/%s", chain, policy, port, proto)
		}

		rules = append(rules, ParsedFirewallRule{
			ID:        ruleID,
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

	return rules, tables, true
}

func (p *Plugin) Read(ctx context.Context, r plugin.Runner, h plugin.HostFacts) (any, error) {
	if r == nil {
		return nil, fmt.Errorf("runner is nil")
	}

	cmd := "sudo -n nft list ruleset 2>&1 || nft list ruleset 2>&1 || echo 'NO_NFT'"
	out, err := r.RunCommand(ctx, cmd)
	if err != nil {
		return nil, err
	}

	rules, tables := ParseNftRuleset(out)
	ports := []string{}
	for _, rule := range rules {
		if rule.Policy == "ACCEPT" && rule.Port != "" && rule.Port != "all" {
			ports = append(ports, fmt.Sprintf("%s/%s", rule.Port, rule.Proto))
		}
	}

	status := "inactive"
	if len(tables) > 0 || len(rules) > 0 {
		status = "active"
	}

	return map[string]any{
		"status":         status,
		"nftablesActive": len(tables) > 0,
		"backend":        "nftables",
		"tables":         tables,
		"zones":          map[string]any{},
		"services":       []string{},
		"ports":          ports,
		"rules":          rules,
	}, nil
}

func (p *Plugin) Actions() []plugin.ActionSpec {
	return []plugin.ActionSpec{
		{
			Name:        "allow_port",
			Label:       "Allow Port",
			Description: "Allow an incoming TCP/UDP port in the nftables inet filter input chain",
			Options: []plugin.OptionSchema{
				{Name: "port", Type: "string", Required: true, Description: "Destination port or range (e.g. 80, 443, 8000-8080)"},
				{Name: "proto", Type: "string", Required: false, Default: "tcp", Description: "Protocol: tcp or udp"},
				{Name: "table", Type: "string", Required: false, Default: "inet filter", Description: "Target nftables family and table"},
				{Name: "chain", Type: "string", Required: false, Default: "input", Description: "Target nftables chain"},
				{Name: "comment", Type: "string", Required: false, Description: "Optional rule comment"},
			},
		},
		{
			Name:        "deny_port",
			Label:       "Deny Port",
			Description: "Drop traffic on a TCP/UDP port in the nftables inet filter input chain",
			Options: []plugin.OptionSchema{
				{Name: "port", Type: "string", Required: true, Description: "Destination port or range (e.g. 23, 8080)"},
				{Name: "proto", Type: "string", Required: false, Default: "tcp", Description: "Protocol: tcp or udp"},
				{Name: "table", Type: "string", Required: false, Default: "inet filter", Description: "Target nftables family and table"},
				{Name: "chain", Type: "string", Required: false, Default: "input", Description: "Target nftables chain"},
				{Name: "policy", Type: "select", Enum: []string{"drop", "reject"}, Default: "drop", Description: "Verdict (drop or reject)"},
			},
		},
	}
}

var safeNftIdentifierRe = regexp.MustCompile(`^[a-zA-Z0-9_\-\s]+$`)

func validateNftPort(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if idx := strings.Index(s, "/"); idx >= 0 {
		s = strings.TrimSpace(s[:idx])
	}
	if s == "" {
		return "", fmt.Errorf("port is required")
	}
	if strings.Contains(s, "-") {
		parts := strings.Split(s, "-")
		if len(parts) != 2 {
			return "", fmt.Errorf("invalid port range %q", raw)
		}
		start, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
		end, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
		if err1 != nil || err2 != nil || start < 1 || start > 65535 || end < 1 || end > 65535 || start >= end {
			return "", fmt.Errorf("invalid port range %q", raw)
		}
		return fmt.Sprintf("%d-%d", start, end), nil
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
	case "allow_port", "port", "deny_port":
		rawPort, _ := opts["port"].(string)
		portSpec, err := validateNftPort(rawPort)
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

		table, _ := opts["table"].(string)
		table = strings.TrimSpace(table)
		if table == "" {
			table = "inet filter"
		}
		if !safeNftIdentifierRe.MatchString(table) {
			return plugin.TaskSpec{}, fmt.Errorf("invalid nftables table %q", table)
		}

		chain, _ := opts["chain"].(string)
		chain = strings.TrimSpace(chain)
		if chain == "" {
			chain = "input"
		}
		if !safeNftIdentifierRe.MatchString(chain) {
			return plugin.TaskSpec{}, fmt.Errorf("invalid nftables chain %q", chain)
		}

		verdict := "accept"
		if norm == "deny_port" {
			verdict = "drop"
			if pol, ok := opts["policy"].(string); ok && strings.EqualFold(strings.TrimSpace(pol), "reject") {
				verdict = "reject"
			}
		}

		// Documented last resort (AGENTS.md §5): Ansible core and standard community.general
		// do not ship a native rule-level nftables mutation module (only whole-file ruleset
		// replacement templates), so `ansible.builtin.command` with `nft add rule` is used
		// as an explicit last resort to append the rule atomically without clobbering existing tables.
		nftCmd := fmt.Sprintf("nft add rule %s %s %s dport %s %s", table, chain, proto, portSpec, verdict)
		title := fmt.Sprintf("nftables %s %s/%s in %s %s", verdict, portSpec, proto, table, chain)

		return plugin.TaskSpec{
			Name: title,
			Steps: []plugin.TaskStep{
				{
					Name:   title,
					Module: "ansible.builtin.command",
					Params: map[string]any{
						"cmd":    nftCmd,
						"become": true,
					},
				},
			},
		}, nil

	default:
		return plugin.TaskSpec{}, fmt.Errorf("unsupported nftables action: %s", action)
	}
}
