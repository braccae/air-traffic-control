import React, { useCallback, useEffect, useMemo, useState } from 'react';
import {
  cloneDocument,
  coerceFieldValue,
  createTaskForModule,
  describeTaskShape,
  fieldValueToText,
  getPlayLabel,
  getPlayTasks,
  getTaskArgs,
  getTaskDirectives,
  getTaskModule,
  optionFieldKind,
  setTaskArg,
  setTaskDirective,
  type AnsibleModuleSpec,
  type FieldKind,
  type ModuleCatalogEntry,
  type ModuleSpecOption,
} from '../utils/playbookVisualModel.ts';

/**
 * Form-based (visual) editor for real Ansible playbooks.
 *
 * The document it renders is the output of the coordinator's PyYAML bridge
 * (POST /api/playbooks/parse) and every mutation is handed back to the parent,
 * which re-serialises the whole document — so the visual form and the YAML text
 * are always the same playbook, never two copies.
 *
 * Parameter fields are not guessed: they come from `ansible-doc -j <module>`
 * through GET /api/ansible/modules and GET /api/ansible/modules/:name/spec.
 * Until a spec is loaded the editor says so instead of offering a generic
 * textarea (Zero-Mock Policy, AGENTS.md §2).
 */

export interface PlaybookVisualEditorProps {
  /** Parsed playbook document (array of plays) from POST /api/playbooks/parse. */
  document: any[];
  /** Receives the whole mutated document; the parent serialises and saves it. */
  onChange: (next: any[]) => void;
  readOnly?: boolean;
  /** Optional prefetched specs — lets a caller (or a test) seed known specs. */
  preloadedSpecs?: Record<string, AnsibleModuleSpec>;
}

const specCache = new Map<string, AnsibleModuleSpec>();
const catalogState: { entries: ModuleCatalogEntry[] | null; loading: Promise<ModuleCatalogEntry[]> | null } = {
  entries: null,
  loading: null,
};

async function loadCatalog(): Promise<ModuleCatalogEntry[]> {
  if (catalogState.entries) return catalogState.entries;
  if (catalogState.loading) return catalogState.loading;

  catalogState.loading = (async () => {
    const res = await fetch('/api/ansible/modules');
    const body = await res.json().catch(() => null);
    if (!res.ok || !body?.success) {
      throw new Error(body?.error || `GET /api/ansible/modules returned ${res.status} ${res.statusText}`);
    }
    const entries: ModuleCatalogEntry[] = Array.isArray(body.modules) ? body.modules : [];
    catalogState.entries = entries;
    return entries;
  })();

  try {
    return await catalogState.loading;
  } finally {
    catalogState.loading = null;
  }
}

async function loadModuleSpec(moduleName: string): Promise<AnsibleModuleSpec> {
  const cached = specCache.get(moduleName);
  if (cached) return cached;

  const res = await fetch(`/api/ansible/modules/${encodeURIComponent(moduleName)}/spec`);
  const body = await res.json().catch(() => null);
  if (!res.ok || !body?.success || !body?.spec) {
    throw new Error(body?.error || `GET /api/ansible/modules/${moduleName}/spec returned ${res.status}`);
  }
  const spec = body.spec as AnsibleModuleSpec;
  specCache.set(moduleName, spec);
  specCache.set(spec.name, spec);
  return spec;
}

/** Exposed for tests that need a clean module-spec cache. */
export function clearVisualEditorCaches(): void {
  specCache.clear();
  catalogState.entries = null;
  catalogState.loading = null;
}

const FIELD_CLASS =
  'w-full bg-[#121528] border border-[#26293c] focus:border-[#b0c9ff] rounded px-2 py-1 text-[11px] font-mono text-[#dfe1fa] placeholder-[#5b5f78] outline-none disabled:opacity-60';

function fieldInputId(playIndex: number, taskIndex: number, field: string): string {
  return `pb-play-${playIndex}-task-${taskIndex}-${field}`;
}

interface FieldControlProps {
  option: ModuleSpecOption;
  value: any;
  disabled: boolean;
  id: string;
  onChange: (next: any) => void;
}

