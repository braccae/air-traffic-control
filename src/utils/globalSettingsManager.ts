import type { GlobalSettingsPage } from '../types/index.ts';

/** Prefix under which module-contributed settings sections appear in the sidebar. */
export const SETTINGS_SECTION_PREFIX = 'settings-';

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
