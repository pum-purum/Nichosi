import { describe, expect, it } from 'vitest';
import { draftValue, rowChanges, rowDeletion, insertionDraft, rowFieldErrors } from './row-edit';
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
  it.each(['timestamp with time zone', 'timestamp(6) with time zone', 'timestamptz'])('preserves PostgreSQL timezone instants for %s', (type) => {
    const temporal = [{ ...columns[1], name: 'created', type }];
    for (const text of ['2026-02-01T12:13:14.123456Z', '2026-02-01T12:13:14+00:00', '2026-02-01T12:13:14+04:00']) {
      const draft = [{ text, isNull: false, useDefault: false }];
      expect(rowChanges(temporal, ['before'], draft, ['created']).changes[0].values).toEqual({ created: text });
      expect(rowChanges(temporal, [], draft, [], true).changes[0].values).toEqual({ created: text });
    }
  });
  it('preserves column names that overlap object prototypes', () => {
    const unusual = [{ ...columns[1], name: '__proto__' }];
    const changes = rowChanges(unusual, ['before'], [{ text: 'after', isNull: false }], ['__proto__']);
    expect(JSON.stringify(changes)).toContain('"__proto__":"after"');
  });
});

describe('record deletion', () => {
  it('uses original composite key values without including row contents', () => {
    expect(rowDeletion(columns, row, ['id', 'amount'])).toEqual({ changes: [{ op: 'delete', key: { id: row[0], amount: row[2] } }] });
  });
  it('rejects missing, null, and unsafe numeric keys', () => {
    for (const key of [[], ['missing']]) expect(() => rowDeletion(columns, row, key)).toThrow('key');
    expect(() => rowDeletion(columns, [null, ...row.slice(1)], ['id'])).toThrow('key');
    expect(() => rowDeletion(columns, [9007199254740992, ...row.slice(1)], ['id'])).toThrow('precision');
  });
});

 it('rejects runtime SQLite blob keys and leaves blob values unchanged', () => {
   const blob = { type: 'blob', hex: '616263' };
   expect(() => rowDeletion(columns, [blob, 'Alice', '10'], ['id'])).toThrow('Binary row keys');
   const original = ['1', blob, '10'];
   const draft = original.map(draftValue); draft[1].text = 'changed';
   expect(rowChanges(columns, original, draft, ['id'])).toEqual({ changes: [] });
 });


describe('record insertion', () => {
  const insertColumns: Column[] = [
    { name: 'id', type: 'bigint', primaryKey: true, nullable: false, autoIncrement: true },
    { name: 'created', type: 'timestamp', primaryKey: false, nullable: false, defaultValue: 'NOW()' },
    { name: 'name', type: 'text', primaryKey: false, nullable: true },
    { name: 'computed', type: 'int', primaryKey: false, nullable: false, generated: true },
  ];
  it('omits defaults and generated columns without evaluating expressions', () => {
    const draft = insertionDraft(insertColumns);
    expect(draft[1].text).toBe('');
    expect(draft[1].useDefault).toBe(true);
    expect(draft[2].useDefault).toBe(false);
    const nullableDefault = insertionDraft([{ ...insertColumns[1], nullable: true }]);
    expect(nullableDefault[0].isNull).toBe(false);
    expect(rowChanges(insertColumns, [], draft, [], true)).toEqual({ changes: [{ op: 'insert', values: { name: null } }] });
  });
  it('accepts explicit primary keys, NULL, empty strings and literal function text', () => {
    const draft = insertionDraft(insertColumns);
    draft[0] = { text: '9007199254740993', isNull: false, useDefault: false };
    draft[2] = { text: '', isNull: false, useDefault: false };
    expect(rowChanges(insertColumns, [], draft, [], true).changes[0].values).toEqual({ id: '9007199254740993', name: '' });
    draft[2].isNull = true;
    expect(rowChanges(insertColumns, [], draft, [], true).changes[0].values?.name).toBeNull();
    draft[2] = { text: 'NOW()', isNull: false, useDefault: false };
    expect(rowChanges(insertColumns, [], draft, [], true).changes[0].values?.name).toBe('NOW()');
  });
  it('validates required numeric input and keeps required fields editable initially', () => {
    const required = [{ ...insertColumns[0], primaryKey: false, autoIncrement: false }];
    const draft = insertionDraft(required);
    expect(draft[0].useDefault).toBe(false);
    expect(() => rowChanges(required, [], draft, [], true)).toThrow('integer');
  });
});

describe('field validation', () => {
  it('accepts empty text while reporting all other invalid insertion fields', () => {
    const fields: Column[] = [
      { name: 'quantity', type: 'integer', nullable: false, primaryKey: false },
      { name: 'title', type: 'text', nullable: false, primaryKey: false },
      { name: 'created', type: 'date', nullable: false, primaryKey: false },
    ];
    const draft = insertionDraft(fields);
    expect(rowFieldErrors(fields, [], draft, true)).toEqual([
      'quantity requires an integer.', '', 'created is required.',
    ]);
    draft[0].text = '1';
    expect(() => rowChanges(fields, [], draft, [], true)).toThrow('created is required.');
    draft[2].text = '2026-11-15';
    expect(rowFieldErrors(fields, [], draft, true)).toEqual(['', '', '']);
    expect(rowChanges(fields, [], draft, [], true).changes[0].values).toEqual({
      quantity: '1', title: '', created: '2026-11-15',
    });
  });
  it('preserves explicit empty strings when editing non-nullable text', () => {
    const fields: Column[] = [{ name: 'title', type: 'text', nullable: false, primaryKey: false }];
    const draft = [{ text: '', isNull: false }];
    expect(rowFieldErrors(fields, ['Example'], draft)).toEqual(['']);
    expect(rowChanges(fields, ['Example'], draft, ['title']).changes[0].values).toEqual({ title: '' });
  });
  it('reports all invalid fields and clears corrected or defaulted values', () => {
    const fields: Column[] = [
      { name: 'artist_id', type: 'integer', nullable: false, primaryKey: false },
      { name: 'release_year', type: 'integer', nullable: false, primaryKey: false },
    ];
    const draft = insertionDraft(fields);
    expect(rowFieldErrors(fields, [], draft, true)).toEqual(['artist_id requires an integer.', 'release_year requires an integer.']);
    draft[0].text = '12';
    draft[1].useDefault = true;
    expect(rowFieldErrors(fields, [], draft, true)).toEqual(['', '']);
  });
  it('ignores unchanged values when editing', () => {
    expect(rowFieldErrors(columns, row, row.map(draftValue))).toEqual(['', '', '']);
  });
});
