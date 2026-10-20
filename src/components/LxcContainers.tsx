import React, { useState, useEffect } from 'react';
import type { NodeItem } from '../types/index.ts';
import { PluginStateView } from '../plugins/kit/index.ts';

// ---------------------------------------------------------------------------
// This view is the UI half of the UNTESTED LXC plugin stub (vm/lxc).
//
// It reports honestly: when no libvirt LXC host is available it renders an
// explicit "unavailable" panel with the daemon's error string and shows ZERO
// containers. It never invents container data (Zero-Mock Policy, AGENTS.md §2).
//
// Mutations are limited to start/shutdown/destroy/suspend. Every operation the
// stub does not implement is listed from the daemon payload and rendered as an
// explicitly-disabled row.
// ---------------------------------------------------------------------------

export interface LxcContainer {
  name: string;
  id: string;
  status: string;
  vcpus: number;
  ramMiB: number;
  ramGiB: number;
  type: string;
}

export interface LxcUnimplementedOp {
  name: string;
  reason: string;
}

export interface LxcReadResult {
  hasLibvirt: boolean;
  libvirtError?: string;
  uri?: string;
  driverActive?: boolean;
  containers?: LxcContainer[];
  unimplemented?: LxcUnimplementedOp[];
  verified?: boolean;
  verifiedNote?: string;
}

/** The mutations this stub actually implements (mirrors Go Actions()). */
export const LXC_ACTIONS = ['start', 'shutdown', 'destroy', 'suspend'] as const;

/**
 * Pure honest-failure discriminator. Given the daemon payload (or null), decide
 * which state the tab renders. Exported for unit testing.
 */
export function lxcAvailability(
  data: LxcReadResult | null | undefined
): 'unavailable' | 'empty' | 'ready' {
  if (!data || !data.hasLibvirt) return 'unavailable';
  if (!data.containers || data.containers.length === 0) return 'empty';
  return 'ready';
}

/** Human-readable reason shown when the host is unavailable. */
export function lxcUnavailableMessage(data: LxcReadResult | null | undefined): string {
  if (data?.libvirtError) return data.libvirtError;
  return 'No libvirt LXC host available on this node (virsh -c lxc:/// unreachable).';
}

/** Format a container's specs, keeping unknown (-1) values explicit. */
export function formatLxcSpec(c: LxcContainer): string {
  const cpu = c.vcpus >= 0 ? `${c.vcpus} vCPU` : 'vCPU unknown';
  const ram =
    c.ramGiB >= 0 ? `${Math.round(c.ramGiB * 100) / 100} GiB` : 'RAM unknown';
  return `${cpu} / ${ram}`;
}

export interface LxcContainersProps {
  node?: NodeItem;
}

