import React, { useState, useEffect, useMemo } from 'react';
import type { NodeItem } from '../types/index.ts';
import { PluginActionForm, type ActionSpec } from '../plugins/kit/index.ts';

interface ProxmoxViewProps {
  node: NodeItem;
  pluginId?: string;
}

interface ProxmoxConsole {
  type?: string;
  node?: string;
  noVNCUrl?: string;
  termUrl?: string;
  spiceUrl?: string;
  spiceVV?: string;
  vncUrl?: string;
  vncTicket?: string;
}

export interface ProxmoxGuest {
  vmid: number;
  name: string;
  node: string;
  type: 'qemu' | 'lxc';
  status: string;
  maxMem: number;
  maxDisk: number;
  maxCpu: number;
  uptime: number;
  console?: ProxmoxConsole;
}

interface ProxmoxReadData {
  endpoint?: string;
  version?: string;
  release?: string;
  /** PVE node this read was scoped to (guests of other members are excluded). */
  node?: string;
  nodes?: Array<{ node: string; status: string; maxcpu: number; maxmem: number; mem: number }>;
  vms?: ProxmoxGuest[];
  containers?: ProxmoxGuest[];
  summary?: { node?: string; nodes: number; vms: number; containers: number; running: number; excluded?: number; filteredTo?: string };
  errors?: string[];
}

const fmtBytes = (b: number): string => {
  if (!b) return '—';
  const gib = b / 1024 / 1024 / 1024;
  return `${gib.toFixed(gib >= 10 ? 0 : 1)} GiB`;
};

const isRunning = (s: string) => s?.toLowerCase() === 'running';

/** Stable identity for a guest row (`qemu-101`, `lxc-201`). */
export const proxmoxGuestKey = (g: Pick<ProxmoxGuest, 'type' | 'vmid'>): string => `${g.type}-${g.vmid}`;

/** Guest label, e.g. `VM 101 · web01`. */
export const proxmoxGuestLabel = (g: ProxmoxGuest): string =>
  `${g.type === 'lxc' ? 'CT' : 'VM'} ${g.vmid}${g.name ? ` · ${g.name}` : ''}`;

/**
 * Seeds an action's node/vmid/guestType options with the selected guest so the
 * operator does not have to retype them. Values stay editable; nothing is
 * submitted without an explicit click. Creation actions only inherit the node:
 * reusing the selected guest's VMID would collide with the existing guest.
 */
export const actionSpecForGuest = (action: ActionSpec, guest: ProxmoxGuest): ActionSpec => {
  const isCreate = /(^|_)create$/.test(action.name || '');
  const options = (action.options || []).map(opt => {
    if (opt.name === 'node' && guest.node) return { ...opt, default: guest.node };
    if (isCreate) return opt;
    if (opt.name === 'vmid' && guest.vmid > 0) return { ...opt, default: guest.vmid };
    if (opt.name === 'guestType') return { ...opt, default: guest.type };
    return opt;
  });
  return { ...action, options };
};

export interface ProxmoxActionToolbarProps {
  actions: ActionSpec[];
  selected: ProxmoxGuest | null;
  onAction: (action: ActionSpec, guest: ProxmoxGuest) => void;
  onClearSelection: () => void;
}

/**
 * Guest action toolbar. It only exists once a guest is selected: with nothing
 * selected it renders a hint instead of live Start/Shutdown/Resize buttons.
 */
