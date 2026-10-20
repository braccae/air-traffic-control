/**
 * Minimal, dependency-free 5-field cron expression engine.
 *
 * Fields: minute hour day-of-month month day-of-week
 *   minute       0-59
 *   hour         0-23
 *   day-of-month 1-31
 *   month        1-12 (jan..dec)
 *   day-of-week  0-7  (0 and 7 = Sunday, sun..sat)
 *
 * Supported syntax per field: `*`, `n`, `a-b`, `a-b/step`, `*​/step`, and
 * comma-separated lists thereof. `?` is accepted as an alias for `*` on the
 * day-of-month / day-of-week fields.
 *
 * Standard cron semantics apply: when BOTH day-of-month and day-of-week are
 * restricted, a day matches if EITHER matches.
 *
 * Times are evaluated against the coordinator host's local clock — the
 * scheduler is an in-process ATC scheduler (see docs/CRON_SCHEDULES.md), so
 * there is no per-target timezone to reconcile.
 */

export interface ParsedCronExpression {
  expression: string;
  minutes: number[];
  hours: number[];
  daysOfMonth: number[];
  months: number[];
  daysOfWeek: number[];
  domRestricted: boolean;
  dowRestricted: boolean;
}

const MACROS: Record<string, string> = {
  '@yearly': '0 0 1 1 *',
  '@annually': '0 0 1 1 *',
  '@monthly': '0 0 1 * *',
  '@weekly': '0 0 * * 0',
  '@daily': '0 0 * * *',
  '@midnight': '0 0 * * *',
  '@hourly': '0 * * * *',
};

const MONTH_NAMES = ['jan', 'feb', 'mar', 'apr', 'may', 'jun', 'jul', 'aug', 'sep', 'oct', 'nov', 'dec'];
const DOW_NAMES = ['sun', 'mon', 'tue', 'wed', 'thu', 'fri', 'sat'];

interface FieldSpec {
  name: string;
  min: number;
  max: number;
  names?: string[];
  /** Map a numeric value in [min,max] to the canonical value space. */
  normalize?: (value: number) => number;
}

function resolveValue(token: string, spec: FieldSpec): number {
  const lower = token.toLowerCase();
  if (spec.names) {
    const idx = spec.names.indexOf(lower);
    if (idx >= 0) {
      return spec.names === DOW_NAMES ? idx : idx + 1;
    }
  }
  if (!/^\d+$/.test(token)) {
    throw new Error(`Invalid ${spec.name} value "${token}"`);
  }
  const value = parseInt(token, 10);
  if (value < spec.min || value > spec.max) {
    throw new Error(`${spec.name} value ${value} out of range ${spec.min}-${spec.max}`);
  }
  return value;
}

function parseField(raw: string, spec: FieldSpec): { values: number[]; restricted: boolean } {
  const field = raw.trim();
  if (field === '') {
    throw new Error(`Empty ${spec.name} field`);
  }

  const restricted = field !== '*' && field !== '?';
  const values = new Set<number>();

  for (const part of field.split(',')) {
    const segment = part.trim();
    if (!segment) {
      throw new Error(`Empty ${spec.name} list entry in "${raw}"`);
    }

    const [base, stepRaw, ...rest] = segment.split('/');
    if (rest.length > 0) {
      throw new Error(`Invalid ${spec.name} segment "${segment}"`);
    }

    let step = 1;
    if (stepRaw !== undefined) {
      if (!/^\d+$/.test(stepRaw) || parseInt(stepRaw, 10) <= 0) {
        throw new Error(`Invalid step "${stepRaw}" in ${spec.name} field`);
      }
      step = parseInt(stepRaw, 10);
    }

    let start: number;
    let end: number;

    if (base === '*' || base === '?') {
      start = spec.min;
      end = spec.max;
    } else if (base.includes('-')) {
      const [a, b, ...extra] = base.split('-');
      if (extra.length > 0) {
        throw new Error(`Invalid ${spec.name} range "${base}"`);
      }
      start = resolveValue(a, spec);
      end = resolveValue(b, spec);
      if (start > end) {
        throw new Error(`Invalid ${spec.name} range "${base}" (start after end)`);
      }
    } else {
      start = resolveValue(base, spec);
      end = stepRaw !== undefined ? spec.max : start;
    }

    for (let v = start; v <= end; v += step) {
      values.add(spec.normalize ? spec.normalize(v) : v);
    }
  }

  return { values: [...values].sort((a, b) => a - b), restricted };
}

