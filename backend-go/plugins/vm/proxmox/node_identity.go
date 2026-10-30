package proxmox

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"sort"
	"strings"

	"air-traffic-control/backend/pkg/plugin"
)

// A Proxmox cluster exposes every guest of every member through
// /cluster/resources. The cockpit view, however, is opened for one inventory
// host, so the listing must be scoped to the PVE node that host represents.
// These helpers resolve that node name from real data (operator override, the
// PVE API's own `local` marker, or the host's SSH identity) and filter the
// cluster listing accordingly. They never fall back to "show everything".

// normalizeNodeName canonicalises a Proxmox node name for comparison.
func normalizeNodeName(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// filterGuestsForNode keeps only the guests that belong to the given PVE node.
//
// Every guest row carries the `node` field from /cluster/resources (or the
// per-node qemu/lxc endpoints). Guests hosted by other cluster members are
// dropped. If the node name is unknown the read fails loudly instead of
// returning the whole cluster.
func filterGuestsForNode(guests []GuestResource, nodeName string) ([]GuestResource, error) {
	want := normalizeNodeName(nodeName)
	if want == "" {
		return nil, fmt.Errorf("cannot scope Proxmox guests: the PVE node name is unknown")
	}
	scoped := make([]GuestResource, 0, len(guests))
	for _, g := range guests {
		if normalizeNodeName(g.Node) == want {
			scoped = append(scoped, g)
		}
	}
	return scoped, nil
}

// localNodeName returns the node flagged local=1 in /cluster/status. Exactly
// one member may carry that flag; anything else is reported as ambiguous.
func localNodeName(entries []ClusterStatusEntry) (string, error) {
	var names []string
	for _, e := range entries {
		if !strings.EqualFold(strings.TrimSpace(e.Type), "node") || e.Local != 1 {
			continue
		}
		if n := strings.TrimSpace(e.Name); n != "" {
			names = append(names, n)
		}
	}
	switch len(names) {
	case 1:
		return names[0], nil
	case 0:
		return "", fmt.Errorf("/cluster/status reported no local node (members: %s)", describeStatusEntries(entries))
	default:
		sort.Strings(names)
		return "", fmt.Errorf("/cluster/status reported multiple local nodes (%s)", strings.Join(names, ", "))
	}
}

// nodeNameByIP matches the configured API host against the member IPs in
// /cluster/status. This covers endpoints that do not expose the local flag
// (e.g. a proxied/shared address in front of a single member).
func nodeNameByIP(entries []ClusterStatusEntry, apiHost string) (string, error) {
	want := normalizeNodeName(hostOnly(apiHost))
	if want == "" {
		return "", fmt.Errorf("no Proxmox API host configured to match against cluster members")
	}
	var names []string
	for _, e := range entries {
		if !strings.EqualFold(strings.TrimSpace(e.Type), "node") {
			continue
		}
		if normalizeNodeName(hostOnly(e.IP)) != want {
			continue
		}
		if n := strings.TrimSpace(e.Name); n != "" {
			names = append(names, n)
		}
	}
	switch len(names) {
	case 1:
		return names[0], nil
	case 0:
		return "", fmt.Errorf("no cluster member in /cluster/status matches API host %q (members: %s)", want, describeStatusEntries(entries))
	default:
		sort.Strings(names)
		return "", fmt.Errorf("API host %q matches multiple cluster members (%s)", want, strings.Join(names, ", "))
	}
}

func describeStatusEntries(entries []ClusterStatusEntry) string {
	var parts []string
	for _, e := range entries {
		if strings.EqualFold(strings.TrimSpace(e.Type), "cluster") {
			continue
		}
		part := strings.TrimSpace(e.Name)
		if e.IP != "" {
			part = fmt.Sprintf("%s (%s)", part, e.IP)
		}
		if e.Local == 1 {
			part += " [local]"
		}
		parts = append(parts, part)
	}
	if len(parts) == 0 {
		return "none"
	}
	sort.Strings(parts)
	return strings.Join(parts, ", ")
}

// hostOnly strips a port (and IPv6 brackets) from a host[:port] string.
func hostOnly(host string) string {
	h := strings.TrimSpace(host)
	if h == "" {
		return ""
	}
	if strings.HasPrefix(h, "http://") || strings.HasPrefix(h, "https://") {
		if i := strings.Index(h, "://"); i >= 0 {
			h = h[i+3:]
		}
		if i := strings.IndexAny(h, "/"); i >= 0 {
			h = h[:i]
		}
	}
	if hostname, _, err := net.SplitHostPort(h); err == nil {
		return strings.Trim(hostname, "[]")
	}
	return strings.Trim(h, "[]")
}

// nodeInCluster reports whether name is one of the cluster members.
func nodeInCluster(name string, nodes []NodeInfo) bool {
	want := normalizeNodeName(name)
	for _, n := range nodes {
		if normalizeNodeName(n.Node) == want {
			return true
		}
	}
	return false
}

// nodeList renders cluster member names for error messages.
func nodeList(nodes []NodeInfo) string {
	names := make([]string, 0, len(nodes))
	for _, n := range nodes {
		if v := strings.TrimSpace(n.Node); v != "" {
			names = append(names, v)
		}
	}
	if len(names) == 0 {
		return "none"
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// resolveNodeName determines which Proxmox node the inspected host represents.
//
// Resolution order (first hit wins, everything is real data):
//  1. the proxmox_node_name override (operator-declared identity),
//  2. the member PVE flags local=1 in /cluster/status,
//  3. a /cluster/status member whose IP equals the configured API host,
//  4. the only member returned by /nodes (standalone host),
//  5. the host's own SSH identity (hostname -s / pvesh /cluster/status).
//
// If none of these produce a name the read fails; the caller must not fall back
// to the cluster-wide listing.
func resolveNodeName(ctx context.Context, c *Client, r plugin.Runner, cfg Config, nodes []NodeInfo) (string, error) {
	if declared := strings.TrimSpace(cfg.NodeName); declared != "" {
		return declared, nil
	}

	var reasons []string
	if c != nil {
		entries, err := c.ClusterStatus(ctx)
		if err != nil {
			reasons = append(reasons, "api /cluster/status: "+err.Error())
		} else {
			if n, err := localNodeName(entries); err == nil {
				return n, nil
			} else {
				reasons = append(reasons, err.Error())
			}
			if n, err := nodeNameByIP(entries, cfg.Host); err == nil {
				return n, nil
			} else {
				reasons = append(reasons, err.Error())
			}
		}
	}

	if len(nodes) == 1 {
		if n := strings.TrimSpace(nodes[0].Node); n != "" {
			return n, nil
		}
	} else if len(nodes) > 1 {
		reasons = append(reasons, fmt.Sprintf("api /nodes lists %d members (%s) and none could be identified as this host", len(nodes), nodeList(nodes)))
	}

	if r != nil {
		if n, err := resolveNodeNameViaSSH(ctx, r, nodes); err == nil {
			return n, nil
		} else {
			reasons = append(reasons, err.Error())
		}
	}

	return "", fmt.Errorf("cannot determine which Proxmox node %q represents: %s (set the proxmox_node_name override to the PVE node name)",
		cfg.Host, strings.Join(reasons, "; "))
}

// resolveNodeNameViaSSH asks the host itself for its identity. Proxmox node
// names are the host's short hostname, and pvesh is present on every PVE node.
func resolveNodeNameViaSSH(ctx context.Context, r plugin.Runner, nodes []NodeInfo) (string, error) {
	if r == nil {
		return "", fmt.Errorf("no runner available for the ssh identity probe")
	}
	var reasons []string

	if out, err := r.RunCommand(ctx, "hostname -s"); err != nil {
		reasons = append(reasons, "hostname -s: "+err.Error())
	} else if n := normalizeNodeName(out); n != "" && !strings.ContainsAny(n, " \t\n") {
		// Only trust the short hostname when it agrees with the cluster when
		// the cluster member list is available.
		if len(nodes) == 0 || nodeInCluster(n, nodes) {
			return n, nil
		}
		reasons = append(reasons, fmt.Sprintf("hostname -s returned %q which is not a cluster member (%s)", n, nodeList(nodes)))
	} else {
		reasons = append(reasons, "hostname -s returned no usable node name")
	}

	out, err := r.RunCommand(ctx, "pvesh get /cluster/status --output-format json 2>/dev/null")
	if err != nil {
		reasons = append(reasons, "pvesh /cluster/status: "+err.Error())
	} else {
		var entries []ClusterStatusEntry
		if uerr := json.Unmarshal([]byte(strings.TrimSpace(out)), &entries); uerr != nil {
			reasons = append(reasons, "pvesh /cluster/status: "+uerr.Error())
		} else if n, nerr := localNodeName(entries); nerr == nil {
			return n, nil
		} else {
			reasons = append(reasons, nerr.Error())
		}
	}

	return "", fmt.Errorf("ssh identity probe failed: %s", strings.Join(reasons, "; "))
}
