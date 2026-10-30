package proxmox_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"air-traffic-control/backend/pkg/plugin"
)

// pveClusterFixture is a real HTTP Proxmox VE API surface for a three member
// cluster (pong, cold-war, noodle) whose guest list spans all members. The
// plugin must scope its read to the member the inventory host represents.
type pveClusterFixture struct {
	server     *httptest.Server
	localFlag  bool
	localState string
}

func newPVEClusterFixture(t *testing.T) *pveClusterFixture {
	t.Helper()
	f := &pveClusterFixture{localFlag: true}
	mux := http.NewServeMux()

	writeData := func(w http.ResponseWriter, data any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}

	mux.HandleFunc("/api2/json/version", func(w http.ResponseWriter, r *http.Request) {
		writeData(w, map[string]any{"version": "8.2.4", "release": "8.2"})
	})
	mux.HandleFunc("/api2/json/cluster/status", func(w http.ResponseWriter, r *http.Request) {
		entries := []map[string]any{
			{"type": "cluster", "name": "leo-banjo", "nodes": 3, "quorate": 1},
			{"type": "node", "name": "pong", "nodeid": 1, "ip": "10.0.0.1", "online": 1},
			{"type": "node", "name": "cold-war", "nodeid": 2, "ip": "10.0.0.2", "online": 1},
			{"type": "node", "name": "noodle", "nodeid": 3, "ip": "10.0.0.3", "online": 1},
		}
		if f.localFlag {
			entries[1]["local"] = 1
		}
		writeData(w, entries)
	})
	mux.HandleFunc("/api2/json/nodes", func(w http.ResponseWriter, r *http.Request) {
		writeData(w, []map[string]any{
			{"node": "pong", "status": "online", "type": "node", "maxcpu": 32},
			{"node": "cold-war", "status": "online", "type": "node", "maxcpu": 16},
			{"node": "noodle", "status": "online", "type": "node", "maxcpu": 16},
		})
	})
	mux.HandleFunc("/api2/json/cluster/resources", func(w http.ResponseWriter, r *http.Request) {
		writeData(w, []map[string]any{
			{"id": "qemu/101", "type": "qemu", "node": "pong", "vmid": 101, "name": "pong-web", "status": "running", "maxmem": 2147483648, "maxdisk": 34359738368, "maxcpu": 4, "uptime": 1000},
			{"id": "lxc/201", "type": "lxc", "node": "pong", "vmid": 201, "name": "pong-ct", "status": "running", "maxmem": 536870912, "maxdisk": 8589934592, "maxcpu": 1, "uptime": 500},
			{"id": "qemu/102", "type": "qemu", "node": "cold-war", "vmid": 102, "name": "cw-db", "status": "running", "maxmem": 4294967296, "maxdisk": 68719476736, "maxcpu": 8, "uptime": 2000},
			{"id": "qemu/103", "type": "qemu", "node": "noodle", "vmid": 103, "name": "noodle-ci", "status": "stopped", "maxmem": 2147483648, "maxdisk": 17179869184, "maxcpu": 2, "uptime": 0},
		})
	})
	// Only pong's guest has live console endpoints: a probe for another node's
	// guest would 404 and show up in the read errors.
	mux.HandleFunc("/api2/json/nodes/pong/qemu/101/spiceproxy", func(w http.ResponseWriter, r *http.Request) {
		writeData(w, map[string]any{"host": "10.0.0.1", "port": 5900, "ticket": "SPICE-PONG"})
	})
	mux.HandleFunc("/api2/json/nodes/pong/qemu/101/vncproxy", func(w http.ResponseWriter, r *http.Request) {
		writeData(w, map[string]any{"port": 5901, "ticket": "VNC-PONG"})
	})

	f.server = httptest.NewServer(mux)
	t.Cleanup(f.server.Close)
	return f
}

func clusterFacts(url string, extra map[string]string) plugin.HostFacts {
	ov := map[string]string{
		"proxmox_api_host":         url,
		"proxmox_api_user":         "root@pam",
		"proxmox_api_token_id":     "atc",
		"proxmox_api_token_secret": testTokenSecret,
	}
	for k, v := range extra {
		ov[k] = v
	}
	return plugin.HostFacts{NodeID: "node-pve-pong", OSFamily: "linux", HostRoute: url, Overrides: ov}
}

type proxmoxReadResult struct {
	Node     string `json:"node"`
	Endpoint string `json:"endpoint"`
	VMs      []struct {
		VMID    int            `json:"vmid"`
		Name    string         `json:"name"`
		Node    string         `json:"node"`
		Console map[string]any `json:"console"`
	} `json:"vms"`
	Containers []struct {
		VMID int    `json:"vmid"`
		Node string `json:"node"`
	} `json:"containers"`
	Summary map[string]any `json:"summary"`
	Errors  []string       `json:"errors"`
}

