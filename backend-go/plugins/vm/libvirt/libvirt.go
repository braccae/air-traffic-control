package libvirt

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"air-traffic-control/backend/pkg/plugin"
)

func init() {
	_ = plugin.Register(New())
}

// Plugin implements KVM / QEMU virtual machine inspection and lifecycle via virsh.
type Plugin struct{}

func New() *Plugin {
	return &Plugin{}
}

func (p *Plugin) Meta() plugin.Meta {
	return plugin.Meta{
		ID:          "vm/libvirt",
		Kind:        plugin.KindVM,
		Name:        "libvirt",
		Label:       "Virtual Machines (KVM)",
		Icon:        "desktop_windows",
		Description: "KVM / QEMU virtualization hypervisor, hardware acceleration, and virsh domain controls",
		Order:       10,
	}
}

func (p *Plugin) Compatible(h plugin.HostFacts) bool {
	return h.OSFamily != "windows" && h.OSFamily != "router_switch"
}

func (p *Plugin) Requirements() plugin.Requirements {
	return plugin.Requirements{
		HostPackages: map[string][]string{
			"dnf": {"libvirt-client", "qemu-kvm"},
			"apt": {"libvirt-clients", "qemu-kvm"},
			"apk": {"libvirt-client", "qemu"},
		},
		ControllerCollections: []string{
			"community.general",
		},
	}
}

func (p *Plugin) Probe(ctx context.Context, r plugin.Runner, h plugin.HostFacts) (plugin.Detection, error) {
	if r == nil {
		return plugin.Detection{Detected: false, Status: "OFFLINE", Error: "runner is nil"}, nil
	}

	out, err := r.RunCommand(ctx, "which virsh 2>&1 || sudo -n which virsh 2>&1 || true")
	if err != nil || !strings.Contains(out, "/virsh") {
		return plugin.Detection{
			Detected: false,
			Status:   "UNAVAILABLE",
			Error:    "virsh binary not found",
		}, nil
	}

	listOut, err := r.RunCommand(ctx, "sudo -n virsh list --all 2>&1 || virsh list --all 2>&1 || true")
	vmCount := 0
	if err == nil {
		lines := strings.Split(strings.TrimSpace(listOut), "\n")
		for _, l := range lines {
			tokens := strings.Fields(l)
			if len(tokens) >= 3 && tokens[0] != "Id" && !strings.HasPrefix(tokens[0], "---") {
				vmCount++
			}
		}
	}

	return plugin.Detection{
		Detected: true,
		Status:   fmt.Sprintf("%d VM(s)", vmCount),
		Details:  "libvirt / virsh hypervisor active",
	}, nil
}

