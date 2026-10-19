import React, { useEffect } from 'react';
import type { ThemeChoice, AccentColor, ResolvedTheme } from '../../utils/themeManager.ts';
import { ACCENT_PRESETS } from '../../utils/themeManager.ts';

export interface ClientSettingsModalProps {
  isOpen: boolean;
  onClose: () => void;
  theme?: ThemeChoice;
  themeChoice?: ThemeChoice;
  onSelectTheme: (theme: ThemeChoice) => void;
  resolvedTheme?: ResolvedTheme;
  accentColor: AccentColor;
  onSelectAccentColor: (accent: AccentColor) => void;
  highContrast: boolean;
  onToggleHighContrast: () => void;
  reduceMotion?: boolean;
  reducedMotion?: boolean;
  onToggleReduceMotion?: () => void;
  onToggleReducedMotion?: () => void;
}

export const ClientSettingsModal: React.FC<ClientSettingsModalProps> = ({
  isOpen,
  onClose,
  theme,
  themeChoice,
  onSelectTheme,
  resolvedTheme,
  accentColor,
  onSelectAccentColor,
  highContrast,
  onToggleHighContrast,
  reduceMotion = false,
  reducedMotion,
  onToggleReduceMotion,
  onToggleReducedMotion,
}) => {
  const activeTheme = themeChoice ?? theme ?? 'system';
  const isReducedMotion = reducedMotion ?? reduceMotion ?? false;
  const handleToggleMotion = onToggleReducedMotion ?? onToggleReduceMotion;
  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape' && isOpen) {
        onClose();
      }
    };
    window.addEventListener('keydown', handleKeyDown);
    return () => window.removeEventListener('keydown', handleKeyDown);
  }, [isOpen, onClose]);

  if (!isOpen) return null;

  const THEMES: Array<{
    id: ThemeChoice;
    title: string;
    description: string;
    icon: string;
    swatches: string[];
  }> = [
    {
      id: 'system',
      title: 'System Default',
      description: 'Synchronizes automatically with your operating system preference.',
      icon: 'desktop_windows',
      swatches: ['#0f1224', '#1b1e31', '#b0c9ff', '#8cd6cb'],
    },
    {
      id: 'macchiato',
      title: 'Catppuccin Macchiato',
      description: 'Deep, soothing dark theme designed for long homelab operational sessions.',
      icon: 'dark_mode',
      swatches: ['#24273a', '#1e2030', '#8aadf4', '#8bd5ca'],
    },
    {
      id: 'latte',
      title: 'Catppuccin Latte',
      description: 'Crisp, high-readability light theme for bright daylight environments.',
      icon: 'light_mode',
      swatches: ['#eff1f5', '#e6e9ef', '#1e66f5', '#179299'],
    },
  ];

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/75 backdrop-blur-xs p-4">
      <div className="w-full max-w-xl bg-[#0a0d1f] rounded-lg shadow-2xl border border-[#313447] overflow-hidden flex flex-col max-h-[90vh]">
        {/* Header */}
        <div className="h-10 bg-[#171a2d] px-4 flex items-center justify-between border-b border-[#26293c] shrink-0">
          <div className="flex items-center gap-2 text-[#b0c9ff]">
            <span className="material-symbols-outlined text-[18px]">palette</span>
            <span className="font-mono text-xs font-bold uppercase tracking-wider text-[#dfe1fa]">
              Client Settings &amp; Preferences
            </span>
          </div>
          <button
            type="button"
            onClick={onClose}
            className="text-[#8d909b] hover:text-[#dfe1fa] p-1 cursor-pointer transition-colors rounded"
            title="Close client settings (Esc)"
          >
            <span className="material-symbols-outlined text-[16px]">close</span>
          </button>
        </div>

        {/* Content Body */}
        <div className="p-4 overflow-y-auto space-y-5 font-mono text-xs scrollbar-thin">
          {/* Section 1: Themes */}
          <div>
            <div className="flex items-center justify-between mb-2">
              <span className="font-bold text-[#dfe1fa] uppercase tracking-wider text-[11px] flex items-center gap-1.5">
                <span className="material-symbols-outlined text-[15px] text-[#f9c068]">contrast</span>
                Theme Selection
              </span>
              <span className="text-[10px] text-[#8d909b]">Active: {activeTheme}</span>
            </div>

            <div className="grid grid-cols-1 sm:grid-cols-3 gap-2">
              {THEMES.map(t => {
                const isSelected = activeTheme === t.id;
                return (
                  <button
                    key={t.id}
                    type="button"
                    onClick={() => onSelectTheme(t.id)}
                    className={`text-left p-3 rounded-lg border transition-all cursor-pointer flex flex-col justify-between ${
                      isSelected
                        ? 'bg-[#1e2338] border-[#8aadf4] shadow-md ring-1 ring-[#8aadf4]/40'
                        : 'bg-[#171a2d] border-[#26293c] hover:border-[#434750] hover:bg-[#1b1e31]'
                    }`}
                  >
                    <div>
                      <div className="flex items-center justify-between mb-1.5">
                        <span className={`material-symbols-outlined text-[18px] ${isSelected ? 'text-[#8aadf4]' : 'text-[#8d909b]'}`}>
                          {t.icon}
                        </span>
                        {isSelected && (
                          <span className="material-symbols-outlined text-[16px] text-[#8cd6cb]">check_circle</span>
                        )}
                      </div>
                      <div className="font-bold text-xs text-[#dfe1fa] mb-1">{t.title}</div>
                      <p className="text-[10px] text-[#8d909b] leading-tight mb-2.5">{t.description}</p>
                    </div>

                    {/* Preview Swatches */}
                    <div className="flex items-center gap-1 mt-auto pt-2 border-t border-[#26293c]/60">
                      {t.swatches.map((hex, i) => (
                        <span
                          key={i}
                          className="w-3.5 h-3.5 rounded-full border border-black/40 shadow-xs"
                          style={{ backgroundColor: hex }}
                          title={hex}
                        />
                      ))}
                    </div>
                  </button>
                );
              })}
            </div>
          </div>

          {/* Section 2: Accent Colors */}
          <div>
            <div className="flex items-center justify-between mb-2">
              <span className="font-bold text-[#dfe1fa] uppercase tracking-wider text-[11px] flex items-center gap-1.5">
                <span className="material-symbols-outlined text-[15px] text-[#8cd6cb]">brush</span>
                Accent Color Customization
              </span>
              <span className="text-[10px] text-[#8d909b]">Active: {accentColor}</span>
            </div>

            <div className="grid grid-cols-2 sm:grid-cols-3 gap-2">
              {ACCENT_PRESETS.map(preset => {
                const isSelected = accentColor === preset.id;
                return (
                  <button
                    key={preset.id}
                    type="button"
                    onClick={() => onSelectAccentColor(preset.id)}
                    className={`flex items-center gap-2 p-2 rounded-lg border transition-all cursor-pointer ${
                      isSelected
                        ? 'bg-[#1e2338] border-[#8cd6cb] ring-1 ring-[#8cd6cb]/40'
                        : 'bg-[#171a2d] border-[#26293c] hover:bg-[#1b1e31]'
                    }`}
                  >
                    <span
                      className="w-4 h-4 rounded-full border border-black/30 shrink-0 shadow-xs"
                      style={{ backgroundColor: preset.hexDark }}
                    />
                    <div className="flex flex-col text-left min-w-0 flex-1">
                      <span className="font-medium text-xs text-[#dfe1fa] truncate">{preset.label}</span>
                      <span className="text-[10px] text-[#8d909b] font-mono">{preset.hexDark}</span>
                    </div>
                    {isSelected && (
                      <span className="material-symbols-outlined text-[15px] text-[#8cd6cb] shrink-0">check</span>
                    )}
                  </button>
                );
              })}
            </div>
          </div>

          {/* Section 3: Display & Accessibility */}
          <div>
            <div className="mb-2">
              <span className="font-bold text-[#dfe1fa] uppercase tracking-wider text-[11px] flex items-center gap-1.5">
                <span className="material-symbols-outlined text-[15px] text-[#d9b9ff]">visibility</span>
                Display &amp; Accessibility
              </span>
            </div>

            <div className="space-y-2 bg-[#121528] p-3 rounded-lg border border-[#26293c]">
              <label className="flex items-center justify-between cursor-pointer">
                <div className="flex flex-col pr-4">
                  <span className="text-xs font-semibold text-[#dfe1fa]">High Contrast Borders</span>
                  <span className="text-[10px] text-[#8d909b]">Enhances visual separation between cockpit panels and navigation cards.</span>
                </div>
                <input
                  type="checkbox"
                  checked={highContrast}
                  onChange={onToggleHighContrast}
                  className="w-4 h-4 rounded border-[#434750] accent-[#8aadf4] cursor-pointer"
                />
              </label>

              {handleToggleMotion && (
                <>
                  <div className="h-[1px] bg-[#26293c]"></div>
                  <label className="flex items-center justify-between cursor-pointer">
                    <div className="flex flex-col pr-4">
                      <span className="text-xs font-semibold text-[#dfe1fa]">Reduce Pulse Animations</span>
                      <span className="text-[10px] text-[#8d909b]">Pauses live ping animations on host status dots to minimize CPU draw.</span>
                    </div>
                    <input
                      type="checkbox"
                      checked={isReducedMotion}
                      onChange={handleToggleMotion}
                      className="w-4 h-4 rounded border-[#434750] accent-[#8aadf4] cursor-pointer"
                    />
                  </label>
                </>
              )}
            </div>
          </div>

          {/* Section 4: Engine & Client Specs */}
          <div className="p-2.5 bg-[#0f1224] rounded border border-[#26293c]/80 text-[#8d909b] text-[10px] flex items-center justify-between">
            <span className="flex items-center gap-1">
              <span className="material-symbols-outlined text-[13px] text-[#8cd6cb]">terminal</span>
              <span>Terminal Engine: libghostty WASM (v0.4.0)</span>
            </span>
            <span className="font-semibold text-[#dfe1fa]">Air Traffic Control</span>
          </div>
        </div>

        {/* Footer */}
        <div className="h-11 bg-[#171a2d] px-4 flex items-center justify-end border-t border-[#26293c] shrink-0">
          <button
            type="button"
            onClick={onClose}
            className="px-4 py-1.5 bg-[#8aadf4] text-[#181926] hover:bg-[#b0c9ff] rounded font-bold font-mono text-xs transition-colors cursor-pointer"
          >
            Done
          </button>
        </div>
      </div>
    </div>
  );
};