func readCluster(t *testing.T, data any) proxmoxReadResult {
	t.Helper()
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	var out proxmoxReadResult
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestProxmox_ReadScopesGuestsToTheInspectedNode(t *testing.T) {
	f := newPVEClusterFixture(t)
	p, _ := plugin.Get("vm/proxmox")
	reader := p.(plugin.Reader)

	data, err := reader.Read(context.Background(), nil, clusterFacts(f.server.URL, nil))
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	got := readCluster(t, data)

	if got.Node != "pong" {
		t.Errorf("expected the read to resolve node pong, got %q", got.Node)
	}
	if len(got.VMs) != 1 || got.VMs[0].VMID != 101 || got.VMs[0].Node != "pong" {
		t.Errorf("expected only pong's VM 101, got %+v", got.VMs)
	}
	if len(got.Containers) != 1 || got.Containers[0].VMID != 201 {
		t.Errorf("expected only pong's container 201, got %+v", got.Containers)
	}
	for _, vm := range got.VMs {
		if vm.VMID == 102 || vm.VMID == 103 {
			t.Errorf("guest %d from another cluster member leaked into the read", vm.VMID)
		}
	}
	if got.Summary["excluded"] != float64(2) {
		t.Errorf("expected 2 excluded guests, got %v", got.Summary["excluded"])
	}
	if len(got.Errors) != 0 {
		t.Errorf("console probes must not target other nodes' guests, got errors: %v", got.Errors)
	}
	if got.VMs[0].Console["vncTicket"] != "VNC-PONG" {
		t.Errorf("expected pong's live console data, got %+v", got.VMs[0].Console)
	}
	if strings.Contains(string(mustJSON(t, data)), testTokenSecret) {
		t.Error("read output leaked the API token secret")
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestProxmox_ReadHonoursNodeNameOverride(t *testing.T) {
	f := newPVEClusterFixture(t)
	p, _ := plugin.Get("vm/proxmox")
	reader := p.(plugin.Reader)

	data, err := reader.Read(context.Background(), nil, clusterFacts(f.server.URL, map[string]string{
		"proxmox_node_name": "cold-war",
	}))
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	got := readCluster(t, data)

	if got.Node != "cold-war" {
		t.Errorf("expected the override node cold-war, got %q", got.Node)
	}
	if len(got.VMs) != 1 || got.VMs[0].VMID != 102 || got.VMs[0].Node != "cold-war" {
		t.Errorf("expected only cold-war's VM 102, got %+v", got.VMs)
	}
	if len(got.Containers) != 0 {
		t.Errorf("expected no containers on cold-war, got %+v", got.Containers)
	}
}

func TestProxmox_ReadRejectsUnknownNodeOverride(t *testing.T) {
	f := newPVEClusterFixture(t)
	p, _ := plugin.Get("vm/proxmox")
	reader := p.(plugin.Reader)

	data, err := reader.Read(context.Background(), nil, clusterFacts(f.server.URL, map[string]string{
		"proxmox_node_name": "does-not-exist",
	}))
	if err == nil {
		t.Fatalf("expected an error for an unknown node name, got data: %+v", data)
	}
	if !strings.Contains(err.Error(), "not a member of this cluster") {
		t.Errorf("error should explain the membership failure, got %v", err)
	}
	for _, member := range []string{"pong", "cold-war", "noodle"} {
		if !strings.Contains(err.Error(), member) {
			t.Errorf("error should list cluster member %q, got %v", member, err)
		}
	}
}

func TestProxmox_ReadFailsWhenNodeIdentityIsUnknown(t *testing.T) {
	f := newPVEClusterFixture(t)
	f.localFlag = false // no member claims to serve this request
	p, _ := plugin.Get("vm/proxmox")
	reader := p.(plugin.Reader)

	data, err := reader.Read(context.Background(), nil, clusterFacts(f.server.URL, nil))
	if err == nil {
		t.Fatalf("expected a hard error instead of the cluster-wide listing, got: %+v", data)
	}
	if !strings.Contains(err.Error(), "cannot determine which Proxmox node") {
		t.Errorf("error should explain the unknown node identity, got %v", err)
	}
}

// fakeRunner serves canned pvesh / hostname output for the SSH fallback path.
type fakeRunner struct {
	responses map[string]string
	calls     []string
}

func (f *fakeRunner) RunCommand(_ context.Context, cmd string) (string, error) {
	f.calls = append(f.calls, cmd)
	for needle, out := range f.responses {
		if strings.Contains(cmd, needle) {
			return out, nil
		}
	}
	return "", fmt.Errorf("fakeRunner: no response for %q", cmd)
}

const clusterResourcesJSON = `[
 {"id":"qemu/101","type":"qemu","node":"pong","vmid":101,"name":"pong-web","status":"running"},
 {"id":"qemu/102","type":"qemu","node":"cold-war","vmid":102,"name":"cw-db","status":"running"},
 {"id":"qemu/103","type":"qemu","node":"noodle","vmid":103,"name":"noodle-ci","status":"running"},
 {"id":"lxc/201","type":"lxc","node":"pong","vmid":201,"name":"pong-ct","status":"running"},
 {"id":"lxc/203","type":"lxc","node":"noodle","vmid":203,"name":"noodle-ct","status":"running"}
]`

const clusterNodesJSON = `[
 {"node":"pong","status":"online","type":"node","maxcpu":32},
 {"node":"cold-war","status":"online","type":"node","maxcpu":16},
 {"node":"noodle","status":"online","type":"node","maxcpu":16}
]`

func TestProxmox_SSHFallbackScopesGuestsToHostIdentity(t *testing.T) {
	p, _ := plugin.Get("vm/proxmox")
	reader := p.(plugin.Reader)

	runner := &fakeRunner{responses: map[string]string{
		"pvesh get /version":           `{"version":"8.2.4","release":"8.2"}`,
		"pvesh get /nodes":             clusterNodesJSON,
		"hostname -s":                  "noodle\n",
		"pvesh get /cluster/resources": clusterResourcesJSON,
	}}

	// No API token/password: the read must fall back to SSH/pvesh.
	facts := plugin.HostFacts{NodeID: "node-pve-noodle", HostRoute: "noodle.leo-banjo.ts.net"}
	data, err := reader.Read(context.Background(), runner, facts)
	if err != nil {
		t.Fatalf("ssh fallback read failed: %v", err)
	}
	got := readCluster(t, data)

	if got.Node != "noodle" {
		t.Errorf("expected node noodle, got %q", got.Node)
	}
	if len(got.VMs) != 1 || got.VMs[0].VMID != 103 {
		t.Errorf("expected only noodle's VM 103, got %+v", got.VMs)
	}
	if len(got.Containers) != 1 || got.Containers[0].VMID != 203 {
		t.Errorf("expected only noodle's CT 203, got %+v", got.Containers)
	}
	if got.Summary["excluded"] != float64(3) {
		t.Errorf("expected 3 excluded guests, got %v", got.Summary["excluded"])
	}
}

func TestProxmox_SSHFallbackFailsOnForeignHostname(t *testing.T) {
	p, _ := plugin.Get("vm/proxmox")
	reader := p.(plugin.Reader)

	runner := &fakeRunner{responses: map[string]string{
		"pvesh get /version": `{"version":"8.2.4","release":"8.2"}`,
		"pvesh get /nodes":   clusterNodesJSON,
		// Not a member of the cluster: must not be used to scope the listing.
		"hostname -s":               "banana\n",
		"pvesh get /cluster/status": `[{"type":"cluster","name":"leo-banjo"},{"type":"node","name":"pong","ip":"10.0.0.1"}]`,
	}}

	facts := plugin.HostFacts{NodeID: "node-pve-unknown", HostRoute: "banana.leo-banjo.ts.net"}
	data, err := reader.Read(context.Background(), runner, facts)
	if err == nil {
		t.Fatalf("expected a hard error, got: %+v", data)
	}
	if !strings.Contains(err.Error(), "ssh identity probe failed") {
		t.Errorf("error should explain the failed identity probe, got %v", err)
	}
}

func TestProxmox_SSHFallbackFailsWhenResourcesUnreadable(t *testing.T) {
	// Every guest row lacks a node field: nothing can be attributed to this
	// node, so the read must return an empty list rather than a cluster dump.
	p, _ := plugin.Get("vm/proxmox")
	reader := p.(plugin.Reader)

	runner := &fakeRunner{responses: map[string]string{
		"pvesh get /version":           `{"version":"8.2.4"}`,
		"pvesh get /nodes":             `[{"node":"pong","status":"online","type":"node"}]`,
		"hostname -s":                  "pong",
		"pvesh get /cluster/resources": `[{"id":"qemu/900","type":"qemu","vmid":900,"name":"anonymous","status":"running"}]`,
	}}

	data, err := reader.Read(context.Background(), runner, plugin.HostFacts{NodeID: "n", HostRoute: "pong.ts.net"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := readCluster(t, data)
	if len(got.VMs) != 0 {
		t.Errorf("guests without a node must not be attributed to pong, got %+v", got.VMs)
	}
}
