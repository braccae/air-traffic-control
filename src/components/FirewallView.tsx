import React, { useState, useEffect } from 'react';
import type { NodeItem } from '../types/index.ts';
import { validatePortOrRange, validateCidrOrIp, validateInterfaceName } from '../utils/firewallRules.ts';
import { compileDaemonPluginMutation, stageTaskInQueue } from '../utils/drivers/index.ts';

interface FirewallViewProps {
  node: NodeItem | null;
  pluginId?: string;
}

export interface FirewallPortForward {
  port: string;
  proto: string;
  toport: string;
  toaddr?: string;
}

export interface FirewallPortForwardInput {
  port: string;
  proto: string;
  toport: string;
  toaddr?: string;
  zone?: string;
  plugin?: string;
}

/**
 * Returns the firewall flavour that can express NAT port forwarding for a
 * plugin id (e.g. "firewall/nftables" -> "nftables").
 */
export function firewallNatFlavour(pluginId?: string): string | null {
  const name = (pluginId || '').startsWith('firewall/') ? (pluginId as string).split('/')[1] : pluginId || 'firewalld';
  const normalized = (name || 'firewalld').toLowerCase();
  if (normalized === 'firewalld' || normalized === 'nftables' || normalized === 'iptables') {
    return normalized;
  }
  return null;
}

/**
 * Compiles a firewall port-forward (DNAT) mutation through the Go daemon and
 * stages it into the Ansible mutation queue. Fails loudly and honestly when the
 * daemon rejects the action or the queue is unreachable (Zero-Mock Policy).
 */
export async function stageFirewallPortForward(
  node: NodeItem,
  input: FirewallPortForwardInput
): Promise<{ success: boolean; title?: string; error?: string }> {
  const port = (input.port || '').trim();
  const toport = (input.toport || '').trim();
  const toaddr = (input.toaddr || '').trim();
  const proto = ((input.proto || 'tcp').trim() || 'tcp').toLowerCase();
  const zone = ((input.zone || 'public').trim() || 'public');
  const plugin = firewallNatFlavour(input.plugin);
  if (!plugin) {
    return { success: false, error: `Port forwarding is not supported for firewall plugin '${input.plugin}'` };
  }

  const portVal = validatePortOrRange(port);
  if (!portVal.valid) {
    return { success: false, error: portVal.error || 'Invalid external port or range' };
  }

  const toPortVal = validatePortOrRange(toport);
  if (!toPortVal.valid) {
    return { success: false, error: toPortVal.error || 'Invalid internal destination port' };
  }

  if (plugin !== 'firewalld' && (portVal.normalized?.includes('-') || toPortVal.normalized?.includes('-'))) {
    return { success: false, error: `Port ranges are not supported for ${plugin} DNAT; provide single ports` };
  }

  if (proto !== 'tcp' && proto !== 'udp') {
    return { success: false, error: `Unsupported protocol '${proto}'. Allowed: tcp, udp` };
  }

  if (toaddr) {
    const addrVal = validateCidrOrIp(toaddr);
    if (!addrVal.valid) {
      return { success: false, error: addrVal.error || 'Invalid destination address' };
    }
  }

  try {
    const options: Record<string, unknown> = {
      port: portVal.normalized,
      proto,
      toport: toPortVal.normalized,
    };
    if (toaddr) options.toaddr = toaddr;
    if (plugin === 'firewalld') options.zone = zone;

    const task = await compileDaemonPluginMutation(node, 'firewall', plugin, 'forward_port', options);
    const target = toaddr || 'same host';
    const scope = plugin === 'firewalld' ? `${zone} ` : '';
    task.title = `Forward ${scope}${portVal.normalized}/${proto} to ${target}:${toPortVal.normalized} on ${
      node.shortName || node.name
    }`;
    const res = await stageTaskInQueue(task);
    if (res.success) {
      return { success: true, title: task.title };
    }
    return { success: false, error: res.error || 'Failed to stage port forward' };
  } catch (err: any) {
    return { success: false, error: err?.message || 'Error staging port forward' };
  }
}

/**
 * Stages the removal of an existing port forward (undoes the DNAT rule and its
 * matching forward accept). Errors are surfaced verbatim.
 */
export async function removeFirewallPortForward(
  node: NodeItem,
  pluginId: string,
  forward: FirewallPortForward
): Promise<{ success: boolean; title?: string; error?: string }> {
  const plugin = firewallNatFlavour(pluginId);
  if (!plugin) {
    return { success: false, error: `Port forwarding is not supported for firewall plugin '${pluginId}'` };
  }
  try {
    const options: Record<string, unknown> = {
      port: forward.port,
      proto: forward.proto,
      toport: forward.toport,
      state: 'disabled',
    };
    if (forward.toaddr) options.toaddr = forward.toaddr;
    if (plugin === 'firewalld') options.zone = 'public';

    const task = await compileDaemonPluginMutation(node, 'firewall', plugin, 'forward_port', options);
    task.title = `Remove port forward ${forward.port}/${forward.proto} -> ${
      forward.toaddr || 'same host'
    }:${forward.toport} on ${node.shortName || node.name}`;
    const res = await stageTaskInQueue(task);
    if (res.success) {
      return { success: true, title: task.title };
    }
    return { success: false, error: res.error || 'Failed to stage port forward removal' };
  } catch (err: any) {
    return { success: false, error: err?.message || 'Error staging port forward removal' };
  }
}

