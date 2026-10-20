package readonlyapi

import (
	"encoding/base64"
	"net/http"
	"regexp"
	"strings"
)

// firewallCmd is the exact command server.ts runs for GET /api/nodes/:id/firewall.
const firewallCmd = "sudo -n firewall-cmd --state 2>&1 || firewall-cmd --state 2>&1 || echo \"NO_FIREWALLD\"; " +
	"echo \"===ZONES===\"; " +
	"sudo -n firewall-cmd --get-active-zones 2>&1 || true; " +
	"echo \"===LIST_ALL===\"; " +
	"sudo -n firewall-cmd --list-all 2>&1 || true; " +
	"echo \"===NFT===\"; " +
	"sudo -n nft list tables 2>&1 || echo \"NO_NFT\""

// FirewallRule is a single ParsedFirewallRule from src/utils/firewallRules.ts.
type FirewallRule struct {
	ID      string `json:"id"`
	Chain   string `json:"chain"`
	Policy  string `json:"policy"`
	Proto   string `json:"proto"`
	Port    string `json:"port"`
	Source  string `json:"source"`
	Comment string `json:"comment"`
	Enabled bool   `json:"enabled"`
	Zone    string `json:"zone"`
	RawRule string `json:"rawRule,omitempty"`
}

// FirewallZone is a zone entry in the zones map.
type FirewallZone struct {
	Interfaces []string `json:"interfaces"`
	Sources    []string `json:"sources"`
}

// FirewallParsed is the return value of parseFirewallListAll.
type FirewallParsed struct {
	Services  []string
	Ports     []string
	RichRules []string
	Rules     []FirewallRule
	Zones     map[string]FirewallZone
}

var (
	reZoneName      = regexp.MustCompile(`^([a-zA-Z0-9_\-]+)`)
	reRuleSource    = regexp.MustCompile(`source address="([^"]+)"`)
	reRulePort      = regexp.MustCompile(`port port="([^"]+)"`)
	reRuleProtocol  = regexp.MustCompile(`protocol="([^"]+)"`)
	reRuleICMP      = regexp.MustCompile(`protocol value="icmp"`)
	reWhitespaceRun = regexp.MustCompile(`\s+`)
)

