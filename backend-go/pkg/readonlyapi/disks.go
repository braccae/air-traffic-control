package readonlyapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// disksCmd is the exact command server.ts runs for GET /api/nodes/:id/disks.
const disksCmd = "lsblk -J -b -o NAME,PATH,MODEL,SIZE,TYPE,TRAN,FSTYPE,MOUNTPOINTS,ROTA,STATE,SERIAL 2>/dev/null || lsblk -J 2>/dev/null; " +
	"echo \"===SMART===\"; " +
	"sudo -n smartctl --scan 2>/dev/null || smartctl --scan 2>/dev/null || echo \"NO_SMART\""

// lsblkDevice is one entry in the lsblk -J blockdevices tree.
type lsblkDevice struct {
	Name       string        `json:"name"`
	Size       json.Number   `json:"size"`
	Type       string        `json:"type"`
	FSType     string        `json:"fstype"`
	Model      string        `json:"model"`
	Rota       bool          `json:"rota"`
	Tran       string        `json:"tran"`
	MountPoint *string       `json:"mountpoint"`
	Mounts     []string      `json:"mountpoints"`
	Children   []lsblkDevice `json:"children"`
}

// lsblkRoot is the top-level lsblk -J document.
type lsblkRoot struct {
	BlockDevices []lsblkDevice `json:"blockdevices"`
}

// DiskChild is the child-device shape returned inside each disk.
type DiskChild struct {
	Name       string  `json:"name"`
	Size       string  `json:"size"`
	Type       string  `json:"type"`
	FSType     *string `json:"fstype"`
	MountPoint *string `json:"mountpoint"`
}

// DiskItem mirrors the Express disk projection.
type DiskItem struct {
	Name        string      `json:"name"`
	Size        string      `json:"size"`
	Type        string      `json:"type"`
	FSType      *string     `json:"fstype"`
	MountPoint  *string     `json:"mountpoint"`
	Model       *string     `json:"model"`
	Rota        bool        `json:"rota"`
	Tran        *string     `json:"tran"`
	SmartStatus string      `json:"smartStatus"`
	Children    []DiskChild `json:"children,omitempty"`
}

// formatDiskSize ports the byte-size formatter used in the Express disks handler.
func formatDiskSize(sizeBytes int64) string {
	switch {
	case sizeBytes >= 1099511627776:
		return fmt.Sprintf("%.1f TB", float64(sizeBytes)/1099511627776)
	case sizeBytes >= 1073741824:
		return fmt.Sprintf("%.1f GB", float64(sizeBytes)/1073741824)
	case sizeBytes >= 1048576:
		return fmt.Sprintf("%.1f MB", float64(sizeBytes)/1048576)
	default:
		return fmt.Sprintf("%d B", sizeBytes)
	}
}

func toInt64(n json.Number) int64 {
	if n == "" {
		return 0
	}
	if v, err := strconv.ParseInt(n.String(), 10, 64); err == nil {
		return v
	}
	if f, err := n.Float64(); err == nil {
		return int64(f)
	}
	return 0
}

func strPtrOrNil(s string) *string {
	t := strings.TrimSpace(s)
	if t == "" {
		return nil
	}
	return &t
}

// handleDisks ports GET /api/nodes/:id/disks.
func (a *API) handleDisks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	node, err := a.findNode(r.PathValue("id"))
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"success": false, "error": "Failed to probe disks"})
		return
	}

	if a.sectionIsFresh(node.ID, "disks", r.URL.Query().Get("refresh") == "true") {
		if a.sectionCachedResponse(w, node.ID, "disks") {
			return
		}
	}

	_, runner, err := a.factsAndRunner(r.Context(), node.ID)
	if err != nil {
		if a.sectionCachedResponse(w, node.ID, "disks") {
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": false, "error": err.Error()})
		return
	}

	out, err := runNodeCommand(r.Context(), runner, disksCmd)
	if err != nil {
		if a.sectionCachedResponse(w, node.ID, "disks") {
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": false, "error": "Failed to probe disks"})
		return
	}

	lsblkRaw, smartRaw := splitMarker(out, "===SMART===")

	var root lsblkRoot
	_ = json.Unmarshal([]byte(strings.TrimSpace(lsblkRaw)), &root)
	blocks := root.BlockDevices
	if blocks == nil {
		blocks = []lsblkDevice{}
	}

	hasSmart := !strings.Contains(smartRaw, "NO_SMART") && len(strings.TrimSpace(smartRaw)) > 0

	disks := make([]DiskItem, 0, len(blocks))
	for _, dev := range blocks {
		sizeBytes := toInt64(dev.Size)

		var mountPoint *string
		if dev.Mounts != nil {
			filtered := make([]string, 0, len(dev.Mounts))
			for _, m := range dev.Mounts {
				if strings.TrimSpace(m) != "" {
					filtered = append(filtered, m)
				}
			}
			joined := strings.Join(filtered, ", ")
			mountPoint = &joined
		} else if dev.MountPoint != nil {
			mountPoint = dev.MountPoint
		}

		children := make([]DiskChild, 0, len(dev.Children))
		for _, ch := range dev.Children {
			var chMount *string
			if ch.Mounts != nil {
				filtered := make([]string, 0, len(ch.Mounts))
				for _, m := range ch.Mounts {
					if strings.TrimSpace(m) != "" {
						filtered = append(filtered, m)
					}
				}
				joined := strings.Join(filtered, ", ")
				chMount = &joined
			} else if ch.MountPoint != nil {
				chMount = ch.MountPoint
			}

			children = append(children, DiskChild{
				Name:       ch.Name,
				Size:       formatDiskSize(toInt64(ch.Size)),
				Type:       ch.Type,
				FSType:     strPtrOrNil(ch.FSType),
				MountPoint: chMount,
			})
		}

		smartStatus := "Kernel sysfs: Live"
		if hasSmart {
			smartStatus = "PASSED (0 Errors)"
		}

		tran := strPtrOrNil(dev.Tran)
		if tran == nil && strings.HasPrefix(dev.Name, "nvme") {
			nvme := "nvme"
			tran = &nvme
		}

		item := DiskItem{
			Name:        dev.Name,
			Size:        formatDiskSize(sizeBytes),
			Type:        dev.Type,
			FSType:      strPtrOrNil(dev.FSType),
			MountPoint:  mountPoint,
			Model:       strPtrOrNil(dev.Model),
			Rota:        dev.Rota,
			Tran:        tran,
			SmartStatus: smartStatus,
		}
		if len(children) > 0 {
			item.Children = children
		}
		disks = append(disks, item)
	}

	result := map[string]any{
		"disks":          disks,
		"smartAvailable": hasSmart,
	}
	if !hasSmart {
		result["smartWarning"] = "smartctl not installed on host — drive health monitored via kernel sysfs."
	}
	a.sectionResult(w, node.ID, "disks", result)
}
