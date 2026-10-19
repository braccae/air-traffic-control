import type { NodeItem, PrivilegeMethod, PrivilegeStatus } from '../types/index.ts';

export type { PrivilegeMethod, PrivilegeStatus };

export interface ProbeEvaluationInput {
  user?: string;
  run0Success?: boolean;
  run0Available?: boolean;
  sudoSuccess?: boolean;
  sudoAvailable?: boolean;
}

/**
 * Strips existing sudo or run0 prefixes from a command string.
 */
export function stripLeadingPrivilege(command: string): string {
  return command.replace(/^(sudo(\s+-n)?|run0(\s+--background=no)?)\s+/, '').trim();
}

/**
 * Wraps a shell command with the appropriate privilege escalation tool (run0 or sudo).
 * When run0 is available and active: run0 --background=no <cmd>
 * When sudo is available and active: sudo -n <cmd>
 * When method is none or user is already root: executes <cmd> directly.
 */
export function wrapElevatedCommand(
  command: string,
  method: PrivilegeMethod = 'none',
  user = 'core'
): string {
  if (!command || !command.trim()) {
    return '';
  }

  const cleanCmd = stripLeadingPrivilege(command.trim());
  if (!cleanCmd) {
    return '';
  }

  if (user === 'root') {
    return cleanCmd;
  }

  switch (method) {
    case 'run0':
      return `run0 --background=no ${cleanCmd}`;
    case 'sudo':
      return `sudo -n ${cleanCmd}`;
    case 'none':
    default:
      return cleanCmd;
  }
}

/**
 * Resolves the privilege status given probe indicators.
 * Prefers run0 when both run0 and sudo are available.
 */
export function evaluatePrivilege(input: ProbeEvaluationInput): PrivilegeStatus {
  const user = input.user || 'core';
  if (user === 'root') {
    return {
      method: 'none',
      active: true,
      user: 'root',
      canElevate: true,
      details: {
        hasRun0: !!input.run0Available,
        run0Active: true,
        hasSudo: !!input.sudoAvailable,
        sudoActive: true,
      },
    };
  }

  // Active run0 has first priority
  if (input.run0Success) {
    return {
      method: 'run0',
      active: true,
      user,
      canElevate: true,
      details: {
        hasRun0: true,
        run0Active: true,
        hasSudo: input.sudoAvailable ?? input.sudoSuccess,
        sudoActive: !!input.sudoSuccess,
      },
    };
  }

  // Active sudo has second priority
  if (input.sudoSuccess) {
    return {
      method: 'sudo',
      active: true,
      user,
      canElevate: true,
      details: {
        hasRun0: !!input.run0Available,
        run0Active: false,
        hasSudo: true,
        sudoActive: true,
      },
    };
  }

  // If neither is passwordless-active, check availability for interactive/elevation requests
  if (input.run0Available) {
    return {
      method: 'run0',
      active: false,
      user,
      canElevate: true,
      details: {
        hasRun0: true,
        run0Active: false,
        hasSudo: !!input.sudoAvailable,
        sudoActive: false,
      },
    };
  }

  if (input.sudoAvailable) {
    return {
      method: 'sudo',
      active: false,
      user,
      canElevate: true,
      details: {
        hasRun0: false,
        run0Active: false,
        hasSudo: true,
        sudoActive: false,
      },
    };
  }

  return {
    method: 'none',
    active: false,
    user,
    canElevate: false,
    details: {
      hasRun0: false,
      run0Active: false,
      hasSudo: false,
      sudoActive: false,
    },
  };
}

/**
 * Generates a non-interactive shell command to probe run0 and sudo status on a host.
 */
export function buildPrivilegeProbeCommand(): string {
  return [
    'u=$(whoami 2>/dev/null || id -un); echo "USER:$u"',
    'if command -v run0 >/dev/null 2>&1; then if run0 --background=no id -u 2>/dev/null | grep -q "^0$"; then echo "RUN0:ACTIVE"; else echo "RUN0:AVAILABLE"; fi; else echo "RUN0:NONE"; fi',
    'if command -v sudo >/dev/null 2>&1; then if sudo -n id -u 2>/dev/null | grep -q "^0$"; then echo "SUDO:ACTIVE"; else echo "SUDO:AVAILABLE"; fi; else echo "SUDO:NONE"; fi',
  ].join(' && ');
}

/**
 * Parses the stdout from the privilege probe command into a structured PrivilegeStatus.
 */