const FieldControl: React.FC<FieldControlProps> = ({ option, value, disabled, id, onChange }) => {
  const kind: FieldKind = optionFieldKind(option);
  const text = fieldValueToText(value);

  if (kind === 'select') {
    const choices = option.choices || [];
    const current = value === undefined || value === null ? '' : String(value);
    return (
      <select
        id={id}
        value={current}
        disabled={disabled}
        onChange={e => onChange(e.target.value === '' ? undefined : e.target.value)}
        className={FIELD_CLASS}
        data-field-name={option.name}
        data-field-kind="select"
      >
        <option value="">(unset{option.default !== undefined ? ` · default ${String(option.default)}` : ''})</option>
        {choices.map(choice => (
          <option key={choice} value={choice}>
            {choice}
          </option>
        ))}
      </select>
    );
  }

  if (kind === 'boolean') {
    const current = value === undefined || value === null ? '' : value === true ? 'true' : 'false';
    return (
      <select
        id={id}
        value={current}
        disabled={disabled}
        onChange={e => onChange(e.target.value === '' ? undefined : e.target.value === 'true')}
        className={FIELD_CLASS}
        data-field-name={option.name}
        data-field-kind="boolean"
      >
        <option value="">(unset)</option>
        <option value="true">true</option>
        <option value="false">false</option>
      </select>
    );
  }

  if (kind === 'number') {
    return (
      <input
        id={id}
        type="number"
        value={text}
        disabled={disabled}
        onChange={e => onChange(coerceFieldValue('number', e.target.value))}
        placeholder={option.default !== undefined ? `default: ${String(option.default)}` : 'unset'}
        className={FIELD_CLASS}
        data-field-name={option.name}
        data-field-kind="number"
      />
    );
  }

  if (kind === 'list' || kind === 'dict') {
    return (
      <textarea
        id={id}
        value={text}
        disabled={disabled}
        rows={kind === 'list' ? Math.min(4, Math.max(2, text.split('\n').length)) : 4}
        onChange={e => onChange(coerceFieldValue(kind, e.target.value))}
        placeholder={
          kind === 'list'
            ? 'one value per line'
            : option.default !== undefined
            ? `default: ${JSON.stringify(option.default)}`
            : 'JSON object'
        }
        className={`${FIELD_CLASS} resize-y leading-4`}
        data-field-name={option.name}
        data-field-kind={kind}
      />
    );
  }

  return (
    <input
      id={id}
      type="text"
      value={text}
      disabled={disabled}
      onChange={e => onChange(coerceFieldValue('text', e.target.value))}
      placeholder={option.default !== undefined ? `default: ${String(option.default)}` : 'unset'}
      className={FIELD_CLASS}
      data-field-name={option.name}
      data-field-kind="text"
    />
  );
};

interface TaskFieldsProps {
  playIndex: number;
  taskIndex: number;
  task: any;
  moduleName: string;
  spec: AnsibleModuleSpec | null;
  specError: string | null;
  readOnly: boolean;
  onArgChange: (argName: string, value: any) => void;
  onDirectiveChange: (key: string, value: any) => void;
  onRemove: () => void;
  onMove: (delta: number) => void;
}

