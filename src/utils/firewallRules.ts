/**
 * Firewall & Network Interface Utility Engine
 * Provides validation, rich rule construction, parsing, and network interface provisioning commands.
 */

export interface FirewallRuleInput {
  port?: string;
  protocol?: string;
  source?: string;
  inInterface?: string;
  zone?: string;
  policy?: 'ACCEPT' | 'DROP' | 'REJECT';
  service?: string;
  comment?: string;
}

export interface FirewallDeleteInput {
  zone?: string;
  ruleId?: string;
  port?: string;
  protocol?: string;
  service?: string;
  rawRule?: string;
}

export interface ConnectNetworkInput {
  type: 'wireguard' | 'bridge' | 'vlan' | 'static' | 'dhcp';
  name: string;
  parent?: string;
  cidr?: string;
  vlanId?: number;
  gateway?: string;
  dns?: string[];
}

export interface ParsedFirewallRule {
  id: string;
  chain: string;
  policy: string;
  proto: string;
  port: string;
  source: string;
  comment: string;
  enabled: boolean;
  zone?: string;
  rawRule?: string;
}

const VALID_PROTOCOLS = new Set(['tcp', 'udp', 'sctp', 'icmp', 'all']);
const VALID_ZONES = new Set(['public', 'internal', 'trusted', 'dmz', 'drop', 'block', 'work', 'home', 'external']);
const VALID_POLICIES = new Set(['ACCEPT', 'DROP', 'REJECT']);

/**
 * Validates a single port (1-65535) or a port range (start-end).
 */
export function validatePortOrRange(input: string): { valid: boolean; error?: string; normalized?: string } {
  if (!input || typeof input !== 'string') {
    return { valid: false, error: 'Port or range is required' };
  }
  const trimmed = input.trim();
  if (!trimmed) {
    return { valid: false, error: 'Port or range is required' };
  }

  // Check for range: e.g. 8000-8080
  if (trimmed.includes('-')) {
    const parts = trimmed.split('-');
    if (parts.length !== 2) {
      return { valid: false, error: 'Port range must have format START-END (e.g. 8000-8080)' };
    }
    const [startStr, endStr] = parts;
    if (!/^\d+$/.test(startStr) || !/^\d+$/.test(endStr)) {
      return { valid: false, error: 'Port range numbers must be positive integers' };
    }
    const start = parseInt(startStr, 10);
    const end = parseInt(endStr, 10);
    if (start < 1 || start > 65535 || end < 1 || end > 65535) {
      return { valid: false, error: 'Port numbers must be between 1 and 65535' };
    }
    if (start >= end) {
      return { valid: false, error: `Invalid port range: start (${start}) must be less than end (${end})` };
    }
    return { valid: true, normalized: `${start}-${end}` };
  }

  // Single port
  if (!/^\d+$/.test(trimmed)) {
    return { valid: false, error: 'Port must be a valid number between 1 and 65535' };
  }
  const port = parseInt(trimmed, 10);
  if (port < 1 || port > 65535) {
    return { valid: false, error: 'Port number must be between 1 and 65535' };
  }
  return { valid: true, normalized: String(port) };
}

/**
 * Validates protocol (tcp, udp, sctp, icmp, all).
 */
export function validateProtocol(proto: string): { valid: boolean; error?: string; normalized?: string } {
  if (!proto || typeof proto !== 'string') {
    return { valid: false, error: 'Protocol is required' };
  }
  const lower = proto.trim().toLowerCase();
  if (!VALID_PROTOCOLS.has(lower)) {
    return { valid: false, error: `Unsupported protocol '${proto}'. Allowed: tcp, udp, sctp, icmp, all` };
  }
  return { valid: true, normalized: lower };
}

/**
 * Validates IPv4 address or CIDR notation. Also accepts 'any' or empty string.
 */
