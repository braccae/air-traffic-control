import React, { useState, useEffect } from 'react';
import {
  findDatacenterSettingCategory,
  findDatacenterSettingPage,
  formatDatacenterRoute,
  getDatacenterSettingPages,
  type DatacenterSettingPageId,
} from '../utils/navigationViewModel.ts';
import {
  loadQuickCommands,
  addQuickCommand,
  updateQuickCommand,
  deleteQuickCommand,
  resetQuickCommands,
  QUICK_COMMAND_EVENT,
  type QuickCommandItem,
  type QuickCommandCategory,
} from '../utils/quickCommandsManager.ts';
import type { AnsibleConfigSettings } from '../utils/ansibleConfigManager.ts';
import { DatacenterVarsPage } from './DatacenterVarsPage.tsx';
import { DatacenterPlaybooksPage } from './DatacenterPlaybooksPage.tsx';
import { RcloneRemotesView } from './RcloneRemotesView.tsx';

export interface DatacenterSettingsViewProps {
  page: DatacenterSettingPageId;
}

const QUICK_CMD_CATEGORY_COLORS: Record<QuickCommandCategory, { bg: string; text: string; border: string }> = {
  system: { bg: 'bg-[#1b2a38]', text: 'text-[#8cd6cb]', border: 'border-[#8cd6cb]/30' },
  containers: { bg: 'bg-[#291e3b]', text: 'text-[#d9b9ff]', border: 'border-[#d9b9ff]/30' },
  storage: { bg: 'bg-[#2d281b]', text: 'text-[#ffd580]', border: 'border-[#ffd580]/30' },
  network: { bg: 'bg-[#182030]', text: 'text-[#79c0ff]', border: 'border-[#79c0ff]/30' },
  custom: { bg: 'bg-[#21262d]', text: 'text-[#8d909b]', border: 'border-[#30363d]' },
};

const DEFAULT_ANSIBLE_CONFIG: AnsibleConfigSettings = {
  stdoutCallback: 'json',
  forks: 10,
  timeout: 30,
  hostKeyChecking: false,
  pipelining: true,
  becomeMethod: 'run0',
};

/** Page frame: breadcrumb, title, stable deep-link URL, and the single setting body. */
const SettingsPageFrame: React.FC<{
  page: DatacenterSettingPageId;
  actions?: React.ReactNode;
  children: React.ReactNode;
}> = ({ page, actions, children }) => {
  const meta = findDatacenterSettingPage(page) ?? getDatacenterSettingPages()[0];
  const category = findDatacenterSettingCategory(meta.category);
  const route = formatDatacenterRoute({ kind: 'setting', setting: meta.id });

  return (
    <div
      className="flex flex-col gap-3 w-full"
      data-testid="datacenter-settings-page"
      data-setting-id={meta.id}
    >
      <div className="rounded-lg bg-[#121528] border border-[#26293c] p-4 shadow-md flex flex-wrap items-center justify-between gap-3">
        <div className="flex items-center gap-3 min-w-0">
          <div className="w-10 h-10 rounded-lg bg-[#1b2a38] border border-[#8cd6cb]/40 flex items-center justify-center text-[#8cd6cb] shadow-sm shrink-0">
            <span className="material-symbols-outlined text-[22px]">{meta.icon}</span>
          </div>
          <div className="min-w-0">
            <div className="flex items-center gap-2 font-mono text-[10px] uppercase tracking-wider text-[#8d909b]">
              <span>Datacenter Settings</span>
              <span className="material-symbols-outlined text-[12px]">chevron_right</span>
              <span className="text-[#b0c9ff]">{category?.title ?? meta.category}</span>
            </div>
            <h1 className="text-lg font-bold font-mono text-[#f0f2ff] tracking-tight truncate">
              {meta.title}
            </h1>
            <p className="text-[11px] text-[#8d909b] font-mono mt-0.5">{meta.description}</p>
          </div>
        </div>
        <div className="flex items-center gap-2">
          {actions}
          <span
            className="px-2 py-1 rounded bg-[#171a2d] border border-[#26293c] font-mono text-[10px] text-[#8d909b]"
            title="Stable deep-link for this setting page"
          >
            {route}
          </span>
        </div>
      </div>

      {children}
    </div>
  );
};