export function parsePrivilegeProbeOutput(rawOutput: string, defaultUser = 'core'): PrivilegeStatus {
  let detectedUser = defaultUser;
  let run0Success = false;
  let run0Available = false;
  let sudoSuccess = false;
  let sudoAvailable = false;

  const lines = rawOutput.split('\n').map((l) => l.trim());
  for (const line of lines) {
    if (line.startsWith('USER:')) {
      const u = line.replace('USER:', '').trim();
      if (u) detectedUser = u;
    } else if (line === 'RUN0:ACTIVE') {
      run0Success = true;
      run0Available = true;
    } else if (line === 'RUN0:AVAILABLE') {
      run0Available = true;
      run0Success = false;
    } else if (line === 'RUN0:NONE') {
      run0Available = false;
      run0Success = false;
    } else if (line === 'SUDO:ACTIVE') {
      sudoSuccess = true;
      sudoAvailable = true;
    } else if (line === 'SUDO:AVAILABLE') {
      sudoAvailable = true;
      sudoSuccess = false;
    } else if (line === 'SUDO:NONE') {
      sudoAvailable = false;
      sudoSuccess = false;
    }
  }

  return evaluatePrivilege({
    user: detectedUser,
    run0Success,
    run0Available,
    sudoSuccess,
    sudoAvailable,
  });
}

/**
 * Formats badge styles and labels for breadcrumbs and node views.
 */
export function formatPrivilegeBadge(status: PrivilegeStatus): {
  label: string;
  badgeColor: string;
  icon: string;
  description: string;
} {
  if (status.user === 'root') {
    return {
      label: 'root',
      badgeColor: 'bg-emerald-500/20 text-emerald-300 border-emerald-500/40',
      icon: 'verified_user',
      description: 'Host session authenticated as root (no elevation wrapper required)',
    };
  }

  if (status.active) {
    if (status.method === 'run0') {
      return {
        label: 'run0 (Active)',
        badgeColor: 'bg-[#8cd6cb]/20 text-[#8cd6cb] border-[#8cd6cb]/40',
        icon: 'bolt',
        description: 'Privileged execution enabled via systemd run0 polkit session',
      };
    }
    return {
      label: 'sudo (Active)',
      badgeColor: 'bg-[#b0c9ff]/20 text-[#b0c9ff] border-[#b0c9ff]/40',
      icon: 'bolt',
      description: 'Privileged execution enabled via passwordless sudo (-n)',
    };
  }

  if (status.canElevate) {
    return {
      label: `${status.method} (Inactive)`,
      badgeColor: 'bg-amber-500/20 text-amber-300 border-amber-500/40',
      icon: 'lock_open',
      description: `${status.method} available on host but requires authorization/elevation`,
    };
  }

  return {
    label: `Unprivileged (${status.user})`,
    badgeColor: 'bg-[#313447] text-[#8d909b] border-[#434750]/50',
    icon: 'lock',
    description: `Running as unprivileged user ${status.user}; neither run0 nor sudo available`,
  };
}

export interface PrivilegeRouteOptions {
  findNode: (id?: string) => NodeItem | null | undefined;
  probeNodePrivilege: (node: NodeItem) => Promise<PrivilegeStatus>;
  testNodePrivilege: (
    node: NodeItem,
    method?: PrivilegeMethod
  ) => Promise<{ success: boolean; method: PrivilegeMethod; output: string; error?: string }>;
  hostCache?: {
    upsertHostSection: (hostId: string, section: string, data: any) => void;
  };
}

/**
 * Registers Express routes for privilege escalation probing and testing.
 */
export function registerPrivilegeRoutes(app: any, options: PrivilegeRouteOptions): void {
  app.get('/api/nodes/:id/privilege', async (req: any, res: any) => {
    const targetNode = options.findNode(req.params.id);
    if (!targetNode) {
      res.status(404).json({ error: 'Node not found' });
      return;
    }

    try {
      const status = await options.probeNodePrivilege(targetNode);
      if (options.hostCache) {
        try {
          options.hostCache.upsertHostSection(targetNode.id, 'privilege', status);
        } catch {
          // Non-fatal cache failure
        }
      }
      res.json({
        method: status.method,
        active: status.active,
        user: status.user,
        canElevate: status.canElevate,
        error: status.error,
        details: status.details,
      });
    } catch (err: any) {
      res.status(500).json({
        method: 'none',
        active: false,
        user: targetNode.sshUser || 'core',
        canElevate: false,
        error: err?.message || 'Privilege probe failed',
      });
    }
  });

  app.post('/api/nodes/:id/privilege/test', async (req: any, res: any) => {
    const targetNode = options.findNode(req.params.id);
    if (!targetNode) {
      res.status(404).json({ error: 'Node not found' });
      return;
    }

    const method = req.body?.method as PrivilegeMethod | undefined;
    try {
      const result = await options.testNodePrivilege(targetNode, method);
      if (options.hostCache) {
        try {
          options.hostCache.upsertHostSection(targetNode.id, 'privilege', {
            method: result.method,
            active: result.success,
            user: targetNode.sshUser || 'core',
            canElevate: true,
          });
        } catch {
          // Non-fatal
        }
      }
      res.json({
        success: result.success,
        method: result.method,
        active: result.success,
        output: result.output,
        error: result.error,
      });
    } catch (err: any) {
      res.status(500).json({
        success: false,
        method: method || targetNode.privilegeMethod || 'run0',
        active: false,
        error: err?.message || 'Elevation test failed',
      });
    }
  });
}
