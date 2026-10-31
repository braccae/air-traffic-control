package libvirt

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"air-traffic-control/backend/pkg/plugin"
	"air-traffic-control/backend/pkg/plugin/newapi"
	"air-traffic-control/backend/pkg/plugin/settingsframework"
)

const (
	// PluginID is the canonical identifier for the libvirt KVM/QEMU VM plugin.
	PluginID = "vm/libvirt"
	// SettingsPageID is the settings page identifier for datacenter libvirt defaults.
	SettingsPageID = "vm/libvirt"

	// DefaultConnectURI is the default libvirt hypervisor connection URI.
	DefaultConnectURI = "qemu:///system"
	// DefaultNetworkBridge is the default bridge interface for provisioned VMs.
	DefaultNetworkBridge = "virbr0"
	// DefaultVCPUs is the default vCPU count when provisioning a new VM.
	DefaultVCPUs = 2
	// DefaultRAMGiB is the default memory allocation in GiB when provisioning a new VM.
	DefaultRAMGiB = 4
	// DefaultDiskGiB is the default disk size in GiB when provisioning a new VM.
	DefaultDiskGiB = 20
	// DefaultStoragePool is the default libvirt storage pool name.
	DefaultStoragePool = "default"
)

// Settings field keys for the libvirt datacenter settings page.
const (
	FieldConnectURI           = "connectUri"
	FieldDefaultBridge        = "defaultBridge"
	FieldDefaultStoragePool   = "defaultStoragePool"
	FieldDefaultVCPUs         = "defaultVcpus"
	FieldDefaultRAMGiB        = "defaultRamGiB"
	FieldDefaultDiskGiB       = "defaultDiskGb"
	FieldShutdownBehavior     = "shutdownBehavior"
	FieldAutostartNewVMs      = "autostartNewVms"
	FieldSpicePasswordVaultVar = "spicePasswordVaultVar"
)

