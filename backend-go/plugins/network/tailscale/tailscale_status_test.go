package tailscale

import (
	"context"
	"strings"
	"testing"

	"air-traffic-control/backend/pkg/plugin"
	"air-traffic-control/backend/pkg/plugin/newapi"
	"air-traffic-control/backend/pkg/plugin/settingsframework"
)

// fakeRunner returns canned output for commands matching a substring key.
type fakeRunner struct {
	responses map[string]string
}

func (f *fakeRunner) RunCommand(_ context.Context, cmd string) (string, error) {
	// Prefer the most specific (longest) matching key so "status --json" does
	// not shadow "debug prefs".
	bestKey := ""
	for k := range f.responses {
		if strings.Contains(cmd, k) && len(k) > len(bestKey) {
			bestKey = k
		}
	}
	if bestKey == "" {
		return "", nil
	}
	return f.responses[bestKey], nil
}

const sampleStatusJSON = `{
  "BackendState": "Running",
  "MagicDNSSuffix": "leo-banjo.ts.net",
  "Health": ["example health note"],
  "Self": {
    "ID": "nSELF123CNTRL",
    "HostName": "dev-pc",
    "DNSName": "dev-pc.leo-banjo.ts.net.",
    "TailscaleIPs": ["100.97.159.101"],
    "Online": true,
    "OS": "linux",
    "Tags": ["tag:host"],
    "ExitNode": false
  },
  "Peer": {
    "nodekey:aaaa": {
      "HostName": "pebble",
      "DNSName": "pebble.leo-banjo.ts.net.",
      "TailscaleIPs": ["100.72.21.74"],
      "Online": true,
      "Active": false,
      "OS": "linux",
      "Relay": "",
      "CurAddr": "192.168.8.10:41641",
      "RxBytes": 1024,
      "TxBytes": 2048,
      "LastSeen": "2026-10-20T09:10:00Z",
      "LastHandshake": "2026-10-20T09:09:00Z",
      "ExitNode": false,
      "ExitNodeOption": true,
      "AllowedIPs": ["100.72.21.74/32"],
      "Tags": ["tag:host"]
    },
    "nodekey:bbbb": {
      "HostName": "flint",
      "DNSName": "flint.leo-banjo.ts.net.",
      "TailscaleIPs": ["100.73.87.87"],
      "Online": false,
      "OS": "linux"
    }
  }
}`

const samplePrefsJSON = `{
  "CorpDNS": true,
  "RouteAll": false,
  "RunSSH": true,
  "AdvertiseRoutes": ["10.0.0.0/8", "192.168.0.0/24"],
  "ExitNodeIP": "",
  "ExitNodeID": "",
  "Hostname": "dev-pc"
}`

func TestReadReturnsStructuredPayload(t *testing.T) {
	r := &fakeRunner{responses: map[string]string{
		"debug prefs":   samplePrefsJSON,
		"status --json": sampleStatusJSON,
	}}

	got, err := New().Read(context.Background(), r, plugin.HostFacts{NodeID: "node-dev-pc"})
	if err != nil {
		t.Fatalf("Read returned error: %v", err)
	}

	result, ok := got.(ReadResult)
	if !ok {
		t.Fatalf("Read returned %T, want ReadResult", got)
	}

	if result.BackendState != "Running" {
		t.Errorf("BackendState = %q, want Running", result.BackendState)
	}
	if result.MagicDNSSuffix != "leo-banjo.ts.net" {
		t.Errorf("MagicDNSSuffix = %q", result.MagicDNSSuffix)
	}
	if len(result.Health) != 1 || result.Health[0] != "example health note" {
		t.Errorf("Health = %v", result.Health)
	}
	if result.Self == nil || result.Self.HostName != "dev-pc" || !result.Self.Online {
		t.Errorf("Self = %+v", result.Self)
	}
	if len(result.Peers) != 2 {
		t.Fatalf("Peers length = %d, want 2", len(result.Peers))
	}
	// Sorted by key: nodekey:aaaa before nodekey:bbbb.
	if result.Peers[0].HostName != "pebble" || !result.Peers[0].ExitNodeOption {
		t.Errorf("Peers[0] = %+v", result.Peers[0])
	}
	if result.Peers[0].RxBytes != 1024 || result.Peers[0].TxBytes != 2048 {
		t.Errorf("Peers[0] byte counters = %v/%v", result.Peers[0].RxBytes, result.Peers[0].TxBytes)
	}
	if result.Peers[1].HostName != "flint" || result.Peers[1].Online {
		t.Errorf("Peers[1] = %+v", result.Peers[1])
	}

	// Prefs mapping.
	if !result.Prefs.AcceptDNS {
		t.Errorf("Prefs.AcceptDNS = false, want true (CorpDNS)")
	}
	if result.Prefs.AcceptRoutes {
		t.Errorf("Prefs.AcceptRoutes = true, want false (RouteAll)")
	}
	if !result.Prefs.SSH {
		t.Errorf("Prefs.SSH = false, want true (RunSSH)")
	}
	if result.Prefs.AdvertiseRoutes != "10.0.0.0/8,192.168.0.0/24" {
		t.Errorf("Prefs.AdvertiseRoutes = %q", result.Prefs.AdvertiseRoutes)
	}
	if result.Prefs.Hostname != "dev-pc" {
		t.Errorf("Prefs.Hostname = %q", result.Prefs.Hostname)
	}
}

