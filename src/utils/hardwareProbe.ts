export interface ProbedHardware {
  cpu: string;
  vcpus: number;
  ramGiB: number;
  motherboard: string;
  bios: string;
  osDescription: string;
  arch: string;
  secureBoot: string;
  iommu: string;
}

/**
 * Returns a shell command string that gathers hardware discovery info
 * across Linux distributions using native kernel/system interfaces.
 */
export function getHardwareProbeCommand(): string {
  return [
    'echo "===ARCH==="',
    'uname -m 2>/dev/null || echo "x86_64"',
    'echo "===OS_RELEASE==="',
    'cat /etc/os-release 2>/dev/null || cat /usr/lib/os-release 2>/dev/null || echo "PRETTY_NAME=\\"Linux\\""',
    'echo "===CPUINFO==="',
    'cat /proc/cpuinfo 2>/dev/null || echo "model name: Generic CPU"',
    'echo "===MEMINFO==="',
    'cat /proc/meminfo 2>/dev/null || free -m 2>/dev/null || echo "MemTotal: 4194304 kB"',
    'echo "===DMI==="',
    'cat /sys/class/dmi/id/board_vendor 2>/dev/null || echo ""',
    'echo "---"',
    'cat /sys/class/dmi/id/board_name 2>/dev/null || echo ""',
    'echo "---"',
    'cat /sys/class/dmi/id/product_name 2>/dev/null || echo ""',
    'echo "---"',
    'cat /sys/class/dmi/id/bios_vendor 2>/dev/null || echo ""',
    'echo "---"',
    'cat /sys/class/dmi/id/bios_version 2>/dev/null || echo ""',
    'echo "---"',
    'cat /sys/class/dmi/id/bios_date 2>/dev/null || echo ""',
    'echo "===EFI==="',
    'test -d /sys/firmware/efi && echo "efi" || echo "legacy"',
    'echo "===IOMMU==="',
    'ls /sys/kernel/iommu_groups 2>/dev/null | head -n 1 || echo ""',
  ].join('; ');
}

/**
 * Extracts human-readable OS description from /etc/os-release contents.
 */
export function parseOsRelease(content: string): string {
  if (!content) return 'Linux (Generic)';

  // 1. Check for PRETTY_NAME
  const prettyMatch = content.match(/PRETTY_NAME="?([^"\n\r]+)"?/);
  if (prettyMatch && prettyMatch[1].trim()) {
    return prettyMatch[1].trim();
  }

  // 2. Check for NAME and VERSION
  const nameMatch = content.match(/^NAME="?([^"\n\r]+)"?/m);
  const verMatch = content.match(/^VERSION="?([^"\n\r]+)"?/m);

  if (nameMatch && nameMatch[1].trim()) {
    const name = nameMatch[1].trim();
    const ver = verMatch ? verMatch[1].trim() : '';
    return ver ? `${name} ${ver}` : name;
  }

  return 'Linux (Generic)';
}

/**
 * Extracts CPU Model and vCPU core count from /proc/cpuinfo contents.
 */
export function parseCpuInfo(content: string): { cpuModel: string; vcpus: number } {
  if (!content) {
    return { cpuModel: 'x86_64 / Generic Processor', vcpus: 1 };
  }

  // Count processors: lines starting with 'processor\t:' or 'processor :'
  const procMatches = content.match(/^processor\s*:\s*\d+/gim);
  const vcpus = procMatches && procMatches.length > 0 ? procMatches.length : 1;

  // Extract model name
  const modelMatch = content.match(/^model name\s*:\s*([^\n\r]+)/im);
  if (modelMatch && modelMatch[1].trim()) {
    return {
      cpuModel: modelMatch[1].trim().replace(/\s+/g, ' '),
      vcpus,
    };
  }

  // ARM check: Model or Hardware
  const armModelMatch = content.match(/^Model\s*:\s*([^\n\r]+)/im);
  const hwMatch = content.match(/^Hardware\s*:\s*([^\n\r]+)/im);

  if (armModelMatch && armModelMatch[1].trim()) {
    return {
      cpuModel: armModelMatch[1].trim().replace(/\s+/g, ' '),
      vcpus,
    };
  }

  if (hwMatch && hwMatch[1].trim()) {
    return {
      cpuModel: `${hwMatch[1].trim().replace(/\s+/g, ' ')} (ARM64)`,
      vcpus,
    };
  }

  return { cpuModel: 'x86_64 / Generic Processor', vcpus };
}

/**
 * Extracts total RAM in GiB from /proc/meminfo or `free -m`.
 */
