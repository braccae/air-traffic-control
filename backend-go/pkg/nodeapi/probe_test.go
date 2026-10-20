package nodeapi

import (
	"strings"
	"testing"
)

func TestProbeCommand_ContainsMarkers(t *testing.T) {
	cmd := ProbeCommand()
	for _, marker := range []string{"===ARCH===", "===OS_RELEASE===", "===CPUINFO===", "===MEMINFO===", "===DMI==="} {
		if !strings.Contains(cmd, marker) {
			t.Errorf("expected ProbeCommand to contain %q", marker)
		}
	}
}

func TestParseOsRelease(t *testing.T) {
	t.Run("parses PRETTY_NAME with quotes", func(t *testing.T) {
		osRelease := `
NAME="AlmaLinux"
VERSION="10.2 (Lavender Lion)"
ID="almalinux"
PRETTY_NAME="AlmaLinux 10.2 (Lavender Lion)"
VERSION_ID="10.2"
`
		res := ParseOsRelease(osRelease)
		if res != "AlmaLinux 10.2 (Lavender Lion)" {
			t.Fatalf("expected 'AlmaLinux 10.2 (Lavender Lion)', got %q", res)
		}
	})

	t.Run("falls back to NAME and VERSION if PRETTY_NAME is missing", func(t *testing.T) {
		osRelease := `
NAME="Debian GNU/Linux"
VERSION="12 (bookworm)"
ID=debian
`
		res := ParseOsRelease(osRelease)
		if res != "Debian GNU/Linux 12 (bookworm)" {
			t.Fatalf("expected 'Debian GNU/Linux 12 (bookworm)', got %q", res)
		}
	})

	t.Run("falls back gracefully on empty or invalid input", func(t *testing.T) {
		if res := ParseOsRelease(""); res != "Linux (Generic)" {
			t.Fatalf("expected 'Linux (Generic)' for empty, got %q", res)
		}
		if res := ParseOsRelease("foo bar baz"); res != "Linux (Generic)" {
			t.Fatalf("expected 'Linux (Generic)' for invalid, got %q", res)
		}
	})
}

func TestParseCpuInfo(t *testing.T) {
	t.Run("parses AMD/Intel x86_64 cpuinfo with multiple cores", func(t *testing.T) {
		cpuinfo := `
processor	: 0
vendor_id	: AuthenticAMD
model name	: AMD Ryzen 5 PRO 2400G with Radeon Vega Graphics
cpu MHz		: 3600.000

processor	: 1
vendor_id	: AuthenticAMD
model name	: AMD Ryzen 5 PRO 2400G with Radeon Vega Graphics
cpu MHz		: 3600.000

processor	: 2
vendor_id	: AuthenticAMD
model name	: AMD Ryzen 5 PRO 2400G with Radeon Vega Graphics
cpu MHz		: 3600.000

processor	: 3
vendor_id	: AuthenticAMD
model name	: AMD Ryzen 5 PRO 2400G with Radeon Vega Graphics
cpu MHz		: 3600.000
`
		model, vcpus := ParseCpuInfo(cpuinfo)
		if model != "AMD Ryzen 5 PRO 2400G with Radeon Vega Graphics" {
			t.Errorf("expected 'AMD Ryzen 5 PRO 2400G with Radeon Vega Graphics', got %q", model)
		}
		if vcpus != 4 {
			t.Errorf("expected 4 vcpus, got %d", vcpus)
		}
	})

	t.Run("parses ARM64 cpuinfo with Hardware line or Model", func(t *testing.T) {
		cpuinfo := `
processor	: 0
BogoMIPS	: 48.00
Features	: fp asimd evtstrm aes pmull sha1 sha2 crc32 cpuid
CPU implementer	: 0x41
CPU architecture: 8
CPU variant	: 0x0
CPU part	: 0xd03
CPU revision	: 4

processor	: 1
BogoMIPS	: 48.00

Hardware	: Allwinner sun50i-h616
Model		: OrangePi Zero 3
`
		model, vcpus := ParseCpuInfo(cpuinfo)
		if !strings.Contains(model, "OrangePi") && !strings.Contains(model, "Allwinner") && !strings.Contains(model, "ARM64") {
			t.Errorf("expected model containing OrangePi/Allwinner/ARM64, got %q", model)
		}
		if vcpus != 2 {
			t.Errorf("expected 2 vcpus, got %d", vcpus)
		}
	})

	t.Run("handles empty cpuinfo safely", func(t *testing.T) {
		model, vcpus := ParseCpuInfo("")
		if model != "x86_64 / Generic Processor" {
			t.Errorf("expected 'x86_64 / Generic Processor', got %q", model)
		}
		if vcpus != 1 {
			t.Errorf("expected 1 vcpu, got %d", vcpus)
		}
	})
}

