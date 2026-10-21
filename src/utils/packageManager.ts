import type { NodeItem, GlobalTaskItem } from '../types/index.ts';

export type PackageManager = 'dnf' | 'apt' | 'apk' | 'rpm-ostree' | 'bootc';

export interface PackageUpdate {
  name: string;
  currentVersion?: string;
  newVersion: string;
  arch?: string;
  repository?: string;
  security?: boolean;
}

export interface PackageCheckResult {
  success: boolean;
  manager: PackageManager;
  isImmutable: boolean;
  updatesCount: number;
  updates: PackageUpdate[];
  lastChecked: string;
  rawOutput?: string;
  rebootRequired?: boolean;
}

/**
 * Probes the target host for available package management systems.
 * Prioritizes container/immutable runtimes (bootc, rpm-ostree) before standard package managers.
 */
export function buildDetectPackageManagerCommand(): string {
  return 'if command -v bootc >/dev/null 2>&1; then echo "bootc"; elif command -v rpm-ostree >/dev/null 2>&1; then echo "rpm-ostree"; elif command -v dnf >/dev/null 2>&1; then echo "dnf"; elif command -v apt-get >/dev/null 2>&1; then echo "apt"; elif command -v apk >/dev/null 2>&1; then echo "apk"; else echo "unknown"; fi';
}

/**
 * Parses the stdout from package manager probe command.
 */
export function parseDetectPackageManagerOutput(output: string): PackageManager | null {
  if (!output) return null;
  const lines = output.trim().split(/\r?\n/).map(l => l.trim()).filter(Boolean);
  if (lines.length === 0) return null;
  const lastLine = lines[lines.length - 1].toLowerCase();

  switch (lastLine) {
    case 'bootc':
      return 'bootc';
    case 'rpm-ostree':
      return 'rpm-ostree';
    case 'dnf':
      return 'dnf';
    case 'apt':
    case 'apt-get':
      return 'apt';
    case 'apk':
      return 'apk';
    default:
      return null;
  }
}

/**
 * Fallback detector based on OS description string.
 */
export function detectPackageManagerFromOs(osDescription?: string): PackageManager | null {
  if (!osDescription) return null;
  const lower = osDescription.toLowerCase();

  if (lower.includes('bootc')) {
    return 'bootc';
  }
  if (
    lower.includes('silverblue') ||
    lower.includes('kinoite') ||
    lower.includes('ostree') ||
    lower.includes('coreos')
  ) {
    return 'rpm-ostree';
  }
  if (
    lower.includes('fedora') ||
    lower.includes('almalinux') ||
    lower.includes('rocky') ||
    lower.includes('rhel') ||
    lower.includes('red hat') ||
    lower.includes('centos')
  ) {
    return 'dnf';
  }
  if (
    lower.includes('debian') ||
    lower.includes('ubuntu') ||
    lower.includes('raspbian') ||
    lower.includes('mint')
  ) {
    return 'apt';
  }
  if (lower.includes('alpine')) {
    return 'apk';
  }

  return null;
}

/**
 * Indicates whether a package manager operates on an immutable/atomic image-based OS.
 */
export function isImmutablePackageManager(manager: PackageManager): boolean {
  return manager === 'rpm-ostree' || manager === 'bootc';
}

/**
 * Generates the shell command to non-destructively check for available updates.
 */
export function buildCheckUpdatesCommand(manager: PackageManager): string {
  switch (manager) {
    case 'rpm-ostree':
      return 'rpm-ostree upgrade --check';
    case 'bootc':
      return 'bootc status';
    case 'dnf':
      // dnf check-update returns exit code 100 when updates are available
      return 'dnf check-update -q || [ $? -eq 100 ]';
    case 'apt':
      return 'apt-get -s -o Debug::NoLocking=true upgrade';
    case 'apk':
      return "apk version -v -l '<'";
    default:
      throw new Error(`Unsupported package manager: ${manager}`);
  }
}

/**
 * Generates the command to apply available updates.
 */