func TestReadWithoutPrefsStillSucceeds(t *testing.T) {
	r := &fakeRunner{responses: map[string]string{
		"status --json": sampleStatusJSON,
	}}

	got, err := New().Read(context.Background(), r, plugin.HostFacts{NodeID: "node-dev-pc"})
	if err != nil {
		t.Fatalf("Read returned error: %v", err)
	}
	result := got.(ReadResult)
	if result.Prefs.AcceptDNS || result.Prefs.SSH || result.Prefs.Hostname != "" {
		t.Errorf("expected zero-value prefs when prefs unavailable, got %+v", result.Prefs)
	}
	if len(result.Peers) != 2 {
		t.Errorf("Peers length = %d, want 2", len(result.Peers))
	}
}

func TestCompileBoolAction(t *testing.T) {
	spec, err := New().Compile(plugin.HostFacts{}, "set_accept_dns", map[string]any{"enabled": false})
	if err != nil {
		t.Fatalf("Compile error: %v", err)
	}
	if len(spec.Steps) != 1 {
		t.Fatalf("steps = %d, want 1", len(spec.Steps))
	}
	if spec.Steps[0].Module != "ansible.builtin.command" {
		t.Errorf("module = %q", spec.Steps[0].Module)
	}
	if got := spec.Steps[0].Params["cmd"]; got != "tailscale set --accept-dns=false" {
		t.Errorf("cmd = %v, want tailscale set --accept-dns=false", got)
	}

	spec, err = New().Compile(plugin.HostFacts{}, "set_ssh", map[string]any{"enabled": true})
	if err != nil {
		t.Fatalf("Compile error: %v", err)
	}
	if got := spec.Steps[0].Params["cmd"]; got != "tailscale set --ssh=true" {
		t.Errorf("cmd = %v, want tailscale set --ssh=true", got)
	}
}

func TestCompileStringAction(t *testing.T) {
	spec, err := New().Compile(plugin.HostFacts{}, "set_advertise_routes", map[string]any{"routes": "10.0.0.0/8,192.168.0.0/24"})
	if err != nil {
		t.Fatalf("Compile error: %v", err)
	}
	if got := spec.Steps[0].Params["cmd"]; got != "tailscale set --advertise-routes=10.0.0.0/8,192.168.0.0/24" {
		t.Errorf("cmd = %v", got)
	}

	spec, err = New().Compile(plugin.HostFacts{}, "set_exit_node", map[string]any{"exitNode": "auto:any"})
	if err != nil {
		t.Fatalf("Compile error: %v", err)
	}
	if got := spec.Steps[0].Params["cmd"]; got != "tailscale set --exit-node=auto:any" {
		t.Errorf("cmd = %v", got)
	}
}

func TestCompileRejectsUnknownAction(t *testing.T) {
	if _, err := New().Compile(plugin.HostFacts{}, "bogus_action", map[string]any{}); err == nil {
		t.Fatal("expected error for unknown action, got nil")
	}
}

