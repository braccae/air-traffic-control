package nodeapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"air-traffic-control/backend/pkg/hostcache"
	"air-traffic-control/backend/pkg/tailscale"
)

// BaselineNodeSpecs holds the full specification for a grounded node.
type BaselineNodeSpecs struct {
	CPU         string `json:"cpu"`
	VCPUs       int    `json:"vcpus"`
	RAMGiB      int    `json:"ramGiB"`
	Motherboard string `json:"motherboard"`
	BIOS        string `json:"bios"`
	IPMI        string `json:"ipmi"`
	ECC         string `json:"ecc"`
	SecureBoot  string `json:"secureBoot"`
	IOMMU       string `json:"iommu"`
}

// BaselineNode represents a grounded baseline node definition from liveCluster.ts.
type BaselineNode struct {
	ID            string            `json:"id"`
	Name          string            `json:"name"`
	ShortName     string            `json:"shortName"`
	Group         string            `json:"group"`
	Datacenter    string            `json:"datacenter"`
	NodeType      string            `json:"nodeType"`
	Badge         string            `json:"badge"`
	BadgeColor    string            `json:"badgeColor"`
	IP            string            `json:"ip"`
	Port          int               `json:"port"`
	SSHUser       string            `json:"sshUser"`
	AuthMethod    string            `json:"authMethod"`
	Tags          []string          `json:"tags"`
	SSHConnected  bool              `json:"sshConnected"`
	RTTMs         string            `json:"rttMs"`
	TailscaleIP   string            `json:"tailscaleIp"`
	TailscaleHost string            `json:"tailscaleHost"`
	OSDescription string            `json:"osDescription"`
	Status        string            `json:"status"`
	Uptime        string            `json:"uptime"`
	HasGlances    bool              `json:"hasGlances"`
	HasCockpit    bool              `json:"hasCockpit"`
	HasPodman     bool              `json:"hasPodman"`
	HasRestic     bool              `json:"hasRestic"`
	Specs         BaselineNodeSpecs `json:"specs"`
}