export function buildApplyUpdatesCommand(manager: PackageManager, packages?: string[]): string {
  const SAFE_PKG_NAME = /^[a-zA-Z0-9_\-\.\+@:~]+$/;
  const pkgs = packages?.map(p => p.trim()).filter(p => p.length > 0 && SAFE_PKG_NAME.test(p));
  const pkgList = pkgs && pkgs.length > 0 ? pkgs.join(' ') : '';

  switch (manager) {
    case 'dnf':
      return pkgList ? `dnf upgrade -y ${pkgList}` : 'dnf upgrade -y';
    case 'apt':
      return pkgList
        ? `DEBIAN_FRONTEND=noninteractive apt-get install --only-upgrade -y ${pkgList}`
        : 'DEBIAN_FRONTEND=noninteractive apt-get upgrade -y';
    case 'apk':
      return pkgList ? `apk add --upgrade ${pkgList}` : 'apk upgrade';
    case 'rpm-ostree':
      return 'rpm-ostree upgrade';
    case 'bootc':
      return 'bootc upgrade';
    default:
      throw new Error(`Unsupported package manager: ${manager}`);
  }
}

/**
 * Checks if a given package is a kernel, base C library, or core system daemon
 * whose update requires or strongly recommends a reboot.
 */
export function isKernelOrSystemUpdate(pkgName: string): boolean {
  const critical = [
    'kernel',
    'linux',
    'systemd',
    'glibc',
    'musl',
    'grub',
    'shim',
    'microcode',
    'dbus',
    'kmod',
  ];
  const lower = pkgName.toLowerCase();
  return critical.some(c => lower.includes(c));
}

/**
 * Determines whether applying these updates or the host state requires a reboot.
 */
export function isRebootRequired(
  manager: PackageManager,
  updates: PackageUpdate[],
  rawStatus?: string
): boolean {
  if (isImmutablePackageManager(manager)) {
    // rpm-ostree and bootc require reboot if an update is available or an actual non-null deployment is staged
    if (updates.length > 0) return true;
    if (!rawStatus) return false;
    const lower = rawStatus.toLowerCase();
    if (lower.includes('staged: null') || lower.includes('"staged": null')) {
      return false;
    }
    return lower.includes('staged') && !lower.includes('staged: null');
  }
  return updates.some(u => isKernelOrSystemUpdate(u.name));
}

/**
 * Parses raw check output into a normalized array of PackageUpdate items.
 */
export function parseCheckUpdatesOutput(manager: PackageManager, rawOutput: string): PackageUpdate[] {
  if (!rawOutput || !rawOutput.trim()) {
    return [];
  }

  switch (manager) {
    case 'dnf':
      return parseDnfCheckUpdates(rawOutput);
    case 'apt':
      return parseAptCheckUpdates(rawOutput);
    case 'apk':
      return parseApkCheckUpdates(rawOutput);
    case 'rpm-ostree':
      return parseRpmOstreeCheckUpdates(rawOutput);
    case 'bootc':
      return parseBootcCheckUpdates(rawOutput);
    default:
      return [];
  }
}

/**
 * Parser for `dnf check-update -q`
 * Output lines typically:
 * <package>.<arch>   <version>   <repo>
 * e.g.:
 * curl.x86_64        7.76.1-29.el9_4  updates
 * kernel.x86_64      5.14.0-427.el9   baseos
 */
function parseDnfCheckUpdates(raw: string): PackageUpdate[] {
  const updates: PackageUpdate[] = [];
  const lines = raw.split(/\r?\n/);

  // Normalize lines in case long package names wrapped
  const mergedLines: string[] = [];
  for (let i = 0; i < lines.length; i++) {
    const trimmed = lines[i].trim();
    if (!trimmed) continue;
    if (trimmed.startsWith('Obsoleting Packages')) break;
    if (trimmed.startsWith('Security:')) continue;

    // If line starts with leading whitespace and doesn't look like a package name with dot, merge with previous
    if (lines[i].startsWith(' ') || lines[i].startsWith('\t')) {
      if (mergedLines.length > 0) {
        mergedLines[mergedLines.length - 1] += ' ' + trimmed;
      }
    } else {
      mergedLines.push(trimmed);
    }
  }

  for (const line of mergedLines) {
    const parts = line.split(/\s+/);
    if (parts.length < 2) continue;

    const pkgWithArch = parts[0];
    const newVersion = parts[1];
    const repository = parts.slice(2).join(' ') || undefined;

    // Skip banner or header noise
    if (
      pkgWithArch.toLowerCase() === 'package' ||
      pkgWithArch.toLowerCase() === 'last' ||
      pkgWithArch.includes('===')
    ) {
      continue;
    }

    let name = pkgWithArch;
    let arch: string | undefined;

    const lastDot = pkgWithArch.lastIndexOf('.');
    if (lastDot > 0 && lastDot < pkgWithArch.length - 1) {
      const candidateArch = pkgWithArch.slice(lastDot + 1);
      // Valid RPM arch identifiers
      if (/^(x86_64|aarch64|noarch|i686|armv7hl|s390x|ppc64le)$/i.test(candidateArch)) {
        name = pkgWithArch.slice(0, lastDot);
        arch = candidateArch;
      }
    }

    const isSecurity =
      (repository ? repository.toLowerCase().includes('security') : false) ||
      line.toLowerCase().includes('sec');

    updates.push({
      name,
      newVersion,
      arch,
      repository,
      security: isSecurity,
    });
  }

  return updates;
}

