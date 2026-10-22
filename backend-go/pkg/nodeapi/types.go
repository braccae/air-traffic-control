package nodeapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"air-traffic-control/backend/pkg/plugin"
	"air-traffic-control/backend/pkg/plugin/newapi"
	"air-traffic-control/backend/pkg/types"
)

// NodeType represents a classified host or device archetype in the datacenter inventory.
// It aliases newapi.InventoryNodeType so that plugin contracts and nodeapi callers
// share the exact underlying type without conversion boilerplate.
type NodeType = newapi.InventoryNodeType

const (
	NodeTypeLinux            NodeType = newapi.NodeTypeLinux
	NodeTypeWindows          NodeType = newapi.NodeTypeWindows
	NodeTypeRouterSwitch     NodeType = newapi.NodeTypeRouterSwitch
	NodeTypeProxmox          NodeType = newapi.NodeTypeProxmox
	NodeTypeContainerHost    NodeType = newapi.NodeTypeContainerHost
	NodeTypeStorageAppliance NodeType = newapi.NodeTypeStorageAppliance
	NodeTypeKubernetes       NodeType = newapi.NodeTypeKubernetes
)

// Grouping criteria constants supported by GroupNodes and GroupTypesNodes.
const (
	GroupByGroup      = "group"
	GroupByDatacenter = "datacenter"
	GroupByNodeType   = "node_type"
	GroupByStatus     = "status"
	GroupByTag        = "tag"
	GroupByOSFamily   = "os_family"
)

// NodeMetadata captures rich inventory metadata for a managed node, extending
// the legacy types.Node record with v2 plugin defaults, capability kinds, tags,
// and structured labels.
type NodeMetadata struct {
	ID               string            `json:"id"`
	Name             string            `json:"name"`
	Group            string            `json:"group"`
	Datacenter       string            `json:"datacenter"`
	NodeType         NodeType          `json:"nodeType"`
	OSFamily         string            `json:"osFamily"`
	OSDescription    string            `json:"osDescription,omitempty"`
	IP               string            `json:"ip"`
	Port             int               `json:"port"`
	TailscaleIP      string            `json:"tailscaleIp,omitempty"`
	TailscaleHost    string            `json:"tailscaleHost,omitempty"`
	Status           string            `json:"status"` // online, offline, degraded
	Uptime           string            `json:"uptime,omitempty"`
	AuthMethod       string            `json:"authMethod,omitempty"`
	ConnectionPlugin string            `json:"connectionPlugin,omitempty"`
	ElevationPlugin  string            `json:"elevationPlugin,omitempty"`
	HasGlances       bool              `json:"hasGlances,omitempty"`
	HasCockpit       bool              `json:"hasCockpit,omitempty"`
	HasPodman        bool              `json:"hasPodman,omitempty"`
	HasRestic        bool              `json:"hasRestic,omitempty"`
	Tags             []string          `json:"tags,omitempty"`
	Labels           map[string]string `json:"labels,omitempty"`
	Capabilities     []plugin.Kind     `json:"capabilities,omitempty"`
	ChassisInfo      types.Chassis     `json:"chassis"`
	LastSeen         time.Time         `json:"lastSeen"`
}

// NodeGroup represents a deterministic bucket of inventory nodes grouped by a
// shared dimension (group name, datacenter, node type, status, tag, or OS family).
type NodeGroup struct {
	Name          string         `json:"name"`
	GroupBy       string         `json:"groupBy"`
	NodeType      NodeType       `json:"nodeType,omitempty"`
	Datacenter    string         `json:"datacenter,omitempty"`
	Count         int            `json:"count"`
	OnlineCount   int            `json:"onlineCount"`
	OfflineCount  int            `json:"offlineCount"`
	DegradedCount int            `json:"degradedCount"`
	Nodes         []NodeMetadata `json:"nodes"`
}