/**
 * Enables or disables masquerade on the host through POST
 * /api/nodes/:id/firewall/masquerade. The endpoint applies the change on the
 * real host and reports real errors (Zero-Mock Policy); no optimistic state.
 */
export async function setFirewallMasquerade(
  node: NodeItem,
  pluginId: string,
  enabled: boolean
): Promise<{ success: boolean; error?: string }> {
  const plugin = firewallNatFlavour(pluginId);
  if (!plugin) {
    return { success: false, error: `Masquerade is not supported for firewall plugin '${pluginId}'` };
  }
  try {
    const res = await fetch(`/api/nodes/${encodeURIComponent(node.id)}/firewall/masquerade`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ enabled, plugin }),
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok || !data?.success) {
      return { success: false, error: data?.error || `Failed to ${enabled ? 'enable' : 'disable'} masquerade (status ${res.status})` };
    }
    return { success: true };
  } catch (err: any) {
    return { success: false, error: err?.message || 'Error updating masquerade' };
  }
}

export const FirewallView: React.FC<FirewallViewProps> = ({ node, pluginId }) => {
  if (!node) {
    return (
      <div className="flex flex-col gap-3 font-mono">
        <div className="bg-[#171a2d] border border-[#26293c] rounded p-3">
          <div className="text-[11px] text-[#8d909b]">No node selected</div>
        </div>
      </div>
    );
  }
  const activePluginName = (pluginId && pluginId.startsWith('firewall/') ? pluginId.split('/')[1] : 'firewalld') || 'firewalld';
  const isFirewalld = activePluginName === 'firewalld';
  // Firewall flavours with a real NAT implementation in this codebase.
  const natFlavour = firewallNatFlavour(activePluginName);
  // UFW exposes no native DNAT/masquerade operation, and pve-firewall owns the
  // ruleset on Proxmox nodes. Both surface as greyed controls instead of
  // pretending the mutation worked.
  const isUfw = activePluginName === 'ufw';
  const pveManaged = activePluginName === 'iptables' && (node.group === 'tag:pve' || node.badge === 'PVE');
  const pveLockTitle = 'Not implemented yet';
  const ufwLockTitle = 'UFW has no native DNAT/masquerade support';
  const supportsNat = natFlavour !== null;

  const [rules, setRules] = useState<Array<{
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
  }>>([]);
  const [status, setStatus] = useState<string>('running');
  const [backendLabel, setBackendLabel] = useState<string>(activePluginName);
  const [nftActive, setNftActive] = useState<boolean>(true);
  const [zones, setZones] = useState<Record<string, { interfaces: string[]; sources: string[] }>>({});
  const [loading, setLoading] = useState(false);

  // Advanced rule creation state
  const [showAddForm, setShowAddForm] = useState(false);
  const [newPort, setNewPort] = useState('');
  const [newProto, setNewProto] = useState('tcp');
  const [newSource, setNewSource] = useState('');
  const [newInterface, setNewInterface] = useState('any');
  const [newZone, setNewZone] = useState('public');
  const [newPolicy, setNewPolicy] = useState<'ACCEPT' | 'DROP' | 'REJECT'>('ACCEPT');
  const [newComment, setNewComment] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const [errorMessage, setErrorMessage] = useState<string | null>(null);

  // Port forwarding (NAT MASQ) state
  const [forwardPorts, setForwardPorts] = useState<FirewallPortForward[]>([]);
  // null = the host did not report masquerade state (unknown, never guessed).
  const [masquerade, setMasquerade] = useState<boolean | null>(null);
  const [masqueradeBusy, setMasqueradeBusy] = useState(false);
  const [adopting, setAdopting] = useState(false);
  const [adoptMessage, setAdoptMessage] = useState<string | null>(null);
  const [masqueradeError, setMasqueradeError] = useState<string | null>(null);
  const [fwdPort, setFwdPort] = useState('');
  const [fwdProto, setFwdProto] = useState('tcp');
  const [fwdToPort, setFwdToPort] = useState('');
  const [fwdToAddr, setFwdToAddr] = useState('');
  const [fwdZone, setFwdZone] = useState('public');
  const [stagingForward, setStagingForward] = useState(false);
  const [removingForward, setRemovingForward] = useState<string | null>(null);
  const [fwdError, setFwdError] = useState<string | null>(null);

  const fetchFirewall = (refresh = false) => {
    setLoading(true);
    if (!isFirewalld) {
      // Drop any previous host's NAT view first: a failed read must surface as
      // unknown, never as stale masquerade/forward state.
      setForwardPorts([]);
      setMasquerade(null);
      fetch(`/api/nodes/${encodeURIComponent(node.id)}/plugins/firewall/${encodeURIComponent(activePluginName)}`)
        .then(res => res.json())
        .then(data => {
          if (data && typeof data === 'object') {
            const rawRules = Array.isArray(data.rules) ? data.rules : [];
            setRules(
              rawRules.map((r: any, idx: number) => ({
                id: r.id || `${activePluginName}-${idx + 1}`,
                chain: r.chain || 'INPUT',
                policy: r.policy || 'ACCEPT',
                proto: r.proto || 'tcp',
                port: r.port || 'all',
                source: r.source || '0.0.0.0/0',
                comment: r.comment || '',
                enabled: r.enabled !== undefined ? Boolean(r.enabled) : true,
                zone: r.zone || r.interface || activePluginName,
                rawRule: r.rawRule,
              }))
            );
            setStatus(data.status || 'inactive');
            setBackendLabel(data.backend || activePluginName);
            setNftActive(Boolean(data.nftablesActive));
            setZones(data.zones || {});
            // Real NAT state reported by the daemon; never inferred locally.
            setForwardPorts(Array.isArray(data.forwardPorts) ? data.forwardPorts : []);
            setMasquerade(typeof data.masquerade === 'boolean' ? data.masquerade : null);
          }
        })
        .catch(() => {})
        .finally(() => setLoading(false));
      return;
    }

    fetch(`/api/nodes/${encodeURIComponent(node.id)}/firewall${refresh ? '?refresh=true' : ''}`)
      .then(res => res.json())
      .then(data => {
        if (data.success) {
          if (Array.isArray(data.rules)) setRules(data.rules);
          if (data.status) setStatus(data.status);
          if (data.backend) setBackendLabel(data.backend);
          if (typeof data.nftablesActive === 'boolean') setNftActive(data.nftablesActive);
          if (data.zones) setZones(data.zones);
        }
      })
      .catch(() => {})
      .finally(() => setLoading(false));
  };

  const fetchForwardPorts = () => {
    // nftables / iptables NAT state is delivered by fetchFirewall from the same
    // plugin read; only firewalld needs the dedicated forward-port query.
    if (!isFirewalld) return;
    setForwardPorts([]);
    setMasquerade(null);
    fetch(`/api/nodes/${encodeURIComponent(node.id)}/plugins/firewall/firewalld`)
      .then(res => res.json())
      .then(data => {
        setForwardPorts(Array.isArray(data?.forwardPorts) ? data.forwardPorts : []);
        setMasquerade(typeof data?.masquerade === 'boolean' ? data.masquerade : null);
      })
      .catch(() => {});
  };

  const refreshAll = (refresh = false) => {
    fetchFirewall(refresh);
    fetchForwardPorts();
  };

  useEffect(() => {
    refreshAll(false);
  }, [node.id, node.shortName, activePluginName]);

  const handlePreset = (preset: {
    port: string;
    proto: string;
    source?: string;
    zone?: string;
    comment: string;
    policy?: 'ACCEPT' | 'DROP' | 'REJECT';
  }) => {
    setNewPort(preset.port);
    setNewProto(preset.proto);
    if (preset.source) setNewSource(preset.source);
    if (preset.zone) setNewZone(preset.zone);
    if (preset.policy) setNewPolicy(preset.policy);
    setNewComment(preset.comment);
    setErrorMessage(null);
  };

  const handleAddRule = async (e: React.FormEvent) => {
    e.preventDefault();
    setErrorMessage(null);

    // Validation
    if (newProto !== 'icmp') {
      const portVal = validatePortOrRange(newPort);
      if (!portVal.valid) {
        setErrorMessage(portVal.error || 'Invalid port or port range');
        return;
      }
    }

    if (newSource.trim() && newSource.trim().toLowerCase() !== 'any') {
      const sourceVal = validateCidrOrIp(newSource);
      if (!sourceVal.valid) {
        setErrorMessage(sourceVal.error || 'Invalid source CIDR or IP');
        return;
      }
    }

    if (newInterface.trim() && newInterface.trim().toLowerCase() !== 'any') {
      const ifaceVal = validateInterfaceName(newInterface);
      if (!ifaceVal.valid) {
        setErrorMessage(ifaceVal.error || 'Invalid interface name');
        return;
      }
    }

    setSubmitting(true);
    try {
      if (!isFirewalld) {
        const actionName = newPolicy === 'ACCEPT' ? 'allow_port' : 'deny_port';
        const task = await compileDaemonPluginMutation(node, 'firewall', activePluginName, actionName, {
          port: newProto === 'icmp' ? '22' : newPort.trim(),
          proto: newProto === 'all' || newProto === 'icmp' ? 'tcp' : newProto,
          source: newSource.trim() || undefined,
          comment: newComment.trim() || undefined,
        });
        const staged = await stageTaskInQueue(task);
        if (!staged.success) {
          throw new Error(staged.error || `Failed to stage ${activePluginName} rule`);
        }
      } else {
        const res = await fetch(`/api/nodes/${encodeURIComponent(node.id)}/firewall/rules`, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            port: newProto === 'icmp' ? undefined : newPort.trim(),
            protocol: newProto,
            source: newSource.trim() || undefined,
            inInterface: newInterface.trim() || undefined,
            zone: newZone,
            policy: newPolicy,
            comment: newComment.trim() || undefined,
          }),
        });
        const data = await res.json();
        if (!res.ok || !data.success) {
          throw new Error(data.error || 'Failed to add firewall rule');
        }
      }

      refreshAll(true);
      setNewPort('');
      setNewSource('');
      setNewComment('');
      setShowAddForm(false);
    } catch (err: any) {
      setErrorMessage(err?.message || 'Error adding firewall rule');
    } finally {
      setSubmitting(false);
    }
  };

  const handleStageForward = async (e: React.FormEvent) => {
    e.preventDefault();
    setFwdError(null);
    setStagingForward(true);
    try {
      const res = await stageFirewallPortForward(node, {
        port: fwdPort,
        proto: fwdProto,
        toport: fwdToPort,
        toaddr: fwdToAddr,
        zone: fwdZone,
        plugin: activePluginName,
      });
      if (!res.success) {
        setFwdError(res.error || 'Failed to stage port forward');
        return;
      }
      setFwdPort('');
      setFwdToPort('');
      setFwdToAddr('');
      refreshAll(true);
    } catch (err: any) {
      setFwdError(err?.message || 'Error staging port forward');
    } finally {
      setStagingForward(false);
    }
  };

  const handleRemoveForward = async (fp: FirewallPortForward) => {
    setFwdError(null);
    setRemovingForward(`${fp.port}-${fp.proto}-${fp.toport}-${fp.toaddr || ''}`);
    try {
      const res = await removeFirewallPortForward(node, activePluginName, fp);
      if (!res.success) {
        setFwdError(res.error || 'Failed to stage port forward removal');
        return;
      }
      refreshAll(true);
    } catch (err: any) {
      setFwdError(err?.message || 'Error staging port forward removal');
    } finally {
      setRemovingForward(null);
    }
  };

  const handleToggleMasquerade = async () => {
    setMasqueradeError(null);
    setMasqueradeBusy(true);
    try {
      // Unknown state resolves to "enable": adding the rule is idempotent.
      const next = masquerade !== true;
      const res = await setFirewallMasquerade(node, activePluginName, next);
      if (!res.success) {
        setMasqueradeError(res.error || `Failed to ${next ? 'enable' : 'disable'} masquerade`);
        return;
      }
      refreshAll(true);
    } catch (err: any) {
      setMasqueradeError(err?.message || 'Error updating masquerade');
    } finally {
      setMasqueradeBusy(false);
    }
  };

  // Import NAT rules that already exist on the host (created outside ATC) into
  // the managed model, so the next rendered drop-in keeps them.
  const handleAdoptNat = async () => {
    setAdopting(true);
    setAdoptMessage(null);
    setMasqueradeError(null);
    try {
      const res = await fetch(`/api/nodes/${encodeURIComponent(node.id)}/firewall/adopt`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ plugin: activePluginName }),
      });
      const data = await res.json().catch(() => ({}));
      if (!res.ok || !data?.success) {
        setMasqueradeError(data?.error || `Adopt failed (status ${res.status})`);
        return;
      }
      setAdoptMessage(
        `${data.adopted} rule${data.adopted === 1 ? '' : 's'} adopted · ${data.managed} managed in total.`,
      );
      refreshAll(true);
    } catch (err: any) {
      setMasqueradeError(err?.message || 'Error adopting NAT rules');
    } finally {
      setAdopting(false);
    }
  };

  const handleDeleteRule = async (rule: {
    id: string;
    port?: string;
    proto?: string;
    chain?: string;
    policy?: string;
    zone?: string;
    rawRule?: string;
  }) => {
    try {
      if (!isFirewalld) {
        const task = await compileDaemonPluginMutation(node, 'firewall', activePluginName, 'delete_port', {
          port: rule.port || '22',
          proto: rule.proto === 'all' || !rule.proto ? 'tcp' : rule.proto,
          chain: rule.chain || 'INPUT',
          jump: rule.policy || 'ACCEPT',
          rule: rule.policy === 'DROP' ? 'deny' : rule.policy === 'REJECT' ? 'reject' : 'allow',
        });
        const staged = await stageTaskInQueue(task);
        if (!staged.success) {
          setErrorMessage(staged.error || `Failed to stage ${activePluginName} rule deletion`);
          return;
        }
        refreshAll(true);
        return;
      }

      const params = new URLSearchParams();
      if (rule.zone) params.set('zone', rule.zone);
      if (rule.rawRule) params.set('rawRule', rule.rawRule);
      const query = params.toString() ? `?${params.toString()}` : '';
      const res = await fetch(`/api/nodes/${encodeURIComponent(node.id)}/firewall/rules/${encodeURIComponent(rule.id)}${query}`, {
        method: 'DELETE',
      });
      if (res.ok) {
        refreshAll(true);
      }
    } catch {
      // error
    }
  };

  return (
    <section className="bg-[#171a2d] rounded-lg p-4 flex flex-col border border-[#313447] shadow-lg">
      <div className="flex flex-wrap items-center justify-between gap-2 pb-3 border-b border-[#26293c]">
        <div className="flex items-center gap-2">
          <span className="material-symbols-outlined text-[#ffb4ab] text-[22px]">security</span>
          <div>
            <h3 className="font-sans text-sm text-[#dfe1fa] font-bold">
              Firewall &amp; Packet Filtering ({isFirewalld ? 'nftables / firewalld' : backendLabel} on {node.shortName})
            </h3>
            <p className="font-mono text-[11px] text-[#8d909b]">
              Stateful connection tracking, port ranges, CIDR restrictions &amp; zone isolation
            </p>
          </div>
        </div>
        <div className="flex items-center gap-2">
          <span className="px-2 py-0.5 bg-[#8cd6cb]/15 text-[#8cd6cb] font-mono text-xs rounded border border-[#8cd6cb]/30">
            {activePluginName}: {status.toUpperCase()} ({isFirewalld && nftActive ? 'nftables ENFORCING' : 'ACTIVE'})
          </span>
          <button
            type="button"
            onClick={() => refreshAll(true)}
            disabled={loading}
            className="flex items-center gap-1 bg-[#26293c] hover:bg-[#35384c] text-[#dfe1fa] px-2.5 py-1 rounded transition-colors border border-[#434750]/40 font-mono text-xs cursor-pointer disabled:opacity-50"
          >
            <span className={`material-symbols-outlined text-[14px] ${loading ? 'animate-spin' : ''}`}>sync</span>
            <span>Refresh</span>
          </button>
          <button
            type="button"
            onClick={() => {
              setShowAddForm(prev => !prev);
              setErrorMessage(null);
            }}
            className="flex items-center gap-1 bg-[#b0c9ff] hover:bg-[#d8e2ff] text-[#002e68] font-bold font-mono text-xs px-2.5 py-1 rounded transition-colors cursor-pointer"
          >
            <span className="material-symbols-outlined text-[14px]">
              {showAddForm ? 'close' : 'add'}
            </span>
            <span>{showAddForm ? 'Close Form' : '+ Add Rule'}</span>
          </button>
        </div>
      </div>

      {/* Active Zones Pill Bar */}
      {Object.keys(zones).length > 0 && (
        <div className="mt-2.5 flex flex-wrap items-center gap-2 font-mono text-xs bg-[#0a0d1f] p-2 rounded border border-[#26293c]">
          <span className="text-[#8d909b] text-[11px] font-bold">ACTIVE ZONES:</span>
          {Object.entries(zones).map(([zoneName, zInfo]) => (
            <span
              key={zoneName}
              className="px-2 py-0.5 rounded bg-[#26293c] text-[#dfe1fa] border border-[#434750]/40 flex items-center gap-1.5"
            >
              <span className="w-1.5 h-1.5 rounded-full bg-[#8cd6cb]"></span>
              <strong className="text-[#b0c9ff]">{zoneName}</strong>: {zInfo.interfaces.join(', ') || zInfo.sources.join(', ') || '(default)'}
            </span>
          ))}
        </div>
      )}

      {showAddForm && (
        <div className="mt-3 p-3 bg-[#0a0d1f] rounded border border-[#b0c9ff]/40 space-y-3 font-mono text-xs">
          {/* Quick Presets Bar */}
          <div className="flex flex-wrap items-center gap-1.5 pb-2 border-b border-[#26293c]">
            <span className="text-[10px] text-[#8d909b] font-bold">QUICK PRESETS:</span>
            <button
              type="button"
              onClick={() =>
                handlePreset({
                  port: '30000-32767',
                  proto: 'tcp',
                  zone: 'public',
                  policy: 'ACCEPT',
                  comment: 'Kubernetes NodePort Range',
                })
              }
              className="px-2 py-0.5 bg-[#171a2d] hover:bg-[#26293c] text-[#b0c9ff] border border-[#434750]/40 rounded text-[10px] cursor-pointer"
            >
              K8s NodePorts (30000-32767)
            </button>
            <button
              type="button"
              onClick={() =>
                handlePreset({
                  port: '8000-8080',
                  proto: 'tcp',
                  source: '100.64.0.0/10',
                  zone: 'trusted',
                  policy: 'ACCEPT',
                  comment: 'Dev HTTP Services via Tailscale Mesh',
                })
              }
              className="px-2 py-0.5 bg-[#171a2d] hover:bg-[#26293c] text-[#8cd6cb] border border-[#434750]/40 rounded text-[10px] cursor-pointer"
            >
              Tailscale 8000-8080
            </button>
            <button
              type="button"
              onClick={() =>
                handlePreset({
                  port: '443',
                  proto: 'tcp',
                  zone: 'public',
                  policy: 'ACCEPT',
                  comment: 'HTTPS Web Traffic',
                })
              }
              className="px-2 py-0.5 bg-[#171a2d] hover:bg-[#26293c] text-[#dfe1fa] border border-[#434750]/40 rounded text-[10px] cursor-pointer"
            >
              HTTPS (443)
            </button>
            <button
              type="button"
              onClick={() =>
                handlePreset({
                  port: '51820',
                  proto: 'udp',
                  zone: 'public',
                  policy: 'ACCEPT',
                  comment: 'WireGuard VPN Tunnel',
                })
              }
              className="px-2 py-0.5 bg-[#171a2d] hover:bg-[#26293c] text-[#d9b9ff] border border-[#434750]/40 rounded text-[10px] cursor-pointer"
            >
              WireGuard (51820/udp)
            </button>
            <button
              type="button"
              onClick={() =>
                handlePreset({
                  port: '23',
                  proto: 'tcp',
                  zone: 'public',
                  policy: 'DROP',
                  comment: 'Drop Insecure Telnet',
                })
              }
              className="px-2 py-0.5 bg-[#93000a]/20 hover:bg-[#93000a]/40 text-[#ffb4ab] border border-[#ffb4ab]/40 rounded text-[10px] cursor-pointer"
            >
              Drop Telnet (23)
            </button>
          </div>

          {errorMessage && (
            <div className="p-2 bg-[#93000a]/20 border border-[#ffb4ab]/50 rounded text-[#ffb4ab] text-[11px] flex items-center gap-1.5">
              <span className="material-symbols-outlined text-[16px]">error</span>
              <span>{errorMessage}</span>
            </div>
          )}

          <form onSubmit={handleAddRule} className="grid grid-cols-1 md:grid-cols-4 lg:grid-cols-7 gap-2 items-end">
            <div>
              <label className="block text-[10px] text-[#8d909b] mb-0.5">PROTOCOL</label>
              <select
                value={newProto}
                onChange={e => setNewProto(e.target.value)}
                className="w-full bg-[#171a2d] border border-[#26293c] rounded px-2 py-1 text-[#dfe1fa]"
              >
                <option value="tcp">TCP</option>
                <option value="udp">UDP</option>
                <option value="sctp">SCTP</option>
                <option value="icmp">ICMP (Ping)</option>
                <option value="all">ALL</option>
              </select>
            </div>

            <div>
              <label className="block text-[10px] text-[#8d909b] mb-0.5">
                PORT / RANGE {newProto === 'icmp' && '(N/A)'}
              </label>
              <input
                type="text"
                disabled={newProto === 'icmp'}
                required={newProto !== 'icmp'}
                value={newPort}
                onChange={e => setNewPort(e.target.value)}
                placeholder="e.g. 443 or 8000-8080"
                className="w-full bg-[#171a2d] border border-[#26293c] rounded px-2 py-1 text-[#dfe1fa] disabled:opacity-40"
              />
            </div>

            <div>
              <label className="block text-[10px] text-[#8d909b] mb-0.5">SOURCE CIDR / IP</label>
              <input
                type="text"
                value={newSource}
                onChange={e => setNewSource(e.target.value)}
                placeholder="any or 100.64.0.0/10"
                className="w-full bg-[#171a2d] border border-[#26293c] rounded px-2 py-1 text-[#dfe1fa]"
              />
            </div>

            <div>
              <label className="block text-[10px] text-[#8d909b] mb-0.5">INBOUND IFACE</label>
              <input
                type="text"
                value={newInterface}
                onChange={e => setNewInterface(e.target.value)}
                placeholder="any or tailscale0, eth0"
                className="w-full bg-[#171a2d] border border-[#26293c] rounded px-2 py-1 text-[#dfe1fa]"
              />
            </div>

            <div>
              <label className="block text-[10px] text-[#8d909b] mb-0.5">ZONE</label>
              <select
                value={newZone}
                onChange={e => setNewZone(e.target.value)}
                className="w-full bg-[#171a2d] border border-[#26293c] rounded px-2 py-1 text-[#dfe1fa]"
              >
                <option value="public">public (default)</option>
                <option value="internal">internal</option>
                <option value="trusted">trusted</option>
                <option value="dmz">dmz</option>
                <option value="drop">drop</option>
              </select>
            </div>

            <div>
              <label className="block text-[10px] text-[#8d909b] mb-0.5">POLICY</label>
              <select
                value={newPolicy}
                onChange={e => setNewPolicy(e.target.value as 'ACCEPT' | 'DROP' | 'REJECT')}
                className="w-full bg-[#171a2d] border border-[#26293c] rounded px-2 py-1 text-[#dfe1fa]"
              >
                <option value="ACCEPT">ACCEPT</option>
                <option value="DROP">DROP</option>
                <option value="REJECT">REJECT</option>
              </select>
            </div>

            <div>
              <label className="block text-[10px] text-[#8d909b] mb-0.5">COMMENT</label>
              <input
                type="text"
                value={newComment}
                onChange={e => setNewComment(e.target.value)}
                placeholder="Rule description"
                className="w-full bg-[#171a2d] border border-[#26293c] rounded px-2 py-1 text-[#dfe1fa]"
              />
            </div>

            <div className="flex gap-1.5 md:col-span-4 lg:col-span-7 justify-end pt-1">
              <button
                type="button"
                onClick={() => {
                  setShowAddForm(false);
                  setErrorMessage(null);
                }}
                className="px-3 py-1 bg-[#26293c] text-[#c3c6d2] rounded cursor-pointer hover:bg-[#35384c] transition-colors"
              >
                Cancel
              </button>
              <button
                type="submit"
                disabled={submitting}
                className="px-4 py-1 bg-[#8cd6cb] hover:bg-[#a0ebe0] text-[#002e68] font-bold rounded cursor-pointer transition-colors disabled:opacity-50 flex items-center gap-1"
              >
                {submitting && <span className="material-symbols-outlined text-[14px] animate-spin">sync</span>}
                <span>{submitting ? 'Applying...' : 'Apply Rule'}</span>
              </button>
            </div>
          </form>
        </div>
      )}

      <div className="overflow-x-auto mt-3 font-mono text-xs">
        <table className="w-full text-left">
          <thead className="bg-[#0a0d1f] text-[#8d909b] text-[10px] uppercase">
            <tr className="h-7">
              <th className="px-3">State</th>
              <th className="px-3">Zone</th>
              <th className="px-3">Chain</th>
              <th className="px-3">Policy</th>
              <th className="px-3">Proto</th>
              <th className="px-3">Port / Range</th>
              <th className="px-3">Source IP / CIDR</th>
              <th className="px-3">Rule Description</th>
              <th className="px-3 text-right">Action</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-[#26293c]/60">
            {rules.map(rule => (
              <tr key={rule.id} className="h-9 hover:bg-[#26293c]/40 transition-colors">
                <td className="px-3">
                  <span
                    className={`w-2 h-2 rounded-full inline-block ${
                      rule.enabled ? 'bg-[#8cd6cb]' : 'bg-[#8d909b]'
                    }`}
                  ></span>
                </td>
                <td className="px-3 text-[#dfe1fa] font-mono">{rule.zone || 'public'}</td>
                <td className="px-3 text-[#b0c9ff] font-bold">{rule.chain}</td>
                <td className="px-3">
                  <span
                    className={`px-1.5 py-0.2 rounded text-[10px] font-bold ${
                      rule.policy === 'ACCEPT'
                        ? 'bg-[#8cd6cb]/20 text-[#8cd6cb]'
                        : 'bg-[#ffb4ab]/20 text-[#ffb4ab]'
                    }`}
                  >
                    {rule.policy}
                  </span>
                </td>
                <td className="px-3 uppercase text-[#d9b9ff]">{rule.proto}</td>
                <td className="px-3 font-semibold text-[#dfe1fa]">{rule.port}</td>
                <td className="px-3 text-[#8d909b]">{rule.source}</td>
                <td className="px-3 text-[#c3c6d2] max-w-xs truncate" title={rule.comment}>
                  {rule.comment}
                </td>
                <td className="px-3 text-right">
                  <button
                    type="button"
                    onClick={() => handleDeleteRule(rule)}
                    className="px-2 py-0.5 bg-[#93000a]/20 hover:bg-[#93000a]/40 text-[#ffb4ab] border border-[#ffb4ab]/30 rounded text-[10px] cursor-pointer"
                  >
                    Delete
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      {/* Port Forwarding (NAT MASQ / DNAT) */}
      {supportsNat && (
      <div className="mt-4 pt-3 border-t border-[#26293c] font-mono text-xs">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <div className="flex items-center gap-2">
            <span className="material-symbols-outlined text-[#d9b9ff] text-[18px]">alt_route</span>
            <h4 className="font-sans text-[13px] text-[#dfe1fa] font-bold">Port Forwarding (NAT MASQ)</h4>
            <span
              className={`px-2 py-0.5 rounded text-[10px] font-bold border ${
                masquerade === true
                  ? 'bg-[#8cd6cb]/15 text-[#8cd6cb] border-[#8cd6cb]/30'
                  : 'bg-[#26293c] text-[#8d909b] border-[#434750]/40'
              }`}
            >
              MASQUERADE: {masquerade === null ? 'UNKNOWN' : masquerade ? 'ENABLED' : 'DISABLED'}
            </span>
            <button
              type="button"
              onClick={handleAdoptNat}
              disabled={adopting || pveManaged}
              data-testid="adopt-nat-rules"
              title={pveManaged ? pveLockTitle : "Import NAT rules that already exist on this host into ATC's managed model"}
              className="px-2 py-0.5 rounded text-[10px] font-bold border bg-[#171a2d] hover:bg-[#21263d] text-[#d9b9ff] border-[#d9b9ff]/40 cursor-pointer transition-colors disabled:opacity-50"
            >
              {adopting ? 'Adopting...' : 'Adopt Existing NAT'}
            </button>
            <button
              type="button"
              onClick={handleToggleMasquerade}
              disabled={masqueradeBusy || isUfw || pveManaged}
              title={
                pveManaged
                  ? pveLockTitle
                  : isUfw
                  ? ufwLockTitle
                  : `${masquerade === true ? 'Disable' : 'Enable'} masquerade on ${node.shortName || node.name} (${activePluginName})`
              }
              className={`px-2 py-0.5 rounded text-[10px] font-bold border cursor-pointer transition-colors disabled:opacity-50 ${
                masquerade === true
                  ? 'bg-[#93000a]/20 hover:bg-[#93000a]/40 text-[#ffb4ab] border-[#ffb4ab]/40'
                  : 'bg-[#b0c9ff] hover:bg-[#d8e2ff] text-[#002e68] border-[#434750]/40'
              }`}
            >
              {masqueradeBusy ? 'Applying...' : masquerade === true ? 'Disable Masquerade' : 'Enable Masquerade'}
            </button>
          </div>
          <p className="text-[10px] text-[#8d909b]">
            Forward an external port to an internal host &amp; port; staged as a declarative {activePluginName} task.
          </p>
        </div>

        {(pveManaged || isUfw) && (
          <div className="mt-2 p-2 bg-[#171a2d] border border-[#434750]/50 rounded text-[11px] text-[#8d909b] flex items-center gap-1.5">
            <span className="material-symbols-outlined text-[16px] text-[#ffd97d]">lock</span>
            <span>
              {pveManaged
                ? 'Firewall is managed by pve-firewall — view only. Mutations are not implemented yet.'
                : 'UFW cannot express DNAT or masquerade through its CLI; port forwarding and masquerade are read-only here.'}
            </span>
          </div>
        )}

        {adoptMessage && (
          <div className="mt-2 p-2 bg-[#162620] border border-[#8cd6cb]/40 rounded text-[#8cd6cb] text-[11px] flex items-center gap-1.5">
            <span className="material-symbols-outlined text-[16px]">check_circle</span>
            <span>{adoptMessage}</span>
          </div>
        )}

        {masqueradeError && (
          <div className="mt-2 p-2 bg-[#93000a]/20 border border-[#ffb4ab]/50 rounded text-[#ffb4ab] text-[11px] flex items-center gap-1.5">
            <span className="material-symbols-outlined text-[16px]">error</span>
            <span>{masqueradeError}</span>
          </div>
        )}

        {fwdError && (
          <div className="mt-2 p-2 bg-[#93000a]/20 border border-[#ffb4ab]/50 rounded text-[#ffb4ab] text-[11px] flex items-center gap-1.5">
            <span className="material-symbols-outlined text-[16px]">error</span>
            <span>{fwdError}</span>
          </div>
        )}

        <form onSubmit={handleStageForward} className="mt-2.5 grid grid-cols-1 md:grid-cols-3 lg:grid-cols-6 gap-2 items-end">
          <div>
            <label className="block text-[10px] text-[#8d909b] mb-0.5">EXTERNAL PORT</label>
            <input
              type="text"
              required
              value={fwdPort}
              onChange={e => setFwdPort(e.target.value)}
              placeholder="e.g. 8080"
              className="w-full bg-[#171a2d] border border-[#26293c] rounded px-2 py-1 text-[#dfe1fa]"
            />
          </div>

          <div>
            <label className="block text-[10px] text-[#8d909b] mb-0.5">PROTOCOL</label>
            <select
              value={fwdProto}
              onChange={e => setFwdProto(e.target.value)}
              className="w-full bg-[#171a2d] border border-[#26293c] rounded px-2 py-1 text-[#dfe1fa]"
            >
              <option value="tcp">TCP</option>
              <option value="udp">UDP</option>
            </select>
          </div>

          <div>
            <label className="block text-[10px] text-[#8d909b] mb-0.5">INTERNAL HOST / IP</label>
            <input
              type="text"
              value={fwdToAddr}
              onChange={e => setFwdToAddr(e.target.value)}
              placeholder="blank = same host, or 192.168.1.10"
              className="w-full bg-[#171a2d] border border-[#26293c] rounded px-2 py-1 text-[#dfe1fa]"
            />
          </div>

          <div>
            <label className="block text-[10px] text-[#8d909b] mb-0.5">INTERNAL PORT</label>
            <input
              type="text"
              required
              value={fwdToPort}
              onChange={e => setFwdToPort(e.target.value)}
              placeholder="e.g. 80"
              className="w-full bg-[#171a2d] border border-[#26293c] rounded px-2 py-1 text-[#dfe1fa]"
            />
          </div>

          {isFirewalld && (
          <div>
            <label className="block text-[10px] text-[#8d909b] mb-0.5">ZONE</label>
            <select
              value={fwdZone}
              onChange={e => setFwdZone(e.target.value)}
              className="w-full bg-[#171a2d] border border-[#26293c] rounded px-2 py-1 text-[#dfe1fa]"
            >
              <option value="public">public (default)</option>
              <option value="internal">internal</option>
              <option value="trusted">trusted</option>
              <option value="dmz">dmz</option>
              <option value="external">external</option>
            </select>
          </div>
          )}

          <div className="flex justify-end lg:col-span-1">
            <button
              type="submit"
              disabled={stagingForward}
              title={`Stage ${activePluginName} port-forward task into Ansible Mutation Queue`}
              className="w-full flex items-center justify-center gap-1.5 bg-[#d9b9ff] hover:bg-[#e9d4ff] text-[#2a0a54] font-bold px-3 py-1 rounded transition-colors shadow-sm cursor-pointer disabled:opacity-50"
            >
              <span className="material-symbols-outlined text-[15px]">alt_route</span>
              <span>{stagingForward ? 'Staging...' : 'Stage Port Forward'}</span>
            </button>
          </div>
        </form>

        <div className="overflow-x-auto mt-2.5">
          <table className="w-full text-left">
            <thead className="bg-[#0a0d1f] text-[#8d909b] text-[10px] uppercase">
              <tr className="h-7">
                <th className="px-3">External</th>
                <th className="px-3">Proto</th>
                <th className="px-3">Internal Host</th>
                <th className="px-3">Internal Port</th>
                <th className="px-3 text-right">Action</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-[#26293c]/60">
              {forwardPorts.length === 0 ? (
                <tr className="h-9">
                  <td className="px-3 text-[#8d909b]" colSpan={5}>
                    No port forwards configured in the active {isFirewalld ? 'zone' : 'ruleset'}.
                  </td>
                </tr>
              ) : (
                forwardPorts.map((fp, i) => {
                  const forwardKey = `${fp.port}-${fp.proto}-${fp.toport}-${fp.toaddr || ''}-${i}`;
                  return (
                    <tr key={forwardKey} className="h-9 hover:bg-[#26293c]/40 transition-colors">
                      <td className="px-3 font-semibold text-[#dfe1fa]">{fp.port}</td>
                      <td className="px-3 uppercase text-[#d9b9ff]">{fp.proto}</td>
                      <td className="px-3 text-[#8d909b]">{fp.toaddr || 'same host'}</td>
                      <td className="px-3 font-semibold text-[#dfe1fa]">{fp.toport}</td>
                      <td className="px-3 text-right">
                        <button
                          type="button"
                          onClick={() => handleRemoveForward(fp)}
                          disabled={removingForward === `${fp.port}-${fp.proto}-${fp.toport}-${fp.toaddr || ''}`}
                          className="px-2 py-0.5 bg-[#93000a]/20 hover:bg-[#93000a]/40 text-[#ffb4ab] border border-[#ffb4ab]/30 rounded text-[10px] cursor-pointer disabled:opacity-50"
                        >
                          {removingForward === `${fp.port}-${fp.proto}-${fp.toport}-${fp.toaddr || ''}` ? 'Removing...' : 'Remove'}
                        </button>
                      </td>
                    </tr>
                  );
                })
              )}
            </tbody>
          </table>
        </div>
      </div>
      )}
    </section>
  );
};