// LIVE_TAILNET_NODES matches src/utils/liveCluster.ts lines 27-324 verbatim.
var LiveTailnetNodes = []BaselineNode{
	{
		ID:            "node-likes-it-cold",
		Name:          "likes-it-cold",
		ShortName:     "likes-it-cold",
		Group:         "tag:host",
		Datacenter:    "leo-banjo.ts.net",
		NodeType:      "linux",
		Badge:         "SSH",
		BadgeColor:    "text-[#8cd6cb]",
		IP:            "192.168.8.249",
		Port:          22,
		SSHUser:       "core",
		AuthMethod:    "ssh-ed25519 (ssh-agent)",
		Tags:          []string{"tag:host"},
		SSHConnected:  true,
		RTTMs:         "1.8ms",
		TailscaleIP:   "100.105.71.49",
		TailscaleHost: "likes-it-cold.leo-banjo.ts.net",
		OSDescription: "AlmaLinux 10.2 (Lavender Lion)",
		Status:        "online",
		Uptime:        "3d 14h",
		HasGlances:    false,
		HasCockpit:    true,
		HasPodman:     true,
		HasRestic:     true,
		Specs: BaselineNodeSpecs{
			CPU:         "AMD Ryzen 5 PRO 2400G with Radeon Vega Graphics",
			VCPUs:       8,
			RAMGiB:      32,
			Motherboard: "Lenovo 3141 (ThinkCentre M725s)",
			BIOS:        "Lenovo M25KT2FA (2019-04-23)",
			IPMI:        "N/A (Desktop SFF)",
			ECC:         "Non-ECC DDR4",
			SecureBoot:  "Enabled",
			IOMMU:       "Enabled (AMD-Vi)",
		},
	},
	{
		ID:            "node-ping",
		Name:          "ping",
		ShortName:     "ping",
		Group:         "tag:host",
		Datacenter:    "leo-banjo.ts.net",
		NodeType:      "linux",
		Badge:         "SSH",
		BadgeColor:    "text-[#8cd6cb]",
		IP:            "100.73.87.87",
		Port:          22,
		SSHUser:       "core",
		AuthMethod:    "ssh-ed25519 (ssh-agent)",
		Tags:          []string{"tag:host"},
		SSHConnected:  true,
		RTTMs:         "2.1ms",
		TailscaleIP:   "100.73.87.87",
		TailscaleHost: "ping.leo-banjo.ts.net",
		OSDescription: "AlmaLinux 10.2 (Lavender Lion)",
		Status:        "online",
		Uptime:        "4d 14h",
		HasGlances:    false,
		HasCockpit:    true,
		HasPodman:     true,
		HasRestic:     true,
		Specs: BaselineNodeSpecs{
			CPU:         "Intel(R) Core(TM) i5-6500 CPU @ 3.20GHz",
			VCPUs:       4,
			RAMGiB:      8,
			Motherboard: "Dell 0Y7WYT (OptiPlex 7040)",
			BIOS:        "Dell 1.24.0 (2022-07-14)",
			IPMI:        "Intel AMT",
			ECC:         "Non-ECC DDR4",
			SecureBoot:  "Enabled",
			IOMMU:       "Enabled (VT-d)",
		},
	},
	{
		ID:            "node-dev-pc",
		Name:          "dev-pc",
		ShortName:     "dev-pc",
		Group:         "tag:host",
		Datacenter:    "leo-banjo.ts.net",
		NodeType:      "linux",
		Badge:         "LOCAL",
		BadgeColor:    "text-[#b0c9ff]",
		IP:            "127.0.0.1",
		Port:          22,
		SSHUser:       "pants",
		AuthMethod:    "ssh-ed25519 (ssh-agent)",
		Tags:          []string{"tag:admin", "tag:host"},
		SSHConnected:  true,
		RTTMs:         "1.0ms",
		TailscaleIP:   "100.97.159.101",
		TailscaleHost: "dev-pc.leo-banjo.ts.net",
		OSDescription: "CachyOS Linux (6.19.11-1-cachyos)",
		Status:        "online",
		Uptime:        "1d 22h",
		HasGlances:    true,
		HasCockpit:    true,
		HasPodman:     true,
		HasRestic:     true,
		Specs: BaselineNodeSpecs{
			CPU:         "AMD Ryzen 5 5600X 6-Core Processor",
			VCPUs:       12,
			RAMGiB:      32,
			Motherboard: "ASUS ROG STRIX B450-F GAMING",
			BIOS:        "ASUS 4801",
			IPMI:        "Local Bus",
			ECC:         "Non-ECC",
			SecureBoot:  "Disabled",
			IOMMU:       "Enabled (AMD-Vi)",
		},
	},
	{
		ID:            "node-fred",
		Name:          "fred",
		ShortName:     "fred",
		Group:         "tag:host",
		Datacenter:    "leo-banjo.ts.net",
		NodeType:      "linux",
		Badge:         "TS-SSH",
		BadgeColor:    "text-[#8cd6cb]",
		IP:            "100.100.173.57",
		Port:          22,
		SSHUser:       "root",
		AuthMethod:    "tailscale-ssh",
		Tags:          []string{"tag:host"},
		SSHConnected:  true,
		RTTMs:         "3.4ms",
		TailscaleIP:   "100.100.173.57",
		TailscaleHost: "fred.leo-banjo.ts.net",
		OSDescription: "Debian GNU/Linux 13 (trixie)",
		Status:        "online",
		Uptime:        "37d 04h",
		HasGlances:    true,
		HasCockpit:    true,
		HasPodman:     true,
		HasRestic:     true,
		Specs: BaselineNodeSpecs{
			CPU:         "Common KVM processor",
			VCPUs:       4,
			RAMGiB:      4,
			Motherboard: "QEMU Standard PC (i440FX + PIIX)",
			BIOS:        "SeaBIOS",
			IPMI:        "Proxmox VE",
			ECC:         "Host Managed",
			SecureBoot:  "Disabled",
			IOMMU:       "N/A",
		},
	},
	{
		ID:            "node-the-freezer",
		Name:          "the-freezer",
		ShortName:     "the-freezer",
		Group:         "tag:host",
		Datacenter:    "leo-banjo.ts.net",
		NodeType:      "linux",
		Badge:         "MGR",
		BadgeColor:    "text-[#d9b9ff]",
		IP:            "192.168.8.3",
		Port:          22,
		SSHUser:       "core",
		AuthMethod:    "ssh-ed25519 (ssh-agent)",
		Tags:          []string{"tag:host", "tag:manager"},
		SSHConnected:  true,
		RTTMs:         "1.5ms",
		TailscaleIP:   "100.115.75.78",
		TailscaleHost: "the-freezer.leo-banjo.ts.net",
		OSDescription: "AlmaLinux 10.2 (Lavender Lion)",
		Status:        "online",
		Uptime:        "14d 08h",
		HasGlances:    false,
		HasCockpit:    true,
		HasPodman:     true,
		HasRestic:     true,
		Specs: BaselineNodeSpecs{
			CPU:         "AMD Ryzen 7 5700X 8-Core Processor",
			VCPUs:       16,
			RAMGiB:      64,
			Motherboard: "ASUS PRIME B550-PLUS",
			BIOS:        "ASUS UEFI 3607 (2024-03-22)",
			IPMI:        "N/A (Tower ATX)",
			ECC:         "Non-ECC DDR4",
			SecureBoot:  "Enabled",
			IOMMU:       "Enabled (AMD-Vi)",
		},
	},
	{
		ID:            "node-chuck",
		Name:          "chuck",
		ShortName:     "chuck",
		Group:         "tag:host",
		Datacenter:    "leo-banjo.ts.net",
		NodeType:      "linux",
		Badge:         "HOST",
		BadgeColor:    "text-[#8d909b]",
		IP:            "100.114.97.98",
		Port:          22,
		SSHUser:       "core",
		AuthMethod:    "ssh-ed25519 (ssh-agent)",
		Tags:          []string{"tag:host"},
		SSHConnected:  false,
		RTTMs:         "2.8ms",
		TailscaleIP:   "100.114.97.98",
		TailscaleHost: "chuck.leo-banjo.ts.net",
		OSDescription: "Linux (tag:host)",
		Status:        "online",
		Uptime:        "9d 12h",
		HasGlances:    true,
		HasCockpit:    true,
		HasPodman:     true,
		HasRestic:     true,
		Specs: BaselineNodeSpecs{
			CPU:         "x86_64 Host Node",
			VCPUs:       4,
			RAMGiB:      8,
			Motherboard: "Homelab Host",
			BIOS:        "UEFI",
			IPMI:        "N/A",
			ECC:         "Standard",
			SecureBoot:  "Enabled",
			IOMMU:       "Enabled",
		},
	},
	{
		ID:            "node-larry",
		Name:          "larry",
		ShortName:     "larry",
		Group:         "tag:host",
		Datacenter:    "leo-banjo.ts.net",
		NodeType:      "linux",
		Badge:         "HOST",
		BadgeColor:    "text-[#8d909b]",
		IP:            "192.168.8.144",
		Port:          22,
		SSHUser:       "core",
		AuthMethod:    "ssh-ed25519 (ssh-agent)",
		Tags:          []string{"tag:host"},
		SSHConnected:  false,
		RTTMs:         "1.9ms",
		TailscaleIP:   "100.107.167.19",
		TailscaleHost: "larry.leo-banjo.ts.net",
		OSDescription: "Linux (tag:host)",
		Status:        "online",
		Uptime:        "11d 03h",
		HasGlances:    true,
		HasCockpit:    true,
		HasPodman:     true,
		HasRestic:     true,
		Specs: BaselineNodeSpecs{
			CPU:         "x86_64 Host Node",
			VCPUs:       4,
			RAMGiB:      8,
			Motherboard: "Homelab Host",
			BIOS:        "UEFI",
			IPMI:        "192.168.8.144",
			ECC:         "Standard",
			SecureBoot:  "Enabled",
			IOMMU:       "Enabled",
		},
	},
	{
		ID:            "node-orangepizero3",
		Name:          "orangepizero3",
		ShortName:     "orangepizero3",
		Group:         "tag:host",
		Datacenter:    "leo-banjo.ts.net",
		NodeType:      "linux",
		Badge:         "ARM64",
		BadgeColor:    "text-[#ffd97d]",
		IP:            "100.65.100.14",
		Port:          22,
		SSHUser:       "core",
		AuthMethod:    "ssh-ed25519 (ssh-agent)",
		Tags:          []string{"tag:host"},
		SSHConnected:  false,
		RTTMs:         "4.2ms",
		TailscaleIP:   "100.65.100.14",
		TailscaleHost: "orangepizero3.leo-banjo.ts.net",
		OSDescription: "Debian / Armbian (Allwinner H618 ARM64)",
		Status:        "online",
		Uptime:        "21d 06h",
		HasGlances:    true,
		HasCockpit:    false,
		HasPodman:     true,
		HasRestic:     false,
		Specs: BaselineNodeSpecs{
			CPU:         "Allwinner H618 Quad-Core Cortex-A53",
			VCPUs:       4,
			RAMGiB:      4,
			Motherboard: "Orange Pi Zero 3 SBC",
			BIOS:        "U-Boot ARM64",
			IPMI:        "GPIO / Serial",
			ECC:         "Non-ECC LPDDR4",
			SecureBoot:  "Disabled",
			IOMMU:       "N/A",
		},
	},
}

