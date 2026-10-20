package readonlyapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// interfacesCmd is the exact command server.ts runs for GET /api/nodes/:id/interfaces.
const interfacesCmd = "ip -j addr 2>/dev/null; echo \"===LINK===\"; ip -j -s link 2>/dev/null"

// ipAddr is one entry from `ip -j addr`.
type ipAddr struct {
	IfName   string `json:"ifname"`
	AddrInfo []struct {
		Family    string `json:"family"`
		Local     string `json:"local"`
		PrefixLen int    `json:"prefixlen"`
	} `json:"addr_info"`
}

// ipLink is one entry from `ip -j -s link`.
type ipLink struct {
	IfName    string   `json:"ifname"`
	Address   string   `json:"address"`
	MTU       int      `json:"mtu"`
	OperState string   `json:"operstate"`
	Flags     []string `json:"flags"`
	Stats64   *struct {
		Rx struct {
			Bytes int64 `json:"bytes"`
		} `json:"rx"`
		Tx struct {
			Bytes int64 `json:"bytes"`
		} `json:"tx"`
	} `json:"stats64"`
}

// InterfaceItem mirrors the Express interface projection.
type InterfaceItem struct {
	Name   string `json:"name"`
	Type   string `json:"type"`
	IP     string `json:"ip"`
	MAC    string `json:"mac"`
	Speed  string `json:"speed"`
	Rx     string `json:"rx"`
	Tx     string `json:"tx"`
	Status string `json:"status"`
	MTU    int    `json:"mtu"`
}

// formatIfaceBytes ports the formatBytes helper in the Express interfaces handler.
func formatIfaceBytes(b int64) string {
	switch {
	case b >= 1073741824:
		return fmt.Sprintf("%.2f GiB", float64(b)/1073741824)
	case b >= 1048576:
		return fmt.Sprintf("%.1f MiB", float64(b)/1048576)
	case b >= 1024:
		return fmt.Sprintf("%.0f KiB", float64(b)/1024)
	default:
		return fmt.Sprintf("%d B", b)
	}
}

// handleInterfaces ports GET /api/nodes/:id/interfaces (list only).
func (a *API) handleInterfaces(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	node, err := a.findNode(r.PathValue("id"))
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"success": false, "error": "Failed to probe interfaces"})
		return
	}

	if a.sectionIsFresh(node.ID, "interfaces", r.URL.Query().Get("refresh") == "true") {
		if a.sectionCachedResponse(w, node.ID, "interfaces") {
			return
		}
	}

	_, runner, err := a.factsAndRunner(r.Context(), node.ID)
	if err != nil {
		if a.sectionCachedResponse(w, node.ID, "interfaces") {
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": false, "error": err.Error()})
		return
	}

	out, err := runNodeCommand(r.Context(), runner, interfacesCmd)
	if err != nil {
		if a.sectionCachedResponse(w, node.ID, "interfaces") {
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": false, "error": "Failed to probe interfaces"})
		return
	}

	addrRaw, linkRaw := splitMarker(out, "===LINK===")

	var addrs []ipAddr
	_ = json.Unmarshal([]byte(strings.TrimSpace(addrRaw)), &addrs)

	var links []ipLink
	_ = json.Unmarshal([]byte(strings.TrimSpace(linkRaw)), &links)

	ifaces := make([]InterfaceItem, 0, len(links))
	for _, link := range links {
		name := link.IfName

		var addrObj *ipAddr
		for i := range addrs {
			if addrs[i].IfName == name {
				addrObj = &addrs[i]
				break
			}
		}

		ipStr := "No IP assigned"
		if addrObj != nil {
			for _, info := range addrObj.AddrInfo {
				if info.Family == "inet" {
					ipStr = fmt.Sprintf("%s/%d", info.Local, info.PrefixLen)
					break
				}
			}
			if ipStr == "No IP assigned" {
				for _, info := range addrObj.AddrInfo {
					if info.Family == "inet6" {
						ipStr = fmt.Sprintf("%s/%d", info.Local, info.PrefixLen)
						break
					}
				}
			}
		}

		var rxBytes, txBytes int64
		if link.Stats64 != nil {
			rxBytes = link.Stats64.Rx.Bytes
			txBytes = link.Stats64.Tx.Bytes
		}

		ifaceType := "Physical Ethernet"
		speed := "1000 Mbps (Full Duplex)"
		switch {
		case name == "lo":
			ifaceType = "Loopback"
			speed = "Virtual"
		case strings.HasPrefix(name, "tailscale"):
			ifaceType = "WireGuard Tunnel"
			speed = "10 Gbps (Mesh)"
		case strings.Contains(name, "."):
			ifaceType = "802.1Q VLAN"
			speed = "Sub-interface"
		case strings.HasPrefix(name, "docker") || strings.HasPrefix(name, "br-") || strings.HasPrefix(name, "podman"):
			ifaceType = "Container Bridge"
			speed = "Virtual"
		case strings.HasPrefix(name, "veth"):
			ifaceType = "Container veth peer"
			speed = "Virtual"
		}

		isUp := false
		for _, f := range link.Flags {
			if f == "UP" {
				isUp = true
				break
			}
		}
		isCarrier := link.OperState == "UP" || link.OperState == "UNKNOWN"

		status := "DOWN"
		if isUp {
			if isCarrier {
				status = "UP / CARRIER"
			} else {
				status = "UP / NO-CARRIER"
			}
		}

		mtu := link.MTU
		if mtu == 0 {
			mtu = 1500
		}

		mac := link.Address
		if mac == "" {
			mac = "N/A"
		}

		ifaces = append(ifaces, InterfaceItem{
			Name:   name,
			Type:   ifaceType,
			IP:     ipStr,
			MAC:    mac,
			Speed:  speed,
			Rx:     formatIfaceBytes(rxBytes),
			Tx:     formatIfaceBytes(txBytes),
			Status: status,
			MTU:    mtu,
		})
	}

	result := map[string]any{"interfaces": ifaces}
	a.sectionResult(w, node.ID, "interfaces", result)
}
