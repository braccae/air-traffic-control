package tailscale

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// tailscaleAPIBaseURL is a test seam for the Tailscale API.
var tailscaleAPIBaseURL = "https://api.tailscale.com/api/v2"

type apiDevice struct {
	ID                 string   `json:"id"`
	Name               string   `json:"name"`
	Hostname           string   `json:"hostname"`
	ConnectedToControl bool     `json:"connectedToControl"`
	OS                 string   `json:"os"`
	LastSeen           string   `json:"lastSeen"`
	Addresses          []string `json:"addresses"`
}

type apiErrorMessage struct {
	Message string `json:"message"`
}

type tailscaleAPIClient struct {
	apiKey  string
	tailnet string
	baseURL string
	http    *http.Client
}

func newTailscaleAPIClient(apiKey, tailnet string) *tailscaleAPIClient {
	tailnet = strings.TrimSpace(tailnet)
	if tailnet == "" {
		tailnet = "-"
	}
	return &tailscaleAPIClient{
		apiKey:  strings.TrimSpace(apiKey),
		tailnet: tailnet,
		baseURL: strings.TrimRight(tailscaleAPIBaseURL, "/"),
		http:    &http.Client{Timeout: 20 * time.Second},
	}
}

func (c *tailscaleAPIClient) do(ctx context.Context, method, path string, body any) ([]byte, error) {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message := strings.TrimSpace(string(raw))
		var apiErr apiErrorMessage
		if json.Unmarshal(raw, &apiErr) == nil && apiErr.Message != "" {
			message = apiErr.Message
		}
		if message == "" {
			message = http.StatusText(resp.StatusCode)
		}
		return nil, fmt.Errorf("tailscale api %s %s: %s (HTTP %d)", method, path, message, resp.StatusCode)
	}
	return raw, nil
}

func (c *tailscaleAPIClient) listDevices(ctx context.Context) ([]apiDevice, error) {
	raw, err := c.do(ctx, http.MethodGet, "/tailnet/"+url.PathEscape(c.tailnet)+"/devices", nil)
	if err != nil {
		return nil, err
	}
	var payload struct {
		Devices []apiDevice `json:"devices"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("decode tailscale devices: %w", err)
	}
	return payload.Devices, nil
}

func (c *tailscaleAPIClient) deleteDevice(ctx context.Context, id string) error {
	_, err := c.do(ctx, http.MethodDelete, "/device/"+url.PathEscape(id), nil)
	return err
}

func (c *tailscaleAPIClient) renameDevice(ctx context.Context, id, name string) error {
	_, err := c.do(ctx, http.MethodPost, "/device/"+url.PathEscape(id)+"/name", map[string]string{"name": name})
	return err
}

// DeviceRef is a non-secret summary of a device touched by cleanup.
type DeviceRef struct {
	ID       string `json:"id"`
	Hostname string `json:"hostname"`
	Name     string `json:"name"`
	Online   bool   `json:"online"`
	Reason   string `json:"reason,omitempty"`
}

// DeviceCleanupResult reports what the cleanup removed, skipped, or renamed.
type DeviceCleanupResult struct {
	Requested     string      `json:"requested"`
	CurrentName   string      `json:"currentName"`
	CurrentNodeID string      `json:"currentNodeId"`
	Deleted       []DeviceRef `json:"deleted"`
	Skipped       []DeviceRef `json:"skipped"`
	Renamed       bool        `json:"renamed"`
	Message       string      `json:"message"`
}

// CleanupDevices removes offline devices that still hold ATC's requested
// hostname, then renames the running node back to that name. It is a no-op
// unless a Tailscale API key is configured, and it never deletes a device that
// is still connected to control.
func (p *Plugin) CleanupDevices(ctx context.Context) (DeviceCleanupResult, error) {
	cfg := p.effectiveSettings()
	result := DeviceCleanupResult{Requested: cfg.Hostname}
	if cfg.APIKey == "" {
		return result, fmt.Errorf("tailscale API key is not configured; add it in Datacenter Settings -> ATC Tailscale")
	}

	st := p.SelfStatus(ctx)
	if st.NodeID == "" {
		return result, fmt.Errorf("current Tailscale device identity is unavailable; bring ATC's tsnet node up first")
	}
	requested := strings.TrimSpace(st.RequestedHostname)
	if requested == "" {
		requested = strings.TrimSpace(cfg.Hostname)
	}
	result.Requested = requested
	result.CurrentNodeID = st.NodeID
	result.CurrentName = st.Hostname

	client := newTailscaleAPIClient(cfg.APIKey, cfg.Tailnet)
	return cleanupStaleDevices(ctx, client, requested, st.NodeID, st.Hostname)
}

// cleanupStaleDevices is the pure cleanup policy, split out so it can be tested
// against a fake API without a live tsnet node.
func cleanupStaleDevices(ctx context.Context, client *tailscaleAPIClient, requested, currentID, currentName string) (DeviceCleanupResult, error) {
	result := DeviceCleanupResult{
		Requested:     requested,
		CurrentNodeID: currentID,
		CurrentName:   currentName,
	}
	devices, err := client.listDevices(ctx)
	if err != nil {
		return result, err
	}

	for _, device := range devices {
		if device.ID == currentID {
			continue
		}
		if !deviceMatchesName(device, requested) {
			continue
		}
		if device.ConnectedToControl {
			result.Skipped = append(result.Skipped, DeviceRef{
				ID: device.ID, Hostname: device.Hostname, Name: device.Name,
				Online: true, Reason: "still connected to control",
			})
			continue
		}
		if err := client.deleteDevice(ctx, device.ID); err != nil {
			result.Skipped = append(result.Skipped, DeviceRef{
				ID: device.ID, Hostname: device.Hostname, Name: device.Name,
				Reason: err.Error(),
			})
			continue
		}
		result.Deleted = append(result.Deleted, DeviceRef{
			ID: device.ID, Hostname: device.Hostname, Name: device.Name,
		})
	}

	if currentID != "" && currentName != requested {
		if err := client.renameDevice(ctx, currentID, requested); err != nil {
			result.Skipped = append(result.Skipped, DeviceRef{
				ID: currentID, Hostname: currentName, Reason: "rename failed: " + err.Error(),
			})
		} else {
			result.Renamed = true
			result.CurrentName = requested
		}
	}

	switch {
	case len(result.Deleted) == 0 && !result.Renamed:
		result.Message = "no stale devices found; nothing changed"
	case len(result.Deleted) > 0 && result.Renamed:
		result.Message = fmt.Sprintf("removed %d stale device(s) and renamed this node to %s", len(result.Deleted), requested)
	case len(result.Deleted) > 0:
		result.Message = fmt.Sprintf("removed %d stale device(s); this node kept its current name", len(result.Deleted))
	default:
		result.Message = "renamed this node to " + requested
	}
	return result, nil
}

// deviceMatchesName reports whether a device holds (or held) the requested
// name, comparing both the requested hostname and the assigned DNS label.
func deviceMatchesName(device apiDevice, requested string) bool {
	requested = strings.TrimSpace(requested)
	if requested == "" {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(device.Hostname), requested) {
		return true
	}
	label := strings.TrimSuffix(strings.TrimSpace(device.Name), ".")
	if i := strings.IndexByte(label, '.'); i > 0 {
		label = label[:i]
	}
	return strings.EqualFold(label, requested)
}
