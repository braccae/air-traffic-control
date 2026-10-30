// Package hostcache owns persistence of the hosts-cache.sqlite store. This
// file adds local-host introspection so ATC can auto-detect the machine it
// runs on and register localhost as a managed node in the inventory without
// any SSH connection.
package hostcache

import (
	"encoding/json"
	"fmt"
	"os"
	"os/user"
	"runtime"
	"strconv"
	"strings"
)

// localNode is the JSON shape of a localhost entry in the inventory. It
// mirrors the TypeScript NodeItem so the frontend renders it identically to
// any other node, while the scalar columns of NodeRecord stay consistent.
type localNode struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	ShortName     string   `json:"shortName"`
	Group         string   `json:"group"`
	Datacenter    string   `json:"datacenter"`
	NodeType      string   `json:"nodeType"`
	Badge         string   `json:"badge"`
	BadgeColor    string   `json:"badgeColor"`
	IP            string   `json:"ip"`
	Port          int      `json:"port"`
	SSHUser       string   `json:"sshUser"`
	AuthMethod    string   `json:"authMethod"`
	Tags          []string `json:"tags"`
	SSHConnected  bool     `json:"sshConnected"`
	RTTMs         string   `json:"rttMs"`
	TailscaleIP   string   `json:"tailscaleIp"`
	TailscaleHost string   `json:"tailscaleHost"`
	OSDescription string   `json:"osDescription"`
	Status        string   `json:"status"`
	Uptime        string   `json:"uptime"`
	HasGlances    bool     `json:"hasGlances"`
	HasCockpit    bool     `json:"hasCockpit"`
	HasPodman     bool     `json:"hasPodman"`
	HasRestic     bool     `json:"hasRestic"`
	Specs         struct {
		CPU         string `json:"cpu"`
		VCPUs       int    `json:"vcpus"`
		RAMGiB      int    `json:"ramGiB"`
		Motherboard string `json:"motherboard"`
		BIOS        string `json:"bios"`
		IPMI        string `json:"ipmi"`
		ECC         string `json:"ecc"`
		SecureBoot  string `json:"secureBoot"`
		IOMMU       string `json:"iommu"`
	} `json:"specs"`
}

// DetectLocalhost resolves the machine this binary runs on and returns a
// NodeRecord representing it, so ATC can register localhost as a managed
// node in the inventory without an SSH connection. The id is constant
// ("localhost"), so repeated calls (e.g. on every server startup) upsert
// the same row instead of duplicating it.
func DetectLocalhost() NodeRecord {
	hostname, err := os.Hostname()
	if hostname == "" || err != nil {
		hostname = "localhost"
	}
	hostname = strings.TrimSpace(hostname)
	if hostname == "" {
		hostname = "localhost"
	}

	osDescription := osReleasePrettyName()
	if osDescription == "" {
		osDescription = fmt.Sprintf("%s (%s)", runtime.GOOS, runtime.GOARCH)
	}

	cpuModel := detectCpuModel()
	vcpus := runtime.NumCPU()
	if vcpus < 1 {
		vcpus = 1
	}

	ramGiB := detectRamGiB()

	uptime := detectUptime()

	sshUser := "core"
	if u, err := user.Current(); err == nil && u != nil && u.Username != "" {
		sshUser = u.Username
	}

	node := localNode{
		ID:            "localhost",
		Name:          hostname,
		ShortName:     hostname,
		Group:         "tag:host",
		Datacenter:    "localhost",
		NodeType:      "linux",
		Badge:         "LOCAL",
		BadgeColor:    "text-[#b0c9ff]",
		IP:            "127.0.0.1",
		Port:          22,
		SSHUser:       sshUser,
		AuthMethod:    "local-socket / ssh-agent",
		Tags:          []string{"tag:admin", "tag:host"},
		SSHConnected:  true,
		RTTMs:         "0.1ms",
		TailscaleIP:   "",
		TailscaleHost: "",
		OSDescription: osDescription,
		Status:        "online",
		Uptime:        uptime,
		HasGlances:    true,
		HasCockpit:    true,
		HasPodman:     true,
		HasRestic:     true,
	}
	node.Specs.CPU = cpuModel
	node.Specs.VCPUs = vcpus
	node.Specs.RAMGiB = ramGiB
	node.Specs.Motherboard = "Local Host Platform"
	node.Specs.BIOS = "UEFI"
	node.Specs.IPMI = "Local Bus"
	node.Specs.ECC = "Non-ECC"
	node.Specs.SecureBoot = "Enabled"
	node.Specs.IOMMU = "Enabled"

	blob, err := json.Marshal(node)
	if err != nil {
		// Fall back to a minimal blob so persistence still works.
		blob = []byte(fmt.Sprintf(`{"id":"localhost","name":%q,"shortName":%q,"group":"tag:host","datacenter":"localhost","nodeType":"linux","badge":"LOCAL","badgeColor":"text-[#b0c9ff]","ip":"127.0.0.1","port":22,"sshUser":%q,"authMethod":"local-socket / ssh-agent","tags":["tag:admin","tag:host"],"sshConnected":true,"rttMs":"0.1ms","tailscaleIp":"","tailscaleHost":"","osDescription":%q,"status":"online","uptime":%q,"hasGlances":true,"hasCockpit":true,"hasPodman":true,"hasRestic":true,"specs":{"cpu":%q,"vcpus":%d,"ramGiB":%d,"motherboard":"Local Host Platform","bios":"UEFI","ipmi":"Local Bus","ecc":"Non-ECC","secureBoot":"Enabled","iommu":"Enabled"}}`, hostname, hostname, sshUser, osDescription, uptime, cpuModel, vcpus, ramGiB))
	}

	return NodeRecord{
		ID:        "localhost",
		Name:      hostname,
		ShortName: hostname,
		Group:     "tag:host",
		IP:        "127.0.0.1",
		Port:      22,
		SSHUser:   sshUser,
		Status:    "online",
		NodeJSON:  json.RawMessage(blob),
	}
}

