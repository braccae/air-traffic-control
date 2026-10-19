import React, { useState, useEffect, useMemo, useRef } from 'react';
import {
  VarEntry,
  VarType,
  generateVarId,
  parseYamlToVars,
  varsToYaml,
  varsToObject,
  objectToVars,
  coerceValueByType,
} from '../utils/varsParser';

export interface VarsSecretsEditorProps {
  yamlContent?: string;
  onChangeYaml?: (newYaml: string) => void;
  value?: Record<string, any>;
  onChange?: (val: Record<string, any>) => void;
  readOnly?: boolean;
  onSave?: () => void;
  title?: string;
  compact?: boolean;
  allowModeToggle?: boolean;
  defaultMode?: 'structured' | 'yaml';
}

export const VarsSecretsEditor: React.FC<VarsSecretsEditorProps> = ({
  yamlContent,
  onChangeYaml,
  value,
  onChange,
  readOnly = false,
  onSave,
  title,
  compact = false,
  allowModeToggle = true,
  defaultMode = 'structured',
}) => {
  const [mode, setMode] = useState<'structured' | 'yaml'>(defaultMode);
  const [vars, setVars] = useState<VarEntry[]>(() => {
    if (yamlContent !== undefined) {
      return parseYamlToVars(yamlContent).vars;
    }
    if (value !== undefined) {
      return objectToVars(value);
    }
    return [];
  });

  const [yamlText, setYamlText] = useState<string>(() => {
    if (yamlContent !== undefined) return yamlContent;
    if (value !== undefined) return varsToYaml(objectToVars(value));
    return '---\n';
  });

  const [searchQuery, setSearchQuery] = useState('');
  const [revealedSecrets, setRevealedSecrets] = useState<Record<string, boolean>>({});
  const [syntaxStatus, setSyntaxStatus] = useState<{ isError: boolean; message: string } | null>(null);

  // Keep track of external updates to yamlContent
  const lastYamlPropRef = useRef(yamlContent);
  useEffect(() => {
    if (yamlContent !== undefined && yamlContent !== lastYamlPropRef.current) {
      lastYamlPropRef.current = yamlContent;
      setYamlText(yamlContent);
      const parsed = parseYamlToVars(yamlContent);
      if (!parsed.hasErrors) {
        setVars(parsed.vars);
        setSyntaxStatus(null);
      }
    }
  }, [yamlContent]);

  // Keep track of external updates to value
  const lastValuePropRef = useRef(value);
  useEffect(() => {
    if (value !== undefined && value !== lastValuePropRef.current) {
      lastValuePropRef.current = value;
      const converted = objectToVars(value);
      setVars(converted);
      setYamlText(varsToYaml(converted));
    }
  }, [value]);

  // Propagate changes upwards
  const emitChanges = (newVars: VarEntry[]) => {
    setVars(newVars);
    const newYaml = varsToYaml(newVars);
    setYamlText(newYaml);
    lastYamlPropRef.current = newYaml;

    if (onChangeYaml) {
      onChangeYaml(newYaml);
    }
    if (onChange) {
      onChange(varsToObject(newVars));
    }
  };

  const handleAddVariable = () => {
    if (readOnly) return;
    const newVar: VarEntry = {
      id: generateVarId(),
      key: `var_${vars.length + 1}`,
      type: 'string',
      value: '',
    };
    const updated = [...vars, newVar];
    emitChanges(updated);
  };

  const handleDeleteVariable = (id: string) => {
    if (readOnly) return;
    const updated = vars.filter(v => v.id !== id);
    emitChanges(updated);
  };

  const handleUpdateKey = (id: string, newKey: string) => {
    if (readOnly) return;
    const updated = vars.map(v => (v.id === id ? { ...v, key: newKey } : v));
    emitChanges(updated);
  };

  const handleUpdateType = (id: string, newType: VarType) => {
    if (readOnly) return;
    const updated = vars.map(v => {
      if (v.id !== id) return v;
      let coercedVal = coerceValueByType(newType, v.value);
      if (newType === 'boolean' && typeof coercedVal !== 'boolean') {
        coercedVal = false;
      }
      return {
        ...v,
        type: newType,
        value: coercedVal,
        isVault: newType === 'secret' ? v.isVault || String(v.value).includes('$ANSIBLE_VAULT;') : false,
      };
    });
    emitChanges(updated);
  };

  const handleUpdateValue = (id: string, newValue: any) => {
    if (readOnly) return;
    const updated = vars.map(v => {
      if (v.id !== id) return v;
      return { ...v, value: coerceValueByType(v.type, newValue) };
    });
    emitChanges(updated);
  };

  const toggleRevealSecret = (id: string) => {
    setRevealedSecrets(prev => ({ ...prev, [id]: !prev[id] }));
  };

  const handleModeSwitch = (targetMode: 'structured' | 'yaml') => {
    if (targetMode === mode) return;

    if (targetMode === 'yaml') {
      // Serialize structured vars into YAML
      const serialized = varsToYaml(vars);
      setYamlText(serialized);
      setSyntaxStatus(null);
      setMode('yaml');
    } else {
      // Parse YAML into structured vars
      const result = parseYamlToVars(yamlText);
      if (result.hasErrors) {
        setSyntaxStatus({
          isError: true,
          message: `Cannot switch to Structured UI: ${result.errorMessage || 'Invalid YAML format'}`,
        });
      } else {
        setVars(result.vars);
        setSyntaxStatus(null);
        setMode('structured');
        emitChanges(result.vars);
      }
    }
  };

  const handleYamlTextChange = (newText: string) => {
    setYamlText(newText);
    lastYamlPropRef.current = newText;
    if (onChangeYaml) {
      onChangeYaml(newText);
    }

    const res = parseYamlToVars(newText);
    if (!res.hasErrors) {
      setVars(res.vars);
      setSyntaxStatus({ isError: false, message: 'Valid YAML structure' });
      if (onChange) {
        onChange(varsToObject(res.vars));
      }
    } else {
      setSyntaxStatus({ isError: true, message: res.errorMessage || 'YAML Syntax Warning' });
    }
  };

  const filteredVars = useMemo(() => {
    if (!searchQuery.trim()) return vars;
    const q = searchQuery.toLowerCase();
    return vars.filter(
      v => v.key.toLowerCase().includes(q) || String(v.value).toLowerCase().includes(q)
    );
  }, [vars, searchQuery]);

  // Compute line numbers for raw YAML editor
  const yamlLines = useMemo(() => {
    return yamlText.split('\n').map((_, idx) => idx + 1);
  }, [yamlText]);

  return (
    <div className={`flex flex-col h-full bg-[#0a0d1f] text-[#dfe1fa] font-mono select-text ${compact ? 'text-xs' : 'text-xs'}`}>
      {/* Top Toolbar */}
      <div className="flex items-center justify-between gap-2 px-3 py-2 bg-[#171a2d] border-b border-[#26293c] shrink-0">
        <div className="flex items-center gap-2 flex-1 min-w-0">
          {title && (
            <span className="font-bold text-[#b0c9ff] truncate max-w-[200px]" title={title}>
              {title}
            </span>
          )}

          {mode === 'structured' && (
            <div className="relative flex-1 max-w-xs">
              <span className="material-symbols-outlined absolute left-2 top-1/2 -translate-y-1/2 text-[14px] text-[#8d909b]">
                search
              </span>
              <input
                type="text"
                placeholder="Filter variables..."
                value={searchQuery}
                onChange={e => setSearchQuery(e.target.value)}
                className="w-full bg-[#1b1e31] border border-[#26293c] focus:border-[#b0c9ff] rounded pl-7 pr-2 py-1 text-xs text-[#dfe1fa] placeholder-[#8d909b] outline-none"
              />
            </div>
          )}

          <span className="text-[11px] text-[#8d909b] whitespace-nowrap">
            {vars.length} {vars.length === 1 ? 'variable' : 'variables'}
            {searchQuery && ` (matching ${filteredVars.length})`}
          </span>
        </div>

        <div className="flex items-center gap-2 shrink-0">
          {mode === 'structured' && !readOnly && (
            <button
              type="button"
              onClick={handleAddVariable}
              className="px-2.5 py-1 bg-[#1b1e31] hover:bg-[#26293c] text-[#8cd6cb] border border-[#8cd6cb]/30 hover:border-[#8cd6cb] rounded flex items-center gap-1 cursor-pointer transition-colors shadow-xs"
              title="Add a new variable row"
            >
              <span className="material-symbols-outlined text-[14px]">add</span>
              <span className="font-bold text-[11px]">+ Add Variable</span>
            </button>
          )}

          {allowModeToggle && (
            <div className="flex items-center bg-[#101326] p-0.5 rounded border border-[#26293c]">
              <button
                type="button"
                onClick={() => handleModeSwitch('structured')}
                className={`px-2 py-0.5 rounded text-[11px] flex items-center gap-1 cursor-pointer transition-colors ${
                  mode === 'structured'
                    ? 'bg-[#26293c] text-[#b0c9ff] font-bold shadow-xs'
                    : 'text-[#8d909b] hover:text-[#dfe1fa]'
                }`}
                title="Structured Key-Value Table"
              >
                <span className="material-symbols-outlined text-[13px]">table_rows</span>
                <span>Structured UI</span>
              </button>
              <button
                type="button"
                onClick={() => handleModeSwitch('yaml')}
                className={`px-2 py-0.5 rounded text-[11px] flex items-center gap-1 cursor-pointer transition-colors ${
                  mode === 'yaml'
                    ? 'bg-[#26293c] text-[#b0c9ff] font-bold shadow-xs'
                    : 'text-[#8d909b] hover:text-[#dfe1fa]'
                }`}
                title="Raw YAML Code View"
              >
                <span className="material-symbols-outlined text-[13px]">code</span>
                <span>Raw YAML</span>
              </button>
            </div>
          )}
        </div>
      </div>

      {/* Syntax Error / Warning Banner */}
      {syntaxStatus && (
        <div
          className={`px-3 py-1.5 text-xs flex items-center justify-between border-b ${
            syntaxStatus.isError
              ? 'bg-[#ffb4ab]/10 border-[#ffb4ab]/30 text-[#ffb4ab]'
              : 'bg-[#8cd6cb]/10 border-[#8cd6cb]/30 text-[#8cd6cb]'
          }`}
        >
          <div className="flex items-center gap-1.5">
            <span className="material-symbols-outlined text-[14px]">
              {syntaxStatus.isError ? 'error' : 'check_circle'}
            </span>
            <span>{syntaxStatus.message}</span>
          </div>
          {syntaxStatus.isError && mode === 'yaml' && (
            <span className="text-[10px] text-[#8d909b]">Correct YAML syntax to enable Structured view</span>
          )}
        </div>
      )}

      {/* Main Content Area */}
      {mode === 'structured' ? (
        <div className="flex-1 overflow-auto bg-[#0a0d1f]">
          {filteredVars.length === 0 ? (
            <div className="flex flex-col items-center justify-center h-full py-12 text-[#8d909b] space-y-2">
              <span className="material-symbols-outlined text-4xl text-[#313447]">vpn_key_off</span>
              <p className="text-xs">
                {searchQuery ? 'No variables match your filter' : 'No variables defined'}
              </p>
              {!readOnly && (
                <button
                  type="button"
                  onClick={handleAddVariable}
                  className="px-3 py-1 bg-[#1b1e31] hover:bg-[#26293c] text-[#b0c9ff] border border-[#313447] rounded cursor-pointer text-xs"
                >
                  Create First Variable
                </button>
              )}
            </div>
          ) : (
            <table className="w-full border-collapse text-left">
              <thead>
                <tr className="bg-[#121528] text-[#8d909b] text-[10px] uppercase tracking-wider border-b border-[#26293c] sticky top-0 z-10">
                  <th className="py-2 px-3 font-semibold w-1/3">Variable Key</th>
                  <th className="py-2 px-2 font-semibold w-28">Type</th>
                  <th className="py-2 px-3 font-semibold">Value</th>
                  {!readOnly && <th className="py-2 px-2 font-semibold w-12 text-center">Action</th>}
                </tr>
              </thead>
              <tbody className="divide-y divide-[#1f2238]">
                {filteredVars.map(item => {
                  const isRevealed = !!revealedSecrets[item.id];
                  const isSecret = item.type === 'secret';
                  const isBool = item.type === 'boolean';
                  const isNum = item.type === 'number';

                  return (
                    <tr
                      key={item.id}
                      className="hover:bg-[#15182b] transition-colors group"
                    >
                      {/* Key Column */}
                      <td className="py-1.5 px-3 align-top">
                        <input
                          type="text"
                          value={item.key}
                          onChange={e => handleUpdateKey(item.id, e.target.value)}
                          disabled={readOnly}
                          placeholder="variable_name"
                          className="w-full bg-[#171a2d] border border-[#26293c] focus:border-[#b0c9ff] rounded px-2 py-1 text-xs font-mono text-[#b0c9ff] font-semibold outline-none disabled:opacity-70"
                        />
                      </td>

                      {/* Type Column */}
                      <td className="py-1.5 px-2 align-top">
                        <select
                          value={item.type}
                          onChange={e => handleUpdateType(item.id, e.target.value as VarType)}
                          disabled={readOnly}
                          className="w-full bg-[#171a2d] border border-[#26293c] focus:border-[#b0c9ff] rounded px-2 py-1 text-xs text-[#dfe1fa] outline-none disabled:opacity-70 cursor-pointer"
                        >
                          <option value="string">String</option>
                          <option value="number">Number</option>
                          <option value="boolean">Boolean</option>
                          <option value="secret">Secret / Vault</option>
                        </select>
                      </td>

                      {/* Value Column */}
                      <td className="py-1.5 px-3 align-top">
                        {isBool ? (
                          <div className="flex items-center gap-2 py-0.5">
                            <button
                              type="button"
                              onClick={() => !readOnly && handleUpdateValue(item.id, !item.value)}
                              disabled={readOnly}
                              className={`relative inline-flex h-5 w-10 shrink-0 cursor-pointer rounded-full border-2 border-transparent transition-colors duration-200 ease-in-out focus:outline-none ${
                                item.value ? 'bg-[#8cd6cb]' : 'bg-[#313447]'
                              }`}
                            >
                              <span
                                className={`pointer-events-none inline-block h-4 w-4 transform rounded-full bg-[#0a0d1f] shadow-lg ring-0 transition duration-200 ease-in-out ${
                                  item.value ? 'translate-x-5' : 'translate-x-0'
                                }`}
                              />
                            </button>
                            <span
                              className={`text-xs font-mono font-bold ${
                                item.value ? 'text-[#8cd6cb]' : 'text-[#8d909b]'
                              }`}
                            >
                              {item.value ? 'true' : 'false'}
                            </span>
                          </div>
                        ) : isSecret ? (
                          <div className="space-y-1">
                            <div className="flex items-center gap-1.5">
                              <div className="relative flex-1">
                                <input
                                  type={isRevealed ? 'text' : 'password'}
                                  value={String(item.value ?? '')}
                                  onChange={e => handleUpdateValue(item.id, e.target.value)}
                                  disabled={readOnly}
                                  placeholder="Secret value / token"
                                  className="w-full bg-[#171a2d] border border-[#26293c] focus:border-[#d9b9ff] rounded pl-2 pr-7 py-1 text-xs font-mono text-[#d9b9ff] outline-none disabled:opacity-70"
                                />
                                <button
                                  type="button"
                                  onClick={() => toggleRevealSecret(item.id)}
                                  className="absolute right-1.5 top-1/2 -translate-y-1/2 text-[#8d909b] hover:text-[#d9b9ff] p-0.5 cursor-pointer"
                                  title={isRevealed ? 'Mask secret' : 'Reveal secret'}
                                >
                                  <span className="material-symbols-outlined text-[14px]">
                                    {isRevealed ? 'visibility_off' : 'visibility'}
                                  </span>
                                </button>
                              </div>
                              <span
                                className="px-1.5 py-0.5 rounded text-[10px] font-mono whitespace-nowrap bg-[#d9b9ff]/15 text-[#d9b9ff] border border-[#d9b9ff]/30 shrink-0"
                                title="Stored with secret masking or Ansible Vault encryption"
                              >
                                {item.isVault ? 'Vault Encrypted' : 'Secret'}
                              </span>
                            </div>
                          </div>
                        ) : isNum ? (
                          <input
                            type="number"
                            value={item.value ?? ''}
                            onChange={e => handleUpdateValue(item.id, e.target.value)}
                            disabled={readOnly}
                            className="w-full bg-[#171a2d] border border-[#26293c] focus:border-[#b0c9ff] rounded px-2 py-1 text-xs font-mono text-[#dfe1fa] outline-none disabled:opacity-70"
                          />
                        ) : Array.isArray(item.value) ? (
                          <div className="space-y-1">
                            <input
                              type="text"
                              value={item.value.join(', ')}
                              onChange={e =>
                                handleUpdateValue(
                                  item.id,
                                  e.target.value
                                    .split(',')
                                    .map(s => s.trim())
                                    .filter(Boolean)
                                )
                              }
                              disabled={readOnly}
                              placeholder="comma, separated, items"
                              className="w-full bg-[#171a2d] border border-[#26293c] focus:border-[#b0c9ff] rounded px-2 py-1 text-xs font-mono text-[#dfe1fa] outline-none disabled:opacity-70"
                            />
                            <div className="text-[10px] text-[#8d909b] flex items-center gap-1">
                              <span>List items ({item.value.length})</span>
                            </div>
                          </div>
                        ) : (
                          <input
                            type="text"
                            value={String(item.value ?? '')}
                            onChange={e => handleUpdateValue(item.id, e.target.value)}
                            disabled={readOnly}
                            placeholder="Value"
                            className="w-full bg-[#171a2d] border border-[#26293c] focus:border-[#b0c9ff] rounded px-2 py-1 text-xs font-mono text-[#dfe1fa] outline-none disabled:opacity-70"
                          />
                        )}
                      </td>

                      {/* Action Column */}
                      {!readOnly && (
                        <td className="py-1.5 px-2 align-top text-center">
                          <button
                            type="button"
                            onClick={() => handleDeleteVariable(item.id)}
                            className="text-[#8d909b] hover:text-[#ffb4ab] p-1 rounded hover:bg-[#ffb4ab]/10 cursor-pointer transition-colors"
                            title="Delete variable"
                          >
                            <span className="material-symbols-outlined text-[15px]">delete</span>
                          </button>
                        </td>
                      )}
                    </tr>
                  );
                })}
              </tbody>
            </table>
          )}
        </div>
      ) : (
        /* Raw YAML Editor View */
        <div className="flex-1 flex overflow-hidden bg-[#0a0d1f] font-mono text-xs">
          {/* Line Numbers Gutter */}
          <div className="w-10 bg-[#121528] py-2 px-1 text-right text-[#8d909b]/50 select-none border-r border-[#26293c] shrink-0 font-mono text-[11px] overflow-hidden">
            {yamlLines.map(lineNum => (
              <div key={lineNum} className="leading-5 h-5">
                {lineNum}
              </div>
            ))}
          </div>

          {/* Text Area */}
          <div className="flex-1 p-2 overflow-auto">
            <textarea
              value={yamlText}
              onChange={e => handleYamlTextChange(e.target.value)}
              disabled={readOnly}
              className="w-full h-full bg-transparent text-[#dfe1fa] focus:outline-none font-mono text-xs resize-none leading-5 whitespace-pre selection:bg-[#b0c9ff]/30"
              spellCheck={false}
              autoCapitalize="off"
              autoComplete="off"
            />
          </div>
        </div>
      )}

      {/* Footer Status Bar */}
      <div className="h-7 bg-[#121528] px-3 flex items-center justify-between border-t border-[#26293c] text-[10px] text-[#8d909b] shrink-0">
        <div className="flex items-center gap-3">
          <span>Mode: {mode === 'structured' ? 'Structured Table' : 'YAML Stream'}</span>
          <span>•</span>
          <span>{vars.length} entries</span>
        </div>

        <div className="flex items-center gap-2">
          {onSave && !readOnly && (
            <button
              type="button"
              onClick={onSave}
              className="px-2 py-0.5 bg-[#b0c9ff] text-[#002e68] font-bold rounded hover:bg-[#d8e2ff] cursor-pointer"
            >
              Save
            </button>
          )}
        </div>
      </div>
    </div>
  );
};
