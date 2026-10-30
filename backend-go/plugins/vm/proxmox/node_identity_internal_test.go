package proxmox

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func testClient(t *testing.T, h http.Handler, cfg Config) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	cfg.Host = srv.URL
	cfg.User = "root@pam"
	cfg.TokenID = "atc"
	cfg.TokenSecret = "test-secret"
	return NewClient(cfg)
}

func writeEnvelope(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
}

func TestFilterGuestsForNode(t *testing.T) {
	guests := []GuestResource{
		{ID: "qemu/100", Type: "qemu", Node: "pong", VMID: 100, Name: "web01"},
		{ID: "qemu/101", Type: "qemu", Node: "cold-war", VMID: 101, Name: "db01"},
		{ID: "lxc/200", Type: "lxc", Node: "noodle", VMID: 200, Name: "ct01"},
		{ID: "lxc/201", Type: "lxc", Node: "pong", VMID: 201, Name: "ct02"},
	}

	t.Run("keeps only guests of the inspected node", func(t *testing.T) {
		got, err := filterGuestsForNode(guests, "pong")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("expected 2 pong guests, got %d (%+v)", len(got), got)
		}
		for _, g := range got {
			if g.Node != "pong" {
				t.Errorf("guest %s from node %q leaked into the pong listing", g.ID, g.Node)
			}
		}
		if got[0].VMID != 100 || got[1].VMID != 201 {
			t.Errorf("unexpected guests kept: %+v", got)
		}
	})

	t.Run("excludes guests of other cluster members", func(t *testing.T) {
		got, err := filterGuestsForNode(guests, "cold-war")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 1 || got[0].VMID != 101 {
			t.Fatalf("expected only the cold-war guest, got %+v", got)
		}
	})

	t.Run("node name matching is trimmed and case-insensitive", func(t *testing.T) {
		got, err := filterGuestsForNode(guests, "  PONG ")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("expected 2 pong guests, got %+v", got)
		}
	})

	t.Run("unknown node name fails instead of returning the whole cluster", func(t *testing.T) {
		for _, name := range []string{"", "   "} {
			got, err := filterGuestsForNode(guests, name)
			if err == nil {
				t.Fatalf("expected an error for node name %q, got %d guests", name, len(got))
			}
			if got != nil {
				t.Errorf("expected no guests alongside the error, got %+v", got)
			}
		}
	})

	t.Run("guests with no node field are not attributed to this node", func(t *testing.T) {
		got, err := filterGuestsForNode([]GuestResource{{ID: "qemu/300", Type: "qemu", VMID: 300}}, "pong")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("expected no guests, got %+v", got)
		}
	})

	t.Run("node without guests yields an empty non-nil list", func(t *testing.T) {
		got, err := filterGuestsForNode(guests, "noodle-2")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got == nil || len(got) != 0 {
			t.Errorf("expected an empty list, got %#v", got)
		}
	})
}

func TestLocalNodeName(t *testing.T) {
	entries := []ClusterStatusEntry{
		{Type: "cluster", Name: "leo-banjo"},
		{Type: "node", Name: "pong", IP: "10.0.0.1", Local: 1},
		{Type: "node", Name: "cold-war", IP: "10.0.0.2"},
	}
	got, err := localNodeName(entries)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "pong" {
		t.Errorf("expected pong, got %q", got)
	}

	if _, err := localNodeName([]ClusterStatusEntry{{Type: "cluster", Name: "leo-banjo"}}); err == nil {
		t.Error("expected an error when no member is flagged local")
	}

	if _, err := localNodeName([]ClusterStatusEntry{
		{Type: "node", Name: "pong", Local: 1},
		{Type: "node", Name: "noodle", Local: 1},
	}); err == nil {
		t.Error("expected an error when multiple members are flagged local")
	}
}

