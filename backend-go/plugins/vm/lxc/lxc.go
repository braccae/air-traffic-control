package lxc

// Package lxc is a STUB Go plugin for managing LXC system containers through a
// libvirt LXC driver (the `lxc:///` connection URI).
//
// ---------------------------------------------------------------------------
// UNTESTED — NO REAL LIBVIRT LXC HOST WAS AVAILABLE
// ---------------------------------------------------------------------------
// This plugin has NOT been verified against a real libvirt LXC host. It was
// authored without access to a machine running libvirtd with the lxc driver
// enabled, so every command string, parse path, and Ansible module compilation
// below is a best-effort implementation based on documented `virsh` behaviour
// and the existing `vm/libvirt` plugin precedent. It has only been exercised
// against unit-test mock runners (see lxc_test.go).
//
// Steps that remain UNVERIFIED (see README.md for the full re-open checklist):
//   - `virsh -c lxc:/// list --all` output format and the exact state strings
//     returned for LXC domains (assumed to match the QEMU driver column layout).
//   - `virsh -c lxc:/// dumpxml <name>` element layout for `<vcpu>` and
//     `<memory unit='KiB'>` on LXC domains (assumed identical to QEMU domains).
//   - Whether `community.general.virt` supports the `uri: lxc:///` parameter
//     for start/shutdown/destroy/suspend, and whether `become: true` /
//     privilege escalation is required or sufficient for the LXC driver.
//   - Behaviour of `virsh -c lxc:/// version` as an availability probe on
//     hosts where libvirtd is present but the lxc driver is not.
//
// Zero-Mock Policy (AGENTS.md §2): Read NEVER fabricates container data. When
// libvirt or the lxc driver is unavailable, Read returns a structured result
// with HasLibvirt=false and an explicit error string so the UI reports the host
// as unavailable instead of inventing containers.
//
// Mutation support is intentionally limited. Operations that were not
// implemented are enumerated in UnimplementedOperations and surfaced verbatim
// in the Read payload (and therefore the UI); they are NOT registered as
// runnable Actions, so no fabricated success is possible.

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"air-traffic-control/backend/pkg/plugin"
)

// lxcURI is the libvirt connection URI for the LXC system-container driver.
const lxcURI = "lxc:///"

func init() {
	_ = plugin.Register(New())
}

// Plugin implements LXC system-container inspection and a limited lifecycle
// via the libvirt LXC driver (virsh -c lxc:///).
type Plugin struct{}

func New() *Plugin { return &Plugin{} }

func (p *Plugin) Meta() plugin.Meta {
	return plugin.Meta{
		ID:    "vm/lxc",
		Kind:  plugin.KindVM,
		Name:  "lxc",
		Label: "Linux Containers (LXC)",
		Icon:  "deployed_code",
		Description: "LXC system containers managed through the libvirt LXC driver " +
			"(lxc:///) — UNTESTED against a real libvirt host",
		Order: 20,
	}
}

func (p *Plugin) Compatible(h plugin.HostFacts) bool {
	return h.OSFamily != "windows" && h.OSFamily != "router_switch"
}

func (p *Plugin) Requirements() plugin.Requirements {
	return plugin.Requirements{
		HostPackages: map[string][]string{
			"dnf": {"libvirt-client", "libvirt-daemon-lxc", "lxc"},
			"apt": {"libvirt-clients", "libvirt-daemon-system", "lxc"},
			"apk": {"libvirt-client", "lxc"},
		},
		ControllerCollections: []string{
			"community.general",
		},
	}
}

// Container is a single LXC domain as reported by libvirt.
type Container struct {
	Name   string  `json:"name"`
	ID     string  `json:"id"`     // virsh domain id; "-" when not running
	Status string  `json:"status"` // RUNNING | STOPPED | SUSPENDED | UNKNOWN
	VCPUs  int     `json:"vcpus"`  // -1 when not discoverable
	RAMMiB int64   `json:"ramMiB"` // -1 when not discoverable
	RAMGiB float64 `json:"ramGiB"`
	Type   string  `json:"type"` // "lxc"
}

// UnimplementedOperation documents a mutation this stub deliberately does NOT
// provide. These are surfaced in the Read payload so the UI can render them as
// explicitly unavailable rather than silently omitting them.
type UnimplementedOperation struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

// UnimplementedOperations lists every mutation that has been intentionally left
// unimplemented in this stub. Keep this in sync with README.md.
var UnimplementedOperations = []UnimplementedOperation{
	{Name: "create", Reason: "Container provisioning (lxc-create / virt-install --connect lxc:///) is not implemented."},
	{Name: "undefine", Reason: "Destroying and undefining a container domain is not implemented."},
	{Name: "autostart", Reason: "Toggling domain autostart is not implemented."},
	{Name: "console", Reason: "Attaching an interactive console (virsh -c lxc:/// console) is not implemented."},
	{Name: "snapshot", Reason: "Snapshot/clone operations are not implemented."},
}