export const LxcContainers: React.FC<LxcContainersProps> = ({ node }) => {
  const [data, setData] = useState<LxcReadResult | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [pending, setPending] = useState<string | null>(null);

  const fetchData = async () => {
    if (!node?.id) return;
    setLoading(true);
    setError(null);
    try {
      const res = await fetch(
        `/api/nodes/${encodeURIComponent(node.id)}/plugins/vm/lxc`
      );
      if (!res.ok) {
        const txt = await res.text();
        throw new Error(`HTTP ${res.status}: ${txt || res.statusText}`);
      }
      setData((await res.json()) as LxcReadResult);
    } catch (err: any) {
      setError(err?.message || 'Failed to reach the plugin API');
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchData();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [node?.id]);

  const runAction = async (action: string, name: string) => {
    if (!node?.id) return;
    setPending(`${action}:${name}`);
    setNotice(null);
    try {
      const compileRes = await fetch(
        `/api/nodes/${encodeURIComponent(node.id)}/plugins/vm/lxc/actions/${encodeURIComponent(action)}`,
        {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ options: { name } }),
        }
      );
      if (!compileRes.ok) {
        const j = await compileRes.json().catch(() => ({}));
        throw new Error(j.error || `Failed to compile action (HTTP ${compileRes.status})`);
      }
      const taskSpec = await compileRes.json();
      const queueRes = await fetch('/api/queue/task', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          title: taskSpec.name || `${action} LXC container ${name}`,
          targetNodeId: node.id,
          targetNodeName: node.name,
          targetHostRoute: node.tailscaleHost || node.tailscaleIp || node.ip || 'localhost',
          targetSshUser: node.sshUser || 'core',
          targetSshPort: node.port || 22,
          category: 'vm',
          primaryModule: taskSpec.steps?.[0]?.module || 'community.general.virt',
          steps: taskSpec.steps || [],
          become: true,
          status: 'staged',
        }),
      });
      if (!queueRes.ok) {
        const qErr = await queueRes.json().catch(() => ({}));
        throw new Error(qErr.error || 'Failed to stage task into queue');
      }
      setNotice(`Staged "${action} ${name}" into the Ansible Queue.`);
    } catch (err: any) {
      setNotice(`Error: ${err?.message || 'action failed'}`);
    } finally {
      setPending(null);
    }
  };

  const header = (
    <div className="flex items-center justify-between pb-1.5 bg-[#0a0d1f]/40 px-2 py-1 rounded border border-[#26293c]/40">
      <div className="flex items-center gap-2">
        <span className="material-symbols-outlined text-[#b0c9ff] text-[18px]">deployed_code</span>
        <div className="flex items-center gap-2">
          <span className="font-sans text-sm text-[#dfe1fa] font-semibold">
            Linux Containers (LXC)
          </span>
          <span className="px-1.5 py-0.5 bg-[#ffb4ab]/15 text-[#ffb4ab] rounded text-[9px] font-bold border border-[#ffb4ab]/30">
            UNTESTED
          </span>
        </div>
      </div>
      <button
        type="button"
        onClick={fetchData}
        disabled={loading}
        className="p-1 bg-[#26293c] hover:bg-[#35384c] text-[#dfe1fa] rounded border border-[#434750]/40 transition-colors cursor-pointer disabled:opacity-50"
        title="Refresh LXC containers"
      >
        <span className={`material-symbols-outlined text-[13px] ${loading ? 'animate-spin' : ''}`}>sync</span>
      </button>
    </div>
  );

  const unimplemented = data?.unimplemented || [];

  const unimplementedPanel = unimplemented.length > 0 && (
    <div className="bg-[#171a2d] border border-[#26293c] rounded p-3 font-mono">
      <span className="text-[10px] uppercase font-bold text-[#8d909b] block mb-2">
        Unimplemented operations (not runnable)
      </span>
      <ul className="flex flex-col gap-1">
        {unimplemented.map(op => (
          <li key={op.name} className="flex items-start gap-2 text-[11px]">
            <span className="material-symbols-outlined text-[14px] text-[#8d909b]">block</span>
            <span className="text-[#c3c6d2]">
              <span className="font-bold">{op.name}</span> — {op.reason}
            </span>
          </li>
        ))}
      </ul>
    </div>
  );

  const verificationBanner = (
    <div className="bg-[#331114]/60 border border-[#ffb4ab]/30 rounded p-2 font-mono text-[11px] text-[#ffb4ab]">
      {data?.verifiedNote ||
        'UNTESTED: this plugin has not been validated against a real libvirt LXC host.'}
    </div>
  );

  if (loading && !data) {
    return (
      <section className="md:col-span-6">
        {header}
        <PluginStateView
          state="loading"
          title="Loading LXC containers"
          message="Querying virsh -c lxc:/// via the host daemon"
        />
      </section>
    );
  }

  if (error) {
    return (
      <section className="md:col-span-6 flex flex-col gap-2">
        {header}
        <PluginStateView
          state="error"
          title="LXC plugin probe error"
          error={error}
          onRetry={fetchData}
        />
      </section>
    );
  }

  const availability = lxcAvailability(data);

  return (
    <section className="md:col-span-6 flex flex-col gap-2">
      {header}
      {verificationBanner}

      {notice && (
        <div
          className={`p-2 rounded text-xs border font-mono ${
            notice.startsWith('Error')
              ? 'bg-[#331114] text-[#ffb4ab] border-[#ffb4ab]/30'
              : 'bg-[#0f231e] text-[#8cd6cb] border-[#8cd6cb]/30'
          }`}
        >
          {notice}
        </div>
      )}

      {availability === 'unavailable' && (
        <PluginStateView
          state="offline"
          title="No libvirt LXC host available"
          message={lxcUnavailableMessage(data)}
          onRetry={fetchData}
        />
      )}

      {availability === 'empty' && (
        <PluginStateView
          state="empty"
          title="No LXC containers defined"
          message="The libvirt LXC driver is reachable but returned no container domains."
          onRetry={fetchData}
        />
      )}

      {availability === 'ready' && (
        <div className="overflow-x-auto font-mono text-xs bg-[#171a2d] border border-[#26293c] rounded">
          <table className="w-full text-left">
            <thead className="bg-[#0a0d1f] text-[#8d909b] text-[10px]">
              <tr className="h-6">
                <th className="px-2">Name</th>
                <th className="px-2">Status</th>
                <th className="px-2">Specs</th>
                <th className="px-2 text-right">Actions</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-[#26293c]/50">
              {(data?.containers || []).map(c => (
                <tr key={c.name} className="h-9 hover:bg-[#26293c]/40 transition-colors">
                  <td className="px-2">
                    <div className="flex items-center gap-1.5">
                      <span className="material-symbols-outlined text-[#b0c9ff] text-[15px]">deployed_code</span>
                      <span className="text-[#dfe1fa] font-bold">{c.name}</span>
                    </div>
                  </td>
                  <td className="px-2">
                    <span
                      className={`px-1.5 py-0.5 rounded text-[10px] font-bold border ${
                        c.status === 'RUNNING'
                          ? 'bg-[#8cd6cb]/20 text-[#8cd6cb] border-[#8cd6cb]/30'
                          : 'bg-[#313447] text-[#8d909b] border-[#434750]/40'
                      }`}
                    >
                      {c.status}
                    </span>
                  </td>
                  <td className="px-2 text-[#c3c6d2]">{formatLxcSpec(c)}</td>
                  <td className="px-2 text-right space-x-1">
                    {LXC_ACTIONS.filter(
                      a => !(a === 'suspend' && c.status !== 'RUNNING')
                    ).map(a => (
                      <button
                        key={a}
                        type="button"
                        disabled={pending === `${a}:${c.name}`}
                        onClick={() => runAction(a, c.name)}
                        className="px-1.5 py-0.5 rounded bg-[#26293c] hover:bg-[#35384c] text-[#dfe1fa] text-[10px] border border-[#434750]/40 transition-colors cursor-pointer disabled:opacity-40"
                      >
                        {a}
                      </button>
                    ))}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {unimplementedPanel}
    </section>
  );
};

export default LxcContainers;
