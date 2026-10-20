package nodeapi

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"air-traffic-control/backend/pkg/ssh"
)

// TestLiveHardwareProbe is skipped unless ATC_NODEAPI_LIVE_HOST is set.
// It executes a REAL hardware discovery probe over SSH against the target host
// and asserts parsed findings and real stdout execution.
func TestLiveHardwareProbe(t *testing.T) {
	host := os.Getenv("ATC_NODEAPI_LIVE_HOST")
	if host == "" {
		t.Skip("set ATC_NODEAPI_LIVE_HOST to run the live hardware probe test")
	}
	user := os.Getenv("ATC_NODEAPI_LIVE_USER")
	if user == "" {
		user = "core"
	}
	port := 22
	if raw := os.Getenv("ATC_NODEAPI_LIVE_PORT"); raw != "" {
		p, err := strconv.Atoi(raw)
		if err != nil {
			t.Fatalf("ATC_NODEAPI_LIVE_PORT: %v", err)
		}
		port = p
	}

	sshMgr := ssh.NewClientManager()
	runner := NewSSHRunner(sshMgr.Pool())

	node := NewObject()
	node.SetString("id", "live-probe-node")
	node.SetString("name", host)
	node.SetString("ip", host)
	node.SetJSON("port", port)
	node.SetString("sshUser", user)

	cmd := ProbeCommand()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	rawOutput, err := runner.RunNodeCommand(ctx, node, cmd, 15*time.Second)
	if err != nil {
		t.Fatalf("live RunNodeCommand failed on host %s: %v", host, err)
	}

	if strings.TrimSpace(rawOutput) == "" {
		t.Fatalf("expected non-empty real stdout from live host")
	}
	if !strings.Contains(rawOutput, "===ARCH===") {
		t.Errorf("expected rawOutput to contain ===ARCH=== marker, got %q", rawOutput)
	}

	hardware := ParseHardwareProbeOutput(rawOutput)
	if hardware.CPU == "" {
		t.Errorf("expected non-empty hardware.cpu from live host")
	}
	if hardware.VCPUs < 1 {
		t.Errorf("expected vcpus >= 1, got %d", hardware.VCPUs)
	}
	if hardware.RamGiB < 1 {
		t.Errorf("expected ramGiB >= 1, got %d", hardware.RamGiB)
	}
}
