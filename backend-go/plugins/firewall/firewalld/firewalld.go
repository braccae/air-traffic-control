package firewalld

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
			// The native ansible.posix.firewalld module needs the Python bindings,
			// not just the firewalld binary.
			"dnf": {"firewalld", "python3-firewall"},
			"apt": {"firewalld", "python3-firewall"},
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

	// 1. Check if firewalld daemon is actively running.
	out, err := r.RunCommand(ctx, "sudo -n firewall-cmd --state 2>/dev/null || firewall-cmd --state 2>/dev/null || /usr/bin/firewall-cmd --state 2>/dev/null || /usr/sbin/firewall-cmd --state 2>/dev/null || /sbin/firewall-cmd --state 2>/dev/null || true")
	if err == nil && isFirewalldRunningState(out) {
		return plugin.Detection{
			Detected: true,
			Status:   "running",
			Details:  "firewalld daemon active",
		}, nil
	}

	// 2. Check if the firewall-cmd binary exists in PATH or common absolute locations.
	cmdOut, err := r.RunCommand(ctx, "command -v firewall-cmd 2>/dev/null || sudo -n sh -c 'command -v firewall-cmd' 2>/dev/null || for p in /usr/bin/firewall-cmd /usr/sbin/firewall-cmd /sbin/firewall-cmd; do [ -x \"$p\" ] && echo \"$p\" && break; done")
	if err == nil && hasFirewallCmdBinary(cmdOut) {
		return plugin.Detection{
			Detected: true,
			Status:   "inactive",
			Details:  "firewalld installed but not running",
		}, nil
	}

	// 3. Fallback: check if firewalld service unit exists via systemctl.
	unitOut, err := r.RunCommand(ctx, "sudo -n systemctl cat firewalld 2>/dev/null || systemctl cat firewalld 2>/dev/null || sudo -n systemctl status firewalld 2>/dev/null || systemctl status firewalld 2>/dev/null")
	if err == nil && isSystemctlUnitPresent(unitOut) {
		return plugin.Detection{
			Detected: true,
			Status:   "inactive",
			Details:  "firewalld installed but not running",
		}, nil
	}

	return plugin.Detection{
		Detected: false,
		Status:   "UNAVAILABLE",
		Details:  "firewalld / firewall-cmd not found",
	}, nil
}

// isFirewalldRunningState reports whether `firewall-cmd --state` output indicates
// the daemon is actively running (and not "not running" or a sudo/permission error).
func isFirewalldRunningState(out string) bool {
	for _, line := range strings.Split(out, "\n") {
		trimmed := strings.ToLower(strings.TrimSpace(line))
		if trimmed == "running" {
			return true
		}
	}
	lower := strings.ToLower(strings.TrimSpace(out))
	return strings.Contains(lower, "running") && !strings.Contains(lower, "not running")
}

// hasFirewallCmdBinary reports whether command output contains a resolved path to firewall-cmd.
func hasFirewallCmdBinary(out string) bool {
	for _, line := range strings.Split(out, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		lower := strings.ToLower(trimmed)
		if strings.Contains(lower, "sudo:") || strings.Contains(lower, "permission denied") || strings.Contains(lower, "not found") {
			continue
		}
		if strings.HasSuffix(trimmed, "/firewall-cmd") || trimmed == "firewall-cmd" {
			return true
		}
	}
	return false
}

