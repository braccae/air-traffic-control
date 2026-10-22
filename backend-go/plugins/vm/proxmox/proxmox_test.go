package proxmox_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"air-traffic-control/backend/pkg/plugin"
	"air-traffic-control/backend/pkg/plugin/newapi"
	"air-traffic-control/backend/pkg/plugin/settingsframework"
	_ "air-traffic-control/backend/plugins/all"
	"air-traffic-control/backend/plugins/vm/proxmox"
)

const testTokenSecret = "super-secret-token-value"

// fakePVE serves a minimal but real Proxmox JSON API so the plugin's HTTP
// client is exercised end-to-end. It records the last Authorization header.
type fakePVE struct {
	server   *httptest.Server
	mux      *http.ServeMux
	lastAuth string
	broken   bool
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
		NodeID:    "node-pve",
		OSFamily:  "linux",
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
	if _, ok := p.(plugin.SettingsPageProvider); !ok {
		t.Error("vm/proxmox must implement SettingsPageProvider")
	}
	if !p.Compatible(plugin.HostFacts{OSFamily: "linux"}) {
		t.Error("expected compatible with linux")
	}
	if p.Compatible(plugin.HostFacts{OSFamily: "windows"}) {
		t.Error("expected incompatible with windows")
	}
	if p.Compatible(plugin.HostFacts{OSFamily: "router_switch"}) {
		t.Error("expected incompatible with router_switch")
	}
}

func TestProxmox_NewAPIInterfaces(t *testing.T) {
	p, ok := plugin.Get("vm/proxmox")
	if !ok {
		t.Fatal("vm/proxmox not registered")
	}

	dc, ok := p.(newapi.DataCollectionPlugin)
	if !ok {
		t.Fatal("expected vm/proxmox to implement newapi.DataCollectionPlugin")
	}
	sched := dc.CollectionSchedule()
	if sched.TTL != 60*time.Second || sched.Interval != 60*time.Second {
		t.Errorf("expected 60s schedule for vm/proxmox, got %+v", sched)
	}
	schema := dc.CollectionSchema()
	if schema.Version != "v2" || schema.RootKey != "vms" {
		t.Errorf("unexpected collection schema: %+v", schema)
	}

	mgmt, ok := p.(newapi.ManagementPlugin)
	if !ok {
		t.Fatal("expected vm/proxmox to implement newapi.ManagementPlugin")
	}
	mgmtActions := mgmt.ManagementActions()
	if len(mgmtActions) != 14 {
		t.Fatalf("expected 14 management actions, got %d", len(mgmtActions))
	}
	if len(p.(plugin.Actor).Actions()) != len(mgmtActions) {
		t.Errorf("expected Actions() length %d to match ManagementActions() length %d", len(p.(plugin.Actor).Actions()), len(mgmtActions))
	}

	facts := plugin.HostFacts{NodeID: "node-pve", OSFamily: "linux"}
	if err := mgmt.ValidateAction(facts, "vm_start", map[string]any{"node": "pve", "vmid": 100}); err != nil {
		t.Errorf("expected valid vm_start action, got: %v", err)
	}
	if err := mgmt.ValidateAction(facts, "update_config", map[string]any{
		"node": "pve", "guestType": "qemu", "vmid": 100, "key": "memory", "value": "4096",
	}); err != nil {
		t.Errorf("expected valid update_config action, got: %v", err)
	}
	if err := mgmt.ValidateAction(facts, "update_config", map[string]any{
		"node": "pve", "guestType": "invalid_type", "vmid": 100, "key": "memory", "value": "4096",
	}); err == nil {
		t.Error("expected error for invalid guestType enum")
	}
	if err := mgmt.ValidateAction(facts, "vm_start", map[string]any{}); err == nil {
		t.Error("expected error for missing required options")
	}
	if err := mgmt.ValidateAction(facts, "vm_start", map[string]any{"node": "pve", "vmid": 0}); err == nil {
		t.Error("expected error for non-positive vmid")
	}
	if err := mgmt.ValidateAction(facts, "unknown_action", map[string]any{}); err == nil {
		t.Error("expected error for unknown action")
	}
}

func TestProxmox_SettingsFramework(t *testing.T) {
	p, ok := plugin.Get("vm/proxmox")
	if !ok {
		t.Fatal("vm/proxmox not registered")
	}

	csp, ok := p.(newapi.CategorizedSettingsProvider)
	if !ok {
		t.Fatal("expected vm/proxmox to implement newapi.CategorizedSettingsProvider")
	}

	catPage := csp.CategorizedSettingsPage()
	if catPage.ID != proxmox.SettingsPageID {
		t.Errorf("expected settings page ID %q, got %q", proxmox.SettingsPageID, catPage.ID)
	}
	if catPage.Scope != newapi.SettingsScopeDatacenter || catPage.Category != "vm" {
		t.Errorf("expected datacenter/vm categorized page, got scope=%q category=%q", catPage.Scope, catPage.Category)
	}
	if err := settingsframework.ValidateSchema(catPage.SettingsPage); err != nil {
		t.Fatalf("ValidateSchema failed: %v", err)
	}
	if err := newapi.ValidateCategorizedSettingsPage(catPage); err != nil {
		t.Fatalf("ValidateCategorizedSettingsPage failed: %v", err)
	}

	if _, ok := plugin.GetSettingsPage("vm/proxmox"); !ok {
		t.Error("expected vm/proxmox settings page to be registered in global registry")
	}
	if regCat, ok := plugin.GetCategorizedSettingsPage("vm/proxmox"); !ok || regCat.Category != "vm" {
		t.Errorf("expected vm/proxmox categorized settings page in global registry, got %+v (ok=%v)", regCat, ok)
	}

	if got := proxmox.DefaultPort(); got != proxmox.DefaultAPIPort {
		t.Errorf("DefaultPort() = %d, want %d", got, proxmox.DefaultAPIPort)
	}
	if got := proxmox.DefaultUser(); got != proxmox.DefaultAPIUser {
		t.Errorf("DefaultUser() = %q, want %q", got, proxmox.DefaultAPIUser)
	}
	if got := proxmox.DefaultStoragePool(); got != proxmox.DefaultStorage {
		t.Errorf("DefaultStoragePool() = %q, want %q", got, proxmox.DefaultStorage)
	}
	if got := proxmox.DefaultNetworkBridge(); got != proxmox.DefaultBridge {
		t.Errorf("DefaultNetworkBridge() = %q, want %q", got, proxmox.DefaultBridge)
	}
	if !proxmox.DefaultTLSInsecure() {
		t.Error("expected DefaultTLSInsecure() default true")
	}
	if _, ok := proxmox.APITokenSecretVaultVar(); ok {
		t.Error("expected APITokenSecretVaultVar() to be unset by default")
	}
}