func TestParseMemInfo(t *testing.T) {
	t.Run("parses MemTotal in kB from /proc/meminfo and converts to GiB", func(t *testing.T) {
		meminfo := `
MemTotal:       32724484 kB
MemFree:         4823120 kB
MemAvailable:   24123984 kB
`
		gib := ParseMemInfo(meminfo)
		if gib != 31 {
			t.Fatalf("expected 31 GiB, got %d", gib)
		}
	})

	t.Run("parses free -m format fallback", func(t *testing.T) {
		freeOut := `
               total        used        free      shared  buff/cache   available
Mem:           16048        4210        8920         120        2918       11420
Swap:           4096           0        4096
`
		gib := ParseMemInfo(freeOut)
		if gib != 16 {
			t.Fatalf("expected 16 GiB, got %d", gib)
		}
	})

	t.Run("returns minimum 1 GiB for zero or tiny memory", func(t *testing.T) {
		if gib := ParseMemInfo("MemTotal: 512000 kB"); gib != 1 {
			t.Fatalf("expected 1 GiB for 512MB, got %d", gib)
		}
		if gib := ParseMemInfo(""); gib != 1 {
			t.Fatalf("expected 1 GiB for empty, got %d", gib)
		}
	})
}

func TestParseDmiInfo(t *testing.T) {
	t.Run("extracts motherboard and BIOS from DMI sections", func(t *testing.T) {
		dmiOutput := strings.Join([]string{
			"Lenovo",
			"---",
			"3141",
			"---",
			"ThinkCentre M725s",
			"---",
			"Lenovo",
			"---",
			"M25KT2FA",
			"---",
			"2019-04-23",
		}, "\n")

		mobo, bios := ParseDmiInfo(dmiOutput)
		if mobo != "Lenovo 3141 (ThinkCentre M725s)" {
			t.Errorf("expected 'Lenovo 3141 (ThinkCentre M725s)', got %q", mobo)
		}
		if bios != "Lenovo M25KT2FA (2019-04-23)" {
			t.Errorf("expected 'Lenovo M25KT2FA (2019-04-23)', got %q", bios)
		}
	})

	t.Run("handles missing or unreadable DMI gracefully", func(t *testing.T) {
		dmiOutput := "cat: /sys/class/dmi/id/board_vendor: Permission denied\n---\n---"
		mobo, bios := ParseDmiInfo(dmiOutput)
		if len(mobo) == 0 || mobo == "Permission denied" {
			t.Errorf("invalid mobo fallback: %q", mobo)
		}
		if len(bios) == 0 {
			t.Errorf("expected non-empty bios, got %q", bios)
		}
	})
}

