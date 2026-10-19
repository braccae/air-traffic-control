import { describe, it } from 'node:test';
import assert from 'node:assert';
import {
  validatePortOrRange,
  validateProtocol,
  validateCidrOrIp,
  validateInterfaceName,
  validateZone,
  buildFirewallAddCommand,
  buildFirewallDeleteCommand,
  buildConnectNetworkCommand,
  parseFirewallListAll,
} from './firewallRules.ts';

describe('Firewall Rules & Network Interface Configuration Utilities', () => {
  describe('Port and Port Range Validation', () => {
    it('accepts valid single ports', () => {
      assert.deepStrictEqual(validatePortOrRange('80'), { valid: true, normalized: '80' });
      assert.deepStrictEqual(validatePortOrRange('443'), { valid: true, normalized: '443' });
      assert.deepStrictEqual(validatePortOrRange('1'), { valid: true, normalized: '1' });
      assert.deepStrictEqual(validatePortOrRange('65535'), { valid: true, normalized: '65535' });
      assert.deepStrictEqual(validatePortOrRange('  8080  '), { valid: true, normalized: '8080' });
    });

    it('accepts valid port ranges', () => {
      assert.deepStrictEqual(validatePortOrRange('8000-8080'), { valid: true, normalized: '8000-8080' });
      assert.deepStrictEqual(validatePortOrRange('30000-32767'), { valid: true, normalized: '30000-32767' });
      assert.deepStrictEqual(validatePortOrRange('1-1024'), { valid: true, normalized: '1-1024' });
    });

    it('rejects invalid single ports', () => {
      assert.strictEqual(validatePortOrRange('0').valid, false);
      assert.strictEqual(validatePortOrRange('65536').valid, false);
      assert.strictEqual(validatePortOrRange('-1').valid, false);
      assert.strictEqual(validatePortOrRange('http').valid, false);
      assert.strictEqual(validatePortOrRange('').valid, false);
    });

    it('rejects invalid port ranges', () => {
      // start >= end
      assert.strictEqual(validatePortOrRange('8080-8000').valid, false);
      assert.strictEqual(validatePortOrRange('80-80').valid, false);
      // out of bounds
      assert.strictEqual(validatePortOrRange('0-100').valid, false);
      assert.strictEqual(validatePortOrRange('100-70000').valid, false);
      // malformed
      assert.strictEqual(validatePortOrRange('8000-').valid, false);
      assert.strictEqual(validatePortOrRange('-8080').valid, false);
      assert.strictEqual(validatePortOrRange('8000-8080-9000').valid, false);
    });
  });

  describe('Protocol Validation', () => {
    it('accepts standard and extended protocols', () => {
      assert.deepStrictEqual(validateProtocol('tcp'), { valid: true, normalized: 'tcp' });
      assert.deepStrictEqual(validateProtocol('UDP'), { valid: true, normalized: 'udp' });
      assert.deepStrictEqual(validateProtocol('sctp'), { valid: true, normalized: 'sctp' });
      assert.deepStrictEqual(validateProtocol('icmp'), { valid: true, normalized: 'icmp' });
      assert.deepStrictEqual(validateProtocol('all'), { valid: true, normalized: 'all' });
    });

    it('rejects unsupported protocols', () => {
      assert.strictEqual(validateProtocol('http').valid, false);
      assert.strictEqual(validateProtocol('ssh').valid, false);
      assert.strictEqual(validateProtocol('; rm -rf').valid, false);
    });
  });

  describe('Source CIDR / IP Validation', () => {
    it('accepts any, empty, and 0.0.0.0/0', () => {
      assert.deepStrictEqual(validateCidrOrIp(''), { valid: true, normalized: 'any' });
      assert.deepStrictEqual(validateCidrOrIp('any'), { valid: true, normalized: 'any' });
      assert.deepStrictEqual(validateCidrOrIp('0.0.0.0/0'), { valid: true, normalized: 'any' });
    });

    it('accepts valid IPv4 and CIDR blocks', () => {
      assert.deepStrictEqual(validateCidrOrIp('192.168.1.50'), { valid: true, normalized: '192.168.1.50' });
      assert.deepStrictEqual(validateCidrOrIp('100.64.0.0/10'), { valid: true, normalized: '100.64.0.0/10' });
      assert.deepStrictEqual(validateCidrOrIp('10.0.0.0/8'), { valid: true, normalized: '10.0.0.0/8' });
      assert.deepStrictEqual(validateCidrOrIp('172.16.0.1/32'), { valid: true, normalized: '172.16.0.1/32' });
    });

    it('rejects invalid IPs or CIDRs and prevents shell injection', () => {
      assert.strictEqual(validateCidrOrIp('256.1.1.1').valid, false);
      assert.strictEqual(validateCidrOrIp('10.0.0.1/33').valid, false);
      assert.strictEqual(validateCidrOrIp('10.0.0.1; rm -rf').valid, false);
      assert.strictEqual(validateCidrOrIp('invalid-ip').valid, false);
    });
  });

  describe('Interface Name Validation', () => {
    it('accepts valid Linux interface names', () => {
      assert.deepStrictEqual(validateInterfaceName('eth0'), { valid: true, normalized: 'eth0' });
      assert.deepStrictEqual(validateInterfaceName('tailscale0'), { valid: true, normalized: 'tailscale0' });
      assert.deepStrictEqual(validateInterfaceName('br0'), { valid: true, normalized: 'br0' });
      assert.deepStrictEqual(validateInterfaceName('vlan10'), { valid: true, normalized: 'vlan10' });
      assert.deepStrictEqual(validateInterfaceName('wg-mesh'), { valid: true, normalized: 'wg-mesh' });
      assert.deepStrictEqual(validateInterfaceName('eth0.100'), { valid: true, normalized: 'eth0.100' });
      assert.deepStrictEqual(validateInterfaceName('any'), { valid: true, normalized: 'any' });
      assert.deepStrictEqual(validateInterfaceName(''), { valid: true, normalized: 'any' });
    });

    it('rejects illegal interface characters and injection strings', () => {
      assert.strictEqual(validateInterfaceName('eth 0').valid, false);
      assert.strictEqual(validateInterfaceName('eth0; reboot').valid, false);
      assert.strictEqual(validateInterfaceName('eth$foo').valid, false);
      assert.strictEqual(validateInterfaceName('a'.repeat(16)).valid, false); // >15 chars
    });
  });

  describe('Zone Validation', () => {
    it('defaults empty to public and accepts valid firewalld zones', () => {
      assert.strictEqual(validateZone('').normalized, 'public');
      assert.strictEqual(validateZone('public').normalized, 'public');
      assert.strictEqual(validateZone('internal').normalized, 'internal');
      assert.strictEqual(validateZone('trusted').normalized, 'trusted');
      assert.strictEqual(validateZone('dmz').normalized, 'dmz');
      assert.strictEqual(validateZone('drop').normalized, 'drop');
    });

    it('rejects invalid zones', () => {
      assert.strictEqual(validateZone('invalid_zone').valid, false);
    });
  });

  describe('buildFirewallAddCommand', () => {
    it('builds standard single-port command', () => {
      const cmd = buildFirewallAddCommand({
        port: '443',
        protocol: 'tcp',
        zone: 'public',
        policy: 'ACCEPT',
      });
      assert.strictEqual(
        cmd,
        'sudo firewall-cmd --permanent --zone=public --add-port=443/tcp && sudo firewall-cmd --reload'
      );
    });

    it('builds standard port-range command', () => {
      const cmd = buildFirewallAddCommand({
        port: '8000-8080',
        protocol: 'udp',
        zone: 'internal',
        policy: 'ACCEPT',
      });
      assert.strictEqual(
        cmd,
        'sudo firewall-cmd --permanent --zone=internal --add-port=8000-8080/udp && sudo firewall-cmd --reload'
      );
    });

    it('builds rich rule when source CIDR is restricted', () => {
      const cmd = buildFirewallAddCommand({
        port: '30000-32767',
        protocol: 'tcp',
        source: '100.64.0.0/10',
        zone: 'trusted',
        policy: 'ACCEPT',
      });
      assert.strictEqual(
        cmd,
        'sudo firewall-cmd --permanent --zone=trusted --add-rich-rule=\'rule family="ipv4" source address="100.64.0.0/10" port port="30000-32767" protocol="tcp" accept\' && sudo firewall-cmd --reload'
      );
    });

    it('builds rich rule for DROP/REJECT policy', () => {
      const cmd = buildFirewallAddCommand({
        port: '23',
        protocol: 'tcp',
        zone: 'public',
        policy: 'DROP',
      });
      assert.strictEqual(
        cmd,
        'sudo firewall-cmd --permanent --zone=public --add-rich-rule=\'rule family="ipv4" port port="23" protocol="tcp" drop\' && sudo firewall-cmd --reload'
      );
    });

    it('builds rich rule with inbound interface binding', () => {
      const cmd = buildFirewallAddCommand({
        port: '8443',
        protocol: 'tcp',
        inInterface: 'tailscale0',
        zone: 'trusted',
        policy: 'ACCEPT',
      });
      assert.strictEqual(
        cmd,
        'sudo firewall-cmd --permanent --zone=trusted --add-port=8443/tcp && sudo firewall-cmd --permanent --zone=trusted --add-interface=tailscale0 && sudo firewall-cmd --reload'
      );
    });

    it('builds rich rule for ICMP protocol', () => {
      const cmd = buildFirewallAddCommand({
        protocol: 'icmp',
        source: '192.168.1.0/24',
        zone: 'dmz',
        policy: 'ACCEPT',
      });
      assert.strictEqual(
        cmd,
        'sudo firewall-cmd --permanent --zone=dmz --add-rich-rule=\'rule family="ipv4" source address="192.168.1.0/24" protocol value="icmp" accept\' && sudo firewall-cmd --reload'
      );
    });

    it('builds service rule', () => {
      const cmd = buildFirewallAddCommand({
        service: 'cockpit',
        zone: 'public',
      });
      assert.strictEqual(
        cmd,
        'sudo firewall-cmd --permanent --zone=public --add-service=cockpit && sudo firewall-cmd --reload'
      );
    });
  });

  describe('buildFirewallDeleteCommand', () => {
    it('deletes port rule', () => {
      const cmd = buildFirewallDeleteCommand({
        ruleId: '8080/tcp',
        zone: 'public',
      });
      assert.strictEqual(
        cmd,
        'sudo firewall-cmd --permanent --zone=public --remove-port=8080/tcp && sudo firewall-cmd --reload'
      );
    });

    it('deletes port range rule', () => {
      const cmd = buildFirewallDeleteCommand({
        port: '8000-8080',
        protocol: 'udp',
        zone: 'internal',
      });
      assert.strictEqual(
        cmd,
        'sudo firewall-cmd --permanent --zone=internal --remove-port=8000-8080/udp && sudo firewall-cmd --reload'
      );
    });

    it('deletes rich rule using rawRule string', () => {
      const raw = 'rule family="ipv4" source address="100.64.0.0/10" port port="8000-8080" protocol="tcp" accept';
      const cmd = buildFirewallDeleteCommand({
        zone: 'trusted',
        rawRule: raw,
      });
      assert.strictEqual(
        cmd,
        `sudo firewall-cmd --permanent --zone=trusted --remove-rich-rule='${raw}' && sudo firewall-cmd --reload`
      );
    });

    it('deletes rich rule using base64-encoded ruleId', () => {
      const raw = 'rule family="ipv4" port port="23" protocol="tcp" drop';
      const base64 = Buffer.from(raw).toString('base64');
      const cmd = buildFirewallDeleteCommand({
        ruleId: `rich:${base64}`,
        zone: 'public',
      });
      assert.strictEqual(
        cmd,
        `sudo firewall-cmd --permanent --zone=public --remove-rich-rule='${raw}' && sudo firewall-cmd --reload`
      );
    });

    it('deletes service rule', () => {
      const cmd = buildFirewallDeleteCommand({
        ruleId: 'svc:cockpit',
        zone: 'public',
      });
      assert.strictEqual(
        cmd,
        'sudo firewall-cmd --permanent --zone=public --remove-service=cockpit && sudo firewall-cmd --reload'
      );
    });
  });

  describe('buildConnectNetworkCommand', () => {
    it('builds WireGuard interface connection command', () => {
      const cmd = buildConnectNetworkCommand({
        type: 'wireguard',
        name: 'wg1',
        cidr: '10.0.0.1/24',
        gateway: '10.0.0.254',
      });
      assert.strictEqual(
        cmd,
        'sudo ip link add dev wg1 type wireguard && sudo ip addr add 10.0.0.1/24 dev wg1 && sudo ip link set up dev wg1 && sudo ip route replace default via 10.0.0.254 dev wg1'
      );
    });

    it('builds Linux Bridge interface command with parent interface', () => {
      const cmd = buildConnectNetworkCommand({
        type: 'bridge',
        name: 'br0',
        parent: 'eth0',
        cidr: '192.168.1.10/24',
      });
      assert.strictEqual(
        cmd,
        'sudo ip link add name br0 type bridge && sudo ip link set eth0 master br0 && sudo ip addr add 192.168.1.10/24 dev br0 && sudo ip link set dev br0 up'
      );
    });

    it('builds 802.1Q VLAN interface command', () => {
      const cmd = buildConnectNetworkCommand({
        type: 'vlan',
        name: 'vlan10',
        parent: 'eth0',
        vlanId: 10,
        cidr: '10.10.10.5/24',
      });
      assert.strictEqual(
        cmd,
        'sudo ip link add link eth0 name vlan10 type vlan id 10 && sudo ip addr add 10.10.10.5/24 dev vlan10 && sudo ip link set dev vlan10 up'
      );
    });

    it('builds Static IP configuration command with gateway and DNS', () => {
      const cmd = buildConnectNetworkCommand({
        type: 'static',
        name: 'eth1',
        cidr: '192.168.10.50/24',
        gateway: '192.168.10.1',
        dns: ['1.1.1.1', '8.8.8.8'],
      });
      assert.strictEqual(
        cmd,
        'sudo ip addr add 192.168.10.50/24 dev eth1 && sudo ip link set dev eth1 up && sudo ip route replace default via 192.168.10.1 dev eth1 && (sudo resolvectl dns eth1 1.1.1.1 8.8.8.8 2>/dev/null || true)'
      );
    });

    it('builds DHCP activation command', () => {
      const cmd = buildConnectNetworkCommand({
        type: 'dhcp',
        name: 'eth2',
      });
      assert.strictEqual(
        cmd,
        'sudo ip link set dev eth2 up && (sudo dhclient -v eth2 || sudo udhcpc -i eth2 || true)'
      );
    });

    it('throws on missing required parameters', () => {
      assert.throws(() => {
        buildConnectNetworkCommand({
          type: 'vlan',
          name: 'vlan20',
          // missing parent
        });
      }, /Parent interface is required/);

      assert.throws(() => {
        buildConnectNetworkCommand({
          type: 'static',
          name: 'eth3',
          // missing cidr
        });
      }, /IP Address \/ CIDR is required/);
    });
  });

  describe('parseFirewallListAll', () => {
    it('parses firewalld list-all output with services, ports, ranges, and rich rules', () => {
      const listAllMock = `
public (active)
  target: default
  icmp-block-inversion: no
  interfaces: eth0
  sources: 
  services: cockpit dhcpv6-client ssh
  ports: 22/tcp 8000-8080/tcp 51820/udp
  protocols: 
  forward: yes
  masquerade: no
  forward-ports: 
  source-ports: 
  icmp-blocks: 
  rich rules: 
	rule family="ipv4" source address="100.64.0.0/10" port port="9090" protocol="tcp" accept
	rule family="ipv4" port port="23" protocol="tcp" drop
      `;
      const zonesMock = `
public
  interfaces: eth0
trusted
  interfaces: tailscale0
      `;

      const result = parseFirewallListAll(listAllMock, zonesMock);

      assert.deepStrictEqual(result.services, ['cockpit', 'dhcpv6-client', 'ssh']);
      assert.deepStrictEqual(result.ports, ['22/tcp', '8000-8080/tcp', '51820/udp']);
      assert.strictEqual(result.richRules.length, 2);

      // Tailscale mesh rule
      const tsRule = result.rules.find(r => r.id === 'fw-ts');
      assert.ok(tsRule);
      assert.strictEqual(tsRule?.source, 'tailscale0 (100.64.0.0/10)');

      // Port range rule
      const rangeRule = result.rules.find(r => r.port === '8000-8080');
      assert.ok(rangeRule);
      assert.strictEqual(rangeRule?.proto, 'tcp');
      assert.strictEqual(rangeRule?.policy, 'ACCEPT');

      // Rich rule accept
      const richAccept = result.rules.find(r => r.port === '9090');
      assert.ok(richAccept);
      assert.strictEqual(richAccept?.source, '100.64.0.0/10');
      assert.strictEqual(richAccept?.policy, 'ACCEPT');

      // Rich rule drop
      const richDrop = result.rules.find(r => r.port === '23');
      assert.ok(richDrop);
      assert.strictEqual(richDrop?.policy, 'DROP');
    });
  });
});