const SPECS: FieldSpec[] = [
  { name: 'minute', min: 0, max: 59 },
  { name: 'hour', min: 0, max: 23 },
  { name: 'day-of-month', min: 1, max: 31 },
  { name: 'month', min: 1, max: 12, names: MONTH_NAMES },
  { name: 'day-of-week', min: 0, max: 7, names: DOW_NAMES, normalize: v => (v === 7 ? 0 : v) },
];

export function parseCronExpression(expression: string): ParsedCronExpression {
  if (typeof expression !== 'string' || expression.trim() === '') {
    throw new Error('Cron expression is required');
  }

  const trimmed = expression.trim();
  const expanded = MACROS[trimmed.toLowerCase()] ?? trimmed;
  const fields = expanded.split(/\s+/);

  if (fields.length !== 5) {
    throw new Error(
      `Cron expression must have exactly 5 fields (minute hour day-of-month month day-of-week), got ${fields.length}`
    );
  }

  const [minute, hour, dom, month, dow] = fields.map((field, idx) =>
    parseField(field, SPECS[idx])
  );

  return {
    // Collapse whitespace (and expand macros) so persisted expressions are canonical.
    expression: fields.join(' '),
    minutes: minute.values,
    hours: hour.values,
    daysOfMonth: dom.values,
    months: month.values,
    daysOfWeek: dow.values,
    domRestricted: dom.restricted,
    dowRestricted: dow.restricted,
  };
}

export function validateCronExpression(expression: string): {
  valid: boolean;
  error?: string;
  normalized?: string;
} {
  try {
    const parsed = parseCronExpression(expression);
    return { valid: true, normalized: parsed.expression };
  } catch (err: any) {
    return { valid: false, error: err?.message || String(err) };
  }
}

export function matchesCron(parsed: ParsedCronExpression, date: Date): boolean {
  if (!parsed.minutes.includes(date.getMinutes())) return false;
  if (!parsed.hours.includes(date.getHours())) return false;
  if (!parsed.months.includes(date.getMonth() + 1)) return false;

  const domMatch = parsed.daysOfMonth.includes(date.getDate());
  const dowMatch = parsed.daysOfWeek.includes(date.getDay());

  if (parsed.domRestricted && parsed.dowRestricted) {
    return domMatch || dowMatch;
  }
  return domMatch && dowMatch;
}

/** Hard stop for next-run search: 5 years of minutes. */
const MAX_SEARCH_MINUTES = 5 * 366 * 24 * 60;

/**
 * Returns the next epoch-millisecond timestamp strictly after `from` at which
 * the expression fires, or null when it can never fire again (e.g. `0 0 30 2 *`).
 */
export function nextCronRun(
  expression: string | ParsedCronExpression,
  from: number | Date = Date.now()
): number | null {
  const parsed = typeof expression === 'string' ? parseCronExpression(expression) : expression;
  const fromMs = typeof from === 'number' ? from : from.getTime();

  // Start at the next whole minute after `from`.
  const start = new Date(fromMs);
  start.setSeconds(0, 0);
  start.setMinutes(start.getMinutes() + 1);

  const cursor = new Date(start.getTime());
  const seen = new Set<string>();

  for (let i = 0; i < MAX_SEARCH_MINUTES; i++) {
    // Cheap month/day guard to skip impossible days quickly.
    if (!parsed.months.includes(cursor.getMonth() + 1)) {
      cursor.setMonth(cursor.getMonth() + 1, 1);
      cursor.setHours(0, 0, 0, 0);
      continue;
    }

    const key = `${cursor.getFullYear()}-${cursor.getMonth()}-${cursor.getDate()}`;
    if (!seen.has(key)) {
      seen.add(key);
      const domMatch = parsed.daysOfMonth.includes(cursor.getDate());
      const dowMatch = parsed.daysOfWeek.includes(cursor.getDay());
      const dayMatches =
        parsed.domRestricted && parsed.dowRestricted ? domMatch || dowMatch : domMatch && dowMatch;

      if (!dayMatches) {
        cursor.setDate(cursor.getDate() + 1);
        cursor.setHours(0, 0, 0, 0);
        continue;
      }
    }

    if (!parsed.hours.includes(cursor.getHours())) {
      cursor.setHours(cursor.getHours() + 1, 0, 0, 0);
      continue;
    }

    if (!parsed.minutes.includes(cursor.getMinutes())) {
      cursor.setMinutes(cursor.getMinutes() + 1, 0, 0);
      continue;
    }

    return cursor.getTime();
  }

  return null;
}

/** Human-readable list of the distinct minute offsets the expression fires at within an hour. */
export function cronMinuteSummary(expression: string | ParsedCronExpression): string {
  const parsed = typeof expression === 'string' ? parseCronExpression(expression) : expression;
  return parsed.minutes.map(m => String(m).padStart(2, '0')).join(',');
}
