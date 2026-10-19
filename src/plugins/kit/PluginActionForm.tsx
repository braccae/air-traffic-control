import React, { useState } from 'react';

export interface OptionSchema {
  name: string;
  type: string; // 'string' | 'int' | 'bool' | 'select' | string
  required?: boolean;
  default?: any;
  enum?: string[];
  description?: string;
  secret?: boolean;
}

export interface ActionSpec {
  name: string;
  label: string;
  description?: string;
  options?: OptionSchema[];
}

export interface PluginActionFormProps {
  action: ActionSpec;
  onSubmit: (values: Record<string, any>) => void | Promise<void>;
  onCancel?: () => void;
  submitting?: boolean;
  className?: string;
}

export const PluginActionForm: React.FC<PluginActionFormProps> = ({
  action,
  onSubmit,
  onCancel,
  submitting = false,
  className = '',
}) => {
  const options = action.options || [];

  const [formValues, setFormValues] = useState<Record<string, any>>(() => {
    const initial: Record<string, any> = {};
    for (const opt of options) {
      if (opt.default !== undefined) {
        initial[opt.name] = opt.default;
      } else if (opt.type === 'bool') {
        initial[opt.name] = false;
      } else if (opt.type === 'int') {
        initial[opt.name] = 0;
      } else if (opt.type === 'select' && opt.enum && opt.enum.length > 0) {
        initial[opt.name] = opt.enum[0];
      } else {
        initial[opt.name] = '';
      }
    }
    return initial;
  });

  const [showSecretMap, setShowSecretMap] = useState<Record<string, boolean>>({});

  const handleChange = (name: string, value: any) => {
    setFormValues(prev => ({
      ...prev,
      [name]: value,
    }));
  };

  const toggleSecretMask = (name: string) => {
    setShowSecretMap(prev => ({
      ...prev,
      [name]: !prev[name],
    }));
  };

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    onSubmit(formValues);
  };

  return (
    <form
      onSubmit={handleSubmit}
      className={`bg-[#171a2d] border border-[#26293c] rounded p-4 flex flex-col gap-3 font-mono text-xs ${className}`.trim()}
    >
      <div className="border-b border-[#26293c] pb-2">
        <h4 className="font-bold text-[#b0c9ff] text-sm flex items-center gap-1.5">
          <span className="material-symbols-outlined text-[16px]">play_circle</span>
          <span>{action.label || action.name}</span>
        </h4>
        {action.description && (
          <p className="text-[#8d909b] text-[11px] mt-0.5">{action.description}</p>
        )}
      </div>

      <div className="flex flex-col gap-3">
        {options.map(opt => {
          const val = formValues[opt.name];
          const isSecret = Boolean(opt.secret);
          const isRevealed = Boolean(showSecretMap[opt.name]);

          if (opt.type === 'bool') {
            return (
              <label
                key={opt.name}
                className="flex items-center gap-2 cursor-pointer select-none py-1"
              >
                <input
                  type="checkbox"
                  checked={Boolean(val)}
                  onChange={e => handleChange(opt.name, e.target.checked)}
                  className="rounded border-[#434750] bg-[#0a0d1f] text-[#b0c9ff] focus:ring-0 focus:ring-offset-0 cursor-pointer"
                />
                <span className="text-[#dfe1fa] font-semibold">{opt.name}</span>
                {opt.required && <span className="text-[#ffb4ab]">*</span>}
                {opt.description && (
                  <span className="text-[#8d909b] text-[11px]">({opt.description})</span>
                )}
              </label>
            );
          }

          if (opt.type === 'select' || (opt.enum && opt.enum.length > 0)) {
            return (
              <div key={opt.name} className="flex flex-col gap-1">
                <label className="text-[#8d909b] text-[11px] font-semibold flex items-center gap-1">
                  <span>{opt.name}</span>
                  {opt.required && <span className="text-[#ffb4ab]">*</span>}
                </label>
                <select
                  value={val ?? ''}
                  onChange={e => handleChange(opt.name, e.target.value)}
                  className="bg-[#0a0d1f] border border-[#26293c] rounded px-2 py-1.5 text-[#dfe1fa] focus:border-[#b0c9ff] outline-none"
                >
                  {(opt.enum || []).map(choice => (
                    <option key={choice} value={choice}>
                      {choice}
                    </option>
                  ))}
                </select>
                {opt.description && (
                  <span className="text-[#8d909b] text-[10px]">{opt.description}</span>
                )}
              </div>
            );
          }

          return (
            <div key={opt.name} className="flex flex-col gap-1">
              <label className="text-[#8d909b] text-[11px] font-semibold flex items-center gap-1">
                <span>{opt.name}</span>
                {opt.required && <span className="text-[#ffb4ab]">*</span>}
                {isSecret && (
                  <span className="text-[10px] bg-[#ffd97d]/10 text-[#ffd97d] px-1 py-0.2 rounded border border-[#ffd97d]/30">
                    SECRET / MASKED
                  </span>
                )}
              </label>

              <div className="relative flex items-center">
                <input
                  type={
                    isSecret
                      ? isRevealed
                        ? 'text'
                        : 'password'
                      : opt.type === 'int'
                      ? 'number'
                      : 'text'
                  }
                  required={opt.required}
                  value={val ?? ''}
                  onChange={e =>
                    handleChange(
                      opt.name,
                      opt.type === 'int'
                        ? e.target.value === ''
                          ? ''
                          : parseInt(e.target.value, 10)
                        : e.target.value
                    )
                  }
                  placeholder={isSecret ? '••••••••' : opt.description || opt.name}
                  className="w-full bg-[#0a0d1f] border border-[#26293c] rounded px-2 py-1.5 text-[#dfe1fa] placeholder-[#575a68] focus:border-[#b0c9ff] outline-none pr-8"
                />
                {isSecret && (
                  <button
                    type="button"
                    onClick={() => toggleSecretMask(opt.name)}
                    className="absolute right-2 text-[#8d909b] hover:text-[#dfe1fa] cursor-pointer"
                    title={isRevealed ? 'Mask secret' : 'Reveal secret'}
                  >
                    <span className="material-symbols-outlined text-[15px]">
                      {isRevealed ? 'visibility_off' : 'visibility'}
                    </span>
                  </button>
                )}
              </div>

              {opt.description && (
                <span className="text-[#8d909b] text-[10px]">{opt.description}</span>
              )}
            </div>
          );
        })}
      </div>

      <div className="flex items-center justify-end gap-2 pt-2 border-t border-[#26293c]">
        {onCancel && (
          <button
            type="button"
            onClick={onCancel}
            disabled={submitting}
            className="px-3 py-1 rounded bg-[#26293c] hover:bg-[#35384c] text-[#c3c6d2] transition-colors cursor-pointer disabled:opacity-50"
          >
            Cancel
          </button>
        )}
        <button
          type="submit"
          disabled={submitting}
          className="flex items-center gap-1.5 px-3 py-1 rounded bg-[#b0c9ff] hover:bg-[#b0c9ff]/80 text-[#002d6c] font-bold transition-colors cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed"
        >
          {submitting && (
            <span className="material-symbols-outlined text-[14px] animate-spin">sync</span>
          )}
          <span>{submitting ? 'Staging Task...' : 'Stage Action'}</span>
        </button>
      </div>
    </form>
  );
};