// Detection of libvirt + lxc driver availability.
func (p *Plugin) Probe(ctx context.Context, r plugin.Runner, h plugin.HostFacts) (plugin.Detection, error) {
	if r == nil {
		return plugin.Detection{Detected: false, Status: "OFFLINE", Error: "runner is nil"}, nil
	}

	binOut, err := r.RunCommand(ctx, "command -v virsh 2>&1 || which virsh 2>&1 || echo 'NO_VIRSH'")
	if err != nil || strings.Contains(binOut, "NO_VIRSH") || !strings.Contains(binOut, "virsh") {
		return plugin.Detection{
			Detected: false,
			Status:   "UNAVAILABLE",
			Error:    "virsh binary not found (install libvirt-client)",
		}, nil
	}

	// Probe the LXC driver explicitly: a host may run libvirtd for QEMU/KVM
	// without the lxc driver being available.
	verOut, err := r.RunCommand(ctx, "virsh -c "+lxcURI+" version 2>&1 || echo 'LXC_DRIVER_UNAVAILABLE'")
	if err != nil || strings.Contains(verOut, "LXC_DRIVER_UNAVAILABLE") ||
		strings.Contains(strings.ToLower(verOut), "error") ||
		strings.Contains(strings.ToLower(verOut), "failed to connect") {
		return plugin.Detection{
			Detected: false,
			Status:   "UNAVAILABLE",
			Error:    "no libvirt LXC host available (virsh -c lxc:/// version failed): " + firstLine(verOut),
		}, nil
	}

	return plugin.Detection{
		Detected: true,
		Status:   "LXC driver active",
		Details:  "libvirt LXC driver reachable via " + lxcURI,
	}, nil
}

var (
	reVCPU   = regexp.MustCompile(`<vcpu[^>]*>(\d+)</vcpu>`)
	reMemory = regexp.MustCompile(`<memory[^>]*unit='KiB'[^>]*>(\d+)</memory>`)
)

// Read returns the real state of LXC containers on the host. It never fabricates
// containers: on any failure it returns a result with HasLibvirt=false and a
// human-readable error, leaving Containers empty.
func (p *Plugin) Read(ctx context.Context, r plugin.Runner, h plugin.HostFacts) (any, error) {
	if r == nil {
		return nil, fmt.Errorf("runner is nil")
	}

	binOut, err := r.RunCommand(ctx, "command -v virsh 2>&1 || which virsh 2>&1 || echo 'NO_VIRSH'")
	if err != nil || strings.Contains(binOut, "NO_VIRSH") || !strings.Contains(binOut, "virsh") {
		return p.unavailable("virsh binary not found on host (install libvirt-client)"), nil
	}

	listOut, err := r.RunCommand(ctx, "virsh -c "+lxcURI+" list --all 2>&1 || echo 'LXC_LIST_FAILED'")
	if err != nil {
		return p.unavailable("failed to execute virsh list: " + err.Error()), nil
	}
	if strings.Contains(listOut, "LXC_LIST_FAILED") || strings.Contains(strings.ToLower(listOut), "failed to connect") {
		return p.unavailable("no libvirt LXC host available: " + firstLine(listOut)), nil
	}

	containers := parseList(listOut)

	// Enrich each container with real specs from its domain XML. Specs that
	// cannot be read stay at -1 (unknown) rather than being fabricated.
	for i := range containers {
		xmlOut, xmlErr := r.RunCommand(ctx, "virsh -c "+lxcURI+" dumpxml "+shellQuote(containers[i].Name)+" 2>&1 || true")
		if xmlErr != nil {
			continue
		}
		applySpec(&containers[i], xmlOut)
	}

	return ReadResult{
		HasLibvirt:    true,
		URI:           lxcURI,
		DriverActive:  true,
		Containers:    containers,
		Unimplemented: UnimplementedOperations,
		Verified:      false,
		VerifiedNote:  "UNTESTED: this plugin has not been validated against a real libvirt LXC host. Container data above is read live from virsh but its parsing is unverified.",
	}, nil
}

// ReadResult is the JSON payload returned by Read and consumed by the LXC tab.
type ReadResult struct {
	HasLibvirt    bool                     `json:"hasLibvirt"`
	LibvirtError  string                   `json:"libvirtError,omitempty"`
	URI           string                   `json:"uri"`
	DriverActive  bool                     `json:"driverActive"`
	Containers    []Container              `json:"containers"`
	Unimplemented []UnimplementedOperation `json:"unimplemented"`
	Verified      bool                     `json:"verified"`
	VerifiedNote  string                   `json:"verifiedNote"`
}

func (p *Plugin) unavailable(reason string) ReadResult {
	return ReadResult{
		HasLibvirt:    false,
		LibvirtError:  reason,
		URI:           lxcURI,
		DriverActive:  false,
		Containers:    []Container{},
		Unimplemented: UnimplementedOperations,
		Verified:      false,
		VerifiedNote:  "UNTESTED: no libvirt LXC host available in this environment; no container data is reported.",
	}
}

