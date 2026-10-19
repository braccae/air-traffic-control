import test, { describe } from 'node:test';
import assert from 'node:assert/strict';
import {
  parseHardwareProbeOutput,
  parseOsRelease,
  parseCpuInfo,
  parseMemInfo,
  parseDmiInfo,
  getHardwareProbeCommand,
  resolveConnectionRoute,
  applyHardwareProbeToNode,
} from './hardwareProbe.ts';

describe('Hardware Probe Extraction & Parsing', () => {
  describe('getHardwareProbeCommand', () => {
    test('returns command string with probe markers', () => {
      const cmd = getHardwareProbeCommand();
      assert.ok(cmd.includes('===ARCH==='));
      assert.ok(cmd.includes('===OS_RELEASE==='));
      assert.ok(cmd.includes('===CPUINFO==='));
      assert.ok(cmd.includes('===MEMINFO==='));
      assert.ok(cmd.includes('===DMI==='));
    });
  });

  describe('parseOsRelease', () => {
    test('parses PRETTY_NAME with quotes', () => {
      const osRelease = `
NAME="AlmaLinux"
VERSION="10.2 (Lavender Lion)"
ID="almalinux"
PRETTY_NAME="AlmaLinux 10.2 (Lavender Lion)"
VERSION_ID="10.2"
      `;
      assert.equal(parseOsRelease(osRelease), 'AlmaLinux 10.2 (Lavender Lion)');
    });

    test('falls back to NAME and VERSION if PRETTY_NAME is missing', () => {
      const osRelease = `
NAME="Debian GNU/Linux"
VERSION="12 (bookworm)"
ID=debian
      `;
      assert.equal(parseOsRelease(osRelease), 'Debian GNU/Linux 12 (bookworm)');
    });

    test('falls back gracefully on empty or invalid input', () => {
      assert.equal(parseOsRelease(''), 'Linux (Generic)');
      assert.equal(parseOsRelease('foo bar baz'), 'Linux (Generic)');
    });
  });

  describe('parseCpuInfo', () => {
    test('parses AMD/Intel x86_64 cpuinfo with multiple cores', () => {
      const cpuinfo = `
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
      `;
      const result = parseCpuInfo(cpuinfo);
      assert.equal(result.cpuModel, 'AMD Ryzen 5 PRO 2400G with Radeon Vega Graphics');
      assert.equal(result.vcpus, 4);
    });

    test('parses ARM64 cpuinfo with Hardware line or Model', () => {
      const cpuinfo = `
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
      `;
      const result = parseCpuInfo(cpuinfo);
      assert.ok(result.cpuModel.includes('OrangePi') || result.cpuModel.includes('Allwinner') || result.cpuModel.includes('ARM64'));
      assert.equal(result.vcpus, 2);
    });

    test('handles empty cpuinfo safely', () => {
      const result = parseCpuInfo('');
      assert.equal(result.cpuModel, 'x86_64 / Generic Processor');
      assert.equal(result.vcpus, 1);
    });
  });

  describe('parseMemInfo', () => {
    test('parses MemTotal in kB from /proc/meminfo and converts to GiB', () => {
      const meminfo = `
MemTotal:       32724484 kB
MemFree:         4823120 kB
MemAvailable:   24123984 kB
      `;
      // 32724484 kB ~ 31.2 GB -> rounds to 31 or 32 GiB
      const gib = parseMemInfo(meminfo);
      assert.equal(gib, 31);
    });

    test('parses free -m format fallback', () => {
      const freeOut = `
               total        used        free      shared  buff/cache   available
Mem:           16048        4210        8920         120        2918       11420
Swap:           4096           0        4096
      `;
      const gib = parseMemInfo(freeOut);
      assert.equal(gib, 16);
    });

    test('returns minimum 1 GiB for zero or tiny memory', () => {
      assert.equal(parseMemInfo('MemTotal: 512000 kB'), 1);
      assert.equal(parseMemInfo(''), 1);
    });
  });

  describe('parseDmiInfo', () => {
    test('extracts motherboard and BIOS from DMI sections', () => {
      const dmiOutput = [
        'Lenovo',
        '---',
        '3141',
        '---',
        'ThinkCentre M725s',
        '---',
        'Lenovo',
        '---',
        'M25KT2FA',
        '---',
        '2019-04-23',
      ].join('\n');

      const result = parseDmiInfo(dmiOutput);
      assert.equal(result.motherboard, 'Lenovo 3141 (ThinkCentre M725s)');
      assert.equal(result.bios, 'Lenovo M25KT2FA (2019-04-23)');
    });

    test('handles missing or unreadable DMI gracefully', () => {
      const dmiOutput = 'cat: /sys/class/dmi/id/board_vendor: Permission denied\n---\n---';
      const result = parseDmiInfo(dmiOutput);
      assert.ok(result.motherboard.length > 0);
      assert.ok(result.bios.length > 0);
      assert.notEqual(result.motherboard, 'Permission denied');
    });
  });

  describe('parseHardwareProbeOutput (Full Pipeline)', () => {
    test('parses complete probe output into full ProbedHardware specification', () => {
      const fullProbe = `
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
      `;

      const hardware = parseHardwareProbeOutput(fullProbe);
      assert.equal(hardware.arch, 'x86_64');
      assert.equal(hardware.osDescription, 'AlmaLinux 10.2 (Lavender Lion)');
      assert.equal(hardware.cpu, 'AMD Ryzen 7 5700X 8-Core Processor');
      assert.equal(hardware.vcpus, 8);
      assert.equal(hardware.ramGiB, 63);
      assert.ok(hardware.motherboard.includes('ASUSTeK') || hardware.motherboard.includes('PRIME B550-PLUS'));
      assert.ok(hardware.bios.includes('3607'));
      assert.equal(hardware.secureBoot, 'Enabled');
      assert.equal(hardware.iommu, 'Enabled (AMD-Vi)');
    });

    test('handles partial VM probe output with SeaBIOS and QEMU hardware', () => {
      const vmProbe = `
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
      `;

      const hardware = parseHardwareProbeOutput(vmProbe);
      assert.equal(hardware.osDescription, 'Debian GNU/Linux 13 (trixie)');
      assert.equal(hardware.cpu, 'Common KVM processor');
      assert.equal(hardware.vcpus, 2);
      assert.equal(hardware.ramGiB, 4);
      assert.ok(hardware.motherboard.includes('QEMU Standard PC') || hardware.motherboard.includes('Standard PC'));
      assert.ok(hardware.bios.includes('SeaBIOS'));
    });
  });

  describe('resolveConnectionRoute', () => {
    test('resolves primary route via tailscaleHost and fallback via LAN fallbackIp', () => {
      const node: any = {
        id: 'node-alpha',
        name: 'Alpha Host',
        tailscaleHost: 'alpha.leo-banjo.ts.net',
        tailscaleIp: '100.64.0.10',
        ip: '192.168.1.100',
        fallbackIp: '192.168.1.100',
      };
      const routes = resolveConnectionRoute(node);
      assert.equal(routes.primary, 'alpha.leo-banjo.ts.net');
      assert.equal(routes.fallback, '192.168.1.100');
    });

    test('resolves fallback via fallbackHost if provided', () => {
      const node: any = {
        id: 'node-beta',
        name: 'Beta Host',
        tailscaleHost: 'beta.leo-banjo.ts.net',
        tailscaleIp: '100.64.0.11',
        ip: '192.168.1.101',
        fallbackHost: 'beta.local',
      };
      const routes = resolveConnectionRoute(node);
      assert.equal(routes.primary, 'beta.leo-banjo.ts.net');
      assert.equal(routes.fallback, 'beta.local');
    });

    test('resolves fallback via node.ip when distinct from primary and no fallbackHost is set', () => {
      const node: any = {
        id: 'node-gamma',
        name: 'Gamma Host',
        tailscaleHost: 'gamma.leo-banjo.ts.net',
        ip: '10.0.0.5',
      };
      const routes = resolveConnectionRoute(node);
      assert.equal(routes.primary, 'gamma.leo-banjo.ts.net');
      assert.equal(routes.fallback, '10.0.0.5');
    });

    test('falls back to tailscaleIp or ip as primary when tailscaleHost is empty', () => {
      const node1: any = {
        id: 'node-delta',
        name: 'Delta Host',
        tailscaleHost: '',
        tailscaleIp: '100.64.0.25',
        ip: '192.168.1.200',
      };
      assert.equal(resolveConnectionRoute(node1).primary, '100.64.0.25');

      const node2: any = {
        id: 'node-eps',
        name: 'Epsilon Host',
        tailscaleHost: '',
        tailscaleIp: '',
        ip: '192.168.1.201',
      };
      assert.equal(resolveConnectionRoute(node2).primary, '192.168.1.201');
      assert.equal(resolveConnectionRoute(node2).fallback, undefined);
    });

    test('returns undefined fallback when fallback matches primary', () => {
      const node: any = {
        id: 'node-zeta',
        name: 'Zeta Host',
        tailscaleHost: '',
        tailscaleIp: '',
        ip: '192.168.1.250',
        fallbackIp: '192.168.1.250',
      };
      const routes = resolveConnectionRoute(node);
      assert.equal(routes.primary, '192.168.1.250');
      assert.equal(routes.fallback, undefined);
    });

    test('prefers fallbackHost over fallbackIp when both are present', () => {
      const node: any = {
        id: 'node-eta',
        name: 'Eta Host',
        tailscaleHost: 'eta.leo-banjo.ts.net',
        fallbackHost: 'eta-lan.local',
        fallbackIp: '192.168.1.155',
      };
      const routes = resolveConnectionRoute(node);
      assert.equal(routes.primary, 'eta.leo-banjo.ts.net');
      assert.equal(routes.fallback, 'eta-lan.local');
    });
  });

  describe('applyHardwareProbeToNode', () => {
    test('merges probed hardware telemetry into node specs and updates osDescription', () => {
      const initialNode: any = {
        id: 'node-pve-01',
        name: 'Proxmox Alpha',
        osDescription: 'Debian 12',
        specs: {
          cpu: 'Generic',
          vcpus: 1,
          ramGiB: 2,
          motherboard: 'Unknown',
          bios: 'Old',
          secureBoot: 'Disabled',
          iommu: 'Disabled',
          ipmi: '192.168.1.254',
          ecc: 'Active ECC',
        },
      };

      const probed: any = {
        cpu: 'AMD EPYC 7002',
        vcpus: 32,
        ramGiB: 128,
        motherboard: 'Supermicro H11SSL-i',
        bios: '2.5b (2023-11-01)',
        osDescription: 'Proxmox VE 8.2 (Debian 12)',
        secureBoot: 'Enabled',
        iommu: 'Enabled (AMD-Vi)',
      };

      const updated = applyHardwareProbeToNode(initialNode, probed);
      assert.equal(updated.osDescription, 'Proxmox VE 8.2 (Debian 12)');
      assert.equal(updated.specs.cpu, 'AMD EPYC 7002');
      assert.equal(updated.specs.vcpus, 32);
      assert.equal(updated.specs.ramGiB, 128);
      assert.equal(updated.specs.motherboard, 'Supermicro H11SSL-i');
      assert.equal(updated.specs.bios, '2.5b (2023-11-01)');
      assert.equal(updated.specs.secureBoot, 'Enabled');
      assert.equal(updated.specs.iommu, 'Enabled (AMD-Vi)');
      assert.equal(updated.specs.ipmi, '192.168.1.254');
      assert.equal(updated.specs.ecc, 'Active ECC');
    });

    test('handles empty or partial probe gracefully without crashing or wiping specs', () => {
      const initialNode: any = {
        id: 'node-bare',
        name: 'Bare Node',
        osDescription: 'Fedora 40',
        specs: {
          cpu: 'Intel Xeon E-2288G',
          vcpus: 8,
          ramGiB: 64,
          motherboard: 'ASRock Rack E3C246D4U',
          bios: 'P2.40',
          secureBoot: 'Enabled',
          iommu: 'Enabled (VT-d)',
          ipmi: '10.0.1.5',
          ecc: 'ECC Unbuffered',
        },
      };

      const updated = applyHardwareProbeToNode(initialNode, {});
      assert.equal(updated.osDescription, 'Fedora 40');
      assert.equal(updated.specs.cpu, 'Intel Xeon E-2288G');
      assert.equal(updated.specs.vcpus, 8);
      assert.equal(updated.specs.ramGiB, 64);
      assert.equal(updated.specs.motherboard, 'ASRock Rack E3C246D4U');
      assert.equal(updated.specs.bios, 'P2.40');
      assert.equal(updated.specs.secureBoot, 'Enabled');
      assert.equal(updated.specs.iommu, 'Enabled (VT-d)');
      assert.equal(updated.specs.ipmi, '10.0.1.5');
      assert.equal(updated.specs.ecc, 'ECC Unbuffered');
    });
  });
});
