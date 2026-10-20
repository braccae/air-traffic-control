package readonlyapi

import (
	"net/http"
	"strings"
)

// sharesCmd is the exact command server.ts runs for GET /api/nodes/:id/shares.
const sharesCmd = "cat /var/lib/nfs/etab /etc/exports 2>/dev/null || echo \"NO_NFS\"; " +
	"echo \"===SAMBA===\"; " +
	"sudo -n testparm -s 2>/dev/null || testparm -s 2>/dev/null || cat /etc/samba/smb.conf 2>/dev/null || echo \"NO_SAMBA\"; " +
	"echo \"===ZFS_SHARES===\"; " +
	"sudo -n cat /etc/cockpit/zfs/shares.conf 2>/dev/null || cat /etc/cockpit/zfs/shares.conf 2>/dev/null || echo \"NO_ZFS_SHARES\"; " +
	"echo \"===NET_CONF===\"; " +
	"sudo -n net conf list 2>/dev/null || echo \"NO_NET_CONF\"; " +
	"echo \"===SERVICES===\"; " +
	"systemctl is-active nfs-server smb smbd 2>&1 || true"

// handleShares ports GET /api/nodes/:id/shares (server.ts).
func (a *API) handleShares(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	node, err := a.findNode(r.PathValue("id"))
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"success": false, "error": "Shares probe failed"})
		return
	}

	if a.sectionIsFresh(node.ID, "shares", r.URL.Query().Get("refresh") == "true") {
		if a.sectionCachedResponse(w, node.ID, "shares") {
			return
		}
	}

	_, runner, err := a.factsAndRunner(r.Context(), node.ID)
	if err != nil {
		if a.sectionCachedResponse(w, node.ID, "shares") {
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": false, "error": err.Error()})
		return
	}

	out, err := runNodeCommand(r.Context(), runner, sharesCmd)
	if err != nil {
		if a.sectionCachedResponse(w, node.ID, "shares") {
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": false, "error": "Shares probe failed"})
		return
	}

	nfsRaw, rest1 := splitMarker(out, "===SAMBA===")
	smbRaw, rest2 := splitMarker(rest1, "===ZFS_SHARES===")
	zfsSharesRaw, rest3 := splitMarker(rest2, "===NET_CONF===")
	netConfRaw, srvRaw := splitMarker(rest3, "===SERVICES===")

	result := map[string]any{
		"nfs":       strings.TrimSpace(nfsRaw),
		"samba":     strings.TrimSpace(smbRaw),
		"zfsShares": strings.TrimSpace(zfsSharesRaw),
		"netConf":   strings.TrimSpace(netConfRaw),
		"services":  strings.TrimSpace(srvRaw),
	}
	a.sectionResult(w, node.ID, "shares", result)
}
