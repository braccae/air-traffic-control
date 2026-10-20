import React, { useCallback, useEffect, useMemo, useState } from 'react';
import { PlaybookYamlEditor } from './PlaybookYamlEditor.tsx';
import { PlaybookVisualEditor } from './PlaybookVisualEditor.tsx';
import { describeVisualEditorSupport, serializePlaybook } from '../utils/playbookVisualModel.ts';

/**
 * Full page editor for Ansible playbooks.
 *
 * Every read and write talks to the coordinator's real playbook repository:
 *   GET    /api/playbooks                -> { playbooks: string[] }
 *   GET    /api/playbooks/:name          -> { success, name, content }
 *   POST   /api/playbooks                -> { success, name, content }   (rejects duplicates)
 *   PUT    /api/playbooks/:name          -> { success, name, content }
 *   DELETE /api/playbooks/:name          -> { success, name }            (site.yml is protected)
 *   POST   /api/playbook/syntax-check    -> ansible-playbook --syntax-check output
 *   POST   /api/playbooks/:name/queue    -> stages a run on the on-the-fly queue
 *
 * Runs go through the on-the-fly Ansible queue (ansible_queue_tasks) with
 * ANSIBLE_STDOUT_CALLBACK=json, never through a client-side simulation.
 * Zero-Mock Policy (AGENTS.md §2): when the repository cannot be read the page
 * reports the failure instead of showing invented playbooks.
 */

const PROTECTED_PLAYBOOKS = new Set(['site.yml', 'site.yaml']);

export interface PlaybookRunResult {
  success: boolean;
  taskId?: string;
  changed?: boolean;
  failed?: boolean;
  error?: string;
  durationMs?: number;
  jsonOutput?: {
    stats?: Record<string, { ok?: number; changed?: number; failures?: number; unreachable?: number; skipped?: number }>;
    plays?: Array<{ play?: { name?: string } }>;
  } | null;
}

export interface PlaybookQueueResponse {
  success: boolean;
  task?: { id: string; title: string; status: string };
  result?: PlaybookRunResult | null;
  remainingCount?: number;
  haltedOnError?: boolean;
  error?: string;
}

export interface DatacenterPlaybooksPageProps {
  /**
   * When false the page omits its own title block. Used when the settings shell
   * already renders the page header, so the setting is not labelled twice.
   */
  showHeader?: boolean;
  /** Called after a run task has been staged/executed so a host can refresh the queue. */
  onQueueChanged?: () => void;
}

export function isProtectedPlaybookName(name: string): boolean {
  return PROTECTED_PLAYBOOKS.has(name);
}

/** Accepts `name` or `name.yml`; the coordinator appends the extension itself. */
export function normalizeNewPlaybookName(raw: string): string {
  const trimmed = raw.trim();
  if (!trimmed) return '';
  if (trimmed.endsWith('.yml') || trimmed.endsWith('.yaml')) return trimmed;
  return `${trimmed}.yml`;
}

export function isValidNewPlaybookName(name: string): boolean {
  return /^[A-Za-z0-9_.-]+\.ya?ml$/.test(name) && !name.includes('..') && !name.startsWith('.');
}