export function validateCidrOrIp(input: string): { valid: boolean; error?: string; normalized?: string } {
  if (!input || typeof input !== 'string') {
    return { valid: true, normalized: 'any' };
  }
  const trimmed = input.trim();
  if (trimmed === '' || trimmed.toLowerCase() === 'any' || trimmed === '0.0.0.0/0') {
    return { valid: true, normalized: 'any' };
  }

  // Check IPv4 with optional /mask
  const match = trimmed.match(/^(\d{1,3}\.){3}\d{1,3}(\/(\d{1,2}))?$/);
  if (!match) {
    return { valid: false, error: `Invalid IP address or CIDR format: '${trimmed}'` };
  }

  const [ipPart, maskPart] = trimmed.split('/');
  const octets = ipPart.split('.').map(o => parseInt(o, 10));
  for (const octet of octets) {
    if (octet < 0 || octet > 255) {
      return { valid: false, error: `Invalid IP octet ${octet} in '${trimmed}'` };
    }
  }

  if (maskPart !== undefined) {
    const mask = parseInt(maskPart, 10);
    if (mask < 0 || mask > 32) {
      return { valid: false, error: `Invalid CIDR prefix /${mask} in '${trimmed}'. Must be 0-32` };
    }
  }

  return { valid: true, normalized: trimmed };
}

/**
 * Validates Linux interface names (e.g. eth0, tailscale0, wg1, br0, vlan10).
 */
export function validateInterfaceName(iface: string): { valid: boolean; error?: string; normalized?: string } {
  if (!iface || typeof iface !== 'string') {
    return { valid: true, normalized: 'any' };
  }
  const trimmed = iface.trim();
  if (trimmed === '' || trimmed.toLowerCase() === 'any') {
    return { valid: true, normalized: 'any' };
  }

  // Linux network interface names must be 1-15 characters, alphanumeric with '.', '-', '_'
  if (!/^[a-zA-Z0-9][a-zA-Z0-9._-]{0,14}$/.test(trimmed)) {
    return {
      valid: false,
      error: `Invalid interface name '${trimmed}'. Must be 1-15 characters, alphanumeric with ., -, or _`,
    };
  }
  return { valid: true, normalized: trimmed };
}

/**
 * Validates firewalld zone name.
 */
export function validateZone(zone?: string): { valid: boolean; error?: string; normalized: string } {
  if (!zone || typeof zone !== 'string' || !zone.trim()) {
    return { valid: true, normalized: 'public' };
  }
  const lower = zone.trim().toLowerCase();
  if (!VALID_ZONES.has(lower)) {
    return { valid: false, error: `Unknown zone '${zone}'. Allowed: ${Array.from(VALID_ZONES).join(', ')}`, normalized: 'public' };
  }
  return { valid: true, normalized: lower };
}

/**
 * Builds firewalld command to add a rule.
 * Uses rich rules when source, interface, or non-ACCEPT policy is specified.
 */