var (
	_ plugin.Plugin                      = (*Plugin)(nil)
	_ plugin.Reader                      = (*Plugin)(nil)
	_ plugin.Actor                       = (*Plugin)(nil)
	_ plugin.SettingsPageProvider        = (*Plugin)(nil)
	_ newapi.DataCollectionPlugin        = (*Plugin)(nil)
	_ newapi.ManagementPlugin            = (*Plugin)(nil)
	_ newapi.CategorizedSettingsProvider = (*Plugin)(nil)
	_ newapi.ContextMenuProvider         = (*Plugin)(nil)
	_ newapi.TaskCompileProvider         = (*Plugin)(nil)
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
		ID:          PluginID,
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

// CollectionSchedule returns the standard polling and cache TTL schedule for VM telemetry.
func (p *Plugin) CollectionSchedule() newapi.CollectionSchedule {
	return newapi.DefaultScheduleForKind(p.Meta().Kind)
}

// CollectionSchema documents the payload structure returned by Read.
func (p *Plugin) CollectionSchema() newapi.CollectionSchema {
	return newapi.CollectionSchema{
		Version:     "v2",
		RootKey:     "vms",
		Description: p.Meta().Description,
	}
}

// CategorizedSettingsPage declares datacenter-wide KVM/libvirt virtualization defaults.
func (p *Plugin) CategorizedSettingsPage() newapi.CategorizedSettingsPage {
	meta := p.Meta()
	return settingsframework.NewPageBuilder(SettingsPageID, "KVM / libvirt Virtualization Defaults").
		WithIcon(meta.Icon).
		WithCategory(string(meta.Kind)).
		WithScope(newapi.SettingsScopeDatacenter).
		WithOrder(meta.Order).
		WithDescription("Datacenter-wide libvirt connection URI, default VM resource sizing, network bridge, and console security defaults.").
		StringField(
			FieldConnectURI,
			"Default Connection URI",
			DefaultConnectURI,
			settingsframework.WithPlaceholder(DefaultConnectURI),
			settingsframework.WithDescription("Default libvirt hypervisor connection URI (e.g. qemu:///system or qemu:///session)."),
		).
		StringField(
			FieldDefaultBridge,
			"Default Network Bridge",
			DefaultNetworkBridge,
			settingsframework.WithPlaceholder(DefaultNetworkBridge),
			settingsframework.WithDescription("Default host bridge interface attached to newly provisioned virtual machines."),
		).
		StringField(
			FieldDefaultStoragePool,
			"Default Storage Pool",
			DefaultStoragePool,
			settingsframework.WithPlaceholder(DefaultStoragePool),
			settingsframework.WithDescription("Default libvirt storage pool used for VM disk allocation."),
		).
		IntField(
			FieldDefaultVCPUs,
			"Default vCPUs",
			DefaultVCPUs,
			settingsframework.WithMinMax(1, 128),
			settingsframework.WithDescription("Default virtual CPU count when provisioning a new VM without explicit vCPUs."),
		).
		IntField(
			FieldDefaultRAMGiB,
			"Default Memory (GiB)",
			DefaultRAMGiB,
			settingsframework.WithMinMax(1, 1024),
			settingsframework.WithDescription("Default memory in GiB when provisioning a new VM without explicit RAM."),
		).
		IntField(
			FieldDefaultDiskGiB,
			"Default Disk Size (GiB)",
			DefaultDiskGiB,
			settingsframework.WithMinMax(1, 16384),
			settingsframework.WithDescription("Default disk capacity in GiB when provisioning a new VM."),
		).
		SelectField(
			FieldShutdownBehavior,
			"Default Host Shutdown Guest Action",
			"shutdown",
			[]plugin.SettingsFieldOption{
				{Value: "shutdown", Label: "Graceful ACPI Shutdown"},
				{Value: "suspend", Label: "Managed Save / Suspend"},
				{Value: "destroy", Label: "Force Power Off"},
			},
			settingsframework.WithDescription("Default action taken on running guests when the hypervisor host shuts down."),
		).
		BoolField(
			FieldAutostartNewVMs,
			"Autostart Newly Created VMs",
			false,
			settingsframework.WithDescription("Enable libvirt domain autostart by default when provisioning new virtual machines."),
		).
		SecretField(
			FieldSpicePasswordVaultVar,
			"SPICE / VNC Console Password Vault Variable",
			settingsframework.WithPlaceholder("vault_libvirt_spice_password"),
			settingsframework.WithDescription("Ansible Vault variable name holding the default SPICE/VNC console password."),
		).
		BuildCategorized()
}

// SettingsPage returns the underlying plugin.SettingsPage for legacy settings consumers.
func (p *Plugin) SettingsPage() plugin.SettingsPage {
	return p.CategorizedSettingsPage().SettingsPage
}

// ConnectURI returns the persisted datacenter default libvirt URI or DefaultConnectURI.
func ConnectURI() string {
	return settingsframework.NewStoreAdapter(nil).StringOrDefault(SettingsPageID, FieldConnectURI, DefaultConnectURI)
}

// DefaultBridge returns the persisted datacenter default network bridge or DefaultNetworkBridge.
func DefaultBridge() string {
	return settingsframework.NewStoreAdapter(nil).StringOrDefault(SettingsPageID, FieldDefaultBridge, DefaultNetworkBridge)
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

	// Read-only libvirt connection on the unprivileged fallback: a read-write
	// connection asks polkit for org.libvirt.unix.manage and blocks on a TTY
	// password prompt when the host has a polkit agent attached.
	listOut, err := r.RunCommand(ctx, "sudo -n virsh list --all 2>&1 || virsh -r list --all 2>&1 || true")
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

	cmd := "which virsh 2>&1 || sudo -n which virsh 2>&1 || echo 'NO_VIRSH'; echo '===VIRSH_LIST==='; sudo -n virsh list --all 2>&1 || virsh -r list --all 2>&1 || echo 'NO_GUESTS'; echo '===SERVICES==='; systemctl is-active libvirtd virtqemud 2>&1 || true; echo '===KVM==='; lsmod | grep kvm 2>&1 || true"
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
		"hasVirsh":      hasVirsh,
		"hasKvm":        hasKvm,
		"serviceStatus": serviceStatus,
		"kvmModule":     kvmModule,
		"hypervisor":    fmt.Sprintf("KVM / QEMU (%s)", kvmModule),
		"vms":           vms,
	}, nil
}

