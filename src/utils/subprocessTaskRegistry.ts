import { spawn, type ChildProcess } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';

/**
 * Subprocess task registry.
 *
 * Every real long-running child process (Ansible playbooks, queue playbook
 * runs, long probes) is spawned through this registry so the console's
 * "Active Tasks" tab can list it and stream its stdout/stderr live.
 *
 * Zero-Mock Policy: this module only ever reports data that a real child
 * process produced. Nothing here synthesises progress, output, or status.
 */

export type SubprocessTaskStatus = 'RUNNING' | 'OK' | 'FAILED';

export type SubprocessOutputStream = 'stdout' | 'stderr' | 'system';

export interface SubprocessTaskInfo {
  id: string;
  label: string;
  kind: string;
  commandLine: string;
  nodeTarget: string;
  user: string;
  description: string;
  status: SubprocessTaskStatus;
  startTime: string;
  endTime?: string;
  exitCode?: number | null;
  pid?: number;
  lineCount: number;
  truncated: boolean;
  /** True when the process was terminated through the registry (not a failure). */
  cancelled?: boolean;
}

export interface SubprocessOutputChunk {
  seq: number;
  stream: SubprocessOutputStream;
  data: string;
}

export type SubprocessStreamEvent =
  | { type: 'output'; taskId: string; seq: number; stream: SubprocessOutputStream; data: string }
  | { type: 'status'; task: SubprocessTaskInfo }
  | { type: 'exit'; task: SubprocessTaskInfo; exitCode: number | null };

export interface SpawnSubprocessOptions {
  id?: string;
  label: string;
  kind?: string;
  command: string;
  args?: string[];
  nodeTarget?: string;
  user?: string;
  description?: string;
  cwd?: string;
  env?: Record<string, string | undefined>;
  /** Working directory used for on-disk captured output. */
  logDir?: string;
}

export interface SubprocessRegistryOptions {
  /** Max captured characters kept in memory per task (oldest chunks dropped first). */
  maxCapturedChars?: number;
  /** How many finished tasks stay addressable in memory. */
  maxFinishedTasks?: number;
  /** Persist captured output for finished tasks under this directory. */
  logDir?: string;
  persist?: boolean;
}

interface TaskRecord {
  info: SubprocessTaskInfo;
  proc: ChildProcess | null;
  chunks: SubprocessOutputChunk[];
  capturedChars: number;
  nextSeq: number;
  listeners: Set<(ev: SubprocessStreamEvent) => void>;
  cancelled: boolean;
  logDir: string;
}

const DEFAULT_MAX_CAPTURED_CHARS = 2_000_000;
const DEFAULT_MAX_FINISHED_TASKS = 50;
const DEFAULT_LOG_DIR = path.resolve(process.cwd(), 'data/subprocess-logs');