export function buildFirewallAddCommand(rule: FirewallRuleInput): string {
  const zoneValidation = validateZone(rule.zone);
  const zone = zoneValidation.normalized;
  const policy = (rule.policy || 'ACCEPT').toUpperCase() as 'ACCEPT' | 'DROP' | 'REJECT';
  const protoValidation = rule.protocol ? validateProtocol(rule.protocol) : { valid: true, normalized: 'tcp' };
  const proto = protoValidation.normalized || 'tcp';

  // If service rule
  if (rule.service && rule.service.trim()) {
    const svc = rule.service.trim().replace(/[^a-zA-Z0-9_-]/g, '');
    return `sudo firewall-cmd --permanent --zone=${zone} --add-service=${svc} && sudo firewall-cmd --reload`;
  }

  const sourceValidation = validateCidrOrIp(rule.source || '');
  const source = sourceValidation.normalized || 'any';

  const ifaceValidation = validateInterfaceName(rule.inInterface || '');
  const inIface = ifaceValidation.normalized || 'any';

  const isRichRuleRequired =
    source !== 'any' ||
    policy !== 'ACCEPT' ||
    proto === 'icmp';

  if (isRichRuleRequired) {
    const parts: string[] = ['rule family="ipv4"'];

    if (source !== 'any') {
      parts.push(`source address="${source}"`);
    }

    if (proto === 'icmp') {
      parts.push('protocol value="icmp"');
    } else if (rule.port && rule.port.trim()) {
      const portValidation = validatePortOrRange(rule.port);
      const portVal = portValidation.normalized || rule.port.trim();
      parts.push(`port port="${portVal}" protocol="${proto}"`);
    }

    // Policy in rich rule syntax: accept, drop, reject
    parts.push(policy.toLowerCase());

    const richRule = parts.join(' ');
    let cmd = `sudo firewall-cmd --permanent --zone=${zone} --add-rich-rule='${richRule}'`;

    // If interface binding requested to this zone
    if (inIface !== 'any') {
      cmd += ` && sudo firewall-cmd --permanent --zone=${zone} --add-interface=${inIface}`;
    }

    cmd += ' && sudo firewall-cmd --reload';
    return cmd;
  }

  // Simple port rule
  const portValidation = validatePortOrRange(rule.port || '');
  const portVal = portValidation.normalized || rule.port?.trim() || '80';
  let cmd = `sudo firewall-cmd --permanent --zone=${zone} --add-port=${portVal}/${proto}`;
  if (inIface !== 'any') {
    cmd += ` && sudo firewall-cmd --permanent --zone=${zone} --add-interface=${inIface}`;
  }
  cmd += ' && sudo firewall-cmd --reload';
  return cmd;
}

/**
 * Builds firewalld command to delete a rule.
 */
export function buildFirewallDeleteCommand(rule: FirewallDeleteInput): string {
  const zoneValidation = validateZone(rule.zone);
  const zone = zoneValidation.normalized;

  if (rule.rawRule && rule.rawRule.trim()) {
    // Rich rule removal
    return `sudo firewall-cmd --permanent --zone=${zone} --remove-rich-rule='${rule.rawRule.trim()}' && sudo firewall-cmd --reload`;
  }

  if (rule.ruleId && rule.ruleId.startsWith('rich:')) {
    const raw = Buffer.from(rule.ruleId.slice(5), 'base64').toString('utf-8');
    return `sudo firewall-cmd --permanent --zone=${zone} --remove-rich-rule='${raw.trim()}' && sudo firewall-cmd --reload`;
  }

  if (rule.ruleId && rule.ruleId.startsWith('svc:')) {
    const svc = rule.ruleId.slice(4).trim();
    return `sudo firewall-cmd --permanent --zone=${zone} --remove-service=${svc} && sudo firewall-cmd --reload`;
  }

  if (rule.service && rule.service.trim()) {
    return `sudo firewall-cmd --permanent --zone=${zone} --remove-service=${rule.service.trim()} && sudo firewall-cmd --reload`;
  }

  // Port or range removal
  const portSpec = rule.port || rule.ruleId || '';
  let portFormatted = portSpec;
  if (!portFormatted.includes('/') && rule.protocol) {
    portFormatted = `${portFormatted}/${rule.protocol}`;
  }
  return `sudo firewall-cmd --permanent --zone=${zone} --remove-port=${portFormatted} && sudo firewall-cmd --reload`;
}

/**
 * Generates shell commands to connect / configure network interfaces on the host.
 */
