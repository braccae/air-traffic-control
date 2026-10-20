package tailscale

import (
	"context"
	"strings"
	"testing"

	"air-traffic-control/backend/pkg/plugin"
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
