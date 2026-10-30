package proxmox

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"air-traffic-control/backend/pkg/plugin"
)

// readViaSSH uses the node's existing SSH credential and pvesh (which ships
// with every Proxmox host) to enumerate the node. It is the fallback used
// when no Proxmox API token/password is configured, so freshly discovered
// pve-tagged hosts still show their guests.
//
// The listing is scoped to the node the host represents (resolved from its own
// SSH identity); guests of other cluster members are excluded.
func readViaSSH(ctx context.Context, r plugin.Runner, cfg Config) (map[string]any, error) {
	if r == nil {
		return nil, fmt.Errorf("no runner available for ssh fallback")
	}
	host := cfg.Host

	version := "unknown"
	release := ""
	if out, err := r.RunCommand(ctx, "pvesh get /version --output-format json 2>/dev/null"); err == nil {
		var v map[string]any
		if json.Unmarshal([]byte(strings.TrimSpace(out)), &v) == nil {
			if s, ok := v["version"].(string); ok && s != "" {
				version = s
			}
			if s, ok := v["release"].(string); ok {
				release = s
			}
		}
	}

	// Nodes.
	var nodes []NodeInfo
	if out, err := r.RunCommand(ctx, "pvesh get /nodes --output-format json 2>/dev/null"); err == nil {
		var raw []map[string]any
		if json.Unmarshal([]byte(strings.TrimSpace(out)), &raw) == nil {
			for _, n := range raw {
				ni := NodeInfo{}
				if v, ok := n["node"].(string); ok {
					ni.Node = v
				}
				if v, ok := n["status"].(string); ok {
					ni.Status = v
				}
				if v, ok := n["type"].(string); ok {
					ni.Type = v
				}
				if v, ok := n["maxcpu"].(float64); ok {
					ni.MaxCPU = int(v)
				}
				if v, ok := n["maxmem"].(float64); ok {
					ni.MaxMem = int64(v)
				}
				if v, ok := n["mem"].(float64); ok {
					ni.Mem = int64(v)
				}
				if v, ok := n["uptime"].(float64); ok {
					ni.Uptime = int64(v)
				}
				nodes = append(nodes, ni)
			}
		}
	}

	// Resolve which node this host is before listing guests: the cluster-wide
	// resource list must never be shown unscoped.
	nodeName, err := resolveNodeName(ctx, nil, r, cfg, nodes)
	if err != nil {
		return nil, err
	}
	if len(nodes) > 0 && !nodeInCluster(nodeName, nodes) {
		return nil, fmt.Errorf("proxmox node %q (resolved for %s) is not a member of this cluster (%s); set the proxmox_node_name override to the correct node",
			nodeName, host, nodeList(nodes))
	}

	// Cluster resources (VMs + containers).
	var all []GuestResource
	if out, err := r.RunCommand(ctx, "pvesh get /cluster/resources --type vm --output-format json 2>/dev/null"); err == nil {
		_ = json.Unmarshal([]byte(strings.TrimSpace(out)), &all)
	}
	scoped, err := filterGuestsForNode(all, nodeName)
	if err != nil {
		return nil, err
	}

	var vms, containers []guestSummary
	for _, res := range scoped {
		g := guestSummary{
			VMID:    res.VMID,
			Name:    res.Name,
			Node:    nodeName,
			Type:    res.Type,
			Status:  res.Status,
			MaxMem:  res.MaxMem,
			MaxDisk: res.MaxDisk,
			MaxCPU:  res.MaxCPU,
			Uptime:  res.Uptime,
		}
		switch res.Type {
		case "qemu":
			vms = append(vms, g)
		case "lxc":
			containers = append(containers, g)
		}
	}

	sort.Slice(vms, func(i, j int) bool { return vms[i].VMID < vms[j].VMID })
	sort.Slice(containers, func(i, j int) bool { return containers[i].VMID < containers[j].VMID })

	for i := range vms {
		vms[i].Console = staticConsole(cfg, nodes, vms[i], "qemu")
	}
	for i := range containers {
		containers[i].Console = staticConsole(cfg, nodes, containers[i], "lxc")
	}

	summary := map[string]any{
		"node":       nodeName,
		"nodes":      len(nodes),
		"vms":        len(vms),
		"containers": len(containers),
		"running":    countRunning(vms) + countRunning(containers),
		"excluded":   len(all) - len(scoped),
		"filteredTo": nodeName,
	}

	return map[string]any{
		"endpoint":   fmt.Sprintf("ssh://%s", host),
		"version":    version,
		"release":    release,
		"node":       nodeName,
		"nodes":      nodes,
		"vms":        vms,
		"containers": containers,
		"summary":    summary,
		"errors":     []string{},
	}, nil
}
