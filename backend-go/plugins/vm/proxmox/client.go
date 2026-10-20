package proxmox

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Config carries the connection parameters for a Proxmox VE API endpoint.
//
// Credentials are secrets: they are only ever attached to outgoing request
// headers and are never logged, echoed back, or embedded in returned data.
type Config struct {
	Host        string // hostname or IP of the Proxmox node
	Port        int    // API port (default 8006)
	User        string // e.g. "root@pam"
	TokenID     string // token id, either "atc" or the full "root@pam!atc"
	TokenSecret string // token secret (secret)
	Password    string // fallback password auth when no token is configured (secret)
	Insecure    bool   // skip TLS verification (PVE ships a self-signed cert)
	Timeout     time.Duration

	// HTTPClient is optional and only used by tests to inject a transport.
	HTTPClient *http.Client
}

// scheme returns https for the default PVE port, otherwise honours the port.
func (c Config) scheme() string {
	return "https"
}

// baseURL builds the API root URL for the configured endpoint.
func (c Config) baseURL() string {
	port := c.Port
	if port == 0 {
		port = 8006
	}
	host := strings.TrimSpace(c.Host)
	// Allow callers to pass an explicit scheme (used by the httptest harness).
	if strings.HasPrefix(host, "http://") || strings.HasPrefix(host, "https://") {
		u := strings.TrimRight(host, "/")
		return u
	}
	return fmt.Sprintf("%s://%s:%d", c.scheme(), host, port)
}

// Client is a minimal, real Proxmox VE REST API client. It performs genuine
// HTTP requests against https://<host>:<port>/api2/json/... and surfaces
// transport and API errors verbatim. It never fabricates a result.
type Client struct {
	cfg    Config
	hc     *http.Client
	ticket string // PVEAuthCookie, populated lazily for password auth
	csrf   string // CSRFPreventionToken, populated lazily for password auth
}

// NewClient constructs a client from the given configuration.
func NewClient(cfg Config) *Client {
	hc := cfg.HTTPClient
	if hc == nil {
		timeout := cfg.Timeout
		if timeout == 0 {
			timeout = 12 * time.Second
		}
		hc = &http.Client{
			Timeout: timeout,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: cfg.Insecure}, //nolint:gosec // PVE self-signed certs
			},
		}
	}
	return &Client{cfg: cfg, hc: hc}
}

// apiError is returned when the Proxmox API responds with a non-2xx status.
type apiError struct {
	Status int
	Method string
	Path   string
	Body   string
}

func (e *apiError) Error() string {
	msg := strings.TrimSpace(e.Body)
	// PVE error bodies look like {"data":null,"errors":{...}} or plain text.
	if msg == "" {
		msg = http.StatusText(e.Status)
	}
	return fmt.Sprintf("proxmox API %s %s returned HTTP %d: %s", e.Method, e.Path, e.Status, msg)
}

// envelope mirrors the Proxmox JSON response shape: {"data": ...}.
type envelope struct {
	Data   json.RawMessage `json:"data"`
	Errors json.RawMessage `json:"errors,omitempty"`
}

// authHeader returns the Authorization header value for token auth, or "".
func (c *Client) authHeader() string {
	if strings.TrimSpace(c.cfg.TokenSecret) == "" {
		return ""
	}
	tokenID := strings.TrimSpace(c.cfg.TokenID)
	if tokenID == "" {
		return ""
	}
	if !strings.Contains(tokenID, "!") {
		user := strings.TrimSpace(c.cfg.User)
		if user == "" {
			user = "root@pam"
		}
		tokenID = user + "!" + tokenID
	}
	return "PVEAPIToken=" + tokenID + "=" + c.cfg.TokenSecret
}