const AnsibleSettingsPage: React.FC<{ page: DatacenterSettingPageId }> = ({ page }) => {
  const [config, setConfig] = useState<AnsibleConfigSettings>(DEFAULT_ANSIBLE_CONFIG);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [feedback, setFeedback] = useState<{ type: 'success' | 'error'; message: string } | null>(null);

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    fetch('/api/ansible/config')
      .then(res => res.json())
      .then(data => {
        if (!cancelled && data?.success && data?.settings) {
          setConfig(data.settings);
        }
      })
      .catch(err => {
        if (!cancelled) {
          setFeedback({ type: 'error', message: `Failed to load ansible.cfg: ${err?.message || err}` });
        }
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, []);

  const save = async () => {
    setSaving(true);
    setFeedback(null);
    try {
      const res = await fetch('/api/ansible/config', {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ settings: config }),
      });
      const data = await res.json();
      if (data?.success && data?.settings) {
        setConfig(data.settings);
        setFeedback({ type: 'success', message: 'ansible.cfg synchronized successfully to disk.' });
      } else {
        setFeedback({ type: 'error', message: data?.error || 'Failed to save ansible.cfg' });
      }
    } catch (err: any) {
      setFeedback({ type: 'error', message: err?.message || 'Network error saving ansible.cfg' });
    } finally {
      setSaving(false);
    }
  };

  const saveButton = (
    <button
      type="button"
      onClick={save}
      disabled={saving || loading}
      className="px-3 py-1 rounded text-xs font-mono font-bold text-white bg-[#1f6feb] hover:bg-[#388bfd] disabled:opacity-50 transition-colors flex items-center gap-1.5 cursor-pointer shadow-xs"
      title="Save and write updated settings to ansible/ansible.cfg"
    >
      <span className="material-symbols-outlined text-[15px]">{saving ? 'sync' : 'save'}</span>
      <span>{saving ? 'Syncing...' : 'Save & Sync ansible.cfg'}</span>
    </button>
  );

  const fieldCard = (children: React.ReactNode) => (
    <div
      id="datacenter-ansible-config"
      data-testid="datacenter-ansible-config-card"
      className="rounded-lg bg-[#121528] border border-[#26293c] p-4 shadow-md flex flex-col gap-3"
    >
      <p className="text-[11px] text-[#8d909b] font-mono">
        These values are written to <code className="text-[#8cd6cb] font-mono">ansible/ansible.cfg</code> on the
        coordinator and are applied to every on-the-fly and baseline playbook run.
      </p>

      {feedback && (
        <div
          className={`p-2.5 rounded text-xs font-mono border flex items-center justify-between ${
            feedback.type === 'success'
              ? 'bg-[#162620] text-[#8cd6cb] border-[#8cd6cb]/30'
              : 'bg-[#311c1c] text-[#ffb4ab] border-[#ffb4ab]/30'
          }`}
        >
          <div className="flex items-center gap-2">
            <span className="material-symbols-outlined text-[16px]">
              {feedback.type === 'success' ? 'check_circle' : 'error'}
            </span>
            <span>{feedback.message}</span>
          </div>
          <button
            type="button"
            onClick={() => setFeedback(null)}
            className="text-[#8d909b] hover:text-[#dfe1fa] cursor-pointer"
          >
            <span className="material-symbols-outlined text-[14px]">close</span>
          </button>
        </div>
      )}

      <div className="max-w-xl flex flex-col gap-3">{children}</div>

      <div className="flex items-center justify-between pt-1 border-t border-[#26293c]/60">
        <span className="text-[10px] font-mono text-[#8d909b]">
          {loading ? 'Reading ansible/ansible.cfg...' : 'Loaded from ansible/ansible.cfg'}
        </span>
        {saveButton}
      </div>
    </div>
  );

  switch (page) {
    case 'ansible-stdout-callback':
      return (
        <SettingsPageFrame page={page} actions={saveButton}>
          {fieldCard(
            <div className="p-3 rounded-lg bg-[#171a2d] border border-[#26293c] flex flex-col gap-1.5">
              <label className="text-xs font-mono font-bold text-[#dfe1fa] flex items-center justify-between">
                <span>
                  STDOUT Driver (<code className="text-[#79c0ff]">stdout_callback</code>)
                </span>
                <span className="text-[10px] text-[#8d909b] uppercase">Output</span>
              </label>
              <select
                value={config.stdoutCallback}
                onChange={e =>
                  setConfig(prev => ({
                    ...prev,
                    stdoutCallback: e.target.value as 'json' | 'yaml' | 'default',
                  }))
                }
                className="w-full bg-[#121528] border border-[#26293c] rounded px-2.5 py-1.5 text-xs font-mono text-[#dfe1fa] focus:border-[#79c0ff] focus:outline-hidden cursor-pointer"
              >
                <option value="json">json (Structured for Web UI)</option>
                <option value="yaml">yaml (Formatted CLI)</option>
                <option value="default">default (Standard prose)</option>
              </select>
              <span className="text-[10px] font-mono text-[#8d909b]">
                Driver used by the runner to format task payloads and the recap.
              </span>
            </div>
          )}
        </SettingsPageFrame>
      );

    case 'ansible-become-method':
      return (
        <SettingsPageFrame page={page} actions={saveButton}>
          {fieldCard(
            <div className="p-3 rounded-lg bg-[#171a2d] border border-[#26293c] flex flex-col gap-1.5">
              <label className="text-xs font-mono font-bold text-[#dfe1fa] flex items-center justify-between">
                <span>
                  Privilege Elevation (<code className="text-[#d9b9ff]">become_method</code>)
                </span>
                <span className="text-[10px] text-[#8d909b] uppercase">Security</span>
              </label>
              <select
                value={config.becomeMethod}
                onChange={e =>
                  setConfig(prev => ({
                    ...prev,
                    becomeMethod: e.target.value as 'run0' | 'sudo' | 'doas',
                  }))
                }
                className="w-full bg-[#121528] border border-[#26293c] rounded px-2.5 py-1.5 text-xs font-mono text-[#dfe1fa] focus:border-[#d9b9ff] focus:outline-hidden cursor-pointer"
              >
                <option value="run0">run0 (systemd 256+ polkit preferred)</option>
                <option value="sudo">sudo (Standard Linux fallback)</option>
                <option value="doas">doas (Lightweight BSD/Alpine)</option>
              </select>
              <span className="text-[10px] font-mono text-[#8d909b]">
                Method used to execute elevated commands on remote nodes.
              </span>
            </div>
          )}
        </SettingsPageFrame>
      );

    case 'ansible-forks':
      return (
        <SettingsPageFrame page={page} actions={saveButton}>
          {fieldCard(
            <div className="p-3 rounded-lg bg-[#171a2d] border border-[#26293c] flex flex-col gap-1.5">
              <label className="text-xs font-mono font-bold text-[#dfe1fa] flex items-center justify-between">
                <span>
                  Parallel Host Forks (<code className="text-[#ffd580]">forks</code>)
                </span>
                <span className="text-[10px] font-mono text-[#ffd580]">{config.forks} forks</span>
              </label>
              <input
                type="number"
                min={1}
                max={50}
                value={config.forks}
                onChange={e => {
                  const val = parseInt(e.target.value, 10);
                  setConfig(prev => ({
                    ...prev,
                    forks: isNaN(val) ? 1 : Math.max(1, Math.min(50, val)),
                  }));
                }}
                className="w-full bg-[#121528] border border-[#26293c] rounded px-2.5 py-1.5 text-xs font-mono text-[#dfe1fa] focus:border-[#ffd580] focus:outline-hidden"
              />
              <span className="text-[10px] font-mono text-[#8d909b]">
                Max parallel SSH worker processes (1 - 50).
              </span>
            </div>
          )}
        </SettingsPageFrame>
      );

    case 'ansible-timeout':
      return (
        <SettingsPageFrame page={page} actions={saveButton}>
          {fieldCard(
            <div className="p-3 rounded-lg bg-[#171a2d] border border-[#26293c] flex flex-col gap-1.5">
              <label className="text-xs font-mono font-bold text-[#dfe1fa] flex items-center justify-between">
                <span>
                  Connection Timeout (<code className="text-[#ffb4ab]">timeout</code>)
                </span>
                <span className="text-[10px] font-mono text-[#ffb4ab]">{config.timeout}s</span>
              </label>
              <input
                type="number"
                min={10}
                max={300}
                value={config.timeout}
                onChange={e => {
                  const val = parseInt(e.target.value, 10);
                  setConfig(prev => ({
                    ...prev,
                    timeout: isNaN(val) ? 10 : Math.max(10, Math.min(300, val)),
                  }));
                }}
                className="w-full bg-[#121528] border border-[#26293c] rounded px-2.5 py-1.5 text-xs font-mono text-[#dfe1fa] focus:border-[#ffb4ab] focus:outline-hidden"
              />
              <span className="text-[10px] font-mono text-[#8d909b]">
                Default SSH connection timeout in seconds (10 - 300).
              </span>
            </div>
          )}
        </SettingsPageFrame>
      );

    case 'ansible-ssh-transport':
      return (
        <SettingsPageFrame page={page} actions={saveButton}>
          {fieldCard(
            <div className="flex flex-col gap-3">
              <div className="p-3 rounded-lg bg-[#171a2d] border border-[#26293c] flex flex-col gap-2">
                <div className="text-xs font-mono font-bold text-[#dfe1fa] flex items-center justify-between">
                  <span>
                    Host Key Checking (<code className="text-[#8cd6cb]">host_key_checking</code>)
                  </span>
                  <span
                    className={`text-[10px] font-mono px-1.5 py-0.2 rounded ${
                      config.hostKeyChecking
                        ? 'bg-[#162620] text-[#8cd6cb] border border-[#8cd6cb]/30'
                        : 'bg-[#1b2a38] text-[#79c0ff] border border-[#79c0ff]/30'
                    }`}
                  >
                    {config.hostKeyChecking ? 'Strict' : 'Disabled'}
                  </span>
                </div>
                <p className="text-[10px] font-mono text-[#8d909b]">
                  Disable for zero-touch Tailscale node provisioning without host fingerprint prompts.
                </p>
                <label className="inline-flex items-center gap-2 cursor-pointer">
                  <input
                    type="checkbox"
                    checked={config.hostKeyChecking}
                    onChange={e =>
                      setConfig(prev => ({ ...prev, hostKeyChecking: e.target.checked }))
                    }
                    className="rounded bg-[#121528] border-[#26293c] text-[#1f6feb] focus:ring-0 cursor-pointer"
                  />
                  <span className="text-xs font-mono text-[#dfe1fa]">Enforce host key verification</span>
                </label>
              </div>

              <div className="p-3 rounded-lg bg-[#171a2d] border border-[#26293c] flex flex-col gap-2">
                <div className="text-xs font-mono font-bold text-[#dfe1fa] flex items-center justify-between">
                  <span>
                    SSH Pipelining (<code className="text-[#8cd6cb]">pipelining</code>)
                  </span>
                  <span
                    className={`text-[10px] font-mono px-1.5 py-0.2 rounded ${
                      config.pipelining
                        ? 'bg-[#162620] text-[#8cd6cb] border border-[#8cd6cb]/30'
                        : 'bg-[#291717] text-[#ffb4ab] border border-[#ffb4ab]/30'
                    }`}
                  >
                    {config.pipelining ? 'Enabled' : 'Disabled'}
                  </span>
                </div>
                <p className="text-[10px] font-mono text-[#8d909b]">
                  Pipes Python modules directly into the remote SSH session; drastically reduces network
                  roundtrips.
                </p>
                <label className="inline-flex items-center gap-2 cursor-pointer">
                  <input
                    type="checkbox"
                    checked={config.pipelining}
                    onChange={e => setConfig(prev => ({ ...prev, pipelining: e.target.checked }))}
                    className="rounded bg-[#121528] border-[#26293c] text-[#1f6feb] focus:ring-0 cursor-pointer"
                  />
                  <span className="text-xs font-mono text-[#dfe1fa]">Enable SSH pipelining</span>
                </label>
              </div>
            </div>
          )}
        </SettingsPageFrame>
      );

    default:
      return null;
  }
};

