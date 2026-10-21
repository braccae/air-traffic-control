import React, { useCallback, useEffect, useMemo, useState } from 'react';
import { VarsSecretsEditor } from './VarsSecretsEditor.tsx';

/**
 * Full page editor for datacenter vars (Ansible group_vars files).
 *
 * Every read and write goes through the coordinator's real vars store:
 *   GET    /api/group-vars          -> { "<file path>": "<yaml>" }
 *   PUT    /api/group-vars          -> { file, content }
 *   DELETE /api/group-vars?file=... -> { success, file }
 *
 * There are deliberately no client-side defaults: if the store cannot be read the
 * page says so instead of showing invented vars (Zero-Mock Policy, AGENTS.md §2).
 */

const GROUP_VAR_FILE_RE = /^[A-Za-z0-9._/-]+\.ya?ml$/;

export function isValidVarFile(file: string): boolean {
  return (
    GROUP_VAR_FILE_RE.test(file) &&
    !file.includes('..') &&
    !file.startsWith('/') &&
    !file.includes('\\')
  );
}

type SaveState = 'idle' | 'saving' | 'saved';

export interface DatacenterVarsPageProps {
  /** Optional callback so a host shell can react to a persisted vars change. */
  onSaved?: (file: string) => void;
  /**
   * When false the page omits its own title block. Used when the settings shell
   * already renders the page header, so the setting is not labelled twice.
   */
  showHeader?: boolean;
}

