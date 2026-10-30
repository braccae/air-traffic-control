package nodeapi

import (
	"fmt"
	"sort"
	"strings"

	"air-traffic-control/backend/pkg/ansqueue"
	"air-traffic-control/backend/pkg/hostcache"
)

// Firewall persistence for the raw flavours.
//
// firewalld is persistent through ansible.posix.firewalld (permanent: true,
// immediate: true) and needs no files. nftables and iptables have no native
// module, so ATC renders its own drop-in/chain definition and applies it with
// Ansible: `copy` writes the artifact, `command` activates it, `systemd` makes
// sure it is replayed at boot.
const (
	nftFragmentPath   = "/etc/nftables.d/90-atc.nft"
	iptRestorePath    = "/etc/atc/iptables.restore"
	iptApplyPath      = "/usr/local/sbin/atc-firewall-apply"
	iptUnitPath       = "/etc/systemd/system/atc-firewall.service"
	nftManagedTable   = "inet atc"
	nftDropInDir      = "/etc/systemd/system/nftables.service.d"
	nftDropInPath     = nftDropInDir + "/50-atc.conf"
	nftConfPath       = "/etc/nftables.conf"
	iptNatChain       = "ATC_NAT"
	iptForwardChain   = "ATC_FWD"
	firewallFileMode  = "0644"
	firewallScriptMod = "0755"
)

// nftFragment renders the ATC-owned nftables table. The file is idempotent on
// its own: `table inet atc` creates the table when absent, `delete table`
// removes the previous generation, then the new definition is loaded.
func nftFragment(rules []hostcache.FirewallRule) string {
	var masquerade bool
	type forward struct{ port, proto, toAddr, toPort string }
	var forwards []forward
	type accept struct{ port, proto string }
	var accepts []accept

	for _, rule := range rules {
		switch rule.Kind {
		case "masquerade":
			masquerade = true
		case "forward_port":
			forwards = append(forwards, forward{
				port:   rule.Params["port"],
				proto:  firstNonEmptyString(rule.Params["proto"], "tcp"),
				toAddr: rule.Params["toaddr"],
				toPort: firstNonEmptyString(rule.Params["toport"], rule.Params["port"]),
			})
		case "allow_port":
			accepts = append(accepts, accept{
				port:  rule.Params["port"],
				proto: firstNonEmptyString(rule.Params["proto"], "tcp"),
			})
		}
	}

	sort.Slice(forwards, func(i, j int) bool { return forwards[i].port < forwards[j].port })
	sort.Slice(accepts, func(i, j int) bool { return accepts[i].port < accepts[j].port })

	var b strings.Builder
	b.WriteString("# Managed by Air Traffic Control — do not edit by hand.\n")
	b.WriteString("# Regenerated from the coordinator's firewall model on every change.\n")
	b.WriteString("table " + nftManagedTable + "\n")
	b.WriteString("delete table " + nftManagedTable + "\n")
	b.WriteString("table " + nftManagedTable + " {\n")

	b.WriteString("\tchain prerouting {\n")
	b.WriteString("\t\ttype nat hook prerouting priority dstnat; policy accept;\n")
	for _, f := range forwards {
		target := f.toPort
		if f.toAddr != "" {
			target = f.toAddr + ":" + f.toPort
		}
		b.WriteString(fmt.Sprintf("\t\t%s dport %s dnat to %s\n", f.proto, f.port, target))
	}
	b.WriteString("\t}\n")

	b.WriteString("\tchain postrouting {\n")
	b.WriteString("\t\ttype nat hook postrouting priority srcnat; policy accept;\n")
	if masquerade {
		b.WriteString("\t\tmasquerade\n")
	}
	b.WriteString("\t}\n")

	b.WriteString("\tchain forward {\n")
	b.WriteString("\t\ttype filter hook forward priority filter; policy accept;\n")
	b.WriteString("\t\tct state established,related accept\n")
	for _, f := range forwards {
		if f.toAddr != "" {
			b.WriteString(fmt.Sprintf("\t\tip daddr %s %s dport %s accept\n", f.toAddr, f.proto, f.toPort))
		} else {
			b.WriteString(fmt.Sprintf("\t\t%s dport %s accept\n", f.proto, f.toPort))
		}
	}
	b.WriteString("\t}\n")

	b.WriteString("\tchain input {\n")
	b.WriteString("\t\ttype filter hook input priority filter; policy accept;\n")
	for _, a := range accepts {
		b.WriteString(fmt.Sprintf("\t\t%s dport %s accept\n", a.proto, a.port))
	}
	b.WriteString("\t}\n")
	b.WriteString("}\n")
	return b.String()
}

