package readonlyapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"time"
)

// runLocalCommand runs a command on the daemon host (the coordinator), matching
// the Express handlers that use execSync locally rather than over SSH.
func runLocalCommand(ctx context.Context, timeout time.Duration, name string, args ...string) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	out, err := exec.CommandContext(cctx, name, args...).CombinedOutput()
	return string(out), err
}

// handleNodeTailscale ports GET /api/nodes/:id/tailscale (server.ts:1390).
//
// Express runs `tailscale status --json` locally and matches the requested node
// against the Self entry or the Peer map, then returns the raw peer object with
// isSelf/isDirect(/relay) annotations.
func (a *API) handleNodeTailscale(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	node, err := a.findNode(r.PathValue("id"))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": "Failed to query Tailscale"})
		return
	}

	out, err := runLocalCommand(r.Context(), 5*time.Second, "tailscale", "status", "--json")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
		return
	}

	var data map[string]any
	if err := json.Unmarshal([]byte(out), &data); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
		return
	}

	targetHost := strings.ToLower(node.ShortName)
	targetTsHost := strings.ToLower(node.TailscaleHost)

	var matched map[string]any

	if self, ok := data["Self"].(map[string]any); ok {
		hostName := strings.ToLower(jsonString(self["HostName"]))
		dnsName := strings.ToLower(jsonString(self["DNSName"]))
		if hostName == targetHost || strings.HasPrefix(dnsName, targetHost) {
			matched = cloneMap(self)
			matched["isSelf"] = true
			matched["isDirect"] = true
		}
	}

	if matched == nil {
		if peers, ok := data["Peer"].(map[string]any); ok {
			for _, raw := range peers {
				p, ok := raw.(map[string]any)
				if !ok {
					continue
				}
				hostName := strings.ToLower(jsonString(p["HostName"]))
				dnsName := strings.ToLower(jsonString(p["DNSName"]))
				if hostName == targetHost || strings.HasPrefix(dnsName, targetHost) || strings.HasPrefix(dnsName, targetTsHost) {
					relay := jsonString(p["Relay"])
					matched = cloneMap(p)
					matched["isSelf"] = false
					matched["isDirect"] = relay == ""
					if relay == "" {
						matched["relay"] = "direct"
					} else {
						matched["relay"] = relay
					}
					break
				}
			}
		}
	}

	if matched != nil {
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "peer": matched})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"success": false,
		"message": fmt.Sprintf("Node %s not found in active Tailscale mesh", node.Name),
	})
}

// handleDerpMap ports GET /api/tailscale/derp-map (server.ts:1453).
func (a *API) handleDerpMap(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	out, err := runLocalCommand(r.Context(), 10*time.Second, "tailscale", "netcheck")
	if err != nil {
		msg := err.Error()
		if strings.TrimSpace(out) != "" {
			msg = strings.TrimSpace(out)
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": false, "error": msg})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"success": true, "netcheck": out})
}

func jsonString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func cloneMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m)+2)
	for k, v := range m {
		out[k] = v
	}
	return out
}
