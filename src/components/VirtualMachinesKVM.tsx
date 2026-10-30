import React, { useState, useEffect } from 'react';
import type { VMItem, NodeItem } from '../types/index.ts';

interface VirtualMachinesKVMProps {
  node?: NodeItem | null;
  onOpenConsole?: (vmid: number) => void;
  onNewVM?: () => void;
}

export const VirtualMachinesKVM: React.FC<VirtualMachinesKVMProps> = ({
  node,
  onOpenConsole,
  onNewVM,
}) => {
  const [vms, setVms] = useState<VMItem[]>([]);
  const [hypervisor, setHypervisor] = useState<string>('KVM / QEMU');
  const [serviceStatus, setServiceStatus] = useState<string>('active');
  const [loading, setLoading] = useState(false);

  const fetchVms = (refresh = false) => {
    if (!node?.id) return;
    setLoading(true);
    fetch(`/api/nodes/${encodeURIComponent(node.id)}/vms${refresh ? '?refresh=true' : ''}`)
      .then(res => res.json())
      .then(data => {
        if (data.success) {
          if (Array.isArray(data.vms)) setVms(data.vms);
          if (data.hypervisor) setHypervisor(data.hypervisor);
          if (data.serviceStatus) setServiceStatus(data.serviceStatus);
        }
      })
      .catch(() => {})
      .finally(() => setLoading(false));
  };

  useEffect(() => {
    fetchVms(false);
  }, [node?.id, node?.shortName]);

  const toggleVM = async (vmItem: VMItem) => {
    if (!node?.id) return;
    const action = vmItem.status === 'RUNNING' ? 'shutdown' : 'start';
    try {
      const res = await fetch(`/api/nodes/${encodeURIComponent(node.id)}/vms/${encodeURIComponent(vmItem.name)}/action`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ action }),
      });
      if (res.ok) {
        fetchVms(true);
      }
    } catch {
      // error handling
    }
  };

  const totalVcpus = vms.reduce((acc, vm) => acc + (vm.vcpus || 0), 0);
  const totalRam = vms.reduce((acc, vm) => acc + (vm.ramGiB || 0), 0);

  return (
    <section className="md:col-span-6 bg-[#171a2d] rounded p-3 flex flex-col justify-between border border-[#26293c]">
      <div>
        <div className="flex items-center justify-between pb-1.5 bg-[#0a0d1f]/40 px-2 py-1 rounded border border-[#26293c]/40">
          <div className="flex items-center gap-2">
            <span className="material-symbols-outlined text-[#b0c9ff] text-[18px]">qr_code_2</span>
            <div>
              <span className="font-sans text-sm text-[#dfe1fa] font-semibold">
                Virtual Machines (KVM/QEMU)
              </span>
              {node && (
                <span className="font-mono text-[10px] text-[#8d909b] ml-2">
                  on {node.shortName}
                </span>
              )}
            </div>
          </div>
          <div className="flex items-center gap-1.5">
            <button
              type="button"
              onClick={() => fetchVms(true)}
              disabled={loading}
              className="p-1 bg-[#26293c] hover:bg-[#35384c] text-[#dfe1fa] rounded border border-[#434750]/40 transition-colors cursor-pointer disabled:opacity-50"
              title="Refresh VMs"
            >
              <span className={`material-symbols-outlined text-[13px] ${loading ? 'animate-spin' : ''}`}>sync</span>
            </button>
            <button
              type="button"
              onClick={onNewVM}
              className="flex items-center gap-1 bg-[#26293c] hover:bg-[#35384c] text-[#b0c9ff] px-2 py-0.5 rounded font-mono text-[11px] border border-[#434750]/40 transition-colors cursor-pointer"
            >
              <span className="material-symbols-outlined text-[13px]">add</span>
              <span>New VM</span>
            </button>
          </div>
        </div>

        {vms.length > 0 ? (
          <div className="overflow-x-auto mt-2 font-mono text-xs">
            <table className="w-full text-left">
              <thead className="bg-[#0a0d1f] text-[#8d909b] text-[10px]">
                <tr className="h-6">
                  <th className="px-2">VMID / Name</th>
                  <th className="px-2">Status</th>
                  <th className="px-2">Specs</th>
                  <th className="px-2 text-right">Quick Power</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-[#26293c]/50">
                {vms.map(vm => (
                  <tr key={vm.vmid} className="h-9 hover:bg-[#26293c]/40 transition-colors">
                    <td className="px-2">
                      <div className="flex items-center gap-1.5">
                        <span className="material-symbols-outlined text-[#b0c9ff] text-[15px]">
                          desktop_windows
                        </span>
                        <div className="flex flex-col">
                          <span className="text-[#dfe1fa] font-bold">{vm.name}</span>
                          <span className="text-[#8d909b] text-[10px]">{vm.subtext}</span>
                        </div>
                      </div>
                    </td>
                    <td className="px-2">
                      {vm.status === 'RUNNING' ? (
                        <span className="px-1.5 py-0.2 bg-[#8cd6cb]/20 text-[#8cd6cb] rounded text-[10px] font-bold border border-[#8cd6cb]/30">
                          RUNNING
                        </span>
                      ) : (
                        <span className="px-1.5 py-0.2 bg-[#313447] text-[#8d909b] rounded text-[10px] border border-[#434750]/40">
                          STOPPED
                        </span>
                      )}
                    </td>
                    <td className="px-2 text-[#c3c6d2]">
                      {vm.vcpus} vCPUs / {vm.ramGiB} GiB
                    </td>
                    <td className="px-2 text-right space-x-1">
                      {vm.status === 'RUNNING' ? (
                        <>
                          <button
                            type="button"
                            onClick={() => onOpenConsole?.(vm.vmid)}
                            className="p-1 bg-[#26293c] hover:bg-[#35384c] text-[#b0c9ff] rounded cursor-pointer"
                            title="VNC Console"
                          >
                            <span className="material-symbols-outlined text-[14px]">tv</span>
                          </button>
                          <button
                            type="button"
                            onClick={() => toggleVM(vm)}
                            className="p-1 bg-[#26293c] hover:bg-[#35384c] text-[#ffb4ab] rounded cursor-pointer"
                            title="Shutdown VM"
                          >
                            <span className="material-symbols-outlined text-[14px]">stop</span>
                          </button>
                        </>
                      ) : (
                        <>
                          <button
                            type="button"
                            onClick={() => toggleVM(vm)}
                            className="p-1 bg-[#b0c9ff] text-[#002e68] font-bold rounded cursor-pointer"
                            title="Start VM"
                          >
                            <span className="material-symbols-outlined text-[14px]">play_arrow</span>
                          </button>
                          <button
                            type="button"
                            className="p-1 bg-[#26293c] hover:bg-[#35384c] text-[#8d909b] rounded cursor-pointer"
                            title="VM Settings"
                          >
                            <span className="material-symbols-outlined text-[14px]">settings</span>
                          </button>
                        </>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        ) : (
          <div className="bg-[#0a0d1f] p-4 rounded text-center font-mono text-xs text-[#8d909b] border border-[#26293c] mt-2">
            {loading ? (
              'Probing KVM/QEMU hypervisor...'
            ) : (
              <div className="flex flex-col items-center gap-1">
                <span className="text-[#dfe1fa] font-bold">0 Active Guest VMs</span>
                <span className="text-[11px] text-[#8d909b]">
                  {node?.shortName || 'Host'} hypervisor ready ({hypervisor}, virtqemud: {serviceStatus}). No guests currently instantiated.
                </span>
              </div>
            )}
          </div>
        )}
      </div>

      <div className="mt-2 pt-1.5 bg-[#313447]/20 px-2 py-1 rounded flex items-center justify-between font-mono text-[11px] text-[#8d909b] border border-[#26293c]/40">
        <span>
          Hypervisor: <strong className="text-[#dfe1fa]">{hypervisor}</strong>
        </span>
        <span>Total VM Allocation: {totalVcpus} vCPUs, {totalRam} GiB</span>
      </div>
    </section>
  );
};