/**
 * Parser for `apt-get -s upgrade` or `apt list --upgradable`
 *
 * apt-get -s upgrade lines:
 * Inst curl [7.81.0-1ubuntu1.15] (7.81.0-1ubuntu1.16 Ubuntu:22.04/jammy-updates, Ubuntu:22.04/jammy-security [amd64])
 * Inst linux-image-generic [5.15.0.91.88] (5.15.0.94.91 Ubuntu:22.04/jammy-updates [amd64])
 *
 * apt list --upgradable lines:
 * curl/jammy-security,jammy-updates 7.81.0-1ubuntu1.16 amd64 [upgradable from: 7.81.0-1ubuntu1.15]
 */
function parseAptCheckUpdates(raw: string): PackageUpdate[] {
  const updates: PackageUpdate[] = [];
  const lines = raw.split(/\r?\n/);

  for (const rawLine of lines) {
    const line = rawLine.trim();
    if (!line || line.startsWith('Listing...') || line.startsWith('Reading package lists...')) {
      continue;
    }

    // Check apt-get -s format: "Inst pkg [oldVersion] (newVersion repo [arch])" or "Inst pkg (newVersion repo [arch])"
    if (line.startsWith('Inst ')) {
      // Regex matches:
      // Inst <name> (?:\[<currentVersion>\])? \((<newVersion>)(?: ([^\]\)]+))?(?: \[([^\]]+)\])?\)
      const instMatch = line.match(
        /^Inst\s+([^\s]+)(?:\s+\[([^\]]+)\])?\s+\((?:([^\s,]+)(?:\s+([^\[\)]+))?(?:\s+\[([^\]]+)\])?)\)/
      );

      if (instMatch) {
        const name = instMatch[1];
        const currentVersion = instMatch[2] || undefined;
        const newVersion = instMatch[3];
        const repoStr = instMatch[4]?.trim() || undefined;
        const arch = instMatch[5]?.trim() || undefined;
        const isSecurity = repoStr ? repoStr.toLowerCase().includes('security') : false;

        updates.push({
          name,
          currentVersion,
          newVersion,
          arch,
          repository: repoStr,
          security: isSecurity,
        });
        continue;
      }
    }

    // Check apt list --upgradable format:
    // pkg/repo newVersion arch [upgradable from: currentVersion]
    const listMatch = line.match(
      /^([^\/\s]+)\/([^\s]+)\s+([^\s]+)\s+([^\s]+)(?:\s+\[upgradable from:\s*([^\]]+)\])?/
    );
    if (listMatch) {
      const name = listMatch[1];
      const repository = listMatch[2];
      const newVersion = listMatch[3];
      const arch = listMatch[4];
      const currentVersion = listMatch[5] || undefined;
      const isSecurity = repository.toLowerCase().includes('security');

      updates.push({
        name,
        currentVersion,
        newVersion,
        arch,
        repository,
        security: isSecurity,
      });
    }
  }

  return updates;
}

/**
 * Parser for `apk version -v -l '<'`
 * Output format:
 * musl-1.2.4_git20230717-r1 < 1.2.4_git20230717-r2
 * openssl-3.1.4-r1 < 3.1.4-r2
 * busybox-1.36.1-r4 < 1.36.1-r5
 */
function parseApkCheckUpdates(raw: string): PackageUpdate[] {
  const updates: PackageUpdate[] = [];
  const lines = raw.split(/\r?\n/);

  for (const rawLine of lines) {
    const line = rawLine.trim();
    if (!line || !line.includes('<')) continue;

    // Pattern: <installed-pkg-with-version> < <new-version>
    const parts = line.split('<').map(p => p.trim());
    if (parts.length < 2) continue;

    const left = parts[0];
    const newVersion = parts[1];

    // In Alpine package names, the version begins after the last dash preceding a digit:
    // e.g. "musl-1.2.4_git20230717-r1" or "linux-virt-6.1.66-r0" or "busybox-extras-1.36.1-r4"
    const dashMatch = left.match(/^(.*?)-(\d.*)$/);
    let name = left;
    let currentVersion: string | undefined;

    if (dashMatch) {
      name = dashMatch[1];
      currentVersion = dashMatch[2];
    }

    const isSecurity = isKernelOrSystemUpdate(name) || name.includes('ssl') || name.includes('crypto');

    updates.push({
      name,
      currentVersion,
      newVersion,
      security: isSecurity,
    });
  }

  return updates;
}

