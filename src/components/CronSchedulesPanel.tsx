import React, { useCallback, useEffect, useMemo, useState } from 'react';
import type { NodeItem } from '../types/index.ts';
import type {
  CronSchedule,
  CronScopeKind,
  CronTaskKind,
} from '../types/cron.ts';

export interface CronSchedulesPanelProps {
  nodes: NodeItem[];
}

export interface ScheduleFormState {
  id?: string;
  name: string;
  cronExpression: string;
  enabled: boolean;
  scopeKind: CronScopeKind;
  scopeTarget: string;
  taskKind: CronTaskKind;
  playbook: string;
  stepsJson: string;
}

const EMPTY_FORM: ScheduleFormState = {
  name: '',
  cronExpression: '0 3 * * *',
  enabled: true,
  scopeKind: 'host',
  scopeTarget: '',
  taskKind: 'playbook',
  playbook: 'site.yml',
  stepsJson: JSON.stringify(
    [
      {
        name: 'Restart managed service',
        module: 'ansible.builtin.systemd',
        parameters: { name: 'podman.socket', state: 'restarted' },
      },
    ],
    null,
    2
  ),
};

export function formatCronRunTime(value?: number | null): string {
  if (!value) return '—';
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return '—';
  return date.toLocaleString(undefined, {
    month: 'short',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  });
}

export function describeScheduleScope(schedule: Pick<CronSchedule, 'scope'>): string {
  const { kind, target } = schedule.scope;
  if (kind === 'host') return `Host: ${target}`;
  if (kind === 'group') return `Group: ${target.startsWith('tag:') ? target : `tag:${target}`}`;
  return `Datacenter: ${target || 'all'}`;
}

export function describeScheduleWork(schedule: Pick<CronSchedule, 'taskKind' | 'payload'>): string {
  if (schedule.taskKind === 'playbook') {
    const payload = schedule.payload as { playbook?: string };
    return `ansible-playbook ${payload.playbook || '(missing)'}`;
  }
  const payload = schedule.payload as { title?: string; steps?: unknown[] };
  return `${payload.title || 'Ansible steps'} (${payload.steps?.length ?? 0} step${
    payload.steps?.length === 1 ? '' : 's'
  })`;
}

export function buildSchedulePayload(form: ScheduleFormState): {
  body?: Record<string, unknown>;
  error?: string;
} {
  if (!form.name.trim()) return { error: 'Name is required' };
  if (!form.cronExpression.trim()) return { error: 'Cron expression is required' };
  if (form.scopeKind !== 'datacenter' && !form.scopeTarget.trim()) {
    return { error: `Target is required for ${form.scopeKind} scopes` };
  }

  if (form.taskKind === 'playbook') {
    if (!form.playbook.trim()) return { error: 'Playbook name is required' };
    return {
      body: {
        name: form.name.trim(),
        cronExpression: form.cronExpression.trim(),
        enabled: form.enabled,
        scope: { kind: form.scopeKind, target: form.scopeTarget.trim() || 'all' },
        taskKind: 'playbook',
        payload: { playbook: form.playbook.trim() },
      },
    };
  }

  let steps: unknown;
  try {
    steps = JSON.parse(form.stepsJson);
  } catch (err: any) {
    return { error: `Steps JSON is invalid: ${err?.message || 'parse error'}` };
  }
  if (!Array.isArray(steps) || steps.length === 0) {
    return { error: 'At least one Ansible step is required' };
  }

  return {
    body: {
      name: form.name.trim(),
      cronExpression: form.cronExpression.trim(),
      enabled: form.enabled,
      scope: { kind: form.scopeKind, target: form.scopeTarget.trim() || 'all' },
      taskKind: 'ansible_steps',
      payload: { title: form.name.trim(), steps },
    },
  };
}