func TestNodeNameByIP(t *testing.T) {
	entries := []ClusterStatusEntry{
		{Type: "node", Name: "pong", IP: "10.0.0.1"},
		{Type: "node", Name: "cold-war", IP: "10.0.0.2"},
	}
	cases := []struct {
		host string
		want string
	}{
		{"10.0.0.2", "cold-war"},
		{"100.64.0.9", ""},
		{"", ""},
	}
	for _, tc := range cases {
		got, err := nodeNameByIP(entries, tc.host)
		if tc.want == "" {
			if err == nil {
				t.Errorf("host %q: expected an error, got %q", tc.host, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("host %q: unexpected error: %v", tc.host, err)
		}
		if got != tc.want {
			t.Errorf("host %q: expected %q, got %q", tc.host, tc.want, got)
		}
	}
}

func TestResolveNodeNamePrefersDeclaredOverride(t *testing.T) {
	var hits int
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		http.Error(w, "must not be called", http.StatusInternalServerError)
	}), Config{NodeName: "cold-war"})

	got, err := resolveNodeName(context.Background(), c, nil, Config{Host: "10.0.0.9", NodeName: "cold-war"}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "cold-war" {
		t.Errorf("expected cold-war, got %q", got)
	}
	if hits != 0 {
		t.Errorf("declared node name should not trigger API calls, got %d", hits)
	}
}

func TestResolveNodeNameUsesApiLocalFlag(t *testing.T) {
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api2/json/cluster/status" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		writeEnvelope(w, []map[string]any{
			{"type": "cluster", "name": "leo-banjo"},
			{"type": "node", "name": "pong", "ip": "10.0.0.1", "local": 1},
			{"type": "node", "name": "noodle", "ip": "10.0.0.3"},
		})
	}), Config{})

	got, err := resolveNodeName(context.Background(), c, nil, Config{Host: "10.0.0.1"}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "pong" {
		t.Errorf("expected pong, got %q", got)
	}
}

func TestResolveNodeNameFailsWithoutIdentity(t *testing.T) {
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeEnvelope(w, []map[string]any{
			{"type": "cluster", "name": "leo-banjo"},
			{"type": "node", "name": "pong", "ip": "10.0.0.1"},
			{"type": "node", "name": "cold-war", "ip": "10.0.0.2"},
			{"type": "node", "name": "noodle", "ip": "10.0.0.3"},
		})
	}), Config{})

	nodes := []NodeInfo{{Node: "pong"}, {Node: "cold-war"}, {Node: "noodle"}}
	got, err := resolveNodeName(context.Background(), c, nil, Config{Host: "100.64.0.9"}, nodes)
	if err == nil {
		t.Fatalf("expected an error, got node %q", got)
	}
	if !strings.Contains(err.Error(), "cannot determine which Proxmox node") {
		t.Errorf("error should explain the failed identity resolution, got %v", err)
	}
	if !strings.Contains(err.Error(), "proxmox_node_name") {
		t.Errorf("error should mention the override, got %v", err)
	}
}

func TestResolveNodeNameFallsBackToSingleMember(t *testing.T) {
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"data":null,"errors":{"permission":"no Sys.Audit"}}`, http.StatusForbidden)
	}), Config{})

	got, err := resolveNodeName(context.Background(), c, nil, Config{Host: "10.0.0.1"}, []NodeInfo{{Node: "pong"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "pong" {
		t.Errorf("expected pong, got %q", got)
	}
}

func TestHostOnly(t *testing.T) {
	cases := map[string]string{
		"https://pong:8006":          "pong",
		"pong.leo-banjo.ts.net:8006": "pong.leo-banjo.ts.net",
		"10.0.0.1":                   "10.0.0.1",
		"[fd7a::1]:8006":             "fd7a::1",
		"  ":                         "",
	}
	for in, want := range cases {
		if got := hostOnly(in); got != want {
			t.Errorf("hostOnly(%q) = %q, want %q", in, got, want)
		}
	}
}