/**
 * Parser for `rpm-ostree upgrade --check` / `rpm-ostree status`
 * Example output:
 * AvailableUpdate:
 *   Version: 39.20231110.0 (2023-11-10T14:48:20Z)
 *   Commit: 8e50bc5e8a93a8d11c0f0d2c0b05b63cf26bfb76f183709b11029c0173e6a9f5
 *   Diff: 24 upgraded, 2 added, 1 removed
 *   Upgraded:
 *     kernel 6.5.9-300.fc39 -> 6.5.10-300.fc39
 *     systemd 254.5-1.fc39 -> 254.5-2.fc39
 */
function parseRpmOstreeCheckUpdates(raw: string): PackageUpdate[] {
  const updates: PackageUpdate[] = [];
  if (raw.toLowerCase().includes('no update available')) {
    return [];
  }

  const lines = raw.split(/\r?\n/);
  let inUpgradedSection = false;
  let treeVersion: string | undefined;

  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];
    const trimmed = line.trim();

    if (trimmed.startsWith('Version:') && !treeVersion) {
      treeVersion = trimmed.replace('Version:', '').trim().split(' ')[0];
    }

    if (trimmed.startsWith('Upgraded:') || trimmed.startsWith('Upgrades:')) {
      inUpgradedSection = true;
      continue;
    }

    if (inUpgradedSection) {
      // Check if we hit another top-level section (e.g. Added:, Removed:, etc.)
      if (/^[A-Za-z]+:/.test(trimmed) && !trimmed.includes('->')) {
        inUpgradedSection = false;
        continue;
      }

      // Format: <pkgName> <oldVer> -> <newVer>
      const match = trimmed.match(/^([^\s]+)\s+([^\s]+)\s+->\s+([^\s]+)/);
      if (match) {
        const name = match[1];
        const currentVersion = match[2];
        const newVersion = match[3];

        let arch: string | undefined;
        if (newVersion.includes('.')) {
          const segs = newVersion.split('.');
          const lastSeg = segs[segs.length - 1];
          if (/^(x86_64|aarch64|noarch)$/i.test(lastSeg)) {
            arch = lastSeg;
          }
        }

        updates.push({
          name,
          currentVersion,
          newVersion,
          arch,
          repository: 'ostree-deployment',
          security: isKernelOrSystemUpdate(name),
        });
      }
    }
  }

  // If tree update exists but no individual packages parsed, expose tree image update
  if (updates.length === 0 && treeVersion) {
    updates.push({
      name: 'ostree-system-image',
      newVersion: treeVersion,
      repository: 'ostree',
      security: false,
    });
  }

  return updates;
}

/**
 * Parser for `bootc status` or `bootc upgrade --check`
 */
