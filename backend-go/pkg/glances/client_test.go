package glances

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strconv"
	"testing"
)

func TestFetchTelemetry_ParsesGlancesPayload(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/4/all" && r.URL.Path != "/api/3/all" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"cpu": {"total": 14.5},
			"mem": {"used": 34359738368, "total": 68719476736},
			"load": {"min1": 0.42, "min5": 0.58, "min15": 0.61}
		}`)
	}))
	defer srv.Close()

	host, portStr, err := net.SplitHostPort(srv.Listener.Addr().String())
	if err != nil {
		t.Fatalf("failed to split host port: %v", err)
	}
	port, _ := strconv.Atoi(portStr)

	client := NewClient(host, port)
	telem, err := client.FetchTelemetry("node-150")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if telem.NodeID != "node-150" || telem.CPUUsage != 14.5 || telem.MemoryUsedGiB != 32 || telem.MemoryTotalGiB != 64 {
		t.Errorf("unexpected telemetry: %+v", telem)
	}
}

func TestFetchTelemetry_LocalCLIFallback(t *testing.T) {
	if _, err := exec.LookPath("glances"); err != nil {
		t.Skip("glances CLI not installed on test host")
	}

	// Port 61209 is closed, should fall back to local glances CLI
	client := NewClient("127.0.0.1", 61209)
	telem, err := client.FetchTelemetry("dev-pc")
	if err != nil {
		t.Fatalf("expected CLI fallback to succeed for local host, got: %v", err)
	}
	if telem.NodeID != "dev-pc" || telem.MemoryTotalGiB <= 0 {
		t.Errorf("unexpected telemetry from CLI fallback: %+v", telem)
	}
}
