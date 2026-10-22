package ufw

import (
	"context"
	"fmt"
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

// Plugin implements Uncomplicated Firewall (UFW) inspection and mutation.
type Plugin struct{}

func New() *Plugin {
	return &Plugin{}
}

func (p *Plugin) Meta() plugin.Meta {
	return plugin.Meta{
		ID:          "firewall/ufw",
		Kind:        plugin.KindFirewall,
		Name:        "ufw",
		Label:       "UFW",
		Icon:        "security",
		Description: "Uncomplicated Firewall (UFW) host packet filtering and port rules",
		Order:       13,
	}
}

func (p *Plugin) Compatible(h plugin.HostFacts) bool {
	return h.OSFamily != "windows" && h.OSFamily != "router_switch"
}

func (p *Plugin) Requirements() plugin.Requirements {
	return plugin.Requirements{
		HostPackages: map[string][]string{
			"apt": {"ufw"},
			"dnf": {"ufw"},
			"apk": {"ufw"},
		},
		ControllerCollections: []string{
			"community.general",
		},
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
	return settingsframework.NewPageBuilder(meta.ID, "UFW Firewall Defaults").
		WithIcon(meta.Icon).
		WithCategory(string(meta.Kind)).
		WithScope(newapi.SettingsScopeDatacenter).
		WithOrder(meta.Order).
		WithDescription("Datacenter-wide Uncomplicated Firewall (UFW) default policy, protocol, and logging settings.").
		SelectField("defaultProto", "Default Protocol", "tcp", []plugin.SettingsFieldOption{
			{Value: "tcp", Label: "TCP"},
			{Value: "udp", Label: "UDP"},
			{Value: "any", Label: "TCP & UDP (any)"},
		}, settingsframework.WithDescription("Default protocol used when compiling UFW port rules without an explicit protocol.")).
		SelectField("defaultIncomingPolicy", "Default Incoming Policy", "deny", []plugin.SettingsFieldOption{
			{Value: "deny", Label: "Deny"},
			{Value: "reject", Label: "Reject"},
			{Value: "allow", Label: "Allow"},
		}, settingsframework.WithDescription("Default policy for unmatched incoming traffic.")).
		SelectField("loggingLevel", "Logging Level", "low", []plugin.SettingsFieldOption{
			{Value: "off", Label: "Off"},
			{Value: "low", Label: "Low"},
			{Value: "medium", Label: "Medium"},
			{Value: "high", Label: "High"},
			{Value: "full", Label: "Full"},
		}, settingsframework.WithDescription("UFW packet logging verbosity across managed nodes.")).
		BuildCategorized()
}

func (p *Plugin) SettingsPage() plugin.SettingsPage {
	return p.CategorizedSettingsPage().SettingsPage
}

func defaultUfwProtocol() string {
	return settingsframework.NewStoreAdapter(nil).StringOrDefault("firewall/ufw", "defaultProto", "tcp")
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
			"Delete this UFW port rule from the host firewall?", 90).
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

func (p *Plugin) Probe(ctx context.Context, r plugin.Runner, h plugin.HostFacts) (plugin.Detection, error) {
	if r == nil {
		return plugin.Detection{Detected: false, Status: "OFFLINE", Error: "runner is nil"}, nil
	}

	binOut, err := r.RunCommand(ctx, "command -v ufw || ls /usr/sbin/ufw /sbin/ufw 2>/dev/null || true")
	if err != nil {
		return plugin.Detection{Detected: false, Status: "ERROR", Error: err.Error()}, err
	}
	if strings.TrimSpace(binOut) == "" {
		return plugin.Detection{
			Detected: false,
			Status:   "unavailable",
			Details:  "ufw binary not found on host",
		}, nil
	}

	statusOut, err := r.RunCommand(ctx, "sudo -n ufw status 2>&1 || ufw status 2>&1 || true")
	if err != nil {
		return plugin.Detection{Detected: false, Status: "ERROR", Error: err.Error()}, err
	}

	lower := strings.ToLower(statusOut)
	if strings.Contains(lower, "status: active") {
		return plugin.Detection{
			Detected: true,
			Status:   "active",
			Details:  "ufw firewall active",
		}, nil
	}

	return plugin.Detection{
		Detected: false,
		Status:   "inactive",
		Details:  "ufw installed but inactive",
	}, nil
}

var (
	ufwRuleLineRe = regexp.MustCompile(`^(?:\[\s*(\d+)\]\s+)?(.+?)\s+(ALLOW IN|ALLOW OUT|ALLOW FWD|ALLOW|DENY IN|DENY OUT|DENY FWD|DENY|REJECT IN|REJECT OUT|REJECT FWD|REJECT|LIMIT IN|LIMIT OUT|LIMIT)\s+(.+)$`)
)

// ParseUfwStatus parses `ufw status verbose` or `ufw status numbered` output.
func ParseUfwStatus(raw string) (string, []ParsedFirewallRule) {
	rules := []ParsedFirewallRule{}
	status := "inactive"
	lines := strings.Split(raw, "\n")

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		lower := strings.ToLower(trimmed)
		if strings.HasPrefix(lower, "status:") {
			if strings.Contains(lower, "active") && !strings.Contains(lower, "inactive") {
				status = "active"
			} else {
				status = "inactive"
			}
			break
		}
	}

	inRules := false
	idx := 1
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "--") {
			inRules = true
			continue
		}
		if !inRules {
			continue
		}

		m := ufwRuleLineRe.FindStringSubmatch(trimmed)
		if len(m) != 5 {
			continue
		}

		numID := m[1]
		toField := strings.TrimSpace(m[2])
		actionField := strings.ToUpper(strings.TrimSpace(m[3]))
		fromRest := strings.TrimSpace(m[4])

		comment := ""
		if hashIdx := strings.Index(fromRest, "#"); hashIdx >= 0 {
			comment = strings.TrimSpace(fromRest[hashIdx+1:])
			fromRest = strings.TrimSpace(fromRest[:hashIdx])
		}

		iface := "all"
		if strings.Contains(toField, " on ") {
			parts := strings.SplitN(toField, " on ", 2)
			toField = strings.TrimSpace(parts[0])
			iface = strings.TrimSpace(parts[1])
		}

		port := toField
		proto := "tcp/udp"
		if slashIdx := strings.Index(toField, "/"); slashIdx >= 0 {
			port = toField[:slashIdx]
			proto = strings.ToLower(toField[slashIdx+1:])
		}

		chain := "INPUT"
		if strings.HasSuffix(actionField, " OUT") {
			chain = "OUTPUT"
		}

		policy := "ACCEPT"
		switch {
		case strings.HasPrefix(actionField, "DENY"):
			policy = "DROP"
		case strings.HasPrefix(actionField, "REJECT"):
			policy = "REJECT"
		case strings.HasPrefix(actionField, "LIMIT"):
			policy = "ACCEPT"
		}

		source := fromRest
		if strings.EqualFold(source, "Anywhere") || strings.EqualFold(source, "Anywhere (v6)") || source == "" {
			source = "0.0.0.0/0"
		}

		ruleID := fmt.Sprintf("ufw-%d", idx)
		if numID != "" {
			ruleID = fmt.Sprintf("ufw-%s", numID)
		}
		if comment == "" {
			comment = fmt.Sprintf("UFW %s %s from %s", actionField, toField, source)
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

	return status, rules
}

func (p *Plugin) Read(ctx context.Context, r plugin.Runner, h plugin.HostFacts) (any, error) {
	if r == nil {
		return nil, fmt.Errorf("runner is nil")
	}

	cmd := "sudo -n ufw status verbose 2>&1 || ufw status verbose 2>&1 || echo 'NO_UFW'"
	out, err := r.RunCommand(ctx, cmd)
	if err != nil {
		return nil, err
	}

	status, rules := ParseUfwStatus(out)
	if status != "active" {
		rules = []ParsedFirewallRule{}
	}

	ports := []string{}
	for _, rule := range rules {
		if rule.Policy == "ACCEPT" && rule.Port != "" {
			ports = append(ports, fmt.Sprintf("%s/%s", rule.Port, rule.Proto))
		}
	}

	return map[string]any{
		"status":   status,
		"backend":  "ufw",
		"zones":    map[string]any{},
		"services": []string{},
		"ports":    ports,
		"rules":    rules,
	}, nil
}

func (p *Plugin) ManagementActions() []newapi.ManagementActionSpec {
	return []newapi.ManagementActionSpec{
		{
			ActionSpec: plugin.ActionSpec{
				Name:        "allow_port",
				Label:       "Allow Port",
				Description: "Allow incoming traffic on a port via community.general.ufw",
				Options: []plugin.OptionSchema{
					{Name: "port", Type: "string", Required: true, Description: "Port number or range (e.g. 80, 443, 8000:8080)"},
					{Name: "proto", Type: "string", Required: false, Default: "tcp", Description: "Protocol: tcp, udp, or any"},
					{Name: "from_ip", Type: "string", Required: false, Description: "Optional allowed source IP or CIDR"},
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
				Description: "Deny incoming traffic on a port via community.general.ufw",
				Options: []plugin.OptionSchema{
					{Name: "port", Type: "string", Required: true, Description: "Port number or range (e.g. 23, 8080)"},
					{Name: "proto", Type: "string", Required: false, Default: "tcp", Description: "Protocol: tcp, udp, or any"},
					{Name: "from_ip", Type: "string", Required: false, Description: "Optional source IP or CIDR"},
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
				Description: "Delete an existing UFW port rule via community.general.ufw",
				Options: []plugin.OptionSchema{
					{Name: "port", Type: "string", Required: true, Description: "Port number or range to remove"},
					{Name: "proto", Type: "string", Required: false, Default: "tcp", Description: "Protocol: tcp, udp, or any"},
					{Name: "rule", Type: "select", Enum: []string{"allow", "deny", "reject", "limit"}, Default: "allow", Description: "Rule type to delete"},
				},
			},
			Category:         newapi.ActionCategorySecurity,
			Idempotent:       true,
			Dangerous:        true,
			ConfirmPrompt:    "Delete this UFW port rule from the host firewall?",
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
	norm := strings.ToLower(strings.TrimSpace(action))
	if norm == "port" {
		norm = "allow_port"
	}
	for _, spec := range p.ManagementActions() {
		if strings.EqualFold(spec.Name, norm) {
			return newapi.ValidateManagementOptions(spec, opts)
		}
	}
	return fmt.Errorf("unsupported ufw action: %s", action)
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

func normalizeUfwPort(raw string) (string, error) {
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
		if err := p.ValidateAction(h, norm, opts); err != nil {
			return plugin.TaskSpec{}, err
		}
		rawPort, _ := opts["port"].(string)
		portSpec, err := normalizeUfwPort(rawPort)
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
			proto = defaultUfwProtocol()
		}
		if proto != "tcp" && proto != "udp" && proto != "any" {
			return plugin.TaskSpec{}, fmt.Errorf("invalid protocol %q: must be tcp, udp, or any", proto)
		}

		ruleType := "allow"
		deleteRule := false
		if norm == "deny_port" {
			ruleType = "deny"
		} else if norm == "delete_port" {
			deleteRule = true
			if rt, ok := opts["rule"].(string); ok && strings.TrimSpace(rt) != "" {
				ruleType = strings.ToLower(strings.TrimSpace(rt))
			}
		} else if st, ok := opts["state"].(string); ok && strings.EqualFold(strings.TrimSpace(st), "disabled") {
			deleteRule = true
		}

		params := map[string]any{
			"rule":   ruleType,
			"port":   portSpec,
			"proto":  proto,
			"become": true,
		}
		if deleteRule {
			params["delete"] = true
		}
		if fromIP, ok := opts["from_ip"].(string); ok && strings.TrimSpace(fromIP) != "" {
			params["from_ip"] = strings.TrimSpace(fromIP)
		} else if src, ok := opts["source"].(string); ok && strings.TrimSpace(src) != "" && !strings.EqualFold(strings.TrimSpace(src), "any") {
			params["from_ip"] = strings.TrimSpace(src)
		}
		if comment, ok := opts["comment"].(string); ok && strings.TrimSpace(comment) != "" {
			params["comment"] = strings.TrimSpace(comment)
		}

		title := fmt.Sprintf("UFW %s port %s/%s", ruleType, portSpec, proto)
		if deleteRule {
			title = fmt.Sprintf("Delete UFW %s rule for port %s/%s", ruleType, portSpec, proto)
		}

		return plugin.TaskSpec{
			Name: title,
			Steps: []plugin.TaskStep{
				{
					Name:   title,
					Module: "community.general.ufw",
					Params: params,
				},
			},
		}, nil

	default:
		return plugin.TaskSpec{}, fmt.Errorf("unsupported ufw action: %s", action)
	}
}