function quoteArg(arg: string): string {
  if (arg === '') return "''";
  if (/^[A-Za-z0-9_./:=@+-]+$/.test(arg)) return arg;
  return `'${arg.replace(/'/g, `'\\''`)}'`;
}

export function formatCommandLine(command: string, args: string[] = []): string {
  return [command, ...args].map(quoteArg).join(' ');
}

export class SubprocessTaskRegistry {
  private records = new Map<string, TaskRecord>();
  private finishedOrder: string[] = [];
  private readonly maxCapturedChars: number;
  private readonly maxFinishedTasks: number;
  private readonly logDir: string;
  private readonly persist: boolean;
  private idCounter = 0;

  constructor(options: SubprocessRegistryOptions = {}) {
    this.maxCapturedChars = options.maxCapturedChars ?? DEFAULT_MAX_CAPTURED_CHARS;
    this.maxFinishedTasks = options.maxFinishedTasks ?? DEFAULT_MAX_FINISHED_TASKS;
    this.logDir = options.logDir ?? DEFAULT_LOG_DIR;
    this.persist = options.persist ?? true;
  }

  /** Spawns a real child process and registers it as a streamable task. */
  spawn(options: SpawnSubprocessOptions): { info: SubprocessTaskInfo; proc: ChildProcess } {
    const args = options.args ?? [];
    const id = options.id ?? `sub-${Date.now()}-${++this.idCounter}`;
    const logDir = options.logDir ?? this.logDir;

    if (this.records.has(id)) {
      throw new Error(`Subprocess task ${id} is already registered`);
    }

    const info: SubprocessTaskInfo = {
      id,
      label: options.label,
      kind: options.kind ?? 'subprocess',
      commandLine: formatCommandLine(options.command, args),
      nodeTarget: options.nodeTarget ?? 'localhost',
      user: options.user ?? `${process.env.USER || 'local'}@subprocess`,
      description: options.description ?? options.label,
      status: 'RUNNING',
      startTime: new Date().toISOString(),
      pid: undefined,
      lineCount: 0,
      truncated: false,
    };

    const record: TaskRecord = {
      info,
      proc: null,
      chunks: [],
      capturedChars: 0,
      nextSeq: 1,
      listeners: new Set(),
      listenersSinceSeq: new Map(),
      cancelled: false,
      logDir,
    };

    this.records.set(id, record);

    const proc = spawn(options.command, args, {
      cwd: options.cwd,
      env: options.env as NodeJS.ProcessEnv | undefined,
      stdio: ['ignore', 'pipe', 'pipe'],
    });

    record.proc = proc;
    info.pid = proc.pid;

    const append = (stream: SubprocessOutputStream, raw: string) => {
      if (!raw) return;
      this.appendChunk(record, stream, raw);
    };

    proc.stdout?.on('data', (chunk: Buffer | string) => append('stdout', chunk.toString()));
    proc.stderr?.on('data', (chunk: Buffer | string) => append('stderr', chunk.toString()));

    proc.on('error', (err: Error) => {
      append('system', `[spawn error] ${err.message}\n`);
      this.finish(record, null, true);
    });

    proc.on('close', (code: number | null) => {
      this.finish(record, code, code === null || this.records.get(id)?.info.status === 'FAILED');
    });

    this.emit(record, { type: 'status', task: this.snapshot(record) });

    return { info: this.snapshot(record), proc };
  }

  get(id: string): SubprocessTaskInfo | null {
    const record = this.records.get(id);
    if (record) return this.snapshot(record);
    return this.readPersistedInfo(id);
  }

  listActive(): SubprocessTaskInfo[] {
    return [...this.records.values()]
      .filter(r => r.info.status === 'RUNNING')
      .map(r => this.snapshot(r))
      .sort((a, b) => b.startTime.localeCompare(a.startTime));
  }

  listCompleted(limit = this.maxFinishedTasks): SubprocessTaskInfo[] {
    const merged = new Map<string, SubprocessTaskInfo>();

    for (const id of this.finishedOrder) {
      const record = this.records.get(id);
      if (record) merged.set(id, this.snapshot(record));
    }

    if (this.persist) {
      for (const persisted of this.readPersistedIndex()) {
        if (!merged.has(persisted.id)) merged.set(persisted.id, persisted);
      }
    }

    return [...merged.values()]
      .sort((a, b) => (b.endTime || b.startTime).localeCompare(a.endTime || a.startTime))
      .slice(0, limit);
  }

  /** Full captured output (memory buffer, falling back to the on-disk log). */
  readOutput(id: string): string {
    const record = this.records.get(id);
    if (record && record.chunks.length > 0) {
      return record.chunks.map(c => c.data).join('');
    }
    return this.readPersistedOutput(id) ?? '';
  }

  /**
   * Replays everything captured so far, then delivers live events.
   * Closing the returned subscription never affects the child process.
   */
  subscribe(id: string, listener: (ev: SubprocessStreamEvent) => void): () => void {
    const record = this.records.get(id);
    if (!record) return () => {};

    for (const chunk of record.chunks) {
      listener({
        type: 'output',
        taskId: id,
        seq: chunk.seq,
        stream: chunk.stream,
        data: chunk.data,
      });
    }

    const task = this.snapshot(record);
    if (task.status !== 'RUNNING') {
      listener({ type: 'exit', task, exitCode: task.exitCode ?? null });
      return () => {};
    }

    record.listeners.add(listener);
    return () => {
      record.listeners.delete(listener);
      record.listenersSinceSeq.delete(listener);
    };
  }

  /** Requests termination of a running task. Captured output stays readable. */
  kill(id: string, signal: NodeJS.Signals = 'SIGTERM'): boolean {
    const record = this.records.get(id);
    if (!record || record.info.status !== 'RUNNING' || !record.proc) return false;
    record.cancelled = true;
    record.proc.kill(signal);
    return true;
  }

  /** Kills every running task (test/teardown helper). */
  dispose(): void {
    for (const record of this.records.values()) {
      if (record.info.status === 'RUNNING') {
        try {
          record.proc?.kill('SIGKILL');
        } catch {
          // ignore
        }
      }
      record.listeners.clear();
      record.listenersSinceSeq.clear();
    }
  }

  private appendChunk(record: TaskRecord, stream: SubprocessOutputStream, data: string) {
    const seq = record.nextSeq++;
    record.chunks.push({ seq, stream, data });
    record.capturedChars += data.length;
    record.info.lineCount += countNewlines(data);

    while (record.capturedChars > this.maxCapturedChars && record.chunks.length > 1) {
      const dropped = record.chunks.shift()!;
      record.capturedChars -= dropped.data.length;
      record.info.truncated = true;
    }

    this.emit(record, { type: 'output', taskId: record.info.id, seq, stream, data });
  }

  private finish(record: TaskRecord, exitCode: number | null, failed: boolean) {
    const info = record.info;
    if (info.status !== 'RUNNING') return;

    info.endTime = new Date().toISOString();
    info.exitCode = exitCode;
    info.status = exitCode === 0 && !failed ? 'OK' : 'FAILED';
    info.cancelled = record.cancelled;
    if (info.cancelled && exitCode !== 0) {
      info.status = 'FAILED';
    }

    this.emit(record, { type: 'status', task: this.snapshot(record) });
    this.emit(record, { type: 'exit', task: this.snapshot(record), exitCode });

    record.listeners.clear();
    record.listenersSinceSeq.clear();
    record.proc = null;

    this.finishedOrder.push(info.id);
    while (this.finishedOrder.length > this.maxFinishedTasks) {
      const evicted = this.finishedOrder.shift()!;
      this.records.delete(evicted);
    }

    if (this.persist) {
      this.persistRecord(record);
    }
  }

  private emit(record: TaskRecord, ev: SubprocessStreamEvent) {
    for (const listener of [...record.listeners]) {
      try {
        listener(ev);
      } catch {
        record.listeners.delete(listener);
        record.listenersSinceSeq.delete(listener);
      }
    }
  }

  private snapshot(record: TaskRecord): SubprocessTaskInfo {
    return { ...record.info };
  }

  private persistRecord(record: TaskRecord) {
    try {
      fs.mkdirSync(record.logDir, { recursive: true });
      const base = path.join(record.logDir, record.info.id);
      fs.writeFileSync(`${base}.log`, record.chunks.map(c => c.data).join(''));
      fs.writeFileSync(`${base}.json`, JSON.stringify(record.info, null, 2));
    } catch {
      // Persistence is best-effort: the in-memory buffer remains authoritative.
    }
  }

  private readPersistedInfo(id: string): SubprocessTaskInfo | null {
    if (!this.persist || !/^[A-Za-z0-9._-]+$/.test(id)) return null;
    try {
      const raw = fs.readFileSync(path.join(this.logDir, `${id}.json`), 'utf-8');
      return JSON.parse(raw) as SubprocessTaskInfo;
    } catch {
      return null;
    }
  }

  private readPersistedOutput(id: string): string | null {
    if (!this.persist || !/^[A-Za-z0-9._-]+$/.test(id)) return null;
    try {
      return fs.readFileSync(path.join(this.logDir, `${id}.log`), 'utf-8');
    } catch {
      return null;
    }
  }

  private readPersistedIndex(): SubprocessTaskInfo[] {
    let entries: string[];
    try {
      entries = fs.readdirSync(this.logDir).filter(f => f.endsWith('.json'));
    } catch {
      return [];
    }
    const infos: SubprocessTaskInfo[] = [];
    for (const entry of entries) {
      try {
        const raw = fs.readFileSync(path.join(this.logDir, entry), 'utf-8');
        const info = JSON.parse(raw) as SubprocessTaskInfo;
        if (info?.id && info.status !== 'RUNNING') infos.push(info);
      } catch {
        // ignore unreadable entries
      }
    }
    return infos;
  }
}

function countNewlines(text: string): number {
  let count = 0;
  for (let i = 0; i < text.length; i++) {
    if (text.charCodeAt(i) === 10) count++;
  }
  return count;
}

/** Process-wide registry used by the coordinator server. */
export const subprocessTasks = new SubprocessTaskRegistry();
