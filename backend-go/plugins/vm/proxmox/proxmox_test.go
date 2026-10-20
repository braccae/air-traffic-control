package proxmox_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"air-traffic-control/backend/pkg/plugin"
	_ "air-traffic-control/backend/plugins/all"
)

const testTokenSecret = "super-secret-token-value"

// fakePVE serves a minimal but real Proxmox JSON API so the plugin's HTTP
// client is exercised end-to-end. It records the last Authorization header.
type fakePVE struct {
	server  *httptest.Server
	mux     *http.ServeMux
	lastAuth string
	broken  bool
}

func newFakePVE(t *testing.T) *fakePVE {
	t.Helper()
	f := &fakePVE{mux: http.NewServeMux()}

	writeData := func(w http.ResponseWriter, data any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}

	f.mux.HandleFunc("/api2/json/version", func(w http.ResponseWriter, r *http.Request) {
		if f.broken {
			http.Error(w, `{"data":null,"errors":{"auth":"no ticket"}}`, http.StatusUnauthorized)
			return
		}
		f.lastAuth = r.Header.Get("Authorization")
		writeData(w, map[string]any{"version": "8.2.4", "release": "8.2"})
	})
	f.mux.HandleFunc("/api2/json/nodes", func(w http.ResponseWriter, r *http.Request) {
		writeData(w, []map[string]any{
			{"node": "pve", "status": "online", "type": "node", "maxcpu": 8, "maxmem": 16777216000, "mem": 4000000000},
		})
	})
	f.mux.HandleFunc("/api2/json/cluster/resources", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("type"); got != "vm" {
			t.Errorf("expected type=vm, got %q", got)
		}
		writeData(w, []map[string]any{
			{"id": "qemu/100", "type": "qemu", "node": "pve", "vmid": 100, "name": "web01", "status": "running", "maxmem": 2147483648, "maxdisk": 34359738368, "maxcpu": 2, "uptime": 1000},
			{"id": "qemu/101", "type": "qemu", "node": "pve", "vmid": 101, "name": "db01", "status": "stopped", "maxmem": 4294967296, "maxdisk": 68719476736, "maxcpu": 4, "uptime": 0},
			{"id": "lxc/200", "type": "lxc", "node": "pve", "vmid": 200, "name": "ct01", "status": "running", "maxmem": 536870912, "maxdisk": 8589934592, "maxcpu": 1, "uptime": 500},
		})
	})
	f.mux.HandleFunc("/api2/json/nodes/pve/qemu/100/spiceproxy", func(w http.ResponseWriter, r *http.Request) {
		writeData(w, map[string]any{"host": "pve.local", "port": 5900, "tls-port": 61000, "ticket": "SPICE-TICKET", "proxy": "pve.local"})
	})
	f.mux.HandleFunc("/api2/json/nodes/pve/qemu/100/vncproxy", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("vncproxy must be POST, got %s", r.Method)
		}
		writeData(w, map[string]any{"port": 5901, "ticket": "VNC-TICKET", "user": "root@pam"})
	})

	f.server = httptest.NewServer(f.mux)
	t.Cleanup(f.server.Close)
	return f
}

func factsFor(f *fakePVE) plugin.HostFacts {
	return plugin.HostFacts{
		NodeID:   "node-pve",
		OSFamily: "linux",
		HostRoute: f.server.URL,
		Overrides: map[string]string{
			"proxmox_api_host":         f.server.URL,
			"proxmox_api_user":         "root@pam",
			"proxmox_api_token_id":     "atc",
			"proxmox_api_token_secret": testTokenSecret,
		},
	}
}

func TestProxmox_Registered(t *testing.T) {
	p, ok := plugin.Get("vm/proxmox")
	if !ok {
		t.Fatal("vm/proxmox not registered")
	}
	if _, ok := p.(plugin.Reader); !ok {
		t.Error("vm/proxmox must implement Reader")
	}
	if _, ok := p.(plugin.Actor); !ok {
		t.Error("vm/proxmox must implement Actor")
	}
}

func TestProxmox_ProbeReachable(t *testing.T) {
	f := newFakePVE(t)
	p, _ := plugin.Get("vm/proxmox")

	det, err := p.Probe(context.Background(), nil, factsFor(f))
	if err != nil {
		t.Fatal(err)
	}
	if !det.Detected {
		t.Fatalf("expected detected, got %+v", det)
	}
	if !strings.Contains(det.Status, "8.2.4") {
		t.Errorf("status should carry version, got %q", det.Status)
	}
	if f.lastAuth != "PVEAPIToken=root@pam!atc="+testTokenSecret {
		t.Errorf("unexpected authorization header: %q", f.lastAuth)
	}
}

