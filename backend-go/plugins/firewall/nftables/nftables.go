package nftables

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"

	"air-traffic-control/backend/pkg/plugin"
	"air-traffic-control/backend/pkg/plugin/newapi"
	"air-traffic-control/backend/pkg/plugin/settingsframework"
)

// SettingsPageID is the datacenter settings page identifier for firewall/nftables.
const SettingsPageID = "firewall/nftables"

var (
	_ newapi.DataCollectionPlugin        = (*Plugin)(nil)
	_ newapi.ManagementPlugin            = (*Plugin)(nil)
	_ newapi.CategorizedSettingsProvider = (*Plugin)(nil)
	_ newapi.ContextMenuProvider         = (*Plugin)(nil)
	_ newapi.TaskCompileProvider         = (*Plugin)(nil)
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

// CollectionSchedule returns the v2 polling and cache TTL schedule for nftables inspection.
func (p *Plugin) CollectionSchedule() newapi.CollectionSchedule {
	return newapi.DefaultScheduleForKind(p.Meta().Kind)
}

// CollectionSchema describes the v2 payload structure returned by Read.
func (p *Plugin) CollectionSchema() newapi.CollectionSchema {
	return newapi.CollectionSchema{
		Version:     "v2",
		RootKey:     "rules",
		Description: p.Meta().Description,
	}
}

// CategorizedSettingsPage declares datacenter-wide defaults for nftables rules.
func (p *Plugin) CategorizedSettingsPage() newapi.CategorizedSettingsPage {
	return settingsframework.NewPageBuilder(SettingsPageID, "nftables Firewall Defaults").
		WithIcon(p.Meta().Icon).
		WithCategory(string(p.Meta().Kind)).
		WithScope(newapi.SettingsScopeDatacenter).
		WithOrder(p.Meta().Order).
		WithDescription("Datacenter-wide default nftables table, chain, and verdict policies.").
		StringField("defaultTable", "Default Table", "inet filter",
			settingsframework.WithDescription("Default nftables family and table used when an action omits the table parameter.")).
		StringField("defaultChain", "Default Chain", "input",
			settingsframework.WithDescription("Default nftables chain used when an action omits the chain parameter.")).
		SelectField("defaultDenyVerdict", "Default Deny Verdict", "drop", []plugin.SettingsFieldOption{
			{Value: "drop", Label: "Drop (silent)"},
			{Value: "reject", Label: "Reject (ICMP unreachable)"},
		}, settingsframework.WithDescription("Default verdict applied by deny_port when no policy option is specified.")).
		BuildCategorized()
}

// SettingsPage implements plugin.SettingsPageProvider.
func (p *Plugin) SettingsPage() plugin.SettingsPage {
	return p.CategorizedSettingsPage().SettingsPage
}

// ContextMenuEntries provides contextual firewall actions for compatible hosts.
func (p *Plugin) ContextMenuEntries(ctx context.Context, h plugin.HostFacts) []newapi.ContextMenuEntry {
	if !p.Compatible(h) {
		return nil
	}
	return newapi.NewContextMenuBuilder(p.Meta().ID).
		WithTarget(newapi.TargetFirewall).
		WithGroup(string(p.Meta().Kind)).
		AddAction("allow_port", "Allow Port", "lock_open", "allow_port", 10).
		AddDangerAction("deny_port", "Deny Port", "block", "deny_port", "Drop or reject traffic on a port in nftables?", 20).
		Build()
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

// NATForwardPort describes one DNAT port-forward entry observed in a ruleset.
type NATForwardPort struct {
	Port   string `json:"port"`
	Proto  string `json:"proto"`
	ToPort string `json:"toport"`
	ToAddr string `json:"toaddr,omitempty"`
}

// nftDNATRe matches "tcp dport 8080 dnat to 10.0.0.5:80" and the address-less
// form "dnat to :80" (same-host forwarding).
var nftDNATRe = regexp.MustCompile(`(?:(\w+)\s+dport\s+(\S+)\s+)?dnat\s+to\s+([^\s:]*):(\S+)`)

// ParseNftNAT extracts NAT state from `nft list ruleset`: whether masquerade is
// present in a postrouting chain and which DNAT forwards exist in prerouting.
func ParseNftNAT(raw string) (bool, []NATForwardPort) {
	forwardPorts := []NATForwardPort{}
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || strings.Contains(trimmed, "NO_NFT") {
		return false, forwardPorts
	}

	// JSON rulesets are only scanned best-effort; the textual ruleset is the
	// authoritative source for masquerade / DNAT inspection.
	if strings.HasPrefix(trimmed, "{") {
		masquerade := strings.Contains(trimmed, `"masquerade"`)
		for _, m := range nftDNATRe.FindAllStringSubmatch(trimmed, -1) {
			fp := NATForwardPort{Proto: "tcp", Port: "all", ToPort: m[4], ToAddr: m[3]}
			if m[1] != "" {
				fp.Proto = strings.ToLower(m[1])
			}
			if m[2] != "" {
				fp.Port = m[2]
			}
			forwardPorts = append(forwardPorts, fp)
		}
		return masquerade, forwardPorts
	}

	masquerade := false
	chain := ""
	for _, line := range strings.Split(trimmed, "\n") {
		t := strings.TrimSpace(line)
		if m := nftChainRe.FindStringSubmatch(t); len(m) == 2 {
			chain = strings.ToLower(m[1])
			continue
		}
		if t == "}" {
			chain = ""
			continue
		}
		switch chain {
		case "postrouting":
			if strings.Contains(strings.ToLower(t), "masquerade") {
				masquerade = true
			}
		case "prerouting":
			m := nftDNATRe.FindStringSubmatch(t)
			if len(m) != 5 {
				continue
			}
			fp := NATForwardPort{Proto: "tcp", Port: "all", ToPort: m[4], ToAddr: m[3]}
			if m[1] != "" {
				fp.Proto = strings.ToLower(m[1])
			}
			if m[2] != "" {
				fp.Port = m[2]
			}
			forwardPorts = append(forwardPorts, fp)
		}
	}
	return masquerade, forwardPorts
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

	ownerOut, _ := r.RunCommand(ctx, "(sudo -n firewall-cmd --state 2>/dev/null || firewall-cmd --state 2>/dev/null || true); (sudo -n ufw status 2>/dev/null || ufw status 2>/dev/null || true)")
	ownerLower := strings.ToLower(ownerOut)
	if strings.Contains(ownerLower, "running") || strings.Contains(ownerLower, "status: active") {
		return plugin.Detection{
			Detected: false,
			Status:   "inactive",
			Details:  "firewalld/ufw owns packet filtering on host",
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
		Detected: false,
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
	masquerade, forwardPorts := ParseNftNAT(out)

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
		"masquerade":     masquerade,
		"forwardPorts":   forwardPorts,
	}, nil
}

// ManagementActions returns the structured v2 management action specifications for nftables.
func (p *Plugin) ManagementActions() []newapi.ManagementActionSpec {
	return []newapi.ManagementActionSpec{
		{
			ActionSpec: plugin.ActionSpec{
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
			Category:         newapi.ActionCategorySecurity,
			Idempotent:       false,
			Dangerous:        false,
			InvalidatesKinds: []plugin.Kind{plugin.KindFirewall},
		},
		{
			ActionSpec: plugin.ActionSpec{
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
			Category:         newapi.ActionCategorySecurity,
			Idempotent:       false,
			Dangerous:        true,
			ConfirmPrompt:    "Drop or reject traffic on the specified port in nftables?",
			InvalidatesKinds: []plugin.Kind{plugin.KindFirewall},
		},
		{
			ActionSpec: plugin.ActionSpec{
				Name:        "masquerade",
				Label:       "Toggle Masquerade",
				Description: "Enable or disable source NAT masquerade in the nftables postrouting chain (enables outbound routing for NAT'd guests)",
				Options: []plugin.OptionSchema{
					{Name: "enabled", Type: "bool", Required: false, Default: true, Description: "true enables masquerade, false removes it"},
					{Name: "table", Type: "string", Required: false, Default: "inet filter", Description: "Target nftables family and table"},
				},
			},
			Category:         newapi.ActionCategorySecurity,
			Idempotent:       true,
			Dangerous:        false,
			InvalidatesKinds: []plugin.Kind{plugin.KindFirewall},
		},
		{
			ActionSpec: plugin.ActionSpec{
				Name:        "forward_port",
				Label:       "Forward Port (DNAT)",
				Description: "Forward an external port to an internal host and port with an nftables prerouting DNAT rule plus a matching forward accept",
				Options: []plugin.OptionSchema{
					{Name: "port", Type: "string", Required: true, Description: "External port (single port, e.g. 8080)"},
					{Name: "proto", Type: "string", Required: false, Default: "tcp", Description: "Protocol: tcp or udp"},
					{Name: "toport", Type: "string", Required: true, Description: "Internal destination port (e.g. 80)"},
					{Name: "toaddr", Type: "string", Required: false, Description: "Internal destination IP (blank = same host)"},
					{Name: "table", Type: "string", Required: false, Default: "inet filter", Description: "Target nftables family and table"},
					{Name: "state", Type: "select", Enum: []string{"enabled", "disabled"}, Default: "enabled", Description: "Rule state"},
				},
			},
			Category:         newapi.ActionCategorySecurity,
			Idempotent:       true,
			Dangerous:        false,
			InvalidatesKinds: []plugin.Kind{plugin.KindFirewall},
		},
	}
}

func (p *Plugin) Actions() []plugin.ActionSpec {
	mgmt := p.ManagementActions()
	out := make([]plugin.ActionSpec, len(mgmt))
	for i, m := range mgmt {
		out[i] = m.ActionSpec
	}
	return out
}

// ValidateAction validates an nftables action name and option map against the v2 ManagementActionSpec.
func (p *Plugin) ValidateAction(h plugin.HostFacts, action string, opts map[string]any) error {
	norm := normalizeNFTAction(action)

	var matched *newapi.ManagementActionSpec
	for _, spec := range p.ManagementActions() {
		if strings.EqualFold(spec.Name, norm) {
			s := spec
			matched = &s
			break
		}
	}
	if matched == nil {
		return fmt.Errorf("unsupported nftables action: %s", action)
	}
	if err := newapi.ValidateManagementOptions(*matched, opts); err != nil {
		return err
	}

	switch norm {
	case "masquerade":
		_, err := resolveMasqueradeEnabled(opts)
		return err
	case "forward_port":
		_, err := parseNATForwardOptions(opts)
		return err
	}

	rawPort, _ := opts["port"].(string)
	if _, err := validateNftPort(rawPort); err != nil {
		return err
	}

	proto, _ := opts["proto"].(string)
	proto = strings.ToLower(strings.TrimSpace(proto))
	if proto == "" {
		if idx := strings.Index(rawPort, "/"); idx >= 0 {
			proto = strings.ToLower(strings.TrimSpace(rawPort[idx+1:]))
		}
	}
	if proto != "" && proto != "tcp" && proto != "udp" && proto != "sctp" {
		return fmt.Errorf("invalid protocol %q: must be tcp, udp, or sctp", proto)
	}

	if table, ok := opts["table"].(string); ok && strings.TrimSpace(table) != "" {
		if !safeNftIdentifierRe.MatchString(strings.TrimSpace(table)) {
			return fmt.Errorf("invalid nftables table %q", strings.TrimSpace(table))
		}
	}
	if chain, ok := opts["chain"].(string); ok && strings.TrimSpace(chain) != "" {
		if !safeNftIdentifierRe.MatchString(strings.TrimSpace(chain)) {
			return fmt.Errorf("invalid nftables chain %q", strings.TrimSpace(chain))
		}
	}

	return nil
}

func normalizeNFTAction(action string) string {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "port", "allow_port":
		return "allow_port"
	case "deny_port":
		return "deny_port"
	case "forward_port", "forward-port", "port_forward":
		return "forward_port"
	case "masquerade", "toggle_masquerade":
		return "masquerade"
	default:
		return strings.ToLower(strings.TrimSpace(action))
	}
}

// resolveMasqueradeEnabled reads the boolean "enabled" option (also accepting
// enabled/disabled strings and a "state" selector) for the masquerade action.
func resolveMasqueradeEnabled(opts map[string]any) (bool, error) {
	if raw, ok := opts["enabled"]; ok && raw != nil {
		switch v := raw.(type) {
		case bool:
			return v, nil
		case string:
			switch strings.ToLower(strings.TrimSpace(v)) {
			case "true", "enabled", "enable", "yes", "1", "on":
				return true, nil
			case "false", "disabled", "disable", "no", "0", "off":
				return false, nil
			}
		}
		return false, fmt.Errorf("invalid enabled value %v: must be a boolean or enabled/disabled", raw)
	}
	if st, ok := opts["state"].(string); ok && strings.TrimSpace(st) != "" {
		switch strings.ToLower(strings.TrimSpace(st)) {
		case "enabled", "present":
			return true, nil
		case "disabled", "absent":
			return false, nil
		}
		return false, fmt.Errorf("invalid state %q: must be enabled or disabled", st)
	}
	return true, nil
}

// natForwardSpec is the normalized form of a DNAT port-forward request.
type natForwardSpec struct {
	Port   string
	Proto  string
	ToPort string
	ToAddr string
	Absent bool
}

func parseNATForwardOptions(opts map[string]any) (natForwardSpec, error) {
	spec := natForwardSpec{Proto: "tcp"}

	rawPort, _ := opts["port"].(string)
	port, err := validateNftPort(rawPort)
	if err != nil {
		return spec, err
	}
	if strings.Contains(port, "-") {
		return spec, fmt.Errorf("port ranges are not supported for DNAT forwarding; provide a single external port")
	}
	spec.Port = port

	if rawProto, ok := opts["proto"].(string); ok && strings.TrimSpace(rawProto) != "" {
		proto := strings.ToLower(strings.TrimSpace(rawProto))
		if proto != "tcp" && proto != "udp" {
			return spec, fmt.Errorf("invalid protocol %q: must be tcp or udp", rawProto)
		}
		spec.Proto = proto
	}

	rawTo, _ := opts["toport"].(string)
	toPort, err := validateNftPort(rawTo)
	if err != nil {
		return spec, fmt.Errorf("internal destination port: %w", err)
	}
	if strings.Contains(toPort, "-") {
		return spec, fmt.Errorf("port ranges are not supported for DNAT forwarding; provide a single internal port")
	}
	spec.ToPort = toPort

	if rawAddr, ok := opts["toaddr"].(string); ok && strings.TrimSpace(rawAddr) != "" {
		addr := strings.TrimSpace(rawAddr)
		if net.ParseIP(addr) == nil {
			return spec, fmt.Errorf("invalid destination address %q: must be an IP address", addr)
		}
		spec.ToAddr = addr
	}

	if st, ok := opts["state"].(string); ok && strings.EqualFold(strings.TrimSpace(st), "disabled") {
		spec.Absent = true
	}
	if en, ok := opts["enabled"].(bool); ok && !en {
		spec.Absent = true
	}

	return spec, nil
}

// resolveNftTable resolves and validates the "<family> <table>" target.
func resolveNftTable(opts map[string]any) (string, error) {
	table, _ := opts["table"].(string)
	table = strings.TrimSpace(table)
	if table == "" {
		table = defaultNftTable()
	}
	if !safeNftIdentifierRe.MatchString(table) || len(strings.Fields(table)) != 2 {
		return "", fmt.Errorf("invalid nftables table %q: expected '<family> <table>'", table)
	}
	return table, nil
}

// nftDeleteByMatch removes every rule in chain whose printed form contains match.
// nftables has no match-based delete, so handles are resolved first (last field
// of `nft -a list chain` output is the handle number).
func nftDeleteByMatch(table, chain, match string) string {
	return fmt.Sprintf(
		"nft -a list chain %s %s 2>/dev/null | awk 'index($0,\"%s\"){print $NF}' | while read -r h; do nft delete rule %s %s handle \"$h\"; done || true",
		table, chain, match, table, chain,
	)
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

func defaultNftTable() string {
	return settingsframework.NewStoreAdapter(nil).StringOrDefault(SettingsPageID, "defaultTable", "inet filter")
}

func defaultNftChain() string {
	return settingsframework.NewStoreAdapter(nil).StringOrDefault(SettingsPageID, "defaultChain", "input")
}

func defaultNftDenyVerdict() string {
	v := strings.ToLower(strings.TrimSpace(settingsframework.NewStoreAdapter(nil).StringOrDefault(SettingsPageID, "defaultDenyVerdict", "drop")))
	if v == "reject" {
		return "reject"
	}
	return "drop"
}

func (p *Plugin) Compile(h plugin.HostFacts, action string, opts map[string]any) (plugin.TaskSpec, error) {
	if err := p.ValidateAction(h, action, opts); err != nil {
		return plugin.TaskSpec{}, err
	}

	norm := normalizeNFTAction(action)
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
			table = defaultNftTable()
		}
		if !safeNftIdentifierRe.MatchString(table) {
			return plugin.TaskSpec{}, fmt.Errorf("invalid nftables table %q", table)
		}

		chain, _ := opts["chain"].(string)
		chain = strings.TrimSpace(chain)
		if chain == "" {
			chain = defaultNftChain()
		}
		if !safeNftIdentifierRe.MatchString(chain) {
			return plugin.TaskSpec{}, fmt.Errorf("invalid nftables chain %q", chain)
		}

		verdict := "accept"
		if norm == "deny_port" {
			verdict = defaultNftDenyVerdict()
			if pol, ok := opts["policy"].(string); ok && strings.TrimSpace(pol) != "" {
				if strings.EqualFold(strings.TrimSpace(pol), "reject") {
					verdict = "reject"
				} else {
					verdict = "drop"
				}
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

	case "masquerade":
		enabled, err := resolveMasqueradeEnabled(opts)
		if err != nil {
			return plugin.TaskSpec{}, err
		}
		table, err := resolveNftTable(opts)
		if err != nil {
			return plugin.TaskSpec{}, err
		}

		verb := "Enable"
		if !enabled {
			verb = "Disable"
		}
		title := fmt.Sprintf("%s nftables masquerade in %s postrouting", verb, table)

		cmds := []string{
			fmt.Sprintf("nft list table %s >/dev/null 2>&1 || nft add table %s", table, table),
			fmt.Sprintf("nft list chain %s postrouting >/dev/null 2>&1 || nft add chain %s postrouting '{ type nat hook postrouting priority srcnat; policy accept; }'", table, table),
		}
		if enabled {
			cmds = append(cmds, fmt.Sprintf("nft list chain %s postrouting | grep -qw masquerade || nft add rule %s postrouting masquerade", table, table))
		} else {
			cmds = append(cmds, nftDeleteByMatch(table, "postrouting", "masquerade"))
		}

		// Documented last resort (AGENTS.md §5): no native Ansible module mutates
		// individual nftables rules, and the idempotent "create only if missing"
		// guards require shell composition (pipes, conditionals).
		return plugin.TaskSpec{
			Name: title,
			Steps: []plugin.TaskStep{
				{
					Name:   title,
					Module: "ansible.builtin.shell",
					Params: map[string]any{
						"cmd":    strings.Join(cmds, "; "),
						"become": true,
					},
				},
			},
		}, nil

	case "forward_port":
		spec, err := parseNATForwardOptions(opts)
		if err != nil {
			return plugin.TaskSpec{}, err
		}
		table, err := resolveNftTable(opts)
		if err != nil {
			return plugin.TaskSpec{}, err
		}

		dnatRule := fmt.Sprintf("%s dport %s dnat to %s:%s", spec.Proto, spec.Port, spec.ToAddr, spec.ToPort)
		acceptRule := fmt.Sprintf("%s dport %s accept", spec.Proto, spec.ToPort)
		if spec.ToAddr != "" {
			acceptRule = fmt.Sprintf("ip daddr %s %s dport %s accept", spec.ToAddr, spec.Proto, spec.ToPort)
		}

		target := "same host"
		if spec.ToAddr != "" {
			target = spec.ToAddr
		}

		if spec.Absent {
			title := fmt.Sprintf("Remove nftables port forward %s/%s to %s:%s", spec.Port, spec.Proto, target, spec.ToPort)
			// Documented last resort (AGENTS.md §5): match-based removal needs
			// handle resolution through shell composition.
			return plugin.TaskSpec{
				Name: title,
				Steps: []plugin.TaskStep{
					{
						Name:   title,
						Module: "ansible.builtin.shell",
						Params: map[string]any{
							"cmd":    strings.Join([]string{nftDeleteByMatch(table, "prerouting", dnatRule), nftDeleteByMatch(table, "forward", acceptRule)}, "; "),
							"become": true,
						},
					},
				},
			}, nil
		}

		title := fmt.Sprintf("Forward nftables %s/%s to %s:%s", spec.Port, spec.Proto, target, spec.ToPort)
		cmds := []string{
			fmt.Sprintf("nft list table %s >/dev/null 2>&1 || nft add table %s", table, table),
			fmt.Sprintf("nft list chain %s prerouting >/dev/null 2>&1 || nft add chain %s prerouting '{ type nat hook prerouting priority dstnat; policy accept; }'", table, table),
			fmt.Sprintf("nft list chain %s forward >/dev/null 2>&1 || nft add chain %s forward '{ type filter hook forward priority filter; policy accept; }'", table, table),
			fmt.Sprintf("nft list chain %s prerouting | grep -Fq '%s' || nft add rule %s prerouting %s", table, dnatRule, table, dnatRule),
			fmt.Sprintf("nft list chain %s forward | grep -Fq '%s' || nft add rule %s forward %s", table, acceptRule, table, acceptRule),
		}

		return plugin.TaskSpec{
			Name: title,
			Steps: []plugin.TaskStep{
				{
					Name:   title,
					Module: "ansible.builtin.shell",
					Params: map[string]any{
						"cmd":    strings.Join(cmds, "; "),
						"become": true,
					},
				},
			},
		}, nil

	default:
		return plugin.TaskSpec{}, fmt.Errorf("unsupported nftables action: %s", action)
	}
}

// CompileTask implements newapi.TaskCompileProvider so typed TaskRequests can compile directly via the plugin.
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