function parseBootcCheckUpdates(raw: string): PackageUpdate[] {
  const updates: PackageUpdate[] = [];
  if (raw.toLowerCase().includes('no update available')) {
    return [];
  }

  // Try JSON first if stdout is json
  if (raw.trim().startsWith('{')) {
    try {
      const data = JSON.parse(raw);
      const booted = data?.status?.booted?.image;
      const staged = data?.status?.staged?.image;
      const bootedVer = booted?.version || booted?.image?.image || 'current';

      if (staged) {
        const stagedVer = staged?.version || staged?.image?.image || 'staged-update';
        const imageName = staged?.image?.image || booted?.image?.image || 'bootc-container-image';
        updates.push({
          name: imageName,
          currentVersion: bootedVer,
          newVersion: stagedVer,
          repository: 'container-registry',
          security: false,
        });
        return updates;
      }
    } catch {
      // Fallback to text parsing
    }
  }

  // Text status parsing
  // Booted: quay.io/centos-bootc/centos-bootc:stream9 (9.20240315.0)
  // AvailableUpdate: quay.io/centos-bootc/centos-bootc:stream9 (9.20240320.0)
  // Or: "Update available: <image>" "Version: <version>"
  const lines = raw.split(/\r?\n/);
  let availableImage: string | undefined;
  let availableVersion: string | undefined;
  let currentVersion: string | undefined;

  for (const line of lines) {
    const trimmed = line.trim();
    if (trimmed.startsWith('Booted:') || trimmed.startsWith('booted:')) {
      const match = trimmed.match(/\(([^)]+)\)/);
      if (match) currentVersion = match[1];
    }
    if (
      trimmed.startsWith('AvailableUpdate:') ||
      trimmed.startsWith('Update available:')
    ) {
      const match = trimmed.match(/\(([^)]+)\)/);
      if (match) {
        availableVersion = match[1];
      }
      const parts = trimmed.split(/:\s+/);
      if (parts.length > 1) {
        availableImage = parts[1].split(' ')[0];
      }
    } else if (trimmed.startsWith('staged:') && !trimmed.includes('null')) {
      const match = trimmed.match(/\(([^)]+)\)/);
      if (match) {
        availableVersion = match[1];
      }
      const parts = trimmed.split(/:\s+/);
      if (parts.length > 1 && parts[1].trim() !== 'null') {
        availableImage = parts[1].split(' ')[0];
      }
    }
    if (trimmed.startsWith('Version:') && !availableVersion) {
      availableVersion = trimmed.replace('Version:', '').trim();
    }
    if (trimmed.startsWith('Image:') && !availableImage) {
      availableImage = trimmed.replace('Image:', '').trim();
    }
  }

  if (availableImage || availableVersion) {
    updates.push({
      name: availableImage || 'bootc-container-image',
      currentVersion,
      newVersion: availableVersion || 'latest',
      repository: 'bootc-registry',
      security: false,
    });
  }

  return updates;
}

export interface PackageRouteOptions {
  findNode: (id?: string) => NodeItem | null | undefined;
  hostCache?: {
    getHostSection<T>(id: string, section: string): { data: T; updatedAt: number } | null;
    upsertHostSection<T>(id: string, section: string, data: T): void;
    invalidateHostSections(id?: string): void;
    saveTask?: (task: GlobalTaskItem) => void;
  };
  runNodeCommand?: (node: NodeItem, cmd: string, timeoutMs?: number) => Promise<string>;
  runNodeElevatedCommand?: (node: NodeItem, cmd: string, timeoutMs?: number) => Promise<string>;
  broadcast?: (msg: any) => void;
  tasks?: GlobalTaskItem[];
}

/**
 * Registers REST API endpoints for package checking and updates on target nodes.
 */
