import React, { useCallback, useEffect, useMemo, useState } from 'react';
import { isValidVaultVariableName, maskVaultValue } from '../utils/vaultMasking.ts';

/**
 * Wire shape returned by GET /api/vault/variables. There is no value field:
 * the API only ever emits metadata plus the mask (Secret Protection Invariant).
 */
export interface VaultVariableView {
  name: string;
  createdAt: number;
  updatedAt: number;
  maskedValue?: string;
}

interface VaultStatusResponse {
  hasPasswordSource: boolean;
  source: 'file' | 'env' | 'ask_password' | string;
}

export interface VaultSettingsPageProps {
  /** Optional "back to datacenter" affordance rendered in the header. */
  onBack?: () => void;
}

type EditorMode = 'closed' | 'create' | 'edit';

interface EditorState {
  mode: EditorMode;
  originalName: string;
  name: string;
  value: string;
  password: string;
}

const CLOSED_EDITOR: EditorState = {
  mode: 'closed',
  originalName: '',
  name: '',
  value: '',
  password: '',
};

function formatTimestamp(ms: number): string {
  if (!Number.isFinite(ms) || ms <= 0) return '—';
  const d = new Date(ms);
  const pad = (n: number) => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(
    d.getMinutes()
  )}`;
}

function sourceLabel(status: VaultStatusResponse | null): string {
  if (!status) return 'unknown';
  if (!status.hasPasswordSource) return 'prompt per save';
  if (status.source === 'file') return 'vault_password_file';
  if (status.source === 'env') return 'environment';
  return status.source;
}

/**
 * Full-page editor for the Ansible Vault store.
 *
 * Create / edit / delete are real API calls backed by the SQLite vault store.
 * Values are write-only: the table renders `•••••••• (vault: <name>)` and the
 * edit form accepts a replacement value without ever loading the current one.
 */
export const VaultSettingsPage: React.FC<VaultSettingsPageProps> = ({ onBack }) => {
  const [variables, setVariables] = useState<VaultVariableView[]>([]);
  const [status, setStatus] = useState<VaultStatusResponse | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [editor, setEditor] = useState<EditorState>(CLOSED_EDITOR);
  const [busy, setBusy] = useState(false);
  const [formError, setFormError] = useState<string | null>(null);
  const [pendingDelete, setPendingDelete] = useState<string | null>(null);

  const loadVariables = useCallback(async () => {
    try {
      const res = await fetch('/api/vault/variables');
      const data = await res.json();
      if (!res.ok || data?.success === false) {
        throw new Error(data?.error || `Request failed (HTTP ${res.status})`);
      }
      const list: VaultVariableView[] = Array.isArray(data?.variables) ? data.variables : [];
      setVariables(list);
      setError(null);
    } catch (err: any) {
      setError(err?.message || 'Failed to load vault variables');
    } finally {
      setLoading(false);
    }
  }, []);

  const loadStatus = useCallback(async () => {
    try {
      const res = await fetch('/api/vault/status');
      const data = await res.json();
      if (res.ok && data?.success !== false) {
        setStatus({ hasPasswordSource: Boolean(data.hasPasswordSource), source: data.source });
      }
    } catch {
      // Status is advisory only; the table still works without it.
      setStatus(null);
    }
  }, []);

  useEffect(() => {
    loadVariables();
    loadStatus();
  }, [loadVariables, loadStatus]);

  const openCreate = () => {
    setFormError(null);
    setNotice(null);
    setEditor({ mode: 'create', originalName: '', name: '', value: '', password: '' });
  };

  const openEdit = (variable: VaultVariableView) => {
    setFormError(null);
    setNotice(null);
    setEditor({ mode: 'edit', originalName: variable.name, name: variable.name, value: '', password: '' });
  };

  const closeEditor = () => {
    setEditor(CLOSED_EDITOR);
    setFormError(null);
  };

  const submitEditor = async () => {
    const isCreate = editor.mode === 'create';
    const name = editor.name.trim();
    const originalName = editor.originalName;

    if (!isValidVaultVariableName(name)) {
      setFormError('Name must start with a letter or underscore and contain only letters, digits and underscores.');
      return;
    }
    if (isCreate && editor.value.length === 0) {
      setFormError('A value is required to create a vault variable.');
      return;
    }
    if (!isCreate && editor.value.length === 0 && name === originalName) {
      setFormError('Enter a replacement value, or change the name to rename.');
      return;
    }

    setBusy(true);
    setFormError(null);
    try {
      const payload: Record<string, string> = {};
      if (isCreate) {
        payload.name = name;
        payload.value = editor.value;
      } else {
        if (editor.value.length > 0) payload.value = editor.value;
        if (name !== originalName) payload.newName = name;
      }
      // Inline password is used for this request only and is never persisted.
      if (editor.password.length > 0) payload.password = editor.password;

      const url = isCreate
        ? '/api/vault/variables'
        : `/api/vault/variables/${encodeURIComponent(originalName)}`;
      const method = isCreate ? 'POST' : 'PUT';

      const res = await fetch(url, {
        method,
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload),
      });
      const data = await res.json();
      if (!res.ok || data?.success === false) {
        throw new Error(data?.error || `Request failed (HTTP ${res.status})`);
      }

      closeEditor();
      setNotice(isCreate ? `Encrypted and stored "${name}".` : `Updated "${name}".`);
      await loadVariables();
    } catch (err: any) {
      setFormError(err?.message || 'Failed to save vault variable');
    } finally {
      setBusy(false);
    }
  };

  const confirmDelete = async (name: string) => {
    setBusy(true);
    setError(null);
    try {
      const res = await fetch(`/api/vault/variables/${encodeURIComponent(name)}`, { method: 'DELETE' });
      const data = await res.json();
      if (!res.ok || data?.success === false) {
        throw new Error(data?.error || `Request failed (HTTP ${res.status})`);
      }
      setPendingDelete(null);
      setNotice(`Deleted "${name}".`);
      await loadVariables();
    } catch (err: any) {
      setError(err?.message || 'Failed to delete vault variable');
    } finally {
      setBusy(false);
    }
  };

  const passwordSource = useMemo(() => sourceLabel(status), [status]);
  const editorTitle = editor.mode === 'create' ? 'New vault variable' : `Edit ${editor.originalName}`;

  return (
    <div className="flex flex-col gap-3 w-full font-mono text-xs text-[#dfe1fa]">
      {/* Header */}
      <div className="rounded-lg bg-[#121528] border border-[#26293c] p-4 shadow-md flex flex-col gap-3">
        <div className="flex items-start justify-between gap-3 flex-wrap">
          <div className="flex items-start gap-2.5">
            {onBack && (
              <button
                type="button"
                onClick={onBack}
                className="mt-0.5 text-[#8d909b] hover:text-[#dfe1fa] p-1 rounded hover:bg-[#1b1e31] cursor-pointer"
                title="Back to datacenter overview"
                aria-label="Back to datacenter overview"
              >
                <span className="material-symbols-outlined text-[18px]">arrow_back</span>
              </button>
            )}
            <div>
              <div className="flex items-center gap-2">
                <span className="material-symbols-outlined text-[20px] text-[#d9b9ff]">lock</span>
                <h1 className="text-sm font-bold text-[#f0f2ff] tracking-wide">Ansible Vault</h1>
                <span className="text-[10px] px-1.5 py-0.5 rounded bg-[#291e3b] text-[#d9b9ff] border border-[#d9b9ff]/30">
                  AES256
                </span>
                <span className="text-[10px] px-1.5 py-0.5 rounded bg-[#1b1e31] text-[#8d909b] border border-[#313447]">
                  {variables.length} {variables.length === 1 ? 'variable' : 'variables'}
                </span>
              </div>
              <p className="text-[11px] text-[#8d909b] mt-1">
                Encrypted variables persisted in the coordinator vault store. Values are write-only and
                never rendered or logged in plaintext.
              </p>
            </div>
          </div>

          <div className="flex items-center gap-2">
            <span
              className={`text-[10px] px-1.5 py-0.5 rounded border ${
                status?.hasPasswordSource
                  ? 'bg-[#1b2a38] text-[#8cd6cb] border-[#8cd6cb]/30'
                  : 'bg-[#2d281b] text-[#ffd580] border-[#ffd580]/30'
              }`}
              title="Where the vault password comes from when encrypting or running playbooks"
            >
              password source: {passwordSource}
            </span>
            <button
              type="button"
              onClick={() => {
                setLoading(true);
                loadVariables();
                loadStatus();
              }}
              className="px-2 py-1 rounded border border-[#313447] text-[#b0c9ff] hover:bg-[#26293c] cursor-pointer"
            >
              Refresh
            </button>
            <button
              type="button"
              onClick={openCreate}
              className="px-2.5 py-1 rounded bg-[#1b1e31] text-[#8cd6cb] border border-[#8cd6cb]/30 hover:border-[#8cd6cb] hover:bg-[#26293c] cursor-pointer flex items-center gap-1"
            >
              <span className="material-symbols-outlined text-[14px]">add</span>
              Add Variable
            </button>
          </div>
        </div>

        {notice && (
          <div className="text-[11px] text-[#8cd6cb] flex items-center gap-1.5">
            <span className="material-symbols-outlined text-[14px]">check_circle</span>
            <span>{notice}</span>
          </div>
        )}
        {error && (
          <div className="text-[11px] text-[#ffb4ab] flex items-center gap-1.5">
            <span className="material-symbols-outlined text-[14px]">error</span>
            <span>{error}</span>
          </div>
        )}
      </div>

      {/* Editor */}
      {editor.mode !== 'closed' && (
        <div className="rounded-lg bg-[#121528] border border-[#b0c9ff]/30 p-4 shadow-md flex flex-col gap-3">
          <div className="flex items-center gap-2">
            <span className="material-symbols-outlined text-[16px] text-[#b0c9ff]">
              {editor.mode === 'create' ? 'key' : 'edit'}
            </span>
            <h2 className="text-xs font-bold text-[#dfe1fa]">{editorTitle}</h2>
            {editor.mode === 'edit' && (
              <span className="text-[10px] text-[#8d909b]">
                current value: {maskVaultValue(editor.originalName)}
              </span>
            )}
          </div>

          <div className="grid grid-cols-1 md:grid-cols-3 gap-3">
            <label className="flex flex-col gap-1">
              <span className="text-[10px] uppercase tracking-wider text-[#8d909b]">Variable name</span>
              <input
                type="text"
                value={editor.name}
                onChange={e => setEditor(prev => ({ ...prev, name: e.target.value }))}
                placeholder="restic_password"
                autoComplete="off"
                spellCheck={false}
                className="bg-[#171a2d] border border-[#26293c] focus:border-[#b0c9ff] rounded px-2 py-1 text-xs text-[#b0c9ff] outline-none"
              />
            </label>

            <label className="flex flex-col gap-1 md:col-span-1">
              <span className="text-[10px] uppercase tracking-wider text-[#8d909b]">
                {editor.mode === 'create' ? 'Value' : 'New value (optional)'}
              </span>
              <input
                type="password"
                value={editor.value}
                onChange={e => setEditor(prev => ({ ...prev, value: e.target.value }))}
                placeholder={editor.mode === 'create' ? 'secret value' : 'leave blank to keep unchanged'}
                autoComplete="new-password"
                spellCheck={false}
                className="bg-[#171a2d] border border-[#26293c] focus:border-[#d9b9ff] rounded px-2 py-1 text-xs text-[#d9b9ff] outline-none"
              />
              <span className="text-[10px] text-[#8d909b]">
                Encrypted with ansible-vault before it touches disk.
              </span>
            </label>

            <label className="flex flex-col gap-1">
              <span className="text-[10px] uppercase tracking-wider text-[#8d909b]">
                Vault password {status?.hasPasswordSource ? '(optional)' : '(required)'}
              </span>
              <input
                type="password"
                value={editor.password}
                onChange={e => setEditor(prev => ({ ...prev, password: e.target.value }))}
                placeholder="used for this request only"
                autoComplete="current-password"
                spellCheck={false}
                className="bg-[#171a2d] border border-[#26293c] focus:border-[#ffd580] rounded px-2 py-1 text-xs text-[#ffd580] outline-none"
              />
              <span className="text-[10px] text-[#8d909b]">Never stored or logged.</span>
            </label>
          </div>

          {formError && (
            <div className="text-[11px] text-[#ffb4ab] flex items-center gap-1.5">
              <span className="material-symbols-outlined text-[14px]">error</span>
              <span>{formError}</span>
            </div>
          )}

          <div className="flex items-center justify-end gap-2">
            <button
              type="button"
              onClick={closeEditor}
              disabled={busy}
              className="px-2.5 py-1 rounded bg-[#1b1e31] text-[#c3c6d2] hover:bg-[#26293c] cursor-pointer disabled:opacity-50"
            >
              Cancel
            </button>
            <button
              type="button"
              onClick={submitEditor}
              disabled={busy}
              className="px-3 py-1 rounded bg-[#b0c9ff] text-[#002e68] font-bold hover:bg-[#d8e2ff] cursor-pointer disabled:opacity-50"
            >
              {busy ? 'Encrypting…' : editor.mode === 'create' ? 'Encrypt & Store' : 'Save Changes'}
            </button>
          </div>
        </div>
      )}

      {/* Variable table */}
      <div className="rounded-lg bg-[#121528] border border-[#26293c] shadow-md overflow-hidden">
        <table className="w-full border-collapse text-left">
          <thead>
            <tr className="bg-[#171a2d] text-[#8d909b] text-[10px] uppercase tracking-wider border-b border-[#26293c]">
              <th className="py-2 px-3 font-semibold">Variable</th>
              <th className="py-2 px-3 font-semibold">Value</th>
              <th className="py-2 px-3 font-semibold w-44">Last updated</th>
              <th className="py-2 px-3 font-semibold w-32 text-right">Actions</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-[#1f2238]">
            {loading && variables.length === 0 && (
              <tr>
                <td colSpan={4} className="py-6 px-3 text-center text-[#8d909b]">
                  Loading vault variables…
                </td>
              </tr>
            )}

            {!loading && variables.length === 0 && (
              <tr>
                <td colSpan={4} className="py-8 px-3 text-center text-[#8d909b]">
                  <span className="material-symbols-outlined text-3xl text-[#313447] block mb-1">
                    vpn_key_off
                  </span>
                  No vault variables stored yet. Add one to make it available to playbooks.
                </td>
              </tr>
            )}

            {variables.map(variable => {
              const masked = variable.maskedValue || maskVaultValue(variable.name);
              const isPendingDelete = pendingDelete === variable.name;

              return (
                <tr
                  key={variable.name}
                  className="hover:bg-[#15182b] transition-colors"
                  data-testid="vault-variable-row"
                >
                  <td className="py-2 px-3 text-[#b0c9ff] font-bold whitespace-nowrap">
                    <span className="material-symbols-outlined text-[14px] text-[#d9b9ff] align-middle mr-1.5">
                      key
                    </span>
                    {variable.name}
                  </td>
                  <td className="py-2 px-3">
                    <span
                      className="text-[#8d909b] select-none"
                      data-testid="vault-masked-value"
                      title="The stored value is never sent to the browser"
                    >
                      {masked}
                    </span>
                  </td>
                  <td className="py-2 px-3 text-[#8d909b] whitespace-nowrap">
                    {formatTimestamp(variable.updatedAt)}
                  </td>
                  <td className="py-2 px-3 text-right whitespace-nowrap">
                    {isPendingDelete ? (
                      <span className="inline-flex items-center gap-1">
                        <span className="text-[10px] text-[#ffb4ab]">Delete?</span>
                        <button
                          type="button"
                          onClick={() => confirmDelete(variable.name)}
                          disabled={busy}
                          className="px-1.5 py-0.5 rounded bg-[#ffb4ab]/15 text-[#ffb4ab] border border-[#ffb4ab]/40 hover:bg-[#ffb4ab]/25 cursor-pointer disabled:opacity-50"
                        >
                          Confirm
                        </button>
                        <button
                          type="button"
                          onClick={() => setPendingDelete(null)}
                          disabled={busy}
                          className="px-1.5 py-0.5 rounded text-[#8d909b] hover:text-[#dfe1fa] cursor-pointer disabled:opacity-50"
                        >
                          Cancel
                        </button>
                      </span>
                    ) : (
                      <span className="inline-flex items-center gap-1">
                        <button
                          type="button"
                          onClick={() => openEdit(variable)}
                          className="text-[#8d909b] hover:text-[#b0c9ff] p-1 rounded hover:bg-[#26293c] cursor-pointer"
                          title={`Edit ${variable.name}`}
                          aria-label={`Edit ${variable.name}`}
                        >
                          <span className="material-symbols-outlined text-[15px]">edit</span>
                        </button>
                        <button
                          type="button"
                          onClick={() => setPendingDelete(variable.name)}
                          className="text-[#8d909b] hover:text-[#ffb4ab] p-1 rounded hover:bg-[#ffb4ab]/10 cursor-pointer"
                          title={`Delete ${variable.name}`}
                          aria-label={`Delete ${variable.name}`}
                        >
                          <span className="material-symbols-outlined text-[15px]">delete</span>
                        </button>
                      </span>
                    )}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>

      <p className="text-[10px] text-[#8d909b] leading-relaxed">
        Playbook runs receive these variables as an ephemeral vault vars file
        (<span className="text-[#b0c9ff]">--extra-vars @vault.yml</span>) containing AES256 ciphertext only.
        Decryptions are recorded in the vault audit trail by variable name.
      </p>
    </div>
  );
};