// BaselineNodeObjects returns the 8 grounded baseline nodes as ordered *Objects.
func BaselineNodeObjects() []*Object {
	objs := make([]*Object, 0, len(LiveTailnetNodes))
	for _, b := range LiveTailnetNodes {
		data, err := json.Marshal(b)
		if err != nil {
			continue
		}
		obj, err := ParseObject(data)
		if err != nil {
			continue
		}
		ensureNodeNormalized(obj, &b)
		objs = append(objs, obj)
	}
	return objs
}

func baselineRecords() []hostcache.NodeRecord {
	recs := make([]hostcache.NodeRecord, 0, len(LiveTailnetNodes))
	for _, b := range LiveTailnetNodes {
		data, err := json.Marshal(b)
		if err != nil {
			continue
		}
		recs = append(recs, hostcache.NodeRecord{
			ID:            b.ID,
			Name:          b.Name,
			ShortName:     b.ShortName,
			Group:         b.Group,
			IP:            b.IP,
			Port:          b.Port,
			SSHUser:       b.SSHUser,
			TailscaleIP:   b.TailscaleIP,
			TailscaleHost: b.TailscaleHost,
			Status:        b.Status,
			NodeJSON:      json.RawMessage(data),
		})
	}
	return recs
}

func ensureNodeNormalized(obj *Object, liveDef *BaselineNode) {
	if obj == nil {
		return
	}

	name, _ := obj.GetString("name")
	id, _ := obj.GetString("id")

	// shortName
	shortName, ok := obj.GetString("shortName")
	if !ok || strings.TrimSpace(shortName) == "" {
		if liveDef != nil && liveDef.ShortName != "" {
			shortName = liveDef.ShortName
		} else if name != "" {
			shortName = strings.Split(name, " ")[0]
		} else {
			shortName = id
		}
		obj.SetString("shortName", shortName)
	}

	// badge & badgeColor
	if b, ok := obj.GetString("badge"); !ok || strings.TrimSpace(b) == "" {
		if liveDef != nil && liveDef.Badge != "" {
			obj.SetString("badge", liveDef.Badge)
		} else {
			obj.SetString("badge", "SSH")
		}
	}
	if bc, ok := obj.GetString("badgeColor"); !ok || strings.TrimSpace(bc) == "" {
		if liveDef != nil && liveDef.BadgeColor != "" {
			obj.SetString("badgeColor", liveDef.BadgeColor)
		} else {
			obj.SetString("badgeColor", "text-[#8cd6cb]")
		}
	}

	// sshUser
	if u, ok := obj.GetString("sshUser"); !ok || strings.TrimSpace(u) == "" {
		if liveDef != nil && liveDef.SSHUser != "" {
			obj.SetString("sshUser", liveDef.SSHUser)
		} else {
			obj.SetString("sshUser", "core")
		}
	}

	// tags
	// Leave tags alone when the node has none: inventing `tag:host` from the
	// display group put hosts into Tailscale groups they never carried.
	if rawTags, ok := obj.Get("tags"); !ok || string(rawTags) == "null" {
		if liveDef != nil && len(liveDef.Tags) > 0 {
			_ = obj.SetJSON("tags", liveDef.Tags)
		} else {
			_ = obj.SetJSON("tags", []string{})
		}
	}

	// specs
	var specsObj *Object
	if rawSpecs, ok := obj.Get("specs"); ok && len(rawSpecs) > 0 && string(rawSpecs) != "null" {
		if parsed, err := ParseObject(rawSpecs); err == nil && parsed != nil {
			specsObj = parsed
		}
	}
	if specsObj == nil {
		specsObj = NewObject()
	}

	// If liveDef is present, explicitly mirror live specs fields
	if liveDef != nil {
		specsObj.SetString("cpu", liveDef.Specs.CPU)
		_ = specsObj.SetJSON("vcpus", liveDef.Specs.VCPUs)
		_ = specsObj.SetJSON("ramGiB", liveDef.Specs.RAMGiB)
		specsObj.SetString("motherboard", liveDef.Specs.Motherboard)
		specsObj.SetString("bios", liveDef.Specs.BIOS)
		specsObj.SetString("ipmi", liveDef.Specs.IPMI)
		specsObj.SetString("ecc", liveDef.Specs.ECC)
		specsObj.SetString("secureBoot", liveDef.Specs.SecureBoot)
		specsObj.SetString("iommu", liveDef.Specs.IOMMU)
	} else {
		// Non-matching node: guarantee safe non-empty non-null defaults
		if s, ok := specsObj.GetString("cpu"); !ok || strings.TrimSpace(s) == "" {
			specsObj.SetString("cpu", "x86_64 Host Node")
		}
		if raw, ok := specsObj.Get("vcpus"); !ok || parsePositiveInt(raw) <= 0 {
			_ = specsObj.SetJSON("vcpus", 4)
		}
		if raw, ok := specsObj.Get("ramGiB"); !ok || parsePositiveInt(raw) <= 0 {
			_ = specsObj.SetJSON("ramGiB", 8)
		}
		if s, ok := specsObj.GetString("motherboard"); !ok || strings.TrimSpace(s) == "" {
			specsObj.SetString("motherboard", "Standard Server Platform")
		}
		if s, ok := specsObj.GetString("bios"); !ok || strings.TrimSpace(s) == "" {
			specsObj.SetString("bios", "UEFI")
		}
		if s, ok := specsObj.GetString("ipmi"); !ok || strings.TrimSpace(s) == "" {
			specsObj.SetString("ipmi", "N/A")
		}
		if s, ok := specsObj.GetString("ecc"); !ok || strings.TrimSpace(s) == "" {
			specsObj.SetString("ecc", "Standard")
		}
		if s, ok := specsObj.GetString("secureBoot"); !ok || strings.TrimSpace(s) == "" {
			specsObj.SetString("secureBoot", "Enabled")
		}
		if s, ok := specsObj.GetString("iommu"); !ok || strings.TrimSpace(s) == "" {
			specsObj.SetString("iommu", "Enabled")
		}
	}

	specsBytes, err := specsObj.MarshalJSON()
	if err == nil {
		obj.Set("specs", specsBytes)
	}
}