func TestParseHardwareProbeOutput(t *testing.T) {
	t.Run("parses complete probe output into full ProbedHardware specification", func(t *testing.T) {
		fullProbe := `
===ARCH===
x86_64
===OS_RELEASE===
NAME="AlmaLinux"
VERSION="10.2 (Lavender Lion)"
ID="almalinux"
PRETTY_NAME="AlmaLinux 10.2 (Lavender Lion)"
===CPUINFO===
processor	: 0
model name	: AMD Ryzen 7 5700X 8-Core Processor
processor	: 1
model name	: AMD Ryzen 7 5700X 8-Core Processor
processor	: 2
model name	: AMD Ryzen 7 5700X 8-Core Processor
processor	: 3
model name	: AMD Ryzen 7 5700X 8-Core Processor
processor	: 4
model name	: AMD Ryzen 7 5700X 8-Core Processor
processor	: 5
model name	: AMD Ryzen 7 5700X 8-Core Processor
processor	: 6
model name	: AMD Ryzen 7 5700X 8-Core Processor
processor	: 7
model name	: AMD Ryzen 7 5700X 8-Core Processor
===MEMINFO===
MemTotal:       65792000 kB
===DMI===
ASUSTeK COMPUTER INC.
---
PRIME B550-PLUS
---
System Product Name
---
American Megatrends Inc.
---
3607
---
03/22/2024
===EFI===
efi
===IOMMU===
iommu_group
`
		hardware := ParseHardwareProbeOutput(fullProbe)
		if hardware.Arch != "x86_64" {
			t.Errorf("expected arch x86_64, got %q", hardware.Arch)
		}
		if hardware.OSDescription != "AlmaLinux 10.2 (Lavender Lion)" {
			t.Errorf("expected osDescription 'AlmaLinux 10.2 (Lavender Lion)', got %q", hardware.OSDescription)
		}
		if hardware.CPU != "AMD Ryzen 7 5700X 8-Core Processor" {
			t.Errorf("expected cpu 'AMD Ryzen 7 5700X 8-Core Processor', got %q", hardware.CPU)
		}
		if hardware.VCPUs != 8 {
			t.Errorf("expected 8 vcpus, got %d", hardware.VCPUs)
		}
		if hardware.RamGiB != 63 {
			t.Errorf("expected 63 GiB, got %d", hardware.RamGiB)
		}
		if !strings.Contains(hardware.Motherboard, "ASUSTeK") && !strings.Contains(hardware.Motherboard, "PRIME B550-PLUS") {
			t.Errorf("expected mobo with ASUSTeK or PRIME B550-PLUS, got %q", hardware.Motherboard)
		}
		if !strings.Contains(hardware.BIOS, "3607") {
			t.Errorf("expected bios with 3607, got %q", hardware.BIOS)
		}
		if hardware.SecureBoot != "Enabled" {
			t.Errorf("expected secureBoot Enabled, got %q", hardware.SecureBoot)
		}
		if hardware.IOMMU != "Enabled (AMD-Vi)" {
			t.Errorf("expected iommu 'Enabled (AMD-Vi)', got %q", hardware.IOMMU)
		}
	})

	t.Run("handles partial VM probe output with SeaBIOS and QEMU hardware", func(t *testing.T) {
		vmProbe := `
===ARCH===
x86_64
===OS_RELEASE===
PRETTY_NAME="Debian GNU/Linux 13 (trixie)"
===CPUINFO===
processor	: 0
model name	: Common KVM processor
processor	: 1
model name	: Common KVM processor
===MEMINFO===
MemTotal:       4048000 kB
===DMI===
QEMU
---
Standard PC (i440FX + PIIX, 1996)
---
Standard PC (i440FX + PIIX, 1996)
---
SeaBIOS
---
rel-1.16.2-0-gea1b7a073390-prebuilt.qemu.org
---
04/01/2014
`
		hardware := ParseHardwareProbeOutput(vmProbe)
		if hardware.OSDescription != "Debian GNU/Linux 13 (trixie)" {
			t.Errorf("expected osDescription 'Debian GNU/Linux 13 (trixie)', got %q", hardware.OSDescription)
		}
		if hardware.CPU != "Common KVM processor" {
			t.Errorf("expected cpu 'Common KVM processor', got %q", hardware.CPU)
		}
		if hardware.VCPUs != 2 {
			t.Errorf("expected 2 vcpus, got %d", hardware.VCPUs)
		}
		if hardware.RamGiB != 4 {
			t.Errorf("expected 4 GiB, got %d", hardware.RamGiB)
		}
		if !strings.Contains(hardware.Motherboard, "Standard PC") {
			t.Errorf("expected mobo with Standard PC, got %q", hardware.Motherboard)
		}
		if !strings.Contains(hardware.BIOS, "SeaBIOS") {
			t.Errorf("expected bios with SeaBIOS, got %q", hardware.BIOS)
		}
	})

	t.Run("handles empty input with default specifications", func(t *testing.T) {
		hw := ParseHardwareProbeOutput("")
		if hw.CPU != "x86_64 Host Processor" || hw.VCPUs != 4 || hw.RamGiB != 8 {
			t.Errorf("unexpected empty defaults: %+v", hw)
		}
		if hw.Motherboard != "Standard Homelab Platform" || hw.BIOS != "UEFI 2.8" {
			t.Errorf("unexpected empty defaults: %+v", hw)
		}
		if hw.OSDescription != "Linux (Generic)" || hw.Arch != "x86_64" {
			t.Errorf("unexpected empty defaults: %+v", hw)
		}
		if hw.SecureBoot != "Disabled" || hw.IOMMU != "Disabled" {
			t.Errorf("unexpected empty defaults: %+v", hw)
		}
	})
}

