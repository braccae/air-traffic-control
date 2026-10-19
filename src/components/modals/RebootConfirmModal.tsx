import React, { useState } from 'react';
import type { NodeItem } from '../../types/index.ts';
import { getDriver, systemDriver, stageTaskInQueue } from '../../utils/drivers/index.ts';

interface RebootConfirmModalProps {
  node: NodeItem;
  isOpen: boolean;
  onClose: () => void;
  onConfirmReboot: (mode: string) => void;
}

export const RebootConfirmModal: React.FC<RebootConfirmModalProps> = ({
  node,
  isOpen,
  onClose,
  onConfirmReboot,
}) => {
  const [mode, setMode] = useState('graceful');
  const [isStaging, setIsStaging] = useState(false);

  if (!isOpen) return null;

  const handleStageReboot = async () => {
    setIsStaging(true);
    try {
      const driver = getDriver('system', node) || systemDriver;
      const task = driver.compileMutation(node, 'reboot', {
        mode,
        message: `Safety reboot (${mode}) staged from Cockpit`,
      });
      const res = await stageTaskInQueue(task);
      if (res.success) {
        onClose();
      }
    } catch (err) {
      console.error('Failed to stage reboot task:', err);
    } finally {
      setIsStaging(false);
    }
  };

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/70 backdrop-blur-xs p-4">
      <div className="w-full max-w-md bg-[#0a0d1f] rounded-lg shadow-2xl border border-[#313447] overflow-hidden">
        <div className="h-9 bg-[#171a2d] px-3 flex items-center justify-between border-b border-[#26293c]">
          <div className="flex items-center gap-2 text-[#ffb4ab]">
            <span className="material-symbols-outlined text-[18px]">warning</span>
            <span className="font-mono text-xs font-bold text-[#dfe1fa]">
              Confirm Safety Reboot: {node.shortName}
            </span>
          </div>
          <button
            type="button"
            onClick={onClose}
            className="text-[#8d909b] hover:text-[#dfe1fa] p-1 cursor-pointer"
          >
            <span className="material-symbols-outlined text-[16px]">close</span>
          </button>
        </div>

        <div className="p-4 space-y-3 font-mono text-xs">
          <p className="text-[#c3c6d2]">
            You are about to restart node <strong className="text-[#b0c9ff]">{node.name}</strong>.
            All active container workloads and KVM virtual machines will be notified.
          </p>

          <div className="space-y-1.5">
            <label className="flex items-center gap-2 p-2 bg-[#1b1e31] rounded border border-[#26293c] cursor-pointer">
              <input
                type="radio"
                name="reboot-mode"
                value="graceful"
                checked={mode === 'graceful'}
                onChange={() => setMode('graceful')}
              />
              <div>
                <span className="font-bold text-[#8cd6cb]">Graceful ACPI Shutdown &amp; Reboot</span>
                <p className="text-[10px] text-[#8d909b]">Flushes ZFS write buffers and syncs disk states.</p>
              </div>
            </label>

            <label className="flex items-center gap-2 p-2 bg-[#1b1e31] rounded border border-[#26293c] cursor-pointer">
              <input
                type="radio"
                name="reboot-mode"
                value="soft"
                checked={mode === 'soft'}
                onChange={() => setMode('soft')}
              />
              <div>
                <span className="font-bold text-[#b0c9ff]">Soft Reboot (systemctl reboot)</span>
                <p className="text-[10px] text-[#8d909b]">Invokes systemd target reboot.target directly.</p>
              </div>
            </label>

            <label className="flex items-center gap-2 p-2 bg-[#1b1e31] rounded border border-[#26293c] cursor-pointer">
              <input
                type="radio"
                name="reboot-mode"
                value="hard"
                checked={mode === 'hard'}
                onChange={() => setMode('hard')}
              />
              <div>
                <span className="font-bold text-[#ffb4ab]">Hard Reset (BMC / IPMI Chassis Power)</span>
                <p className="text-[10px] text-[#8d909b]">Immediate hardware reset. Use only if kernel panic.</p>
              </div>
            </label>
          </div>

          <div className="pt-2 flex items-center justify-end gap-2 border-t border-[#26293c]">
            <button
              type="button"
              onClick={onClose}
              className="px-3 py-1.5 bg-[#1b1e31] hover:bg-[#26293c] text-[#c3c6d2] rounded cursor-pointer"
            >
              Cancel
            </button>
            <button
              type="button"
              disabled={isStaging}
              onClick={handleStageReboot}
              className="px-3 py-1.5 bg-[#171a2d] hover:bg-[#26293c] text-[#ffd97d] border border-[#ffd97d]/40 rounded cursor-pointer font-bold flex items-center gap-1.5 transition-colors disabled:opacity-50"
              title="Stage reboot task into Ansible Mutation Queue without immediate execution"
            >
              <span className="material-symbols-outlined text-[15px]">queue</span>
              <span>{isStaging ? 'Staging...' : 'Stage in Queue'}</span>
            </button>
            <button
              type="button"
              onClick={() => {
                onConfirmReboot(mode);
                onClose();
              }}
              className="px-3 py-1.5 bg-[#93000a] hover:bg-[#ff5449] text-white font-bold rounded cursor-pointer shadow-sm"
            >
              Proceed with Reboot
            </button>
          </div>
        </div>
      </div>
    </div>
  );
};