// SeedAndSyncBaseline seeds the store if empty and synchronizes live hardware specs.
func (a *API) SeedAndSyncBaseline() error {
	if a.store == nil {
		return nil
	}

	base := baselineRecords()
	if _, err := a.store.SeedHostsIfEmpty(base); err != nil {
		return fmt.Errorf("nodeapi: seed baseline hosts: %w", err)
	}

	existing, err := a.store.GetAllHosts()
	if err != nil {
		return fmt.Errorf("nodeapi: get all hosts: %w", err)
	}

	updatedRecs := make([]hostcache.NodeRecord, 0, len(existing))
	for _, rec := range existing {
		var obj *Object
		if len(rec.NodeJSON) > 0 {
			if parsed, err := ParseObject(rec.NodeJSON); err == nil {
				obj = parsed
			}
		}
		if obj == nil {
			data, _ := json.Marshal(rec)
			obj, _ = ParseObject(data)
		}
		if obj == nil {
			obj = NewObject()
		}

		id, _ := obj.GetString("id")
		shortName, _ := obj.GetString("shortName")

		var match *BaselineNode
		for i := range LiveTailnetNodes {
			l := &LiveTailnetNodes[i]
			if l.ID == id || (shortName != "" && l.ShortName == shortName) {
				match = l
				break
			}
		}

		if match != nil {
			if match.OSDescription != "" {
				obj.SetString("osDescription", match.OSDescription)
			}
			_ = obj.SetJSON("hasGlances", match.HasGlances)
		}

		ensureNodeNormalized(obj, match)

		updatedRec, err := nodeRecordFromObject(obj)
		if err == nil {
			updatedRecs = append(updatedRecs, updatedRec)
		}
	}

	if len(updatedRecs) > 0 {
		if err := a.store.UpsertHosts(updatedRecs); err != nil {
			return fmt.Errorf("nodeapi: upsert baseline synced hosts: %w", err)
		}
	}

	return nil
}