export const ProxmoxActionToolbar: React.FC<ProxmoxActionToolbarProps> = ({
  actions,
  selected,
  onAction,
  onClearSelection,
}) => {
  if (!selected) {
    return (
      <div
        data-testid="proxmox-selection-hint"
        className="bg-[#171a2d] border border-dashed border-[#26293c] rounded px-3 py-2 text-[11px] text-[#8d909b] flex items-center gap-1.5"
      >
        <span className="material-symbols-outlined text-[14px]">ads_click</span>
        <span>Select a guest to manage it</span>
      </div>
    );
  }

  return (
    <div
      role="toolbar"
      aria-label={`Actions for ${proxmoxGuestLabel(selected)}`}
      data-testid="proxmox-action-toolbar"
      data-selected-vmid={selected.vmid}
      className="bg-[#171a2d] border border-[#26293c] rounded px-3 py-2 flex items-center justify-between gap-3 flex-wrap"
    >
      <div className="flex items-center gap-1.5 text-[11px] text-[#8d909b]">
        <span className="material-symbols-outlined text-[14px] text-[#8cd6cb]">check_circle</span>
        <span>
          Selected <span className="text-[#dfe1fa] font-semibold">{proxmoxGuestLabel(selected)}</span>
          {selected.node ? <span className="text-[#8d909b]"> on {selected.node}</span> : null}
        </span>
      </div>
      <div className="flex items-center gap-2 flex-wrap">
        {actions.map(act => (
          <button
            key={act.name}
            type="button"
            onClick={() => onAction(actionSpecForGuest(act, selected), selected)}
            title={act.description || act.label || act.name}
            className="flex items-center gap-1.5 px-2.5 py-1 rounded bg-[#26293c] hover:bg-[#35384c] text-[#dfe1fa] text-xs font-semibold transition-colors border border-[#434750]/40 cursor-pointer"
          >
            <span className="material-symbols-outlined text-[14px]">play_circle</span>
            <span>{act.label || act.name}</span>
          </button>
        ))}
        <button
          type="button"
          onClick={onClearSelection}
          title="Clear selection"
          className="flex items-center gap-1 px-2 py-1 rounded bg-[#26293c] hover:bg-[#35384c] text-[#8d909b] hover:text-[#dfe1fa] text-xs cursor-pointer"
        >
          <span className="material-symbols-outlined text-[14px]">close</span>
          <span>Clear</span>
        </button>
      </div>
    </div>
  );
};

export interface ProxmoxGuestsPanelProps {
  data: ProxmoxReadData;
  actions: ActionSpec[];
  /** Optional pre-selected guest (`qemu-101`, `lxc-201`). */
  initialSelectedKey?: string | null;
  onAction?: (action: ActionSpec, guest: ProxmoxGuest) => void;
  onOpenConsole?: (guest: ProxmoxGuest) => void;
}

/**
 * Guest tables plus the selection-gated action toolbar. Selecting a row is the
 * only way to reach lifecycle actions; the resolve below keeps the selection
 * honest when a guest disappears between refreshes.
 */