export function registerPackageRoutes(app: any, options: PackageRouteOptions): void {
  app.get('/api/nodes/:id/packages/check', async (req: any, res: any) => {
    const targetNode = options.findNode(req.params.id);
    if (!targetNode) {
      res.status(404).json({ success: false, error: 'Host not found' });
      return;
    }

    const isRefresh = req.query.refresh === 'true';

    // Check SQLite cache if not explicitly refreshing
    if (!isRefresh && options.hostCache) {
      const cached = options.hostCache.getHostSection<PackageCheckResult>(targetNode.id, 'packages');
      if (cached && Date.now() - cached.updatedAt < 60000) {
        res.json(cached.data);
        return;
      }
    }

    try {
      // 1. Detect package manager on host
      let detectedManager: PackageManager | null = null;
      if (options.runNodeCommand) {
        try {
          const probeCmd = buildDetectPackageManagerCommand();
          const probeOut = await options.runNodeCommand(targetNode, probeCmd, 8000);
          detectedManager = parseDetectPackageManagerOutput(probeOut);
        } catch {
          // Fall through to OS fallback
        }
      }

      if (!detectedManager) {
        detectedManager = detectPackageManagerFromOs(targetNode.osDescription);
      }

      if (!detectedManager) {
        res.status(400).json({
          success: false,
          error: `No supported package manager detected on ${targetNode.name} (${targetNode.osDescription || 'unknown OS'})`,
        });
        return;
      }

      // 2. Build check updates command
      const checkCmd = buildCheckUpdatesCommand(detectedManager);
      let rawOutput = '';
      const execFn = options.runNodeElevatedCommand || options.runNodeCommand;
      if (execFn) {
        try {
          rawOutput = await execFn(targetNode, checkCmd, 45000);
        } catch (cmdErr: any) {
          if (cmdErr?.stdout && (cmdErr?.code === 100 || detectedManager === 'dnf')) {
            rawOutput = cmdErr.stdout;
          } else if (cmdErr?.stdout && cmdErr.stdout.length > 0) {
            rawOutput = cmdErr.stdout;
          } else {
            throw cmdErr;
          }
        }
      }

      // 3. Parse output
      const updates = parseCheckUpdatesOutput(detectedManager, rawOutput);
      const isImmutable = isImmutablePackageManager(detectedManager);
      const lastChecked = new Date().toISOString();
      const rebootRequired = isRebootRequired(detectedManager, updates, rawOutput);

      const resultData: PackageCheckResult = {
        success: true,
        manager: detectedManager,
        isImmutable,
        updatesCount: updates.length,
        updates,
        lastChecked,
        rebootRequired,
      };

      if (options.hostCache) {
        options.hostCache.upsertHostSection(targetNode.id, 'packages', resultData);
      }

      res.json(resultData);
    } catch (err: any) {
      res.status(500).json({
        success: false,
        error: err?.message || `Failed to check updates for ${targetNode.name}`,
      });
    }
  });

  app.post('/api/nodes/:id/packages/apply', async (req: any, res: any) => {
    const targetNode = options.findNode(req.params.id);
    if (!targetNode) {
      res.status(404).json({ success: false, error: 'Host not found' });
      return;
    }

    let manager: PackageManager | null = req.body?.manager || null;
    const packages: string[] | undefined = Array.isArray(req.body?.packages) ? req.body.packages : undefined;

    if (!manager && options.runNodeCommand) {
      try {
        const probeCmd = buildDetectPackageManagerCommand();
        const probeOut = await options.runNodeCommand(targetNode, probeCmd, 8000);
        manager = parseDetectPackageManagerOutput(probeOut);
      } catch {
        // Fallback
      }
    }

    if (!manager) {
      manager = detectPackageManagerFromOs(targetNode.osDescription);
    }

    if (!manager) {
      res.status(400).json({
        success: false,
        error: `Could not determine package manager for ${targetNode.name}`,
      });
      return;
    }

    const applyCmd = buildApplyUpdatesCommand(manager, packages);
    const taskId = `task-update-${Date.now()}`;
    const updateTask: GlobalTaskItem = {
      id: taskId,
      startTime: new Date().toISOString().replace('T', ' ').substring(0, 19),
      nodeTarget: targetNode.tailscaleHost || targetNode.name,
      user: `${targetNode.sshUser || 'core'}@ssh-agent`,
      description: `Package upgrade (${manager}): ${applyCmd}`,
      progress: 10,
      progressEta: `Executing ${applyCmd}...`,
      status: 'RUNNING',
      category: 'active',
      logs: [`[packageManager] Starting updates via ${manager} on ${targetNode.name}: ${applyCmd}`],
    };

    if (options.tasks) {
      options.tasks.unshift(updateTask);
    }
    if (options.hostCache?.saveTask) {
      options.hostCache.saveTask(updateTask);
    }
    if (options.broadcast) {
      options.broadcast({ type: 'task_started', task: updateTask });
    }

    // Invalidate packages section in host cache
    if (options.hostCache) {
      options.hostCache.invalidateHostSections(targetNode.id);
    }

    const execFn = options.runNodeElevatedCommand || options.runNodeCommand;
    if (execFn) {
      execFn(targetNode, applyCmd, 300000)
        .then((out) => {
          updateTask.progress = 100;
          updateTask.progressEta = 'Updates applied successfully';
          updateTask.status = 'OK';
          if (out && out.trim()) {
            updateTask.logs?.push(out.trim());
          }
          updateTask.logs?.push('[packageManager] Package upgrade finished successfully.');
          if (options.hostCache?.saveTask) {
            options.hostCache.saveTask(updateTask);
          }
          if (options.broadcast) {
            options.broadcast({ type: 'task_updated', task: updateTask });
          }
        })
        .catch((err: any) => {
          updateTask.progress = 100;
          updateTask.progressEta = `Failed: ${err?.message || 'Execution error'}`;
          updateTask.status = 'FAILED';
          updateTask.logs?.push(`[error] Package upgrade failed: ${err?.message || String(err)}`);
          if (options.hostCache?.saveTask) {
            options.hostCache.saveTask(updateTask);
          }
          if (options.broadcast) {
            options.broadcast({ type: 'task_updated', task: updateTask });
          }
        });
    }

    res.json({
      success: true,
      taskId,
      message: `Applying updates on ${targetNode.name} via ${manager}`,
    });
  });
}