// iptablesRestore renders the iptables-nft restore file for ATC's own chains.
// `--noflush` plus an explicit flush in the apply script keeps foreign rules
// untouched and ATC's chains idempotent.
func iptablesRestore(rules []hostcache.FirewallRule) string {
	var masquerade bool
	type forward struct{ port, proto, toAddr, toPort string }
	var forwards []forward
	type accept struct{ port, proto string }
	var accepts []accept

	for _, rule := range rules {
		switch rule.Kind {
		case "masquerade":
			masquerade = true
		case "forward_port":
			forwards = append(forwards, forward{
				port:   rule.Params["port"],
				proto:  firstNonEmptyString(rule.Params["proto"], "tcp"),
				toAddr: rule.Params["toaddr"],
				toPort: firstNonEmptyString(rule.Params["toport"], rule.Params["port"]),
			})
		case "allow_port":
			accepts = append(accepts, accept{
				port:  rule.Params["port"],
				proto: firstNonEmptyString(rule.Params["proto"], "tcp"),
			})
		}
	}

	var b strings.Builder
	b.WriteString("# Managed by Air Traffic Control — do not edit by hand.\n")
	b.WriteString("*nat\n")
	b.WriteString(":" + iptNatChain + " - [0:0]\n")
	if masquerade {
		b.WriteString("-A " + iptNatChain + " -j MASQUERADE\n")
	}
	for _, f := range forwards {
		target := f.toAddr
		if target == "" {
			target = "127.0.0.1"
		}
		b.WriteString(fmt.Sprintf("-A %s -p %s --dport %s -j DNAT --to-destination %s:%s\n",
			iptNatChain, f.proto, f.port, target, f.toPort))
	}
	b.WriteString("COMMIT\n")

	b.WriteString("*filter\n")
	b.WriteString(":" + iptForwardChain + " - [0:0]\n")
	for _, f := range forwards {
		if f.toAddr != "" {
			b.WriteString(fmt.Sprintf("-A %s -d %s -p %s --dport %s -j ACCEPT\n",
				iptForwardChain, f.toAddr, f.proto, f.toPort))
		} else {
			b.WriteString(fmt.Sprintf("-A %s -p %s --dport %s -j ACCEPT\n",
				iptForwardChain, f.proto, f.toPort))
		}
	}
	for _, a := range accepts {
		b.WriteString(fmt.Sprintf("-A %s -p %s --dport %s -j ACCEPT\n", iptForwardChain, a.proto, a.port))
	}
	b.WriteString("COMMIT\n")
	return b.String()
}

// iptablesApplyScript bootstraps the ATC chains, jumps into them once, flushes
// them and reloads the rendered restore file. The same script runs at boot from
// atc-firewall.service, so runtime and boot state can never diverge.
const iptablesApplyScript = `#!/bin/sh
# Managed by Air Traffic Control — do not edit by hand.
set -eu
NAT_CHAIN="` + iptNatChain + `"
FWD_CHAIN="` + iptForwardChain + `"

iptables -t nat -N "$NAT_CHAIN" 2>/dev/null || true
iptables -t filter -N "$FWD_CHAIN" 2>/dev/null || true

iptables -t nat -C PREROUTING -j "$NAT_CHAIN" 2>/dev/null || iptables -t nat -I PREROUTING 1 -j "$NAT_CHAIN"
iptables -t filter -C FORWARD -j "$FWD_CHAIN" 2>/dev/null || iptables -t filter -I FORWARD 1 -j "$FWD_CHAIN"

iptables -t nat -F "$NAT_CHAIN"
iptables -t filter -F "$FWD_CHAIN"

iptables-restore --noflush ` + iptRestorePath + `
`

const iptablesUnitFile = `# Managed by Air Traffic Control — do not edit by hand.
[Unit]
Description=ATC managed firewall rules (iptables-nft)
Documentation=https://github.com/air-traffic-control
After=network-online.target netfilter-persistent.service iptables.service
Wants=network-online.target

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=` + iptApplyPath + `

[Install]
WantedBy=multi-user.target
`