// splitFields mirrors `.trim().split(/\s+/).filter(Boolean)`.
func splitFields(s string) []string {
	t := strings.TrimSpace(s)
	if t == "" {
		return []string{}
	}
	parts := reWhitespaceRun.Split(t, -1)
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// parseFirewallListAll ports parseFirewallListAll() from src/utils/firewallRules.ts.
func parseFirewallListAll(listAllRaw, activeZonesRaw string) FirewallParsed {
	zones := map[string]FirewallZone{}

	currentZone := ""
	for _, line := range strings.Split(strings.TrimSpace(activeZonesRaw), "\n") {
		if m := reZoneName.FindStringSubmatch(line); m != nil && !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
			currentZone = m[1]
			if _, ok := zones[currentZone]; !ok {
				zones[currentZone] = FirewallZone{Interfaces: []string{}, Sources: []string{}}
			}
			continue
		}
		if currentZone == "" {
			continue
		}
		if strings.Contains(line, "interfaces:") {
			z := zones[currentZone]
			z.Interfaces = splitFields(strings.Replace(line, "interfaces:", "", 1))
			zones[currentZone] = z
		} else if strings.Contains(line, "sources:") {
			z := zones[currentZone]
			z.Sources = splitFields(strings.Replace(line, "sources:", "", 1))
			zones[currentZone] = z
		}
	}

	services := []string{}
	ports := []string{}
	richRules := []string{}
	inRichRules := false

	for _, line := range strings.Split(listAllRaw, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "services:"):
			services = splitFields(strings.Replace(trimmed, "services:", "", 1))
			inRichRules = false
		case strings.HasPrefix(trimmed, "ports:"):
			ports = splitFields(strings.Replace(trimmed, "ports:", "", 1))
			inRichRules = false
		case strings.HasPrefix(trimmed, "rich rules:"):
			inRichRules = true
		default:
			if !inRichRules {
				continue
			}
			if strings.HasPrefix(trimmed, "rule ") {
				richRules = append(richRules, trimmed)
			} else if trimmed != "" && !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
				inRichRules = false
			}
		}
	}

	rules := []FirewallRule{}

	// Tailscale trusted interface rule if active.
	trustedHasTS := zones["trusted"].Interfaces != nil && contains(zones["trusted"].Interfaces, "tailscale0")
	internalHasTS := zones["internal"].Interfaces != nil && contains(zones["internal"].Interfaces, "tailscale0")
	if trustedHasTS || internalHasTS {
		zone := "internal"
		if trustedHasTS {
			zone = "trusted"
		}
		rules = append(rules, FirewallRule{
			ID:      "fw-ts",
			Chain:   "INPUT",
			Policy:  "ACCEPT",
			Proto:   "all",
			Port:    "all",
			Source:  "tailscale0 (100.64.0.0/10)",
			Comment: "Tailscale Encrypted Mesh Interface (Trusted/Internal Zone)",
			Enabled: true,
			Zone:    zone,
		})
	}

	// Standard ports & port ranges.
	for _, p := range ports {
		portNum := p
		proto := "tcp"
		if idx := strings.Index(p, "/"); idx >= 0 {
			portNum = p[:idx]
			proto = p[idx+1:]
		}
		rules = append(rules, FirewallRule{
			ID:      p,
			Chain:   "INPUT",
			Policy:  "ACCEPT",
			Proto:   proto,
			Port:    portNum,
			Source:  "any",
			Comment: "Open Port (" + p + ")",
			Enabled: true,
			Zone:    "public",
		})
	}

	// Services.
	for _, s := range services {
		rules = append(rules, FirewallRule{
			ID:      "svc:" + s,
			Chain:   "INPUT",
			Policy:  "ACCEPT",
			Proto:   "tcp/udp",
			Port:    s,
			Source:  "public / lan",
			Comment: "firewalld service: " + s,
			Enabled: true,
			Zone:    "public",
		})
	}

	// Rich rules.
	for _, raw := range richRules {
		policy := "ACCEPT"
		if strings.Contains(raw, "drop") {
			policy = "DROP"
		} else if strings.Contains(raw, "reject") {
			policy = "REJECT"
		}

		proto := "any"
		port := "-"
		if reRuleICMP.MatchString(raw) {
			proto = "icmp"
			port = "icmp"
		} else if m := reRuleProtocol.FindStringSubmatch(raw); m != nil {
			proto = m[1]
		}
		if m := reRulePort.FindStringSubmatch(raw); m != nil {
			port = m[1]
		}

		source := "any"
		if m := reRuleSource.FindStringSubmatch(raw); m != nil {
			source = m[1]
		}

		rules = append(rules, FirewallRule{
			ID:      "rich:" + base64.StdEncoding.EncodeToString([]byte(raw)),
			Chain:   "INPUT",
			Policy:  policy,
			Proto:   proto,
			Port:    port,
			Source:  source,
			Comment: "Rich Rule: " + raw,
			Enabled: true,
			Zone:    "public",
			RawRule: raw,
		})
	}

	return FirewallParsed{
		Services:  services,
		Ports:     ports,
		RichRules: richRules,
		Rules:     rules,
		Zones:     zones,
	}
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// handleFirewall ports GET /api/nodes/:id/firewall.
func (a *API) handleFirewall(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	node, err := a.findNode(r.PathValue("id"))
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"success": false, "error": "Failed to probe firewall"})
		return
	}

	if a.sectionIsFresh(node.ID, "firewall", r.URL.Query().Get("refresh") == "true") {
		if a.sectionCachedResponse(w, node.ID, "firewall") {
			return
		}
	}

	_, runner, err := a.factsAndRunner(r.Context(), node.ID)
	if err != nil {
		if a.sectionCachedResponse(w, node.ID, "firewall") {
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": false, "error": err.Error()})
		return
	}

	out, err := runNodeCommand(r.Context(), runner, firewallCmd)
	if err != nil {
		if a.sectionCachedResponse(w, node.ID, "firewall") {
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": false, "error": "Failed to probe firewall"})
		return
	}

	stateRaw, rest1 := splitMarker(out, "===ZONES===")
	zonesRaw, rest2 := splitMarker(rest1, "===LIST_ALL===")
	listAllRaw, nftRaw := splitMarker(rest2, "===NFT===")

	isRunning := strings.Contains(strings.TrimSpace(stateRaw), "running")
	hasNft := !strings.Contains(nftRaw, "NO_NFT") && strings.Contains(nftRaw, "table")

	parsed := parseFirewallListAll(listAllRaw, zonesRaw)

	rules := parsed.Rules
	if !isRunning {
		rules = []FirewallRule{}
	}

	result := map[string]any{
		"status":         map[bool]string{true: "running", false: "inactive"}[isRunning],
		"nftablesActive": hasNft,
		"backend":        "nftables (firewalld)",
		"zones":          parsed.Zones,
		"services":       parsed.Services,
		"ports":          parsed.Ports,
		"rules":          rules,
	}
	a.sectionResult(w, node.ID, "firewall", result)
}
