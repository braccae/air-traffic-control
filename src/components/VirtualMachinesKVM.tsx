import React, { useState } from 'react';
import type { VMItem } from '../types/index.ts';

interface VirtualMachinesKVMProps {
  onOpenConsole?: (vmid: number) => void;
  onNewVM?: () => void;
}

export const VirtualMachinesKVM: React.FC<VirtualMachinesKVMProps> = ({
  onOpenConsole,
  onNewVM,
}) => {
  const [vms, setVms] = useState<VMItem[]>([
    {
      vmid: 201,
      name: '201 (k8s-worker-01)',
      subtext: 'VirtIO-SCSI • ZFS subvol',
      status: 'RUNNING',
      vcpus: 4,
      ramGiB: 8,
      type: 'linux',
    },
    {
      vmid: 202,
      name: '202 (win-render-srv)',
      subtext: 'Windows Server 2022',
      status: 'STOPPED',
      vcpus: 8,
      ramGiB: 16,
      type: 'windows',
    },
  ]);

  const toggleVM = (vmid: number) => {
    setVms(prev =>
      prev.map(vm =>
        vm.vmid === vmid
          ? { ...vm, status: vm.status === 'RUNNING' ? 'STOPPED' : 'RUNNING' }
          : vm
      )
    );
  };

  return (
    <section className="md:col-span-6 bg-[#171a2d] rounded p-3 flex flex-col justify-between border border-[#26293c]">
      <div>
        <div className="flex items-center justify-between pb-1.5 bg-[#0a0d1f]/40 px-2 py-1 rounded border border-[#26293c]/40">
          <div className="flex items-center gap-2">
            <span className="material-symbols-outlined text-[#b0c9ff] text-[18px]">qr_code_2</span>
            <span className="font-sans text-sm text-[#dfe1fa] font-semibold">
              Virtual Machines (KVM/QEMU)
            </span>
          </div>
          <button
            type="button"
            onClick={onNewVM}
            className="flex items-center gap-1 bg-[#26293c] hover:bg-[#35384c] text-[#b0c9ff] px-2 py-0.5 rounded font-mono text-[11px] border border-[#434750]/40 transition-colors cursor-pointer"
          >
            <span className="material-symbols-outlined text-[13px]">add</span>
            <span>New VM</span>
          </button>
        </div>

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
                          onClick={() => toggleVM(vm.vmid)}
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
                          onClick={() => toggleVM(vm.vmid)}
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
      </div>

      <div className="mt-2 pt-1.5 bg-[#313447]/20 px-2 py-1 rounded flex items-center justify-between font-mono text-[11px] text-[#8d909b] border border-[#26293c]/40">
        <span>
          Hypervisor: <strong className="text-[#dfe1fa]">KVM / QEMU 8.2.2</strong>
        </span>
        <span>Total VM Allocation: 12 vCPUs, 24 GiB</span>
      </div>
    </section>
  );
};