export const ProxmoxGuestsPanel: React.FC<ProxmoxGuestsPanelProps> = ({
  data,
  actions,
  initialSelectedKey = null,
  onAction,
  onOpenConsole,
}) => {
  const [selectedKey, setSelectedKey] = useState<string | null>(initialSelectedKey);

  const guests = useMemo(
    () => [...(data.vms || []), ...(data.containers || [])],
    [data.vms, data.containers],
  );
  const selected = useMemo(
    () => guests.find(g => proxmoxGuestKey(g) === selectedKey) || null,
    [guests, selectedKey],
  );

  const toggleGuest = (guest: ProxmoxGuest) => {
    const key = proxmoxGuestKey(guest);
    setSelectedKey(prev => (prev === key ? null : key));
  };

  return (
    <div className="flex flex-col gap-3">
      {guests.length > 0 && (
        <ProxmoxActionToolbar
          actions={actions}
          selected={selected}
          onAction={(action, guest) => onAction?.(action, guest)}
          onClearSelection={() => setSelectedKey(null)}
        />
      )}

      {[
        { title: 'Virtual Machines (QEMU/KVM)', guests: data.vms || [] },
        { title: 'LXC Containers', guests: data.containers || [] },
      ].map(section => (
        <div key={section.title} className="bg-[#171a2d] border border-[#26293c] rounded">
          <div className="px-3 py-2 text-[11px] uppercase font-bold text-[#8d909b] border-b border-[#26293c]">{section.title}</div>
          {section.guests.length === 0 ? (
            <div className="px-3 py-3 text-xs text-[#8d909b]">No guests on this node.</div>
          ) : (
            <table className="w-full text-xs">
              <thead>
                <tr className="text-[#8d909b] text-left">
                  <th className="px-3 py-1.5">VMID</th>
                  <th className="px-3 py-1.5">NAME</th>
                  <th className="px-3 py-1.5">NODE</th>
                  <th className="px-3 py-1.5">STATUS</th>
                  <th className="px-3 py-1.5">CPU</th>
                  <th className="px-3 py-1.5">MEM</th>
                  <th className="px-3 py-1.5">DISK</th>
                  <th className="px-3 py-1.5">CONSOLE</th>
                </tr>
              </thead>
              <tbody>
                {section.guests.map(g => {
                  const key = proxmoxGuestKey(g);
                  const isSelected = key === selectedKey;
                  return (
                    <tr
                      key={key}
                      role="row"
                      aria-selected={isSelected}
                      data-testid={`proxmox-guest-${key}`}
                      data-selected={isSelected ? 'true' : 'false'}
                      tabIndex={0}
                      onClick={() => toggleGuest(g)}
                      onKeyDown={e => {
                        if (e.key === 'Enter' || e.key === ' ') {
                          e.preventDefault();
                          toggleGuest(g);
                        }
                      }}
                      title={`Select ${proxmoxGuestLabel(g)} to manage it`}
                      className={`border-t border-[#26293c]/60 cursor-pointer outline-none ${
                        isSelected
                          ? 'bg-[#26293c] text-[#dfe1fa] border-l-2 border-l-[#b0c9ff]'
                          : 'text-[#dfe1fa] hover:bg-[#1b1e31] border-l-2 border-l-transparent'
                      }`}
                    >
                      <td className="px-3 py-1.5">{g.vmid}</td>
                      <td className="px-3 py-1.5">{g.name}</td>
                      <td className="px-3 py-1.5">{g.node}</td>
                      <td className="px-3 py-1.5">
                        <span className={isRunning(g.status) ? 'text-[#8cd6cb]' : 'text-[#8d909b]'}>{g.status}</span>
                      </td>
                      <td className="px-3 py-1.5">{g.maxCpu}</td>
                      <td className="px-3 py-1.5">{fmtBytes(g.maxMem)}</td>
                      <td className="px-3 py-1.5">{fmtBytes(g.maxDisk)}</td>
                      <td className="px-3 py-1.5">
                        {isRunning(g.status) ? (
                          <button
                            type="button"
                            onClick={e => {
                              e.stopPropagation();
                              onOpenConsole?.(g);
                            }}
                            className="px-2 py-0.5 rounded bg-[#26293c] hover:bg-[#35384c] text-[#b0c9ff]"
                          >
                            Open
                          </button>
                        ) : (
                          <span className="text-[#8d909b]">—</span>
                        )}
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          )}
        </div>
      ))}
    </div>
  );
};

export const ProxmoxView: React.FC<ProxmoxViewProps> = ({ node }) => {
  const [data, setData] = useState<ProxmoxReadData | null>(null);
  const [actions, setActions] = useState<ActionSpec[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [activeAction, setActiveAction] = useState<ActionSpec | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [consoleGuest, setConsoleGuest] = useState<ProxmoxGuest | null>(null);

  const fetchData = async () => {
    if (!node?.id) return;
    setLoading(true);
    setError(null);
    try {
      const metaRes = await fetch('/api/plugins');
      if (metaRes.ok) {
        const summaries = await metaRes.json();
        const found = summaries.find((p: any) => p.id === 'vm/proxmox');
        if (found?.actions) setActions(found.actions);
      }
      const res = await fetch(`/api/nodes/${encodeURIComponent(node.id)}/plugins/vm/proxmox`);
      if (!res.ok) {
        const text = await res.text();
        setError(`HTTP ${res.status}: ${text || res.statusText}`);
      } else {
        setData(await res.json());
      }
    } catch (err: any) {
      setError(err?.message || 'Failed to reach Proxmox API');
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchData();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [node?.id]);

  const handleActionSubmit = async (values: Record<string, any>) => {
    if (!activeAction) return;
    setSubmitting(true);
    setNotice(null);
    try {
      const compileRes = await fetch(
        `/api/nodes/${encodeURIComponent(node.id)}/plugins/vm/proxmox/actions/${encodeURIComponent(activeAction.name)}`,
        {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ options: values }),
        }
      );
      if (!compileRes.ok) {
        const errJson = await compileRes.json().catch(() => ({}));
        throw new Error(errJson.error || `Failed to compile action (HTTP ${compileRes.status})`);
      }
      const taskSpec = await compileRes.json();
      const queueRes = await fetch('/api/queue/task', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          title: taskSpec.name || `${activeAction.label} on ${node.name}`,
          targetNodeId: node.id,
          targetNodeName: node.name,
          targetHostRoute: node.tailscaleHost || node.tailscaleIp || node.ip || 'localhost',
          targetSshUser: node.sshUser || 'root',
          targetSshPort: node.port || 22,
          category: 'vm',
          primaryModule: taskSpec.steps?.[0]?.module || 'ansible.builtin.uri',
          steps: taskSpec.steps || [],
          become: true,
          status: 'staged',
        }),
      });
      if (!queueRes.ok) {
        const qErr = await queueRes.json().catch(() => ({}));
        throw new Error(qErr.error || 'Failed to stage task into queue');
      }
      const qData = await queueRes.json();
      setNotice(`"${activeAction.label}" staged into the Ansible Queue (Task ID: ${qData.task?.id || 'staged'}).`);
      setActiveAction(null);
      fetchData();
    } catch (err: any) {
      setNotice(`Error: ${err?.message || 'Failed to execute action'}`);
    } finally {
      setSubmitting(false);
    }
  };

  const downloadVV = (guest: ProxmoxGuest) => {
    const vv = guest.console?.spiceVV;
    if (!vv) return;
    const blob = new Blob([vv], { type: 'application/x-virt-viewer' });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = `proxmox-${guest.vmid}.vv`;
    a.click();
    URL.revokeObjectURL(url);
  };

  if (loading) {
    return <div className="text-xs text-[#8d909b] font-mono p-4">Querying Proxmox API on {node.name}…</div>;
  }
  if (error) {
    return (
      <div className="p-3 rounded text-xs border bg-[#331114] text-[#ffb4ab] border-[#ffb4ab]/30 font-mono">
        <div className="font-bold mb-1">Proxmox API unreachable</div>
        <div className="break-all">{error}</div>
        <button onClick={fetchData} className="mt-2 px-2 py-1 rounded bg-[#26293c] text-[#dfe1fa]">Retry</button>
      </div>
    );
  }

  const summary = data?.summary;

  return (
    <div className="flex flex-col gap-3 font-mono">
      <div className="bg-[#171a2d] border border-[#26293c] rounded p-4 flex items-center justify-between gap-3 flex-wrap">
        <div className="flex items-center gap-2">
          <span className="material-symbols-outlined text-[#b0c9ff] text-[22px]">cloud</span>
          <div>
            <h3 className="text-sm font-bold text-[#dfe1fa]">Proxmox VE {data?.version} <span className="text-[#8d909b] text-[11px]">({data?.release})</span></h3>
            <span className="text-[11px] text-[#8d909b]">
              {data?.endpoint}
              {data?.node ? <> · node <span className="text-[#dfe1fa]">{data.node}</span></> : null}
              {' '}· {summary?.vms ?? 0} VM(s) · {summary?.containers ?? 0} CT(s)
              {' '}· {summary?.nodes ?? 0} cluster node(s)
              {summary?.excluded ? <> · {summary.excluded} guest(s) on other cluster nodes hidden</> : null}
            </span>
          </div>
        </div>
        <button type="button" onClick={fetchData} className="p-1 rounded bg-[#26293c] hover:bg-[#35384c] text-[#8d909b] hover:text-[#dfe1fa] cursor-pointer" title="Refresh">
          <span className="material-symbols-outlined text-[16px]">refresh</span>
        </button>
      </div>

      {notice && (
        <div className={`p-3 rounded text-xs border ${notice.startsWith('Error') ? 'bg-[#331114] text-[#ffb4ab] border-[#ffb4ab]/30' : 'bg-[#0f231e] text-[#8cd6cb] border-[#8cd6cb]/30'}`}>
          {notice}
        </div>
      )}

      {activeAction && (
        <PluginActionForm action={activeAction} onSubmit={handleActionSubmit} onCancel={() => setActiveAction(null)} submitting={submitting} />
      )}

      {data?.errors && data.errors.length > 0 && (
        <div className="p-2 rounded text-[11px] border bg-[#2b2310] text-[#ffd479] border-[#ffd479]/30">
          {data.errors.map((e, i) => <div key={i}>console probe: {e}</div>)}
        </div>
      )}

      {data && (
        <ProxmoxGuestsPanel
          data={data}
          actions={actions}
          onAction={action => {
            setNotice(null);
            setActiveAction(action);
          }}
          onOpenConsole={guest => setConsoleGuest(guest)}
        />
      )}

      {consoleGuest && (
        <div className="fixed inset-0 bg-black/60 flex items-center justify-center z-50" onClick={() => setConsoleGuest(null)}>
          <div className="bg-[#171a2d] border border-[#26293c] rounded p-4 w-[520px] max-w-full" onClick={e => e.stopPropagation()}>
            <div className="flex items-center justify-between mb-3">
              <h3 className="text-sm font-bold text-[#dfe1fa]">Console — {consoleGuest.name} ({consoleGuest.vmid})</h3>
              <button type="button" onClick={() => setConsoleGuest(null)} className="text-[#8d909b] hover:text-[#dfe1fa]">
                <span className="material-symbols-outlined text-[18px]">close</span>
              </button>
            </div>
            <div className="flex flex-col gap-2 text-xs text-[#dfe1fa]">
              {consoleGuest.console?.noVNCUrl && (
                <a href={consoleGuest.console.noVNCUrl} target="_blank" rel="noopener noreferrer" className="px-3 py-1.5 rounded bg-[#26293c] hover:bg-[#35384c] text-[#b0c9ff]">
                  Open noVNC console (PVE web) ↗
                </a>
              )}
              {consoleGuest.console?.termUrl && (
                <a href={consoleGuest.console.termUrl} target="_blank" rel="noopener noreferrer" className="px-3 py-1.5 rounded bg-[#26293c] hover:bg-[#35384c] text-[#b0c9ff]">
                  Open container term (xterm.js) ↗
                </a>
              )}
              {consoleGuest.console?.spiceUrl && (
                <a href={consoleGuest.console.spiceUrl} className="px-3 py-1.5 rounded bg-[#26293c] hover:bg-[#35384c] text-[#8cd6cb]">
                  Launch SPICE ({consoleGuest.console.spiceUrl})
                </a>
              )}
              {consoleGuest.console?.spiceVV && (
                <button type="button" onClick={() => downloadVV(consoleGuest)} className="text-left px-3 py-1.5 rounded bg-[#26293c] hover:bg-[#35384c] text-[#8cd6cb]">
                  Download SPICE .vv file (virt-viewer)
                </button>
              )}
              {consoleGuest.console?.vncUrl && (
                <div className="px-3 py-1.5 rounded bg-[#1b1e31] border border-[#26293c]">
                  <div>VNC: {consoleGuest.console.vncUrl}</div>
                  {consoleGuest.console.vncTicket && <div className="text-[#8d909b]">password/ticket: {consoleGuest.console.vncTicket}</div>}
                </div>
              )}
              {!consoleGuest.console?.noVNCUrl && !consoleGuest.console?.spiceUrl && !consoleGuest.console?.vncUrl && (
                <div className="text-[#ffb4ab]">No console endpoint returned by the Proxmox API for this guest.</div>
              )}
            </div>
          </div>
        </div>
      )}
    </div>
  );
};
