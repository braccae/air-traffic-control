package nodeapi

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// ===========================================================================
// Firewall & Network Interface Command Builders & Validators
// Ported from src/utils/firewallRules.ts
// ===========================================================================

var (
	validProtocols = map[string]bool{"tcp": true, "udp": true, "sctp": true, "icmp": true, "all": true}
	validZones     = map[string]bool{
		"public": true, "internal": true, "trusted": true, "dmz": true,
		"drop": true, "block": true, "work": true, "home": true, "external": true,
	}
	reIfaceName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,14}$`)
	reDigits    = regexp.MustCompile(`^\d+$`)
)

type ValidationResult struct {
	Valid      bool
	Error      string
	Normalized string
}

func validateInterfaceName(iface string) ValidationResult {
	trimmed := strings.TrimSpace(iface)
	if trimmed == "" || strings.ToLower(trimmed) == "any" {
		return ValidationResult{Valid: true, Normalized: "any"}
	}
	if !reIfaceName.MatchString(trimmed) {
		return ValidationResult{
			Valid: false,
			Error: fmt.Sprintf("Invalid interface name '%s'. Must be 1-15 characters, alphanumeric with ., -, or _", trimmed),
		}
	}
	return ValidationResult{Valid: true, Normalized: trimmed}
}

func validateZone(zone string) ValidationResult {
	trimmed := strings.TrimSpace(zone)
	if trimmed == "" {
		return ValidationResult{Valid: true, Normalized: "public"}
	}
	lower := strings.ToLower(trimmed)
	if !validZones[lower] {
		allowed := []string{"public", "internal", "trusted", "dmz", "drop", "block", "work", "home", "external"}
		return ValidationResult{
			Valid:      false,
			Error:      fmt.Sprintf("Unknown zone '%s'. Allowed: %s", zone, strings.Join(allowed, ", ")),
			Normalized: "public",
		}
	}
	return ValidationResult{Valid: true, Normalized: lower}
}

func validatePortOrRange(input string) ValidationResult {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return ValidationResult{Valid: false, Error: "Port or range is required"}
	}

	if strings.Contains(trimmed, "-") {
		parts := strings.Split(trimmed, "-")
		if len(parts) != 2 {
			return ValidationResult{Valid: false, Error: "Port range must have format START-END (e.g. 8000-8080)"}
		}
		startStr := strings.TrimSpace(parts[0])
		endStr := strings.TrimSpace(parts[1])
		if !reDigits.MatchString(startStr) || !reDigits.MatchString(endStr) {
			return ValidationResult{Valid: false, Error: "Port range numbers must be positive integers"}
		}
		start, err1 := strconv.Atoi(startStr)
		end, err2 := strconv.Atoi(endStr)
		if err1 != nil || err2 != nil || start < 1 || start > 65535 || end < 1 || end > 65535 {
			return ValidationResult{Valid: false, Error: "Port numbers must be between 1 and 65535"}
		}
		if start >= end {
			return ValidationResult{Valid: false, Error: fmt.Sprintf("Invalid port range: start (%d) must be less than end (%d)", start, end)}
		}
		return ValidationResult{Valid: true, Normalized: fmt.Sprintf("%d-%d", start, end)}
	}

	if !reDigits.MatchString(trimmed) {
		return ValidationResult{Valid: false, Error: "Port must be a valid number between 1 and 65535"}
	}
	port, err := strconv.Atoi(trimmed)
	if err != nil || port < 1 || port > 65535 {
		return ValidationResult{Valid: false, Error: "Port number must be between 1 and 65535"}
	}
	return ValidationResult{Valid: true, Normalized: strconv.Itoa(port)}
}

func validateProtocol(proto string) ValidationResult {
	trimmed := strings.TrimSpace(proto)
	if trimmed == "" {
		return ValidationResult{Valid: false, Error: "Protocol is required"}
	}
	lower := strings.ToLower(trimmed)
	if !validProtocols[lower] {
		return ValidationResult{Valid: false, Error: fmt.Sprintf("Unsupported protocol '%s'. Allowed: tcp, udp, sctp, icmp, all", proto)}
	}
	return ValidationResult{Valid: true, Normalized: lower}
}

func validateCidrOrIp(input string) ValidationResult {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" || strings.ToLower(trimmed) == "any" || trimmed == "0.0.0.0/0" {
		return ValidationResult{Valid: true, Normalized: "any"}
	}

	reMatch := regexp.MustCompile(`^(\d{1,3}\.){3}\d{1,3}(/(\d{1,2}))?$`)
	if !reMatch.MatchString(trimmed) {
		return ValidationResult{Valid: false, Error: fmt.Sprintf("Invalid IP address or CIDR format: '%s'", trimmed)}
	}

	parts := strings.Split(trimmed, "/")
	ipPart := parts[0]
	octets := strings.Split(ipPart, ".")
	for _, octetStr := range octets {
		octet, err := strconv.Atoi(octetStr)
		if err != nil || octet < 0 || octet > 255 {
			return ValidationResult{Valid: false, Error: fmt.Sprintf("Invalid IP octet %s in '%s'", octetStr, trimmed)}
		}
	}

	if len(parts) > 1 {
		mask, err := strconv.Atoi(parts[1])
		if err != nil || mask < 0 || mask > 32 {
			return ValidationResult{Valid: false, Error: fmt.Sprintf("Invalid CIDR prefix /%s in '%s'. Must be 0-32", parts[1], trimmed)}
		}
	}

	return ValidationResult{Valid: true, Normalized: trimmed}
}

type ConnectNetworkInput struct {
	Type    string   `json:"type"`
	Name    string   `json:"name"`
	Parent  string   `json:"parent,omitempty"`
	Cidr    string   `json:"cidr,omitempty"`
	VlanID  *int     `json:"vlanId,omitempty"`
	Gateway string   `json:"gateway,omitempty"`
	DNS     []string `json:"dns,omitempty"`
}

func BuildConnectNetworkCommand(options ConnectNetworkInput) (string, error) {
	ifaceVal := validateInterfaceName(options.Name)
	if !ifaceVal.Valid {
		return "", errors.New(ifaceVal.Error)
	}
	name := ifaceVal.Normalized

	var commands []string

	switch options.Type {
	case "wireguard":
		commands = append(commands, fmt.Sprintf("sudo ip link add dev %s type wireguard", name))
		if strings.TrimSpace(options.Cidr) != "" {
			cidrVal := validateCidrOrIp(options.Cidr)
			if !cidrVal.Valid {
				return "", errors.New(cidrVal.Error)
			}
			commands = append(commands, fmt.Sprintf("sudo ip addr add %s dev %s", cidrVal.Normalized, name))
		}
		commands = append(commands, fmt.Sprintf("sudo ip link set up dev %s", name))
		if strings.TrimSpace(options.Gateway) != "" {
			gwVal := validateCidrOrIp(options.Gateway)
			if !gwVal.Valid {
				return "", errors.New(gwVal.Error)
			}
			commands = append(commands, fmt.Sprintf("sudo ip route replace default via %s dev %s", gwVal.Normalized, name))
		}

	case "bridge":
		commands = append(commands, fmt.Sprintf("sudo ip link add name %s type bridge", name))
		if strings.TrimSpace(options.Parent) != "" {
			parentVal := validateInterfaceName(options.Parent)
			if !parentVal.Valid {
				return "", errors.New(parentVal.Error)
			}
			commands = append(commands, fmt.Sprintf("sudo ip link set %s master %s", parentVal.Normalized, name))
		}
		if strings.TrimSpace(options.Cidr) != "" {
			cidrVal := validateCidrOrIp(options.Cidr)
			if !cidrVal.Valid {
				return "", errors.New(cidrVal.Error)
			}
			commands = append(commands, fmt.Sprintf("sudo ip addr add %s dev %s", cidrVal.Normalized, name))
		}
		commands = append(commands, fmt.Sprintf("sudo ip link set dev %s up", name))

	case "vlan":
		if strings.TrimSpace(options.Parent) == "" {
			return "", errors.New("Parent interface is required for 802.1Q VLAN")
		}
		parentVal := validateInterfaceName(options.Parent)
		if !parentVal.Valid {
			return "", errors.New(parentVal.Error)
		}

		vlanID := 10
		if options.VlanID != nil {
			vlanID = *options.VlanID
		}
		if vlanID < 1 || vlanID > 4094 {
			return "", errors.New("VLAN ID must be an integer between 1 and 4094")
		}
		commands = append(commands, fmt.Sprintf("sudo ip link add link %s name %s type vlan id %d", parentVal.Normalized, name, vlanID))
		if strings.TrimSpace(options.Cidr) != "" {
			cidrVal := validateCidrOrIp(options.Cidr)
			if !cidrVal.Valid {
				return "", errors.New(cidrVal.Error)
			}
			commands = append(commands, fmt.Sprintf("sudo ip addr add %s dev %s", cidrVal.Normalized, name))
		}
		commands = append(commands, fmt.Sprintf("sudo ip link set dev %s up", name))

	case "static":
		if strings.TrimSpace(options.Cidr) == "" {
			return "", errors.New("IP Address / CIDR is required for static configuration")
		}
		cidrVal := validateCidrOrIp(options.Cidr)
		if !cidrVal.Valid {
			return "", errors.New(cidrVal.Error)
		}
		commands = append(commands, fmt.Sprintf("sudo ip addr add %s dev %s", cidrVal.Normalized, name))
		commands = append(commands, fmt.Sprintf("sudo ip link set dev %s up", name))
		if strings.TrimSpace(options.Gateway) != "" {
			gwVal := validateCidrOrIp(options.Gateway)
			if !gwVal.Valid {
				return "", errors.New(gwVal.Error)
			}
			commands = append(commands, fmt.Sprintf("sudo ip route replace default via %s dev %s", gwVal.Normalized, name))
		}

	case "dhcp":
		commands = append(commands, fmt.Sprintf("sudo ip link set dev %s up", name))
		commands = append(commands, fmt.Sprintf("(sudo dhclient -v %s || sudo udhcpc -i %s || true)", name, name))

	default:
		return "", fmt.Errorf("Unsupported network type '%s'", options.Type)
	}

	if len(options.DNS) > 0 {
		var validDNS []string
		for _, d := range options.DNS {
			d = strings.TrimSpace(d)
			val := validateCidrOrIp(d)
			if val.Valid && val.Normalized != "any" {
				validDNS = append(validDNS, d)
			}
		}
		if len(validDNS) > 0 {
			commands = append(commands, fmt.Sprintf("(sudo resolvectl dns %s %s 2>/dev/null || true)", name, strings.Join(validDNS, " ")))
		}
	}

	return strings.Join(commands, " && "), nil
}

type FirewallRuleInput struct {
	Port        string `json:"port,omitempty"`
	Protocol    string `json:"protocol,omitempty"`
	Source      string `json:"source,omitempty"`
	InInterface string `json:"inInterface,omitempty"`
	Zone        string `json:"zone,omitempty"`
	Policy      string `json:"policy,omitempty"`
	Service     string `json:"service,omitempty"`
	Comment     string `json:"comment,omitempty"`
	Toport      string `json:"toport,omitempty"`
	Toaddr      string `json:"toaddr,omitempty"`
	// Plugin selects the firewall flavour that must receive the rule. Empty
	// (or "firewalld") keeps the legacy firewalld behaviour; "nftables" and
	// "iptables" compile raw netfilter commands instead.
	Plugin string `json:"plugin,omitempty"`
}

// normalizeFirewallFlavour maps a plugin id or short name onto the supported
// firewall flavour ("firewalld", "nftables", "iptables", "ufw").
func normalizeFirewallFlavour(name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	if i := strings.LastIndex(n, "/"); i >= 0 {
		n = n[i+1:]
	}
	switch n {
	case "", "firewalld", "firewall-cmd":
		return "firewalld"
	case "nftables", "nft":
		return "nftables"
	case "iptables", "iptables-legacy":
		return "iptables"
	case "ufw":
		return "ufw"
	}
	return n
}

// natForwardCommand builds an idempotent DNAT + FORWARD pair for raw netfilter
// backends. Ranges are rejected because a single DNAT destination cannot map a
// port range deterministically.
func natForwardCommand(flavour string, rule FirewallRuleInput) (string, error) {
	portVal := validatePortOrRange(rule.Port)
	if !portVal.Valid {
		return "", errors.New(portVal.Error)
	}
	toportVal := validatePortOrRange(rule.Toport)
	if !toportVal.Valid {
		return "", errors.New(toportVal.Error)
	}
	if strings.Contains(portVal.Normalized, "-") || strings.Contains(toportVal.Normalized, "-") {
		return "", errors.New("port ranges are not supported for nftables/iptables DNAT; provide single ports")
	}

	protoVal := validateProtocol(rule.Protocol)
	proto := "tcp"
	if strings.TrimSpace(rule.Protocol) != "" {
		if !protoVal.Valid {
			return "", errors.New(protoVal.Error)
		}
		proto = protoVal.Normalized
	}
	if proto != "tcp" && proto != "udp" {
		return "", fmt.Errorf("invalid protocol %q: DNAT forwarding supports tcp or udp", proto)
	}

	toaddr := strings.TrimSpace(rule.Toaddr)
	if toaddr != "" {
		addrVal := validateCidrOrIp(toaddr)
		if !addrVal.Valid || strings.Contains(addrVal.Normalized, "/") {
			return "", fmt.Errorf("invalid destination address %q: must be a single IP address", toaddr)
		}
		toaddr = addrVal.Normalized
	}

	if flavour == "nftables" {
		table := "inet filter"
		dnatRule := fmt.Sprintf("%s dport %s dnat to %s:%s", proto, portVal.Normalized, toaddr, toportVal.Normalized)
		acceptRule := fmt.Sprintf("%s dport %s accept", proto, toportVal.Normalized)
		if toaddr != "" {
			acceptRule = fmt.Sprintf("ip daddr %s %s dport %s accept", toaddr, proto, toportVal.Normalized)
		}
		return strings.Join([]string{
			fmt.Sprintf("sudo nft list table %s >/dev/null 2>&1 || sudo nft add table %s", table, table),
			fmt.Sprintf("sudo nft list chain %s prerouting >/dev/null 2>&1 || sudo nft add chain %s prerouting '{ type nat hook prerouting priority dstnat; policy accept; }'", table, table),
			fmt.Sprintf("sudo nft list chain %s forward >/dev/null 2>&1 || sudo nft add chain %s forward '{ type filter hook forward priority filter; policy accept; }'", table, table),
			fmt.Sprintf("sudo nft list chain %s prerouting | grep -Fq '%s' || sudo nft add rule %s prerouting %s", table, dnatRule, table, dnatRule),
			fmt.Sprintf("sudo nft list chain %s forward | grep -Fq '%s' || sudo nft add rule %s forward %s", table, acceptRule, table, acceptRule),
		}, "; "), nil
	}

	// iptables: -t nat -A PREROUTING ... -j DNAT --to-destination <toaddr>:<toport>
	// plus the matching FORWARD accept, guarded with -C for idempotency.
	dnatRule := fmt.Sprintf("-p %s --dport %s -j DNAT --to-destination %s:%s", proto, portVal.Normalized, toaddr, toportVal.Normalized)
	forwardRule := fmt.Sprintf("-p %s --dport %s -j ACCEPT", proto, toportVal.Normalized)
	if toaddr != "" {
		forwardRule = fmt.Sprintf("-p %s -d %s --dport %s -j ACCEPT", proto, toaddr, toportVal.Normalized)
	}
	return fmt.Sprintf(
		"sudo iptables -t nat -C PREROUTING %s 2>/dev/null || sudo iptables -t nat -A PREROUTING %s; sudo iptables -C FORWARD %s 2>/dev/null || sudo iptables -A FORWARD %s",
		dnatRule, dnatRule, forwardRule, forwardRule,
	), nil
}

// BuildFirewallMasqueradeCommand builds the idempotent command that enables or
// disables source NAT masquerade for the given firewall flavour.
func BuildFirewallMasqueradeCommand(pluginName, zone string, enabled bool) (string, error) {
	switch normalizeFirewallFlavour(pluginName) {
	case "firewalld":
		zoneVal := validateZone(zone)
		action := "--add-masquerade"
		if !enabled {
			action = "--remove-masquerade"
		}
		return fmt.Sprintf("sudo firewall-cmd --permanent --zone=%s %s && sudo firewall-cmd --reload", zoneVal.Normalized, action), nil

	case "nftables":
		table := "inet filter"
		cmds := []string{
			fmt.Sprintf("sudo nft list table %s >/dev/null 2>&1 || sudo nft add table %s", table, table),
			fmt.Sprintf("sudo nft list chain %s postrouting >/dev/null 2>&1 || sudo nft add chain %s postrouting '{ type nat hook postrouting priority srcnat; policy accept; }'", table, table),
		}
		if enabled {
			cmds = append(cmds, fmt.Sprintf("sudo nft list chain %s postrouting | grep -qw masquerade || sudo nft add rule %s postrouting masquerade", table, table))
		} else {
			cmds = append(cmds, fmt.Sprintf("sudo nft -a list chain %s postrouting 2>/dev/null | awk 'index($0,\"masquerade\"){print $NF}' | while read -r h; do sudo nft delete rule %s postrouting handle \"$h\"; done || true", table, table))
		}
		return strings.Join(cmds, "; "), nil

	case "iptables":
		if enabled {
			return "sudo iptables -t nat -C POSTROUTING -j MASQUERADE 2>/dev/null || sudo iptables -t nat -A POSTROUTING -j MASQUERADE", nil
		}
		return "sudo iptables -t nat -C POSTROUTING -j MASQUERADE 2>/dev/null && sudo iptables -t nat -D POSTROUTING -j MASQUERADE || true", nil

	case "ufw":
		return "", errors.New("masquerade toggle is not implemented for ufw hosts")
	}
	return "", fmt.Errorf("unsupported firewall plugin %q for masquerade", pluginName)
}

func BuildFirewallAddCommand(rule FirewallRuleInput) (string, error) {
	flavour := normalizeFirewallFlavour(rule.Plugin)
	if flavour != "firewalld" {
		if strings.TrimSpace(rule.Toport) != "" {
			return natForwardCommand(flavour, rule)
		}
		return "", fmt.Errorf("firewall plugin %q has no direct command builder for this rule; stage it through POST /api/nodes/:id/plugins/firewall/%s/actions/allow_port", flavour, flavour)
	}

	zoneVal := validateZone(rule.Zone)
	zone := zoneVal.Normalized
	policy := strings.ToUpper(strings.TrimSpace(rule.Policy))
	if policy == "" {
		policy = "ACCEPT"
	}
	proto := "tcp"
	if strings.TrimSpace(rule.Protocol) != "" {
		pv := validateProtocol(rule.Protocol)
		if !pv.Valid {
			return "", errors.New(pv.Error)
		}
		proto = pv.Normalized
	}

	// Forward port (NAT MASQ)
	if strings.TrimSpace(rule.Toport) != "" {
		portVal := validatePortOrRange(rule.Port)
		if !portVal.Valid {
			return "", errors.New(portVal.Error)
		}
		toportVal := validatePortOrRange(rule.Toport)
		if !toportVal.Valid {
			return "", errors.New(toportVal.Error)
		}
		spec := fmt.Sprintf("port=%s:proto=%s:toport=%s", portVal.Normalized, proto, toportVal.Normalized)
		if strings.TrimSpace(rule.Toaddr) != "" {
			spec += fmt.Sprintf(":toaddr=%s", strings.TrimSpace(rule.Toaddr))
		}
		cmd := fmt.Sprintf("sudo firewall-cmd --permanent --zone=%s --add-forward-port=%s && sudo firewall-cmd --reload", zone, spec)
		return cmd, nil
	}

	// Service rule
	if strings.TrimSpace(rule.Service) != "" {
		svc := regexp.MustCompile(`[^a-zA-Z0-9_-]`).ReplaceAllString(strings.TrimSpace(rule.Service), "")
		return fmt.Sprintf("sudo firewall-cmd --permanent --zone=%s --add-service=%s && sudo firewall-cmd --reload", zone, svc), nil
	}

	sourceVal := validateCidrOrIp(rule.Source)
	source := sourceVal.Normalized
	if source == "" {
		source = "any"
	}

	ifaceVal := validateInterfaceName(rule.InInterface)
	inIface := ifaceVal.Normalized
	if inIface == "" {
		inIface = "any"
	}

	isRichRuleRequired := source != "any" || policy != "ACCEPT" || proto == "icmp"

	if isRichRuleRequired {
		parts := []string{`rule family="ipv4"`}
		if source != "any" {
			parts = append(parts, fmt.Sprintf(`source address="%s"`, source))
		}
		if proto == "icmp" {
			parts = append(parts, `protocol value="icmp"`)
		} else if strings.TrimSpace(rule.Port) != "" {
			pv := validatePortOrRange(rule.Port)
			p := pv.Normalized
			if p == "" {
				p = strings.TrimSpace(rule.Port)
			}
			parts = append(parts, fmt.Sprintf(`port port="%s" protocol="%s"`, p, proto))
		}
		parts = append(parts, strings.ToLower(policy))

		richRule := strings.Join(parts, " ")
		cmd := fmt.Sprintf("sudo firewall-cmd --permanent --zone=%s --add-rich-rule='%s'", zone, richRule)
		if inIface != "any" {
			cmd += fmt.Sprintf(" && sudo firewall-cmd --permanent --zone=%s --add-interface=%s", zone, inIface)
		}
		cmd += " && sudo firewall-cmd --reload"
		return cmd, nil
	}

	portVal := validatePortOrRange(rule.Port)
	p := portVal.Normalized
	if p == "" {
		p = strings.TrimSpace(rule.Port)
	}
	if p == "" {
		p = "80"
	}
	cmd := fmt.Sprintf("sudo firewall-cmd --permanent --zone=%s --add-port=%s/%s", zone, p, proto)
	if inIface != "any" {
		cmd += fmt.Sprintf(" && sudo firewall-cmd --permanent --zone=%s --add-interface=%s", zone, inIface)
	}
	cmd += " && sudo firewall-cmd --reload"
	return cmd, nil
}

type FirewallDeleteInput struct {
	Zone     string `json:"zone,omitempty"`
	RuleID   string `json:"ruleId,omitempty"`
	Port     string `json:"port,omitempty"`
	Protocol string `json:"protocol,omitempty"`
	Service  string `json:"service,omitempty"`
	RawRule  string `json:"rawRule,omitempty"`
}

func BuildFirewallDeleteCommand(rule FirewallDeleteInput) (string, error) {
	zoneVal := validateZone(rule.Zone)
	zone := zoneVal.Normalized

	if strings.TrimSpace(rule.RawRule) != "" {
		return fmt.Sprintf("sudo firewall-cmd --permanent --zone=%s --remove-rich-rule='%s' && sudo firewall-cmd --reload", zone, strings.TrimSpace(rule.RawRule)), nil
	}

	if strings.HasPrefix(rule.RuleID, "rich:") {
		decoded, err := base64.StdEncoding.DecodeString(rule.RuleID[5:])
		if err == nil {
			return fmt.Sprintf("sudo firewall-cmd --permanent --zone=%s --remove-rich-rule='%s' && sudo firewall-cmd --reload", zone, strings.TrimSpace(string(decoded))), nil
		}
	}

	if strings.HasPrefix(rule.RuleID, "svc:") {
		svc := strings.TrimSpace(rule.RuleID[4:])
		return fmt.Sprintf("sudo firewall-cmd --permanent --zone=%s --remove-service=%s && sudo firewall-cmd --reload", zone, svc), nil
	}

	if strings.TrimSpace(rule.Service) != "" {
		return fmt.Sprintf("sudo firewall-cmd --permanent --zone=%s --remove-service=%s && sudo firewall-cmd --reload", zone, strings.TrimSpace(rule.Service)), nil
	}

	portSpec := rule.Port
	if portSpec == "" {
		portSpec = rule.RuleID
	}
	portFormatted := portSpec
	if !strings.Contains(portFormatted, "/") && strings.TrimSpace(rule.Protocol) != "" {
		portFormatted = fmt.Sprintf("%s/%s", portFormatted, strings.TrimSpace(rule.Protocol))
	}
	return fmt.Sprintf("sudo firewall-cmd --permanent --zone=%s --remove-port=%s && sudo firewall-cmd --reload", zone, portFormatted), nil
}

// ===========================================================================
// Shares & Users Command Builders & Sanitizers
// Ported from src/utils/sharesManager.ts
// ===========================================================================

func escapeShellSingleQuote(str string) string {
	return strings.ReplaceAll(str, "'", "'\\''")
}

var (
	reUsername  = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_-]{0,31}$`)
	rePathChars = regexp.MustCompile("[\x00\r\n;`$&|<>\"']")
	reShareName = regexp.MustCompile(`^[a-zA-Z0-9_\-. ]+$`)
	reNfsClient = regexp.MustCompile(`^[a-zA-Z0-9_.*@\-/]+$`)
	reNfsOption = regexp.MustCompile(`^[a-zA-Z0-9_\-=]+$`)
)