export function buildConnectNetworkCommand(options: ConnectNetworkInput): string {
  const ifaceVal = validateInterfaceName(options.name);
  if (!ifaceVal.valid) {
    throw new Error(ifaceVal.error || 'Invalid interface name');
  }
  const name = ifaceVal.normalized!;
  const commands: string[] = [];

  switch (options.type) {
    case 'wireguard': {
      commands.push(`sudo ip link add dev ${name} type wireguard`);
      if (options.cidr && options.cidr.trim()) {
        const cidrVal = validateCidrOrIp(options.cidr);
        if (!cidrVal.valid) throw new Error(cidrVal.error);
        commands.push(`sudo ip addr add ${cidrVal.normalized} dev ${name}`);
      }
      commands.push(`sudo ip link set up dev ${name}`);
      if (options.gateway && options.gateway.trim()) {
        const gwVal = validateCidrOrIp(options.gateway);
        if (!gwVal.valid) throw new Error(gwVal.error);
        commands.push(`sudo ip route replace default via ${gwVal.normalized} dev ${name}`);
      }
      break;
    }

    case 'bridge': {
      commands.push(`sudo ip link add name ${name} type bridge`);
      if (options.parent && options.parent.trim()) {
        const parentVal = validateInterfaceName(options.parent);
        if (!parentVal.valid) throw new Error(parentVal.error);
        commands.push(`sudo ip link set ${parentVal.normalized} master ${name}`);
      }
      if (options.cidr && options.cidr.trim()) {
        const cidrVal = validateCidrOrIp(options.cidr);
        if (!cidrVal.valid) throw new Error(cidrVal.error);
        commands.push(`sudo ip addr add ${cidrVal.normalized} dev ${name}`);
      }
      commands.push(`sudo ip link set dev ${name} up`);
      break;
    }

    case 'vlan': {
      if (!options.parent || !options.parent.trim()) {
        throw new Error('Parent interface is required for 802.1Q VLAN');
      }
      const parentVal = validateInterfaceName(options.parent);
      if (!parentVal.valid) throw new Error(parentVal.error);

      const vlanId = options.vlanId !== undefined ? Number(options.vlanId) : 10;
      if (isNaN(vlanId) || vlanId < 1 || vlanId > 4094) {
        throw new Error('VLAN ID must be an integer between 1 and 4094');
      }
      commands.push(`sudo ip link add link ${parentVal.normalized} name ${name} type vlan id ${vlanId}`);
      if (options.cidr && options.cidr.trim()) {
        const cidrVal = validateCidrOrIp(options.cidr);
        if (!cidrVal.valid) throw new Error(cidrVal.error);
        commands.push(`sudo ip addr add ${cidrVal.normalized} dev ${name}`);
      }
      commands.push(`sudo ip link set dev ${name} up`);
      break;
    }

    case 'static': {
      if (!options.cidr || !options.cidr.trim()) {
        throw new Error('IP Address / CIDR is required for static configuration');
      }
      const cidrVal = validateCidrOrIp(options.cidr);
      if (!cidrVal.valid) throw new Error(cidrVal.error);

      commands.push(`sudo ip addr add ${cidrVal.normalized} dev ${name}`);
      commands.push(`sudo ip link set dev ${name} up`);
      if (options.gateway && options.gateway.trim()) {
        const gwVal = validateCidrOrIp(options.gateway);
        if (!gwVal.valid) throw new Error(gwVal.error);
        commands.push(`sudo ip route replace default via ${gwVal.normalized} dev ${name}`);
      }
      break;
    }

    case 'dhcp': {
      commands.push(`sudo ip link set dev ${name} up`);
      commands.push(`(sudo dhclient -v ${name} || sudo udhcpc -i ${name} || true)`);
      break;
    }

    default:
      throw new Error(`Unsupported network type '${options.type}'`);
  }

  // Handle DNS servers if specified
  if (Array.isArray(options.dns) && options.dns.length > 0) {
    const validDns = options.dns
      .map(d => d.trim())
      .filter(d => validateCidrOrIp(d).valid && d !== 'any');
    if (validDns.length > 0) {
      commands.push(`(sudo resolvectl dns ${name} ${validDns.join(' ')} 2>/dev/null || true)`);
    }
  }

  return commands.join(' && ');
}

/**
 * Parses firewall-cmd --list-all and zones output into structured rules.
 */
