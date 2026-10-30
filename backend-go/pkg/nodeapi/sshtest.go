package nodeapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"air-traffic-control/backend/pkg/ssh"
)

// sshTestRequest is the payload for POST /api/ssh/test. It lets the UI (and the
// e2e suite) verify a host/credential combination before onboarding a node.
type sshTestRequest struct {
	Host          string `json:"host"`
	Port          int    `json:"port"`
	SSHUser       string `json:"sshUser"`
	VaultSecretID string `json:"vaultSecretId"`
	AuthMethod    string `json:"authMethod"`
	TimeoutMs     int    `json:"timeoutMs"`
}

// sshTestResponse reports the real result of an SSH connection attempt. Output
// is produced by the remote host, never synthesized.
type sshTestResponse struct {
	Success       bool     `json:"success"`
	Host          string   `json:"host"`
	Port          int      `json:"port"`
	SSHUser       string   `json:"sshUser"`
	VaultSecretID string   `json:"vaultSecretId,omitempty"`
	AuthMethods   []string `json:"authMethods,omitempty"`
	Output        string   `json:"output,omitempty"`
	LatencyMs     int64    `json:"latencyMs,omitempty"`
	Error         string   `json:"error,omitempty"`
}

const sshTestCommand = `printf 'user=%s\n' "$(id -un)"; printf 'host=%s\n' "$(uname -n)"`

// NewSSHTestHandler builds the POST /api/ssh/test handler. It resolves the
// referenced vault secret, opens a real SSH connection, and reports the remote
// identity. Failures return 502 with the exact transport error.
func NewSSHTestHandler(pool *ssh.ClientPool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{
				"success": false,
				"error":   "Method not allowed",
			})
			return
		}

		var req sshTestRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"success": false,
				"error":   "Invalid request payload",
			})
			return
		}

		req.Host = strings.TrimSpace(req.Host)
		if req.Host == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"success": false,
				"error":   "host is required",
			})
			return
		}
		if req.Port <= 0 || req.Port > 65535 {
			req.Port = 22
		}
		req.SSHUser = strings.TrimSpace(req.SSHUser)
		if req.SSHUser == "" {
			// Zero-mock invariant: never guess a login user — an unset or
			// mistyped `sshUser` (e.g. "user" instead of "sshUser") must fail
			// loudly rather than silently attempting "core" and hiding the error.
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"success": false,
				"error":   "sshUser is required (set it explicitly, e.g. \"core\" or \"root\")",
			})
			return
		}
		timeout := time.Duration(req.TimeoutMs) * time.Millisecond
		if timeout <= 0 || timeout > 30*time.Second {
			timeout = 10 * time.Second
		}

		cfg := ssh.Config{
			Host:          req.Host,
			Port:          req.Port,
			User:          req.SSHUser,
			VaultSecretID: strings.TrimSpace(req.VaultSecretID),
			AuthMethod:    strings.TrimSpace(req.AuthMethod),
			Timeout:       timeout,
		}
		resp := sshTestResponse{
			Host:          cfg.Host,
			Port:          cfg.Port,
			SSHUser:       cfg.User,
			VaultSecretID: cfg.VaultSecretID,
		}

		// Validate credentials and surface unresolved vault references before
		// dialing, so the error names the missing variable.
		methods, err := ssh.OfferAuthMethods(cfg)
		if err != nil {
			resp.Error = err.Error()
			writeJSON(w, http.StatusBadGateway, resp)
			return
		}
		resp.AuthMethods = methods

		if pool == nil {
			resp.Error = "ssh client pool is not configured"
			writeJSON(w, http.StatusBadGateway, resp)
			return
		}

		started := time.Now()
		out, err := pool.RunCommand(r.Context(), cfg, sshTestCommand)
		resp.LatencyMs = time.Since(started).Milliseconds()
		if err != nil {
			resp.Error = strings.TrimSpace(err.Error())
			if resp.Error == "" {
				resp.Error = fmt.Sprintf("SSH test to %s@%s:%d failed", cfg.User, cfg.Host, cfg.Port)
			}
			writeJSON(w, http.StatusBadGateway, resp)
			return
		}

		resp.Success = true
		resp.Output = strings.TrimSpace(out)
		writeJSON(w, http.StatusOK, resp)
	}
}