const QuickCommandsSettingsPage: React.FC<{ page: DatacenterSettingPageId }> = ({ page }) => {
  const [quickCommands, setQuickCommands] = useState<QuickCommandItem[]>(() => loadQuickCommands());
  const [editingCommand, setEditingCommand] = useState<QuickCommandItem | null>(null);
  const [isAddingCommand, setIsAddingCommand] = useState(false);
  const [formLabel, setFormLabel] = useState('');
  const [formCmd, setFormCmd] = useState('');
  const [formDescription, setFormDescription] = useState('');
  const [formCategory, setFormCategory] = useState<QuickCommandCategory>('custom');
  const [confirmReset, setConfirmReset] = useState(false);
  const [formError, setFormError] = useState('');

  useEffect(() => {
    const handleQuickCommandsUpdated = (event: Event) => {
      const customEvt = event as CustomEvent<QuickCommandItem[]>;
      if (customEvt.detail && Array.isArray(customEvt.detail)) {
        setQuickCommands(customEvt.detail);
      } else {
        setQuickCommands(loadQuickCommands());
      }
    };

    window.addEventListener(QUICK_COMMAND_EVENT, handleQuickCommandsUpdated);
    return () => {
      window.removeEventListener(QUICK_COMMAND_EVENT, handleQuickCommandsUpdated);
    };
  }, []);

  const handleOpenAdd = () => {
    setEditingCommand(null);
    setFormLabel('');
    setFormCmd('');
    setFormDescription('');
    setFormCategory('custom');
    setFormError('');
    setIsAddingCommand(true);
  };

  const handleOpenEdit = (cmd: QuickCommandItem) => {
    setIsAddingCommand(false);
    setEditingCommand(cmd);
    setFormLabel(cmd.label);
    setFormCmd(cmd.cmd);
    setFormDescription(cmd.description || '');
    setFormCategory(cmd.category || 'custom');
    setFormError('');
  };

  const handleCancelForm = () => {
    setEditingCommand(null);
    setIsAddingCommand(false);
    setFormError('');
  };

  const handleSaveForm = (e: React.FormEvent) => {
    e.preventDefault();
    const trimmedLabel = formLabel.trim();
    const trimmedCmd = formCmd.trim();
    if (!trimmedLabel || !trimmedCmd) {
      setFormError('Label and Command are required.');
      return;
    }

    if (editingCommand) {
      setQuickCommands(
        updateQuickCommand(editingCommand.id, {
          label: trimmedLabel,
          cmd: trimmedCmd,
          description: formDescription.trim() || undefined,
          category: formCategory,
        })
      );
      setEditingCommand(null);
    } else {
      setQuickCommands(
        addQuickCommand({
          label: trimmedLabel,
          cmd: trimmedCmd,
          description: formDescription.trim() || undefined,
          category: formCategory,
        })
      );
      setIsAddingCommand(false);
    }
    setFormError('');
  };

  const handleDeleteCommand = (id: string) => {
    setQuickCommands(deleteQuickCommand(id));
    if (editingCommand?.id === id) {
      setEditingCommand(null);
    }
  };

  const handleResetDefaults = () => {
    setQuickCommands(resetQuickCommands());
    setConfirmReset(false);
    setEditingCommand(null);
    setIsAddingCommand(false);
  };

  const headerActions = (
    <div className="flex items-center gap-2">
      {confirmReset ? (
        <div className="flex items-center gap-1.5 bg-[#291717] px-2 py-1 rounded border border-[#ffb4ab]/40 text-xs font-mono">
          <span className="text-[#ffb4ab]">Reset all to defaults?</span>
          <button
            type="button"
            onClick={handleResetDefaults}
            className="px-2 py-0.5 rounded bg-[#ffb4ab] text-[#121528] font-bold hover:bg-white transition-colors cursor-pointer"
          >
            Yes, Reset
          </button>
          <button
            type="button"
            onClick={() => setConfirmReset(false)}
            className="px-1.5 py-0.5 rounded text-gray-400 hover:text-white cursor-pointer"
          >
            Cancel
          </button>
        </div>
      ) : (
        <button
          type="button"
          onClick={() => setConfirmReset(true)}
          className="px-2.5 py-1 rounded text-xs font-mono text-[#8d909b] hover:text-[#dfe1fa] bg-[#171a2d] hover:bg-[#21263d] border border-[#26293c] transition-colors flex items-center gap-1 cursor-pointer"
          title="Reset quick commands to homelab defaults"
        >
          <span className="material-symbols-outlined text-[14px]">restart_alt</span>
          <span>Reset to Defaults</span>
        </button>
      )}

      <button
        type="button"
        onClick={handleOpenAdd}
        className="px-2.5 py-1 rounded text-xs font-mono font-bold text-white bg-[#1f6feb] hover:bg-[#388bfd] transition-colors flex items-center gap-1 cursor-pointer shadow-xs"
        title="Add a new quick command"
      >
        <span className="material-symbols-outlined text-[15px]">add</span>
        <span>Add Command</span>
      </button>
    </div>
  );

  return (
    <SettingsPageFrame page={page} actions={headerActions}>
      <div
        id="datacenter-quick-commands"
        data-testid="datacenter-quick-commands-settings"
        className="rounded-lg bg-[#121528] border border-[#26293c] p-4 shadow-md flex flex-col gap-3"
      >
        <div className="flex items-center justify-between">
          <div className="flex items-center gap-2">
            <span className="material-symbols-outlined text-[18px] text-[#79c0ff]">terminal</span>
            <h2 className="text-xs font-mono font-bold text-[#dfe1fa] uppercase tracking-wider">
              Terminal Quick Commands
            </h2>
            <span className="text-[10px] font-mono px-1.5 py-0.2 rounded bg-[#1b2a38] text-[#79c0ff] border border-[#79c0ff]/30">
              {quickCommands.length} Configured
            </span>
          </div>
        </div>

        <p className="text-[11px] text-[#8d909b] font-mono">
          Configure quick command shortcuts available in operator terminal sessions across all nodes. Changes
          apply reactively in real time.
        </p>

        {(isAddingCommand || editingCommand) && (
          <form
            onSubmit={handleSaveForm}
            className="p-3.5 rounded-lg bg-[#171a2d] border border-[#58a6ff]/40 flex flex-col gap-3 shadow-md"
          >
            <div className="flex items-center justify-between border-b border-[#26293c] pb-2">
              <span className="text-xs font-mono font-bold text-[#dfe1fa] flex items-center gap-1.5">
                <span className="material-symbols-outlined text-[16px] text-[#79c0ff]">
                  {editingCommand ? 'edit' : 'add_circle'}
                </span>
                {editingCommand ? `Edit Quick Command: ${editingCommand.label}` : 'Add New Quick Command'}
              </span>
              <button
                type="button"
                onClick={handleCancelForm}
                className="text-gray-400 hover:text-white cursor-pointer"
                title="Cancel"
              >
                <span className="material-symbols-outlined text-[16px]">close</span>
              </button>
            </div>

            {formError && (
              <div className="text-xs font-mono text-[#ffb4ab] bg-[#311c1c] p-2 rounded border border-[#ffb4ab]/30">
                {formError}
              </div>
            )}

            <div className="grid grid-cols-1 md:grid-cols-4 gap-3">
              <div>
                <label className="block text-[10px] font-mono text-[#8d909b] uppercase mb-1">
                  Label (Button Text) *
                </label>
                <input
                  type="text"
                  value={formLabel}
                  onChange={e => setFormLabel(e.target.value)}
                  placeholder="e.g. zpool status"
                  className="w-full bg-[#121528] border border-[#26293c] focus:border-[#58a6ff] rounded px-2.5 py-1.5 text-xs font-mono text-[#dfe1fa] outline-none"
                  required
                />
              </div>

              <div className="md:col-span-2">
                <label className="block text-[10px] font-mono text-[#8d909b] uppercase mb-1">
                  Command (CLI string) *
                </label>
                <input
                  type="text"
                  value={formCmd}
                  onChange={e => setFormCmd(e.target.value)}
                  placeholder="e.g. zpool status -v"
                  className="w-full bg-[#121528] border border-[#26293c] focus:border-[#58a6ff] rounded px-2.5 py-1.5 text-xs font-mono text-[#dfe1fa] outline-none"
                  required
                />
              </div>

              <div>
                <label className="block text-[10px] font-mono text-[#8d909b] uppercase mb-1">Category</label>
                <select
                  value={formCategory}
                  onChange={e => setFormCategory(e.target.value as QuickCommandCategory)}
                  className="w-full bg-[#121528] border border-[#26293c] focus:border-[#58a6ff] rounded px-2 py-1.5 text-xs font-mono text-[#dfe1fa] outline-none"
                >
                  <option value="system">System</option>
                  <option value="containers">Containers</option>
                  <option value="storage">Storage</option>
                  <option value="network">Network</option>
                  <option value="custom">Custom</option>
                </select>
              </div>
            </div>

            <div>
              <label className="block text-[10px] font-mono text-[#8d909b] uppercase mb-1">
                Description (Tooltip &amp; Note)
              </label>
              <input
                type="text"
                value={formDescription}
                onChange={e => setFormDescription(e.target.value)}
                placeholder="e.g. Inspect ZFS storage pool health and vdev status"
                className="w-full bg-[#121528] border border-[#26293c] focus:border-[#58a6ff] rounded px-2.5 py-1.5 text-xs font-mono text-[#dfe1fa] outline-none"
              />
            </div>

            <div className="flex items-center justify-end gap-2 pt-1">
              <button
                type="button"
                onClick={handleCancelForm}
                className="px-3 py-1 rounded text-xs font-mono text-gray-400 hover:text-white bg-[#121528] border border-[#26293c] cursor-pointer"
              >
                Cancel
              </button>
              <button
                type="submit"
                className="px-3 py-1 rounded text-xs font-mono font-bold text-white bg-[#1f6feb] hover:bg-[#388bfd] cursor-pointer"
              >
                {editingCommand ? 'Save Changes' : 'Add Command'}
              </button>
            </div>
          </form>
        )}

        {quickCommands.length === 0 ? (
          <div className="p-4 text-center text-xs font-mono text-[#8d909b] bg-[#171a2d] rounded border border-[#26293c]">
            No quick commands configured. Click "+ Add Command" or "Reset to Defaults".
          </div>
        ) : (
          <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-2.5">
            {quickCommands.map(qc => {
              const catColor =
                QUICK_CMD_CATEGORY_COLORS[qc.category || 'custom'] || QUICK_CMD_CATEGORY_COLORS.custom;
              return (
                <div
                  key={qc.id}
                  className="p-3 rounded-lg bg-[#171a2d] border border-[#26293c] hover:border-[#388bfd]/50 transition-colors flex flex-col justify-between gap-2.5"
                >
                  <div className="flex items-start justify-between gap-2">
                    <div className="flex items-center gap-1.5 flex-wrap">
                      <span className="px-2 py-0.5 rounded text-xs font-mono font-bold bg-[#1b2a38] text-[#79c0ff] border border-[#79c0ff]/30">
                        {qc.label}
                      </span>
                      <span
                        className={`px-1.5 py-0.2 rounded text-[10px] font-mono uppercase tracking-wider border ${catColor.bg} ${catColor.text} ${catColor.border}`}
                      >
                        {qc.category || 'custom'}
                      </span>
                    </div>

                    <div className="flex items-center gap-1 shrink-0">
                      <button
                        type="button"
                        onClick={() => handleOpenEdit(qc)}
                        className="p-1 rounded text-gray-400 hover:text-[#79c0ff] hover:bg-[#21262d] transition-colors cursor-pointer"
                        title={`Edit command '${qc.label}'`}
                      >
                        <span className="material-symbols-outlined text-[15px]">edit</span>
                      </button>
                      <button
                        type="button"
                        onClick={() => handleDeleteCommand(qc.id)}
                        className="p-1 rounded text-gray-400 hover:text-red-400 hover:bg-[#21262d] transition-colors cursor-pointer"
                        title={`Delete command '${qc.label}'`}
                      >
                        <span className="material-symbols-outlined text-[15px]">delete</span>
                      </button>
                    </div>
                  </div>

                  <div className="bg-[#121528] px-2.5 py-1.5 rounded border border-[#26293c] font-mono text-xs text-[#58a6ff] truncate">
                    <code>{qc.cmd}</code>
                  </div>

                  {qc.description && (
                    <div className="text-[11px] text-[#8d909b] font-mono truncate" title={qc.description}>
                      {qc.description}
                    </div>
                  )}
                </div>
              );
            })}
          </div>
        )}
      </div>
    </SettingsPageFrame>
  );
};

/**
 * Renders exactly one datacenter setting (or setting group) page.
 * The sidebar drives `page`; the parent keeps this component mounted while the
 * route stays inside the settings catalog so unsaved form state survives
 * navigation between settings pages.
 */
export const DatacenterSettingsView: React.FC<DatacenterSettingsViewProps> = ({ page }) => {
  const meta = findDatacenterSettingPage(page);
  if (!meta) return null;

  if (meta.category === 'playbooks') {
    return (
      <SettingsPageFrame page={meta.id}>
        <DatacenterPlaybooksPage showHeader={false} />
      </SettingsPageFrame>
    );
  }
  if (meta.category === 'variables') {
    return (
      <SettingsPageFrame page={meta.id}>
        <DatacenterVarsPage showHeader={false} />
      </SettingsPageFrame>
    );
  }
  if (meta.category === 'storage') {
    return (
      <SettingsPageFrame page={meta.id}>
        <RcloneRemotesView mode="datacenter" />
      </SettingsPageFrame>
    );
  }
  if (meta.category === 'terminal') {
    return <QuickCommandsSettingsPage page={meta.id} />;
  }
  return <AnsibleSettingsPage page={meta.id} />;
};
