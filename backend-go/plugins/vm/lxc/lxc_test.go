package lxc_test

import (
	"context"
	"strings"
	"testing"

	"air-traffic-control/backend/pkg/plugin"
	_ "air-traffic-control/backend/plugins/all"
	"air-traffic-control/backend/plugins/vm/lxc"
)

type mockRunner struct {
	responses map[string]string
}

// RunCommand returns the response for the longest matching pattern so a more
// specific key wins deterministically over an incidental substring match.
func (m *mockRunner) RunCommand(_ context.Context, cmd string) (string, error) {
	best, bestLen := "", -1
	for pattern, resp := range m.responses {
		if strings.Contains(cmd, pattern) && len(pattern) > bestLen {
			best, bestLen = resp, len(pattern)
		}
	}
	if bestLen < 0 {
		return "", nil
	}
	return best, nil
}

func TestLXCPlugin_Registered(t *testing.T) {
	p, ok := plugin.Get("vm/lxc")
	if !ok {
		t.Fatal("vm/lxc plugin not registered")
	}
	if p.Meta().Kind != plugin.KindVM {
		t.Errorf("unexpected kind: %v", p.Meta().Kind)
	}
	if _, ok := p.(plugin.Reader); !ok {
		t.Error("vm/lxc must implement plugin.Reader")
	}
	if _, ok := p.(plugin.Actor); !ok {
		t.Error("vm/lxc must implement plugin.Actor")
	}
}

// Probe must report honestly when virsh is not installed — never a fabricated
// "detected".
func TestLXCPlugin_ProbeNoVirsh(t *testing.T) {
	p, _ := plugin.Get("vm/lxc")
	r := &mockRunner{responses: map[string]string{
		"command -v virsh": "NO_VIRSH",
	}}
	det, err := p.Probe(context.Background(), r, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatalf("probe errored: %v", err)
	}
	if det.Detected {
		t.Fatalf("must not detect lxc without virsh: %+v", det)
	}
	if det.Status != "UNAVAILABLE" {
		t.Errorf("expected UNAVAILABLE, got %q", det.Status)
	}
}

// Probe must report honestly when virsh exists but the LXC driver is not
// reachable (e.g. libvirtd running for QEMU/KVM only).
func TestLXCPlugin_ProbeDriverUnavailable(t *testing.T) {
	p, _ := plugin.Get("vm/lxc")
	r := &mockRunner{responses: map[string]string{
		"command -v virsh": "/usr/bin/virsh",
		"lxc:/// version":  "error: failed to connect to the hypervisor",
	}}
	det, err := p.Probe(context.Background(), r, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatalf("probe errored: %v", err)
	}
	if det.Detected {
		t.Fatalf("must not detect lxc when driver unreachable: %+v", det)
	}
	if !strings.Contains(det.Error, "no libvirt LXC host") {
		t.Errorf("expected honest driver error, got %q", det.Error)
	}
}

// Read must NOT fabricate containers when the host is unavailable.
func TestLXCPlugin_ReadNoLibvirtFabricatesNothing(t *testing.T) {
	p, _ := plugin.Get("vm/lxc")
	reader := p.(plugin.Reader)

	r := &mockRunner{responses: map[string]string{
		"command -v virsh":   "/usr/bin/virsh",
		"lxc:/// list --all": "error: failed to connect to the hypervisor\nLXC_LIST_FAILED",
	}}
	data, err := reader.Read(context.Background(), r, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatalf("read should return a structured honest result, not an error: %v", err)
	}
	res, ok := data.(lxc.ReadResult)
	if !ok {
		t.Fatalf("unexpected payload type %T", data)
	}
	if res.HasLibvirt {
		t.Error("hasLibvirt must be false when the host is unreachable")
	}
	if len(res.Containers) != 0 {
		t.Errorf("no containers may be fabricated, got %d", len(res.Containers))
	}
	if res.LibvirtError == "" {
		t.Error("an honest error string must be present")
	}
	if res.Verified {
		t.Error("plugin must not claim verification without a real host")
	}
	if len(res.Unimplemented) == 0 {
		t.Error("unimplemented operations must still be surfaced when unavailable")
	}
}

// Read must parse real virsh output into containers with real specs.
func TestLXCPlugin_ReadParsesContainers(t *testing.T) {
	p, _ := plugin.Get("vm/lxc")
	reader := p.(plugin.Reader)

	list := " Id   Name       State\n" +
		"----------------------------\n" +
		" 1234 web-01     running\n" +
		" -    db-01      shut off\n"

	r := &mockRunner{responses: map[string]string{
		"command -v virsh":   "/usr/bin/virsh",
		"lxc:/// list --all": list,
		"dumpxml 'web-01'":   "<domain type='lxc'><name>web-01</name><vcpu>4</vcpu><memory unit='KiB'>2097152</memory></domain>",
		"dumpxml 'db-01'":    "<domain type='lxc'><name>db-01</name><vcpu>2</vcpu><memory unit='KiB'>1048576</memory></domain>",
	}}
	data, err := reader.Read(context.Background(), r, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	res, ok := data.(lxc.ReadResult)
	if !ok {
		t.Fatalf("unexpected payload type %T", data)
	}
	if !res.HasLibvirt {
		t.Fatal("expected hasLibvirt true")
	}
	if len(res.Containers) != 2 {
		t.Fatalf("expected 2 containers, got %d", len(res.Containers))
	}
	if res.Containers[0].Name != "web-01" || res.Containers[0].Status != "RUNNING" {
		t.Errorf("unexpected first container: %+v", res.Containers[0])
	}
	if res.Containers[1].Status != "STOPPED" {
		t.Errorf("unexpected second container status: %+v", res.Containers[1])
	}
	if res.Containers[0].VCPUs != 4 || res.Containers[0].RAMMiB != 2048 {
		t.Errorf("specs not parsed from dumpxml: %+v", res.Containers[0])
	}
	if len(res.Unimplemented) == 0 {
		t.Error("unimplemented operations must be reported to the UI")
	}
	if res.Verified {
		t.Error("plugin must not claim verification without a real host")
	}
}

func TestLXCPlugin_Compile(t *testing.T) {
	p, _ := plugin.Get("vm/lxc")
	actor := p.(plugin.Actor)

	cases := map[string]string{
		"start":    "running",
		"shutdown": "shutdown",
		"destroy":  "destroyed",
		"suspend":  "paused",
	}
	for action, wantState := range cases {
		task, err := actor.Compile(plugin.HostFacts{NodeID: "n1"}, action, map[string]any{"name": "web-01"})
		if err != nil {
			t.Fatalf("compile %s failed: %v", action, err)
		}
		if len(task.Steps) != 1 || task.Steps[0].Module != "community.general.virt" {
			t.Errorf("%s: unexpected steps %+v", action, task.Steps)
		}
		if task.Steps[0].Params["state"] != wantState {
			t.Errorf("%s: expected state %s, got %v", action, wantState, task.Steps[0].Params["state"])
		}
		if task.Steps[0].Params["uri"] != "lxc:///" {
			t.Errorf("%s: expected lxc:/// uri, got %v", action, task.Steps[0].Params["uri"])
		}
	}

	if _, err := actor.Compile(plugin.HostFacts{}, "start", map[string]any{}); err == nil {
		t.Error("expected error when name is missing")
	}
	if _, err := actor.Compile(plugin.HostFacts{}, "clone", map[string]any{"name": "x"}); err == nil {
		t.Error("expected error for unsupported action")
	}
}