func (p *Plugin) Read(ctx context.Context, r plugin.Runner, h plugin.HostFacts) (any, error) {
	if r == nil {
		return nil, fmt.Errorf("runner is nil")
	}

	cmd := "which virsh 2>&1 || sudo -n which virsh 2>&1 || echo 'NO_VIRSH'; echo '===VIRSH_LIST==='; sudo -n virsh list --all 2>&1 || virsh list --all 2>&1 || echo 'NO_GUESTS'; echo '===SERVICES==='; systemctl is-active libvirtd virtqemud 2>&1 || true; echo '===KVM==='; lsmod | grep kvm 2>&1 || true"
	out, err := r.RunCommand(ctx, cmd)
	if err != nil {
		return nil, err
	}

	parts := strings.Split(out, "===VIRSH_LIST===")
	virshBin := strings.TrimSpace(parts[0])
	var virshList, servicesRaw, kvmRaw string
	if len(parts) > 1 {
		rest1 := strings.Split(parts[1], "===SERVICES===")
		virshList = strings.TrimSpace(rest1[0])
		if len(rest1) > 1 {
			rest2 := strings.Split(rest1[1], "===KVM===")
			servicesRaw = strings.TrimSpace(rest2[0])
			if len(rest2) > 1 {
				kvmRaw = strings.TrimSpace(rest2[1])
			}
		}
	}

	hasVirsh := !strings.Contains(virshBin, "NO_VIRSH") && strings.Contains(virshBin, "/virsh")
	hasKvm := strings.Contains(kvmRaw, "kvm_amd") || strings.Contains(kvmRaw, "kvm_intel") || strings.Contains(kvmRaw, "kvm")
	isServiceActive := strings.Contains(servicesRaw, "active")

	var vms []map[string]any
	if hasVirsh && !strings.Contains(virshList, "NO_GUESTS") && !strings.Contains(virshList, "error") {
		lines := strings.Split(virshList, "\n")
		for _, line := range lines {
			tokens := strings.Fields(line)
			if len(tokens) >= 3 && tokens[0] != "Id" && !strings.HasPrefix(tokens[0], "---") {
				vmid, parseErr := strconv.Atoi(tokens[0])
				if parseErr != nil {
					vmid = 100 + len(vms)
				}
				name := tokens[1]
				statusStr := strings.ToUpper(strings.Join(tokens[2:], " "))
				status := "STOPPED"
				if strings.Contains(statusStr, "RUNNING") {
					status = "RUNNING"
				}

				vms = append(vms, map[string]any{
					"vmid":    vmid,
					"name":    fmt.Sprintf("%d (%s)", vmid, name),
					"subtext": fmt.Sprintf("KVM Guest (%s)", name),
					"status":  status,
					"vcpus":   2,
					"ramGiB":  4,
					"type":    "linux",
				})
			}
		}
	}

	kvmModule := "software emulation"
	if strings.Contains(kvmRaw, "kvm_amd") {
		kvmModule = "kvm_amd (AMD-V)"
	} else if strings.Contains(kvmRaw, "kvm_intel") {
		kvmModule = "kvm_intel (VT-x)"
	} else if hasKvm {
		kvmModule = "kvm"
	}

	serviceStatus := "inactive"
	if isServiceActive {
		serviceStatus = "active"
	}

	return map[string]any{
		"hasVirsh":       hasVirsh,
		"hasKvm":         hasKvm,
		"serviceStatus":  serviceStatus,
		"kvmModule":      kvmModule,
		"hypervisor":     fmt.Sprintf("KVM / QEMU (%s)", kvmModule),
		"vms":            vms,
	}, nil
}

func (p *Plugin) Actions() []plugin.ActionSpec {
	return []plugin.ActionSpec{
		{
			Name:        "start",
			Label:       "Start Virtual Machine",
			Description: "Powers on a virtual machine guest",
			Options: []plugin.OptionSchema{
				{Name: "name", Type: "string", Required: true, Description: "Virtual machine domain name"},
			},
		},
		{
			Name:        "shutdown",
			Label:       "Shutdown Virtual Machine",
			Description: "Sends ACPI graceful shutdown signal to domain",
			Options: []plugin.OptionSchema{
				{Name: "name", Type: "string", Required: true, Description: "Virtual machine domain name"},
			},
		},
		{
			Name:        "destroy",
			Label:       "Force Stop Virtual Machine",
			Description: "Immediately cuts power to domain (equivalent to pulling power cable)",
			Options: []plugin.OptionSchema{
				{Name: "name", Type: "string", Required: true, Description: "Virtual machine domain name"},
			},
		},
		{
			Name:        "reboot",
			Label:       "Reboot Virtual Machine",
			Description: "Reboots a virtual machine guest",
			Options: []plugin.OptionSchema{
				{Name: "name", Type: "string", Required: true, Description: "Virtual machine domain name"},
			},
		},
		{
			Name:        "reset",
			Label:       "Reset Virtual Machine",
			Description: "Force resets a virtual machine guest",
			Options: []plugin.OptionSchema{
				{Name: "name", Type: "string", Required: true, Description: "Virtual machine domain name"},
			},
		},
		{
			Name:        "suspend",
			Label:       "Suspend Virtual Machine",
			Description: "Suspends (pauses) a running virtual machine",
			Options: []plugin.OptionSchema{
				{Name: "name", Type: "string", Required: true, Description: "Virtual machine domain name"},
			},
		},
		{
			Name:        "resume",
			Label:       "Resume Virtual Machine",
			Description: "Resumes a suspended (paused) virtual machine",
			Options: []plugin.OptionSchema{
				{Name: "name", Type: "string", Required: true, Description: "Virtual machine domain name"},
			},
		},
		{
			Name:        "create",
			Label:       "Create Virtual Machine",
			Description: "Provisions a new KVM/QEMU virtual machine",
			Options: []plugin.OptionSchema{
				{Name: "name", Type: "string", Required: true, Description: "Virtual machine domain name"},
			},
		},
	}
}