// isSystemctlUnitPresent reports whether `systemctl cat/status firewalld` output
// confirms a real firewalld unit exists (ignoring empty output or permission/not-found errors).
func isSystemctlUnitPresent(out string) bool {
	trimmed := strings.TrimSpace(out)
	if trimmed == "" {
		return false
	}
	lower := strings.ToLower(trimmed)
	if strings.Contains(lower, "no files found") ||
		strings.Contains(lower, "could not be found") ||
		strings.Contains(lower, "not-found") ||
		strings.Contains(lower, "sudo:") ||
		strings.Contains(lower, "permission denied") {
		return false
	}
	return true
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

// ForwardPort describes a single firewalld port-forwarding (NAT MASQ) entry.
type ForwardPort struct {
	Port   string `json:"port"`
	Proto  string `json:"proto"`
	ToPort string `json:"toport"`
	ToAddr string `json:"toaddr,omitempty"`
}

var zoneHeaderRegex = regexp.MustCompile(`^([a-zA-Z0-9_\-]+)`)

// validPortOrRange reports whether s is a single port (1-65535) or a "START-END" range.
func validPortOrRange(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	if strings.Contains(s, "-") {
		parts := strings.Split(s, "-")
		if len(parts) != 2 {
			return false
		}
		start, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
		end, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
		if err1 != nil || err2 != nil {
			return false
		}
		if start < 1 || start > 65535 || end < 1 || end > 65535 {
			return false
		}
		return start < end
	}
	p, err := strconv.Atoi(s)
	if err != nil {
		return false
	}
	return p >= 1 && p <= 65535
}

// validIPOrCIDR reports whether s is a valid IP address or CIDR block.
func validIPOrCIDR(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	if net.ParseIP(s) != nil {
		return true
	}
	_, _, err := net.ParseCIDR(s)
	return err == nil
}

// parseForwardPortSpec parses one firewalld forward-port token of the form
// "port=8080:proto=tcp:toport=80:toaddr=192.168.1.10" into a ForwardPort.
func parseForwardPortSpec(spec string) (ForwardPort, bool) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return ForwardPort{}, false
	}
	fp := ForwardPort{}
	for _, part := range strings.Split(spec, ":") {
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			continue
		}
		switch kv[0] {
		case "port":
			fp.Port = kv[1]
		case "proto":
			fp.Proto = kv[1]
		case "toport":
			fp.ToPort = kv[1]
		case "toaddr":
			fp.ToAddr = kv[1]
		}
	}
	if fp.Port == "" || fp.ToPort == "" {
		return ForwardPort{}, false
	}
	if fp.Proto == "" {
		fp.Proto = "tcp"
	}
	return fp, true
}

// parseForwardPortSpecs parses a whitespace-separated list of forward-port tokens.
func parseForwardPortSpecs(raw string) []ForwardPort {
	out := []ForwardPort{}
	for _, tok := range strings.Fields(raw) {
		if fp, ok := parseForwardPortSpec(tok); ok {
			out = append(out, fp)
		}
	}
	return out
}

// extractForwardPortsAndMasquerade scans the firewalld --list-all block for the
// "forward-ports:" and "masquerade:" keys (including indented continuation lines).
func extractForwardPortsAndMasquerade(listAllRaw string) ([]ForwardPort, bool) {
	forwardPorts := []ForwardPort{}
	masquerade := false
	lines := strings.Split(listAllRaw, "\n")

	for i := 0; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if strings.HasPrefix(trimmed, "forward-ports:") {
			val := strings.TrimSpace(strings.TrimPrefix(trimmed, "forward-ports:"))
			if val != "" {
				forwardPorts = append(forwardPorts, parseForwardPortSpecs(val)...)
			}
			for j := i + 1; j < len(lines); j++ {
				cont := strings.TrimSpace(lines[j])
				if cont == "" {
					continue
				}
				// Continuation specs always begin with "port="; anything else is a new key.
				if !strings.HasPrefix(cont, "port=") {
					break
				}
				forwardPorts = append(forwardPorts, parseForwardPortSpecs(cont)...)
			}
		} else if strings.HasPrefix(trimmed, "masquerade:") {
			v := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(trimmed, "masquerade:")))
			masquerade = v == "yes" || v == "true"
		}
	}
	return forwardPorts, masquerade
}