export const DatacenterPlaybooksPage: React.FC<DatacenterPlaybooksPageProps> = ({
  showHeader = true,
  onQueueChanged,
}) => {
  const [playbooks, setPlaybooks] = useState<string[] | null>(null);
  const [selected, setSelected] = useState<string | null>(null);
  const [content, setContent] = useState('');
  const [originalContent, setOriginalContent] = useState('');
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const [savedAt, setSavedAt] = useState<number | null>(null);

  const [isCreating, setIsCreating] = useState(false);
  const [newName, setNewName] = useState('');
  const [createError, setCreateError] = useState<string | null>(null);

  const [confirmDelete, setConfirmDelete] = useState<string | null>(null);

  const [checkingSyntax, setCheckingSyntax] = useState(false);
  const [syntaxResult, setSyntaxResult] = useState<{ success: boolean; message: string } | null>(null);

  const [runTarget, setRunTarget] = useState('all');
  const [runCheckMode, setRunCheckMode] = useState(false);
  const [runTags, setRunTags] = useState('');
  const [running, setRunning] = useState<'queue' | 'now' | null>(null);
  const [runResponse, setRunResponse] = useState<PlaybookQueueResponse | null>(null);

  // Visual (field) mode always works on a document parsed by the coordinator's
  // real YAML parser, and hands the serialised text back to `content`, so both
  // modes edit exactly the same playbook text.
  const [mode, setMode] = useState<'yaml' | 'visual'>('yaml');
  const [visualDocument, setVisualDocument] = useState<any[] | null>(null);
  const [visualError, setVisualError] = useState<string | null>(null);
  const [parsingVisual, setParsingVisual] = useState(false);

  const isDirty = content !== originalContent;
  const protectedSelected = selected ? isProtectedPlaybookName(selected) : false;

  const loadContent = useCallback(async (name: string) => {
    setLoading(true);
    setSyntaxResult(null);
    setRunResponse(null);
    setConfirmDelete(null);
    try {
      const res = await fetch(`/api/playbooks/${encodeURIComponent(name)}`);
      const body = await res.json().catch(() => null);
      if (!res.ok || !body?.success) {
        throw new Error(body?.error || `GET /api/playbooks/${name} returned ${res.status} ${res.statusText}`);
      }
      const text = typeof body.content === 'string' ? body.content : '';
      setContent(text);
      setOriginalContent(text);
      setActionError(null);
    } catch (err: any) {
      setContent('');
      setOriginalContent('');
      setActionError(err?.message || String(err));
    } finally {
      setLoading(false);
    }
  }, []);

  const loadPlaybooks = useCallback(
    async (selectTarget?: string) => {
      setLoading(true);
      try {
        const res = await fetch('/api/playbooks');
        if (!res.ok) {
          throw new Error(`GET /api/playbooks returned ${res.status} ${res.statusText}`);
        }
        const body = await res.json();
        const list: string[] = Array.isArray(body?.playbooks) ? body.playbooks : [];
        setPlaybooks(list);
        setLoadError(null);

        const target =
          selectTarget && list.includes(selectTarget)
            ? selectTarget
            : selected && list.includes(selected)
            ? selected
            : list[0] ?? null;

        setSelected(target);
        if (target) {
          await loadContent(target);
        } else {
          setContent('');
          setOriginalContent('');
          setLoading(false);
        }
      } catch (err: any) {
        // Never fall back to a fabricated repository listing.
        setPlaybooks(null);
        setSelected(null);
        setContent('');
        setOriginalContent('');
        setLoadError(err?.message || String(err));
        setLoading(false);
      }
    },
    // `selected` intentionally omitted: only the initial/refreshed selection matters here.
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [loadContent]
  );

  useEffect(() => {
    void loadPlaybooks();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const sortedPlaybooks = useMemo(() => (playbooks ? [...playbooks].sort() : []), [playbooks]);

  const handleSelect = (name: string) => {
    if (name === selected) return;
    if (isDirty && !window.confirm(`Discard unsaved changes in "${selected}" and open "${name}"?`)) {
      return;
    }
    setSelected(name);
    setSavedAt(null);
    // The parsed document belongs to the previous file — drop it rather than
    // showing one playbook's fields over another playbook's text.
    setMode('yaml');
    setVisualDocument(null);
    setVisualError(null);
    void loadContent(name);
  };

  const handleSave = async () => {
    if (!selected || saving) return;
    setSaving(true);
    setActionError(null);
    try {
      const res = await fetch(`/api/playbooks/${encodeURIComponent(selected)}`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ content }),
      });
      const body = await res.json().catch(() => null);
      if (!res.ok || !body?.success) {
        throw new Error(body?.error || `PUT /api/playbooks/${selected} returned ${res.status} ${res.statusText}`);
      }
      // Re-read so the editor shows exactly what the coordinator persisted.
      await loadContent(selected);
      setSavedAt(Date.now());
    } catch (err: any) {
      setActionError(err?.message || String(err));
    } finally {
      setSaving(false);
    }
  };

  const handleCreate = async (e: React.FormEvent) => {
    e.preventDefault();
    const name = normalizeNewPlaybookName(newName);
    if (!isValidNewPlaybookName(name)) {
      setCreateError('Playbook name must be a *.yml / *.yaml filename (letters, numbers, - and _).');
      return;
    }
    if (playbooks && playbooks.includes(name)) {
      setCreateError(`${name} already exists.`);
      return;
    }
    setCreateError(null);
    try {
      const res = await fetch('/api/playbooks', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ name }),
      });
      const body = await res.json().catch(() => null);
      if (!res.ok || !body?.success) {
        throw new Error(body?.error || `POST /api/playbooks returned ${res.status} ${res.statusText}`);
      }
      setIsCreating(false);
      setNewName('');
      await loadPlaybooks(body.name || name);
    } catch (err: any) {
      setCreateError(err?.message || String(err));
    }
  };

  const handleDelete = async (name: string) => {
    setActionError(null);
    try {
      const res = await fetch(`/api/playbooks/${encodeURIComponent(name)}`, { method: 'DELETE' });
      const body = await res.json().catch(() => null);
      if (!res.ok || !body?.success) {
        throw new Error(body?.error || `DELETE /api/playbooks/${name} returned ${res.status} ${res.statusText}`);
      }
      setConfirmDelete(null);
      await loadPlaybooks();
    } catch (err: any) {
      setActionError(err?.message || String(err));
    }
  };

  /**
   * Switches to the field editor: the current text is parsed by the
   * coordinator (PyYAML) so the form shows the real document, and a playbook
   * the form cannot represent stays in YAML mode with the parser's own reason.
   */
  const openVisualMode = async () => {
    if (!selected || parsingVisual) return;
    setParsingVisual(true);
    setVisualError(null);
    try {
      const res = await fetch('/api/playbooks/parse', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ content }),
      });
      const body = await res.json().catch(() => null);
      if (!res.ok || !body?.success) {
        const location = body?.line ? ` (line ${body.line}${body.column ? `:${body.column}` : ''})` : '';
        throw new Error(`${body?.error || `Parse returned ${res.status}`}${location}`);
      }
      const unsupported = describeVisualEditorSupport(body.document);
      if (unsupported) {
        setVisualError(unsupported);
        return;
      }
      setVisualDocument(body.document as any[]);
      setMode('visual');
    } catch (err: any) {
      setVisualError(`Could not parse this playbook: ${err?.message || String(err)}`);
    } finally {
      setParsingVisual(false);
    }
  };

  /** Field edits arrive as a whole document; the text is rewritten from it. */
  const handleVisualDocumentChange = useCallback((next: any[]) => {
    setVisualDocument(next);
    setContent(serializePlaybook(next));
  }, []);

  const handleSyntaxCheck = async () => {
    if (!selected) return;
    setCheckingSyntax(true);
    setSyntaxResult(null);
    try {
      const res = await fetch('/api/playbook/syntax-check', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ playbook: selected, content }),
      });
      const body = await res.json().catch(() => null);
      if (res.ok && body?.success) {
        setSyntaxResult({ success: true, message: body.output || 'Syntax OK' });
      } else {
        setSyntaxResult({
          success: false,
          message: body?.error || `Syntax check returned ${res.status} ${res.statusText}`,
        });
      }
    } catch (err: any) {
      setSyntaxResult({ success: false, message: err?.message || String(err) });
    } finally {
      setCheckingSyntax(false);
    }
  };

  const handleRun = async (execute: boolean) => {
    if (!selected) return;
    if (isDirty) {
      setActionError('Save the playbook before running it — the queue executes the file on disk.');
      return;
    }
    setRunning(execute ? 'now' : 'queue');
    setActionError(null);
    setRunResponse(null);
    try {
      const res = await fetch(`/api/playbooks/${encodeURIComponent(selected)}/queue`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          target: runTarget.trim() || 'all',
          checkMode: runCheckMode,
          tags: runTags.trim() || undefined,
          execute,
        }),
      });
      const body = (await res.json().catch(() => null)) as PlaybookQueueResponse | null;
      if (!res.ok) {
        throw new Error(body?.error || `POST /api/playbooks/${selected}/queue returned ${res.status} ${res.statusText}`);
      }
      setRunResponse(body);
      if (onQueueChanged) onQueueChanged();
    } catch (err: any) {
      setActionError(err?.message || String(err));
    } finally {
      setRunning(null);
    }
  };

  const runSummary = (() => {
    if (!runResponse) return null;
    const result = runResponse.result;
    if (!result) {
      return `Staged on the queue as ${runResponse.task?.id ?? 'unknown task'} — status ${runResponse.task?.status ?? 'staged'}.`;
    }
    const hosts = result.jsonOutput?.stats ? Object.keys(result.jsonOutput.stats) : [];
    const changedHosts = result.jsonOutput?.stats
      ? hosts.filter(h => (result.jsonOutput?.stats?.[h]?.changed ?? 0) > 0)
      : [];
    const failedHosts = result.jsonOutput?.stats
      ? hosts.filter(
          h =>
            (result.jsonOutput?.stats?.[h]?.failures ?? 0) > 0 ||
            (result.jsonOutput?.stats?.[h]?.unreachable ?? 0) > 0
        )
      : [];
    return [
      result.success ? 'Run succeeded' : 'Run failed',
      result.failed ? 'failed' : null,
      `${hosts.length} host(s) in recap`,
      changedHosts.length ? `changed: ${changedHosts.join(', ')}` : 'no changes',
      failedHosts.length ? `failed: ${failedHosts.join(', ')}` : null,
      typeof result.durationMs === 'number' ? `${result.durationMs}ms` : null,
    ]
      .filter(Boolean)
      .join(' · ');
  })();

  const footer = selected
    ? `${selected}${isDirty ? ' — unsaved changes' : ' — on disk'}`
    : 'No playbook selected';

  return (
    <div
      id="datacenter-playbook-editor"
      data-testid="datacenter-playbooks-page"
      className="rounded-lg bg-[#121528] border border-[#26293c] p-4 shadow-md flex flex-col gap-3"
    >
      {/* Header */}
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="flex items-center gap-2 flex-wrap">
          <span className="material-symbols-outlined text-[18px] text-[#b0c9ff]">menu_book</span>
          {showHeader && (
            <h2 className="text-xs font-mono font-bold text-[#dfe1fa] uppercase tracking-wider">
              Ansible Playbooks
            </h2>
          )}
          <span className="text-[10px] font-mono px-1.5 py-0.5 rounded bg-[#1b2a38] text-[#b0c9ff] border border-[#b0c9ff]/30">
            {playbooks ? `${playbooks.length} ${playbooks.length === 1 ? 'playbook' : 'playbooks'}` : 'repository unavailable'}
          </span>
          <span className="text-[10px] font-mono text-[#8d909b]">source: ansible/playbooks/</span>
        </div>

        <div className="flex items-center gap-2">
          <button
            type="button"
            onClick={() => {
              setIsCreating(v => !v);
              setCreateError(null);
            }}
            disabled={Boolean(loadError)}
            className="px-2.5 py-1 rounded text-xs font-mono text-[#8cd6cb] bg-[#171a2d] hover:bg-[#21263d] border border-[#26293c] hover:border-[#8cd6cb]/60 disabled:opacity-50 transition-colors flex items-center gap-1 cursor-pointer"
            title="Create a new playbook file in ansible/playbooks"
          >
            <span className="material-symbols-outlined text-[14px]">note_add</span>
            <span>New Playbook</span>
          </button>

          <button
            type="button"
            onClick={() => void handleSave()}
            disabled={!selected || !isDirty || saving}
            className="px-3 py-1 rounded text-xs font-mono font-bold text-[#121528] bg-[#b0c9ff] hover:bg-[#d8e2ff] disabled:opacity-40 disabled:cursor-not-allowed transition-colors flex items-center gap-1.5 cursor-pointer"
            title="Persist the selected playbook through PUT /api/playbooks/:name"
          >
            <span className="material-symbols-outlined text-[15px]">{saving ? 'sync' : 'save'}</span>
            <span>{saving ? 'Saving...' : 'Save'}</span>
          </button>
        </div>
      </div>

      {showHeader && (
        <p className="text-[11px] text-[#8d909b] font-mono">
          Playbooks are real YAML files in <code className="text-[#8cd6cb]">ansible/playbooks/</code>. Saving writes the
          file; running stages it on the on-the-fly Ansible queue with the JSON stdout callback.
        </p>
      )}

      {/* New playbook form */}
      {isCreating && (
        <form
          onSubmit={handleCreate}
          className="flex flex-wrap items-center gap-2 bg-[#171a2d] border border-[#26293c] rounded p-2 font-mono text-xs"
        >
          <span className="text-[#8d909b]">Filename</span>
          <input
            type="text"
            value={newName}
            onChange={e => setNewName(e.target.value)}
            placeholder="wireguard-restart.yml"
            className="flex-1 min-w-[220px] bg-[#121528] border border-[#26293c] focus:border-[#b0c9ff] rounded px-2 py-1 text-xs font-mono text-[#dfe1fa] placeholder-[#8d909b] outline-none"
          />
          <button
            type="submit"
            className="px-2.5 py-1 rounded bg-[#1b2a38] text-[#8cd6cb] border border-[#8cd6cb]/30 hover:border-[#8cd6cb] cursor-pointer"
          >
            Create
          </button>
          <button
            type="button"
            onClick={() => {
              setIsCreating(false);
              setNewName('');
              setCreateError(null);
            }}
            className="px-2 py-1 rounded text-[#8d909b] hover:text-[#dfe1fa] cursor-pointer"
          >
            Cancel
          </button>
          {createError && <span className="text-[#ffb4ab]">{createError}</span>}
        </form>
      )}

      {/* Real failures are surfaced, never masked */}
      {loadError && (
        <div className="p-2.5 rounded text-xs font-mono border bg-[#311c1c] text-[#ffb4ab] border-[#ffb4ab]/30 flex items-center justify-between gap-3">
          <span className="flex items-center gap-1.5">
            <span className="material-symbols-outlined text-[15px]">error</span>
            <span>Could not read the playbook repository: {loadError}</span>
          </span>
          <button
            type="button"
            onClick={() => void loadPlaybooks()}
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

      {visualError && (
        <div className="p-2.5 rounded text-xs font-mono border bg-[#2b2416] text-[#ffd580] border-[#ffd580]/30 flex items-start justify-between gap-3">
          <span className="flex items-start gap-1.5">
            <span className="material-symbols-outlined text-[15px]">info</span>
            <span>Visual editor unavailable for this playbook: {visualError}</span>
          </span>
          <button
            type="button"
            onClick={() => setVisualError(null)}
            className="text-current hover:opacity-80 cursor-pointer"
            title="Dismiss"
          >
            <span className="material-symbols-outlined text-[14px]">close</span>
          </button>
        </div>
      )}

      {syntaxResult && (
        <div
          className={`p-2.5 rounded text-xs font-mono border flex items-start justify-between gap-3 ${
            syntaxResult.success
              ? 'bg-[#162620] text-[#8cd6cb] border-[#8cd6cb]/30'
              : 'bg-[#311c1c] text-[#ffb4ab] border-[#ffb4ab]/30'
          }`}
        >
          <div className="flex items-start gap-2 min-w-0">
            <span className="material-symbols-outlined text-[16px]">
              {syntaxResult.success ? 'check_circle' : 'error'}
            </span>
            <div className="min-w-0">
              <div className="font-bold">
                {syntaxResult.success ? 'Syntax check succeeded' : 'Syntax check failed'}
              </div>
              <pre className="text-[11px] whitespace-pre-wrap max-h-24 overflow-y-auto opacity-90">
                {syntaxResult.message}
              </pre>
            </div>
          </div>
          <button
            type="button"
            onClick={() => setSyntaxResult(null)}
            className="text-current hover:opacity-80 cursor-pointer"
            title="Dismiss"
          >
            <span className="material-symbols-outlined text-[14px]">close</span>
          </button>
        </div>
      )}

      {runResponse && (
        <div
          className={`p-2.5 rounded text-xs font-mono border flex flex-col gap-1 ${
            runResponse.result
              ? runResponse.result.success
                ? 'bg-[#162620] text-[#8cd6cb] border-[#8cd6cb]/30'
                : 'bg-[#311c1c] text-[#ffb4ab] border-[#ffb4ab]/30'
              : 'bg-[#1b2a38] text-[#79c0ff] border-[#79c0ff]/30'
          }`}
        >
          <div className="flex items-center justify-between gap-3">
            <span className="flex items-center gap-1.5">
              <span className="material-symbols-outlined text-[15px]">queue</span>
              <span>{runSummary}</span>
            </span>
            <button
              type="button"
              onClick={() => setRunResponse(null)}
              className="hover:opacity-80 cursor-pointer"
              title="Dismiss"
            >
              <span className="material-symbols-outlined text-[14px]">close</span>
            </button>
          </div>
          {runResponse.result?.error && <div className="opacity-90">error: {runResponse.result.error}</div>}
        </div>
      )}

      {/* Body */}
      <div className="flex flex-col lg:flex-row gap-3 min-h-[420px]">
        {/* Playbook list */}
        <div className="lg:w-64 shrink-0 rounded border border-[#26293c] bg-[#0f1224] flex flex-col overflow-hidden">
          <div className="px-2.5 py-1.5 bg-[#171a2d] border-b border-[#26293c] text-[10px] font-mono uppercase tracking-wider text-[#8d909b]">
            Playbooks
          </div>
          <div className="flex-1 overflow-auto">
            {loading && !playbooks ? (
              <div className="p-3 text-xs font-mono text-[#8d909b]">Reading playbook repository…</div>
            ) : sortedPlaybooks.length === 0 ? (
              <div className="p-3 text-xs font-mono text-[#8d909b]">
                {loadError ? 'Repository unavailable.' : 'No playbooks in ansible/playbooks yet.'}
              </div>
            ) : (
              <ul className="divide-y divide-[#1f2238]">
                {sortedPlaybooks.map(name => {
                  const active = name === selected;
                  const dirty = active && isDirty;
                  const isProtected = isProtectedPlaybookName(name);
                  return (
                    <li
                      key={name}
                      className={`px-2 py-1.5 flex items-center gap-1 ${active ? 'bg-[#1b2136]' : 'hover:bg-[#15182b]'}`}
                    >
                      <button
                        type="button"
                        onClick={() => handleSelect(name)}
                        className={`flex-1 text-left font-mono text-[11px] truncate cursor-pointer ${
                          active ? 'text-[#b0c9ff] font-bold' : 'text-[#c3c6d2]'
                        }`}
                        title={name}
                        data-playbook-name={name}
                      >
                        {name}
                        {dirty && <span className="ml-1 text-[#ffd580]">*</span>}
                        {isProtected && (
                          <span className="ml-1.5 text-[9px] uppercase px-1 py-0.2 rounded bg-[#26293c] text-[#8cd6cb] border border-[#313447]">
                            Root
                          </span>
                        )}
                      </button>

                      {isProtected ? null : confirmDelete === name ? (
                        <span className="flex items-center gap-1">
                          <button
                            type="button"
                            onClick={() => void handleDelete(name)}
                            className="px-1 py-0.5 rounded text-[10px] bg-[#ffb4ab] text-[#121528] font-bold cursor-pointer"
                            title={`Delete ${name} from ansible/playbooks`}
                          >
                            Delete
                          </button>
                          <button
                            type="button"
                            onClick={() => setConfirmDelete(null)}
                            className="px-1 py-0.5 rounded text-[10px] text-[#8d909b] hover:text-[#dfe1fa] cursor-pointer"
                          >
                            No
                          </button>
                        </span>
                      ) : (
                        <button
                          type="button"
                          onClick={() => setConfirmDelete(name)}
                          className="text-[#8d909b] hover:text-[#ffb4ab] p-0.5 cursor-pointer"
                          title={`Remove ${name}`}
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

        {/* Editor + run controls */}
        <div className="flex-1 min-w-0 rounded border border-[#26293c] overflow-hidden flex flex-col">
          <div className="h-9 bg-[#171a2d] px-3 flex flex-wrap items-center justify-between gap-2 border-b border-[#26293c] font-mono text-xs">
            <div className="flex items-center gap-2">
              <span className="font-bold text-[#dfe1fa]">{selected ?? 'no playbook'}</span>
              {selected &&
                (isDirty ? (
                  <span className="text-[10px] text-[#ffd580] bg-[#ffd580]/10 px-1.5 py-0.5 rounded border border-[#ffd580]/20">
                    Unsaved Changes
                  </span>
                ) : (
                  <span className="text-[10px] text-[#8cd6cb] bg-[#8cd6cb]/10 px-1.5 py-0.5 rounded border border-[#8cd6cb]/20">
                    {savedAt ? 'Saved to disk' : 'On disk'}
                  </span>
                ))}
              {protectedSelected && (
                <span className="text-[10px] text-[#8d909b] bg-[#121528] px-1.5 py-0.5 rounded border border-[#26293c]">
                  Protected
                </span>
              )}
            </div>

            <div className="flex items-center gap-1.5">
              <div
                className="flex items-center rounded border border-[#26293c] overflow-hidden"
                role="group"
                aria-label="Editor mode"
                data-testid="playbook-editor-mode"
              >
                <button
                  type="button"
                  onClick={() => setMode('yaml')}
                  disabled={!selected}
                  className={`px-2 py-0.5 text-[11px] font-mono disabled:opacity-50 cursor-pointer ${
                    mode === 'yaml' ? 'bg-[#1b2136] text-[#dfe1fa] font-bold' : 'text-[#8d909b] hover:text-[#dfe1fa]'
                  }`}
                  title="Edit the playbook as YAML text"
                  data-mode="yaml"
                >
                  YAML
                </button>
                <button
                  type="button"
                  onClick={() => {
                    if (mode === 'visual') return;
                    void openVisualMode();
                  }}
                  disabled={!selected || parsingVisual}
                  className={`px-2 py-0.5 text-[11px] font-mono border-l border-[#26293c] disabled:opacity-50 cursor-pointer flex items-center gap-1 ${
                    mode === 'visual' ? 'bg-[#1b2136] text-[#dfe1fa] font-bold' : 'text-[#8d909b] hover:text-[#dfe1fa]'
                  }`}
                  title="Edit the playbook as typed fields sourced from ansible-doc"
                  data-mode="visual"
                >
                  <span className={`material-symbols-outlined text-[13px] ${parsingVisual ? 'animate-spin' : ''}`}>
                    {parsingVisual ? 'progress_activity' : 'edit_note'}
                  </span>
                  <span>Fields</span>
                </button>
              </div>

              <button
                type="button"
                onClick={() => void handleSyntaxCheck()}
                disabled={!selected || checkingSyntax || loading}
                className="flex items-center gap-1 px-2 py-0.5 rounded text-[11px] bg-[#1b1e31] hover:bg-[#26293c] text-[#8cd6cb] border border-[#26293c] disabled:opacity-50 cursor-pointer"
                title="ansible-playbook --syntax-check against the inventory"
              >
                <span className={`material-symbols-outlined text-[14px] ${checkingSyntax ? 'animate-spin' : ''}`}>
                  {checkingSyntax ? 'progress_activity' : 'check_circle'}
                </span>
                <span>{checkingSyntax ? 'Checking...' : 'Check Syntax'}</span>
              </button>

              <button
                type="button"
                onClick={() => void handleRun(false)}
                disabled={!selected || running !== null || isDirty}
                className="flex items-center gap-1 px-2 py-0.5 rounded text-[11px] bg-[#1b1e31] hover:bg-[#26293c] text-[#b0c9ff] border border-[#26293c] disabled:opacity-50 cursor-pointer"
                title="Stage this playbook on the on-the-fly Ansible queue"
              >
                <span className="material-symbols-outlined text-[14px]">queue</span>
                <span>Queue Run</span>
              </button>

              <button
                type="button"
                onClick={() => void handleRun(true)}
                disabled={!selected || running !== null || isDirty}
                className="flex items-center gap-1 px-2 py-0.5 rounded text-[11px] bg-[#b0c9ff] text-[#002e68] font-bold hover:bg-[#d8e2ff] disabled:opacity-50 cursor-pointer"
                title="Stage on the queue and execute it now"
              >
                <span className={`material-symbols-outlined text-[14px] ${running === 'now' ? 'animate-spin' : ''}`}>
                  {running === 'now' ? 'progress_activity' : 'play_arrow'}
                </span>
                <span>{running === 'now' ? 'Running...' : 'Run Now'}</span>
              </button>
            </div>
          </div>

          {/* Run parameters */}
          <div className="flex flex-wrap items-center gap-2 px-3 py-1.5 bg-[#0f1224] border-b border-[#26293c] font-mono text-[11px] text-[#8d909b]">
            <label className="flex items-center gap-1">
              <span>Limit</span>
              <input
                type="text"
                value={runTarget}
                onChange={e => setRunTarget(e.target.value)}
                placeholder="all"
                className="w-36 bg-[#121528] border border-[#26293c] focus:border-[#b0c9ff] rounded px-1.5 py-0.5 text-[11px] font-mono text-[#dfe1fa] placeholder-[#8d909b] outline-none"
              />
            </label>
            <label className="flex items-center gap-1">
              <span>Tags</span>
              <input
                type="text"
                value={runTags}
                onChange={e => setRunTags(e.target.value)}
                placeholder="(none)"
                className="w-32 bg-[#121528] border border-[#26293c] focus:border-[#b0c9ff] rounded px-1.5 py-0.5 text-[11px] font-mono text-[#dfe1fa] placeholder-[#8d909b] outline-none"
              />
            </label>
            <label className="inline-flex items-center gap-1.5 cursor-pointer">
              <input
                type="checkbox"
                checked={runCheckMode}
                onChange={e => setRunCheckMode(e.target.checked)}
                className="rounded bg-[#121528] border-[#26293c] text-[#1f6feb] focus:ring-0 cursor-pointer"
              />
              <span>--check (dry run)</span>
            </label>
            <span className="text-[10px] text-[#4e526b]">runs via ansible_queue_tasks (JSON stdout callback)</span>
          </div>

          {loading ? (
            <div className="flex-1 flex items-center justify-center font-mono text-xs text-[#8d909b] gap-2 py-10">
              <span className="material-symbols-outlined animate-spin text-[18px]">progress_activity</span>
              <span>Loading playbook…</span>
            </div>
          ) : selected && mode === 'visual' && visualDocument ? (
            <PlaybookVisualEditor document={visualDocument} onChange={handleVisualDocumentChange} />
          ) : selected ? (
            <PlaybookYamlEditor value={content} onChange={setContent} />
          ) : (
            <div className="flex-1 flex flex-col items-center justify-center gap-2 text-[#8d909b] font-mono text-xs py-12">
              <span className="material-symbols-outlined text-3xl text-[#313447]">menu_book</span>
              <span>
                {loadError
                  ? 'Playbook repository unreachable — nothing to display.'
                  : 'Select a playbook, or create one with New Playbook.'}
              </span>
            </div>
          )}
        </div>
      </div>

      {/* Footer status */}
      <div className="h-7 bg-[#121528] px-1 flex items-center justify-between font-mono text-[10px] text-[#8d909b]">
        <span>{footer}</span>
        <span>
          {content ? `${content.split('\n').length} lines · ${content.length} chars` : 'Writes go to /api/playbooks'}
        </span>
      </div>
    </div>
  );
};
