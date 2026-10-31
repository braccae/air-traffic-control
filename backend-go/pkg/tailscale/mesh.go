package tailscale

import (
	"encoding/json"
	"fmt"
	"os/exec"
)

type PeerStatus struct {
	ID           string   `json:"ID"`
	HostName     string   `json:"HostName"`
	DNSName      string   `json:"DNSName"`
	TailscaleIPs []string `json:"TailscaleIPs"`
	Addrs        []string `json:"Addrs"`
	Tags         []string `json:"Tags"`
	OS           string   `json:"OS"`
	Online       bool     `json:"Online"`
	Active       bool     `json:"Active"`
	Relay        string   `json:"Relay"`
	RxBytes      int64    `json:"RxBytes"`
	TxBytes      int64    `json:"TxBytes"`
}

type Status struct {
	Self struct {
		ID           string   `json:"ID"`
		PublicKey    string   `json:"PublicKey"`
		HostName     string   `json:"HostName"`
		DNSName      string   `json:"DNSName"`
		TailscaleIPs []string `json:"TailscaleIPs"`
		Addrs        []string `json:"Addrs"`
		Tags         []string `json:"Tags"`
		OS           string   `json:"OS"`
		Online       bool     `json:"Online"`
		ExitNode     bool     `json:"ExitNode"`
	} `json:"Self"`
	// BackendState is the daemon's ipn state ("Running", "Stopped",
	// "NeedsLogin", ...). It is what tells the UI whether the mesh is actually
	// up, as opposed to merely having peers in the netmap.
	BackendState string                `json:"BackendState"`
	Peer         map[string]PeerStatus `json:"Peer"`
}

// GetLocalStatus queries tailscale status via the tailscale CLI or local socket
func GetLocalStatus() (*Status, error) {
	cmd := exec.Command("tailscale", "status", "--json")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to execute tailscale status: %w", err)
	}

	var status Status
	if err := json.Unmarshal(out, &status); err != nil {
		return nil, fmt.Errorf("failed to decode tailscale json: %w", err)
	}
	return &status, nil
}
