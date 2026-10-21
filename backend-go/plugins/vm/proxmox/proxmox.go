package proxmox

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"air-traffic-control/backend/pkg/plugin"
)

func init() {
	_ = plugin.Register(New())
}

// Plugin manages a Proxmox VE host (QEMU/KVM virtual machines and LXC
// containers) through Proxmox's own HTTP API. It performs real API calls and
// surfaces transport/API failures verbatim: it never fabricates a result.
type Plugin struct{}

// New returns a new Proxmox plugin instance.
func New() *Plugin { return &Plugin{} }

func (p *Plugin) Meta() plugin.Meta {
	return plugin.Meta{
		ID:          "vm/proxmox",
		Kind:        plugin.KindVM,
		Name:        "proxmox",
		Label:       "Proxmox VE",
		Icon:        "cloud",
		Description: "Proxmox VE hypervisor: QEMU/KVM virtual machines, LXC containers, guest settings, hardware, and noVNC/SPICE consoles",
		Order:       5,
	}
}

// Compatible restricts the plugin to Linux-family hosts.
func (p *Plugin) Compatible(h plugin.HostFacts) bool {
	return h.OSFamily != "windows" && h.OSFamily != "router_switch"
}

// Requirements declares the Galaxy collection used by the compiled mutations.
func (p *Plugin) Requirements() plugin.Requirements {
	return plugin.Requirements{
		ControllerCollections: []string{"community.proxmox"},
	}
}

// Override keys consulted for API connectivity. The token id/secret pair is a
// secret: values flow only into outgoing request headers.
const (
	ovHost        = "proxmox_api_host"
	ovPort        = "proxmox_api_port"
	ovUser        = "proxmox_api_user"
	ovTokenID     = "proxmox_api_token_id"
	ovTokenSecret = "proxmox_api_token_secret"
	ovPassword    = "proxmox_api_password"
	ovInsecure    = "proxmox_tls_insecure"
)

// configFromFacts derives the Proxmox API connection config from host facts.
func configFromFacts(h plugin.HostFacts) Config {
	ov := h.Overrides
	host := firstNonEmpty(ov[ovHost], h.HostRoute)
	port := 8006
	if p := firstNonEmpty(ov[ovPort], ov["api_port"]); p != "" {
		if v, err := strconv.Atoi(p); err == nil && v > 0 {
			port = v
		}
	}
	user := firstNonEmpty(ov[ovUser], ov["api_user"], "root@pam")
	// PVE ships a self-signed certificate by default; verification is opt-in
	// via proxmox_tls_insecure="false".
	insecure := true
	if v, ok := ov[ovInsecure]; ok && strings.EqualFold(v, "false") {
		insecure = false
	}
	return Config{
		Host:        host,
		Port:        port,
		User:        user,
		TokenID:     ov[ovTokenID],
		TokenSecret: ov[ovTokenSecret],
		Password:    ov[ovPassword],
		Insecure:    insecure,
	}
}