// nftServiceGuard reports (as a failing, non-halting step) that the rendered
// rules are active but will not be replayed at boot because nftables.service is
// disabled. The operator asked for the failure to be visible without stopping
// the rest of the queue, so the step runs with ignore_errors.
const nftServiceGuard = `systemctl is-enabled nftables.service >/dev/null 2>&1 || { echo "nftables.service is disabled: ATC rules are active now but will NOT survive a reboot (enable it with: systemctl enable --now nftables)"; exit 1; }`

// firewallPersistSteps builds the Ansible task graph that makes a host's
// managed rules permanent for the given raw flavour.
func firewallPersistSteps(flavour string, rules []hostcache.FirewallRule) []ansqueue.Step {
	switch flavour {
	case "nftables":
		return []ansqueue.Step{
			{
				Name:       "Ensure nftables drop-in directory",
				Module:     "ansible.builtin.file",
				Parameters: map[string]any{"path": "/etc/nftables.d", "state": "directory", "mode": "0755"},
			},
			{
				Name:       "Ensure nftables service drop-in directory",
				Module:     "ansible.builtin.file",
				Parameters: map[string]any{"path": nftDropInDir, "state": "directory", "mode": "0755"},
			},
			{
				Name:   "Install nftables.service drop-in",
				Module: "ansible.builtin.copy",
				Parameters: map[string]any{
					"dest":    nftDropInPath,
					"mode":    firewallFileMode,
					"content": "[Service]\nExecStartPost=/usr/sbin/nft -f " + nftFragmentPath + "\n",
				},
			},
			{
				Name:   "Write ATC nftables fragment",
				Module: "ansible.builtin.copy",
				Parameters: map[string]any{
					"dest":    nftFragmentPath,
					"mode":    firewallFileMode,
					"content": nftFragment(rules),
				},
			},
			{
				Name:       "Apply ATC nftables fragment",
				Module:     "ansible.builtin.command",
				Parameters: map[string]any{"cmd": "nft -f " + nftFragmentPath},
			},
			{
				Name:   "Enable nftables.service so the fragment replays at boot",
				Module: "ansible.builtin.systemd",
				Parameters: map[string]any{
					"name":          "nftables.service",
					"enabled":       true,
					"state":         "started",
					"daemon_reload": true,
				},
			},
			{
				Name:         "Verify nftables boot persistence",
				Module:       "ansible.builtin.shell",
				Parameters:   map[string]any{"cmd": nftServiceGuard},
				IgnoreErrors: true,
			},
			{
				Name:       "Verify ATC nftables table",
				Module:     "ansible.builtin.command",
				Parameters: map[string]any{"cmd": "nft list table " + nftManagedTable},
			},
		}

	case "iptables":
		return []ansqueue.Step{
			{
				Name:   "Write ATC iptables restore file",
				Module: "ansible.builtin.copy",
				Parameters: map[string]any{
					"dest":    iptRestorePath,
					"mode":    firewallFileMode,
					"content": iptablesRestore(rules),
				},
			},
			{
				Name:   "Write ATC iptables apply script",
				Module: "ansible.builtin.copy",
				Parameters: map[string]any{
					"dest":    iptApplyPath,
					"mode":    firewallScriptMod,
					"content": iptablesApplyScript,
				},
			},
			{
				Name:   "Write atc-firewall.service unit",
				Module: "ansible.builtin.copy",
				Parameters: map[string]any{
					"dest":    iptUnitPath,
					"mode":    firewallFileMode,
					"content": iptablesUnitFile,
				},
			},
			{
				Name:       "Apply ATC iptables rules now",
				Module:     "ansible.builtin.command",
				Parameters: map[string]any{"cmd": iptApplyPath},
			},
			{
				Name:   "Enable ATC firewall unit at boot",
				Module: "ansible.builtin.systemd",
				Parameters: map[string]any{
					"name":          "atc-firewall.service",
					"enabled":       true,
					"state":         "started",
					"daemon_reload": true,
				},
			},
			{
				Name:       "Verify ATC iptables chains",
				Module:     "ansible.builtin.command",
				Parameters: map[string]any{"cmd": "iptables -t nat -S " + iptNatChain},
			},
		}
	}
	return nil
}

func firstNonEmptyString(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
