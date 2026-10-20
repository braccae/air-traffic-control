package readonlyapi

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// vmsCmd is the exact command server.ts runs for GET /api/nodes/:id/vms.
const vmsCmd = "which virsh 2>&1 || sudo -n which virsh 2>&1 || echo \"NO_VIRSH\"; " +
	"echo \"===VIRSH_LIST===\"; " +
	"sudo -n virsh list --all 2>&1 || virsh list --all 2>&1 || echo \"NO_GUESTS\"; " +
	"echo \"===SERVICES===\"; " +
	"systemctl is-active libvirtd virtqemud 2>&1 || true; " +
	"echo \"===KVM===\"; " +
	"lsmod | grep kvm 2>&1 || true"

// VMItem mirrors the Express VM projection.
type VMItem struct {
	ID      int    `json:"vmid"`
	Name    string `json:"name"`
	Subtext string `json:"subtext"`
	Status  string `json:"status"`
	VCPUs   int    `json:"vcpus"`
	RamGiB  int    `json:"ramGiB"`
	Type    string `json:"type"`
}

// handleVMs ports GET /api/nodes/:id/vms (list only).
func (a *API) handleVMs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	node, err := a.findNode(r.PathValue("id"))
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"success": false, "error": "VM probe failed"})
		return
	}

	if a.sectionIsFresh(node.ID, "vms", r.URL.Query().Get("refresh") == "true") {
		if a.sectionCachedResponse(w, node.ID, "vms") {
			return
		}
	}

	_, runner, err := a.factsAndRunner(r.Context(), node.ID)
	if err != nil {
		if a.sectionCachedResponse(w, node.ID, "vms") {
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": false, "error": err.Error()})
		return
	}

	out, err := runNodeCommand(r.Context(), runner, vmsCmd)
	if err != nil {
		if a.sectionCachedResponse(w, node.ID, "vms") {
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": false, "error": "VM probe failed"})
		return
	}

	virshBin, rest1 := splitMarker(out, "===VIRSH_LIST===")
	virshList, rest2 := splitMarker(rest1, "===SERVICES===")
	servicesRaw, kvmRaw := splitMarker(rest2, "===KVM===")

	hasVirsh := !strings.Contains(virshBin, "NO_VIRSH") && strings.Contains(virshBin, "/virsh")
	hasKvm := strings.Contains(kvmRaw, "kvm_amd") || strings.Contains(kvmRaw, "kvm_intel") || strings.Contains(kvmRaw, "kvm")
	isServiceActive := strings.Contains(servicesRaw, "active")

	kvmModule := "software emulation"
	switch {
	case strings.Contains(kvmRaw, "kvm_amd"):
		kvmModule = "kvm_amd (AMD-V)"
	case strings.Contains(kvmRaw, "kvm_intel"):
		kvmModule = "kvm_intel (VT-x)"
	case hasKvm:
		kvmModule = "kvm"
	}

	vms := []VMItem{}
	if hasVirsh && !strings.Contains(virshList, "NO_GUESTS") && !strings.Contains(virshList, "error") {
		for _, line := range strings.Split(strings.TrimSpace(virshList), "\n") {
			tokens := splitFields(line)
			if len(tokens) < 3 || tokens[0] == "Id" || tokens[0] == "---" {
				continue
			}
			id, convErr := strconv.Atoi(tokens[0])
			if convErr != nil {
				id = 100 + len(vms)
			}
			name := tokens[1]
			status := strings.ToUpper(strings.Join(tokens[2:], " "))
			vmStatus := "STOPPED"
			if strings.Contains(status, "RUNNING") {
				vmStatus = "RUNNING"
			}
			vms = append(vms, VMItem{
				ID:      id,
				Name:    fmt.Sprintf("%d (%s)", id, name),
				Subtext: "KVM Guest (" + name + ")",
				Status:  vmStatus,
				VCPUs:   2,
				RamGiB:  4,
				Type:    "linux",
			})
		}
	}

	serviceStatus := "inactive"
	if isServiceActive {
		serviceStatus = "active"
	}

	result := map[string]any{
		"hasVirsh":      hasVirsh,
		"hasKvm":        hasKvm,
		"serviceStatus": serviceStatus,
		"kvmModule":     kvmModule,
		"hypervisor":    "KVM / QEMU (" + kvmModule + ")",
		"vms":           vms,
	}
	a.sectionResult(w, node.ID, "vms", result)
}