export const CronSchedulesPanel: React.FC<CronSchedulesPanelProps> = ({ nodes }) => {
  const [schedules, setSchedules] = useState<CronSchedule[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [busyId, setBusyId] = useState<string | null>(null);
  const [form, setForm] = useState<ScheduleFormState | null>(null);
  const [formError, setFormError] = useState<string | null>(null);
  const [playbooks, setPlaybooks] = useState<string[]>([]);

  const loadSchedules = useCallback(() => {
    fetch('/api/cron/schedules')
      .then(res => res.json())
      .then(data => {
        if (data?.success && Array.isArray(data.schedules)) {
          setSchedules(data.schedules);
          setError(null);
        } else {
          setError(data?.error || 'Failed to load schedules');
        }
      })
      .catch(err => setError(err?.message || 'Failed to load schedules'))
      .finally(() => setLoading(false));
  }, []);

  useEffect(() => {
    loadSchedules();
    const timer = setInterval(loadSchedules, 20000);
    return () => clearInterval(timer);
  }, [loadSchedules]);

  useEffect(() => {
    fetch('/api/playbooks')
      .then(res => res.json())
      .then(data => {
        if (Array.isArray(data?.playbooks)) setPlaybooks(data.playbooks);
      })
      .catch(() => {});
  }, []);

  const hostOptions = useMemo(
    () => nodes.map(node => node.shortName || node.name || node.id).filter(Boolean),
    [nodes]
  );

  const groupOptions = useMemo(() => {
    const tags = new Set<string>();
    for (const node of nodes) {
      for (const tag of node.tags || []) tags.add(tag.startsWith('tag:') ? tag : `tag:${tag}`);
      if (node.tailscaleTag) tags.add(`tag:${node.tailscaleTag.replace(/^tag:/, '')}`);
    }
    return [...tags].sort();
  }, [nodes]);

  const openCreate = () => {
    setFormError(null);
    setForm({ ...EMPTY_FORM, playbook: playbooks[0] || 'site.yml' });
  };

  const openEdit = (schedule: CronSchedule) => {
    setFormError(null);
    const steps =
      schedule.taskKind === 'ansible_steps'
        ? JSON.stringify((schedule.payload as { steps?: unknown }).steps ?? [], null, 2)
        : EMPTY_FORM.stepsJson;
    setForm({
      id: schedule.id,
      name: schedule.name,
      cronExpression: schedule.cronExpression,
      enabled: schedule.enabled,
      scopeKind: schedule.scope.kind,
      scopeTarget: schedule.scope.target,
      taskKind: schedule.taskKind,
      playbook: (schedule.payload as { playbook?: string }).playbook || 'site.yml',
      stepsJson: steps,
    });
  };

  const submitForm = async (event: React.FormEvent) => {
    event.preventDefault();
    if (!form) return;
    const { body, error: buildError } = buildSchedulePayload(form);
    if (buildError || !body) {
      setFormError(buildError || 'Invalid schedule');
      return;
    }

    const editing = Boolean(form.id);
    const res = await fetch(editing ? `/api/cron/schedules/${form.id}` : '/api/cron/schedules', {
      method: editing ? 'PUT' : 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok || !data?.success) {
      setFormError(data?.error || `Failed to ${editing ? 'update' : 'create'} schedule`);
      return;
    }
    setForm(null);
    setFormError(null);
    loadSchedules();
  };

  const toggleEnabled = async (schedule: CronSchedule) => {
    setBusyId(schedule.id);
    try {
      await fetch(`/api/cron/schedules/${schedule.id}`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ enabled: !schedule.enabled }),
      });
      loadSchedules();
    } finally {
      setBusyId(null);
    }
  };

  const runNow = async (schedule: CronSchedule) => {
    setBusyId(schedule.id);
    try {
      const res = await fetch(`/api/cron/schedules/${schedule.id}/run`, { method: 'POST' });
      const data = await res.json().catch(() => ({}));
      if (!data?.success) {
        setError(data?.run?.error || data?.error || 'Run failed');
      } else {
        setError(null);
      }
      loadSchedules();
    } finally {
      setBusyId(null);
    }
  };

  const remove = async (schedule: CronSchedule) => {
    if (!window.confirm(`Delete schedule "${schedule.name}"?`)) return;
    setBusyId(schedule.id);
    try {
      await fetch(`/api/cron/schedules/${schedule.id}`, { method: 'DELETE' });
      loadSchedules();
    } finally {
      setBusyId(null);
    }
  };

  return (
    <div
      id="datacenter-schedules"
      data-testid="datacenter-cron-schedules"
      className="rounded-lg bg-[#121528] border border-[#26293c] p-4 shadow-md flex flex-col gap-3"
    >
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="flex items-center gap-2">
          <span className="material-symbols-outlined text-[18px] text-[#d9b9ff]">schedule</span>
          <h2 className="text-xs font-mono font-bold text-[#dfe1fa] uppercase tracking-wider">
            Scheduled Playbooks &amp; Tasks
          </h2>
          <span className="text-[10px] font-mono px-1.5 py-0.2 rounded bg-[#21263d] text-[#d9b9ff] border border-[#d9b9ff]/30">
            {schedules.filter(s => s.enabled).length}/{schedules.length} enabled
          </span>
          <span className="text-[10px] font-mono text-[#8d909b]">
            driven by the ATC scheduler (in-process)
          </span>
        </div>
        <button
          type="button"
          onClick={openCreate}
          className="px-2.5 py-1 rounded bg-[#1f2238] hover:bg-[#2c304d] text-[#8cd6cb] border border-[#3b405e] text-[11px] font-mono cursor-pointer"
        >
          + New Schedule
        </button>
      </div>

      {error && (
        <div className="p-2 rounded bg-[#311c1c] border border-[#ffb4ab]/30 text-[#ffb4ab] text-[11px] font-mono">
          {error}
        </div>
      )}

      {form && (
        <form
          onSubmit={submitForm}
          className="p-3 rounded bg-[#171a2d] border border-[#26293c] grid grid-cols-1 md:grid-cols-2 gap-2 font-mono text-[11px]"
        >
          <label className="flex flex-col gap-1 text-[#c3c6d2]">
            Name
            <input
              value={form.name}
              onChange={e => setForm({ ...form, name: e.target.value })}
              className="bg-[#0f1224] border border-[#3b405e] rounded px-2 py-1 text-[#dfe1fa]"
              placeholder="Nightly baseline"
            />
          </label>
          <label className="flex flex-col gap-1 text-[#c3c6d2]">
            Cron expression (minute hour dom month dow)
            <input
              value={form.cronExpression}
              onChange={e => setForm({ ...form, cronExpression: e.target.value })}
              className="bg-[#0f1224] border border-[#3b405e] rounded px-2 py-1 text-[#dfe1fa]"
              placeholder="0 3 * * *"
            />
          </label>
          <label className="flex flex-col gap-1 text-[#c3c6d2]">
            Scope
            <select
              value={form.scopeKind}
              onChange={e => setForm({ ...form, scopeKind: e.target.value as CronScopeKind })}
              className="bg-[#0f1224] border border-[#3b405e] rounded px-2 py-1 text-[#dfe1fa]"
            >
              <option value="host">Single host</option>
              <option value="group">Host group (tag)</option>
              <option value="datacenter">Datacenter-wide</option>
            </select>
          </label>
          <label className="flex flex-col gap-1 text-[#c3c6d2]">
            Target
            <input
              list={form.scopeKind === 'group' ? 'cron-group-options' : 'cron-host-options'}
              value={form.scopeTarget}
              disabled={form.scopeKind === 'datacenter'}
              onChange={e => setForm({ ...form, scopeTarget: e.target.value })}
              className="bg-[#0f1224] border border-[#3b405e] rounded px-2 py-1 text-[#dfe1fa] disabled:opacity-50"
              placeholder={form.scopeKind === 'datacenter' ? 'all' : 'host name or tag'}
            />
            <datalist id="cron-host-options">
              {hostOptions.map(host => (
                <option key={host} value={host} />
              ))}
            </datalist>
            <datalist id="cron-group-options">
              {groupOptions.map(group => (
                <option key={group} value={group} />
              ))}
            </datalist>
          </label>
          <label className="flex flex-col gap-1 text-[#c3c6d2]">
            Work to run
            <select
              value={form.taskKind}
              onChange={e => setForm({ ...form, taskKind: e.target.value as CronTaskKind })}
              className="bg-[#0f1224] border border-[#3b405e] rounded px-2 py-1 text-[#dfe1fa]"
            >
              <option value="playbook">Ansible playbook</option>
              <option value="ansible_steps">Declarative Ansible steps (mutation queue)</option>
            </select>
          </label>
          {form.taskKind === 'playbook' ? (
            <label className="flex flex-col gap-1 text-[#c3c6d2]">
              Playbook
              <select
                value={form.playbook}
                onChange={e => setForm({ ...form, playbook: e.target.value })}
                className="bg-[#0f1224] border border-[#3b405e] rounded px-2 py-1 text-[#dfe1fa]"
              >
                {(playbooks.length > 0 ? playbooks : [form.playbook]).map(name => (
                  <option key={name} value={name}>
                    {name}
                  </option>
                ))}
              </select>
            </label>
          ) : (
            <label className="flex flex-col gap-1 text-[#c3c6d2] md:col-span-2">
              Steps JSON (AnsibleTaskStep[])
              <textarea
                value={form.stepsJson}
                onChange={e => setForm({ ...form, stepsJson: e.target.value })}
                rows={6}
                className="bg-[#0f1224] border border-[#3b405e] rounded px-2 py-1 text-[#dfe1fa] font-mono"
              />
            </label>
          )}

          <label className="flex items-center gap-2 text-[#c3c6d2]">
            <input
              type="checkbox"
              checked={form.enabled}
              onChange={e => setForm({ ...form, enabled: e.target.checked })}
            />
            Enabled
          </label>

          {formError && (
            <div className="md:col-span-2 p-2 rounded bg-[#311c1c] border border-[#ffb4ab]/30 text-[#ffb4ab]">
              {formError}
            </div>
          )}

          <div className="md:col-span-2 flex items-center gap-2 pt-1">
            <button
              type="submit"
              className="px-3 py-1 rounded bg-[#1f3a2d] hover:bg-[#25483a] text-[#8cd6cb] border border-[#8cd6cb]/30 cursor-pointer"
            >
              {form.id ? 'Save changes' : 'Create schedule'}
            </button>
            <button
              type="button"
              onClick={() => {
                setForm(null);
                setFormError(null);
              }}
              className="px-3 py-1 rounded bg-[#1f2238] hover:bg-[#2c304d] text-[#c3c6d2] border border-[#3b405e] cursor-pointer"
            >
              Cancel
            </button>
          </div>
        </form>
      )}

      {loading ? (
        <div className="p-3 text-center text-[11px] font-mono text-[#8d909b]">Loading schedules…</div>
      ) : schedules.length === 0 ? (
        <div className="p-3 text-center text-[11px] font-mono text-[#8d909b]">
          No schedules yet — create one to run a playbook or declarative task on a cron expression.
        </div>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full text-[11px] font-mono border-collapse">
            <thead>
              <tr className="text-left text-[#8d909b] border-b border-[#26293c]">
                <th className="py-1.5 pr-2">Name</th>
                <th className="py-1.5 pr-2">Cron</th>
                <th className="py-1.5 pr-2">Scope</th>
                <th className="py-1.5 pr-2">Work</th>
                <th className="py-1.5 pr-2">Last run</th>
                <th className="py-1.5 pr-2">Next run</th>
                <th className="py-1.5 pr-2">Actions</th>
              </tr>
            </thead>
            <tbody>
              {schedules.map(schedule => (
                <tr key={schedule.id} className="border-b border-[#1f2238] align-top">
                  <td className="py-1.5 pr-2 text-[#dfe1fa] font-semibold">
                    {schedule.name}
                    {!schedule.enabled && (
                      <span className="ml-1.5 px-1 py-0.2 rounded text-[9px] bg-[#21263d] text-[#8d909b] border border-[#3b405e]">
                        DISABLED
                      </span>
                    )}
                  </td>
                  <td className="py-1.5 pr-2 text-[#b0c9ff]">{schedule.cronExpression}</td>
                  <td className="py-1.5 pr-2 text-[#c3c6d2]">{describeScheduleScope(schedule)}</td>
                  <td className="py-1.5 pr-2 text-[#c3c6d2]">{describeScheduleWork(schedule)}</td>
                  <td className="py-1.5 pr-2">
                    {schedule.lastRunAt ? (
                      <span
                        className={
                          schedule.lastRunStatus === 'OK'
                            ? 'text-[#8cd6cb]'
                            : schedule.lastRunStatus === 'FAILED'
                            ? 'text-[#ffb4ab]'
                            : 'text-[#d9b9ff]'
                        }
                        title={schedule.lastRunError || ''}
                      >
                        {formatCronRunTime(schedule.lastRunAt)} · {schedule.lastRunStatus || '—'}
                      </span>
                    ) : (
                      <span className="text-[#8d909b]">never</span>
                    )}
                  </td>
                  <td className="py-1.5 pr-2 text-[#c3c6d2]">
                    {schedule.enabled ? formatCronRunTime(schedule.nextRunAt) : 'paused'}
                  </td>
                  <td className="py-1.5 pr-2">
                    <div className="flex flex-wrap items-center gap-1">
                      <button
                        type="button"
                        disabled={busyId === schedule.id}
                        onClick={() => toggleEnabled(schedule)}
                        className="px-1.5 py-0.5 rounded bg-[#1f2238] hover:bg-[#2c304d] text-[#b0c9ff] border border-[#3b405e] cursor-pointer disabled:opacity-50"
                      >
                        {schedule.enabled ? 'Disable' : 'Enable'}
                      </button>
                      <button
                        type="button"
                        disabled={busyId === schedule.id}
                        onClick={() => runNow(schedule)}
                        className="px-1.5 py-0.5 rounded bg-[#1f3a2d] hover:bg-[#25483a] text-[#8cd6cb] border border-[#8cd6cb]/30 cursor-pointer disabled:opacity-50"
                      >
                        Run now
                      </button>
                      <button
                        type="button"
                        onClick={() => openEdit(schedule)}
                        className="px-1.5 py-0.5 rounded bg-[#1f2238] hover:bg-[#2c304d] text-[#c3c6d2] border border-[#3b405e] cursor-pointer"
                      >
                        Edit
                      </button>
                      <button
                        type="button"
                        disabled={busyId === schedule.id}
                        onClick={() => remove(schedule)}
                        className="px-1.5 py-0.5 rounded bg-[#311c1c] hover:bg-[#3d2222] text-[#ffb4ab] border border-[#ffb4ab]/30 cursor-pointer disabled:opacity-50"
                      >
                        Delete
                      </button>
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
};