// AllNodeTypes returns all built-in inventory NodeType values in display order.
func AllNodeTypes() []NodeType {
	specs := newapi.DefaultNodeTypes()
	out := make([]NodeType, 0, len(specs))
	for _, spec := range specs {
		out = append(out, spec.Type)
	}
	return out
}

// BuiltinNodeTypeSpecs returns the built-in NodeTypeSpec definitions sorted by Order.
func BuiltinNodeTypeSpecs() []newapi.NodeTypeSpec {
	specs := newapi.DefaultNodeTypes()
	sortNodeTypeSpecs(specs)
	return specs
}

// MergeNodeTypeSpecs merges built-in node type specs with custom plugin-provided
// specs, deduplicating by Type (later specs override earlier ones) and sorting by Order.
func MergeNodeTypeSpecs(extra ...[]newapi.NodeTypeSpec) []newapi.NodeTypeSpec {
	byType := make(map[NodeType]newapi.NodeTypeSpec)
	for _, spec := range newapi.DefaultNodeTypes() {
		byType[spec.Type] = spec
	}
	for _, list := range extra {
		for _, spec := range list {
			if err := newapi.ValidateNodeTypeSpec(spec); err != nil {
				continue
			}
			byType[spec.Type] = spec
		}
	}
	merged := make([]newapi.NodeTypeSpec, 0, len(byType))
	for _, spec := range byType {
		merged = append(merged, spec)
	}
	sortNodeTypeSpecs(merged)
	return merged
}

func sortNodeTypeSpecs(specs []newapi.NodeTypeSpec) {
	sort.SliceStable(specs, func(i, j int) bool {
		if specs[i].Order != specs[j].Order {
			return specs[i].Order < specs[j].Order
		}
		return string(specs[i].Type) < string(specs[j].Type)
	})
}

// LookupNodeTypeSpec returns the NodeTypeSpec for a given NodeType if known.
func LookupNodeTypeSpec(t NodeType, extra ...[]newapi.NodeTypeSpec) (newapi.NodeTypeSpec, bool) {
	normalized := NodeType(strings.ToLower(strings.TrimSpace(string(t))))
	for _, spec := range MergeNodeTypeSpecs(extra...) {
		if spec.Type == normalized {
			return spec, true
		}
	}
	return newapi.NodeTypeSpec{}, false
}

// ValidateNodeType checks whether t is a recognized built-in or plugin-provided NodeType.
func ValidateNodeType(t NodeType, extra ...[]newapi.NodeTypeSpec) error {
	trimmed := strings.TrimSpace(string(t))
	if trimmed == "" {
		return fmt.Errorf("node type cannot be empty")
	}
	if _, ok := LookupNodeTypeSpec(NodeType(trimmed), extra...); !ok {
		return fmt.Errorf("unknown node type %q", t)
	}
	return nil
}

// NormalizeNodeType parses and normalizes a raw node type string (including common
// aliases like "router", "switch", "pve", "k8s", "nas", "podman", "docker") into a
// canonical NodeType.
func NormalizeNodeType(raw string, extra ...[]newapi.NodeTypeSpec) (NodeType, error) {
	cleaned := strings.ToLower(strings.TrimSpace(raw))
	cleaned = strings.ReplaceAll(cleaned, "-", "_")
	cleaned = strings.ReplaceAll(cleaned, " ", "_")

	switch cleaned {
	case "", "linux", "linux_host", "server", "workstation":
		return NodeTypeLinux, nil
	case "windows", "windows_host", "win":
		return NodeTypeWindows, nil
	case "router_switch", "router", "switch", "network", "firewall_appliance":
		return NodeTypeRouterSwitch, nil
	case "proxmox", "pve", "proxmox_ve", "hypervisor":
		return NodeTypeProxmox, nil
	case "container_host", "container", "podman", "docker", "bootc":
		return NodeTypeContainerHost, nil
	case "storage_appliance", "storage", "nas", "san", "truenas", "zfs_appliance":
		return NodeTypeStorageAppliance, nil
	case "kubernetes", "k8s", "kube", "k3s", "rke2", "talos":
		return NodeTypeKubernetes, nil
	}

	candidate := NodeType(cleaned)
	if err := ValidateNodeType(candidate, extra...); err != nil {
		return "", err
	}
	return candidate, nil
}