func TestResolveConnectionRoute(t *testing.T) {
	t.Run("resolves primary route via tailscaleHost and fallback via LAN fallbackIp", func(t *testing.T) {
		node := NewObject()
		node.SetString("id", "node-alpha")
		node.SetString("name", "Alpha Host")
		node.SetString("tailscaleHost", "alpha.leo-banjo.ts.net")
		node.SetString("tailscaleIp", "100.64.0.10")
		node.SetString("ip", "192.168.1.100")
		node.SetString("fallbackIp", "192.168.1.100")

		routes := ResolveConnectionRoute(node)
		if routes.Primary != "alpha.leo-banjo.ts.net" {
			t.Errorf("expected primary alpha.leo-banjo.ts.net, got %q", routes.Primary)
		}
		if routes.Fallback == nil || *routes.Fallback != "192.168.1.100" {
			t.Errorf("expected fallback 192.168.1.100, got %v", routes.Fallback)
		}
	})

	t.Run("resolves fallback via fallbackHost if provided", func(t *testing.T) {
		node := NewObject()
		node.SetString("id", "node-beta")
		node.SetString("name", "Beta Host")
		node.SetString("tailscaleHost", "beta.leo-banjo.ts.net")
		node.SetString("tailscaleIp", "100.64.0.11")
		node.SetString("ip", "192.168.1.101")
		node.SetString("fallbackHost", "beta.local")

		routes := ResolveConnectionRoute(node)
		if routes.Primary != "beta.leo-banjo.ts.net" {
			t.Errorf("expected primary beta.leo-banjo.ts.net, got %q", routes.Primary)
		}
		if routes.Fallback == nil || *routes.Fallback != "beta.local" {
			t.Errorf("expected fallback beta.local, got %v", routes.Fallback)
		}
	})

	t.Run("resolves fallback via node.ip when distinct from primary and no fallbackHost is set", func(t *testing.T) {
		node := NewObject()
		node.SetString("id", "node-gamma")
		node.SetString("name", "Gamma Host")
		node.SetString("tailscaleHost", "gamma.leo-banjo.ts.net")
		node.SetString("ip", "10.0.0.5")

		routes := ResolveConnectionRoute(node)
		if routes.Primary != "gamma.leo-banjo.ts.net" {
			t.Errorf("expected primary gamma.leo-banjo.ts.net, got %q", routes.Primary)
		}
		if routes.Fallback == nil || *routes.Fallback != "10.0.0.5" {
			t.Errorf("expected fallback 10.0.0.5, got %v", routes.Fallback)
		}
	})

	t.Run("falls back to tailscaleIp or ip as primary when tailscaleHost is empty", func(t *testing.T) {
		node1 := NewObject()
		node1.SetString("id", "node-delta")
		node1.SetString("name", "Delta Host")
		node1.SetString("tailscaleHost", "")
		node1.SetString("tailscaleIp", "100.64.0.25")
		node1.SetString("ip", "192.168.1.200")
		if r := ResolveConnectionRoute(node1); r.Primary != "100.64.0.25" {
			t.Errorf("expected primary 100.64.0.25, got %q", r.Primary)
		}

		node2 := NewObject()
		node2.SetString("id", "node-eps")
		node2.SetString("name", "Epsilon Host")
		node2.SetString("tailscaleHost", "")
		node2.SetString("tailscaleIp", "")
		node2.SetString("ip", "192.168.1.201")
		r2 := ResolveConnectionRoute(node2)
		if r2.Primary != "192.168.1.201" {
			t.Errorf("expected primary 192.168.1.201, got %q", r2.Primary)
		}
		if r2.Fallback != nil {
			t.Errorf("expected nil fallback, got %v", *r2.Fallback)
		}
	})

	t.Run("returns undefined fallback when fallback matches primary", func(t *testing.T) {
		node := NewObject()
		node.SetString("id", "node-zeta")
		node.SetString("name", "Zeta Host")
		node.SetString("tailscaleHost", "")
		node.SetString("tailscaleIp", "")
		node.SetString("ip", "192.168.1.250")
		node.SetString("fallbackIp", "192.168.1.250")

		routes := ResolveConnectionRoute(node)
		if routes.Primary != "192.168.1.250" {
			t.Errorf("expected primary 192.168.1.250, got %q", routes.Primary)
		}
		if routes.Fallback != nil {
			t.Errorf("expected nil fallback when fallback matches primary, got %v", *routes.Fallback)
		}
	})

	t.Run("prefers fallbackHost over fallbackIp when both are present", func(t *testing.T) {
		node := NewObject()
		node.SetString("id", "node-eta")
		node.SetString("name", "Eta Host")
		node.SetString("tailscaleHost", "eta.leo-banjo.ts.net")
		node.SetString("fallbackHost", "eta-lan.local")
		node.SetString("fallbackIp", "192.168.1.155")

		routes := ResolveConnectionRoute(node)
		if routes.Primary != "eta.leo-banjo.ts.net" {
			t.Errorf("expected primary eta.leo-banjo.ts.net, got %q", routes.Primary)
		}
		if routes.Fallback == nil || *routes.Fallback != "eta-lan.local" {
			t.Errorf("expected fallback eta-lan.local, got %v", routes.Fallback)
		}
	})
}

