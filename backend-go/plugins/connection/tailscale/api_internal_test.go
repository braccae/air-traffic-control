package tailscale

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestTailnetHostnameUsesAssignedName(t *testing.T) {
	cases := []struct {
		dnsName   string
		requested string
		want      string
	}{
		{"air-traffic-control-dev-1.leo-banjo.ts.net.", "air-traffic-control-dev", "air-traffic-control-dev-1"},
		{"node.tailnet.ts.net.", "node", "node"},
		{"", "requested-name", "requested-name"},
		{"no-dots", "requested-name", "no-dots"},
	}
	for _, tc := range cases {
		if got := tailnetHostname(tc.dnsName, tc.requested); got != tc.want {
			t.Fatalf("tailnetHostname(%q, %q) = %q, want %q", tc.dnsName, tc.requested, got, tc.want)
		}
	}
}

func TestDeviceMatchesName(t *testing.T) {
	if !deviceMatchesName(apiDevice{Hostname: "atc", Name: "atc-1.tailnet.ts.net."}, "atc") {
		t.Fatal("hostname match failed")
	}
	if !deviceMatchesName(apiDevice{Hostname: "other", Name: "atc.tailnet.ts.net."}, "atc") {
		t.Fatal("assigned-name match failed")
	}
	if deviceMatchesName(apiDevice{Hostname: "other", Name: "other.tailnet.ts.net."}, "atc") {
		t.Fatal("unrelated device matched")
	}
	if deviceMatchesName(apiDevice{Hostname: "atc", Name: "atc.tailnet.ts.net."}, "  ") {
		t.Fatal("blank requested name matched")
	}
}

func TestCleanupStaleDevicesDeletesOfflineAndRenames(t *testing.T) {
	var mu sync.Mutex
	var deleted []string
	var renamed string

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/tailnet/-/devices", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			http.Error(w, `{"message":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"devices": []map[string]any{
				{"id": "n1", "name": "atc-1.tailnet.ts.net", "hostname": "atc", "connectedToControl": true},
				{"id": "n2", "name": "atc.tailnet.ts.net", "hostname": "atc", "connectedToControl": false},
				{"id": "n3", "name": "atc-2.tailnet.ts.net", "hostname": "atc", "connectedToControl": true},
				{"id": "n4", "name": "other.tailnet.ts.net", "hostname": "other", "connectedToControl": false},
			},
		})
	})
	mux.HandleFunc("/api/v2/device/", func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodDelete && r.URL.Path == "/api/v2/device/n2":
			mu.Lock()
			deleted = append(deleted, "n2")
			mu.Unlock()
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v2/device/n1/name":
			var body struct {
				Name string `json:"name"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			mu.Lock()
			renamed = body.Name
			mu.Unlock()
			w.WriteHeader(http.StatusOK)
		default:
			http.Error(w, `{"message":"unexpected"}`, http.StatusBadRequest)
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := &tailscaleAPIClient{
		apiKey:  "test-key",
		tailnet: "-",
		baseURL: srv.URL + "/api/v2",
		http:    srv.Client(),
	}
	result, err := cleanupStaleDevices(context.Background(), client, "atc", "n1", "atc-1")
	if err != nil {
		t.Fatalf("cleanupStaleDevices: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if strings.Join(deleted, ",") != "n2" {
		t.Fatalf("deleted = %v, want only the offline duplicate n2", deleted)
	}
	if renamed != "atc" {
		t.Fatalf("renamed = %q, want %q", renamed, "atc")
	}
	if !result.Renamed || result.CurrentName != "atc" {
		t.Fatalf("result = %+v", result)
	}
	if len(result.Deleted) != 1 || result.Deleted[0].ID != "n2" {
		t.Fatalf("deleted refs = %+v", result.Deleted)
	}
	if len(result.Skipped) != 1 || result.Skipped[0].ID != "n3" {
		t.Fatalf("skipped refs = %+v", result.Skipped)
	}
}

func TestCleanupStaleDevicesSkipsOnlineOnly(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/tailnet/-/devices", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"devices": []map[string]any{
				{"id": "n1", "name": "atc.tailnet.ts.net", "hostname": "atc", "connectedToControl": true},
			},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := &tailscaleAPIClient{apiKey: "k", tailnet: "-", baseURL: srv.URL + "/api/v2", http: srv.Client()}
	result, err := cleanupStaleDevices(context.Background(), client, "atc", "n1", "atc")
	if err != nil {
		t.Fatalf("cleanupStaleDevices: %v", err)
	}
	if len(result.Deleted) != 0 || result.Renamed {
		t.Fatalf("unexpected changes: %+v", result)
	}
	if !strings.Contains(result.Message, "nothing changed") {
		t.Fatalf("message = %q", result.Message)
	}
}