func SanitizeUsername(username string) (string, error) {
	trimmed := strings.TrimSpace(username)
	if trimmed == "" {
		return "", errors.New("Username cannot be empty")
	}
	if !reUsername.MatchString(trimmed) {
		return "", fmt.Errorf("Invalid username \"%s\". Usernames must start with a letter or underscore and contain only letters, numbers, underscores, or dashes (max 32 chars).", trimmed)
	}
	return trimmed, nil
}

func SanitizePath(rawPath string) (string, error) {
	trimmed := strings.TrimSpace(rawPath)
	if trimmed == "" {
		return "", errors.New("Path cannot be empty")
	}
	if !strings.HasPrefix(trimmed, "/") {
		return "", fmt.Errorf("Path must be an absolute path starting with \"/\": \"%s\"", trimmed)
	}
	if rePathChars.MatchString(trimmed) {
		return "", fmt.Errorf("Path contains invalid shell characters: \"%s\"", trimmed)
	}
	// Normalize consecutive slashes
	reSlashes := regexp.MustCompile(`/+`)
	return reSlashes.ReplaceAllString(trimmed, "/"), nil
}

func SanitizeShareName(name string) (string, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return "", errors.New("Share name cannot be empty")
	}
	if len(trimmed) > 80 {
		return "", errors.New("Share name cannot exceed 80 characters")
	}
	reserved := map[string]bool{"global": true, "printers": true, "print$": true, "homes": true}
	if reserved[strings.ToLower(trimmed)] {
		return "", fmt.Errorf("Share name \"%s\" is a reserved Samba configuration section", trimmed)
	}
	if !reShareName.MatchString(trimmed) {
		return "", fmt.Errorf("Share name contains invalid characters: \"%s\"", trimmed)
	}
	return trimmed, nil
}

