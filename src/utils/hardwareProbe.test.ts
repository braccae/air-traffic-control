import test, { describe } from 'node:test';
import assert from 'node:assert/strict';
import {
  parseHardwareProbeOutput,
  parseOsRelease,
  parseCpuInfo,
  parseMemInfo,
  parseDmiInfo,
  getHardwareProbeCommand,
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
});