// ManagementActions returns structured v2 action specifications for libvirt VM lifecycle and provisioning.
func (p *Plugin) ManagementActions() []newapi.ManagementActionSpec {
	domainOpt := []plugin.OptionSchema{
		{Name: "name", Type: "string", Required: true, Description: "Virtual machine domain name"},
	}
	return []newapi.ManagementActionSpec{
		{
			ActionSpec: plugin.ActionSpec{
				Name:        "start",
				Label:       "Start Virtual Machine",
				Description: "Powers on a virtual machine guest",
				Options:     domainOpt,
			},
			Category:         newapi.ActionCategoryLifecycle,
			Idempotent:       true,
			InvalidatesKinds: []plugin.Kind{plugin.KindVM},
		},
		{
			ActionSpec: plugin.ActionSpec{
				Name:        "shutdown",
				Label:       "Shutdown Virtual Machine",
				Description: "Sends ACPI graceful shutdown signal to domain",
				Options:     domainOpt,
			},
			Category:         newapi.ActionCategoryLifecycle,
			Idempotent:       true,
			InvalidatesKinds: []plugin.Kind{plugin.KindVM},
		},
		{
			ActionSpec: plugin.ActionSpec{
				Name:        "destroy",
				Label:       "Force Stop Virtual Machine",
				Description: "Immediately cuts power to domain (equivalent to pulling power cable)",
				Options:     domainOpt,
			},
			Category:         newapi.ActionCategoryLifecycle,
			Idempotent:       true,
			Dangerous:        true,
			ConfirmPrompt:    "Force stop this virtual machine immediately? Unsaved guest data may be lost.",
			InvalidatesKinds: []plugin.Kind{plugin.KindVM},
		},
		{
			ActionSpec: plugin.ActionSpec{
				Name:        "reboot",
				Label:       "Reboot Virtual Machine",
				Description: "Reboots a virtual machine guest",
				Options:     domainOpt,
			},
			Category:         newapi.ActionCategoryLifecycle,
			Idempotent:       false,
			InvalidatesKinds: []plugin.Kind{plugin.KindVM},
		},
		{
			ActionSpec: plugin.ActionSpec{
				Name:        "reset",
				Label:       "Reset Virtual Machine",
				Description: "Force resets a virtual machine guest",
				Options:     domainOpt,
			},
			Category:         newapi.ActionCategoryLifecycle,
			Idempotent:       false,
			Dangerous:        true,
			ConfirmPrompt:    "Force reset this virtual machine? Unsaved guest state will be lost.",
			InvalidatesKinds: []plugin.Kind{plugin.KindVM},
		},
		{
			ActionSpec: plugin.ActionSpec{
				Name:        "suspend",
				Label:       "Suspend Virtual Machine",
				Description: "Suspends (pauses) a running virtual machine",
				Options:     domainOpt,
			},
			Category:         newapi.ActionCategoryLifecycle,
			Idempotent:       true,
			InvalidatesKinds: []plugin.Kind{plugin.KindVM},
		},
		{
			ActionSpec: plugin.ActionSpec{
				Name:        "resume",
				Label:       "Resume Virtual Machine",
				Description: "Resumes a suspended (paused) virtual machine",
				Options:     domainOpt,
			},
			Category:         newapi.ActionCategoryLifecycle,
			Idempotent:       true,
			InvalidatesKinds: []plugin.Kind{plugin.KindVM},
		},
		{
			ActionSpec: plugin.ActionSpec{
				Name:        "create",
				Label:       "Create Virtual Machine",
				Description: "Provisions a new KVM/QEMU virtual machine",
				Options:     domainOpt,
			},
			Category:         newapi.ActionCategoryLifecycle,
			Idempotent:       true,
			InvalidatesKinds: []plugin.Kind{plugin.KindVM},
		},
	}
}

func (p *Plugin) Actions() []plugin.ActionSpec {
	mgmt := p.ManagementActions()
	specs := make([]plugin.ActionSpec, len(mgmt))
	for i, m := range mgmt {
		specs[i] = m.ActionSpec
	}
	return specs
}

