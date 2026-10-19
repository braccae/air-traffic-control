import React, { useState, useEffect, useRef } from 'react';

interface PlaybookEditorModalProps {
  isOpen: boolean;
  onClose: () => void;
  initialPlaybook?: string;
  onRunPlaybook?: (playbook: string) => void;
}

export const PlaybookEditorModal: React.FC<PlaybookEditorModalProps> = ({
  isOpen,
  onClose,
  initialPlaybook,
  onRunPlaybook,
}) => {
  const [playbooks, setPlaybooks] = useState<string[]>([]);
  const [selectedPlaybook, setSelectedPlaybook] = useState<string>('site.yml');
  const [content, setContent] = useState<string>('');
  const [originalContent, setOriginalContent] = useState<string>('');
  const [isLoading, setIsLoading] = useState<boolean>(false);
  const [isSaving, setIsSaving] = useState<boolean>(false);
  const [searchFilter, setSearchFilter] = useState<string>('');

  // Syntax check state
  const [checkingSyntax, setCheckingSyntax] = useState<boolean>(false);
  const [syntaxResult, setSyntaxResult] = useState<{ success: boolean; message: string } | null>(null);

  // Status message state
  const [statusMessage, setStatusMessage] = useState<{ type: 'success' | 'error'; text: string } | null>(null);

  // New playbook creation state
  const [isCreating, setIsCreating] = useState<boolean>(false);
  const [newPlaybookName, setNewPlaybookName] = useState<string>('');
  const [createError, setCreateError] = useState<string | null>(null);

  // Delete confirmation state
  const [isConfirmingDelete, setIsConfirmingDelete] = useState<boolean>(false);

  const textareaRef = useRef<HTMLTextAreaElement>(null);
  const lineGutterRef = useRef<HTMLDivElement>(null);

  const isDirty = content !== originalContent;
  const isProtected = selectedPlaybook === 'site.yml' || selectedPlaybook === 'site.yaml';

  // Fetch playbooks list
  const loadPlaybooks = async (selectTarget?: string) => {
    try {
      const res = await fetch('/api/playbooks');
      const data = await res.json();
      if (Array.isArray(data?.playbooks)) {
        setPlaybooks(data.playbooks);
        const target = selectTarget || (data.playbooks.includes(selectedPlaybook) ? selectedPlaybook : data.playbooks[0] || 'site.yml');
        if (target) {
          setSelectedPlaybook(target);
          loadPlaybookContent(target);
        }
      }
    } catch {
      setStatusMessage({ type: 'error', text: 'Failed to load playbooks list' });
    }
  };

  // Fetch individual playbook content
  const loadPlaybookContent = async (name: string) => {
    setIsLoading(true);
    setSyntaxResult(null);
    setStatusMessage(null);
    setIsConfirmingDelete(false);
    try {
      const res = await fetch(`/api/playbooks/${encodeURIComponent(name)}`);
      const data = await res.json();
      if (res.ok && data.success) {
        setContent(data.content);
        setOriginalContent(data.content);
      } else {
        setStatusMessage({ type: 'error', text: data.error || 'Failed to read playbook content' });
      }
    } catch {
      setStatusMessage({ type: 'error', text: `Failed to load ${name}` });
    } finally {
      setIsLoading(false);
    }
  };

  useEffect(() => {
    if (isOpen) {
      loadPlaybooks(initialPlaybook);
    }
  }, [isOpen, initialPlaybook]);

  // Keep line gutter scrolled in sync with textarea
  const handleScroll = () => {
    if (textareaRef.current && lineGutterRef.current) {
      lineGutterRef.current.scrollTop = textareaRef.current.scrollTop;
    }
  };

  // Support Tab key for 2-space indentation and Ctrl+S for saving
  const handleKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if ((e.ctrlKey || e.metaKey) && e.key === 's') {
      e.preventDefault();
      handleSave();
      return;
    }

    if (e.key === 'Tab') {
      e.preventDefault();
      const textarea = textareaRef.current;
      if (!textarea) return;

      const start = textarea.selectionStart;
      const end = textarea.selectionEnd;
      const val = textarea.value;

      const updated = val.substring(0, start) + '  ' + val.substring(end);
      setContent(updated);

      requestAnimationFrame(() => {
        textarea.selectionStart = textarea.selectionEnd = start + 2;
      });
    }
  };

  const handleSelectPlaybook = (name: string) => {
    if (name === selectedPlaybook) return;
    if (isDirty) {
      const proceed = window.confirm(
        `You have unsaved changes in "${selectedPlaybook}". Discard changes and open "${name}"?`
      );
      if (!proceed) return;
    }
    setSelectedPlaybook(name);
    loadPlaybookContent(name);
  };

  const handleSave = async () => {
    if (!selectedPlaybook) return;
    setIsSaving(true);
    setStatusMessage(null);
    try {
      const res = await fetch(`/api/playbooks/${encodeURIComponent(selectedPlaybook)}`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ content }),
      });
      const data = await res.json();
      if (res.ok && data.success) {
        setOriginalContent(content);
        setStatusMessage({ type: 'success', text: `Saved "${selectedPlaybook}" successfully` });
        setTimeout(() => setStatusMessage(null), 3500);
      } else {
        setStatusMessage({ type: 'error', text: data.error || 'Failed to save playbook' });
      }
    } catch (err: any) {
      setStatusMessage({ type: 'error', text: err?.message || 'Network error while saving' });
    } finally {
      setIsSaving(false);
    }
  };

  const handleSyntaxCheck = async () => {
    setCheckingSyntax(true);
    setSyntaxResult(null);
    try {
      const res = await fetch('/api/playbook/syntax-check', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ playbook: selectedPlaybook, content }),
      });
      const data = await res.json();
      if (res.ok && data.success) {
        setSyntaxResult({
          success: true,
          message: data.output || 'Syntax OK: Playbook structure verified',
        });
      } else {
        setSyntaxResult({
          success: false,
          message: data.error || 'Syntax verification failed',
        });
      }
    } catch (err: any) {
      setSyntaxResult({
        success: false,
        message: err?.message || 'Connection error during syntax check',
      });
    } finally {
      setCheckingSyntax(false);
    }
  };

  const handleCreatePlaybook = async (e: React.FormEvent) => {
    e.preventDefault();
    let name = newPlaybookName.trim();
    if (!name) {
      setCreateError('Please enter a playbook filename');
      return;
    }
    if (!name.endsWith('.yml') && !name.endsWith('.yaml')) {
      name = `${name}.yml`;
    }

    try {
      const res = await fetch('/api/playbooks', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ name }),
      });
      const data = await res.json();
      if (res.ok && data.success) {
        setIsCreating(false);
        setNewPlaybookName('');
        setCreateError(null);
        await loadPlaybooks(data.name);
        setStatusMessage({ type: 'success', text: `Created playbook "${data.name}"` });
      } else {
        setCreateError(data.error || 'Failed to create playbook');
      }
    } catch (err: any) {
      setCreateError(err?.message || 'Error creating playbook');
    }
  };

  const handleDeletePlaybook = async () => {
    if (!selectedPlaybook || isProtected) return;
    try {
      const res = await fetch(`/api/playbooks/${encodeURIComponent(selectedPlaybook)}`, {
        method: 'DELETE',
      });
      const data = await res.json();
      if (res.ok && data.success) {
        setIsConfirmingDelete(false);
        setStatusMessage({ type: 'success', text: `Deleted playbook "${selectedPlaybook}"` });
        await loadPlaybooks('site.yml');
      } else {
        setStatusMessage({ type: 'error', text: data.error || 'Failed to delete playbook' });
      }
    } catch (err: any) {
      setStatusMessage({ type: 'error', text: err?.message || 'Error deleting playbook' });
    }
  };

  if (!isOpen) return null;

  const filteredPlaybooks = playbooks.filter(p =>
    p.toLowerCase().includes(searchFilter.toLowerCase())
  );

  const lineCount = content ? content.split('\n').length : 1;
  const lineNumbers = Array.from({ length: Math.max(lineCount, 1) }, (_, i) => i + 1);

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/75 backdrop-blur-xs p-4">
      <div className="w-full max-w-5xl h-[85vh] min-h-[550px] max-h-[850px] bg-[#0a0d1f] rounded-lg shadow-2xl border border-[#313447] flex flex-col overflow-hidden">
        {/* Header Bar */}
        <div className="h-10 bg-[#171a2d] px-4 flex items-center justify-between border-b border-[#26293c]">
          <div className="flex items-center gap-2.5">
            <span className="material-symbols-outlined text-[#b0c9ff] text-[20px]">
              menu_book
            </span>
            <div className="flex items-center gap-2">
              <span className="font-mono text-xs text-[#dfe1fa] font-bold">
                Ansible Playbook Manager &amp; YAML Editor
              </span>
              <span className="text-[10px] font-mono text-[#8d909b] bg-[#111425] px-2 py-0.5 rounded border border-[#26293c]">
                ansible/playbooks/
              </span>
            </div>
          </div>
          <button
            type="button"
            onClick={onClose}
            className="text-[#8d909b] hover:text-[#dfe1fa] p-1 cursor-pointer transition-colors"
            title="Close Editor"
          >
            <span className="material-symbols-outlined text-[18px]">close</span>
          </button>
        </div>

        {/* Main Body */}
        <div className="flex-1 flex overflow-hidden">
          {/* Left Sidebar */}
          <div className="w-64 border-r border-[#26293c] bg-[#111425] flex flex-col shrink-0">
            {/* Sidebar Controls */}
            <div className="p-3 border-b border-[#26293c] space-y-2">
              <div className="flex items-center justify-between">
                <span className="font-mono text-[11px] font-bold text-[#8d909b] uppercase tracking-wider">
                  Playbooks ({playbooks.length})
                </span>
                <button
                  type="button"
                  onClick={() => {
                    setIsCreating(!isCreating);
                    setCreateError(null);
                  }}
                  className="flex items-center gap-1 text-[11px] font-mono text-[#b0c9ff] hover:text-[#d8e2ff] cursor-pointer"
                >
                  <span className="material-symbols-outlined text-[14px]">
                    {isCreating ? 'close' : 'add'}
                  </span>
                  <span>{isCreating ? 'Cancel' : 'New'}</span>
                </button>
              </div>

              {/* Inline Create Form */}
              {isCreating && (
                <form onSubmit={handleCreatePlaybook} className="bg-[#1b1e31] p-2 rounded border border-[#313447] space-y-2">
                  <div className="text-[10px] text-[#dfe1fa] font-mono font-bold">New Playbook Name</div>
                  <input
                    type="text"
                    placeholder="e.g. wireguard-restart.yml"
                    value={newPlaybookName}
                    onChange={e => setNewPlaybookName(e.target.value)}
                    autoFocus
                    className="w-full bg-[#0a0d1f] border border-[#313447] rounded px-2 py-1 text-xs text-[#dfe1fa] font-mono outline-none focus:border-[#b0c9ff]"
                  />
                  {createError && (
                    <div className="text-[10px] text-[#ffb4ab] font-mono">{createError}</div>
                  )}
                  <div className="flex justify-end gap-1.5 pt-1">
                    <button
                      type="button"
                      onClick={() => setIsCreating(false)}
                      className="px-2 py-0.5 bg-[#26293c] text-[#c3c6d2] rounded text-[11px] font-mono hover:bg-[#313447]"
                    >
                      Cancel
                    </button>
                    <button
                      type="submit"
                      className="px-2.5 py-0.5 bg-[#b0c9ff] text-[#002e68] font-bold rounded text-[11px] font-mono hover:bg-[#d8e2ff]"
                    >
                      Create
                    </button>
                  </div>
                </form>
              )}

              {/* Search filter */}
              <div className="relative">
                <input
                  type="text"
                  placeholder="Filter playbooks..."
                  value={searchFilter}
                  onChange={e => setSearchFilter(e.target.value)}
                  className="w-full bg-[#1b1e31] border border-[#26293c] rounded px-2.5 py-1 text-[11px] text-[#dfe1fa] font-mono placeholder:text-[#8d909b] outline-none focus:border-[#b0c9ff]"
                />
                {searchFilter && (
                  <button
                    type="button"
                    onClick={() => setSearchFilter('')}
                    className="absolute right-2 top-1.5 text-[#8d909b] hover:text-[#dfe1fa]"
                  >
                    <span className="material-symbols-outlined text-[12px]">close</span>
                  </button>
                )}
              </div>
            </div>

            {/* Playbook List Items */}
            <div className="flex-1 overflow-y-auto p-2 space-y-1">
              {filteredPlaybooks.map(p => {
                const isSelected = p === selectedPlaybook;
                const isItemProtected = p === 'site.yml' || p === 'site.yaml';
                return (
                  <button
                    key={p}
                    type="button"
                    onClick={() => handleSelectPlaybook(p)}
                    className={`w-full text-left px-2.5 py-2 rounded font-mono text-xs flex items-center justify-between transition-colors cursor-pointer ${
                      isSelected
                        ? 'bg-[#26293c] text-[#dfe1fa] border-l-2 border-[#b0c9ff] font-bold shadow-sm'
                        : 'text-[#c3c6d2] hover:bg-[#1b1e31] hover:text-[#dfe1fa]'
                    }`}
                  >
                    <div className="flex items-center gap-2 truncate">
                      <span className="material-symbols-outlined text-[16px] text-[#b0c9ff] shrink-0">
                        description
                      </span>
                      <span className="truncate">{p}</span>
                    </div>
                    <div className="flex items-center gap-1 shrink-0 ml-1">
                      {isItemProtected && (
                        <span className="text-[9px] bg-[#26293c] border border-[#313447] text-[#8cd6cb] px-1 py-0.2 rounded uppercase">
                          Root
                        </span>
                      )}
                      {isSelected && isDirty && (
                        <span className="w-1.5 h-1.5 rounded-full bg-[#ffb776]" title="Unsaved changes" />
                      )}
                    </div>
                  </button>
                );
              })}
              {filteredPlaybooks.length === 0 && (
                <div className="text-center py-6 text-xs text-[#8d909b] font-mono">
                  No matching playbooks
                </div>
              )}
            </div>
          </div>

          {/* Right Editor Area */}
          <div className="flex-1 flex flex-col bg-[#0a0d1f] overflow-hidden">
            {/* Editor Action Toolbar */}
            <div className="h-11 bg-[#171a2d] px-3.5 flex items-center justify-between border-b border-[#26293c] font-mono text-xs">
              <div className="flex items-center gap-2">
                <span className="font-bold text-[#dfe1fa]">{selectedPlaybook}</span>
                {isDirty ? (
                  <span className="text-[10px] text-[#ffb776] bg-[#ffb776]/10 px-1.5 py-0.5 rounded border border-[#ffb776]/20">
                    Unsaved Changes *
                  </span>
                ) : (
                  <span className="text-[10px] text-[#8cd6cb] bg-[#8cd6cb]/10 px-1.5 py-0.5 rounded border border-[#8cd6cb]/20">
                    Saved
                  </span>
                )}
                {isProtected && (
                  <span className="text-[10px] text-[#8d909b] bg-[#111425] px-1.5 py-0.5 rounded border border-[#26293c]">
                    Protected
                  </span>
                )}
              </div>

              {/* Action Buttons */}
              <div className="flex items-center gap-2">
                {/* Syntax Check */}
                <button
                  type="button"
                  onClick={handleSyntaxCheck}
                  disabled={checkingSyntax}
                  className="flex items-center gap-1 px-2.5 py-1 bg-[#1b1e31] hover:bg-[#26293c] text-[#8cd6cb] border border-[#26293c] rounded font-mono text-xs cursor-pointer transition-colors disabled:opacity-50"
                  title="Check YAML syntax against inventory"
                >
                  <span className={`material-symbols-outlined text-[14px] ${checkingSyntax ? 'animate-spin' : ''}`}>
                    {checkingSyntax ? 'progress_activity' : 'check_circle'}
                  </span>
                  <span>{checkingSyntax ? 'Checking...' : 'Check Syntax'}</span>
                </button>

                {/* Save */}
                <button
                  type="button"
                  onClick={handleSave}
                  disabled={isSaving || !isDirty}
                  className={`flex items-center gap-1 px-3 py-1 rounded font-mono text-xs font-bold transition-colors cursor-pointer ${
                    isDirty
                      ? 'bg-[#b0c9ff] text-[#002e68] hover:bg-[#d8e2ff] shadow-sm'
                      : 'bg-[#1b1e31] text-[#8d909b] border border-[#26293c] cursor-not-allowed opacity-60'
                  }`}
                  title="Save changes (Ctrl+S)"
                >
                  <span className="material-symbols-outlined text-[14px]">save</span>
                  <span>{isSaving ? 'Saving...' : 'Save'}</span>
                </button>

                {/* Run Playbook */}
                {onRunPlaybook && (
                  <button
                    type="button"
                    onClick={() => onRunPlaybook(selectedPlaybook)}
                    className="flex items-center gap-1 px-2.5 py-1 bg-[#26293c] hover:bg-[#313447] text-[#dfe1fa] border border-[#313447] rounded font-mono text-xs cursor-pointer transition-colors"
                    title="Open Playbook Runner for this playbook"
                  >
                    <span className="material-symbols-outlined text-[14px] text-[#b0c9ff]">play_arrow</span>
                    <span>Run</span>
                  </button>
                )}

                {/* Delete button */}
                {!isProtected && (
                  <button
                    type="button"
                    onClick={() => setIsConfirmingDelete(true)}
                    className="flex items-center gap-1 px-2 py-1 text-[#ffb4ab] hover:bg-[#ffb4ab]/10 border border-[#ffb4ab]/20 rounded font-mono text-xs cursor-pointer transition-colors"
                    title="Delete playbook"
                  >
                    <span className="material-symbols-outlined text-[14px]">delete</span>
                    <span>Delete</span>
                  </button>
                )}
              </div>
            </div>

            {/* Notification / Status Feedback Banners */}
            {statusMessage && (
              <div
                className={`px-3.5 py-1.5 font-mono text-xs flex items-center justify-between border-b ${
                  statusMessage.type === 'success'
                    ? 'bg-[#8cd6cb]/10 text-[#8cd6cb] border-[#8cd6cb]/30'
                    : 'bg-[#ffb4ab]/10 text-[#ffb4ab] border-[#ffb4ab]/30'
                }`}
              >
                <span>{statusMessage.text}</span>
                <button
                  type="button"
                  onClick={() => setStatusMessage(null)}
                  className="hover:opacity-80"
                >
                  <span className="material-symbols-outlined text-[14px]">close</span>
                </button>
              </div>
            )}

            {/* Syntax Check Result Banner */}
            {syntaxResult && (
              <div
                className={`px-3.5 py-2 font-mono text-xs flex items-start justify-between border-b ${
                  syntaxResult.success
                    ? 'bg-[#8cd6cb]/10 text-[#8cd6cb] border-[#8cd6cb]/30'
                    : 'bg-[#ffb4ab]/10 text-[#ffb4ab] border-[#ffb4ab]/30'
                }`}
              >
                <div className="flex items-start gap-2">
                  <span className="material-symbols-outlined text-[16px] shrink-0 mt-0.5">
                    {syntaxResult.success ? 'check_circle' : 'error'}
                  </span>
                  <div className="space-y-0.5">
                    <div className="font-bold">
                      {syntaxResult.success ? 'Syntax Check Succeeded' : 'Syntax Check Failed'}
                    </div>
                    <pre className="text-[11px] whitespace-pre-wrap max-h-24 overflow-y-auto opacity-90">
                      {syntaxResult.message}
                    </pre>
                  </div>
                </div>
                <button
                  type="button"
                  onClick={() => setSyntaxResult(null)}
                  className="hover:opacity-80 ml-2"
                >
                  <span className="material-symbols-outlined text-[14px]">close</span>
                </button>
              </div>
            )}

            {/* Delete Confirmation Alert */}
            {isConfirmingDelete && (
              <div className="bg-[#ffb4ab]/10 border-b border-[#ffb4ab]/30 px-3.5 py-2.5 flex items-center justify-between font-mono text-xs">
                <div className="flex items-center gap-2 text-[#ffb4ab]">
                  <span className="material-symbols-outlined text-[16px]">warning</span>
                  <span>
                    Permanently delete <strong>{selectedPlaybook}</strong> from cluster repository?
                  </span>
                </div>
                <div className="flex items-center gap-2">
                  <button
                    type="button"
                    onClick={() => setIsConfirmingDelete(false)}
                    className="px-2.5 py-1 bg-[#1b1e31] hover:bg-[#26293c] text-[#c3c6d2] rounded cursor-pointer"
                  >
                    Cancel
                  </button>
                  <button
                    type="button"
                    onClick={handleDeletePlaybook}
                    className="px-2.5 py-1 bg-[#ffb4ab] hover:bg-[#ff897d] text-[#690005] font-bold rounded cursor-pointer"
                  >
                    Confirm Delete
                  </button>
                </div>
              </div>
            )}

            {/* Editor Textarea with Line Number Gutter */}
            <div className="flex-1 flex overflow-hidden relative">
              {isLoading ? (
                <div className="flex-1 flex items-center justify-center font-mono text-xs text-[#8d909b] gap-2">
                  <span className="material-symbols-outlined animate-spin text-[18px]">progress_activity</span>
                  <span>Loading playbook...</span>
                </div>
              ) : (
                <>
                  {/* Line Number Gutter */}
                  <div
                    ref={lineGutterRef}
                    className="w-12 bg-[#0c0f24] text-[#4e526b] font-mono text-xs leading-5 py-3 px-2 text-right select-none border-r border-[#26293c] overflow-hidden"
                  >
                    {lineNumbers.map(n => (
                      <div key={n}>{n}</div>
                    ))}
                  </div>

                  {/* Main YAML Code Textarea */}
                  <textarea
                    ref={textareaRef}
                    value={content}
                    onChange={e => setContent(e.target.value)}
                    onScroll={handleScroll}
                    onKeyDown={handleKeyDown}
                    placeholder="---&#10;- name: Sample Playbook&#10;  hosts: tag:host&#10;  tasks:&#10;    - ping:&#10;"
                    spellCheck={false}
                    className="flex-1 h-full bg-[#0a0d1f] text-[#dfe1fa] font-mono text-xs leading-5 p-3 outline-none resize-none overflow-y-auto selection:bg-[#26293c]"
                  />
                </>
              )}
            </div>

            {/* Editor Footer Status Bar */}
            <div className="h-7 bg-[#171a2d] px-3.5 flex items-center justify-between border-t border-[#26293c] font-mono text-[11px] text-[#8d909b]">
              <div className="flex items-center gap-3">
                <span>{lineCount} lines</span>
                <span>{content.length} chars</span>
                <span className="hidden md:inline">YAML Mode</span>
              </div>
              <div className="flex items-center gap-3">
                <span className="hidden sm:inline">Ctrl+S to save</span>
                <span className="hidden sm:inline">Tab = 2 spaces</span>
                <span className="text-[#8cd6cb]">leo-banjo.ts.net</span>
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>
  );
};