// login performs password authentication and stores the ticket + CSRF token.
// It is only invoked when token auth is not configured.
func (c *Client) login(ctx context.Context) error {
	if c.ticket != "" {
		return nil
	}
	if strings.TrimSpace(c.cfg.Password) == "" {
		return fmt.Errorf("proxmox authentication is not configured: set an API token (id + secret) or a password")
	}
	user := strings.TrimSpace(c.cfg.User)
	if user == "" {
		user = "root@pam"
	}
	form := url.Values{}
	form.Set("username", user)
	form.Set("password", c.cfg.Password)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.baseURL()+"/api2/json/access/ticket", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("proxmox login request failed: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &apiError{Status: resp.StatusCode, Method: http.MethodPost, Path: "/access/ticket", Body: string(body)}
	}
	var env envelope
	if err := json.Unmarshal(body, &env); err != nil {
		return fmt.Errorf("proxmox login: invalid response: %w", err)
	}
	var ticket struct {
		Ticket string `json:"ticket"`
		CSRF   string `json:"CSRFPreventionToken"`
	}
	if err := json.Unmarshal(env.Data, &ticket); err != nil {
		return fmt.Errorf("proxmox login: invalid ticket payload: %w", err)
	}
	if ticket.Ticket == "" {
		return fmt.Errorf("proxmox login: empty ticket for user %s", user)
	}
	c.ticket = ticket.Ticket
	c.csrf = ticket.CSRF
	return nil
}

// do executes an API request and returns the raw "data" field.
func (c *Client) do(ctx context.Context, method, path string, params url.Values) (json.RawMessage, error) {
	if auth := c.authHeader(); auth == "" {
		if err := c.login(ctx); err != nil {
			return nil, err
		}
	}

	var bodyReader io.Reader
	fullURL := c.cfg.baseURL() + "/api2/json" + path
	if method == http.MethodGet || method == http.MethodDelete {
		if len(params) > 0 {
			fullURL += "?" + params.Encode()
		}
	} else if params != nil {
		bodyReader = strings.NewReader(params.Encode())
	}

	req, err := http.NewRequestWithContext(ctx, method, fullURL, bodyReader)
	if err != nil {
		return nil, err
	}
	if auth := c.authHeader(); auth != "" {
		req.Header.Set("Authorization", auth)
	} else {
		req.Header.Set("Cookie", "PVEAuthCookie="+c.ticket)
		if method != http.MethodGet {
			req.Header.Set("CSRFPreventionToken", c.csrf)
		}
	}
	if bodyReader != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}

	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("proxmox API %s %s unreachable: %w", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &apiError{Status: resp.StatusCode, Method: method, Path: path, Body: string(raw)}
	}
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("proxmox API %s %s: invalid JSON response: %w", method, path, err)
	}
	return env.Data, nil
}

func (c *Client) getData(ctx context.Context, path string, out any) error {
	data, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	if len(data) == 0 || string(data) == "null" {
		return nil
	}
	return json.Unmarshal(data, out)
}

// --- Typed endpoint helpers ---

// Version probes the API and returns its version payload.
func (c *Client) Version(ctx context.Context) (map[string]any, error) {
	var out map[string]any
	if err := c.getData(ctx, "/version", &out); err != nil {
		return nil, err
	}
	return out, nil
}

// NodeInfo describes a Proxmox cluster node.
type NodeInfo struct {
	Node   string `json:"node"`
	Status string `json:"status"`
	Type   string `json:"type"`
	MaxCPU int    `json:"maxcpu"`
	MaxMem int64  `json:"maxmem"`
	Mem    int64  `json:"mem"`
	Uptime int64  `json:"uptime"`
}

// Nodes lists cluster nodes.
func (c *Client) Nodes(ctx context.Context) ([]NodeInfo, error) {
	var out []NodeInfo
	if err := c.getData(ctx, "/nodes", &out); err != nil {
		return nil, err
	}
	return out, nil
}

// GuestResource is one row from /cluster/resources (qemu, lxc, node, storage).
type GuestResource struct {
	ID       string `json:"id"` // "qemu/100", "lxc/101"
	Type     string `json:"type"`
	Node     string `json:"node"`
	VMID     int    `json:"vmid"`
	Name     string `json:"name"`
	Status   string `json:"status"`
	MaxMem   int64  `json:"maxmem"`
	MaxDisk  int64  `json:"maxdisk"`
	MaxCPU   int    `json:"maxcpu"`
	Uptime   int64  `json:"uptime"`
	Template int    `json:"template"`
}

// Resources returns cluster resources, optionally filtered by type
// ("vm" matches both qemu and lxc, per the PVE API).
func (c *Client) Resources(ctx context.Context, resourceType string) ([]GuestResource, error) {
	params := url.Values{}
	if resourceType != "" {
		params.Set("type", resourceType)
	}
	data, err := c.do(ctx, http.MethodGet, "/cluster/resources", params)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 || string(data) == "null" {
		return nil, nil
	}
	var out []GuestResource
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("proxmox /cluster/resources: %w", err)
	}
	return out, nil
}