func (p *Plugin) Compile(h plugin.HostFacts, action string, opts map[string]any) (plugin.TaskSpec, error) {
	norm := strings.ToLower(action)
	name, _ := opts["name"].(string)
	if name == "" {
		name, _ = opts["vmid"].(string)
	}
	if name == "" {
		return plugin.TaskSpec{}, fmt.Errorf("vm domain name is required")
	}

	if norm == "create" {
		vcpus := 2
		if v, ok := opts["vcpus"].(int); ok && v > 0 {
			vcpus = v
		} else if vf, ok := opts["vcpus"].(float64); ok && vf > 0 {
			vcpus = int(vf)
		}
		ramGiB := 4
		if r, ok := opts["ramGiB"].(int); ok && r > 0 {
			ramGiB = r
		} else if rf, ok := opts["ramGiB"].(float64); ok && rf > 0 {
			ramGiB = int(rf)
		}
		ramMb := ramGiB * 1024
		diskGb := 20
		if d, ok := opts["diskGb"].(int); ok && d > 0 {
			diskGb = d
		} else if df, ok := opts["diskGb"].(float64); ok && df > 0 {
			diskGb = int(df)
		}
		bridge, _ := opts["bridge"].(string)
		if bridge == "" {
			bridge = "virbr0"
		}
		cmd := fmt.Sprintf("virt-install --name %q --vcpus %d --memory %d --disk size=%d --network bridge=%s --noautoconsole --import --check path_in_use=off 2>&1 || virsh define /dev/stdin << 'EOF'\n<domain type='kvm'><name>%s</name><memory unit='MiB'>%d</memory><vcpu>%d</vcpu><os><type arch='x86_64'>hvm</type></os></domain>\nEOF 2>&1", name, vcpus, ramMb, diskGb, bridge, name, ramMb, vcpus)
		return plugin.TaskSpec{
			Name: fmt.Sprintf("Create virtual machine %s", name),
			Steps: []plugin.TaskStep{
				{
					Name:   fmt.Sprintf("Define and provision VM %s", name),
					Module: "ansible.builtin.command",
					Params: map[string]any{
						"cmd":    cmd,
						"become": true,
					},
				},
			},
		}, nil
	}

	stateMap := map[string]string{
		"start":    "running",
		"shutdown": "shutdown",
		"destroy":  "destroyed",
		"reboot":   "restarted",
		"reset":    "restarted",
		"suspend":  "paused",
		"resume":   "unpaused",
	}

	state, ok := stateMap[norm]
	if !ok {
		return plugin.TaskSpec{}, fmt.Errorf("unsupported libvirt action: %s", action)
	}

	return plugin.TaskSpec{
		Name: fmt.Sprintf("%s virtual machine %s", strings.Title(norm), name),
		Steps: []plugin.TaskStep{
			{
				Name:   fmt.Sprintf("%s VM %s", strings.Title(norm), name),
				Module: "community.general.virt",
				Params: map[string]any{
					"name":   name,
					"state":  state,
					"become": true,
				},
			},
		},
	}, nil
}
