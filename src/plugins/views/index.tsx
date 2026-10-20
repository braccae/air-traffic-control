import React, { useState, useEffect } from 'react';
import type { NodeItem, QueuedAnsibleTask } from '../../types/index.ts';
import {
  PluginResourceTable,
  PluginActionForm,
  PluginStateView,
  type ActionSpec,
} from '../kit/index.ts';

// Import existing custom views
import { ZfsManagementView } from '../../components/ZfsManagementView.tsx';
import { NetworkSharesView } from '../../components/NetworkSharesView.tsx';
import { SystemLogsView } from '../../components/SystemLogsView.tsx';
import { PodmanManager } from '../../components/PodmanManager.tsx';
import { DisksSmartView } from '../../components/DisksSmartView.tsx';
import { NetworkInterfacesView } from '../../components/NetworkInterfacesView.tsx';
import { FirewallView } from '../../components/FirewallView.tsx';
import { TailscaleExplorerView } from '../../components/TailscaleExplorerView.tsx';
import { TailscaleMeshCard } from '../../components/TailscaleMeshCard.tsx';
import { VirtualMachinesKVM } from '../../components/VirtualMachinesKVM.tsx';
import { ResticBackupPolicy } from '../../components/ResticBackupPolicy.tsx';
import { SystemHealthTelemetry } from '../../components/SystemHealthTelemetry.tsx';
import { SystemServicesView } from '../../components/SystemServicesView.tsx';
import { PackageUpdatesView } from '../../components/PackageUpdatesView.tsx';

export interface PluginViewProps {
  node: NodeItem;
  pluginId?: string;
  kind?: string;
  name?: string;
  onRefresh?: () => void;
  onOpenLogs?: (target: string, user?: string, uid?: number) => void;
  onOpenExec?: (target: string) => void;
  onOpenTerminal?: (ipOrId?: string) => void;
  telemetry?: any;
}

export type PluginViewComponent = React.FC<PluginViewProps>;

/**
 * Generic fallback view for plugins that don't have dedicated custom views.
 * Composes PluginResourceTable and PluginActionForm.
 */
