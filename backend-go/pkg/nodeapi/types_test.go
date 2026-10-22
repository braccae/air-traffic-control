package nodeapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"air-traffic-control/backend/pkg/plugin"
	"air-traffic-control/backend/pkg/plugin/newapi"
	"air-traffic-control/backend/pkg/types"
)

func TestAllNodeTypesAndValidation(t *testing.T) {
	all := AllNodeTypes()
	if len(all) != 7 {
		t.Fatalf("expected 7 built-in node types, got %d: %v", len(all), all)
	}

	expected := []NodeType{
		NodeTypeLinux,
		NodeTypeProxmox,
		NodeTypeContainerHost,
		NodeTypeStorageAppliance,
		NodeTypeKubernetes,
		NodeTypeRouterSwitch,
		NodeTypeWindows,
	}
	for i, want := range expected {
		if all[i] != want {
			t.Errorf("AllNodeTypes[%d] = %q, want %q", i, all[i], want)
		}
		if err := ValidateNodeType(want); err != nil {
			t.Errorf("ValidateNodeType(%q) returned unexpected error: %v", want, err)
		}
		spec, ok := LookupNodeTypeSpec(want)
		if !ok {
			t.Errorf("LookupNodeTypeSpec(%q) not found", want)
		}
		if spec.Type != want || spec.Label == "" || spec.DefaultOSFamily == "" {
			t.Errorf("invalid spec for %q: %+v", want, spec)
		}
	}

	if err := ValidateNodeType(""); err == nil {
		t.Errorf("expected error for empty node type")
	}
	if err := ValidateNodeType("mainframe_z16"); err == nil {
		t.Errorf("expected error for unknown node type")
	}
}

