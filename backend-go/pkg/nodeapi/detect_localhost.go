package nodeapi

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os/exec"
	"strings"
	"time"

	"air-traffic-control/backend/pkg/hostcache"
)

// EnsureLocalhostNode detects the local host running ATC and automatically
// provisions or registers it as a managed node in the inventory with appropriate
// metadata (hostname, OS, architecture, resource metrics).
func EnsureLocalhostNode(opts Options) error {
	store := opts.Store
	if store == nil {
		return nil
	}

	now := opts.Now
	if now == nil {
		now = time.Now
	}

	runner := opts.Runner
	if runner == nil {
		runner = NewLocalRunner("sh", "")
	}

	// Probe local hardware using the existing probe command.
	out, err := runner.RunNodeCommand(context.Background(), nil, ProbeCommand(), 10*time.Second)
	if err != nil {
		log.Printf("localhost auto-detection: probe failed: %v", err)
		// Fall back to minimal detection.
		out = ""
	}

	probed := ParseHardwareProbeOutput(out)

	hostname := "localhost"
	if h, err := exec.Command("hostname").Output(); err == nil {
		if hStr := strings.TrimSpace(string(h)); hStr != "" {
			hostname = hStr
		}
	}

	// Check if localhost node already exists.
	recs, err := store.GetAllHosts()
	if err == nil {
		for _, rec := range recs {
			if strings.EqualFold(rec.IP, "127.0.0.1") || strings.EqualFold(rec.ShortName, hostname) {
				return nil
			}
		}
	}

	// Build NodeJSON with metadata.
	nodeJSON := NewObject()
	nodeJSON.SetString("id", "node-localhost")
	nodeJSON.SetString("name", hostname+" (local)")
	nodeJSON.SetString("shortName", hostname)
	nodeJSON.SetString("group", "tag:host")
	nodeJSON.SetString("datacenter", "local")
	nodeJSON.SetString("nodeType", "linux")
	nodeJSON.SetString("badge", "LOCAL")
	nodeJSON.SetString("badgeColor", "text-[#b0c9ff]")
	nodeJSON.SetString("ip", "127.0.0.1")
	nodeJSON.SetJSON("port", 22)
	nodeJSON.SetString("sshUser", "core")
	nodeJSON.SetString("authMethod", "local-socket")
	nodeJSON.SetString("status", "online")
	nodeJSON.SetJSON("sshConnected", true)
	nodeJSON.SetString("rttMs", "0.1ms")
	nodeJSON.SetString("uptime", "1m")
	nodeJSON.SetJSON("hasGlances", true)
	nodeJSON.SetJSON("hasCockpit", true)
	nodeJSON.SetJSON("hasPodman", true)
	nodeJSON.SetJSON("hasRestic", true)
	nodeJSON.SetString("osDescription", probed.OSDescription)
	nodeJSON.SetJSON("tags", []string{"tag:host"})

	specsObj := NewObject()
	specsObj.SetString("cpu", probed.CPU)
	specsObj.SetJSON("vcpus", probed.VCPUs)
	specsObj.SetJSON("ramGiB", probed.RamGiB)
	specsObj.SetString("motherboard", probed.Motherboard)
	specsObj.SetString("bios", probed.BIOS)
	specsObj.SetString("ipmi", "Local Bus")
	specsObj.SetString("ecc", "Non-ECC")
	specsObj.SetString("secureBoot", probed.SecureBoot)
	specsObj.SetString("iommu", probed.IOMMU)
	specsBytes, _ := specsObj.MarshalJSON()
	nodeJSON.Set("specs", specsBytes)

	nodeBytes, err := nodeJSON.MarshalJSON()
	if err != nil {
		return fmt.Errorf("localhost auto-detection: marshal node: %w", err)
	}

	rec := hostcache.NodeRecord{
		ID:        "node-localhost",
		Name:      hostname + " (local)",
		ShortName: hostname,
		Group:     "tag:host",
		IP:        "127.0.0.1",
		Port:      22,
		SSHUser:   "core",
		Status:    "online",
		NodeJSON:  json.RawMessage(nodeBytes),
		UpdatedAt: now().UnixMilli(),
	}

	if err := store.UpsertHost(rec); err != nil {
		return fmt.Errorf("localhost auto-detection: upsert host: %w", err)
	}

	log.Printf("localhost auto-detection: registered local node %s", hostname)
	return nil
}