export function parseMemInfo(content: string): number {
  if (!content) return 1;

  // Check /proc/meminfo: MemTotal: 32724484 kB
  const memTotalMatch = content.match(/MemTotal:\s*(\d+)\s*kB/i);
  if (memTotalMatch) {
    const kb = parseInt(memTotalMatch[1], 10);
    const gib = Math.round(kb / (1024 * 1024));
    return Math.max(1, gib);
  }

  // Check free -m: Mem: 16048 ...
  const freeMatch = content.match(/Mem:\s*(\d+)/i);
  if (freeMatch) {
    const mb = parseInt(freeMatch[1], 10);
    const gib = Math.round(mb / 1024);
    return Math.max(1, gib);
  }

  return 1;
}

/**
 * Extracts Motherboard and BIOS information from sysfs DMI files.
 */
export function parseDmiInfo(content: string): { motherboard: string; bios: string } {
  const parts = content.split('---').map(p => p.trim());
  const clean = (val?: string) => {
    if (!val) return '';
    const trimmed = val.replace(/cat:.*(Permission denied|No such file).*/gi, '').trim();
    if (!trimmed || trimmed.toLowerCase() === 'none' || trimmed.toLowerCase() === 'system product name') {
      return '';
    }
    return trimmed;
  };

  const boardVendor = clean(parts[0]);
  const boardName = clean(parts[1]);
  const productName = clean(parts[2]);
  const biosVendor = clean(parts[3]);
  const biosVersion = clean(parts[4]);
  const biosDate = clean(parts[5]);

  let motherboard = '';
  if (boardVendor || boardName || productName) {
    const brand = boardVendor || '';
    const model = boardName || productName || '';
    const extra = productName && productName !== boardName && productName !== brand ? ` (${productName})` : '';
    motherboard = `${brand} ${model}${extra}`.trim().replace(/\s+/g, ' ');
  }

  if (!motherboard || motherboard.length < 3) {
    motherboard = 'Homelab Host Platform';
  }

  let bios = '';
  if (biosVendor || biosVersion || biosDate) {
    const bVendor = biosVendor ? `${biosVendor} ` : '';
    const bVer = biosVersion || 'UEFI';
    const bDate = biosDate ? ` (${biosDate})` : '';
    bios = `${bVendor}${bVer}${bDate}`.trim().replace(/\s+/g, ' ');
  }

  if (!bios) {
    bios = 'UEFI / SeaBIOS';
  }

  return { motherboard, bios };
}

/**
 * End-to-end parser taking the complete output of getHardwareProbeCommand()
 * and transforming it into a structured ProbedHardware object.
 */
export function parseHardwareProbeOutput(raw: string): ProbedHardware {
  if (!raw) {
    return {
      cpu: 'x86_64 Host Processor',
      vcpus: 4,
      ramGiB: 8,
      motherboard: 'Standard Homelab Platform',
      bios: 'UEFI 2.8',
      osDescription: 'Linux (Generic)',
      arch: 'x86_64',
      secureBoot: 'Disabled',
      iommu: 'Disabled',
    };
  }

  const sections: Record<string, string> = {};
  const sectionKeys = ['ARCH', 'OS_RELEASE', 'CPUINFO', 'MEMINFO', 'DMI', 'EFI', 'IOMMU'];

  for (let i = 0; i < sectionKeys.length; i++) {
    const key = sectionKeys[i];
    const marker = `===${key}===`;
    const startIdx = raw.indexOf(marker);
    if (startIdx === -1) continue;

    const contentStart = startIdx + marker.length;
    let nextStart = raw.length;

    for (let j = 0; j < sectionKeys.length; j++) {
      if (i === j) continue;
      const otherMarker = `===${sectionKeys[j]}===`;
      const oIdx = raw.indexOf(otherMarker, contentStart);
      if (oIdx !== -1 && oIdx < nextStart) {
        nextStart = oIdx;
      }
    }

    sections[key] = raw.substring(contentStart, nextStart).trim();
  }

  const arch = sections.ARCH ? sections.ARCH.split(/\s+/)[0] : 'x86_64';
  const osDescription = parseOsRelease(sections.OS_RELEASE || '');
  const { cpuModel, vcpus } = parseCpuInfo(sections.CPUINFO || '');
  const ramGiB = parseMemInfo(sections.MEMINFO || '');
  const { motherboard, bios } = parseDmiInfo(sections.DMI || '');

  const hasEfi = sections.EFI && sections.EFI.includes('efi');
  const secureBoot = hasEfi ? 'Enabled' : 'Disabled';

  const hasIommu = sections.IOMMU && sections.IOMMU.trim().length > 0;
  const iommu = hasIommu
    ? cpuModel.toLowerCase().includes('amd')
      ? 'Enabled (AMD-Vi)'
      : 'Enabled (VT-d)'
    : 'Disabled';

  return {
    cpu: cpuModel,
    vcpus,
    ramGiB,
    motherboard,
    bios,
    osDescription,
    arch,
    secureBoot,
    iommu,
  };
}