export const DatacenterVarsPage: React.FC<DatacenterVarsPageProps> = ({ onSaved, showHeader = true }) => {
  const [stored, setStored] = useState<Record<string, string> | null>(null);
  const [drafts, setDrafts] = useState<Record<string, string>>({});
  const [selectedFile, setSelectedFile] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);
  const [saveState, setSaveState] = useState<SaveState>('idle');
  const [newFilePath, setNewFilePath] = useState('');
  const [isAddingFile, setIsAddingFile] = useState(false);
  const [confirmDeleteFile, setConfirmDeleteFile] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const loadVars = useCallback(async () => {
    setLoading(true);
    try {
      const res = await fetch('/api/group-vars');
      if (!res.ok) {
        throw new Error(`GET /api/group-vars returned ${res.status} ${res.statusText}`);
      }
      const data = await res.json();
      if (!data || typeof data !== 'object' || Array.isArray(data)) {
        throw new Error('GET /api/group-vars returned an unexpected payload');
      }
      const next: Record<string, string> = {};
      for (const [file, content] of Object.entries(data as Record<string, unknown>)) {
        next[file] = typeof content === 'string' ? content : '';
      }
      setStored(next);
      setDrafts(next);
      setLoadError(null);
      setSelectedFile(prev => {
        if (prev && Object.prototype.hasOwnProperty.call(next, prev)) return prev;
        const keys = Object.keys(next).sort();
        return keys.length > 0 ? keys[0] : null;
      });
    } catch (err: any) {
      // Never fall back to fabricated defaults — report the real failure.
      setStored(null);
      setDrafts({});
      setSelectedFile(null);
      setLoadError(err?.message || String(err));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void loadVars();
  }, [loadVars]);

  const fileList = useMemo(() => (stored ? Object.keys(stored).sort() : []), [stored]);

  const currentDraft = selectedFile ? drafts[selectedFile] ?? '' : '';
  const currentStored = selectedFile && stored ? stored[selectedFile] ?? '' : '';
  const isDirty = Boolean(selectedFile) && currentDraft !== currentStored;

  const handleSelectFile = (file: string) => {
    setSelectedFile(file);
    setActionError(null);
    setSaveState('idle');
  };

  const handleYamlChange = (yaml: string) => {
    if (!selectedFile) return;
    setDrafts(prev => ({ ...prev, [selectedFile]: yaml }));
    setSaveState('idle');
  };

  const handleSave = async () => {
    if (!selectedFile || busy) return;
    setBusy(true);
    setActionError(null);
    setSaveState('saving');
    try {
      const res = await fetch('/api/group-vars', {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ file: selectedFile, content: currentDraft }),
      });
      const body = await res.json().catch(() => null);
      if (!res.ok || !body?.success) {
        throw new Error(body?.error || `PUT /api/group-vars returned ${res.status} ${res.statusText}`);
      }
      // Re-read the store so the UI reflects what was actually persisted.
      await loadVars();
      setSaveState('saved');
      if (onSaved) onSaved(selectedFile);
    } catch (err: any) {
      setSaveState('idle');
      setActionError(err?.message || String(err));
    } finally {
      setBusy(false);
    }
  };

  const handleAddFile = async () => {
    const file = newFilePath.trim();
    if (!isValidVarFile(file)) {
      setActionError('Vars file must be a relative *.yml path, e.g. group_vars/tag-worker.yml');
      return;
    }
    if (stored && Object.prototype.hasOwnProperty.call(stored, file)) {
      setActionError(`${file} already exists`);
      return;
    }
    setBusy(true);
    setActionError(null);
    try {
      const res = await fetch('/api/group-vars', {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ file, content: '---\n' }),
      });
      const body = await res.json().catch(() => null);
      if (!res.ok || !body?.success) {
        throw new Error(body?.error || `PUT /api/group-vars returned ${res.status} ${res.statusText}`);
      }
      setNewFilePath('');
      setIsAddingFile(false);
      await loadVars();
      setSelectedFile(file);
      setSaveState('idle');
    } catch (err: any) {
      setActionError(err?.message || String(err));
    } finally {
      setBusy(false);
    }
  };

  const handleDeleteFile = async (file: string) => {
    setBusy(true);
    setActionError(null);
    try {
      const res = await fetch(`/api/group-vars?file=${encodeURIComponent(file)}`, {
        method: 'DELETE',
      });
      const body = await res.json().catch(() => null);
      if (!res.ok || !body?.success) {
        throw new Error(body?.error || `DELETE /api/group-vars returned ${res.status} ${res.statusText}`);
      }
      setConfirmDeleteFile(null);
      await loadVars();
    } catch (err: any) {
      setActionError(err?.message || String(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div
      id="datacenter-vars_vault"
      data-testid="datacenter-vars-page"
      className="rounded-lg bg-[#121528] border border-[#26293c] p-4 shadow-md flex flex-col gap-3"
    >
      {/* Header */}
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="flex items-center gap-2 flex-wrap">
          <span className="material-symbols-outlined text-[18px] text-[#b0c9ff]">data_object</span>
          {showHeader && (
            <h2 className="text-xs font-mono font-bold text-[#dfe1fa] uppercase tracking-wider">
              Datacenter Vars
            </h2>
          )}
          <span className="text-[10px] font-mono px-1.5 py-0.5 rounded bg-[#1b2a38] text-[#b0c9ff] border border-[#b0c9ff]/30">
            {stored ? `${fileList.length} ${fileList.length === 1 ? 'file' : 'files'}` : 'store unavailable'}
          </span>
          <span className="text-[10px] font-mono text-[#8d909b]">source: /api/group-vars (SQLite store)</span>
        </div>

        <div className="flex items-center gap-2">
          <button
            type="button"
            onClick={() => {
              setIsAddingFile(v => !v);
              setActionError(null);
            }}
            disabled={busy || loading || Boolean(loadError)}
            className="px-2.5 py-1 rounded text-xs font-mono text-[#8cd6cb] bg-[#171a2d] hover:bg-[#21263d] border border-[#26293c] hover:border-[#8cd6cb]/60 disabled:opacity-50 transition-colors flex items-center gap-1 cursor-pointer"
            title="Create a new group_vars file in the datacenter vars store"
          >
            <span className="material-symbols-outlined text-[14px]">note_add</span>
            <span>New Vars File</span>
          </button>

          <button
            type="button"
            onClick={handleSave}
            disabled={!selectedFile || !isDirty || busy}
            className="px-3 py-1 rounded text-xs font-mono font-bold text-[#121528] bg-[#b0c9ff] hover:bg-[#d8e2ff] disabled:opacity-40 disabled:cursor-not-allowed transition-colors flex items-center gap-1.5 cursor-pointer"
            title="Persist the selected vars file through PUT /api/group-vars"
          >
            <span className="material-symbols-outlined text-[15px]">
              {saveState === 'saving' ? 'sync' : 'save'}
            </span>
            <span>{saveState === 'saving' ? 'Saving...' : saveState === 'saved' ? 'Saved' : 'Save Changes'}</span>
          </button>
        </div>
      </div>

      {showHeader && (
        <p className="text-[11px] text-[#8d909b] font-mono">
          Ansible group variables used by the datacenter playbooks. Values are persisted to the coordinator's
          SQLite vars store and survive reloads.
        </p>
      )}

      {/* New file form */}
      {isAddingFile && (
        <div className="flex flex-wrap items-center gap-2 bg-[#171a2d] border border-[#26293c] rounded p-2 font-mono text-xs">
          <span className="text-[#8d909b]">Path</span>
          <input
            type="text"
            value={newFilePath}
            onChange={e => setNewFilePath(e.target.value)}
            placeholder="group_vars/tag-worker.yml"
            className="flex-1 min-w-[240px] bg-[#121528] border border-[#26293c] focus:border-[#b0c9ff] rounded px-2 py-1 text-xs font-mono text-[#dfe1fa] placeholder-[#8d909b] outline-none"
          />
          <button
            type="button"
            onClick={handleAddFile}
            disabled={busy || !newFilePath.trim()}
            className="px-2.5 py-1 rounded bg-[#1b2a38] text-[#8cd6cb] border border-[#8cd6cb]/30 hover:border-[#8cd6cb] disabled:opacity-50 cursor-pointer"
          >
            Create
          </button>
          <button
            type="button"
            onClick={() => {
              setIsAddingFile(false);
              setNewFilePath('');
              setActionError(null);
            }}
            className="px-2 py-1 rounded text-[#8d909b] hover:text-[#dfe1fa] cursor-pointer"
          >
            Cancel
          </button>
        </div>
      )}

      {/* Load / action errors — real failures are surfaced, never masked */}
      {loadError && (
        <div className="p-2.5 rounded text-xs font-mono border bg-[#311c1c] text-[#ffb4ab] border-[#ffb4ab]/30 flex items-center justify-between gap-3">
          <span className="flex items-center gap-1.5">
            <span className="material-symbols-outlined text-[15px]">error</span>
            <span>Could not read the vars store: {loadError}</span>
          </span>
          <button
            type="button"
            onClick={() => void loadVars()}
            className="px-2 py-0.5 rounded border border-[#ffb4ab]/40 hover:bg-[#ffb4ab]/10 cursor-pointer"
          >
            Retry
          </button>
        </div>
      )}

      {actionError && (
        <div className="p-2.5 rounded text-xs font-mono border bg-[#311c1c] text-[#ffb4ab] border-[#ffb4ab]/30 flex items-center gap-1.5">
          <span className="material-symbols-outlined text-[15px]">error</span>
          <span>{actionError}</span>
        </div>
      )}

      {/* Body */}
      <div className="flex flex-col lg:flex-row gap-3 min-h-[420px]">
        {/* File list */}
        <div className="lg:w-64 shrink-0 rounded border border-[#26293c] bg-[#0f1224] flex flex-col overflow-hidden">
          <div className="px-2.5 py-1.5 bg-[#171a2d] border-b border-[#26293c] text-[10px] font-mono uppercase tracking-wider text-[#8d909b]">
            Group Vars Files
          </div>
          <div className="flex-1 overflow-auto">
            {loading ? (
              <div className="p-3 text-xs font-mono text-[#8d909b]">Reading vars store…</div>
            ) : fileList.length === 0 ? (
              <div className="p-3 text-xs font-mono text-[#8d909b]">
                {loadError ? 'Store unavailable.' : 'No vars files in the store yet.'}
              </div>
            ) : (
              <ul className="divide-y divide-[#1f2238]">
                {fileList.map(file => {
                  const dirty = (drafts[file] ?? '') !== (stored?.[file] ?? '');
                  const active = file === selectedFile;
                  return (
                    <li key={file} className={`px-2 py-1.5 flex items-center gap-1 ${active ? 'bg-[#1b2136]' : 'hover:bg-[#15182b]'}`}>
                      <button
                        type="button"
                        onClick={() => handleSelectFile(file)}
                        className={`flex-1 text-left font-mono text-[11px] truncate cursor-pointer ${
                          active ? 'text-[#b0c9ff] font-bold' : 'text-[#c3c6d2]'
                        }`}
                        title={file}
                      >
                        {file}
                        {dirty && <span className="ml-1 text-[#ffd580]">*</span>}
                      </button>

                      {confirmDeleteFile === file ? (
                        <span className="flex items-center gap-1">
                          <button
                            type="button"
                            onClick={() => void handleDeleteFile(file)}
                            disabled={busy}
                            className="px-1 py-0.5 rounded text-[10px] bg-[#ffb4ab] text-[#121528] font-bold cursor-pointer disabled:opacity-50"
                            title={`Delete ${file} from the vars store`}
                          >
                            Delete
                          </button>
                          <button
                            type="button"
                            onClick={() => setConfirmDeleteFile(null)}
                            className="px-1 py-0.5 rounded text-[10px] text-[#8d909b] hover:text-[#dfe1fa] cursor-pointer"
                          >
                            No
                          </button>
                        </span>
                      ) : (
                        <button
                          type="button"
                          onClick={() => setConfirmDeleteFile(file)}
                          disabled={busy}
                          className="text-[#8d909b] hover:text-[#ffb4ab] p-0.5 cursor-pointer disabled:opacity-50"
                          title={`Remove ${file}`}
                        >
                          <span className="material-symbols-outlined text-[14px]">delete</span>
                        </button>
                      )}
                    </li>
                  );
                })}
              </ul>
            )}
          </div>
        </div>

        {/* Editor */}
        <div className="flex-1 min-w-0 rounded border border-[#26293c] overflow-hidden flex flex-col">
          {selectedFile ? (
            <VarsSecretsEditor
              yamlContent={currentDraft}
              onChangeYaml={handleYamlChange}
              title={selectedFile}
              defaultMode="structured"
            />
          ) : (
            <div className="flex-1 flex flex-col items-center justify-center gap-2 text-[#8d909b] font-mono text-xs py-12">
              <span className="material-symbols-outlined text-3xl text-[#313447]">data_object</span>
              <span>
                {loading
                  ? 'Loading datacenter vars…'
                  : loadError
                  ? 'Vars store unreachable — nothing to display.'
                  : 'Select a vars file, or create one with New Vars File.'}
              </span>
            </div>
          )}
        </div>
      </div>

      {/* Footer status */}
      <div className="h-7 bg-[#121528] px-1 flex items-center justify-between font-mono text-[10px] text-[#8d909b]">
        <span>
          {selectedFile
            ? `${selectedFile}${isDirty ? ' — unsaved changes' : ' — in sync with store'}`
            : 'No file selected'}
        </span>
        <span>
          {saveState === 'saved' ? 'Persisted to SQLite vars store' : 'Writes go to /api/group-vars'}
        </span>
      </div>
    </div>
  );
};