func TestProxmox_ContextMenu(t *testing.T) {
	p, ok := plugin.Get("vm/proxmox")
	if !ok {
		t.Fatal("vm/proxmox not registered")
	}

	cmp, ok := p.(newapi.ContextMenuProvider)
	if !ok {
		t.Fatal("expected vm/proxmox to implement newapi.ContextMenuProvider")
	}

	ctx := context.Background()
	linuxFacts := plugin.HostFacts{NodeID: "node-pve", OSFamily: "linux"}
	entries := cmp.ContextMenuEntries(ctx, linuxFacts)
	if len(entries) != 10 {
		t.Fatalf("expected 10 context menu entries for linux host, got %d", len(entries))
	}
	if entries[0].ID != "proxmox-vm-start" || entries[0].Target != newapi.TargetVM || entries[0].PluginID != "vm/proxmox" {
		t.Errorf("unexpected first context menu entry: %+v", entries[0])
	}
	last := entries[len(entries)-1]
	if last.ID != "proxmox-delete-guest" || !last.Danger || last.ConfirmPrompt == "" {
		t.Errorf("expected danger entry last, got %+v", last)
	}

	winEntries := cmp.ContextMenuEntries(ctx, plugin.HostFacts{NodeID: "win1", OSFamily: "windows"})
	if len(winEntries) != 0 {
		t.Errorf("expected 0 context menu entries for windows host, got %d", len(winEntries))
	}

	regEntries := plugin.ListContextMenuEntries(ctx, newapi.TargetVM, linuxFacts)
	foundProxmox := false
	for _, e := range regEntries {
		if e.PluginID == "vm/proxmox" {
			foundProxmox = true
			break
		}
	}
	if !foundProxmox {
		t.Error("expected global registry ListContextMenuEntries(TargetVM) to include vm/proxmox entries")
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
		Endpoint string `json:"endpoint"`
		Version  string `json:"version"`
		VMs      []struct {
			VMID    int            `json:"vmid"`
			Name    string         `json:"name"`
			Type    string         `json:"type"`
			Status  string         `json:"status"`
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

func TestProxmox_CompileTask(t *testing.T) {
	p, ok := plugin.Get("vm/proxmox")
	if !ok {
		t.Fatal("vm/proxmox not registered")
	}

	tcp, ok := p.(newapi.TaskCompileProvider)
	if !ok {
		t.Fatal("expected vm/proxmox to implement newapi.TaskCompileProvider")
	}

	facts := plugin.HostFacts{
		NodeID:    "node-pve",
		HostRoute: "192.168.1.10",
		Overrides: map[string]string{"proxmox_api_port": "8006"},
	}

	compiled, err := tcp.CompileTask(context.Background(), facts, newapi.TaskRequest{
		ID:       "req-pve-1",
		Type:     newapi.TaskTypePluginAction,
		NodeID:   "node-pve",
		PluginID: "vm/proxmox",
		Action:   "vm_start",
		Params:   map[string]any{"node": "pve", "vmid": 100},
	})
	if err != nil {
		t.Fatalf("CompileTask failed: %v", err)
	}
	if compiled.Name != "Proxmox: POST /nodes/pve/qemu/100/status/start" {
		t.Errorf("unexpected CompileTask name: %q", compiled.Name)
	}

	// Fallback action from Params["action"]
	compiledFallback, err := tcp.CompileTask(context.Background(), facts, newapi.TaskRequest{
		ID:       "req-pve-2",
		Type:     newapi.TaskTypePluginAction,
		NodeID:   "node-pve",
		PluginID: "vm/proxmox",
		Params:   map[string]any{"action": "ct_shutdown", "node": "pve", "vmid": 200},
	})
	if err != nil {
		t.Fatalf("CompileTask fallback failed: %v", err)
	}
	if !strings.Contains(compiledFallback.Name, "/nodes/pve/lxc/200/status/shutdown") {
		t.Errorf("unexpected CompileTask fallback name: %q", compiledFallback.Name)
	}

	// Missing action
	if _, err := tcp.CompileTask(context.Background(), facts, newapi.TaskRequest{
		ID:       "req-pve-3",
		Type:     newapi.TaskTypePluginAction,
		NodeID:   "node-pve",
		PluginID: "vm/proxmox",
		Params:   map[string]any{"node": "pve", "vmid": 100},
	}); err == nil {
		t.Error("expected error when action is missing from TaskRequest")
	}
}