// osReleasePrettyName reads /etc/os-release (and a few fallbacks) and returns
// the PRETTY_NAME value, or "" if unavailable.
func osReleasePrettyName() string {
	candidates := []string{"/etc/os-release", "/usr/lib/os-release"}
	for _, p := range candidates {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "PRETTY_NAME=") {
				val := strings.TrimPrefix(line, "PRETTY_NAME=")
				val = strings.Trim(val, "\"")
				val = strings.TrimSpace(val)
				if val != "" {
					return val
				}
			}
		}
		// Fall back to NAME + VERSION_ID if PRETTY_NAME is absent.
		var name, version string
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "NAME=") {
				name = strings.Trim(strings.TrimPrefix(line, "NAME="), "\"")
			} else if strings.HasPrefix(line, "VERSION_ID=") {
				version = strings.Trim(strings.TrimPrefix(line, "VERSION_ID="), "\"")
			}
		}
		if name != "" {
			if version != "" {
				return name + " " + version
			}
			return name
		}
	}
	return ""
}

// detectCpuModel reads /proc/cpuinfo and returns the model name, or a
// generic fallback if unavailable.
func detectCpuModel() string {
	data, err := os.ReadFile("/proc/cpuinfo")
	if err != nil {
		return "Generic CPU (" + runtime.GOARCH + ")"
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(strings.ToLower(line), "model name") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				val := strings.TrimSpace(parts[1])
				if val != "" {
					return val
				}
			}
		}
	}
	return "Generic CPU (" + runtime.GOARCH + ")"
}

// parseMemTotal extracts the total memory in GiB from /proc/meminfo (or
// free -m) style content. It returns 1 on any parse failure.
func parseMemTotal(content string) int {
	if strings.TrimSpace(content) == "" {
		return 1
	}
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(strings.ToLower(line), "memtotal") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				kb, err := strconv.ParseFloat(fields[1], 64)
				if err == nil {
					gib := int(kb / (1024.0 * 1024.0))
					if gib < 1 {
						return 1
					}
					return gib
				}
			}
		}
	}
	// free -m style fallback: "Mem: 7867" (MiB)
	for _, line := range strings.Split(content, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && strings.EqualFold(fields[0], "mem:") {
			mb, err := strconv.ParseFloat(fields[1], 64)
			if err == nil {
				gib := int(mb / 1024.0)
				if gib < 1 {
					return 1
				}
				return gib
			}
		}
	}
	return 1
}

// detectRamGiB reads /proc/meminfo and returns total RAM in GiB.
func detectRamGiB() int {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 1
	}
	return parseMemTotal(string(data))
}

// parseUptimeSeconds extracts the first (total) uptime value from
// /proc/uptime style content. Returns 0 on failure.
func parseUptimeSeconds(content string) float64 {
	fields := strings.Fields(strings.TrimSpace(content))
	if len(fields) == 0 {
		return 0
	}
	sec, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0
	}
	return sec
}

// formatUptime renders a /proc/uptime value into a compact human-readable
// string matching the TS node conventions (e.g. "1d 22h", "8h 12m").
func formatUptime(seconds float64) string {
	if seconds <= 0 {
		return "unknown"
	}
	total := int(seconds)
	days := total / 86400
	hours := (total % 86400) / 3600
	mins := (total % 3600) / 60
	if days >= 1 {
		return fmt.Sprintf("%dd %dh", days, hours)
	}
	if hours >= 1 {
		return fmt.Sprintf("%dh %dm", hours, mins)
	}
	if mins >= 1 {
		return fmt.Sprintf("%dm", mins)
	}
	return "<1m"
}

// detectUptime reads /proc/uptime and formats it.
func detectUptime() string {
	data, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return "unknown"
	}
	return formatUptime(parseUptimeSeconds(string(data)))
}