// InferNodeType inspects a legacy types.Node record (explicit NodeType, OSDescription,
// group name, and capability flags) to determine the most specific canonical NodeType.
func InferNodeType(n types.Node) NodeType {
	rawType := strings.TrimSpace(string(n.NodeType))
	if rawType != "" && !strings.EqualFold(rawType, string(NodeTypeLinux)) {
		if normalized, err := NormalizeNodeType(rawType); err == nil {
			return normalized
		}
	}

	osDesc := strings.ToLower(n.OSDescription)
	group := strings.ToLower(n.Group)
	name := strings.ToLower(n.Name)
	combined := osDesc + " " + group + " " + name

	switch {
	case strings.Contains(combined, "proxmox") || strings.Contains(combined, "pve"):
		return NodeTypeProxmox
	case strings.Contains(combined, "kubernetes") || strings.Contains(combined, "k8s") || strings.Contains(combined, "k3s") || strings.Contains(combined, "talos"):
		return NodeTypeKubernetes
	case strings.Contains(combined, "truenas") || strings.Contains(combined, "synology") || strings.Contains(combined, "storage") || strings.Contains(combined, "nas"):
		return NodeTypeStorageAppliance
	case strings.Contains(combined, "router") || strings.Contains(combined, "switch") || strings.Contains(combined, "opnsense") || strings.Contains(combined, "pfsense") || strings.Contains(combined, "openwrt") || strings.Contains(combined, "mikrotik") || strings.Contains(combined, "vyos"):
		return NodeTypeRouterSwitch
	case strings.Contains(combined, "windows"):
		return NodeTypeWindows
	case strings.Contains(combined, "bootc") || strings.Contains(combined, "coreos") || strings.Contains(combined, "container"):
		return NodeTypeContainerHost
	}

	if rawType != "" {
		if normalized, err := NormalizeNodeType(rawType); err == nil {
			return normalized
		}
	}
	return NodeTypeLinux
}

// FromTypesNode converts a legacy types.Node into a rich NodeMetadata structure,
// populating default OSFamily, connection/elevation plugins, and expected capabilities
// from the resolved NodeTypeSpec.
func FromTypesNode(n types.Node, extra ...[]newapi.NodeTypeSpec) NodeMetadata {
	resolvedType := InferNodeType(n)
	spec, hasSpec := LookupNodeTypeSpec(resolvedType, extra...)

	osFamily := "linux"
	connPlugin := "connection/ssh"
	elevPlugin := "elevation/run0"
	var caps []plugin.Kind

	if hasSpec {
		if spec.DefaultOSFamily != "" {
			osFamily = spec.DefaultOSFamily
		}
		if spec.DefaultConnection != "" {
			connPlugin = spec.DefaultConnection
		}
		if spec.DefaultElevation != "" {
			elevPlugin = spec.DefaultElevation
		}
		if len(spec.Capabilities) > 0 {
			caps = append([]plugin.Kind(nil), spec.Capabilities...)
		}
	}

	if n.TailscaleIP != "" && n.IP == n.TailscaleIP {
		connPlugin = "connection/tailscale"
	}

	port := n.Port
	if port <= 0 {
		port = 22
	}

	status := strings.ToLower(strings.TrimSpace(n.Status))
	if status == "" {
		status = "offline"
	}

	group := strings.TrimSpace(n.Group)
	if group == "" {
		group = "Ungrouped"
	}

	datacenter := strings.TrimSpace(n.Datacenter)
	if datacenter == "" {
		datacenter = "default"
	}

	var tags []string
	if n.HasGlances {
		tags = append(tags, "glances")
	}
	if n.HasCockpit {
		tags = append(tags, "cockpit")
	}
	if n.HasPodman {
		tags = append(tags, "podman")
	}
	if n.HasRestic {
		tags = append(tags, "restic")
	}

	return NodeMetadata{
		ID:               n.ID,
		Name:             n.Name,
		Group:            group,
		Datacenter:       datacenter,
		NodeType:         resolvedType,
		OSFamily:         osFamily,
		OSDescription:    n.OSDescription,
		IP:               n.IP,
		Port:             port,
		TailscaleIP:      n.TailscaleIP,
		TailscaleHost:    n.TailscaleHost,
		Status:           status,
		Uptime:           n.Uptime,
		AuthMethod:       n.AuthMethod,
		ConnectionPlugin: connPlugin,
		ElevationPlugin:  elevPlugin,
		HasGlances:       n.HasGlances,
		HasCockpit:       n.HasCockpit,
		HasPodman:        n.HasPodman,
		HasRestic:        n.HasRestic,
		Tags:             tags,
		Labels: map[string]string{
			"node_type":  string(resolvedType),
			"os_family":  osFamily,
			"datacenter": datacenter,
			"group":      group,
		},
		Capabilities: caps,
		ChassisInfo:  n.ChassisInfo,
		LastSeen:     n.LastSeen,
	}
}