const TaskFields: React.FC<TaskFieldsProps> = ({
  playIndex,
  taskIndex,
  task,
  moduleName,
  spec,
  specError,
  readOnly,
  onArgChange,
  onDirectiveChange,
  onRemove,
  onMove,
}) => {
  const [showOptional, setShowOptional] = useState(false);
  const args = getTaskArgs(task, moduleName);
  const directives = getTaskDirectives(task, moduleName);
  const nameValue = typeof task?.name === 'string' ? task.name : '';
  const shapeNote = describeTaskShape(task);

  const options = spec?.options || [];
  const requiredOptions = options.filter(option => option.required);
  const setOptionalOptions = options.filter(option => !option.required && args[option.name] !== undefined);
  const hiddenOptionalOptions = options.filter(option => !option.required && args[option.name] === undefined);
  const visibleOptions = [...requiredOptions, ...setOptionalOptions, ...(showOptional ? hiddenOptionalOptions : [])];

  return (
    <div
      className="rounded border border-[#26293c] bg-[#0f1224] p-2.5 flex flex-col gap-2"
      data-testid="visual-task-card"
      data-module={moduleName}
      data-task-index={taskIndex}
    >
      <div className="flex items-center justify-between gap-2">
        <div className="flex items-center gap-1.5 min-w-0">
          <span className="material-symbols-outlined text-[14px] text-[#8cd6cb]">extension</span>
          <span className="font-mono text-[11px] text-[#8cd6cb] truncate" title={moduleName}>
            {moduleName}
          </span>
          {spec && (
            <span className="text-[10px] font-mono text-[#8d909b] truncate" title={spec.shortDescription}>
              — {spec.shortDescription}
            </span>
          )}
          {!spec && !specError && <span className="text-[10px] font-mono text-[#8d909b]">loading module spec…</span>}
          {specError && (
            <span className="text-[10px] font-mono text-[#ffb4ab] truncate" title={specError}>
              spec unavailable: {specError}
            </span>
          )}
        </div>
        <div className="flex items-center gap-1 shrink-0">
          <button
            type="button"
            onClick={() => onMove(-1)}
            disabled={readOnly || taskIndex === 0}
            className="px-1 py-0.5 rounded text-[10px] font-mono bg-[#1b1e31] border border-[#26293c] text-[#8d909b] hover:text-[#dfe1fa] disabled:opacity-40 cursor-pointer"
            title="Move task up"
          >
            <span className="material-symbols-outlined text-[13px]">arrow_upward</span>
          </button>
          <button
            type="button"
            onClick={() => onMove(1)}
            disabled={readOnly}
            className="px-1 py-0.5 rounded text-[10px] font-mono bg-[#1b1e31] border border-[#26293c] text-[#8d909b] hover:text-[#dfe1fa] disabled:opacity-40 cursor-pointer"
            title="Move task down"
          >
            <span className="material-symbols-outlined text-[13px]">arrow_downward</span>
          </button>
          <button
            type="button"
            onClick={onRemove}
            disabled={readOnly}
            className="px-1 py-0.5 rounded text-[10px] font-mono bg-[#1b1e31] border border-[#26293c] text-[#8d909b] hover:text-[#ffb4ab] disabled:opacity-40 cursor-pointer"
            title="Remove this task"
          >
            <span className="material-symbols-outlined text-[13px]">delete</span>
          </button>
        </div>
      </div>

      {shapeNote && (
        <div className="text-[10px] font-mono text-[#ffd580] bg-[#ffd580]/10 border border-[#ffd580]/20 rounded px-2 py-1">
          {shapeNote} The YAML view shows the full definition.
        </div>
      )}

      <div className="grid grid-cols-1 md:grid-cols-2 gap-2">
        <label className="flex flex-col gap-0.5" htmlFor={fieldInputId(playIndex, taskIndex, 'name')}>
          <span className="text-[10px] font-mono uppercase tracking-wider text-[#8d909b]">task name</span>
          <input
            id={fieldInputId(playIndex, taskIndex, 'name')}
            type="text"
            value={nameValue}
            disabled={readOnly}
            onChange={e => onDirectiveChange('name', e.target.value)}
            placeholder="Describe the step"
            className={FIELD_CLASS}
            data-field-name="name"
            data-field-kind="text"
          />
        </label>

        <label className="flex flex-col gap-0.5" htmlFor={fieldInputId(playIndex, taskIndex, 'when')}>
          <span className="text-[10px] font-mono uppercase tracking-wider text-[#8d909b]">when</span>
          <input
            id={fieldInputId(playIndex, taskIndex, 'when')}
            type="text"
            value={fieldValueToText(directives.when)}
            disabled={readOnly}
            onChange={e => onDirectiveChange('when', e.target.value)}
            placeholder="(always run)"
            className={FIELD_CLASS}
            data-field-name="when"
          />
        </label>
      </div>

      <div className="flex flex-wrap gap-2">
        {(['register', 'tags', 'changed_when', 'loop'] as const).map(key => (
          <label
            key={key}
            className="flex flex-col gap-0.5 flex-1 min-w-[130px]"
            htmlFor={fieldInputId(playIndex, taskIndex, key)}
          >
            <span className="text-[10px] font-mono uppercase tracking-wider text-[#8d909b]">{key}</span>
            <input
              id={fieldInputId(playIndex, taskIndex, key)}
              type="text"
              value={fieldValueToText(directives[key])}
              disabled={readOnly}
              onChange={e => onDirectiveChange(key, e.target.value)}
              placeholder="unset"
              className={FIELD_CLASS}
              data-field-name={key}
            />
          </label>
        ))}
      </div>

      <div className="flex flex-col gap-1.5 border-t border-[#1f2238] pt-2">
        <div className="flex items-center justify-between gap-2">
          <span className="text-[10px] font-mono uppercase tracking-wider text-[#8d909b]">
            {spec ? `parameters — ${visibleOptions.length}/${options.length} shown` : 'parameters'}
          </span>
          {spec && hiddenOptionalOptions.length > 0 && (
            <button
              type="button"
              onClick={() => setShowOptional(v => !v)}
              className="text-[10px] font-mono text-[#b0c9ff] hover:underline cursor-pointer"
            >
              {showOptional ? 'hide optional fields' : `show ${hiddenOptionalOptions.length} optional fields`}
            </button>
          )}
        </div>

        {!spec && !specError && (
          <div className="text-[10px] font-mono text-[#8d909b]">
            Fetching the module parameter spec from <code className="text-[#8cd6cb]">ansible-doc</code>…
          </div>
        )}

        {specError && (
          <div className="text-[10px] font-mono text-[#ffb4ab]">
            {specError} — edit this task in YAML mode; no fields are invented.
          </div>
        )}

        {spec && options.length === 0 && (
          <div className="text-[10px] font-mono text-[#8d909b]">
            {spec.name} takes no options; it is driven by task keywords only.
          </div>
        )}

        {spec && visibleOptions.length > 0 && (
          <div className="grid grid-cols-1 md:grid-cols-2 gap-2">
            {visibleOptions.map(option => {
              const id = fieldInputId(playIndex, taskIndex, `arg-${option.name}`);
              const value = args[option.name];
              return (
                <label key={option.name} className="flex flex-col gap-0.5" htmlFor={id}>
                  <span className="text-[10px] font-mono text-[#8d909b] flex items-center gap-1">
                    <span className={option.required ? 'text-[#ffd580] font-bold' : ''}>{option.name}</span>
                    <span className="text-[9px] uppercase px-1 rounded bg-[#121528] border border-[#26293c]">
                      {option.type}
                    </span>
                    {option.required && <span className="text-[9px] text-[#ffd580]">required</span>}
                    {value !== undefined && (
                      <button
                        type="button"
                        onClick={() => onArgChange(option.name, undefined)}
                        disabled={readOnly}
                        className="ml-auto text-[9px] text-[#8d909b] hover:text-[#ffb4ab] cursor-pointer"
                        title="Clear this parameter"
                      >
                        clear
                      </button>
                    )}
                  </span>
                  <FieldControl
                    id={id}
                    option={option}
                    value={value}
                    disabled={readOnly}
                    onChange={next => onArgChange(option.name, next)}
                  />
                  {option.description && (
                    <span className="text-[9px] font-mono text-[#5b5f78] line-clamp-2" title={option.description}>
                      {option.description}
                    </span>
                  )}
                </label>
              );
            })}
          </div>
        )}
      </div>
    </div>
  );
};