func TestApplyHardwareProbeToNode(t *testing.T) {
	t.Run("merges probed hardware telemetry into node specs and updates osDescription", func(t *testing.T) {
		initialNodeJSON := `{
			"id": "node-pve-01",
			"name": "Proxmox Alpha",
			"osDescription": "Debian 12",
			"specs": {
				"cpu": "Generic",
				"vcpus": 1,
				"ramGiB": 2,
				"motherboard": "Unknown",
				"bios": "Old",
				"secureBoot": "Disabled",
				"iommu": "Disabled",
				"ipmi": "192.168.1.254",
				"ecc": "Active ECC"
			}
		}`
		initialNode, err := ParseObject([]byte(initialNodeJSON))
		if err != nil {
			t.Fatalf("ParseObject failed: %v", err)
		}

		probed := ProbedHardware{
			CPU:           "AMD EPYC 7002",
			VCPUs:         32,
			RamGiB:        128,
			Motherboard:   "Supermicro H11SSL-i",
			BIOS:          "2.5b (2023-11-01)",
			OSDescription: "Proxmox VE 8.2 (Debian 12)",
			SecureBoot:    "Enabled",
			IOMMU:         "Enabled (AMD-Vi)",
		}

		updated := ApplyProbe(initialNode, probed)

		osDesc, _ := updated.GetString("osDescription")
		if osDesc != "Proxmox VE 8.2 (Debian 12)" {
			t.Errorf("expected osDescription 'Proxmox VE 8.2 (Debian 12)', got %q", osDesc)
		}

		specsRaw, ok := updated.Get("specs")
		if !ok {
			t.Fatalf("missing specs")
		}
		specs, err := ParseObject(specsRaw)
		if err != nil {
			t.Fatalf("ParseObject(specs) failed: %v", err)
		}

		if s, _ := specs.GetString("cpu"); s != "AMD EPYC 7002" {
			t.Errorf("expected cpu 'AMD EPYC 7002', got %q", s)
		}
		if n := parsePositiveInt(specs.vals["vcpus"]); n != 32 {
			t.Errorf("expected vcpus 32, got %d", n)
		}
		if n := parsePositiveInt(specs.vals["ramGiB"]); n != 128 {
			t.Errorf("expected ramGiB 128, got %d", n)
		}
		if s, _ := specs.GetString("motherboard"); s != "Supermicro H11SSL-i" {
			t.Errorf("expected motherboard 'Supermicro H11SSL-i', got %q", s)
		}
		if s, _ := specs.GetString("bios"); s != "2.5b (2023-11-01)" {
			t.Errorf("expected bios '2.5b (2023-11-01)', got %q", s)
		}
		if s, _ := specs.GetString("secureBoot"); s != "Enabled" {
			t.Errorf("expected secureBoot Enabled, got %q", s)
		}
		if s, _ := specs.GetString("iommu"); s != "Enabled (AMD-Vi)" {
			t.Errorf("expected iommu 'Enabled (AMD-Vi)', got %q", s)
		}
		if s, _ := specs.GetString("ipmi"); s != "192.168.1.254" {
			t.Errorf("expected ipmi 192.168.1.254, got %q", s)
		}
		if s, _ := specs.GetString("ecc"); s != "Active ECC" {
			t.Errorf("expected ecc 'Active ECC', got %q", s)
		}
	})

	t.Run("handles empty or partial probe gracefully without crashing or wiping specs", func(t *testing.T) {
		initialNodeJSON := `{
			"id": "node-bare",
			"name": "Bare Node",
			"osDescription": "Fedora 40",
			"specs": {
				"cpu": "Intel Xeon E-2288G",
				"vcpus": 8,
				"ramGiB": 64,
				"motherboard": "ASRock Rack E3C246D4U",
				"bios": "P2.40",
				"secureBoot": "Enabled",
				"iommu": "Enabled (VT-d)",
				"ipmi": "10.0.1.5",
				"ecc": "ECC Unbuffered"
			}
		}`
		initialNode, err := ParseObject([]byte(initialNodeJSON))
		if err != nil {
			t.Fatalf("ParseObject failed: %v", err)
		}

		updated := ApplyProbe(initialNode, ProbedHardware{})

		osDesc, _ := updated.GetString("osDescription")
		if osDesc != "Fedora 40" {
			t.Errorf("expected osDescription 'Fedora 40', got %q", osDesc)
		}

		specsRaw, ok := updated.Get("specs")
		if !ok {
			t.Fatalf("missing specs")
		}
		specs, err := ParseObject(specsRaw)
		if err != nil {
			t.Fatalf("ParseObject(specs) failed: %v", err)
		}

		if s, _ := specs.GetString("cpu"); s != "Intel Xeon E-2288G" {
			t.Errorf("expected cpu 'Intel Xeon E-2288G', got %q", s)
		}
		if n := parsePositiveInt(specs.vals["vcpus"]); n != 8 {
			t.Errorf("expected vcpus 8, got %d", n)
		}
		if n := parsePositiveInt(specs.vals["ramGiB"]); n != 64 {
			t.Errorf("expected ramGiB 64, got %d", n)
		}
		if s, _ := specs.GetString("motherboard"); s != "ASRock Rack E3C246D4U" {
			t.Errorf("expected motherboard 'ASRock Rack E3C246D4U', got %q", s)
		}
		if s, _ := specs.GetString("bios"); s != "P2.40" {
			t.Errorf("expected bios 'P2.40', got %q", s)
		}
		if s, _ := specs.GetString("secureBoot"); s != "Enabled" {
			t.Errorf("expected secureBoot Enabled, got %q", s)
		}
		if s, _ := specs.GetString("iommu"); s != "Enabled (VT-d)" {
			t.Errorf("expected iommu 'Enabled (VT-d)', got %q", s)
		}
		if s, _ := specs.GetString("ipmi"); s != "10.0.1.5" {
			t.Errorf("expected ipmi 10.0.1.5, got %q", s)
		}
		if s, _ := specs.GetString("ecc"); s != "ECC Unbuffered" {
			t.Errorf("expected ecc 'ECC Unbuffered', got %q", s)
		}
	})
}