// parseList parses `virsh list --all` output. Rows have the form:
//
//	 Id   Name              State
//	---------------------------------
//	 1234 web-01            running
//	 -    db-01             shut off
func parseList(out string) []Container {
	containers := []Container{}
	for _, line := range strings.Split(out, "\n") {
		tokens := strings.Fields(line)
		if len(tokens) < 3 {
			continue
		}
		if tokens[0] == "Id" || strings.HasPrefix(tokens[0], "---") {
			continue
		}
		name := tokens[1]
		state := strings.ToUpper(strings.Join(tokens[2:], " "))
		containers = append(containers, Container{
			Name:   name,
			ID:     tokens[0],
			Status: normalizeState(state),
			VCPUs:  -1,
			RAMMiB: -1,
			RAMGiB: -1,
			Type:   "lxc",
		})
	}
	return containers
}

func normalizeState(state string) string {
	switch {
	case strings.Contains(state, "RUNNING"):
		return "RUNNING"
	case strings.Contains(state, "SHUT OFF"):
		return "STOPPED"
	case strings.Contains(state, "PAUSED") || strings.Contains(state, "SUSPENDED"):
		return "SUSPENDED"
	case strings.Contains(state, "CRASHED"):
		return "CRASHED"
	default:
		return "UNKNOWN"
	}
}

// applySpec extracts real vCPU count and memory from a domain XML dump. Values
// that cannot be parsed are left untouched (unknown == -1).
func applySpec(c *Container, xml string) {
	if m := reVCPU.FindStringSubmatch(xml); len(m) == 2 {
		if n, err := strconv.Atoi(m[1]); err == nil {
			c.VCPUs = n
		}
	}
	if m := reMemory.FindStringSubmatch(xml); len(m) == 2 {
		if kib, err := strconv.ParseInt(m[1], 10, 64); err == nil {
			c.RAMMiB = kib / 1024
			c.RAMGiB = float64(kib) / (1024 * 1024)
		}
	}
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		return strings.TrimSpace(s[:idx])
	}
	return s
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// Actions returns the mutations this stub actually implements. Each compiles to
// a declarative community.general.virt task against the lxc:/// URI.
//
// NOTE: community.general.virt with uri=lxc:/// is UNVERIFIED. See the package
// doc comment and README.md.
func (p *Plugin) Actions() []plugin.ActionSpec {
	nameOpt := plugin.OptionSchema{Name: "name", Type: "string", Required: true, Description: "LXC domain name"}
	return []plugin.ActionSpec{
		{
			Name:        "start",
			Label:       "Start Container",
			Description: "Starts an LXC container domain (virsh -c lxc:/// start)",
			Options:     []plugin.OptionSchema{nameOpt},
		},
		{
			Name:        "shutdown",
			Label:       "Graceful Shutdown",
			Description: "Requests a graceful shutdown of an LXC container domain",
			Options:     []plugin.OptionSchema{nameOpt},
		},
		{
			Name:        "destroy",
			Label:       "Force Stop",
			Description: "Immediately destroys (force-stops) an LXC container domain",
			Options:     []plugin.OptionSchema{nameOpt},
		},
		{
			Name:        "suspend",
			Label:       "Suspend Container",
			Description: "Pauses an LXC container domain",
			Options:     []plugin.OptionSchema{nameOpt},
		},
	}
}

// Compile maps an LXC action onto a declarative community.general.virt task.
func (p *Plugin) Compile(h plugin.HostFacts, action string, opts map[string]any) (plugin.TaskSpec, error) {
	norm := strings.ToLower(strings.TrimSpace(action))
	name, _ := opts["name"].(string)
	name = strings.TrimSpace(name)
	if name == "" {
		return plugin.TaskSpec{}, fmt.Errorf("lxc domain name is required")
	}

	stateMap := map[string]string{
		"start":    "running",
		"shutdown": "shutdown",
		"destroy":  "destroyed",
		"suspend":  "paused",
	}
	state, ok := stateMap[norm]
	if !ok {
		return plugin.TaskSpec{}, fmt.Errorf("unsupported lxc action: %s", action)
	}

	label := map[string]string{
		"start":    "Start",
		"shutdown": "Graceful shutdown of",
		"destroy":  "Force stop of",
		"suspend":  "Suspend",
	}[norm]

	return plugin.TaskSpec{
		Name: fmt.Sprintf("%s LXC container %s", label, name),
		Steps: []plugin.TaskStep{
			{
				Name:   fmt.Sprintf("%s LXC container %s", label, name),
				Module: "community.general.virt",
				Params: map[string]any{
					"name":   name,
					"state":  state,
					"uri":    lxcURI,
					"become": true,
				},
			},
		},
	}, nil
}