// ValidateAction validates options for a libvirt management action against its schema.
func (p *Plugin) ValidateAction(h plugin.HostFacts, action string, opts map[string]any) error {
	norm := strings.ToLower(strings.TrimSpace(action))

	normalizedOpts := make(map[string]any, len(opts)+1)
	for k, v := range opts {
		normalizedOpts[k] = v
	}
	if name, _ := normalizedOpts["name"].(string); strings.TrimSpace(name) == "" {
		if vmid, ok := normalizedOpts["vmid"].(string); ok && strings.TrimSpace(vmid) != "" {
			normalizedOpts["name"] = strings.TrimSpace(vmid)
		}
	} else {
		normalizedOpts["name"] = strings.TrimSpace(name)
	}

	for _, spec := range p.ManagementActions() {
		if strings.EqualFold(spec.Name, norm) {
			return newapi.ValidateManagementOptions(spec, normalizedOpts)
		}
	}
	return fmt.Errorf("unsupported libvirt action: %s", action)
}

// ContextMenuEntries provides virtual machine context menu items for compatible hosts.
func (p *Plugin) ContextMenuEntries(ctx context.Context, h plugin.HostFacts) []newapi.ContextMenuEntry {
	if !p.Compatible(h) {
		return nil
	}
	return newapi.NewContextMenuBuilder(PluginID).
		WithTarget(newapi.TargetVM).
		WithGroup(string(plugin.KindVM)).
		AddAction("libvirt-start", "Start Virtual Machine", "play_arrow", "start", 10).
		AddAction("libvirt-shutdown", "Shutdown Virtual Machine", "power_settings_new", "shutdown", 20).
		AddAction("libvirt-reboot", "Reboot Virtual Machine", "restart_alt", "reboot", 30).
		AddAction("libvirt-suspend", "Suspend Virtual Machine", "pause_circle", "suspend", 40).
		AddAction("libvirt-resume", "Resume Virtual Machine", "play_circle", "resume", 50).
		AddAction("libvirt-create", "Create Virtual Machine", "add_to_queue", "create", 60).
		AddDangerAction(
			"libvirt-reset",
			"Reset Virtual Machine",
			"replay",
			"reset",
			"Force reset this virtual machine? Unsaved guest state will be lost.",
			80,
		).
		AddDangerAction(
			"libvirt-destroy",
			"Force Stop Virtual Machine",
			"power_off",
			"destroy",
			"Force stop this virtual machine immediately? Unsaved guest data may be lost.",
			90,
		).
		Build()
}

// CompileTask implements newapi.TaskCompileProvider to compile typed TaskRequests into Ansible TaskSpecs.
func (p *Plugin) CompileTask(ctx context.Context, h plugin.HostFacts, req newapi.TaskRequest) (plugin.TaskSpec, error) {
	action := strings.TrimSpace(req.Action)
	if action == "" && req.Params != nil {
		if a, ok := req.Params["action"].(string); ok {
			action = strings.TrimSpace(a)
		}
	}
	if action == "" {
		return plugin.TaskSpec{}, fmt.Errorf("action is required for plugin %s", PluginID)
	}
	return p.Compile(h, action, req.Params)
}

func (p *Plugin) Compile(h plugin.HostFacts, action string, opts map[string]any) (plugin.TaskSpec, error) {
	norm := strings.ToLower(strings.TrimSpace(action))
	name, _ := opts["name"].(string)
	if name == "" {
		name, _ = opts["vmid"].(string)
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return plugin.TaskSpec{}, fmt.Errorf("vm domain name is required")
	}

	if norm == "create" {
		vcpus := DefaultVCPUs
		if v, ok := opts["vcpus"].(int); ok && v > 0 {
			vcpus = v
		} else if vf, ok := opts["vcpus"].(float64); ok && vf > 0 {
			vcpus = int(vf)
		}
		ramGiB := DefaultRAMGiB
		if r, ok := opts["ramGiB"].(int); ok && r > 0 {
			ramGiB = r
		} else if rf, ok := opts["ramGiB"].(float64); ok && rf > 0 {
			ramGiB = int(rf)
		}
		ramMb := ramGiB * 1024
		diskGb := DefaultDiskGiB
		if d, ok := opts["diskGb"].(int); ok && d > 0 {
			diskGb = d
		} else if df, ok := opts["diskGb"].(float64); ok && df > 0 {
			diskGb = int(df)
		}
		bridge, _ := opts["bridge"].(string)
		if bridge == "" {
			bridge = DefaultBridge()
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
