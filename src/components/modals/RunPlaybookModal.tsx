import React, { useState, useEffect } from 'react';
import { VarsSecretsEditor } from '../VarsSecretsEditor';

interface RunPlaybookModalProps {
  isOpen: boolean;
  onClose: () => void;
  initialPlaybook?: string;
  onRunPlaybook: (
    playbook: string,
    target: string,
    checkMode?: boolean,
    tags?: string,
    extraVars?: string
  ) => void;
  onOpenEditor?: (playbook: string) => void;
}

export const RunPlaybookModal: React.FC<RunPlaybookModalProps> = ({
  isOpen,
  onClose,
  initialPlaybook,
  onRunPlaybook,
  onOpenEditor,
}) => {
  const [playbook, setPlaybook] = useState(initialPlaybook || 'site.yml');
  const [availablePlaybooks, setAvailablePlaybooks] = useState<string[]>([
    'site.yml',
    'security-hardening.yml',
    'tailscale-status.yml',
    'podman-update.yml',
  ]);
  const [target, setTarget] = useState('tag:host');
  const [checkMode, setCheckMode] = useState(false);
  const [tags, setTags] = useState('');
  const [extraVars, setExtraVars] = useState('');
  const [syntaxStatus, setSyntaxStatus] = useState<string | null>(null);
  const [checkingSyntax, setCheckingSyntax] = useState(false);

  useEffect(() => {
    if (isOpen) {
      if (initialPlaybook) {
        setPlaybook(initialPlaybook);
      }
      fetch('/api/playbooks')
        .then(res => res.json())
        .then(data => {
          if (Array.isArray(data?.playbooks) && data.playbooks.length > 0) {
            setAvailablePlaybooks(data.playbooks);
          }
        })
        .catch(() => {});
    }
  }, [isOpen, initialPlaybook]);

  if (!isOpen) return null;

  const handleSyntaxCheck = async () => {
    setCheckingSyntax(true);
    setSyntaxStatus(null);
    try {
      const res = await fetch('/api/playbook/syntax-check', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ playbook }),
      });
      const data = await res.json();
      if (res.ok && data.success) {
        setSyntaxStatus('Syntax OK: Playbook structure verified');
      } else {
        setSyntaxStatus(`Syntax Error: ${data.error || 'Failed validation'}`);
      }
    } catch (err: any) {
      setSyntaxStatus(`Check Failed: ${err?.message || 'Connection error'}`);
    } finally {
      setCheckingSyntax(false);
    }
  };

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    onRunPlaybook(playbook, target, checkMode, tags.trim() || undefined, extraVars.trim() || undefined);
    onClose();
  };

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/70 backdrop-blur-xs p-4">
      <div className="w-full max-w-2xl bg-[#0a0d1f] rounded-lg shadow-2xl border border-[#313447] max-h-[90vh] flex flex-col overflow-hidden">
        <div className="h-9 bg-[#171a2d] px-3 flex items-center justify-between border-b border-[#26293c] shrink-0">
          <div className="flex items-center gap-2">
            <span className="material-symbols-outlined text-[#b0c9ff] text-[18px]">play_arrow</span>
            <span className="font-mono text-xs text-[#dfe1fa] font-bold">
              Ansible Production Playbook Runner
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

        <form onSubmit={handleSubmit} className="p-4 space-y-3 font-mono text-xs flex-1 overflow-y-auto">
          <div>
            <div className="flex items-center justify-between mb-1">
              <label className="text-[#8d909b]">SELECT TARGET PLAYBOOK</label>
              <div className="flex items-center gap-2">
                {onOpenEditor && (
                  <button
                    type="button"
                    onClick={() => onOpenEditor(playbook)}
                    className="text-[10px] text-[#b0c9ff] hover:underline cursor-pointer flex items-center gap-0.5"
                    title="Open in Playbook Editor"
                  >
                    <span className="material-symbols-outlined text-[12px]">edit_note</span>
                    <span>Edit / Manage Playbooks</span>
                  </button>
                )}
                {onOpenEditor && <span className="text-[#313447]">|</span>}
                <button
                  type="button"
                  onClick={handleSyntaxCheck}
                  disabled={checkingSyntax}
                  className="text-[10px] text-[#8cd6cb] hover:underline cursor-pointer flex items-center gap-1"
                >
                  <span className={`material-symbols-outlined text-[12px] ${checkingSyntax ? 'animate-spin' : ''}`}>
                    check_circle
                  </span>
                  <span>{checkingSyntax ? 'Checking...' : 'Check Syntax (--syntax-check)'}</span>
                </button>
              </div>
            </div>
            <div className="space-y-1.5 max-h-36 overflow-y-auto pr-1">
              {availablePlaybooks.map(p => (
                <label
                  key={p}
                  className={`flex items-start gap-2 p-2 rounded border cursor-pointer transition-colors ${
                    playbook === p
                      ? 'bg-[#26293c] border-[#b0c9ff] text-[#dfe1fa]'
                      : 'bg-[#1b1e31] border-[#26293c] text-[#c3c6d2] hover:bg-[#1f2238]'
                  }`}
                >
                  <input
                    type="radio"
                    name="playbook"
                    value={p}
                    checked={playbook === p}
                    onChange={() => {
                      setPlaybook(p);
                      setSyntaxStatus(null);
                    }}
                    className="mt-0.5"
                  />
                  <div>
                    <span className="font-bold text-[#b0c9ff]">{p}</span>
                  </div>
                </label>
              ))}
            </div>
            {syntaxStatus && (
              <p className={`text-[10px] mt-1.5 p-1 rounded ${
                syntaxStatus.startsWith('Syntax OK') ? 'bg-[#8cd6cb]/15 text-[#8cd6cb]' : 'bg-[#ffb4ab]/15 text-[#ffb4ab]'
              }`}>
                {syntaxStatus}
              </p>
            )}
          </div>

          <div className="grid grid-cols-2 gap-2">
            <div>
              <label className="block text-[#8d909b] mb-1">INVENTORY LIMIT / TARGET</label>
              <select
                value={target}
                onChange={e => setTarget(e.target.value)}
                className="w-full bg-[#1b1e31] border border-[#26293c] rounded px-2.5 py-1.5 text-[#dfe1fa] outline-none"
              >
                <option value="tag:host">tag:host (Cluster Nodes)</option>
                <option value="likes-it-cold">likes-it-cold.leo-banjo.ts.net</option>
                <option value="ping">ping.leo-banjo.ts.net</option>
                <option value="dev-pc">dev-pc.leo-banjo.ts.net</option>
                <option value="fred">fred.leo-banjo.ts.net</option>
                <option value="the-freezer">the-freezer.leo-banjo.ts.net</option>
                <option value="chuck">chuck.leo-banjo.ts.net</option>
                <option value="larry">larry.leo-banjo.ts.net</option>
                <option value="orangepizero3">orangepizero3.leo-banjo.ts.net</option>
                <option value="all">all (Full Tailnet)</option>
              </select>
            </div>

            <div>
              <label className="block text-[#8d909b] mb-1">TAGS (--tags)</label>
              <input
                type="text"
                placeholder="e.g. security, firewall"
                value={tags}
                onChange={e => setTags(e.target.value)}
                className="w-full bg-[#1b1e31] border border-[#26293c] rounded px-2.5 py-1.5 text-[#dfe1fa] outline-none"
              />
            </div>
          </div>

          <div className="space-y-1">
            <div className="flex items-center justify-between">
              <label className="text-[#8d909b] font-bold">EXTRA VARIABLES &amp; SECRETS (--extra-vars)</label>
              <span className="text-[10px] text-[#8cd6cb]">Structured Key-Value &amp; Vault Masking</span>
            </div>
            <div className="h-44 border border-[#26293c] rounded overflow-hidden">
              <VarsSecretsEditor
                yamlContent={extraVars}
                onChangeYaml={setExtraVars}
                compact={true}
                allowModeToggle={true}
                defaultMode="structured"
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