func SanitizeNfsClient(client string) (string, error) {
	trimmed := strings.TrimSpace(client)
	if trimmed == "" || trimmed == "*" {
		return "*", nil
	}
	tokens := strings.Fields(trimmed)
	for _, token := range tokens {
		if !reNfsClient.MatchString(token) {
			return "", fmt.Errorf("Invalid NFS client specifier: \"%s\"", token)
		}
	}
	return strings.Join(tokens, " "), nil
}

func SanitizeNfsOptions(options string) (string, error) {
	defaultOpts := "rw,sync,no_subtree_check,no_root_squash"
	trimmed := strings.TrimSpace(options)
	if trimmed == "" {
		return defaultOpts, nil
	}
	parts := strings.Split(trimmed, ",")
	var cleanParts []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if !reNfsOption.MatchString(p) {
			return "", fmt.Errorf("Invalid NFS export option: \"%s\"", p)
		}
		cleanParts = append(cleanParts, p)
	}
	return strings.Join(cleanParts, ","), nil
}

type NfsShareConfig struct {
	Path    string `json:"path"`
	Client  string `json:"client,omitempty"`
	Options string `json:"options,omitempty"`
}

func GenerateNfsExportLine(config NfsShareConfig) (string, error) {
	cleanPath, err := SanitizePath(config.Path)
	if err != nil {
		return "", err
	}
	cleanClient, err := SanitizeNfsClient(config.Client)
	if err != nil {
		return "", err
	}
	cleanOptions, err := SanitizeNfsOptions(config.Options)
	if err != nil {
		return "", err
	}

	clientTokens := strings.Fields(cleanClient)
	var exportClients []string
	for _, c := range clientTokens {
		exportClients = append(exportClients, fmt.Sprintf("%s(%s)", c, cleanOptions))
	}

	return fmt.Sprintf("%s %s", cleanPath, strings.Join(exportClients, " ")), nil
}

