import { describe, expect, it } from 'vitest';
import { cellValue, columnKind } from './cell-value';

describe('SQL value presentation', () => {
  it.each([
    ['INT unsigned', 'integer'], ['BIGINT', 'integer'], ['tinyint(1)', 'boolean'],
    ['DECIMAL(24,8)', 'decimal'], ['double precision', 'decimal'], ['BOOLEAN', 'boolean'],
    ["enum('active','pending')", 'enum'], ["set('a','b')", 'set'], ['varchar(255)', 'text'],
    ['timestamp without time zone', 'datetime'], ['date', 'date'], ['time(6)', 'time'],
    ['jsonb', 'json'], ['bytea', 'binary'], ['uuid', 'uuid'], ['custom_status', 'other'],
  ])('classifies %s using metadata', (type, kind) => {
    expect(columnKind(type)).toBe(kind);
  });

  it('distinguishes SQL null, literal NULL, empty text, and false', () => {
    expect(cellValue(null, 'varchar').kind).toBe('null');
    expect(cellValue('NULL', 'varchar').text).toBe('NULL');
    expect(cellValue('', 'varchar').text).toBe('');
    expect(cellValue(false, 'boolean').text).toBe('false');
    expect(cellValue(0, 'integer').text).toBe('0');
  });

  it('preserves precision, timezone, whitespace and JSON structure', () => {
    expect(cellValue('9007199254740993.0001', 'numeric').text).toBe('9007199254740993.0001');
    expect(cellValue('2026-10-25T12:00:00+04:00', 'timestamp').text).toBe('2026-10-25T12:00:00+04:00');
    expect(cellValue('  hello  ', 'text').text).toBe('  hello  ');
    expect(cellValue('NULL', 'text').text).toBe('NULL');
    expect(cellValue('', 'text').text).toBe('');
    expect(cellValue({ a: false }, 'json').text).toBe('{"a":false}');
    expect(cellValue('2026-10-25', 'text').kind).toBe('text');
  });

  it.each([
    ['2026-10-25T12:00:00Z', 'datetime'],
    ['2026-10-25 12:00:00.123456', 'timestamp without time zone'],
    ['2026-10-25T12:00:00.123456+04:00', 'timestamptz'],
    ['2026-10-25', 'date'],
    ['12:00:00.123456', 'time(6)'],
    ['0000-00-00 00:00:00', 'datetime'],
  ])('preserves the database value %s in the cell and tooltip', (value, type) => {
    const cell = cellValue(value, type);
    expect(cell.text).toBe(value);
    expect(cell.label).toBe(`${type}: ${value}`);
  });
});
