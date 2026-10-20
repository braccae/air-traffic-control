import type { GlobalSettingsField, GlobalSettingsPage } from '../types/index.ts';

/** Prefix under which module-contributed settings sections appear in the sidebar. */
export const SETTINGS_SECTION_PREFIX = 'settings-';

/**
 * The bullet run the backend uses for every masked secret value. A secret field
 * is write-only: the API only ever returns this mask, never the stored value.
 */
export const SECRET_MASK_PREFIX = '••••••••';

/** Returns true when a value is the backend's masked secret placeholder. */
export function isMaskedSecret(value: unknown): boolean {
  return typeof value === 'string' && value.includes(SECRET_MASK_PREFIX);
}

/**
 * Builds the PUT payload for a settings page. Secret fields are only included
 * when the operator actually typed a replacement value, so echoing a mask back
 * can never overwrite the stored secret with its own mask.
 */
export function buildSettingsPayload(
  fields: GlobalSettingsField[],
  values: Record<string, unknown>
): Record<string, unknown> {
  const payload: Record<string, unknown> = {};
  for (const field of fields) {
    const value = values[field.name];
    if (value === undefined) continue;
    if (field.secret || field.type === 'secret') {
      if (typeof value !== 'string') continue;
      const trimmed = value.trim();
      if (trimmed === '' || isMaskedSecret(trimmed)) continue;
      payload[field.name] = trimmed;
      continue;
    }
    payload[field.name] = value;
  }
  return payload;
}

/**
 * Maps a settings page id (e.g. "backup/restic") to the deterministic sidebar
 * section id used for selection and scroll targeting (e.g. "settings-backup-restic").
 */
export function settingsSectionId(pageId: string): string {
  return `${SETTINGS_SECTION_PREFIX}${pageId.replace(/\//g, '-')}`;
}

/** Returns true when a datacenter section id refers to a module settings page. */
export function isSettingsSection(section: string): boolean {
  return section.startsWith(SETTINGS_SECTION_PREFIX);
}

/**
 * Fetches every global settings page contributed by addon modules, each with its
 * current persisted values.
 */
export async function fetchGlobalSettingsPages(): Promise<GlobalSettingsPage[]> {
  const res = await fetch('/api/settings/pages');
  if (!res.ok) {
    throw new Error(`Failed to load global settings pages (HTTP ${res.status})`);
  }
  const data = await res.json();
  return Array.isArray(data) ? (data as GlobalSettingsPage[]) : [];
}

/**
 * Persists the supplied field values for a settings page and returns the
 * re-read page (schema + persisted values) from the backend.
 */
export async function saveGlobalSettingsPage(
  pageId: string,
  values: Record<string, unknown>
): Promise<GlobalSettingsPage> {
  const res = await fetch(`/api/settings/pages/${encodeURI(pageId)}`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ values }),
  });
  if (!res.ok) {
    const text = await res.text().catch(() => '');
    throw new Error(text || `Failed to save settings (HTTP ${res.status})`);
  }
  return (await res.json()) as GlobalSettingsPage;
}
