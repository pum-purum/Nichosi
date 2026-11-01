import { describe, expect, it } from 'vitest';
import { draftValue, rowChanges } from './row-edit';
import type { components } from './api/schema';
type Column = components['schemas']['Column'];
const columns: Column[] = [
  { name: 'id', type: 'bigint', primaryKey: true, nullable: false },
  { name: 'name', type: 'text', primaryKey: false, nullable: true },
  { name: 'amount', type: 'decimal(30,9)', primaryKey: false, nullable: false },
];
const row = ['9007199254740993', 'Alice', '12345678901234567890.123456789'];
describe('record changes', () => {
  it('does not create changes for untouched or reverted values', () => {
    expect(rowChanges(columns, row, row.map(draftValue), ['id'])).toEqual({ changes: [] });
  });
  it('ignores primary key edits and includes only changed editable values', () => {
    const draft = row.map(draftValue); draft[0].text = '9007199254740994'; draft[1].text = 'Bob';
    expect(rowChanges(columns, row, draft, ['id'])).toEqual({ changes: [{ op: 'update', key: { id: row[0] }, values: { name: 'Bob' } }] });
  });
  it('keeps decimal precision and distinguishes NULL from empty strings', () => {
    const draft = row.map(draftValue); draft[2].text = '12345678901234567890.123456788'; draft[1].isNull = true;
    expect(rowChanges(columns, row, draft, ['id']).changes[0].values).toEqual({ name: null, amount: draft[2].text });
    draft[1].isNull = false; draft[1].text = '';
    expect(rowChanges(columns, row, draft, ['id']).changes[0].values?.name).toBe('');
  });
  it('rejects invalid numeric values and missing keys', () => {
    const draft = row.map(draftValue); draft[2].text = 'invalid';
    expect(() => rowChanges(columns, row, draft, ['id'])).toThrow('number');
    expect(() => rowChanges(columns, row, row.map(draftValue), [])).toThrow('key');
  });
  it('ignores generated and binary fields and supports composite keys', () => {
    const special = [...columns, { name: 'computed', type: 'int', generated: true, primaryKey: false, nullable: false }, { name: 'data', type: 'blob', primaryKey: false, nullable: true }];
    const original = [...row, 42, 'raw']; const draft = original.map(draftValue); draft[3].text = '99'; draft[4].text = 'bad'; draft[1].text = 'Bob';
    expect(rowChanges(special, original, draft, ['id', 'amount']).changes[0]).toEqual({ op: 'update', key: { id: row[0], amount: row[2] }, values: { name: 'Bob' } });
  });
  it('rejects unsafe numeric original keys', () => {
    const unsafe = [9007199254740992, ...row.slice(1)];
    expect(() => rowChanges(columns, unsafe, unsafe.map(draftValue), ['id'])).toThrow('precision');
  });
  it('normalizes edited UTC temporal values without losing fractional precision', () => {
    const temporal = [{ ...columns[1], name: 'created', type: 'datetime(6)' }];
    expect(rowChanges(temporal, ['2026-01-01T00:00:00Z'], [{ text: '2026-02-01T12:13:14.123456Z', isNull: false }], ['created']).changes[0].values).toEqual({ created: '2026-02-01 12:13:14.123456' });
  });
  it('preserves column names that overlap object prototypes', () => {
    const unusual = [{ ...columns[1], name: '__proto__' }];
    const changes = rowChanges(unusual, ['before'], [{ text: 'after', isNull: false }], ['__proto__']);
    expect(JSON.stringify(changes)).toContain('"__proto__":"after"');
  });
});
