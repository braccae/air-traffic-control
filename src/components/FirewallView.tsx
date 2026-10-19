import React, { useState, useEffect } from 'react';
import type { NodeItem } from '../types/index.ts';
import { validatePortOrRange, validateCidrOrIp, validateInterfaceName } from '../utils/firewallRules.ts';

interface FirewallViewProps {
  node: NodeItem;
}

export const FirewallView: React.FC<FirewallViewProps> = ({ node }) => {
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

  const fetchFirewall = (refresh = false) => {
    setLoading(true);
    fetch(`/api/nodes/${encodeURIComponent(node.id)}/firewall${refresh ? '?refresh=true' : ''}`)
      .then(res => res.json())
      .then(data => {
        if (data.success) {
          if (Array.isArray(data.rules)) setRules(data.rules);
          if (data.status) setStatus(data.status);
          if (typeof data.nftablesActive === 'boolean') setNftActive(data.nftablesActive);
          if (data.zones) setZones(data.zones);
        }
      })
      .catch(() => {})
      .finally(() => setLoading(false));
  };

  useEffect(() => {
    fetchFirewall(false);
  }, [node.id, node.shortName]);

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

      fetchFirewall(true);
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

  const handleDeleteRule = async (rule: { id: string; zone?: string; rawRule?: string }) => {
    try {
      const params = new URLSearchParams();
      if (rule.zone) params.set('zone', rule.zone);
      if (rule.rawRule) params.set('rawRule', rule.rawRule);
      const query = params.toString() ? `?${params.toString()}` : '';
      const res = await fetch(`/api/nodes/${encodeURIComponent(node.id)}/firewall/rules/${encodeURIComponent(rule.id)}${query}`, {
        method: 'DELETE',
      });
      if (res.ok) {
        fetchFirewall(true);
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
              Firewall &amp; Packet Filtering (nftables / firewalld on {node.shortName})
            </h3>
            <p className="font-mono text-[11px] text-[#8d909b]">
              Stateful connection tracking, port ranges, CIDR restrictions &amp; zone isolation
            </p>
          </div>
        </div>
        <div className="flex items-center gap-2">
          <span className="px-2 py-0.5 bg-[#8cd6cb]/15 text-[#8cd6cb] font-mono text-xs rounded border border-[#8cd6cb]/30">
            firewalld: {status.toUpperCase()} ({nftActive ? 'nftables ENFORCING' : 'ACTIVE'})
          </span>
          <button
            type="button"
            onClick={() => fetchFirewall(true)}
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
    </section>
  );
};
