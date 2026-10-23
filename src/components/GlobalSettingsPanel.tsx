import React, { useState } from 'react';
import type { GlobalSettingsField, GlobalSettingsPage } from '../types/index.ts';
import {
  buildSettingsPayload,
  isMaskedSecret,
  saveGlobalSettingsPage,
} from '../utils/globalSettingsManager.ts';

export interface GlobalSettingsPanelProps {
  page: GlobalSettingsPage;
  onSaved?: (page: GlobalSettingsPage) => void;
  /**
   * Optional persistence hook. When provided it replaces the default
   * PUT /api/settings/pages/:id call — datacenter-scoped pages pass a hook that
   * writes to /api/settings/datacenter/:id instead. Values are already filtered
   * by buildSettingsPayload, so secrets are only sent when genuinely retyped.
   */
  save?: (values: Record<string, unknown>) => Promise<GlobalSettingsPage>;
}

/**
 * Generic renderer for a module-contributed global settings page. The page's
 * field schema (declared by the addon module) drives the form; values are
 * persisted through PUT /api/settings/pages/:id by the Go daemon.
 */
export const GlobalSettingsPanel: React.FC<GlobalSettingsPanelProps> = ({ page, onSaved, save }) => {
  const [values, setValues] = useState<Record<string, unknown>>(page.values || {});
  const [saving, setSaving] = useState(false);
  const [feedback, setFeedback] = useState<{ type: 'success' | 'error'; message: string } | null>(null);

  const setField = (name: string, value: unknown) => {
    setValues(prev => ({ ...prev, [name]: value }));
  };

  const handleSave = async () => {
    setSaving(true);
    setFeedback(null);
    try {
      // Secret fields are write-only: only genuinely retyped values are sent,
      // never the mask the API returned.
      const payload = buildSettingsPayload(page.fields, values);
      const updated = save ? await save(payload) : await saveGlobalSettingsPage(page.id, payload);
      setValues(updated.values || {});
      setFeedback({ type: 'success', message: `Saved ${page.title}.` });
      onSaved?.(updated);
    } catch (err: any) {
      setFeedback({ type: 'error', message: err?.message || 'Failed to save settings' });
    } finally {
      setSaving(false);
    }
  };

  const renderField = (field: GlobalSettingsField) => {
    const raw = values[field.name];
    const inputClass =
      'w-full bg-[#121528] border border-[#26293c] rounded px-2.5 py-1.5 text-xs font-mono text-[#dfe1fa] focus:border-[#8cd6cb] focus:outline-hidden';

    let control: React.ReactNode;
    switch (field.type) {
      case 'bool':
        control = (
          <label className="inline-flex items-center gap-2 cursor-pointer">
            <input
              type="checkbox"
              checked={Boolean(raw)}
              onChange={e => setField(field.name, e.target.checked)}
              className="rounded bg-[#121528] border-[#26293c] text-[#1f6feb] focus:ring-0 cursor-pointer"
            />
            <span className="text-xs font-mono text-[#dfe1fa]">{raw ? 'Enabled' : 'Disabled'}</span>
          </label>
        );
        break;
      case 'select':
        control = (
          <select
            value={String(raw ?? '')}
            onChange={e => setField(field.name, e.target.value)}
            className={`${inputClass} cursor-pointer`}
          >
            <option value="">-- select --</option>
            {(field.options || []).map(opt => (
              <option key={opt.value} value={opt.value}>
                {opt.label}
              </option>
            ))}
          </select>
        );
        break;
      case 'int':
        control = (
          <input
            type="number"
            value={raw === undefined || raw === null ? '' : String(raw)}
            min={field.min}
            max={field.max}
            onChange={e => {
              const parsed = parseInt(e.target.value, 10);
              setField(field.name, Number.isNaN(parsed) ? '' : parsed);
            }}
            className={inputClass}
          />
        );
        break;
      case 'text':
        control = (
          <textarea
            value={String(raw ?? '')}
            placeholder={field.placeholder}
            onChange={e => setField(field.name, e.target.value)}
            rows={3}
            className={inputClass}
          />
        );
        break;
      default: {
        // string, secret
        const isSecret = field.type === 'secret' || field.secret;
        if (isSecret) {
          // The API only ever returns a mask for a stored secret. Never load it
          // into the input: the field stays empty and accepts a replacement.
          const storedMask = isMaskedSecret(raw) ? String(raw) : '';
          control = (
            <div className="flex flex-col gap-1">
              <input
                type="password"
                value=""
                autoComplete="new-password"
                placeholder={storedMask || field.placeholder || 'not set'}
                onChange={e => setField(field.name, e.target.value)}
                className={inputClass}
                aria-label={field.label}
              />
              <span className="text-[10px] font-mono text-[#8d909b]">
                {storedMask ? `Stored: ${storedMask}` : 'No value stored — enter one to set it.'}
              </span>
            </div>
          );
          break;
        }
        control = (
          <input
            type="text"
            value={String(raw ?? '')}
            placeholder={field.placeholder}
            onChange={e => setField(field.name, e.target.value)}
            className={inputClass}
          />
        );
      }
    }

    return (
      <div key={field.name} className="p-3 rounded-lg bg-[#171a2d] border border-[#26293c] flex flex-col gap-1.5">
        <label className="text-xs font-mono font-bold text-[#dfe1fa] flex items-center justify-between gap-2">
          <span className="truncate">
            {field.label}
            {field.required && <span className="text-[#ffb4ab] ml-1">*</span>}
          </span>
          <span className="text-[10px] font-mono text-[#8d909b] uppercase shrink-0">{field.type}</span>
        </label>
        {control}
        {field.description && (
          <span className="text-[10px] font-mono text-[#8d909b] leading-tight">{field.description}</span>
        )}
      </div>
    );
  };

  return (
    <div className="flex flex-col gap-3">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="flex items-center gap-2">
          <span className="material-symbols-outlined text-[18px] text-[#8cd6cb]">{page.icon || 'tune'}</span>
          <h2 className="text-xs font-mono font-bold text-[#dfe1fa] uppercase tracking-wider">{page.title}</h2>
          <span className="text-[10px] font-mono px-1.5 py-0.2 rounded bg-[#1b2a38] text-[#8cd6cb] border border-[#8cd6cb]/30">
            {page.fields.length} settings
          </span>
        </div>
        <button
          type="button"
          onClick={handleSave}
          disabled={saving}
          className="px-3 py-1 rounded text-xs font-mono font-bold text-white bg-[#1f6feb] hover:bg-[#388bfd] disabled:opacity-50 transition-colors flex items-center gap-1.5 cursor-pointer shadow-xs"
          title="Persist these values to the datacenter settings store"
        >
          <span className="material-symbols-outlined text-[15px]">{saving ? 'sync' : 'save'}</span>
          <span>{saving ? 'Saving...' : 'Save Settings'}</span>
        </button>
      </div>

      {page.description && (
        <p className="text-[11px] text-[#8d909b] font-mono">{page.description}</p>
      )}

      {feedback && (
        <div
          className={`p-2.5 rounded text-xs font-mono border ${
            feedback.type === 'success'
              ? 'bg-[#162620] text-[#8cd6cb] border-[#8cd6cb]/30'
              : 'bg-[#311c1c] text-[#ffb4ab] border-[#ffb4ab]/30'
          }`}
        >
          {feedback.message}
        </div>
      )}

      <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-3">
        {page.fields.map(renderField)}
      </div>
    </div>
  );
};
