import { describe, expect, it } from 'vitest';
import { columnOptions } from './column-options';
import { fieldError, insertionDraft, rowChanges } from './row-edit';

describe('column member choices', () => {
  it('decodes quoted members including commas, escapes and empty strings', () => {
    expect(columnOptions(String.raw`ENUM('new','it''s','a,b','back\\slash','line\nnext','')`)).toEqual({
      kind: 'enum', values: ['new', "it's", 'a,b', 'back\\slash', 'line\nnext', ''],
    });
    expect(columnOptions("set('a','b')")).toEqual({ kind: 'set', values: ['a', 'b'] });
  });
  it('ignores unrelated types and incomplete definitions', () => {
    for (const type of ['text', 'enum', "enum('a',)", "set('broken)", "enum('a') extra"]) expect(columnOptions(type)).toBeNull();
  });
  it('validates enum insertions and declared empty strings', () => {
    const column = { name: 'status', type: "enum('new','done')", nullable: false, primaryKey: false };
    const draft = insertionDraft([column]);
    expect(() => rowChanges([column], [], draft, [], true)).toThrow('ENUM');
    draft[0].text = 'done';
    expect(rowChanges([column], [], draft, [], true).changes[0].values).toEqual({ status: 'done' });
    expect(fieldError({ ...column, type: "enum('','done')" }, { text: '', isNull: false }, true)).toBe('');
  });
  it('accepts empty sets, multiple members and NULL while rejecting unknown members', () => {
    const column = { name: 'tags', type: "set('a','b')", nullable: false, primaryKey: false };
    for (const text of ['', 'a', 'a,b']) expect(fieldError(column, { text, isNull: false }, true)).toBe('');
    expect(fieldError(column, { text: 'a,unknown', isNull: false })).toContain('SET');
    expect(fieldError({ ...column, nullable: true }, { text: '', isNull: true })).toBe('');
  });
});