func TestNormalizeAndInferNodeType(t *testing.T) {
	cases := []struct {
		raw  string
		want NodeType
	}{
		{"", NodeTypeLinux},
		{"linux", NodeTypeLinux},
		{"Linux Host", NodeTypeLinux},
		{"pve", NodeTypeProxmox},
		{"proxmox-ve", NodeTypeProxmox},
		{"podman", NodeTypeContainerHost},
		{"bootc", NodeTypeContainerHost},
		{"truenas", NodeTypeStorageAppliance},
		{"nas", NodeTypeStorageAppliance},
		{"k8s", NodeTypeKubernetes},
		{"k3s", NodeTypeKubernetes},
		{"router", NodeTypeRouterSwitch},
		{"switch", NodeTypeRouterSwitch},
		{"windows", NodeTypeWindows},
	}

	for _, tc := range cases {
		got, err := NormalizeNodeType(tc.raw)
		if err != nil {
			t.Fatalf("NormalizeNodeType(%q) unexpected error: %v", tc.raw, err)
		}
		if got != tc.want {
			t.Errorf("NormalizeNodeType(%q) = %q, want %q", tc.raw, got, tc.want)
		}
	}

	if _, err := NormalizeNodeType("unknown_alien_device"); err == nil {
		t.Errorf("expected error for unknown raw type")
	}

	inferCases := []struct {
		name string
		node types.Node
		want NodeType
	}{
		{
			name: "explicit router_switch",
			node: types.Node{Name: "edge-01", NodeType: types.NodeTypeRouterSwitch},
			want: NodeTypeRouterSwitch,
		},
		{
			name: "inferred proxmox from OSDescription",
			node: types.Node{Name: "hv-01", NodeType: types.NodeTypeLinuxLinux, OSDescription: "Proxmox VE 8.2"},
			want: NodeTypeProxmox,
		},
		{
			name: "inferred storage appliance from group",
			node: types.Node{Name: "vault-01", Group: "Storage / TrueNAS", OSDescription: "Debian 12"},
			want: NodeTypeStorageAppliance,
		},
		{
			name: "inferred container host from bootc OS",
			node: types.Node{Name: "pod-01", OSDescription: "Fedora CoreOS bootc 41"},
			want: NodeTypeContainerHost,
		},
		{
			name: "inferred kubernetes from name",
			node: types.Node{Name: "k3s-worker-01", OSDescription: "Ubuntu 24.04"},
			want: NodeTypeKubernetes,
		},
		{
			name: "default linux",
			node: types.Node{Name: "web-01", OSDescription: "Rocky Linux 9.4"},
			want: NodeTypeLinux,
		},
	}

	for _, tc := range inferCases {
		got := InferNodeType(tc.node)
		if got != tc.want {
			t.Errorf("%s: InferNodeType() = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestFromAndToTypesNodeRoundTrip(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	orig := types.Node{
		ID:            "node-1",
		Name:          "pve-alpha",
		Group:         "Hypervisors",
		Datacenter:    "us-east-1",
		NodeType:      types.NodeType("proxmox"),
		IP:            "100.64.0.10",
		Port:          2222,
		TailscaleIP:   "100.64.0.10",
		TailscaleHost: "pve-alpha.tailnet",
		OSDescription: "Proxmox VE 8.2",
		Status:        "online",
		Uptime:        "42d 5h",
		HasGlances:    true,
		HasPodman:     true,
		LastSeen:      now,
		AuthMethod:    "ssh_key",
		ChassisInfo:   types.Chassis{Motherboard: "Supermicro X12"},
	}

	meta := FromTypesNode(orig)
	if meta.NodeType != NodeTypeProxmox {
		t.Fatalf("expected NodeTypeProxmox, got %q", meta.NodeType)
	}
	if meta.ConnectionPlugin != "connection/tailscale" {
		t.Errorf("expected tailscale connection plugin when IP == TailscaleIP, got %q", meta.ConnectionPlugin)
	}
	if meta.ElevationPlugin != "elevation/sudo" {
		t.Errorf("expected elevation/sudo for proxmox, got %q", meta.ElevationPlugin)
	}
	if len(meta.Capabilities) == 0 {
		t.Errorf("expected non-empty capabilities from Proxmox spec")
	}
	if len(meta.Tags) != 2 {
		t.Errorf("expected 2 capability tags (glances, podman), got %v", meta.Tags)
	}

	roundTrip := meta.ToTypesNode()
	if roundTrip.ID != orig.ID || roundTrip.Name != orig.Name || roundTrip.Port != orig.Port || roundTrip.ChassisInfo.Motherboard != orig.ChassisInfo.Motherboard {
		t.Errorf("round-trip mismatch: got %+v, want %+v", roundTrip, orig)
	}
}

func TestGroupNodesAndFilter(t *testing.T) {
	nodes := []types.Node{
		{ID: "n1", Name: "alpha", Group: "Core", Datacenter: "dc1", Status: "online", OSDescription: "Proxmox VE 8", HasPodman: true},
		{ID: "n2", Name: "beta", Group: "Core", Datacenter: "dc1", Status: "degraded", OSDescription: "Rocky Linux 9", HasGlances: true},
		{ID: "n3", Name: "gamma", Group: "Edge", Datacenter: "dc2", NodeType: types.NodeTypeRouterSwitch, Status: "offline", OSDescription: "OPNsense"},
	}

	byGroup := GroupTypesNodes(nodes, GroupByGroup)
	if len(byGroup) != 2 {
		t.Fatalf("expected 2 groups by group, got %d", len(byGroup))
	}
	if byGroup[0].Name != "Core" || byGroup[0].Count != 2 || byGroup[0].OnlineCount != 1 || byGroup[0].DegradedCount != 1 {
		t.Errorf("unexpected Core group stats: %+v", byGroup[0])
	}
	if byGroup[1].Name != "Edge" || byGroup[1].OfflineCount != 1 {
		t.Errorf("unexpected Edge group stats: %+v", byGroup[1])
	}

	byType := GroupTypesNodes(nodes, GroupByNodeType)
	if len(byType) != 3 {
		t.Fatalf("expected 3 groups by node_type, got %d", len(byType))
	}

	byDC := GroupTypesNodes(nodes, GroupByDatacenter)
	if len(byDC) != 2 || byDC[0].Datacenter != "dc1" || byDC[0].Count != 2 {
		t.Errorf("unexpected datacenter grouping: %+v", byDC)
	}

	byTag := GroupTypesNodes(nodes, GroupByTag)
	if len(byTag) != 3 { // glances, podman, untagged
		t.Errorf("expected 3 tag buckets (glances, podman, untagged), got %d: %+v", len(byTag), byTag)
	}

	var meta []NodeMetadata
	for _, n := range nodes {
		meta = append(meta, FromTypesNode(n))
	}
	proxmoxOnly := FilterNodesByType(meta, NodeTypeProxmox)
	if len(proxmoxOnly) != 1 || proxmoxOnly[0].ID != "n1" {
		t.Errorf("expected 1 proxmox node (n1), got %+v", proxmoxOnly)
	}
}

func TestHandlerServesMergedAndFilteredSpecs(t *testing.T) {
	customSpec := newapi.NodeTypeSpec{
		Type:            NodeType("bsd_appliance"),
		Label:           "BSD Firewall Appliance",
		Icon:            "security",
		DefaultOSFamily: "bsd",
		Capabilities:    []plugin.Kind{plugin.KindFirewall, plugin.KindNetwork},
		Order:           65,
	}

	handler := NewHandler(func() []newapi.NodeTypeSpec {
		return []newapi.NodeTypeSpec{customSpec}
	})

	req := httptest.NewRequest(http.MethodGet, "/api/node-types", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", rec.Code)
	}

	var resp NodeTypesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp.Count != 8 || len(resp.NodeTypes) != 8 {
		t.Fatalf("expected 8 merged node types (7 builtin + 1 custom), got %d", resp.Count)
	}

	// Filter by osFamily=bsd
	reqBSD := httptest.NewRequest(http.MethodGet, "/api/node-types?osFamily=bsd", nil)
	recBSD := httptest.NewRecorder()
	handler.ServeHTTP(recBSD, reqBSD)

	var respBSD NodeTypesResponse
	if err := json.Unmarshal(recBSD.Body.Bytes(), &respBSD); err != nil {
		t.Fatalf("failed to decode filtered response: %v", err)
	}
	if respBSD.Count != 1 || respBSD.NodeTypes[0].Type != "bsd_appliance" {
		t.Fatalf("expected 1 bsd node type, got %+v", respBSD)
	}
}