func TestProxmox_ProbeUnreachableSurfacesError(t *testing.T) {
	f := newFakePVE(t)
	f.broken = true
	p, _ := plugin.Get("vm/proxmox")

	det, err := p.Probe(context.Background(), nil, factsFor(f))
	if err != nil {
		t.Fatalf("probe should not return a hard error, got %v", err)
	}
	if det.Detected {
		t.Fatal("expected detection to fail on API error")
	}
	if det.Error == "" {
		t.Error("Zero-Mock: probe must surface the API error, not hide it")
	}
}

func TestProxmox_ReadGuestDetails(t *testing.T) {
	f := newFakePVE(t)
	p, _ := plugin.Get("vm/proxmox")
	reader := p.(plugin.Reader)

	data, err := reader.Read(context.Background(), nil, factsFor(f))
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	// Re-encode then decode to reach the unexported structs generically.
	raw, _ := json.Marshal(data)
	var decoded struct {
		Endpoint   string `json:"endpoint"`
		Version    string `json:"version"`
		VMs        []struct {
			VMID    int    `json:"vmid"`
			Name    string `json:"name"`
			Type    string `json:"type"`
			Status  string `json:"status"`
			Console map[string]any `json:"console"`
		} `json:"vms"`
		Containers []struct {
			VMID    int            `json:"vmid"`
			Status  string         `json:"status"`
			Console map[string]any `json:"console"`
		} `json:"containers"`
		Summary map[string]any `json:"summary"`
		Errors  []string       `json:"errors"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}

	if len(decoded.VMs) != 2 {
		t.Fatalf("expected 2 VMs, got %d", len(decoded.VMs))
	}
	if len(decoded.Containers) != 1 {
		t.Fatalf("expected 1 container, got %d", len(decoded.Containers))
	}
	if decoded.VMs[0].Status != "running" || decoded.VMs[1].Status != "stopped" {
		t.Errorf("unexpected VM statuses: %+v", decoded.VMs)
	}
	if !strings.Contains(decoded.Endpoint, f.server.URL) {
		t.Errorf("endpoint missing host: %q", decoded.Endpoint)
	}

	// Running VM must expose a noVNC URL and live SPICE/VNC tickets.
	running := decoded.VMs[0].Console
	if !strings.Contains(running["noVNCUrl"].(string), "console=kvm") {
		t.Errorf("missing noVNC url: %+v", running)
	}
	if running["spiceVV"] == nil || !strings.Contains(running["spiceVV"].(string), "SPICE-TICKET") {
		t.Errorf("missing spice vv file: %+v", running)
	}
	if running["vncTicket"] != "VNC-TICKET" {
		t.Errorf("missing vnc ticket: %+v", running)
	}
	// Stopped VM must not attempt live tickets.
	stopped := decoded.VMs[1].Console
	if _, hasSpice := stopped["spiceVV"]; hasSpice {
		t.Errorf("stopped VM should not have live spice data: %+v", stopped)
	}
	// Container uses xterm.js term proxy.
	if !strings.Contains(decoded.Containers[0].Console["termUrl"].(string), "xtermjs=1") {
		t.Errorf("missing term url: %+v", decoded.Containers[0].Console)
	}

	// Secret Protection: the token secret must never appear in returned data.
	if strings.Contains(string(raw), testTokenSecret) {
		t.Error("read output leaked the API token secret")
	}
}

func TestProxmox_ReadSurfacesAPIError(t *testing.T) {
	f := newFakePVE(t)
	f.broken = true
	p, _ := plugin.Get("vm/proxmox")
	reader := p.(plugin.Reader)

	if _, err := reader.Read(context.Background(), nil, factsFor(f)); err == nil {
		t.Fatal("Zero-Mock: unreachable API must produce an error, not fabricated data")
	}
}

func compile(t *testing.T, action string, opts map[string]any) plugin.TaskSpec {
	t.Helper()
	p, _ := plugin.Get("vm/proxmox")
	actor := p.(plugin.Actor)
	task, err := actor.Compile(plugin.HostFacts{
		NodeID:    "node-pve",
		HostRoute: "192.168.1.10",
		Overrides: map[string]string{"proxmox_api_port": "8006"},
	}, action, opts)
	if err != nil {
		t.Fatalf("compile %s failed: %v", action, err)
	}
	return task
}

func TestProxmox_CompileLifecycle(t *testing.T) {
	task := compile(t, "vm_shutdown", map[string]any{"node": "pve", "vmid": 100})
	if len(task.Steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(task.Steps))
	}
	step := task.Steps[0]
	if step.Module != "ansible.builtin.uri" {
		t.Errorf("unexpected module %q", step.Module)
	}
	url, _ := step.Params["url"].(string)
	if !strings.Contains(url, "/nodes/pve/qemu/100/status/shutdown") {
		t.Errorf("unexpected url %q", url)
	}
	if step.Params["method"] != "POST" {
		t.Errorf("expected POST, got %v", step.Params["method"])
	}
	if task.SecretsMasked["proxmox_api_token_secret"] == "" {
		t.Error("token secret must be registered for masking")
	}
}

func TestProxmox_CompileStopUsesStopEndpoint(t *testing.T) {
	task := compile(t, "ct_stop", map[string]any{"node": "pve", "vmid": 200})
	url, _ := task.Steps[0].Params["url"].(string)
	if !strings.Contains(url, "/nodes/pve/lxc/200/status/stop") {
		t.Errorf("unexpected url %q", url)
	}
}

func TestProxmox_CompileCreateVM(t *testing.T) {
	task := compile(t, "vm_create", map[string]any{
		"node": "pve", "vmid": 300, "name": "new-vm", "memory": 4096, "cores": 4,
	})
	step := task.Steps[0]
	if step.Module != "community.proxmox.proxmox_kvm" {
		t.Fatalf("unexpected module %q", step.Module)
	}
	if step.Params["vmid"] != 300 {
		t.Errorf("vmid not passed: %+v", step.Params["vmid"])
	}
	if step.Params["memory"] != 4096 {
		t.Errorf("memory not passed: %+v", step.Params["memory"])
	}
}

func TestProxmox_CompileCreateCT(t *testing.T) {
	task := compile(t, "ct_create", map[string]any{
		"node": "pve", "vmid": 301, "hostname": "new-ct", "ostemplate": "local:vztmpl/debian-12.tar.zst",
	})
	step := task.Steps[0]
	if step.Module != "community.proxmox.proxmox" {
		t.Fatalf("unexpected module %q", step.Module)
	}
	if step.Params["ostemplate"] != "local:vztmpl/debian-12.tar.zst" {
		t.Errorf("ostemplate not passed: %+v", step.Params["ostemplate"])
	}
}

func TestProxmox_CompileUpdateConfigAndResizeAndDelete(t *testing.T) {
	upd := compile(t, "update_config", map[string]any{
		"node": "pve", "guestType": "qemu", "vmid": 100, "key": "memory", "value": "8192",
	})
	url, _ := upd.Steps[0].Params["url"].(string)
	if !strings.Contains(url, "/nodes/pve/qemu/100/config") || upd.Steps[0].Params["method"] != "PUT" {
		t.Errorf("unexpected update task: %+v", upd.Steps[0])
	}

	rz := compile(t, "resize_disk", map[string]any{
		"node": "pve", "guestType": "qemu", "vmid": 100, "disk": "scsi0", "size": "+10G",
	})
	url, _ = rz.Steps[0].Params["url"].(string)
	if !strings.Contains(url, "/nodes/pve/qemu/100/resize") {
		t.Errorf("unexpected resize url %q", url)
	}

	del := compile(t, "delete_guest", map[string]any{"node": "pve", "guestType": "lxc", "vmid": 200})
	if del.Steps[0].Params["method"] != "DELETE" {
		t.Errorf("expected DELETE, got %v", del.Steps[0].Params["method"])
	}
}

func TestProxmox_CompileRejectsMissingArgs(t *testing.T) {
	p, _ := plugin.Get("vm/proxmox")
	actor := p.(plugin.Actor)
	if _, err := actor.Compile(plugin.HostFacts{}, "vm_start", map[string]any{}); err == nil {
		t.Error("expected error when node/vmid missing")
	}
	if _, err := actor.Compile(plugin.HostFacts{}, "bogus_action", map[string]any{}); err == nil {
		t.Error("expected error for unsupported action")
	}
}
