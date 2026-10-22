package newapi

import (
	"fmt"
	"strings"

	"air-traffic-control/backend/pkg/plugin"
)

// InventoryNodeType enumerates the supported inventory host/device classes.
type InventoryNodeType string

const (
	NodeTypeLinux            InventoryNodeType = "linux"
	NodeTypeWindows          InventoryNodeType = "windows"
	NodeTypeRouterSwitch     InventoryNodeType = "router_switch"
	NodeTypeProxmox          InventoryNodeType = "proxmox"
	NodeTypeContainerHost    InventoryNodeType = "container_host"
	NodeTypeStorageAppliance InventoryNodeType = "storage_appliance"
	NodeTypeKubernetes       InventoryNodeType = "kubernetes"
)

// NodeTypeSpec describes a node classification in the datacenter inventory,
// including its default OS family, transport/elevation defaults, and expected
// capability kinds.
type NodeTypeSpec struct {
	Type              InventoryNodeType `json:"type"`
	Label             string            `json:"label"`
	Icon              string            `json:"icon"`
	Description       string            `json:"description,omitempty"`
	DefaultOSFamily   string            `json:"defaultOsFamily"`
	DefaultConnection string            `json:"defaultConnection,omitempty"`
	DefaultElevation  string            `json:"defaultElevation,omitempty"`
	Capabilities      []plugin.Kind     `json:"capabilities,omitempty"`
	Order             int               `json:"order"`
}

// NodeTypeProvider is an optional interface implemented by plugins that
// register or extend inventory node type definitions.
type NodeTypeProvider interface {
	NodeTypes() []NodeTypeSpec
}

// ValidateNodeTypeSpec verifies that a NodeTypeSpec has the required identity
// and OS family fields populated.
func ValidateNodeTypeSpec(spec NodeTypeSpec) error {
	if strings.TrimSpace(string(spec.Type)) == "" {
		return fmt.Errorf("node type cannot be empty")
	}
	if strings.TrimSpace(spec.Label) == "" {
		return fmt.Errorf("node type %q label cannot be empty", spec.Type)
	}
	if strings.TrimSpace(spec.DefaultOSFamily) == "" {
		return fmt.Errorf("node type %q defaultOsFamily cannot be empty", spec.Type)
	}
	return nil
}

// DefaultNodeTypes returns the built-in inventory node type specifications.
func DefaultNodeTypes() []NodeTypeSpec {
	return []NodeTypeSpec{
		{
			Type:              NodeTypeLinux,
			Label:             "Linux Host",
			Icon:              "dns",
			Description:       "General-purpose Linux server or workstation managed over SSH or Tailscale.",
			DefaultOSFamily:   "linux",
			DefaultConnection: "connection/ssh",
			DefaultElevation:  "elevation/run0",
			Capabilities: []plugin.Kind{
				plugin.KindSystem,
				plugin.KindPackage,
				plugin.KindStorage,
				plugin.KindContainer,
				plugin.KindNetwork,
				plugin.KindFirewall,
				plugin.KindTelemetry,
				plugin.KindLogs,
			},
			Order: 10,
		},
		{
			Type:              NodeTypeProxmox,
			Label:             "Proxmox VE Node",
			Icon:              "Screenshot_Monitor",
			Description:       "Proxmox hypervisor host supporting KVM/QEMU VMs, LXC containers, and ZFS/LVM storage.",
			DefaultOSFamily:   "linux",
			DefaultConnection: "connection/ssh",
			DefaultElevation:  "elevation/sudo",
			Capabilities: []plugin.Kind{
				plugin.KindVM,
				plugin.KindContainer,
				plugin.KindStorage,
				plugin.KindNetwork,
				plugin.KindFirewall,
				plugin.KindBackup,
				plugin.KindTelemetry,
			},
			Order: 20,
		},
		{
			Type:              NodeTypeContainerHost,
			Label:             "Container Host",
			Icon:              "deployed_code",
			Description:       "Dedicated Podman or Docker container runtime host (including bootc/OSTree immutable nodes).",
			DefaultOSFamily:   "linux",
			DefaultConnection: "connection/ssh",
			DefaultElevation:  "elevation/run0",
			Capabilities: []plugin.Kind{
				plugin.KindContainer,
				plugin.KindStorage,
				plugin.KindNetwork,
				plugin.KindFirewall,
				plugin.KindTelemetry,
			},
			Order: 30,
		},
		{
			Type:              NodeTypeStorageAppliance,
			Label:             "Storage Appliance",
			Icon:              "hard_drive",
			Description:       "NAS or SAN storage server providing ZFS/Btrfs/LVM pools and NFS/SMB shares.",
			DefaultOSFamily:   "linux",
			DefaultConnection: "connection/ssh",
			DefaultElevation:  "elevation/sudo",
			Capabilities: []plugin.Kind{
				plugin.KindStorage,
				plugin.KindShare,
				plugin.KindDisk,
				plugin.KindBackup,
				plugin.KindTelemetry,
			},
			Order: 40,
		},
		{
			Type:              NodeTypeKubernetes,
			Label:             "Kubernetes Node",
			Icon:              "hub",
			Description:       "Kubernetes control-plane or worker node.",
			DefaultOSFamily:   "linux",
			DefaultConnection: "connection/ssh",
			DefaultElevation:  "elevation/sudo",
			Capabilities: []plugin.Kind{
				plugin.KindContainer,
				plugin.KindNetwork,
				plugin.KindStorage,
				plugin.KindTelemetry,
			},
			Order: 50,
		},
		{
			Type:              NodeTypeRouterSwitch,
			Label:             "Router / Switch",
			Icon:              "router",
			Description:       "Managed network router, firewall appliance, or L2/L3 switch.",
			DefaultOSFamily:   "router_switch",
			DefaultConnection: "connection/ssh",
			DefaultElevation:  "elevation/none",
			Capabilities: []plugin.Kind{
				plugin.KindNetwork,
				plugin.KindFirewall,
				plugin.KindTelemetry,
			},
			Order: 60,
		},
		{
			Type:              NodeTypeWindows,
			Label:             "Windows Host",
			Icon:              "desktop_windows",
			Description:       "Windows Server or workstation managed via OpenSSH/WinRM.",
			DefaultOSFamily:   "windows",
			DefaultConnection: "connection/ssh",
			DefaultElevation:  "elevation/none",
			Capabilities: []plugin.Kind{
				plugin.KindSystem,
				plugin.KindStorage,
				plugin.KindShare,
				plugin.KindNetwork,
				plugin.KindTelemetry,
			},
			Order: 70,
		},
	}
}
