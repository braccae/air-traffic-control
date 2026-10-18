package tailscale

import (
	"encoding/json"
	"fmt"
	"os/exec"
)

type Status struct {
	Self struct {
		ID           string   `json:"ID"`
		PublicKey    string   `json:"PublicKey"`
		HostName     string   `json:"HostName"`
		DNSName      string   `json:"DNSName"`
		TailscaleIPs []string `json:"TailscaleIPs"`
		Online       bool     `json:"Online"`
		ExitNode     bool     `json:"ExitNode"`
	} `json:"Self"`
	Peer map[string]struct {
		HostName     string   `json:"HostName"`
		DNSName      string   `json:"DNSName"`
		TailscaleIPs []string `json:"TailscaleIPs"`
		Online       bool     `json:"Online"`
		Active       bool     `json:"Active"`
		Relay        string   `json:"Relay"`
		RxBytes      int64    `json:"RxBytes"`
		TxBytes      int64    `json:"TxBytes"`
	} `json:"Peer"`
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