type SmbShareConfig struct {
	Name       string `json:"name"`
	Path       string `json:"path"`
	Comment    string `json:"comment,omitempty"`
	ReadOnly   bool   `json:"readOnly,omitempty"`
	ValidUsers string `json:"validUsers,omitempty"`
	GuestOk    bool   `json:"guestOk,omitempty"`
	Browseable *bool  `json:"browseable,omitempty"`
}

func GenerateSmbShareBlock(config SmbShareConfig) (string, error) {
	cleanName, err := SanitizeShareName(config.Name)
	if err != nil {
		return "", err
	}
	cleanPath, err := SanitizePath(config.Path)
	if err != nil {
		return "", err
	}
	reNewlines := regexp.MustCompile(`[\r\n]+`)
	comment := reNewlines.ReplaceAllString(strings.TrimSpace(config.Comment), " ")
	readOnlyStr := "no"
	if config.ReadOnly {
		readOnlyStr = "yes"
	}
	guestOkStr := "no"
	if config.GuestOk {
		guestOkStr = "yes"
	}
	browseableStr := "yes"
	if config.Browseable != nil && !*config.Browseable {
		browseableStr = "no"
	}

	lines := []string{
		fmt.Sprintf("[%s]", cleanName),
	}
	if comment != "" {
		lines = append(lines, fmt.Sprintf("   comment = %s", comment))
	}
	lines = append(lines,
		fmt.Sprintf("   path = %s", cleanPath),
		fmt.Sprintf("   read only = %s", readOnlyStr),
		fmt.Sprintf("   guest ok = %s", guestOkStr),
		fmt.Sprintf("   browseable = %s", browseableStr),
		"   create mask = 0664",
		"   directory mask = 0775",
	)

	if strings.TrimSpace(config.ValidUsers) != "" {
		userTokens := regexp.MustCompile(`[\s,]+`).Split(strings.TrimSpace(config.ValidUsers), -1)
		var sanitizedUsers []string
		for _, u := range userTokens {
			u = strings.TrimSpace(u)
			if u == "" {
				continue
			}
			if strings.HasPrefix(u, "@") {
				cleanU, err := SanitizeUsername(u[1:])
				if err != nil {
					return "", err
				}
				sanitizedUsers = append(sanitizedUsers, "@"+cleanU)
			} else {
				cleanU, err := SanitizeUsername(u)
				if err != nil {
					return "", err
				}
				sanitizedUsers = append(sanitizedUsers, cleanU)
			}
		}
		if len(sanitizedUsers) > 0 {
			lines = append(lines, fmt.Sprintf("   valid users = %s", strings.Join(sanitizedUsers, " ")))
		}
	}

	return strings.Join(lines, "\n"), nil
}

