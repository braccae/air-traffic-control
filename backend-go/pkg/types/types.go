package types

import "time"

// NodeType specifies the operating environment or device class
type NodeType string

const (
	NodeTypeLinuxLinux  NodeType = "linux"
	NodeTypeWindows     NodeType = "windows"
	NodeTypeRouterSwitch NodeType = "router_switch"
)

// Node represents a managed compute or networking target
type Node struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	Group          string    `json:"group"`
	Datacenter     string    `json:"datacenter"`
	NodeType       NodeType  `json:"nodeType"`
	IP             string    `json:"ip"`
	Port           int       `json:"port"`
	TailscaleIP    string    `json:"tailscaleIp"`
	TailscaleHost  string    `json:"tailscaleHost"`
	OSDescription  string    `json:"osDescription"`
	Status         string    `json:"status"` // online, offline, degraded
	Uptime         string    `json:"uptime"`
	HasGlances     bool      `json:"hasGlances"`
	HasCockpit     bool      `json:"hasCockpit"`
	HasPodman      bool      `json:"hasPodman"`
	HasRestic      bool      `json:"hasRestic"`
	LastSeen       time.Time `json:"lastSeen"`
	AuthMethod     string    `json:"authMethod"` // ssh_key, ansible_vault, pam
	ChassisInfo    Chassis   `json:"chassis"`
}

type Chassis struct {
	Motherboard string `json:"motherboard"`
	BIOS        string `json:"bios"`
	IPMI        string `json:"ipmi"`
	ECC         string `json:"ecc"`
	SecureBoot  string `json:"secureBoot"`
	IOMMU       string `json:"iommu"`
}

// Telemetry captures live glances/PCP system metrics
type Telemetry struct {
	NodeID          string    `json:"nodeId"`
	Timestamp       time.Time `json:"timestamp"`
	CPUUsage        float64   `json:"cpuUsage"` // percent
	CPUModel        string    `json:"cpuModel"`
	VCPUs           int       `json:"vCpus"`
	MemoryUsedGiB   float64   `json:"memoryUsedGiB"`
	MemoryTotalGiB  float64   `json:"memoryTotalGiB"`
	ArcCacheGiB     float64   `json:"arcCacheGiB"`
	ZFSTankUsedTB   float64   `json:"zfsTankUsedTb"`
	ZFSTankTotalTB  float64   `json:"zfsTankTotalTb"`
	ZFSRaidType     string    `json:"zfsRaidType"`
	ZFSScrubStatus  string    `json:"zfsScrubStatus"`
	LoadAvg1m       float64   `json:"loadAvg1m"`
	LoadAvg5m       float64   `json:"loadAvg5m"`
	LoadAvg15m      float64   `json:"loadAvg15m"`
	IODelayPercent  float64   `json:"ioDelayPercent"`
	CPUTempC        float64   `json:"cpuTempC"`
	FanSpeedRPM     int       `json:"fanSpeedRpm"`
	PackageWatts    float64   `json:"packageWatts"`
	SparklinePoints []float64 `json:"sparklinePoints"`
}

// PodmanContainer represents an individual container
type PodmanContainer struct {
	ID        string  `json:"id"`
	PodID     string  `json:"podId,omitempty"`
	Name      string  `json:"name"`
	Image     string  `json:"image"`
	Status    string  `json:"status"` // Running, Exited, Paused
	CPU       string  `json:"cpu"`
	Memory    string  `json:"memory"`
	Ports     string  `json:"ports,omitempty"`
	GPUAcc    string  `json:"gpuAcc,omitempty"`
	StartedAt string  `json:"startedAt"`
}

// PodmanPod represents a group of containers sharing IPC and network
type PodmanPod struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Status      string            `json:"status"`
	Containers  []PodmanContainer `json:"containers"`
	Ports       string            `json:"ports"`
	TotalCPU    string            `json:"totalCpu"`
	TotalMemory string            `json:"totalMemory"`
	GPUInfo     string            `json:"gpuInfo,omitempty"`
	VRAMUsage   string            `json:"vramUsage,omitempty"`
	GPUUtil     int               `json:"gpuUtil,omitempty"`
}

// KVMVirtualMachine represents a local QEMU/KVM virtual machine
type KVMVirtualMachine struct {
	VMID       int    `json:"vmid"`
	Name       string `json:"name"`
	Status     string `json:"status"` // RUNNING, STOPPED, PAUSED
	VCPUs      int    `json:"vCpus"`
	MemoryGiB  int    `json:"memoryGiB"`
	Storage    string `json:"storage"`
	OSDetails  string `json:"osDetails"`
}

// ResticSnapshot represents an immutable backup snapshot
type ResticSnapshot struct {
	ID           string    `json:"id"`
	ShortID      string    `json:"shortId"`
	Time         time.Time `json:"time"`
	Paths        []string  `json:"paths"`
	Tags         []string  `json:"tags"`
	Hostname     string    `json:"hostname"`
	Size         string    `json:"size"`
	Deduplication string   `json:"deduplication"`
}

// GlobalTask represents an execution log entry
type GlobalTask struct {
	ID          string    `json:"id"`
	StartTime   string    `json:"startTime"`
	NodeTarget  string    `json:"nodeTarget"`
	User        string    `json:"user"`
	Description string    `json:"description"`
	Progress    int       `json:"progress"` // 0-100
	ProgressETA string    `json:"progressEta"`
	Status      string    `json:"status"` // RUNNING, OK, FAILED
	LogLines    []string  `json:"logLines,omitempty"`
}

// ServiceIdentity represents PAM and service accounts
type ServiceIdentity struct {
	User         string `json:"user"`
	UIDGID       string `json:"uidGid"`
	RolesGroups  string `json:"rolesGroups"`
	AuthMechanism string `json:"authMechanism"`
	LingerSubUID string `json:"lingerSubUid"`
}
