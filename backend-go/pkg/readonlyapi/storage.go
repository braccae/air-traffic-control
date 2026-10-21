package readonlyapi

import (
	"net/http"
	"strings"
)

// storageCmd is the exact command server.ts:1082 runs for GET /api/nodes/:id/storage.
const storageCmd = "findmnt -J -l -t ext4,xfs,btrfs,zfs,nfs,cifs,smb3 2>/dev/null || true; " +
	"echo \"===ZFS===\"; " +
	"(sudo -n zfs list 2>/dev/null || zfs list 2>/dev/null || echo \"NO_ZFS\"); " +
	"echo \"===ZPOOL===\"; " +
	"(sudo -n zpool status 2>/dev/null || zpool status 2>/dev/null || echo \"NO_ZPOOL\"); " +
	"echo \"===ZPOOL_LIST===\"; " +
	"(sudo -n zpool list -H 2>/dev/null || zpool list -H 2>/dev/null || echo \"NO_ZPOOL_LIST\"); " +
	"echo \"===BTRFS===\"; " +
	"(btrfs filesystem show 2>/dev/null || echo \"NO_BTRFS\")"

// handleStorage ports GET /api/nodes/:id/storage (server.ts:1082).
func (a *API) handleStorage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	node, err := a.findNode(r.PathValue("id"))
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"success": false, "error": "Storage probe failed"})
		return
	}

	if a.sectionIsFresh(node.ID, "storage", r.URL.Query().Get("refresh") == "true") {
		if a.sectionCachedResponse(w, node.ID, "storage") {
			return
		}
	}

	_, runner, err := a.factsAndRunner(r.Context(), node.ID)
	if err != nil {
		if a.sectionCachedResponse(w, node.ID, "storage") {
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": false, "error": err.Error()})
		return
	}

	out, err := runNodeCommand(r.Context(), runner, storageCmd)
	if err != nil {
		if a.sectionCachedResponse(w, node.ID, "storage") {
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": false, "error": "Storage probe failed"})
		return
	}

	findmnt, rest := splitMarker(out, "===ZFS===")
	zfsList, rest := splitMarker(rest, "===ZPOOL===")
	zpoolStatus, rest := splitMarker(rest, "===ZPOOL_LIST===")
	zpoolList, btrfs := splitMarker(rest, "===BTRFS===")

	result := map[string]any{
		"findmnt":     strings.TrimSpace(findmnt),
		"zfsList":     strings.TrimSpace(zfsList),
		"zpoolStatus": strings.TrimSpace(zpoolStatus),
		"zpoolList":   strings.TrimSpace(zpoolList),
		"btrfs":       strings.TrimSpace(btrfs),
	}
	a.sectionResult(w, node.ID, "storage", result)
}
