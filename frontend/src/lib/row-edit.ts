import { writable } from 'svelte/store';
import type { components } from './api/schema';
import { columnKind } from './cell-value';

type Column = components['schemas']['Column'];
type ChangeSet = components['schemas']['ChangeSet'];
export type DraftValue = { text: string; isNull: boolean };
export function draftValue(value: unknown): DraftValue {
  return { text: value === null ? '' : typeof value === 'object' ? JSON.stringify(value) : String(value), isNull: value === null };
}
export function editableColumn(column: Column): boolean {
  return !column.primaryKey && !column.generated && columnKind(column.type) !== 'binary';
}
export function rowChanges(columns: Column[], original: unknown[], draft: DraftValue[], editKey: string[]): ChangeSet {
  const values: Record<string, unknown> = {};
  const key = rowKey(columns, original, editKey);
  columns.forEach((column, index) => {
    if (!editableColumn(column)) return;
    const before = draftValue(original[index]);
    const after = draft[index];
    if (before.isNull === after.isNull && (after.isNull || before.text === after.text)) return;
    if (after.isNull && !column.nullable) throw new Error(`${column.name} cannot be NULL.`);
    if (!after.isNull) {
      const kind = columnKind(column.type);
      if (kind === 'integer' && !/^[+-]?\d+$/.test(after.text)) throw new Error(`${column.name} requires an integer.`);
      if (kind === 'decimal' && !/^[+-]?(?:\d+(?:\.\d*)?|\.\d+)(?:[eE][+-]?\d+)?$/.test(after.text)) throw new Error(`${column.name} requires a number.`);
      if (kind === 'json') {
        try { JSON.parse(after.text); } catch { throw new Error(`${column.name} requires valid JSON.`); }
      }
    }
    let value: unknown = after.isNull ? null : after.text;
    const kind = columnKind(column.type);
    // Go serializes MySQL DATE/DATETIME as RFC3339 in UTC. Keep fractional precision.
    if (!after.isNull && (kind === 'date' || kind === 'datetime')) {
      const temporal = /^(\d{4}-\d{2}-\d{2})T(\d{2}:\d{2}:\d{2}(?:\.\d+)?)(?:Z|[+]00:00)$/.exec(after.text);
      if (temporal) value = kind === 'date' ? temporal[1] : `${temporal[1]} ${temporal[2]}`;
    }
    Object.defineProperty(values, column.name, { value, enumerable: true });
  });
  return { changes: Object.keys(values).length ? [{ op: 'update', key, values }] : [] };
}
export function rowKey(columns: Column[], original: unknown[], editKey: string[]): Record<string, unknown> {
  const key: Record<string, unknown> = {};
  for (const name of editKey) {
    const index = columns.findIndex(column => column.name === name);
    if (index < 0 || original[index] === null || original[index] === undefined) throw new Error('This row has no usable key.');
    if (typeof original[index] === 'number' && columnKind(columns[index].type) === 'integer' && !Number.isSafeInteger(original[index])) throw new Error('This row key exceeds safe numeric precision. Reload the data before editing.');
    Object.defineProperty(key, name, { value: original[index], enumerable: true });
  }
  if (!editKey.length) throw new Error('This table has no usable key.');
  return key;
}
export function rowDeletion(columns: Column[], original: unknown[], editKey: string[]): ChangeSet {
  return { changes: [{ op: 'delete', key: rowKey(columns, original, editKey) }] };
}
export function createChangeSetStore() { return writable<ChangeSet>({ changes: [] }); }
