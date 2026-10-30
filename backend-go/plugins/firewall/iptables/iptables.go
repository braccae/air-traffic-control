package iptables

import (
	"context"
	"fmt"
	"net"
	"regexp"
	"strconv"
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

func (p *Plugin) CollectionSchedule() newapi.CollectionSchedule {
	return newapi.DefaultScheduleForKind(p.Meta().Kind)
}

func (p *Plugin) CollectionSchema() newapi.CollectionSchema {
	return newapi.CollectionSchema{
		Version:     "v2",
		RootKey:     "rules",
		Description: p.Meta().Description,
	}
}

func (p *Plugin) CategorizedSettingsPage() newapi.CategorizedSettingsPage {
	meta := p.Meta()
	return settingsframework.NewPageBuilder(meta.ID, "iptables Firewall Defaults").
		WithIcon(meta.Icon).
		WithCategory(string(meta.Kind)).
		WithScope(newapi.SettingsScopeDatacenter).
		WithOrder(meta.Order).
		WithDescription("Datacenter-wide iptables / ip6tables default chain, protocol, and jump policy settings.").
		SelectField("defaultChain", "Default Chain", "INPUT", []plugin.SettingsFieldOption{
			{Value: "INPUT", Label: "INPUT"},
			{Value: "FORWARD", Label: "FORWARD"},
			{Value: "OUTPUT", Label: "OUTPUT"},
		}, settingsframework.WithDescription("Default netfilter chain used when compiling iptables rules without an explicit chain.")).
		SelectField("defaultProto", "Default Protocol", "tcp", []plugin.SettingsFieldOption{
			{Value: "tcp", Label: "TCP"},
			{Value: "udp", Label: "UDP"},
			{Value: "sctp", Label: "SCTP"},
		}, settingsframework.WithDescription("Default transport protocol used when compiling iptables port rules.")).
		SelectField("defaultDenyJump", "Default Deny Jump Target", "DROP", []plugin.SettingsFieldOption{
			{Value: "DROP", Label: "DROP"},
			{Value: "REJECT", Label: "REJECT"},
		}, settingsframework.WithDescription("Default jump target when denying traffic on a port.")).
		BuildCategorized()
}

func (p *Plugin) SettingsPage() plugin.SettingsPage {
	return p.CategorizedSettingsPage().SettingsPage
}

func defaultIptablesChain() string {
	return settingsframework.NewStoreAdapter(nil).StringOrDefault("firewall/iptables", "defaultChain", "INPUT")
}

func defaultIptablesProtocol() string {
	return settingsframework.NewStoreAdapter(nil).StringOrDefault("firewall/iptables", "defaultProto", "tcp")
}

func (p *Plugin) ContextMenuEntries(ctx context.Context, h plugin.HostFacts) []newapi.ContextMenuEntry {
	if !p.Compatible(h) {
		return nil
	}
	return newapi.NewContextMenuBuilder(p.Meta().ID).
		WithTarget(newapi.TargetFirewall).
		WithGroup(string(p.Meta().Kind)).
		AddAction("allow_port", "Allow Port", "lock_open", "allow_port", 10).
		AddAction("deny_port", "Deny Port", "block", "deny_port", 20).
		AddDangerAction("delete_port", "Delete Port Rule", "delete_forever", "delete_port",
			"Delete this iptables port rule from the host firewall?", 90).
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

// NATForwardPort describes one DNAT port-forward entry observed in the nat table.
type NATForwardPort struct {
	Port   string `json:"port"`
	Proto  string `json:"proto"`
	ToPort string `json:"toport"`
	ToAddr string `json:"toaddr,omitempty"`
}

// ParseIptablesNAT inspects `iptables -t nat -S` output for masquerade on
// POSTROUTING and DNAT forwards on PREROUTING.
func ParseIptablesNAT(raw string) (bool, []NATForwardPort) {
	forwardPorts := []NATForwardPort{}
	masquerade := false

	for _, line := range strings.Split(raw, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "-A ") {
			continue
		}
		fields := tokenizeIptablesLine(trimmed)
		chain := ""
		if len(fields) > 1 {
			chain = fields[1]
		}
		proto := "tcp"
		port := "all"
		jump := ""
		toDest := ""

		for i := 0; i < len(fields); i++ {
			switch fields[i] {
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
			case "-j":
				if i+1 < len(fields) {
					jump = strings.ToUpper(fields[i+1])
					i++
				}
			case "--to-destination", "--to":
				if i+1 < len(fields) {
					toDest = fields[i+1]
					i++
				}
			}
		}

		if chain == "POSTROUTING" && jump == "MASQUERADE" {
			masquerade = true
			continue
		}
		if chain == "PREROUTING" && jump == "DNAT" && toDest != "" {
			fp := NATForwardPort{Port: port, Proto: proto, ToPort: toDest}
			if idx := strings.LastIndex(toDest, ":"); idx >= 0 {
				fp.ToAddr = strings.Trim(toDest[:idx], "[]")
				fp.ToPort = toDest[idx+1:]
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
			Detected: false,
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
		Detected: false,
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
		"(sudo -n ip6tables -S 2>&1 || ip6tables -S 2>&1 || /usr/sbin/ip6tables -S 2>&1 || /sbin/ip6tables -S 2>&1 || echo 'NO_IP6TABLES'); " +
		"echo '===NAT==='; " +
		"(sudo -n iptables -t nat -S 2>&1 || iptables -t nat -S 2>&1 || echo 'NO_IPTABLES')"
	out, err := r.RunCommand(ctx, cmd)
	if err != nil {
		return nil, err
	}

	parts := strings.Split(out, "===IP6===")
	v4Raw := strings.TrimSpace(parts[0])
	var v6Raw, natRaw string
	if len(parts) > 1 {
		rest := strings.SplitN(parts[1], "===NAT===", 2)
		v6Raw = strings.TrimSpace(rest[0])
		if len(rest) > 1 {
			natRaw = strings.TrimSpace(rest[1])
		}
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
	masquerade, forwardPorts := ParseIptablesNAT(natRaw)

	status := "inactive"
	if hasV4 || hasV6 || len(rules) > 0 {
		status = "active"
	}

	return map[string]any{
		"status":       status,
		"backend":      "iptables",
		"zones":        map[string]any{},
		"services":     []string{},
		"ports":        ports,
		"rules":        rules,
		"masquerade":   masquerade,
		"forwardPorts": forwardPorts,
	}, nil
}

func (p *Plugin) ManagementActions() []newapi.ManagementActionSpec {
	return []newapi.ManagementActionSpec{
		{
			ActionSpec: plugin.ActionSpec{
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
			Category:         newapi.ActionCategorySecurity,
			Idempotent:       true,
			InvalidatesKinds: []plugin.Kind{plugin.KindFirewall},
		},
		{
			ActionSpec: plugin.ActionSpec{
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
			Category:         newapi.ActionCategorySecurity,
			Idempotent:       true,
			InvalidatesKinds: []plugin.Kind{plugin.KindFirewall},
		},
		{
			ActionSpec: plugin.ActionSpec{
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
			Category:         newapi.ActionCategorySecurity,
			Idempotent:       true,
			Dangerous:        true,
			ConfirmPrompt:    "Delete this iptables port rule from the host firewall?",
			InvalidatesKinds: []plugin.Kind{plugin.KindFirewall},
		},
		{
			ActionSpec: plugin.ActionSpec{
				Name:        "masquerade",
				Label:       "Toggle Masquerade",
				Description: "Enable or disable source NAT masquerade on the nat POSTROUTING chain",
				Options: []plugin.OptionSchema{
					{Name: "enabled", Type: "bool", Required: false, Default: true, Description: "true adds -j MASQUERADE, false removes it"},
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
				Description: "Forward an external port to an internal host and port with nat PREROUTING DNAT plus a matching FORWARD accept",
				Options: []plugin.OptionSchema{
					{Name: "port", Type: "string", Required: true, Description: "External port (single port, e.g. 8080)"},
					{Name: "proto", Type: "string", Required: false, Default: "tcp", Description: "Protocol: tcp or udp"},
					{Name: "toport", Type: "string", Required: true, Description: "Internal destination port (e.g. 80)"},
					{Name: "toaddr", Type: "string", Required: false, Description: "Internal destination IP (blank = same host)"},
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
	specs := make([]plugin.ActionSpec, len(mgmt))
	for i, m := range mgmt {
		specs[i] = m.ActionSpec
	}
	return specs
}

func (p *Plugin) ValidateAction(h plugin.HostFacts, action string, opts map[string]any) error {
	norm := normalizeIPTablesAction(action)
	for _, spec := range p.ManagementActions() {
		if strings.EqualFold(spec.Name, norm) {
			if err := newapi.ValidateManagementOptions(spec, opts); err != nil {
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
			return nil
		}
	}
	return fmt.Errorf("unsupported iptables action: %s", action)
}

func normalizeIPTablesAction(action string) string {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "port", "allow_port":
		return "allow_port"
	case "deny_port":
		return "deny_port"
	case "delete_port":
		return "delete_port"
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
	port, err := normalizePortSpec(rawPort)
	if err != nil {
		return spec, err
	}
	if strings.Contains(port, ":") {
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
	toPort, err := normalizePortSpec(rawTo)
	if err != nil {
		return spec, fmt.Errorf("internal destination port: %w", err)
	}
	if strings.Contains(toPort, ":") {
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
	norm := normalizeIPTablesAction(action)
	switch norm {
	case "allow_port", "port", "deny_port", "delete_port":
		if err := p.ValidateAction(h, norm, opts); err != nil {
			return plugin.TaskSpec{}, err
		}
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
			proto = defaultIptablesProtocol()
		}
		if proto != "tcp" && proto != "udp" && proto != "sctp" {
			return plugin.TaskSpec{}, fmt.Errorf("invalid protocol %q: must be tcp, udp, or sctp", proto)
		}

		chain, _ := opts["chain"].(string)
		chain = strings.ToUpper(strings.TrimSpace(chain))
		if chain == "" {
			chain = defaultIptablesChain()
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

	case "masquerade":
		if err := p.ValidateAction(h, norm, opts); err != nil {
			return plugin.TaskSpec{}, err
		}
		enabled, err := resolveMasqueradeEnabled(opts)
		if err != nil {
			return plugin.TaskSpec{}, err
		}

		// Documented last resort (AGENTS.md §5): no native Ansible module covers
		// the paired NAT/FORWARD rule set this action manages. The explicit
		// `-C ... || -A ...` guard keeps the mutation idempotent without
		// flushing or reordering the operator's existing nat table.
		cmd := "iptables -t nat -C POSTROUTING -j MASQUERADE 2>/dev/null || iptables -t nat -A POSTROUTING -j MASQUERADE"
		title := "Enable iptables masquerade on nat POSTROUTING"
		if !enabled {
			cmd = "iptables -t nat -C POSTROUTING -j MASQUERADE 2>/dev/null && iptables -t nat -D POSTROUTING -j MASQUERADE || true"
			title = "Disable iptables masquerade on nat POSTROUTING"
		}

		return plugin.TaskSpec{
			Name: title,
			Steps: []plugin.TaskStep{
				{
					Name:   title,
					Module: "ansible.builtin.shell",
					Params: map[string]any{
						"cmd":    cmd,
						"become": true,
					},
				},
			},
		}, nil

	case "forward_port":
		if err := p.ValidateAction(h, norm, opts); err != nil {
			return plugin.TaskSpec{}, err
		}
		spec, err := parseNATForwardOptions(opts)
		if err != nil {
			return plugin.TaskSpec{}, err
		}

		destination := fmt.Sprintf("%s:%s", spec.ToAddr, spec.ToPort)
		dnatRule := fmt.Sprintf("-p %s --dport %s -j DNAT --to-destination %s", spec.Proto, spec.Port, destination)
		forwardRule := fmt.Sprintf("-p %s --dport %s -j ACCEPT", spec.Proto, spec.ToPort)
		if spec.ToAddr != "" {
			forwardRule = fmt.Sprintf("-p %s -d %s --dport %s -j ACCEPT", spec.Proto, spec.ToAddr, spec.ToPort)
		}

		target := "same host"
		if spec.ToAddr != "" {
			target = spec.ToAddr
		}

		// Documented last resort (AGENTS.md §5): the DNAT + FORWARD pair needs two
		// coordinated rules; explicit `-C ... || -A ...` guards keep it idempotent.
		dnatCheck := fmt.Sprintf("iptables -t nat -C PREROUTING %s", dnatRule)
		dnatAdd := fmt.Sprintf("iptables -t nat -A PREROUTING %s", dnatRule)
		fwdCheck := fmt.Sprintf("iptables -C FORWARD %s", forwardRule)
		fwdAdd := fmt.Sprintf("iptables -A FORWARD %s", forwardRule)

		cmd := fmt.Sprintf("%s 2>/dev/null || %s; %s 2>/dev/null || %s", dnatCheck, dnatAdd, fwdCheck, fwdAdd)
		title := fmt.Sprintf("Forward iptables %s/%s to %s:%s", spec.Port, spec.Proto, target, spec.ToPort)
		if spec.Absent {
			dnatDel := fmt.Sprintf("iptables -t nat -D PREROUTING %s", dnatRule)
			fwdDel := fmt.Sprintf("iptables -D FORWARD %s", forwardRule)
			cmd = fmt.Sprintf("%s 2>/dev/null && %s; %s 2>/dev/null && %s; true", dnatCheck, dnatDel, fwdCheck, fwdDel)
			title = fmt.Sprintf("Remove iptables port forward %s/%s to %s:%s", spec.Port, spec.Proto, target, spec.ToPort)
		}

		return plugin.TaskSpec{
			Name: title,
			Steps: []plugin.TaskStep{
				{
					Name:   title,
					Module: "ansible.builtin.shell",
					Params: map[string]any{
						"cmd":    cmd,
						"become": true,
					},
				},
			},
		}, nil

	default:
		return plugin.TaskSpec{}, fmt.Errorf("unsupported iptables action: %s", action)
	}
}
