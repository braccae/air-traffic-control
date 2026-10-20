/**
 * Maps known icon names or Lucide/Feather conventions to valid Material Symbols icon names.
 * Ensures unknown icon strings never render as literal text ligatures.
 */
const KNOWN_MATERIAL_SYMBOLS = new Set([
  'admin_panel_settings',
  'archive',
  'arrow_back',
  'arrow_forward',
  'backup',
  'badge',
  'bolt',
  'check_circle',
  'cloud_done',
  'cloud_queue',
  'computer',
  'dashboard',
  'database',
  'delete',
  'deployed_code',
  'description',
  'desktop_windows',
  'developer_board',
  'dns',
  'error',
  'extension',
  'folder',
  'folder_managed',
  'folder_shared',
  'gpp_bad',
  'hard_drive',
  'hub',
  'insights',
  'inventory_2',
  'lan',
  'layers',
  'lock',
  'memory',
  'monitoring',
  'network_ping',
  'package_2',
  'person',
  'person_check',
  'play_arrow',
  'power_settings_new',
  'refresh',
  'restart_alt',
  'router',
  'security',
  'settings',
  'settings_ethernet',
  'settings_suggest',
  'shield',
  'stop',
  'storage',
  'sync',
  'terminal',
  'tune',
  'verified_user',
  'view_module',
  'vpn_key',
  'warning',
  'widgets',
]);

const ICON_FALLBACK_MAP: Record<string, string> = {
  activity: 'settings_suggest',
  container: 'deployed_code',
  server: 'dns',
  box: 'package_2',
  'shield-check': 'verified_user',
  'shield-alert': 'admin_panel_settings',
  'user-check': 'person_check',
  shield_check: 'verified_user',
  shield_alert: 'admin_panel_settings',
  user_check: 'person_check',
  service: 'settings_suggest',
  services: 'settings_suggest',
  host: 'dns',
  packages: 'package_2',
  package: 'package_2',
  disk: 'storage',
  disks: 'storage',
  drive: 'storage',
};

export function resolvePluginIcon(icon?: string): string {
  if (!icon) return 'extension';
  const clean = icon.trim().toLowerCase();
  if (ICON_FALLBACK_MAP[clean]) {
    return ICON_FALLBACK_MAP[clean];
  }
  const normalized = clean.replace(/-/g, '_');
  if (ICON_FALLBACK_MAP[normalized]) {
    return ICON_FALLBACK_MAP[normalized];
  }
  if (KNOWN_MATERIAL_SYMBOLS.has(normalized)) {
    return normalized;
  }
  return 'extension';
}