func clientFromFacts(h plugin.HostFacts) *Client {
	return NewClient(configFromFacts(h))
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// Probe verifies the Proxmox API is reachable and authenticates.
func (p *Plugin) Probe(ctx context.Context, r plugin.Runner, h plugin.HostFacts) (plugin.Detection, error) {
	cfg := configFromFacts(h)
	if cfg.Host == "" {
		return plugin.Detection{
			Detected: false,
			Status:   "UNCONFIGURED",
			Error:    "no Proxmox API host configured",
		}, nil
	}
	c := NewClient(cfg)
	ver, err := c.Version(ctx)
	if err != nil {
		return plugin.Detection{
			Detected: false,
			Status:   "UNREACHABLE",
			Error:    err.Error(),
		}, nil
	}

	nodes, _ := c.Nodes(ctx)
	version := strAny(ver["version"])
	release := strAny(ver["release"])
	status := fmt.Sprintf("PVE %s", version)
	if len(nodes) > 0 {
		status = fmt.Sprintf("%s · %d node(s)", status, len(nodes))
	}
	return plugin.Detection{
		Detected: true,
		Status:   status,
		Details:  fmt.Sprintf("Proxmox VE %s (%s) at %s://%s:%d", version, release, cfg.scheme(), cfg.Host, cfg.Port),
	}, nil
}

// guestSummary is the per-guest shape returned to the frontend.
type guestSummary struct {
	VMID    int            `json:"vmid"`
	Name    string         `json:"name"`
	Node    string         `json:"node"`
	Type    string         `json:"type"` // "qemu" | "lxc"
	Status  string         `json:"status"`
	MaxMem  int64          `json:"maxMem"`
	MaxDisk int64          `json:"maxDisk"`
	MaxCPU  int            `json:"maxCpu"`
	Uptime  int64          `json:"uptime"`
	Console map[string]any `json:"console,omitempty"`
}

// Read lists cluster nodes and every VM / LXC container, plus console
// connection details for running guests.
func (p *Plugin) Read(ctx context.Context, r plugin.Runner, h plugin.HostFacts) (any, error) {
	cfg := configFromFacts(h)
	if cfg.Host == "" {
		return nil, fmt.Errorf("no Proxmox API host configured for node %s", h.NodeID)
	}
	c := NewClient(cfg)

	var readErrors []string

	ver, err := c.Version(ctx)
	if err != nil {
		return nil, err // unreachable / auth failure must surface loudly
	}

	nodes, err := c.Nodes(ctx)
	if err != nil {
		return nil, err
	}

	resources, err := c.Resources(ctx, "vm")
	if err != nil {
		return nil, err
	}

	var vms, containers []guestSummary
	for _, res := range resources {
		g := guestSummary{
			VMID:    res.VMID,
			Name:    res.Name,
			Node:    res.Node,
			Type:    res.Type,
			Status:  res.Status,
			MaxMem:  res.MaxMem,
			MaxDisk: res.MaxDisk,
			MaxCPU:  res.MaxCPU,
			Uptime:  res.Uptime,
		}
		switch res.Type {
		case "qemu":
			vms = append(vms, g)
		case "lxc":
			containers = append(containers, g)
		}
	}

	sort.Slice(vms, func(i, j int) bool { return vms[i].VMID < vms[j].VMID })
	sort.Slice(containers, func(i, j int) bool { return containers[i].VMID < containers[j].VMID })

	// Console descriptors are deterministic for every guest; live SPICE/VNC
	// tickets require a running QEMU guest and are fetched best-effort (capped).
	const maxLiveConsole = 20
	liveConsole := 0
	for i := range vms {
		vms[i].Console = staticConsole(cfg, nodes, vms[i], "qemu")
		if strings.EqualFold(vms[i].Status, "running") && liveConsole < maxLiveConsole {
			liveConsole++
			mergeLiveConsole(ctx, c, cfg, &vms[i], &readErrors)
		}
	}
	for i := range containers {
		containers[i].Console = staticConsole(cfg, nodes, containers[i], "lxc")
	}

	summary := map[string]any{
		"nodes":      len(nodes),
		"vms":        len(vms),
		"containers": len(containers),
		"running":    countRunning(vms) + countRunning(containers),
	}

	return map[string]any{
		"endpoint":   fmt.Sprintf("%s://%s:%d", cfg.scheme(), cfg.Host, cfg.Port),
		"version":    strAny(ver["version"]),
		"release":    strAny(ver["release"]),
		"nodes":      nodes,
		"vms":        vms,
		"containers": containers,
		"summary":    summary,
		"errors":     readErrors,
	}, nil
}

func countRunning(gs []guestSummary) int {
	n := 0
	for _, g := range gs {
		if strings.EqualFold(g.Status, "running") {
			n++
		}
	}
	return n
}

// staticConsole builds the Proxmox web UI console URLs for a guest. These are
// served by the PVE host itself (noVNC for VMs, xterm.js for containers) and
// require an authenticated PVE session in the browser.
func staticConsole(cfg Config, nodes []NodeInfo, g guestSummary, guestType string) map[string]any {
	node := g.Node
	if node == "" && len(nodes) == 1 {
		node = nodes[0].Node
	}
	base := strings.TrimRight(cfg.baseURL(), "/")
	name := url.QueryEscape(g.Name)
	descriptor := map[string]any{
		"node": node,
	}
	if guestType == "lxc" {
		descriptor["type"] = "xtermjs"
		descriptor["termUrl"] = fmt.Sprintf("%s/?console=lxc&xtermjs=1&node=%s&vmid=%d", base, node, g.VMID)
		return descriptor
	}
	descriptor["type"] = "novnc"
	descriptor["noVNCUrl"] = fmt.Sprintf("%s/?console=kvm&novnc=1&node=%s&vmid=%d&vmname=%s&resize=scale", base, node, g.VMID, name)
	return descriptor
}

// mergeLiveConsole fetches a SPICE proxy ticket and a VNC proxy ticket for a
// running QEMU guest and merges the resulting connection details in. Failures
// are recorded (Zero-Mock) rather than silently swallowed.
func mergeLiveConsole(ctx context.Context, c *Client, cfg Config, g *guestSummary, errs *[]string) {
	if g.Console == nil {
		g.Console = map[string]any{}
	}
	if spice, err := c.SPICEProxy(ctx, g.Node, g.VMID, cfg.Host); err != nil {
		*errs = append(*errs, fmt.Sprintf("spice %d: %v", g.VMID, err))
	} else if len(spice) > 0 {
		g.Console["spice"] = spice
		g.Console["spiceUrl"] = buildSpiceURL(spice)
		g.Console["spiceVV"] = buildVVFile(spice)
	}
	if vnc, err := c.VNCProxy(ctx, g.Node, g.VMID); err != nil {
		*errs = append(*errs, fmt.Sprintf("vnc %d: %v", g.VMID, err))
	} else if len(vnc) > 0 {
		g.Console["vnc"] = vnc
		port := strAny(vnc["port"])
		host := firstNonEmpty(strAny(vnc["host"]), cfg.Host)
		g.Console["vncUrl"] = fmt.Sprintf("vnc://%s:%s", host, port)
		g.Console["vncTicket"] = strAny(vnc["ticket"])
	}
}

// buildSpiceURL builds a spice:// URI from a SPICE proxy payload.
func buildSpiceURL(spice map[string]any) string {
	host := strAny(spice["host"])
	port := strAny(spice["port"])
	if host == "" {
		return ""
	}
	u := url.URL{Scheme: "spice", Host: fmt.Sprintf("%s:%s", host, port)}
	q := url.Values{}
	if tp := strAny(spice["tls-port"]); tp != "" {
		q.Set("tls-port", tp)
	}
	if t := strAny(spice["ticket"]); t != "" {
		q.Set("password", t)
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// buildVVFile builds a virt-viewer .vv connection file for the SPICE proxy.
func buildVVFile(spice map[string]any) string {
	var b strings.Builder
	b.WriteString("[virt-viewer]\n")
	b.WriteString("type=spice\n")
	if v := strAny(spice["host"]); v != "" {
		fmt.Fprintf(&b, "host=%s\n", v)
	}
	if v := strAny(spice["port"]); v != "" {
		fmt.Fprintf(&b, "port=%s\n", v)
	}
	if v := strAny(spice["tls-port"]); v != "" {
		fmt.Fprintf(&b, "tls-port=%s\n", v)
	}
	if v := strAny(spice["ticket"]); v != "" {
		fmt.Fprintf(&b, "password=%s\n", v)
	}
	b.WriteString("delete-this-file=1\n")
	b.WriteString("fullscreen=0\n")
	return b.String()
}

// --- Actions ---

func guestOpts(action, guestID string) []plugin.OptionSchema {
	return []plugin.OptionSchema{
		{Name: "node", Type: "string", Required: true, Description: "Proxmox node hosting the guest"},
		{Name: guestID, Type: "int", Required: true, Description: "Guest VMID"},
	}
}

func (p *Plugin) Actions() []plugin.ActionSpec {
	lifecycle := []struct{ name, label, desc string }{
		{"vm_start", "Start VM", "Power on a QEMU/KVM virtual machine"},
		{"vm_shutdown", "Shutdown VM", "Gracefully request ACPI shutdown of a virtual machine"},
		{"vm_stop", "Force Stop VM", "Hard stop a virtual machine (equivalent to pulling power)"},
		{"vm_reboot", "Reboot VM", "Reset a running virtual machine"},
		{"vm_suspend", "Suspend VM", "Suspend a virtual machine to RAM"},
		{"vm_resume", "Resume VM", "Resume a suspended virtual machine"},
		{"ct_start", "Start Container", "Start an LXC container"},
		{"ct_shutdown", "Shutdown Container", "Gracefully stop an LXC container"},
		{"ct_stop", "Force Stop Container", "Hard stop an LXC container"},
	}
	specs := make([]plugin.ActionSpec, 0, len(lifecycle)+6)
	for _, a := range lifecycle {
		id := "vmid"
		if strings.HasPrefix(a.name, "ct_") {
			id = "vmid"
		}
		specs = append(specs, plugin.ActionSpec{
			Name: a.name, Label: a.label, Description: a.desc,
			Options: guestOpts(a.name, id),
		})
	}

	specs = append(specs,
		plugin.ActionSpec{
			Name:        "vm_create",
			Label:       "Create VM",
			Description: "Create a QEMU/KVM virtual machine on a Proxmox node",
			Options: []plugin.OptionSchema{
				{Name: "node", Type: "string", Required: true, Description: "Target node"},
				{Name: "vmid", Type: "int", Required: true, Description: "New VM ID"},
				{Name: "name", Type: "string", Required: true, Description: "VM name"},
				{Name: "memory", Type: "int", Required: false, Default: 2048, Description: "Memory in MiB"},
				{Name: "cores", Type: "int", Required: false, Default: 2, Description: "CPU cores"},
				{Name: "ostype", Type: "string", Required: false, Default: "l26", Description: "Guest OS type (l26, win11, ...)"},
				{Name: "storage", Type: "string", Required: false, Description: "Storage for the disk (e.g. local-lvm)"},
				{Name: "diskSize", Type: "int", Required: false, Default: 32, Description: "Disk size in GiB"},
				{Name: "bridge", Type: "string", Required: false, Default: "vmbr0", Description: "Network bridge"},
				{Name: "iso", Type: "string", Required: false, Description: "ISO volume to attach as CD-ROM (e.g. local:iso/x.iso)"},
			},
		},
		plugin.ActionSpec{
			Name:        "ct_create",
			Label:       "Create Container",
			Description: "Create an LXC container on a Proxmox node",
			Options: []plugin.OptionSchema{
				{Name: "node", Type: "string", Required: true, Description: "Target node"},
				{Name: "vmid", Type: "int", Required: true, Description: "New CT ID"},
				{Name: "hostname", Type: "string", Required: true, Description: "Container hostname"},
				{Name: "ostemplate", Type: "string", Required: true, Description: "Template volume (e.g. local:vztmpl/debian-12.tar.zst)"},
				{Name: "storage", Type: "string", Required: false, Default: "local-lvm", Description: "Storage for the rootfs"},
				{Name: "diskSize", Type: "int", Required: false, Default: 8, Description: "Rootfs size in GiB"},
				{Name: "memory", Type: "int", Required: false, Default: 512, Description: "Memory in MiB"},
				{Name: "cores", Type: "int", Required: false, Default: 1, Description: "CPU cores"},
				{Name: "bridge", Type: "string", Required: false, Default: "vmbr0", Description: "Network bridge"},
			},
		},
		plugin.ActionSpec{
			Name:        "update_config",
			Label:       "Update Guest Config",
			Description: "Set a single guest configuration option (memory, cores, boot order, startup/shutdown behaviour, disks, NICs, CD/ISO, passthrough)",
			Options: []plugin.OptionSchema{
				{Name: "node", Type: "string", Required: true, Description: "Proxmox node hosting the guest"},
				{Name: "guestType", Type: "select", Required: true, Enum: []string{"qemu", "lxc"}, Description: "Guest type"},
				{Name: "vmid", Type: "int", Required: true, Description: "Guest VMID"},
				{Name: "key", Type: "string", Required: true, Description: "Config key (memory, cores, boot, onboot, startup, net0, scsi0, ide2, hostpci0, ...)"},
				{Name: "value", Type: "string", Required: true, Description: "Config value"},
			},
		},
		plugin.ActionSpec{
			Name:        "resize_disk",
			Label:       "Resize Disk",
			Description: "Grow a guest disk (disk must be keyed, e.g. scsi0 / rootfs)",
			Options: []plugin.OptionSchema{
				{Name: "node", Type: "string", Required: true, Description: "Proxmox node hosting the guest"},
				{Name: "guestType", Type: "select", Required: true, Enum: []string{"qemu", "lxc"}, Description: "Guest type"},
				{Name: "vmid", Type: "int", Required: true, Description: "Guest VMID"},
				{Name: "disk", Type: "string", Required: true, Description: "Disk key (scsi0, virtio0, rootfs, ...)"},
				{Name: "size", Type: "string", Required: true, Description: "New absolute size (e.g. 64G) or increment (e.g. +10G)"},
			},
		},
		plugin.ActionSpec{
			Name:        "delete_guest",
			Label:       "Delete Guest",
			Description: "Permanently destroy a virtual machine or container",
			Options: []plugin.OptionSchema{
				{Name: "node", Type: "string", Required: true, Description: "Proxmox node hosting the guest"},
				{Name: "guestType", Type: "select", Required: true, Enum: []string{"qemu", "lxc"}, Description: "Guest type"},
				{Name: "vmid", Type: "int", Required: true, Description: "Guest VMID"},
			},
		},
	)
	return specs
}

// Compile turns an action into a declarative Ansible task. Guest mutations that
// have no faithful native module (graceful shutdown, suspend/resume, arbitrary
// config keys, disk resize) compile to ansible.builtin.uri calls against the
// real Proxmox HTTP API; guest creation uses the native community.proxmox
// modules. Secrets are referenced via vault variables and masked.
func (p *Plugin) Compile(h plugin.HostFacts, action string, opts map[string]any) (plugin.TaskSpec, error) {
	norm := strings.ToLower(strings.TrimSpace(action))
	node := optString(opts, "node")
	vmid := optInt(opts, "vmid")

	masked := map[string]string{
		"proxmox_api_token_secret": "•••••••• (vault: proxmox_api_token_secret)",
		"proxmox_api_password":     "•••••••• (vault: proxmox_api_password)",
	}

	switch norm {
	case "vm_start", "vm_shutdown", "vm_stop", "vm_reboot", "vm_suspend", "vm_resume",
		"ct_start", "ct_shutdown", "ct_stop":
		if node == "" || vmid == 0 {
			return plugin.TaskSpec{}, fmt.Errorf("node and vmid are required")
		}
		guestType := "qemu"
		if strings.HasPrefix(norm, "ct_") {
			guestType = "lxc"
		}
		statusAction := strings.TrimPrefix(norm, "vm_")
		statusAction = strings.TrimPrefix(statusAction, "ct_")
		// Map ATC verbs to the Proxmox status sub-endpoint.
		if norm == "vm_stop" || norm == "ct_stop" {
			statusAction = "stop"
		}
		return uriTask(h, fmt.Sprintf("POST /nodes/%s/%s/%d/status/%s", node, guestType, vmid, statusAction),
			"/nodes/"+node+"/"+guestType+"/"+strconv.Itoa(vmid)+"/status/"+statusAction, nil, masked), nil

	case "vm_create":
		return compileVMCreate(h, opts, masked)
	case "ct_create":
		return compileCTCreate(h, opts, masked)

	case "update_config":
		guestType := optString(opts, "guestType")
		key := optString(opts, "key")
		value := optString(opts, "value")
		if node == "" || vmid == 0 || guestType == "" || key == "" {
			return plugin.TaskSpec{}, fmt.Errorf("node, guestType, vmid and key are required")
		}
		body := url.Values{}
		body.Set(key, value)
		return uriTask(h, fmt.Sprintf("PUT /nodes/%s/%s/%d/config", node, guestType, vmid),
			"/nodes/"+node+"/"+guestType+"/"+strconv.Itoa(vmid)+"/config", body, masked), nil

	case "resize_disk":
		guestType := optString(opts, "guestType")
		disk := optString(opts, "disk")
		size := optString(opts, "size")
		if node == "" || vmid == 0 || guestType == "" || disk == "" || size == "" {
			return plugin.TaskSpec{}, fmt.Errorf("node, guestType, vmid, disk and size are required")
		}
		body := url.Values{}
		body.Set("disk", disk)
		body.Set("size", size)
		return uriTask(h, fmt.Sprintf("PUT /nodes/%s/%s/%d/resize", node, guestType, vmid),
			"/nodes/"+node+"/"+guestType+"/"+strconv.Itoa(vmid)+"/resize", body, masked), nil

	case "delete_guest":
		guestType := optString(opts, "guestType")
		if node == "" || vmid == 0 || guestType == "" {
			return plugin.TaskSpec{}, fmt.Errorf("node, guestType and vmid are required")
		}
		return uriTask(h, fmt.Sprintf("DELETE /nodes/%s/%s/%d", node, guestType, vmid),
			"/nodes/"+node+"/"+guestType+"/"+strconv.Itoa(vmid), nil, masked), nil
	}

	return plugin.TaskSpec{}, fmt.Errorf("unsupported proxmox action: %s", action)
}

// apiEndpoint returns the API base URL derived from host facts.
func apiEndpoint(h plugin.HostFacts) string {
	cfg := configFromFacts(h)
	return cfg.baseURL()
}

// uriTask builds a TaskSpec that issues a real Proxmox HTTP API call via
// ansible.builtin.uri. Credentials are supplied through vault-referenced
// variables and registered for masking.
func uriTask(h plugin.HostFacts, name, path string, body url.Values, masked map[string]string) plugin.TaskSpec {
	method := "POST"
	if strings.HasPrefix(name, "PUT ") {
		method = "PUT"
	} else if strings.HasPrefix(name, "DELETE ") {
		method = "DELETE"
	}
	params := map[string]any{
		"url":                    apiEndpoint(h) + "/api2/json" + path,
		"method":                 method,
		"headers":                apiAuthHeaders(),
		"validate_certs":         false,
		"status_code":            []int{200},
		"return_content":         true,
		"timeout":                30,
	}
	if body != nil && len(body) > 0 {
		params["body_format"] = "form-urlencoded"
		params["body"] = parseForm(body)
	}
	return plugin.TaskSpec{
		Name:          "Proxmox: " + name,
		Steps:         []plugin.TaskStep{{Name: name, Module: "ansible.builtin.uri", Params: params}},
		SecretsMasked: masked,
	}
}

func apiAuthHeaders() map[string]any {
	return map[string]any{
		"Authorization": "PVEAPIToken={{ proxmox_api_user }}!{{ proxmox_api_token_id }}={{ proxmox_api_token_secret }}",
	}
}

func parseForm(v url.Values) map[string]any {
	out := make(map[string]any, len(v))
	for k := range v {
		out[k] = v.Get(k)
	}
	return out
}

func compileVMCreate(h plugin.HostFacts, opts map[string]any, masked map[string]string) (plugin.TaskSpec, error) {
	node := optString(opts, "node")
	vmid := optInt(opts, "vmid")
	name := optString(opts, "name")
	if node == "" || vmid == 0 || name == "" {
		return plugin.TaskSpec{}, fmt.Errorf("node, vmid and name are required")
	}
	storage := optString(opts, "storage")
	if storage == "" {
		storage = "local-lvm"
	}
	diskSize := optIntDefault(opts, "diskSize", 32)
	bridge := optString(opts, "bridge")
	if bridge == "" {
		bridge = "vmbr0"
	}
	params := map[string]any{
		"api_host":         configFromFacts(h).Host,
		"api_user":         "{{ proxmox_api_user }}",
		"api_token_id":     "{{ proxmox_api_token_id }}",
		"api_token_secret": "{{ proxmox_api_token_secret }}",
		"node":             node,
		"vmid":             vmid,
		"name":             name,
		"memory":           optIntDefault(opts, "memory", 2048),
		"cores":            optIntDefault(opts, "cores", 2),
		"ostype":           firstNonEmpty(optString(opts, "ostype"), "l26"),
		"scsihw":           "virtio-scsi-single",
		"scsi":             map[string]any{"scsi0": fmt.Sprintf("%s:%d", storage, diskSize)},
		"net":              map[string]any{"net0": fmt.Sprintf("virtio,bridge=%s", bridge)},
		"boot":             "order=scsi0;ide2",
		"state":            "present",
		"validate_certs":   false,
	}
	if iso := optString(opts, "iso"); iso != "" {
		params["ide"] = map[string]any{"ide2": iso + ",media=cdrom"}
	}
	return plugin.TaskSpec{
		Name:          fmt.Sprintf("Create VM %d (%s) on %s", vmid, name, node),
		Steps:         []plugin.TaskStep{{Name: fmt.Sprintf("Create VM %d", vmid), Module: "community.proxmox.proxmox_kvm", Params: params}},
		SecretsMasked: masked,
	}, nil
}

func compileCTCreate(h plugin.HostFacts, opts map[string]any, masked map[string]string) (plugin.TaskSpec, error) {
	node := optString(opts, "node")
	vmid := optInt(opts, "vmid")
	hostname := optString(opts, "hostname")
	ostemplate := optString(opts, "ostemplate")
	if node == "" || vmid == 0 || hostname == "" || ostemplate == "" {
		return plugin.TaskSpec{}, fmt.Errorf("node, vmid, hostname and ostemplate are required")
	}
	storage := firstNonEmpty(optString(opts, "storage"), "local-lvm")
	diskSize := optIntDefault(opts, "diskSize", 8)
	bridge := firstNonEmpty(optString(opts, "bridge"), "vmbr0")
	params := map[string]any{
		"api_host":         configFromFacts(h).Host,
		"api_user":         "{{ proxmox_api_user }}",
		"api_token_id":     "{{ proxmox_api_token_id }}",
		"api_token_secret": "{{ proxmox_api_token_secret }}",
		"node":             node,
		"vmid":             vmid,
		"hostname":         hostname,
		"ostemplate":       ostemplate,
		"storage":          storage,
		"disk_volume":      map[string]any{"storage": storage, "size": diskSize},
		"memory":           optIntDefault(opts, "memory", 512),
		"cores":            optIntDefault(opts, "cores", 1),
		"netif":            map[string]any{"net0": fmt.Sprintf("name=eth0,bridge=%s,ip=dhcp", bridge)},
		"state":            "present",
		"validate_certs":   false,
	}
	return plugin.TaskSpec{
		Name:          fmt.Sprintf("Create LXC %d (%s) on %s", vmid, hostname, node),
		Steps:         []plugin.TaskStep{{Name: fmt.Sprintf("Create LXC %d", vmid), Module: "community.proxmox.proxmox", Params: params}},
		SecretsMasked: masked,
	}, nil
}

func optString(opts map[string]any, key string) string {
	if opts == nil {
		return ""
	}
	v, ok := opts[key]
	if !ok || v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	default:
		return strings.TrimSpace(strAny(t))
	}
}

func optInt(opts map[string]any, key string) int {
	return optIntDefault(opts, key, 0)
}

func optIntDefault(opts map[string]any, key string, def int) int {
	s := optString(opts, key)
	if s == "" {
		return def
	}
	if v, err := strconv.Atoi(s); err == nil {
		return v
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return int(f)
	}
	return def
}
