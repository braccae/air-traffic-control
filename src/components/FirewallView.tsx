import React, { useState, useEffect } from 'react';
import type { NodeItem } from '../types/index.ts';

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
  }>>([]);
  const [status, setStatus] = useState<string>('running');
  const [nftActive, setNftActive] = useState<boolean>(true);
  const [zones, setZones] = useState<Record<string, { interfaces: string[]; sources: string[] }>>({});
  const [loading, setLoading] = useState(false);

  const [showAddForm, setShowAddForm] = useState(false);
  const [newPort, setNewPort] = useState('');
  const [newProto, setNewProto] = useState('tcp');
  const [newComment, setNewComment] = useState('');

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

  const handleAddRule = (e: React.FormEvent) => {
    e.preventDefault();
    if (!newPort.trim()) return;
    const newRule = {
      id: `fw-${Date.now()}`,
      chain: 'INPUT',
      policy: 'ACCEPT',
      proto: newProto,
      port: newPort.trim(),
      source: '100.64.0.0/10 (Tailscale)',
      comment: newComment.trim() || 'Custom service rule',
      enabled: true,
    };
    setRules(prev => [...prev.slice(0, -1), newRule, prev[prev.length - 1]]);
    setNewPort('');
    setNewComment('');
    setShowAddForm(false);
  };

  const toggleRule = (id: string) => {
    setRules(prev =>
      prev.map(r => (r.id === id ? { ...r, enabled: !r.enabled } : r))
    );
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
              Stateful connection tracking, Tailscale ACL enforcement &amp; host port shielding
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
            onClick={() => setShowAddForm(prev => !prev)}
            className="flex items-center gap-1 bg-[#b0c9ff] hover:bg-[#d8e2ff] text-[#002e68] font-bold font-mono text-xs px-2.5 py-1 rounded transition-colors cursor-pointer"
          >
            <span className="material-symbols-outlined text-[14px]">add</span>
            <span>+ Add Rule</span>
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
        <form
          onSubmit={handleAddRule}
          className="mt-3 p-3 bg-[#0a0d1f] rounded border border-[#b0c9ff]/40 grid grid-cols-1 md:grid-cols-4 gap-2 font-mono text-xs items-end"
        >
          <div>
            <label className="block text-[10px] text-[#8d909b] mb-0.5">PROTOCOL</label>
            <select
              value={newProto}
              onChange={e => setNewProto(e.target.value)}
              className="w-full bg-[#171a2d] border border-[#26293c] rounded px-2 py-1 text-[#dfe1fa]"
            >
              <option value="tcp">TCP</option>
              <option value="udp">UDP</option>
              <option value="all">ALL</option>
            </select>
          </div>
          <div>
            <label className="block text-[10px] text-[#8d909b] mb-0.5">PORT / RANGE</label>
            <input
              type="text"
              required
              value={newPort}
              onChange={e => setNewPort(e.target.value)}
              placeholder="e.g. 8443, 25565"
              className="w-full bg-[#171a2d] border border-[#26293c] rounded px-2 py-1 text-[#dfe1fa]"
            />
          </div>
          <div>
            <label className="block text-[10px] text-[#8d909b] mb-0.5">COMMENT / SERVICE</label>
            <input
              type="text"
              value={newComment}
              onChange={e => setNewComment(e.target.value)}
              placeholder="e.g. Game server / Vault"
              className="w-full bg-[#171a2d] border border-[#26293c] rounded px-2 py-1 text-[#dfe1fa]"
            />
          </div>
          <div className="flex gap-1.5">
            <button
              type="submit"
              className="px-3 py-1 bg-[#8cd6cb] text-[#002e68] font-bold rounded cursor-pointer"
            >
              Apply Rule
            </button>
            <button
              type="button"
              onClick={() => setShowAddForm(false)}
              className="px-2 py-1 bg-[#26293c] text-[#c3c6d2] rounded cursor-pointer"
            >
              Cancel
            </button>
          </div>
        </form>
      )}

      <div className="overflow-x-auto mt-3 font-mono text-xs">
        <table className="w-full text-left">
          <thead className="bg-[#0a0d1f] text-[#8d909b] text-[10px] uppercase">
            <tr className="h-7">
              <th className="px-3">State</th>
              <th className="px-3">Chain</th>
              <th className="px-3">Policy</th>
              <th className="px-3">Proto</th>
              <th className="px-3">Port</th>
              <th className="px-3">Source IP / Network</th>
              <th className="px-3">Rule Description</th>
              <th className="px-3 text-right">Toggle</th>
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
                <td className="px-3 text-[#c3c6d2]">{rule.comment}</td>
                <td className="px-3 text-right">
                  <button
                    type="button"
                    onClick={() => toggleRule(rule.id)}
                    className="px-2 py-0.5 bg-[#26293c] hover:bg-[#35384c] text-[#dfe1fa] rounded text-[10px] cursor-pointer"
                  >
                    {rule.enabled ? 'Disable' : 'Enable'}
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
