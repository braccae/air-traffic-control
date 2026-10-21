import { describe, it } from 'node:test';
import assert from 'node:assert/strict';
import {
  parseCronExpression,
  validateCronExpression,
  matchesCron,
  nextCronRun,
} from './cronExpression.ts';

function localDate(
  year: number,
  month: number,
  day: number,
  hour: number,
  minute: number
): Date {
  return new Date(year, month - 1, day, hour, minute, 0, 0);
}

describe('cron expression parsing', () => {
  it('parses a standard 5-field expression', () => {
    const parsed = parseCronExpression('*/15 2-4 * jan mon-fri');
    assert.deepEqual(parsed.minutes, [0, 15, 30, 45]);
    assert.deepEqual(parsed.hours, [2, 3, 4]);
    assert.deepEqual(parsed.months, [1]);
    assert.deepEqual(parsed.daysOfWeek, [1, 2, 3, 4, 5]);
  });

  it('normalizes day-of-week 7 to Sunday', () => {
    const parsed = parseCronExpression('0 0 * * 7');
    assert.deepEqual(parsed.daysOfWeek, [0]);
  });

  it('accepts @daily macro', () => {
    const parsed = parseCronExpression('@daily');
    assert.deepEqual(parsed.minutes, [0]);
    assert.deepEqual(parsed.hours, [0]);
  });

  it('rejects wrong field counts and out-of-range values', () => {
    assert.equal(validateCronExpression('* * *').valid, false);
    assert.equal(validateCronExpression('60 * * * *').valid, false);
    assert.equal(validateCronExpression('* 24 * * *').valid, false);
    assert.equal(validateCronExpression('*/0 * * * *').valid, false);
    assert.equal(validateCronExpression('').valid, false);
  });

  it('reports a normalized expression for valid input', () => {
    const result = validateCronExpression('  0  3 * * *  ');
    assert.equal(result.valid, true);
    assert.equal(result.normalized, '0 3 * * *');
  });
});

describe('cron matching and next-run computation', () => {
  it('matches an exact minute', () => {
    const parsed = parseCronExpression('30 4 * * *');
    assert.equal(matchesCron(parsed, localDate(2026, 3, 10, 4, 30)), true);
    assert.equal(matchesCron(parsed, localDate(2026, 3, 10, 4, 29)), false);
  });

  it('finds the next minute for */5', () => {
    const from = localDate(2026, 3, 10, 10, 7);
    const next = nextCronRun('*/5 * * * *', from);
    assert.notEqual(next, null);
    const nextDate = new Date(next!);
    assert.equal(nextDate.getHours(), 10);
    assert.equal(nextDate.getMinutes(), 10);
    assert.equal(nextDate.getSeconds(), 0);
  });

  it('rolls over to the next day for a fixed daily time', () => {
    const from = localDate(2026, 3, 10, 5, 0);
    const next = nextCronRun('0 3 * * *', from);
    assert.notEqual(next, null);
    const nextDate = new Date(next!);
    assert.equal(nextDate.getDate(), 11);
    assert.equal(nextDate.getHours(), 3);
    assert.equal(nextDate.getMinutes(), 0);
  });

  it('uses OR semantics when both dom and dow are restricted', () => {
    const parsed = parseCronExpression('0 0 1 * 1');
    // 2026-06-01 is a Monday and the 1st of the month -> both match
    assert.equal(matchesCron(parsed, localDate(2026, 6, 1, 0, 0)), true);
    // 2026-06-08 is a Monday but not the 1st -> dow match is enough
    assert.equal(matchesCron(parsed, localDate(2026, 6, 8, 0, 0)), true);
    // 2026-07-01 is a Wednesday and the 1st -> dom match is enough
    assert.equal(matchesCron(parsed, localDate(2026, 7, 1, 0, 0)), true);
    // 2026-06-03 is neither
    assert.equal(matchesCron(parsed, localDate(2026, 6, 3, 0, 0)), false);
  });

  it('returns null for an expression that can never fire', () => {
    assert.equal(nextCronRun('0 0 30 2 *', localDate(2026, 1, 1, 0, 0)), null);
  });
});
