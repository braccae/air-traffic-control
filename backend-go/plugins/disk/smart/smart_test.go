package smart_test

import (
	"context"
	"strings"
	"testing"

	"air-traffic-control/backend/pkg/plugin"
	_ "air-traffic-control/backend/plugins/all"
)

type mockRunner struct {
	responses map[string]string
}

func (m *mockRunner) RunCommand(ctx context.Context, cmd string) (string, error) {
	if strings.Contains(cmd, "===SMART===") {
		return m.responses["===SMART==="], nil
	}
	for pattern, resp := range m.responses {
		if strings.Contains(cmd, pattern) {
			return resp, nil
		}
	}
	return "", nil
}

func TestSMARTPlugin_ProbeAndRead(t *testing.T) {
	p, ok := plugin.Get("disk/smart")
	if !ok {
		t.Fatalf("disk/smart not registered")
	}

	runner := &mockRunner{
		responses: map[string]string{
			"command -v lsblk": "lsblk",
			"smartctl --scan":  "/dev/nvme0n1 -d nvme # /dev/nvme0n1, NVM Express device",
			"===SMART===":      "{\"blockdevices\": [{\"name\": \"nvme0n1\", \"size\": 1000204886016, \"type\": \"disk\", \"model\": \"Samsung SSD 980\", \"rota\": false, \"children\": [{\"name\": \"nvme0n1p1\", \"size\": 536870912, \"type\": \"part\", \"fstype\": \"vfat\", \"mountpoints\": [\"/boot/efi\"]}]}]}\n===SMART===\n/dev/nvme0n1 -d nvme # /dev/nvme0n1, NVM Express device",
		},
	}

	det, err := p.Probe(context.Background(), runner, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatal(err)
	}
	if !det.Detected || !strings.Contains(det.Details, "SMART enabled") {
		t.Errorf("unexpected probe result: %+v", det)
	}

	reader := p.(plugin.Reader)
	data, err := reader.Read(context.Background(), runner, plugin.HostFacts{OSFamily: "linux"})
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}

	m := data.(map[string]any)
	disks := m["disks"].([]map[string]any)
	if len(disks) != 1 {
		t.Fatalf("expected 1 disk, got %d", len(disks))
	}
	if disks[0]["name"] != "nvme0n1" || disks[0]["smartStatus"] != "PASSED (0 Errors)" {
		t.Errorf("unexpected disk data: %+v", disks[0])
	}
}