// GuestConfig returns the raw config map for a guest.
func (c *Client) GuestConfig(ctx context.Context, guestType, node string, vmid int) (map[string]any, error) {
	path := fmt.Sprintf("/nodes/%s/%s/%d/config", url.PathEscape(node), guestType, vmid)
	var out map[string]any
	if err := c.getData(ctx, path, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// GuestStatus returns the current runtime status of a guest.
func (c *Client) GuestStatus(ctx context.Context, guestType, node string, vmid int) (map[string]any, error) {
	path := fmt.Sprintf("/nodes/%s/%s/%d/status/current", url.PathEscape(node), guestType, vmid)
	var out map[string]any
	if err := c.getData(ctx, path, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// LifecycleAction issues a real guest lifecycle call. action is one of
// start, stop, shutdown, reboot, suspend, resume.
func (c *Client) LifecycleAction(ctx context.Context, guestType, node string, vmid int, action string, params url.Values) (json.RawMessage, error) {
	if params == nil {
		params = url.Values{}
	}
	path := fmt.Sprintf("/nodes/%s/%s/%d/status/%s", url.PathEscape(node), guestType, vmid, action)
	return c.do(ctx, http.MethodPost, path, params)
}

// CreateGuest creates a guest via POST /nodes/{node}/{type} and returns the UPID.
func (c *Client) CreateGuest(ctx context.Context, guestType, node string, params url.Values) (string, error) {
	path := fmt.Sprintf("/nodes/%s/%s", url.PathEscape(node), guestType)
	data, err := c.do(ctx, http.MethodPost, path, params)
	if err != nil {
		return "", err
	}
	var upid string
	_ = json.Unmarshal(data, &upid)
	return upid, nil
}

// UpdateGuestConfig performs PUT /nodes/{node}/{type}/{vmid}/config with the
// supplied parameters and returns the apply-pending description.
func (c *Client) UpdateGuestConfig(ctx context.Context, guestType, node string, vmid int, params url.Values) (json.RawMessage, error) {
	path := fmt.Sprintf("/nodes/%s/%s/%d/config", url.PathEscape(node), guestType, vmid)
	return c.do(ctx, http.MethodPut, path, params)
}

// DeleteGuest removes a guest.
func (c *Client) DeleteGuest(ctx context.Context, guestType, node string, vmid int) (string, error) {
	path := fmt.Sprintf("/nodes/%s/%s/%d", url.PathEscape(node), guestType, vmid)
	data, err := c.do(ctx, http.MethodDelete, path, nil)
	if err != nil {
		return "", err
	}
	var upid string
	_ = json.Unmarshal(data, &upid)
	return upid, nil
}

// VNCProxy requests a VNC proxy ticket + port for a running QEMU guest.
func (c *Client) VNCProxy(ctx context.Context, node string, vmid int) (map[string]any, error) {
	params := url.Values{}
	params.Set("websocket", "1")
	path := fmt.Sprintf("/nodes/%s/qemu/%d/vncproxy", url.PathEscape(node), vmid)
	data, err := c.do(ctx, http.MethodPost, path, params)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// SPICEProxy requests a SPICE connection ticket for a running QEMU guest.
func (c *Client) SPICEProxy(ctx context.Context, node string, vmid int, proxyHost string) (map[string]any, error) {
	params := url.Values{}
	if proxyHost != "" {
		params.Set("proxy", proxyHost)
	}
	path := fmt.Sprintf("/nodes/%s/qemu/%d/spiceproxy", url.PathEscape(node), vmid)
	data, err := c.do(ctx, http.MethodGet, path, params)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// StorageInfo lists storage defined on a node.
func (c *Client) StorageInfo(ctx context.Context, node string) ([]map[string]any, error) {
	path := fmt.Sprintf("/nodes/%s/storage", url.PathEscape(node))
	var out []map[string]any
	if err := c.getData(ctx, path, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// strAny coerces a JSON value to a string, tolerating numeric types.
func strAny(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case float64:
		if t == float64(int64(t)) {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(t)
	default:
		b, err := json.Marshal(t)
		if err != nil {
			return fmt.Sprintf("%v", t)
		}
		return string(b)
	}
}