export function parseFirewallListAll(listAllRaw: string, activeZonesRaw = ''): {
  services: string[];
  ports: string[];
  richRules: string[];
  rules: ParsedFirewallRule[];
  zones: Record<string, { interfaces: string[]; sources: string[] }>;
} {
  const zones: Record<string, { interfaces: string[]; sources: string[] }> = {};
  const zoneLines = activeZonesRaw.trim().split('\n');
  let currentZone = '';
  for (const line of zoneLines) {
    const matchZone = line.match(/^([a-zA-Z0-9_\-]+)/);
    if (matchZone && !line.startsWith(' ') && !line.startsWith('\t')) {
      currentZone = matchZone[1];
      zones[currentZone] = { interfaces: [], sources: [] };
    } else if (currentZone && line.includes('interfaces:')) {
      zones[currentZone].interfaces = line.replace('interfaces:', '').trim().split(/\s+/).filter(Boolean);
    } else if (currentZone && line.includes('sources:')) {
      zones[currentZone].sources = line.replace('sources:', '').trim().split(/\s+/).filter(Boolean);
    }
  }

  let services: string[] = [];
  let ports: string[] = [];
  const richRules: string[] = [];
  let inRichRules = false;

  for (const line of listAllRaw.split('\n')) {
    const trimmed = line.trim();
    if (trimmed.startsWith('services:')) {
      services = trimmed.replace(/services:/, '').trim().split(/\s+/).filter(Boolean);
      inRichRules = false;
    } else if (trimmed.startsWith('ports:')) {
      ports = trimmed.replace(/ports:/, '').trim().split(/\s+/).filter(Boolean);
      inRichRules = false;
    } else if (trimmed.startsWith('rich rules:')) {
      inRichRules = true;
    } else if (inRichRules) {
      if (trimmed.startsWith('rule ')) {
        richRules.push(trimmed);
      } else if (trimmed && !line.startsWith(' ') && !line.startsWith('\t')) {
        inRichRules = false;
      }
    }
  }

  const rules: ParsedFirewallRule[] = [];

  // Tailscale trusted interface rule if active
  if (zones['trusted']?.interfaces?.includes('tailscale0') || zones['internal']?.interfaces?.includes('tailscale0')) {
    rules.push({
      id: 'fw-ts',
      chain: 'INPUT',
      policy: 'ACCEPT',
      proto: 'all',
      port: 'all',
      source: 'tailscale0 (100.64.0.0/10)',
      comment: 'Tailscale Encrypted Mesh Interface (Trusted/Internal Zone)',
      enabled: true,
      zone: zones['trusted']?.interfaces?.includes('tailscale0') ? 'trusted' : 'internal',
    });
  }

  // Standard ports & port ranges
  ports.forEach((p) => {
    const [portNum, proto = 'tcp'] = p.split('/');
    rules.push({
      id: p,
      chain: 'INPUT',
      policy: 'ACCEPT',
      proto,
      port: portNum,
      source: 'any',
      comment: `Open Port (${p})`,
      enabled: true,
      zone: 'public',
    });
  });

  // Services
  services.forEach((s) => {
    rules.push({
      id: `svc:${s}`,
      chain: 'INPUT',
      policy: 'ACCEPT',
      proto: 'tcp/udp',
      port: s,
      source: 'public / lan',
      comment: `firewalld service: ${s}`,
      enabled: true,
      zone: 'public',
    });
  });

  // Rich rules
  richRules.forEach((raw) => {
    const sourceMatch = raw.match(/source address="([^"]+)"/);
    const portMatch = raw.match(/port port="([^"]+)"/);
    const protoMatch = raw.match(/protocol="([^"]+)"/);
    const icmpMatch = raw.match(/protocol value="icmp"/);
    const isDrop = raw.includes('drop');
    const isReject = raw.includes('reject');
    const policy = isDrop ? 'DROP' : isReject ? 'REJECT' : 'ACCEPT';
    const proto = icmpMatch ? 'icmp' : (protoMatch ? protoMatch[1] : 'any');
    const port = portMatch ? portMatch[1] : (icmpMatch ? 'icmp' : '-');
    const source = sourceMatch ? sourceMatch[1] : 'any';

    const base64Rule = Buffer.from(raw).toString('base64');
    rules.push({
      id: `rich:${base64Rule}`,
      chain: 'INPUT',
      policy,
      proto,
      port,
      source,
      comment: `Rich Rule: ${raw}`,
      enabled: true,
      zone: 'public',
      rawRule: raw,
    });
  });

  return {
    services,
    ports,
    richRules,
    rules,
    zones,
  };
}