// ToTypesNode converts NodeMetadata back into a legacy types.Node structure for
// backwards compatibility with existing daemon and cache routes.
func (m NodeMetadata) ToTypesNode() types.Node {
	return types.Node{
		ID:            m.ID,
		Name:          m.Name,
		Group:         m.Group,
		Datacenter:    m.Datacenter,
		NodeType:      types.NodeType(m.NodeType),
		IP:            m.IP,
		Port:          m.Port,
		TailscaleIP:   m.TailscaleIP,
		TailscaleHost: m.TailscaleHost,
		OSDescription: m.OSDescription,
		Status:        m.Status,
		Uptime:        m.Uptime,
		HasGlances:    m.HasGlances,
		HasCockpit:    m.HasCockpit,
		HasPodman:     m.HasPodman,
		HasRestic:     m.HasRestic,
		LastSeen:      m.LastSeen,
		AuthMethod:    m.AuthMethod,
		ChassisInfo:   m.ChassisInfo,
	}
}

// FilterNodesByType returns all nodes matching the given NodeType.
func FilterNodesByType(nodes []NodeMetadata, t NodeType) []NodeMetadata {
	normalized := NodeType(strings.ToLower(strings.TrimSpace(string(t))))
	var out []NodeMetadata
	for _, n := range nodes {
		if n.NodeType == normalized {
			out = append(out, n)
		}
	}
	return out
}

