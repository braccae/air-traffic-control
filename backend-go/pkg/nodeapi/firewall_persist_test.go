package nodeapi

import (
	"strings"
	"testing"

	"air-traffic-control/backend/pkg/hostcache"
)

func sampleRules() []hostcache.FirewallRule {
	return []hostcache.FirewallRule{
		{ID: "1", Plugin: "nftables", Kind: "masquerade", Params: map[string]string{}},
		{ID: "2", Plugin: "nftables", Kind: "forward_port", Params: map[string]string{"port": "8080", "proto": "tcp", "toport": "80", "toaddr": "10.0.0.5"}},
		{ID: "3", Plugin: "nftables", Kind: "allow_port", Params: map[string]string{"port": "22", "proto": "tcp"}},
	}
}

func TestNftFragmentIsSelfContainedAndIdempotent(t *testing.T) {
	out := nftFragment(sampleRules())
	for _, want := range []string{
		"table inet atc\n",
		"delete table inet atc\n",
		"type nat hook prerouting priority dstnat",
		"tcp dport 8080 dnat to 10.0.0.5:80",
		"type nat hook postrouting priority srcnat",
		"masquerade",
		"ip daddr 10.0.0.5 tcp dport 80 accept",
		"type filter hook input priority filter",
		"tcp dport 22 accept",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("nft fragment missing %q\n%s", want, out)
		}
	}
	if strings.Count(out, "masquerade\n") != 1 {
		t.Errorf("masquerade must be emitted exactly once:\n%s", out)
	}
}

func TestNftFragmentIsEmptyButValidWithoutRules(t *testing.T) {
	out := nftFragment(nil)
	if !strings.Contains(out, "table inet atc") || strings.Contains(out, "dnat") {
		t.Fatalf("empty model must still render a clean table:\n%s", out)
	}
}

func TestIptablesRestoreUsesOwnChains(t *testing.T) {
	out := iptablesRestore(sampleRules())
	for _, want := range []string{
		"*nat\n",
		":" + iptNatChain + " - [0:0]",
		"-A " + iptNatChain + " -j MASQUERADE",
		"-A " + iptNatChain + " -p tcp --dport 8080 -j DNAT --to-destination 10.0.0.5:80",
		"*filter\n",
		"-A " + iptForwardChain + " -d 10.0.0.5 -p tcp --dport 80 -j ACCEPT",
		"COMMIT",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("iptables restore missing %q\n%s", want, out)
		}
	}
	if strings.Contains(out, "PREROUTING") || strings.Contains(out, "POSTROUTING") {
		t.Errorf("ATC must never write foreign chains directly:\n%s", out)
	}
}

func TestPersistStepsShape(t *testing.T) {
	nft := firewallPersistSteps("nftables", sampleRules())
	if len(nft) != 8 {
		t.Fatalf("nftables graph should be dirs/drop-in/fragment/apply/enable/guard/verify, got %d", len(nft))
	}
	if nft[0].Parameters["path"] != "/etc/nftables.d" || nft[0].Parameters["state"] != "directory" {
		t.Fatalf("first step must create the fragment directory: %+v", nft[0].Parameters)
	}
	if nft[2].Parameters["dest"] != nftDropInPath {
		t.Fatalf("drop-in dest = %v", nft[2].Parameters["dest"])
	}
	if !strings.Contains(nft[2].Parameters["content"].(string), "ExecStartPost=/usr/sbin/nft -f "+nftFragmentPath) {
		t.Fatalf("drop-in must replay the fragment: %v", nft[2].Parameters["content"])
	}
	if nft[5].Module != "ansible.builtin.systemd" {
		t.Fatalf("expected systemd enable step, got %s", nft[5].Module)
	}
	if !nft[6].IgnoreErrors {
		t.Fatal("the nftables.service guard must fail without halting the queue")
	}
	if nft[3].Parameters["dest"] != nftFragmentPath {
		t.Fatalf("fragment dest = %v", nft[3].Parameters["dest"])
	}

	ipt := firewallPersistSteps("iptables", sampleRules())
	if len(ipt) != 6 {
		t.Fatalf("iptables graph should be 3 copies + apply + unit + verify, got %d", len(ipt))
	}
	if ipt[4].Module != "ansible.builtin.systemd" {
		t.Fatalf("expected a systemd step to enable the unit, got %s", ipt[4].Module)
	}
	if enabled, _ := ipt[4].Parameters["enabled"].(bool); !enabled {
		t.Fatal("atc-firewall.service must be enabled at boot")
	}

	if steps := firewallPersistSteps("firewalld", nil); len(steps) != 0 {
		t.Fatalf("firewalld is persisted by its native module, got %d extra steps", len(steps))
	}
}

func TestFirewallRuleIDIsDeterministic(t *testing.T) {
	body := FirewallRuleInput{Port: "8080", Protocol: "tcp", Toport: "80", Toaddr: "10.0.0.5"}
	a := firewallRuleID("node-1", "nftables", "forward_port", body)
	b := firewallRuleID("node-1", "nftables", "forward_port", body)
	if a != b {
		t.Fatalf("rule id must be stable, got %q and %q", a, b)
	}
	if strings.ContainsAny(a, "/: ") {
		t.Fatalf("rule id must be shell/url safe: %q", a)
	}
}