func BuildNfsSaveCommand(config NfsShareConfig, shareName string) (string, error) {
	line, err := GenerateNfsExportLine(config)
	if err != nil {
		return "", err
	}
	cleanPath, err := SanitizePath(config.Path)
	if err != nil {
		return "", err
	}

	name := shareName
	if name == "" {
		pathParts := strings.Split(cleanPath, "/")
		for i := len(pathParts) - 1; i >= 0; i-- {
			if pathParts[i] != "" {
				name = pathParts[i]
				break
			}
		}
		if name == "" {
			name = "share"
		}
	}
	cleanName := regexp.MustCompile(`[^a-z0-9_-]`).ReplaceAllString(strings.ToLower(name), "_")
	escapedLine := escapeShellSingleQuote(line)

	commands := []string{
		"sudo mkdir -p /etc/exports.d",
		fmt.Sprintf("printf '%%s\\n' '%s' | sudo tee /etc/exports.d/cockpit-%s.exports >/dev/null", escapedLine, cleanName),
		"sudo exportfs -ra",
	}
	return strings.Join(commands, " && "), nil
}

func BuildSmbSaveCommand(config SmbShareConfig) (string, error) {
	block, err := GenerateSmbShareBlock(config)
	if err != nil {
		return "", err
	}
	cleanName, err := SanitizeShareName(config.Name)
	if err != nil {
		return "", err
	}
	escapedBlock := escapeShellSingleQuote(block)

	jsonCleanName, _ := json.Marshal(cleanName)
	pythonScript := fmt.Sprintf(`import sys, re
conf_path = '/etc/samba/smb.conf'
try:
    with open(conf_path, 'r') as f: content = f.read()
except Exception: content = ''
sec_name = %s
pattern = r'(?ms)^\s*\[' + re.escape(sec_name) + r'\]\s*$.*?(?=^\s*\[|\Z)'
new_block = sys.stdin.read().strip() + '\n'
if re.search(pattern, content):
    content = re.sub(pattern, new_block, content)
else:
    if content and not content.endswith('\n'): content += '\n'
    content += '\n' + new_block
tmp_path = sys.argv[1] if len(sys.argv) > 1 else '/tmp/smb.conf.$$.tmp'
with open(tmp_path, 'w') as f: f.write(content)`, string(jsonCleanName))

	escapedPython := escapeShellSingleQuote(pythonScript)

	commands := []string{
		`TMP_FILE="/tmp/smb.conf.$$.tmp"`,
		fmt.Sprintf("printf '%%s\\n' '%s' | python3 -c '%s' \"$TMP_FILE\"", escapedBlock, escapedPython),
		"sudo cp /etc/samba/smb.conf /etc/samba/smb.conf.bak 2>/dev/null || true",
		"sudo mv \"$TMP_FILE\" /etc/samba/smb.conf",
		"sudo smbcontrol all reload-config 2>&1 || sudo systemctl reload smbd smb 2>&1 || sudo systemctl restart smbd smb 2>&1 || true",
	}
	return strings.Join(commands, " && "), nil
}