export const GenericPluginView: React.FC<PluginViewProps> = ({
  node,
  pluginId,
  kind: propKind,
  name: propName,
}) => {
  const kind = propKind || (pluginId ? pluginId.split('/')[0] : '');
  const name = propName || (pluginId ? pluginId.split('/')[1] : '');
  const fullId = pluginId || (kind && name ? `${kind}/${name}` : '');

  const [loading, setLoading] = useState(true);
  const [data, setData] = useState<any>(null);
  const [error, setError] = useState<string | null>(null);
  const [actions, setActions] = useState<ActionSpec[]>([]);
  const [activeAction, setActiveAction] = useState<ActionSpec | null>(null);
  const [submittingAction, setSubmittingAction] = useState(false);
  const [statusNotice, setStatusNotice] = useState<string | null>(null);

  const fetchData = async () => {
    if (!node?.id || !kind || !name) return;
    setLoading(true);
    setError(null);
    try {
      // 1. Fetch plugin metadata / actions from /api/plugins
      const metaRes = await fetch('/api/plugins');
      if (metaRes.ok) {
        const summaries = await metaRes.json();
        const found = summaries.find((p: any) => p.id === fullId);
        if (found && Array.isArray(found.actions)) {
          setActions(found.actions);
        }
      }

      // 2. Fetch read data from /api/nodes/:id/plugins/:kind/:name
      const res = await fetch(`/api/nodes/${encodeURIComponent(node.id)}/plugins/${encodeURIComponent(kind)}/${encodeURIComponent(name)}`);
      if (!res.ok) {
        if (res.status === 400) {
          // Plugin doesn't implement Reader
          setData({});
        } else {
          const errText = await res.text();
          setError(`HTTP ${res.status}: ${errText || res.statusText}`);
        }
      } else {
        const json = await res.json();
        setData(json);
      }
    } catch (err: any) {
      setError(err?.message || 'Failed to communicate with plugin API');
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchData();
  }, [node?.id, fullId]);

  const handleActionSubmit = async (values: Record<string, any>) => {
    if (!activeAction) return;
    setSubmittingAction(true);
    setStatusNotice(null);
    try {
      const compileRes = await fetch(
        `/api/nodes/${encodeURIComponent(node.id)}/plugins/${encodeURIComponent(kind)}/${encodeURIComponent(name)}/actions/${encodeURIComponent(activeAction.name)}`,
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

      // Stage task into Ansible Queue
      const queueRes = await fetch('/api/queue/task', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          title: taskSpec.name || `${activeAction.label} on ${node.name}`,
          targetNodeId: node.id,
          targetNodeName: node.name,
          targetHostRoute: node.tailscaleHost || node.tailscaleIp || node.ip || 'localhost',
          targetSshUser: node.sshUser || 'core',
          targetSshPort: node.port || 22,
          category: (kind as any) || 'custom',
          primaryModule: taskSpec.steps?.[0]?.module || 'ansible.builtin.shell',
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
      setStatusNotice(`Action "${activeAction.label}" successfully staged into Ansible Queue (Task ID: ${qData.task?.id || 'staged'}).`);
      setActiveAction(null);
      fetchData();
    } catch (err: any) {
      setStatusNotice(`Error: ${err?.message || 'Failed to execute action'}`);
    } finally {
      setSubmittingAction(false);
    }
  };

  if (loading) {
    return (
      <PluginStateView
        state="loading"
        title={`Loading ${fullId || 'Plugin'}`}
        message="Fetching status and resources from host daemon"
      />
    );
  }

  if (error) {
    return (
      <PluginStateView
        state="error"
        title={`Plugin Probe Error: ${fullId || 'Plugin'}`}
        error={error}
        onRetry={fetchData}
      />
    );
  }

  // Derive resource items for table if data is an array or has a list property
  let tableItems: any[] = [];
  if (Array.isArray(data)) {
    tableItems = data;
  } else if (data && typeof data === 'object') {
    const arrayKey = Object.keys(data).find(k => Array.isArray(data[k]));
    if (arrayKey) {
      tableItems = data[arrayKey];
    }
  }

  // Derive columns from first item
  const columns =
    tableItems.length > 0 && typeof tableItems[0] === 'object'
      ? Object.keys(tableItems[0])
          .slice(0, 5)
          .map(k => ({
            key: k,
            header: k.toUpperCase(),
            render: (item: any) => {
              const val = item[k];
              if (typeof val === 'object') return JSON.stringify(val);
              return String(val ?? '');
            },
          }))
      : [];

  return (
    <div className="flex flex-col gap-3 font-mono">
      <div className="bg-[#171a2d] border border-[#26293c] rounded p-4 flex items-center justify-between">
        <div className="flex items-center gap-2">
          <span className="material-symbols-outlined text-[#b0c9ff] text-[22px]">extension</span>
          <div>
            <h3 className="text-sm font-bold text-[#dfe1fa]">{fullId}</h3>
            <span className="text-[11px] text-[#8d909b]">
              Target: {node.name} ({node.tailscaleHost || node.ip})
            </span>
          </div>
        </div>
        <div className="flex items-center gap-2">
          {actions.map(act => (
            <button
              key={act.name}
              type="button"
              onClick={() => setActiveAction(act)}
              className="flex items-center gap-1.5 px-2.5 py-1 rounded bg-[#26293c] hover:bg-[#35384c] text-[#dfe1fa] text-xs font-semibold transition-colors border border-[#434750]/40 cursor-pointer"
            >
              <span className="material-symbols-outlined text-[14px]">play_circle</span>
              <span>{act.label || act.name}</span>
            </button>
          ))}
          <button
            type="button"
            onClick={fetchData}
            className="p-1 rounded bg-[#26293c] hover:bg-[#35384c] text-[#8d909b] hover:text-[#dfe1fa] cursor-pointer"
            title="Refresh"
          >
            <span className="material-symbols-outlined text-[16px]">refresh</span>
          </button>
        </div>
      </div>

      {statusNotice && (
        <div
          className={`p-3 rounded text-xs border ${
            statusNotice.startsWith('Error')
              ? 'bg-[#331114] text-[#ffb4ab] border-[#ffb4ab]/30'
              : 'bg-[#0f231e] text-[#8cd6cb] border-[#8cd6cb]/30'
          }`}
        >
          {statusNotice}
        </div>
      )}

      {activeAction && (
        <PluginActionForm
          action={activeAction}
          onSubmit={handleActionSubmit}
          onCancel={() => setActiveAction(null)}
          submitting={submittingAction}
        />
      )}

      {tableItems.length > 0 ? (
        <PluginResourceTable
          columns={columns}
          data={tableItems}
          keyExtractor={(item, idx) => item.id || item.name || String(idx)}
        />
      ) : data && Object.keys(data).length > 0 ? (
        <div className="bg-[#171a2d] border border-[#26293c] rounded p-4">
          <span className="text-[10px] uppercase font-bold text-[#8d909b] block mb-2">Host Plugin Telemetry / Data</span>
          <pre className="text-xs text-[#8cd6cb] overflow-x-auto whitespace-pre-wrap max-h-96">
            {JSON.stringify(data, null, 2)}
          </pre>
        </div>
      ) : (
        <PluginStateView
          state="empty"
          title="No Resources Detected"
          message={`No active resources currently returned by ${fullId}.`}
          onRetry={fetchData}
        />
      )}
    </div>
  );
};

// Adapters for custom components to conform to PluginViewProps interface
const ZfsViewAdapter: React.FC<PluginViewProps> = ({ node }) => (
  <ZfsManagementView node={node} />
);

const NetworkSharesViewAdapter: React.FC<PluginViewProps> = ({ node, pluginId }) => {
  const scope: 'smb' | 'nfs' | 'all' =
    pluginId === 'share/smb' ? 'smb' : pluginId === 'share/nfs' ? 'nfs' : 'all';
  return <NetworkSharesView node={node} scope={scope} />;
};

const SystemLogsViewAdapter: React.FC<PluginViewProps> = ({ node }) => (
  <SystemLogsView node={node} />
);

const PodmanViewAdapter: React.FC<PluginViewProps> = ({
  node,
  onOpenLogs = () => {},
  onOpenExec = () => {},
}) => (
  <PodmanManager
    selectedNode={node}
    onOpenLogs={onOpenLogs}
    onOpenExec={onOpenExec}
    onRestartPod={() => {}}
  />
);

const DisksSmartViewAdapter: React.FC<PluginViewProps> = ({ node }) => (
  <DisksSmartView node={node} />
);

const NetworkInterfacesViewAdapter: React.FC<PluginViewProps> = ({ node, onRefresh }) => (
  <NetworkInterfacesView node={node} onRefresh={onRefresh} />
);

const FirewallViewAdapter: React.FC<PluginViewProps> = ({ node }) => (
  <FirewallView node={node} />
);

const TailscaleExplorerViewAdapter: React.FC<PluginViewProps> = ({ onOpenTerminal }) => (
  <TailscaleExplorerView onOpenTerminalForHost={onOpenTerminal} />
);

const TailscaleMeshCardAdapter: React.FC<PluginViewProps> = ({ node }) => (
  <TailscaleMeshCard node={node} />
);

const VirtualMachinesKVMAdapter: React.FC<PluginViewProps> = ({ node, onOpenExec }) => (
  <VirtualMachinesKVM
    node={node}
    onOpenConsole={() => onOpenExec && onOpenExec('console')}
    onNewVM={() => {}}
  />
);

const ResticBackupPolicyAdapter: React.FC<PluginViewProps> = ({ node }) => (
  <ResticBackupPolicy
    nodeName={node.name}
    hasRestic={node.hasRestic}
    onIntegrityCheck={() => {}}
    onBrowseFiles={() => {}}
    onSnapshot={() => {}}
  />
);

const SystemHealthTelemetryAdapter: React.FC<PluginViewProps> = ({ telemetry }) => {
  if (!telemetry) return null;
  return <SystemHealthTelemetry telemetry={telemetry} />;
};

const SystemServicesViewAdapter: React.FC<PluginViewProps> = ({ node, onRefresh }) => (
  <SystemServicesView node={node} onRefresh={onRefresh} />
);

const PackageUpdatesViewAdapter: React.FC<PluginViewProps> = ({ node }) => (
  <PackageUpdatesView node={node} />
);

/**
 * Registry mapping plugin ID (`<kind>/<name>`) or kind to React view components.
 */
export const PLUGIN_VIEW_REGISTRY: Record<string, PluginViewComponent> = {
  // System Services & Power
  'system/service': SystemServicesViewAdapter,
  'system': SystemServicesViewAdapter,
  'services': SystemServicesViewAdapter,

  // Package Managers & Updates
  'package/dnf': PackageUpdatesViewAdapter,
  'package/apt': PackageUpdatesViewAdapter,
  'package/apk': PackageUpdatesViewAdapter,
  'package/bootc': PackageUpdatesViewAdapter,
  'package/rpmostree': PackageUpdatesViewAdapter,
  'package': PackageUpdatesViewAdapter,
  'updates': PackageUpdatesViewAdapter,

  // Storage & Filesystems
  'storage/zfs': ZfsViewAdapter,
  'storage/btrfs': ZfsViewAdapter,
  'storage/lvm': ZfsViewAdapter,
  'storage': ZfsViewAdapter,

  // Network Shares
  'share/nfs': NetworkSharesViewAdapter,
  'share/smb': NetworkSharesViewAdapter,
  'share': NetworkSharesViewAdapter,

  // Logs
  'logs/journald': SystemLogsViewAdapter,
  'logs': SystemLogsViewAdapter,

  // Containers
  'container/podman': PodmanViewAdapter,
  'container': PodmanViewAdapter,

  // Disks & SMART
  'disk/smart': DisksSmartViewAdapter,
  'disk': DisksSmartViewAdapter,

  // Network & Interfaces
  'network/interfaces': NetworkInterfacesViewAdapter,
  'network/tailscale': TailscaleExplorerViewAdapter,
  'network': NetworkInterfacesViewAdapter,

  // Firewall
  'firewall/firewalld': FirewallViewAdapter,
  'firewall': FirewallViewAdapter,

  // VMs
  'vm/libvirt': VirtualMachinesKVMAdapter,
  'vm': VirtualMachinesKVMAdapter,

  // Backups
  'backup/restic': ResticBackupPolicyAdapter,
  'backup': ResticBackupPolicyAdapter,

  // Telemetry
  'telemetry/glances': SystemHealthTelemetryAdapter,
  'telemetry': SystemHealthTelemetryAdapter,
};

/**
 * Resolves a view component for a given plugin ID or kind.
 * Falls back to GenericPluginView if no custom view is registered.
 */
export function getPluginView(pluginIdOrKind: string): PluginViewComponent {
  if (PLUGIN_VIEW_REGISTRY[pluginIdOrKind]) {
    return PLUGIN_VIEW_REGISTRY[pluginIdOrKind];
  }
  const kind = pluginIdOrKind.split('/')[0];
  if (PLUGIN_VIEW_REGISTRY[kind]) {
    return PLUGIN_VIEW_REGISTRY[kind];
  }
  return GenericPluginView;
}

export {
  ZfsViewAdapter as ZfsManagementView,
  NetworkSharesViewAdapter as NetworkSharesView,
  SystemLogsViewAdapter as SystemLogsView,
  PodmanViewAdapter as PodmanManager,
  DisksSmartViewAdapter as DisksSmartView,
  NetworkInterfacesViewAdapter as NetworkInterfacesView,
  FirewallViewAdapter as FirewallView,
  TailscaleExplorerViewAdapter as TailscaleExplorerView,
  TailscaleMeshCardAdapter as TailscaleMeshCard,
  VirtualMachinesKVMAdapter as VirtualMachinesKVM,
  ResticBackupPolicyAdapter as ResticBackupPolicy,
  SystemHealthTelemetryAdapter as SystemHealthTelemetry,
  SystemServicesViewAdapter as SystemServicesView,
  PackageUpdatesViewAdapter as PackageUpdatesView,
};