func parseFirewallListAll(listAllRaw, activeZonesRaw string) (map[string]any, []string, []string, []string, []ParsedFirewallRule, []ForwardPort, bool) {
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

	forwardPorts, masquerade := extractForwardPortsAndMasquerade(listAllRaw)

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

	return convertedZones, services, ports, richRules, rules, forwardPorts, masquerade
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
	return settingsframework.NewPageBuilder(p.Meta().ID, "Firewalld Defaults").
		WithIcon(p.Meta().Icon).
		WithCategory(string(p.Meta().Kind)).
		WithScope(newapi.SettingsScopeDatacenter).
		WithOrder(p.Meta().Order).
		WithDescription("Datacenter-wide firewalld default zone, protocol, and masquerade settings.").
		StringField("defaultZone", "Default Firewall Zone", "public",
			settingsframework.WithDescription("Default firewalld zone used when an action does not specify a target zone.")).
		SelectField("defaultProto", "Default Protocol", "tcp", []plugin.SettingsFieldOption{
			{Value: "tcp", Label: "TCP"},
			{Value: "udp", Label: "UDP"},
		}, settingsframework.WithDescription("Default transport protocol when allowing or forwarding ports.")).
		BoolField("autoMasqueradeOnForward", "Enable Masquerade on Port Forward", true,
			settingsframework.WithDescription("Automatically enable zone masquerading when forwarding ports.")).
		BuildCategorized()
}

func (p *Plugin) SettingsPage() plugin.SettingsPage {
	return p.CategorizedSettingsPage().SettingsPage
}

func defaultFirewallZone() string {
	return settingsframework.NewStoreAdapter(nil).StringOrDefault("firewall/firewalld", "defaultZone", "public")
}

func defaultFirewallProto() string {
	return settingsframework.NewStoreAdapter(nil).StringOrDefault("firewall/firewalld", "defaultProto", "tcp")
}

func (p *Plugin) ContextMenuEntries(ctx context.Context, h plugin.HostFacts) []newapi.ContextMenuEntry {
	if !p.Compatible(h) {
		return nil
	}
	return newapi.NewContextMenuBuilder(p.Meta().ID).
		WithTarget(newapi.TargetFirewall).
		WithGroup(string(p.Meta().Kind)).
		AddAction("allow_port", "Allow Port", "lock_open", "allow_port", 10).
		AddAction("allow_service", "Allow Service", "verified_user", "allow_service", 20).
		AddAction("forward_port", "Forward Port (NAT MASQ)", "alt_route", "forward_port", 30).
		Build()
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

	isRunning := isFirewalldRunningState(stateRaw)
	hasNft := !strings.Contains(nftRaw, "NO_NFT") && strings.Contains(nftRaw, "table")

	zones, services, ports, _, rules, forwardPorts, masquerade := parseFirewallListAll(listAllRaw, zonesRaw)

	if !isRunning {
		rules = []ParsedFirewallRule{}
		forwardPorts = []ForwardPort{}
		masquerade = false
	}

	status := "inactive"
	backend := ""
	if isRunning {
		status = "running"
		if hasNft {
			backend = "nftables (firewalld)"
		} else {
			backend = "iptables (firewalld)"
		}
	}

	return map[string]any{
		"status":         status,
		"nftablesActive": hasNft,
		"backend":        backend,
		"zones":          zones,
		"services":       services,
		"ports":          ports,
		"rules":          rules,
		"forwardPorts":   forwardPorts,
		"masquerade":     masquerade,
	}, nil
}