// GroupNodes partitions a slice of NodeMetadata into deterministic, sorted NodeGroup
// buckets according to the requested grouping dimension ("group", "datacenter",
// "node_type", "status", "tag", or "os_family"). Defaults to "group" when by is empty.
func GroupNodes(nodes []NodeMetadata, by string) []NodeGroup {
	criterion := strings.ToLower(strings.TrimSpace(by))
	switch criterion {
	case "", "group", "groups":
		criterion = GroupByGroup
	case "datacenter", "dc":
		criterion = GroupByDatacenter
	case "node_type", "nodetype", "type":
		criterion = GroupByNodeType
	case "status", "health":
		criterion = GroupByStatus
	case "tag", "tags":
		criterion = GroupByTag
	case "os_family", "osfamily", "os":
		criterion = GroupByOSFamily
	default:
		criterion = GroupByGroup
	}

	buckets := make(map[string]*NodeGroup)
	var keys []string

	addNodeToBucket := func(key string, node NodeMetadata) {
		if key == "" {
			key = "Ungrouped"
		}
		grp, exists := buckets[key]
		if !exists {
			grp = &NodeGroup{
				Name:    key,
				GroupBy: criterion,
				Nodes:   []NodeMetadata{},
			}
			if criterion == GroupByNodeType {
				grp.NodeType = NodeType(key)
			}
			if criterion == GroupByDatacenter {
				grp.Datacenter = key
			}
			buckets[key] = grp
			keys = append(keys, key)
		}
		grp.Nodes = append(grp.Nodes, node)
		grp.Count++
		switch strings.ToLower(strings.TrimSpace(node.Status)) {
		case "online":
			grp.OnlineCount++
		case "degraded", "warning":
			grp.DegradedCount++
		default:
			grp.OfflineCount++
		}
	}

	for _, node := range nodes {
		switch criterion {
		case GroupByDatacenter:
			dc := strings.TrimSpace(node.Datacenter)
			if dc == "" {
				dc = "default"
			}
			addNodeToBucket(dc, node)
		case GroupByNodeType:
			nt := strings.TrimSpace(string(node.NodeType))
			if nt == "" {
				nt = string(NodeTypeLinux)
			}
			addNodeToBucket(nt, node)
		case GroupByStatus:
			st := strings.ToLower(strings.TrimSpace(node.Status))
			if st == "" {
				st = "offline"
			}
			addNodeToBucket(st, node)
		case GroupByOSFamily:
			osFam := strings.ToLower(strings.TrimSpace(node.OSFamily))
			if osFam == "" {
				osFam = "linux"
			}
			addNodeToBucket(osFam, node)
		case GroupByTag:
			if len(node.Tags) == 0 {
				addNodeToBucket("untagged", node)
			} else {
				for _, tag := range node.Tags {
					t := strings.TrimSpace(tag)
					if t != "" {
						addNodeToBucket(t, node)
					}
				}
			}
		default: // GroupByGroup
			g := strings.TrimSpace(node.Group)
			if g == "" {
				g = "Ungrouped"
			}
			addNodeToBucket(g, node)
		}
	}

	sort.Strings(keys)
	result := make([]NodeGroup, 0, len(keys))
	for _, k := range keys {
		grp := buckets[k]
		sort.SliceStable(grp.Nodes, func(i, j int) bool {
			if grp.Nodes[i].Name != grp.Nodes[j].Name {
				return grp.Nodes[i].Name < grp.Nodes[j].Name
			}
			return grp.Nodes[i].ID < grp.Nodes[j].ID
		})
		result = append(result, *grp)
	}
	return result
}

// GroupTypesNodes converts a slice of legacy types.Node values into NodeMetadata and
// groups them by the requested criterion.
func GroupTypesNodes(nodes []types.Node, by string, extra ...[]newapi.NodeTypeSpec) []NodeGroup {
	meta := make([]NodeMetadata, 0, len(nodes))
	for _, n := range nodes {
		meta = append(meta, FromTypesNode(n, extra...))
	}
	return GroupNodes(meta, by)
}

// NodeTypesResponse is the JSON envelope returned by GET /api/node-types.
type NodeTypesResponse struct {
	NodeTypes []newapi.NodeTypeSpec `json:"nodeTypes"`
	Count     int                   `json:"count"`
}

// NewHandler creates an HTTP handler for GET /api/node-types that serves the
// merged built-in and plugin-provided NodeTypeSpec list. Optional query parameter
// ?osFamily=<family> filters results by DefaultOSFamily.
func NewHandler(extraProvider func() []newapi.NodeTypeSpec) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodOptions {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		var extra []newapi.NodeTypeSpec
		if extraProvider != nil {
			extra = extraProvider()
		}
		specs := MergeNodeTypeSpecs(extra)

		if osFilter := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("osFamily"))); osFilter != "" {
			filtered := make([]newapi.NodeTypeSpec, 0, len(specs))
			for _, spec := range specs {
				if strings.ToLower(spec.DefaultOSFamily) == osFilter {
					filtered = append(filtered, spec)
				}
			}
			specs = filtered
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(NodeTypesResponse{
			NodeTypes: specs,
			Count:     len(specs),
		})
	}
}