export const PlaybookVisualEditor: React.FC<PlaybookVisualEditorProps> = ({
  document,
  onChange,
  readOnly = false,
  preloadedSpecs,
}) => {
  const [specs, setSpecs] = useState<Record<string, AnsibleModuleSpec>>(() => ({ ...(preloadedSpecs || {}) }));
  const [specErrors, setSpecErrors] = useState<Record<string, string>>({});
  const [addingToPlay, setAddingToPlay] = useState<number | null>(null);
  const [moduleQuery, setModuleQuery] = useState('');
  const [catalog, setCatalog] = useState<ModuleCatalogEntry[] | null>(() => catalogState.entries);
  const [catalogError, setCatalogError] = useState<string | null>(null);
  const [expandedPlay, setExpandedPlay] = useState<number>(0);

  useEffect(() => {
    if (preloadedSpecs) {
      setSpecs(prev => ({ ...prev, ...preloadedSpecs }));
    }
  }, [preloadedSpecs]);

  const modulesInUse = useMemo(() => {
    const names = new Set<string>();
    for (const play of document) {
      for (const task of getPlayTasks(play)) {
        const moduleName = getTaskModule(task);
        if (moduleName) names.add(moduleName);
      }
    }
    return [...names];
  }, [document]);

  useEffect(() => {
    let cancelled = false;
    for (const moduleName of modulesInUse) {
      if (specs[moduleName] || specErrors[moduleName]) continue;
      void loadModuleSpec(moduleName)
        .then(spec => {
          if (cancelled) return;
          setSpecs(prev => ({ ...prev, [moduleName]: spec }));
          setSpecErrors(prev => {
            const next = { ...prev };
            delete next[moduleName];
            return next;
          });
        })
        .catch((err: any) => {
          if (cancelled) return;
          setSpecErrors(prev => ({ ...prev, [moduleName]: err?.message || String(err) }));
        });
    }
    return () => {
      cancelled = true;
    };
  }, [modulesInUse, specs, specErrors]);

  const mutate = useCallback(
    (fn: (next: any[]) => void) => {
      const next = cloneDocument(document);
      fn(next);
      onChange(next);
    },
    [document, onChange]
  );

  const handleAddTask = useCallback(
    async (playIndex: number, moduleName: string) => {
      let spec: AnsibleModuleSpec | null = null;
      try {
        spec = await loadModuleSpec(moduleName);
        setSpecs(prev => ({ ...prev, [moduleName]: spec as AnsibleModuleSpec }));
      } catch (err: any) {
        // Still allow the step to be added — the card reports the real failure.
        setSpecErrors(prev => ({ ...prev, [moduleName]: err?.message || String(err) }));
      }
      mutate(next => {
        const play = next[playIndex];
        if (!play || typeof play !== 'object') return;
        if (!Array.isArray(play.tasks)) play.tasks = [];
        play.tasks.push(createTaskForModule(moduleName, spec));
      });
      setAddingToPlay(null);
      setModuleQuery('');
    },
    [mutate]
  );

  const filteredCatalog = useMemo(() => {
    const entries = catalog || [];
    const query = moduleQuery.trim().toLowerCase();
    if (!query) return entries.slice(0, 60);
    return entries.filter(entry => entry.name.toLowerCase().includes(query)).slice(0, 60);
  }, [catalog, moduleQuery]);

  const openAddPanel = (playIndex: number) => {
    setAddingToPlay(playIndex);
    if (!catalog && !catalogError) {
      void loadCatalog()
        .then(entries => setCatalog(entries))
        .catch((err: any) => setCatalogError(err?.message || String(err)));
    }
  };

  return (
    <div className="flex-1 flex flex-col gap-2 overflow-auto p-2" data-testid="playbook-visual-editor">
      <div className="flex flex-wrap items-center gap-2 font-mono text-[10px] text-[#8d909b]">
        <span className="flex items-center gap-1 text-[#8cd6cb]">
          <span className="material-symbols-outlined text-[14px]">edit_note</span>
          <span>Field editor</span>
        </span>
        <span>·</span>
        <span>{document.length} play(s)</span>
        <span>·</span>
        <span>fields sourced from ansible-doc</span>
      </div>

      {document.length === 0 && (
        <div className="rounded border border-dashed border-[#26293c] p-3 text-[11px] font-mono text-[#8d909b]">
          This playbook has no plays. Add one in YAML mode, then switch back to edit its tasks as fields.
        </div>
      )}

      {document.map((play: any, playIndex: number) => {
        const tasks = getPlayTasks(play);
        const expanded = expandedPlay === playIndex;
        const hosts = typeof play?.hosts === 'string' ? play.hosts : Array.isArray(play?.hosts) ? play.hosts.join(', ') : '';
        return (
          <div
            key={playIndex}
            className="rounded border border-[#26293c] bg-[#121528] overflow-hidden"
            data-testid="visual-play-card"
            data-play-index={playIndex}
          >
            <button
              type="button"
              onClick={() => setExpandedPlay(expanded ? -1 : playIndex)}
              className="w-full flex items-center justify-between gap-2 px-2.5 py-1.5 bg-[#171a2d] border-b border-[#26293c] text-left cursor-pointer"
            >
              <span className="flex items-center gap-2 min-w-0">
                <span className="material-symbols-outlined text-[14px] text-[#8d909b]">
                  {expanded ? 'expand_more' : 'chevron_right'}
                </span>
                <span className="font-mono text-[11px] text-[#dfe1fa] truncate">{getPlayLabel(play, playIndex)}</span>
                <span className="text-[10px] font-mono text-[#8cd6cb]">hosts: {hosts || '(unset)'}</span>
              </span>
              <span className="text-[10px] font-mono text-[#8d909b]">
                {tasks.length} step{tasks.length === 1 ? '' : 's'}
              </span>
            </button>

            {expanded && (
              <div className="p-2 flex flex-col gap-2">
                {tasks.length === 0 && (
                  <div className="text-[11px] font-mono text-[#8d909b]">No steps in this play yet.</div>
                )}

                {tasks.map((task: any, taskIndex: number) => {
                  const moduleName = getTaskModule(task);
                  if (!moduleName) {
                    const note = describeTaskShape(task);
                    return (
                      <div
                        key={taskIndex}
                        className="rounded border border-[#26293c] bg-[#0f1224] p-2 text-[10px] font-mono text-[#ffd580]"
                        data-testid="visual-task-card-unsupported"
                      >
                        Step {taskIndex + 1}: {note || 'Task has no module — edit it in YAML mode.'}
                      </div>
                    );
                  }
                  const spec =
                    specs[moduleName] ??
                    Object.values(specs).find(
                      candidate => candidate.name === moduleName || candidate.requestedName === moduleName
                    ) ??
                    null;
                  return (
                    <TaskFields
                      key={taskIndex}
                      playIndex={playIndex}
                      taskIndex={taskIndex}
                      task={task}
                      moduleName={moduleName}
                      spec={spec}
                      specError={specErrors[moduleName] || null}
                      readOnly={readOnly}
                      onArgChange={(argName, value) =>
                        mutate(next => setTaskArg(next[playIndex].tasks[taskIndex], moduleName, argName, value))
                      }
                      onDirectiveChange={(key, value) =>
                        mutate(next => setTaskDirective(next[playIndex].tasks[taskIndex], key, value))
                      }
                      onRemove={() =>
                        mutate(next => {
                          next[playIndex].tasks.splice(taskIndex, 1);
                        })
                      }
                      onMove={delta =>
                        mutate(next => {
                          const list = next[playIndex].tasks;
                          const target = taskIndex + delta;
                          if (target < 0 || target >= list.length) return;
                          const [moved] = list.splice(taskIndex, 1);
                          list.splice(target, 0, moved);
                        })
                      }
                    />
                  );
                })}

                {addingToPlay === playIndex ? (
                  <div className="rounded border border-[#b0c9ff]/40 bg-[#0f1224] p-2 flex flex-col gap-1.5">
                    <div className="flex items-center gap-2">
                      <input
                        type="text"
                        autoFocus
                        value={moduleQuery}
                        onChange={e => setModuleQuery(e.target.value)}
                        placeholder="Search ansible modules (e.g. service, copy, ansible.builtin.apt)"
                        className={FIELD_CLASS}
                        data-testid="visual-module-search"
                      />
                      <button
                        type="button"
                        onClick={() => {
                          setAddingToPlay(null);
                          setModuleQuery('');
                        }}
                        className="text-[10px] font-mono text-[#8d909b] hover:text-[#dfe1fa] px-1 cursor-pointer"
                      >
                        Cancel
                      </button>
                    </div>
                    {catalogError && (
                      <div className="text-[10px] font-mono text-[#ffb4ab]">
                        Module catalogue unavailable: {catalogError}
                      </div>
                    )}
                    {!catalog && !catalogError && (
                      <div className="text-[10px] font-mono text-[#8d909b]">Reading ansible-doc module list…</div>
                    )}
                    {catalog && (
                      <div className="max-h-56 overflow-auto flex flex-col">
                        {filteredCatalog.length === 0 && (
                          <div className="text-[10px] font-mono text-[#8d909b] px-1 py-1">No module matches.</div>
                        )}
                        {filteredCatalog.map(entry => (
                          <button
                            key={entry.name}
                            type="button"
                            onClick={() => void handleAddTask(playIndex, entry.name)}
                            className="text-left px-1.5 py-1 rounded hover:bg-[#1b2136] flex items-baseline gap-2 cursor-pointer"
                            data-module-option={entry.name}
                          >
                            <span className="font-mono text-[11px] text-[#b0c9ff] shrink-0">{entry.name}</span>
                            <span className="font-mono text-[10px] text-[#8d909b] truncate">{entry.description}</span>
                          </button>
                        ))}
                      </div>
                    )}
                  </div>
                ) : (
                  <button
                    type="button"
                    onClick={() => openAddPanel(playIndex)}
                    disabled={readOnly}
                    className="self-start px-2 py-1 rounded text-[11px] font-mono text-[#8cd6cb] bg-[#171a2d] border border-[#26293c] hover:border-[#8cd6cb]/60 disabled:opacity-50 transition-colors flex items-center gap-1 cursor-pointer"
                    data-testid="visual-add-step"
                  >
                    <span className="material-symbols-outlined text-[14px]">add</span>
                    <span>Add step</span>
                  </button>
                )}
              </div>
            )}
          </div>
        );
      })}
    </div>
  );
};