func TestCompileRejectsMissingBoolOption(t *testing.T) {
	if _, err := New().Compile(plugin.HostFacts{}, "set_accept_routes", map[string]any{}); err == nil {
		t.Fatal("expected error for missing enabled option, got nil")
	}
}

func TestCompileRejectsEmptyStringOption(t *testing.T) {
	if _, err := New().Compile(plugin.HostFacts{}, "set_hostname", map[string]any{"hostname": "   "}); err == nil {
		t.Fatal("expected error for empty hostname, got nil")
	}
}

func TestCompileRejectsInjection(t *testing.T) {
	cases := []string{
		"evil; rm -rf /",
		"foo bar",
		"foo$(whoami)",
		"foo`id`",
		"-dashleading",
		"foo|bar",
		"foo\"bar",
	}
	for _, bad := range cases {
		if _, err := New().Compile(plugin.HostFacts{}, "set_hostname", map[string]any{"hostname": bad}); err == nil {
			t.Errorf("expected error for hostname %q, got nil", bad)
		}
	}
}

func TestActionsExposeExpectedFeatureSet(t *testing.T) {
	names := map[string]bool{}
	for _, a := range New().Actions() {
		names[a.Name] = true
	}
	for _, want := range []string{
		"set_accept_dns",
		"set_accept_routes",
		"set_ssh",
		"set_advertise_exit_node",
		"set_advertise_routes",
		"set_exit_node",
		"set_hostname",
	} {
		if !names[want] {
			t.Errorf("missing action %q", want)
		}
	}
}

func TestTailscaleStatusPlugin_RegistrationAndProbe(t *testing.T) {
	p, ok := plugin.Get("network/tailscale")
	if !ok {
		t.Fatalf("expected network/tailscale to be registered in default registry")
	}

	meta := p.Meta()
	if meta.ID != "network/tailscale" || meta.Kind != plugin.KindNetwork || meta.Name != "tailscale" || meta.Order != 20 {
		t.Fatalf("unexpected plugin meta: %+v", meta)
	}

	// Nil runner -> OFFLINE
	det, err := p.Probe(context.Background(), nil, plugin.HostFacts{NodeID: "node-1"})
	if err != nil {
		t.Fatalf("unexpected error on nil runner: %v", err)
	}
	if det.Detected || det.Status != "OFFLINE" {
		t.Fatalf("expected OFFLINE detection for nil runner, got %+v", det)
	}

	// Empty output -> UNAVAILABLE
	emptyRunner := &fakeRunner{responses: map[string]string{}}
	det, err = p.Probe(context.Background(), emptyRunner, plugin.HostFacts{NodeID: "node-1"})
	if err != nil {
		t.Fatalf("unexpected error on empty output: %v", err)
	}
	if det.Detected || det.Status != "UNAVAILABLE" {
		t.Fatalf("expected UNAVAILABLE detection for empty output, got %+v", det)
	}

	// Valid JSON -> Detected=true
	activeRunner := &fakeRunner{responses: map[string]string{
		"status --json": sampleStatusJSON,
	}}
	det, err = p.Probe(context.Background(), activeRunner, plugin.HostFacts{NodeID: "node-1"})
	if err != nil {
		t.Fatalf("unexpected error on valid status JSON: %v", err)
	}
	if !det.Detected || det.Status != "Running" || !strings.Contains(det.Details, "2 peer(s)") {
		t.Fatalf("unexpected active detection: %+v", det)
	}
}