func BuildUserCreateCommand(username string) (string, error) {
	cleanUser, err := SanitizeUsername(username)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("sudo useradd -m -s /bin/bash '%s'", cleanUser), nil
}

func BuildUserPasswdCommand(username, password string) (string, error) {
	cleanUser, err := SanitizeUsername(username)
	if err != nil {
		return "", err
	}
	if password == "" {
		return "", errors.New("Password cannot be empty")
	}
	if regexp.MustCompile("[\x00\r\n]").MatchString(password) {
		return "", errors.New("Password contains invalid control characters")
	}
	escapedPassword := escapeShellSingleQuote(password)
	return fmt.Sprintf("printf '%%s:%%s\\n' '%s' '%s' | sudo chpasswd", cleanUser, escapedPassword), nil
}

func BuildSmbPasswdCommand(username, password string) (string, error) {
	cleanUser, err := SanitizeUsername(username)
	if err != nil {
		return "", err
	}
	if password == "" {
		return "", errors.New("Password cannot be empty")
	}
	if regexp.MustCompile("[\x00\r\n]").MatchString(password) {
		return "", errors.New("Password contains invalid control characters")
	}
	escapedPassword := escapeShellSingleQuote(password)
	return fmt.Sprintf("(printf '%%s\\n' '%s'; printf '%%s\\n' '%s') | sudo smbpasswd -s -a '%s'", escapedPassword, escapedPassword, cleanUser), nil
}