// HandleNodes dispatches GET /api/nodes and POST /api/nodes.
func (a *API) HandleNodes(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		a.HandleList(w, r)
	case http.MethodPost:
		a.HandleCreate(w, r)
	default:
		a.notFound(w, r)
	}
}

// HandleList handles GET /api/nodes with optional ?refresh=true.
func (a *API) HandleList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		a.notFound(w, r)
		return
	}

	if a.store == nil {
		writeJSON(w, http.StatusOK, BaselineNodeObjects())
		return
	}

	if r.URL.Query().Get("refresh") == "true" {
		a.refreshFromTailscale()
	}

	recs, err := a.store.GetAllHosts()
	if err != nil {
		writeJSON(w, http.StatusOK, BaselineNodeObjects())
		return
	}

	objs := make([]*Object, 0, len(recs))
	for _, rec := range recs {
		var obj *Object
		if len(rec.NodeJSON) > 0 {
			if parsed, err := ParseObject(rec.NodeJSON); err == nil {
				obj = parsed
			}
		}
		if obj == nil {
			data, _ := json.Marshal(rec)
			obj, _ = ParseObject(data)
		}
		if obj == nil {
			continue
		}

		id, _ := obj.GetString("id")
		shortName, _ := obj.GetString("shortName")
		var match *BaselineNode
		for i := range LiveTailnetNodes {
			l := &LiveTailnetNodes[i]
			if l.ID == id || (shortName != "" && l.ShortName == shortName) {
				match = l
				break
			}
		}

		ensureNodeNormalized(obj, match)
		objs = append(objs, obj)
	}

	if len(objs) == 0 {
		objs = BaselineNodeObjects()
	}

	writeJSON(w, http.StatusOK, objs)
}