func TestTailscaleStatusPlugin_V2NewAPIInterfaces(t *testing.T) {
	p, ok := plugin.Get("network/tailscale")
	if !ok {
		t.Fatalf("network/tailscale not registered")
	}

	facts := plugin.HostFacts{NodeID: "node-dev-pc", OSFamily: "linux"}

	// 1. DataCollectionPlugin
	dc, ok := p.(newapi.DataCollectionPlugin)
	if !ok {
		t.Fatalf("expected network/tailscale to implement newapi.DataCollectionPlugin")
	}
	sched := dc.CollectionSchedule()
	if sched.Interval <= 0 || sched.TTL <= 0 {
		t.Fatalf("expected positive schedule Interval and TTL, got %+v", sched)
	}
	schema := dc.CollectionSchema()
	if schema.Version != "v2" || schema.RootKey != "peers" {
		t.Fatalf("unexpected collection schema: %+v", schema)
	}

	// 2. ManagementPlugin
	mgmt, ok := p.(newapi.ManagementPlugin)
	if !ok {
		t.Fatalf("expected network/tailscale to implement newapi.ManagementPlugin")
	}
	mgmtActions := mgmt.ManagementActions()
	if len(mgmtActions) != 7 {
		t.Fatalf("expected 7 management actions, got %d", len(mgmtActions))
	}
	legacyActions := mgmt.Actions()
	if len(legacyActions) != len(mgmtActions) {
		t.Fatalf("Actions() length %d does not match ManagementActions() length %d", len(legacyActions), len(mgmtActions))
	}

	if err := mgmt.ValidateAction(facts, "set_accept_dns", map[string]any{"enabled": true}); err != nil {
		t.Fatalf("expected valid set_accept_dns options, got error: %v", err)
	}
	if err := mgmt.ValidateAction(facts, "set_accept_dns", map[string]any{}); err == nil {
		t.Fatalf("expected error when required enabled option is missing")
	}
	if err := mgmt.ValidateAction(facts, "set_hostname", map[string]any{"hostname": "dev-pc"}); err != nil {
		t.Fatalf("expected valid set_hostname options, got error: %v", err)
	}
	if err := mgmt.ValidateAction(facts, "set_hostname", map[string]any{"hostname": "evil; rm -rf /"}); err == nil {
		t.Fatalf("expected error for command injection value in ValidateAction")
	}
	if err := mgmt.ValidateAction(facts, "unknown_action", map[string]any{}); err == nil {
		t.Fatalf("expected error for unknown action")
	}

	// 3. CategorizedSettingsProvider
	csp, ok := p.(newapi.CategorizedSettingsProvider)
	if !ok {
		t.Fatalf("expected network/tailscale to implement newapi.CategorizedSettingsProvider")
	}
	catPage := csp.CategorizedSettingsPage()
	if err := settingsframework.ValidateSchema(csp.SettingsPage()); err != nil {
		t.Fatalf("settings schema validation failed: %v", err)
	}
	if err := newapi.ValidateCategorizedSettingsPage(catPage); err != nil {
		t.Fatalf("categorized settings page validation failed: %v", err)
	}
	if catPage.ID != "network/tailscale" || catPage.Category != "network" || catPage.Scope != newapi.SettingsScopeDatacenter {
		t.Fatalf("unexpected categorized settings page metadata: %+v", catPage)
	}

	// 4. ContextMenuProvider
	cmp, ok := p.(newapi.ContextMenuProvider)
	if !ok {
		t.Fatalf("expected network/tailscale to implement newapi.ContextMenuProvider")
	}
	entries := cmp.ContextMenuEntries(context.Background(), facts)
	if len(entries) != 7 {
		t.Fatalf("expected 7 context menu entries, got %d", len(entries))
	}
	for _, entry := range entries {
		if entry.PluginID != "network/tailscale" || entry.Target != newapi.TargetNetwork || entry.Action == "" {
			t.Errorf("unexpected context menu entry: %+v", entry)
		}
	}

	// 5. TaskCompileProvider
	tcp, ok := p.(newapi.TaskCompileProvider)
	if !ok {
		t.Fatalf("expected network/tailscale to implement newapi.TaskCompileProvider")
	}
	compiledTask, err := tcp.CompileTask(context.Background(), facts, newapi.TaskRequest{
		Type:     newapi.TaskTypePluginAction,
		PluginID: "network/tailscale",
		Action:   "set_exit_node",
		NodeID:   "node-dev-pc",
		Params: map[string]any{
			"exitNode": "auto:any",
		},
	})
	if err != nil {
		t.Fatalf("CompileTask failed: %v", err)
	}
	if len(compiledTask.Steps) != 1 || compiledTask.Steps[0].Params["cmd"] != "tailscale set --exit-node=auto:any" {
		t.Fatalf("unexpected CompileTask output: %+v", compiledTask)
	}
}