func (p *Plugin) ManagementActions() []newapi.ManagementActionSpec {
	return []newapi.ManagementActionSpec{
		{
			ActionSpec: plugin.ActionSpec{
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
			Category:         newapi.ActionCategorySecurity,
			Idempotent:       true,
			Dangerous:        false,
			InvalidatesKinds: []plugin.Kind{plugin.KindFirewall},
		},
		{
			ActionSpec: plugin.ActionSpec{
				Name:        "allow_service",
				Label:       "Allow Service",
				Description: "Allow a named service through firewalld",
				Options: []plugin.OptionSchema{
					{Name: "service", Type: "string", Required: true, Description: "Service name (e.g. http, ssh)"},
					{Name: "zone", Type: "string", Required: false, Description: "Target firewall zone"},
					{Name: "state", Type: "select", Enum: []string{"enabled", "disabled"}, Default: "enabled", Description: "Rule state"},
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
				Label:       "Forward Port (NAT MASQ)",
				Description: "Forward an incoming port to an internal host and port via firewalld masquerade",
				Options: []plugin.OptionSchema{
					{Name: "port", Type: "string", Required: true, Description: "External port or range (e.g. 8080 or 8000-8080)"},
					{Name: "proto", Type: "string", Required: false, Default: "tcp", Description: "Protocol: tcp or udp"},
					{Name: "toport", Type: "string", Required: true, Description: "Internal destination port (e.g. 80)"},
					{Name: "toaddr", Type: "string", Required: false, Description: "Internal destination host IP (optional; defaults to same host)"},
					{Name: "zone", Type: "string", Required: false, Default: "public", Description: "Target firewall zone"},
					{Name: "state", Type: "select", Enum: []string{"enabled", "disabled"}, Default: "enabled", Description: "Rule state"},
				},
			},
			Category:         newapi.ActionCategorySecurity,
			Idempotent:       true,
			Dangerous:        false,
			InvalidatesKinds: []plugin.Kind{plugin.KindFirewall},
		},
		{
			ActionSpec: plugin.ActionSpec{
				Name:        "masquerade",
				Label:       "Toggle Masquerade",
				Description: "Enable or disable source NAT masquerade on a firewalld zone",
				Options: []plugin.OptionSchema{
					{Name: "enabled", Type: "bool", Required: false, Default: true, Description: "true enables masquerade, false disables it"},
					{Name: "zone", Type: "string", Required: false, Default: "public", Description: "Target firewall zone"},
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

func normalizeActionName(action string) string {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "allow_port", "port":
		return "allow_port"
	case "allow_service", "service":
		return "allow_service"
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

func (p *Plugin) ValidateAction(h plugin.HostFacts, action string, opts map[string]any) error {
	canonical := normalizeActionName(action)
	for _, spec := range p.ManagementActions() {
		if strings.EqualFold(spec.Name, canonical) {
			if err := newapi.ValidateManagementOptions(spec, opts); err != nil {
				return err
			}
			if canonical == "forward_port" {
				port, _ := opts["port"].(string)
				port = strings.TrimSpace(port)
				if !validPortOrRange(port) {
					return fmt.Errorf("invalid external port %q: must be 1-65535 or START-END", port)
				}
				proto, _ := opts["proto"].(string)
				proto = strings.ToLower(strings.TrimSpace(proto))
				if proto != "" && proto != "tcp" && proto != "udp" {
					return fmt.Errorf("invalid protocol %q: must be tcp or udp", proto)
				}
				toport, _ := opts["toport"].(string)
				toport = strings.TrimSpace(toport)
				if !validPortOrRange(toport) {
					return fmt.Errorf("invalid destination port %q: must be 1-65535 or START-END", toport)
				}
				toaddr, _ := opts["toaddr"].(string)
				toaddr = strings.TrimSpace(toaddr)
				if toaddr != "" && !validIPOrCIDR(toaddr) {
					return fmt.Errorf("invalid destination address %q: must be a valid IP address or CIDR", toaddr)
				}
			}
			if canonical == "masquerade" {
				_, err := resolveMasqueradeEnabled(opts)
				return err
			}
			return nil
		}
	}
	return fmt.Errorf("unsupported firewalld action: %s", action)
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
	switch norm {
	case "allow_port", "port":
		port, _ := opts["port"].(string)
		if port == "" {
			port = "80"
		}
		proto, _ := opts["proto"].(string)
		if proto == "" {
			proto = defaultFirewallProto()
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

	case "forward_port", "forward-port", "port_forward":
		port, _ := opts["port"].(string)
		port = strings.TrimSpace(port)
		if port == "" {
			return plugin.TaskSpec{}, fmt.Errorf("external port is required")
		}
		if !validPortOrRange(port) {
			return plugin.TaskSpec{}, fmt.Errorf("invalid external port %q: must be 1-65535 or START-END", port)
		}

		proto, _ := opts["proto"].(string)
		proto = strings.ToLower(strings.TrimSpace(proto))
		if proto == "" {
			proto = defaultFirewallProto()
		}
		if proto != "tcp" && proto != "udp" {
			return plugin.TaskSpec{}, fmt.Errorf("invalid protocol %q: must be tcp or udp", proto)
		}

		toport, _ := opts["toport"].(string)
		toport = strings.TrimSpace(toport)
		if toport == "" {
			return plugin.TaskSpec{}, fmt.Errorf("internal destination port is required")
		}
		if !validPortOrRange(toport) {
			return plugin.TaskSpec{}, fmt.Errorf("invalid destination port %q: must be 1-65535 or START-END", toport)
		}

		toaddr, _ := opts["toaddr"].(string)
		toaddr = strings.TrimSpace(toaddr)
		if toaddr != "" && !validIPOrCIDR(toaddr) {
			return plugin.TaskSpec{}, fmt.Errorf("invalid destination address %q: must be a valid IP address or CIDR", toaddr)
		}

		zone, _ := opts["zone"].(string)
		zone = strings.TrimSpace(zone)
		if zone == "" {
			zone = defaultFirewallZone()
		}

		state, _ := opts["state"].(string)
		if state == "" {
			state = "enabled"
		}

		forwardSpec := map[string]any{
			"port":   port,
			"proto":  proto,
			"toport": toport,
		}
		if toaddr != "" {
			forwardSpec["toaddr"] = toaddr
		}

		target := "same host"
		if toaddr != "" {
			target = toaddr
		}

		if state == "disabled" {
			return plugin.TaskSpec{
				Name: fmt.Sprintf("Remove port forward %s/%s on zone %s", port, proto, zone),
				Steps: []plugin.TaskStep{
					{
						Name:   fmt.Sprintf("Remove port forward %s/%s -> %s:%s", port, proto, target, toport),
						Module: "ansible.posix.firewalld",
						Params: map[string]any{
							"zone":         zone,
							"port_forward": []map[string]any{forwardSpec},
							"state":        "disabled",
							"permanent":    true,
							"immediate":    true,
							"become":       true,
						},
					},
				},
			}, nil
		}

		return plugin.TaskSpec{
			Name: fmt.Sprintf("Forward port %s/%s on zone %s to %s:%s", port, proto, zone, target, toport),
			Steps: []plugin.TaskStep{
				{
					Name:   fmt.Sprintf("Enable masquerade on zone %s", zone),
					Module: "ansible.posix.firewalld",
					Params: map[string]any{
						"zone":       zone,
						"masquerade": "true",
						"state":      "enabled",
						"permanent":  true,
						"immediate":  true,
						"become":     true,
					},
				},
				{
					Name:   fmt.Sprintf("Forward %s/%s -> %s:%s in zone %s", port, proto, target, toport, zone),
					Module: "ansible.posix.firewalld",
					Params: map[string]any{
						"zone":         zone,
						"port_forward": []map[string]any{forwardSpec},
						"state":        "enabled",
						"permanent":    true,
						"immediate":    true,
						"become":       true,
					},
				},
			},
		}, nil

	case "masquerade", "toggle_masquerade":
		enabled, err := resolveMasqueradeEnabled(opts)
		if err != nil {
			return plugin.TaskSpec{}, err
		}
		zone, _ := opts["zone"].(string)
		zone = strings.TrimSpace(zone)
		if zone == "" {
			zone = defaultFirewallZone()
		}
		if !safeFirewalldZoneRe.MatchString(zone) {
			return plugin.TaskSpec{}, fmt.Errorf("invalid firewalld zone %q", zone)
		}

		state := "enabled"
		verb := "Enable"
		if !enabled {
			state = "disabled"
			verb = "Disable"
		}
		title := fmt.Sprintf("%s masquerade on zone %s", verb, zone)

		return plugin.TaskSpec{
			Name: title,
			Steps: []plugin.TaskStep{
				{
					Name:   title,
					Module: "ansible.posix.firewalld",
					Params: map[string]any{
						"zone":       zone,
						"masquerade": "true",
						"state":      state,
						"permanent":  true,
						"immediate":  true,
						"become":     true,
					},
				},
			},
		}, nil

	default:
		return plugin.TaskSpec{}, fmt.Errorf("unsupported firewalld action: %s", action)
	}
}

var safeFirewalldZoneRe = regexp.MustCompile(`^[a-zA-Z0-9_\-]+$`)