func normalizeTag(tag string) string {
	return strings.TrimPrefix(strings.ToLower(strings.TrimSpace(tag)), "tag:")
}

func matchesTailscaleTag(peerTags []string, targetTag string) bool {
	normalizedTarget := normalizeTag(targetTag)
	if normalizedTarget == "" {
		return false
	}
	for _, t := range peerTags {
		if normalizeTag(t) == normalizedTarget {
			return true
		}
	}
	return false
}

func (a *API) refreshFromTailscale() {
	tsFunc := a.tailscaleStatus
	if tsFunc == nil {
		tsFunc = tailscale.GetLocalStatus
	}

	tsStatus, err := tsFunc()
	if err != nil || tsStatus == nil {
		return
	}

	recs, err := a.store.GetAllHosts()
	if err != nil {
		return
	}

	peersByHost := make(map[string]tailscale.PeerStatus)
	if tsStatus.Self.HostName != "" {
		selfPeer := tailscale.PeerStatus{
			ID:           tsStatus.Self.ID,
			HostName:     tsStatus.Self.HostName,
			DNSName:      tsStatus.Self.DNSName,
			TailscaleIPs: tsStatus.Self.TailscaleIPs,
			Addrs:        tsStatus.Self.Addrs,
			Tags:         tsStatus.Self.Tags,
			OS:           tsStatus.Self.OS,
			Online:       tsStatus.Self.Online,
		}
		selfDns := strings.TrimSuffix(tsStatus.Self.DNSName, ".")
		peersByHost[strings.ToLower(selfDns)] = selfPeer
		peersByHost[strings.ToLower(tsStatus.Self.HostName)] = selfPeer
	}

	for _, p := range tsStatus.Peer {
		dns := strings.TrimSuffix(p.DNSName, ".")
		short := strings.Split(dns, ".")[0]
		if short == "" {
			short = p.HostName
		}
		peersByHost[strings.ToLower(dns)] = p
		peersByHost[strings.ToLower(short)] = p
		peersByHost[strings.ToLower(p.HostName)] = p
	}

	currentNodes := make([]*Object, 0, len(recs))
	for _, rec := range recs {
		var obj *Object
		if len(rec.NodeJSON) > 0 {
			if parsed, err := ParseObject(rec.NodeJSON); err == nil {
				obj = parsed
			}
		}
		if obj == nil {
			data, _ := json.Marshal(rec)
			obj, _ = ParseObject(data)
		}
		if obj == nil {
			continue
		}

		tsHost, _ := obj.GetString("tailscaleHost")
		shortName, _ := obj.GetString("shortName")
		name, _ := obj.GetString("name")

		peer, found := peersByHost[strings.ToLower(tsHost)]
		if !found {
			peer, found = peersByHost[strings.ToLower(shortName)]
		}
		if !found {
			peer, found = peersByHost[strings.ToLower(name)]
		}

		if found {
			tsIp, _ := obj.GetString("tailscaleIp")
			if len(peer.TailscaleIPs) > 0 {
				tsIp = peer.TailscaleIPs[0]
			}
			obj.SetString("tailscaleIp", tsIp)

			dns := strings.TrimSuffix(peer.DNSName, ".")
			if dns != "" {
				obj.SetString("tailscaleHost", dns)
			}

			isSelf := strings.EqualFold(peer.HostName, tsStatus.Self.HostName)

			lanIp, _ := obj.GetString("ip")
			if !isSelf && len(peer.Addrs) > 0 {
				addrIp := strings.Split(peer.Addrs[0], ":")[0]
				if addrIp != "" && !strings.HasPrefix(addrIp, "127.") {
					lanIp = addrIp
				}
			}
			obj.SetString("ip", lanIp)
			if peer.Online || isSelf {
				obj.SetString("status", "online")
			} else {
				obj.SetString("status", "stopped")
			}

			if len(peer.Tags) > 0 {
				_ = obj.SetJSON("tags", peer.Tags)
			}
		}

		ensureNodeNormalized(obj, nil)
		currentNodes = append(currentNodes, obj)
	}

	// Dynamic Tag Inventory Chunks sync (server.ts lines 204-217)
	chunks, _ := a.store.GetAllTagChunks()
	hasDynamicNodes := false
	for _, n := range currentNodes {
		if raw, ok := n.Get("isDynamicTagMember"); ok && isTruthy(raw) {
			hasDynamicNodes = true
			break
		}
	}

	if len(chunks) > 0 || hasDynamicNodes {
		var allPeers []tailscale.PeerStatus
		if tsStatus.Self.HostName != "" {
			allPeers = append(allPeers, tailscale.PeerStatus{
				ID:           tsStatus.Self.ID,
				HostName:     tsStatus.Self.HostName,
				DNSName:      tsStatus.Self.DNSName,
				TailscaleIPs: tsStatus.Self.TailscaleIPs,
				Addrs:        tsStatus.Self.Addrs,
				Tags:         tsStatus.Self.Tags,
				OS:           tsStatus.Self.OS,
				Online:       tsStatus.Self.Online,
			})
		}
		for _, p := range tsStatus.Peer {
			allPeers = append(allPeers, p)
		}

		handledIds := make(map[string]bool)
		processedKeys := make(map[string]bool)
		var added []*Object

		// Derive the real tailnet shortname (e.g. leo-banjo.ts.net) from the
		// controller's own MagicDNS name; never hardcode one.
		tailnet := tailnetSuffix(tsStatus.Self.DNSName)

		for _, chunk := range chunks {
			chunkUser := strings.TrimSpace(chunk.SSHUser)
			if chunkUser == "" {
				chunkUser = "core"
			}
			chunkAuth := strings.TrimSpace(chunk.AuthMethod)
			if chunkAuth == "" {
				chunkAuth = "tailscale-ssh"
			}
			chunkTailnet := tailnet

			// applyChunkAuth writes the chunk's credential identity onto a
			// dynamic node so runtime resolution can decrypt chunk.VaultSecretID.
			applyChunkAuth := func(n *Object) {
				n.SetString("sshUser", chunkUser)
				n.SetString("authMethod", chunkAuth)
				if chunk.VaultSecretID != "" {
					n.SetString("vaultSecretId", chunk.VaultSecretID)
				} else {
					n.Delete("vaultSecretId")
				}
				if chunkTailnet != "" {
					n.SetString("datacenter", chunkTailnet)
				}
			}

			for _, peer := range allPeers {
				if !matchesTailscaleTag(peer.Tags, chunk.Tag) {
					continue
				}

				peerKey := peer.ID
				if peerKey == "" {
					peerKey = peer.HostName
				}
				if peerKey != "" && processedKeys[peerKey] {
					continue
				}
				if peerKey != "" {
					processedKeys[peerKey] = true
				}

				// Find matching node
				var existing *Object
				for _, n := range currentNodes {
					nID, _ := n.GetString("id")
					nHost, _ := n.GetString("tailscaleHost")
					nShort, _ := n.GetString("shortName")
					nName, _ := n.GetString("name")
					pDns := strings.TrimSuffix(peer.DNSName, ".")

					if (peer.ID != "" && nID == peer.ID) ||
						(peer.HostName != "" && nID == peer.HostName) ||
						(pDns != "" && strings.EqualFold(nHost, pDns)) ||
						(peer.HostName != "" && (strings.EqualFold(nShort, peer.HostName) || strings.EqualFold(nName, peer.HostName))) {
						existing = n
						break
					}
				}

				pDns := strings.TrimSuffix(peer.DNSName, ".")
				pHost := peer.HostName
				peerTsIp := ""
				if len(peer.TailscaleIPs) > 0 {
					peerTsIp = peer.TailscaleIPs[0]
				}

				targetStatus := "stopped"
				if peer.Online || strings.EqualFold(peer.HostName, tsStatus.Self.HostName) {
					targetStatus = "online"
				}

				if existing != nil {
					exID, _ := existing.GetString("id")
					handledIds[exID] = true

					rawDyn, _ := existing.Get("isDynamicTagMember")
					if !isTruthy(rawDyn) {
						existing.SetString("status", targetStatus)
						_ = existing.SetJSON("sshConnected", targetStatus == "online")
					} else {
						existing.SetString("group", chunk.Tag)
						existing.SetString("status", targetStatus)
						_ = existing.SetJSON("sshConnected", targetStatus == "online")
						applyChunkAuth(existing)
						if peerTsIp != "" {
							existing.SetString("tailscaleIp", peerTsIp)
						}
						if pDns != "" {
							existing.SetString("tailscaleHost", pDns)
						}
					}
				} else {
					// Create new dynamic node
					candidateId := peer.ID
					if candidateId == "" {
						candidateId = peer.HostName
					}
					if candidateId == "" {
						candidateId = fmt.Sprintf("node-dynamic-%d", time.Now().UnixMilli())
					}

					newNode := NewObject()
					newNode.SetString("id", candidateId)
					newNode.SetString("name", pHost)
					newNode.SetString("shortName", pHost)
					newNode.SetString("group", chunk.Tag)
					newNode.SetString("nodeType", "linux")
					newNode.SetString("badge", "SSH")
					newNode.SetString("badgeColor", "text-[#8cd6cb]")
					newNode.SetString("ip", peerTsIp)
					_ = newNode.SetJSON("port", 22)
					_ = newNode.SetJSON("tags", []string{chunk.Tag})
					_ = newNode.SetJSON("sshConnected", targetStatus == "online")
					newNode.SetString("tailscaleIp", peerTsIp)
					newNode.SetString("tailscaleHost", pDns)
					newNode.SetString("osDescription", "Linux (Tailscale Dynamic)")
					newNode.SetString("status", targetStatus)
					newNode.SetString("uptime", "--")
					_ = newNode.SetJSON("hasGlances", false)
					_ = newNode.SetJSON("hasCockpit", false)
					_ = newNode.SetJSON("hasPodman", false)
					_ = newNode.SetJSON("hasRestic", false)
					_ = newNode.SetJSON("isDynamicTagMember", true)
					newNode.SetString("followedTag", chunk.Tag)
					applyChunkAuth(newNode)

					ensureNodeNormalized(newNode, nil)
					handledIds[candidateId] = true
					added = append(added, newNode)
				}
			}
		}

		filteredNodes := make([]*Object, 0, len(currentNodes)+len(added))
		for _, n := range currentNodes {
			nID, _ := n.GetString("id")
			rawDyn, _ := n.Get("isDynamicTagMember")
			if isTruthy(rawDyn) && !handledIds[nID] {
				_, _ = a.store.DeleteHost(nID)
				continue
			}
			filteredNodes = append(filteredNodes, n)
		}
		filteredNodes = append(filteredNodes, added...)
		currentNodes = filteredNodes
	}

	updatedRecs := make([]hostcache.NodeRecord, 0, len(currentNodes))
	for _, n := range currentNodes {
		rec, err := nodeRecordFromObject(n)
		if err == nil {
			updatedRecs = append(updatedRecs, rec)
		}
	}
	_ = a.store.UpsertHosts(updatedRecs)
}

// tailnetSuffix extracts the MagicDNS suffix (e.g. "leo-banjo.ts.net") from a
// fully-qualified node DNS name. Returns "" when the name is not fully
// qualified, so callers never invent a tailnet that does not exist.
func tailnetSuffix(dnsName string) string {
	clean := strings.TrimSuffix(strings.TrimSpace(dnsName), ".")
	if i := strings.Index(clean, "."); i >= 0 && i+1 < len(clean) {
		return clean[i+1:]
	}
	return ""
}
