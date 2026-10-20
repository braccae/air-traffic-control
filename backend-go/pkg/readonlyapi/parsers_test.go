package readonlyapi

import (
	"encoding/json"
	"testing"
)

// These are pure-function unit tests for the ported parsers. They exercise the
// same command output shapes the Express handlers consume. Route-level parity is
// verified separately against a REAL host over SSH (see PHASE1_REPORT.md); these
// tests only pin the parsing logic so a refactor cannot silently change it.

func TestSplitMarker(t *testing.T) {
	before, after := splitMarker("a===X===b===X===c", "===X===")
	if before != "a" {
		t.Fatalf("before = %q, want %q", before, "a")
	}
	// Only the first marker splits; the rest stays intact in the tail.
	if after != "b===X===c" {
		t.Fatalf("after = %q, want %q", after, "b===X===c")
	}

	before, after = splitMarker("no-marker-here", "===X===")
	if before != "no-marker-here" || after != "" {
		t.Fatalf("no-marker split = (%q, %q)", before, after)
	}
}

func TestParseFirewallListAll(t *testing.T) {
	activeZones := `public (active)
  target: default
  interfaces: eth0 tailscale0
trusted (active)
  target: ACCEPT
  interfaces: tailscale0`

	listAll := `public (active)
  target: default
  services: cockpit dhcpv6-client ssh
  ports: 8080/tcp 8443/tcp
  rich rules:
	rule family="ipv4" source address="100.64.0.0/10" accept
	rule family="ipv4" port port="9443" protocol="tcp" accept`

	parsed := parseFirewallListAll(listAll, activeZones)

	if len(parsed.Services) != 3 {
		t.Fatalf("services = %v, want 3 entries", parsed.Services)
	}
	if parsed.Services[0] != "cockpit" || parsed.Services[2] != "ssh" {
		t.Fatalf("services = %v", parsed.Services)
	}
	if len(parsed.Ports) != 2 || parsed.Ports[0] != "8080/tcp" {
		t.Fatalf("ports = %v", parsed.Ports)
	}
	if len(parsed.RichRules) != 2 {
		t.Fatalf("richRules = %v, want 2", parsed.RichRules)
	}

	// trusted zone carries tailscale0 -> synthetic fw-ts rule must exist.
	foundTS := false
	for _, r := range parsed.Rules {
		if r.ID == "fw-ts" {
			foundTS = true
			if r.Zone != "trusted" {
				t.Fatalf("fw-ts zone = %q, want trusted", r.Zone)
			}
		}
	}
	if !foundTS {
		t.Fatalf("missing synthetic tailscale rule; rules=%+v", parsed.Rules)
	}

	// Zone map must have parsed interfaces.
	if got := parsed.Zones["public"].Interfaces; len(got) != 2 || got[1] != "tailscale0" {
		t.Fatalf("public interfaces = %v", got)
	}
}

func TestFormatDiskSize(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{500, "500 B"},
		{1048576, "1.0 MB"},
		{1073741824, "1.0 GB"},
		{1099511627776, "1.0 TB"},
		{2000398934016, "1.8 TB"},
	}
	for _, c := range cases {
		if got := formatDiskSize(c.in); got != c.want {
			t.Errorf("formatDiskSize(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestFormatIfaceBytes(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{512, "512 B"},
		{2048, "2 KiB"},
		{1572864, "1.5 MiB"},
		{1073741824, "1.00 GiB"},
	}
	for _, c := range cases {
		if got := formatIfaceBytes(c.in); got != c.want {
			t.Errorf("formatIfaceBytes(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSplitFields(t *testing.T) {
	got := splitFields("  a   b\tc  ")
	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("splitFields = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("splitFields[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	if empty := splitFields("   "); len(empty) != 0 {
		t.Fatalf("splitFields(blank) = %v, want empty", empty)
	}
}

func TestLsblkSizeJSONNumber(t *testing.T) {
	// lsblk -b emits byte sizes as JSON numbers, sometimes larger than float64
	// can represent exactly; ensure we decode via json.Number and not float64.
	raw := `{"blockdevices":[{"name":"nvme0n1","size":2000398934016,"type":"disk","rota":false,"children":[{"name":"nvme0n1p1","size":536870912,"type":"part"}]}]}`
	var root lsblkRoot
	if err := json.Unmarshal([]byte(raw), &root); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(root.BlockDevices) != 1 {
		t.Fatalf("devices = %d, want 1", len(root.BlockDevices))
	}
	if got := toInt64(root.BlockDevices[0].Size); got != 2000398934016 {
		t.Fatalf("size = %d, want 2000398934016", got)
	}
	if len(root.BlockDevices[0].Children) != 1 {
		t.Fatalf("children = %d, want 1", len(root.BlockDevices[0].Children))
	}
}
