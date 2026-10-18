import React, { useState } from 'react';

interface RunPlaybookModalProps {
  isOpen: boolean;
  onClose: () => void;
  onRunPlaybook: (playbook: string, target: string) => void;
}

export const RunPlaybookModal: React.FC<RunPlaybookModalProps> = ({
  isOpen,
  onClose,
  onRunPlaybook,
}) => {
  const [playbook, setPlaybook] = useState('site.yml');
  const [target, setTarget] = useState('all (14 hosts)');
  const [checkMode, setCheckMode] = useState(false);
  const [vaultPassword, setVaultPassword] = useState('••••••••••••');

  if (!isOpen) return null;

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    onRunPlaybook(playbook, target);
    onClose();
  };

  const playbooks = [
    { id: 'site.yml', name: 'site.yml', desc: 'Full datacenter orchestration and service stack deployment' },
    { id: 'update-all.yml', name: 'update-all.yml', desc: 'Rolling OS package updates and kernel verification' },
    { id: 'security-hardening.yml', name: 'security-hardening.yml', desc: 'NFTables firewall, SSH strict MFA, rootless podman policies' },
    { id: 'restic-snapshot.yml', name: 'restic-snapshot.yml', desc: 'Run datacenter-wide restic immutable snapshots and deduplication' },
    { id: 'deploy-containers.yml', name: 'deploy-containers.yml', desc: 'Podman compose / pod specification rollout' },
  ];

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/70 backdrop-blur-xs p-4">
      <div className="w-full max-w-lg bg-[#0a0d1f] rounded-lg shadow-2xl border border-[#313447] overflow-hidden">
        <div className="h-9 bg-[#171a2d] px-3 flex items-center justify-between border-b border-[#26293c]">
          <div className="flex items-center gap-2">
            <span className="material-symbols-outlined text-[#b0c9ff] text-[18px]">play_arrow</span>
            <span className="font-mono text-xs text-[#dfe1fa] font-bold">
              Ansible Playbook Orchestration Runner
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

        <form onSubmit={handleSubmit} className="p-4 space-y-3 font-mono text-xs">
          <div>
            <label className="block text-[#8d909b] mb-1">SELECT TARGET PLAYBOOK</label>
            <div className="space-y-1.5">
              {playbooks.map(p => (
                <label
                  key={p.id}
                  className={`flex items-start gap-2 p-2 rounded border cursor-pointer transition-colors ${
                    playbook === p.id
                      ? 'bg-[#26293c] border-[#b0c9ff] text-[#dfe1fa]'
                      : 'bg-[#1b1e31] border-[#26293c] text-[#c3c6d2] hover:bg-[#1f2238]'
                  }`}
                >
                  <input
                    type="radio"
                    name="playbook"
                    value={p.id}
                    checked={playbook === p.id}
                    onChange={() => setPlaybook(p.id)}
                    className="mt-0.5"
                  />
                  <div>
                    <span className="font-bold text-[#b0c9ff]">{p.name}</span>
                    <p className="text-[10px] text-[#8d909b] mt-0.5">{p.desc}</p>
                  </div>
                </label>
              ))}
            </div>
          </div>

          <div className="grid grid-cols-2 gap-2">
            <div>
              <label className="block text-[#8d909b] mb-1">INVENTORY LIMIT / TARGET</label>
              <select
                value={target}
                onChange={e => setTarget(e.target.value)}
                className="w-full bg-[#1b1e31] border border-[#26293c] rounded px-2.5 py-1.5 text-[#dfe1fa] outline-none"
              >
                <option value="all (14 hosts)">all (14 hosts)</option>
                <option value="cold-war">cold-war (5 nodes)</option>
                <option value="noodle">noodle (2 nodes)</option>
                <option value="150 (openmediavault)">150 (openmediavault)</option>
                <option value="153 (llama-inference)">153 (llama-inference)</option>
                <option value="webservers">group: webservers</option>
                <option value="db_cluster">group: db_cluster</option>
              </select>
            </div>

            <div>
              <label className="block text-[#8d909b] mb-1">ANSIBLE VAULT SECRET</label>
              <input
                type="password"
                value={vaultPassword}
                onChange={e => setVaultPassword(e.target.value)}
                className="w-full bg-[#1b1e31] border border-[#26293c] rounded px-2.5 py-1.5 text-[#dfe1fa] outline-none"
              />
            </div>
          </div>

          <div className="flex items-center gap-2 pt-1">
            <input
              type="checkbox"
              id="check-mode"
              checked={checkMode}
              onChange={e => setCheckMode(e.target.checked)}
            />
            <label htmlFor="check-mode" className="text-[#c3c6d2] text-[11px] cursor-pointer">
              Run in dry-run mode (--check --diff)
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
              type="submit"
              className="px-3 py-1.5 bg-[#b0c9ff] hover:bg-[#d8e2ff] text-[#002e68] font-bold rounded cursor-pointer flex items-center gap-1 shadow-sm"
            >
              <span className="material-symbols-outlined text-[15px]">play_arrow</span>
              Execute Playbook
            </button>
          </div>
        </form>
      </div>
    </div>
  );
};
