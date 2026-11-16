import { writable } from 'svelte/store';
import type { components } from './api/schema';
import { columnKind, isBinaryValue } from './cell-value';
import { columnOptions } from './column-options';

type Column = components['schemas']['Column'];
type ChangeSet = components['schemas']['ChangeSet'];
export type DraftValue = { text: string; isNull: boolean; useDefault?: boolean };
export function draftValue(value: unknown): DraftValue {
  return { text: value === null ? '' : typeof value === 'object' ? JSON.stringify(value) : String(value), isNull: value === null };
}
export function editableColumn(column: Column, value?: unknown): boolean {
  return !isBinaryValue(value) && !column.primaryKey && !column.generated && columnKind(column.type) !== 'binary';
}
export function fieldError(column: Column, after: DraftValue, insert = false): string {
  if (after.isNull) return column.nullable ? '' : `${column.name} cannot be NULL.`;
  const kind = columnKind(column.type);
  const options = columnOptions(column.type);
  if (options) {
    const valid = options.kind === 'enum' ? options.values.includes(after.text) : after.text === '' || after.text.split(',').every(value => options.values.includes(value));
    return valid ? '' : `${column.name} requires a valid ${options.kind.toUpperCase()} selection.`;
  }
  if (kind === 'integer' && !/^[+-]?\d+$/.test(after.text)) return `${column.name} requires an integer.`;
  if (kind === 'decimal' && !/^[+-]?(?:\d+(?:\.\d*)?|\.\d+)(?:[eE][+-]?\d+)?$/.test(after.text)) return `${column.name} requires a number.`;
  if (kind === 'json') {
    try { JSON.parse(after.text); } catch { return `${column.name} requires valid JSON.`; }
  }
  if (insert && !column.nullable && kind !== 'text' && after.text === '') return `${column.name} is required.`;
  return '';
}
export function rowFieldErrors(columns: Column[], original: unknown[], draft: DraftValue[], insert = false): string[] {
  return columns.map((column, index) => {
    const after = draft[index];
    if (insert ? !insertableColumn(column) || after.useDefault : !editableColumn(column, original[index])) return '';
    const before = draftValue(original[index]);
    if (!insert && before.isNull === after.isNull && (after.isNull || before.text === after.text)) return '';
    return fieldError(column, after, insert);
  });
}
export function rowChanges(columns: Column[], original: unknown[], draft: DraftValue[], editKey: string[], insert = false): ChangeSet {
  const values: Record<string, unknown> = {};
  const key = insert ? undefined : rowKey(columns, original, editKey);
  columns.forEach((column, index) => {
    if (insert ? !insertableColumn(column) || draft[index].useDefault : !editableColumn(column, original[index])) return;
    const before = draftValue(original[index]);
    const after = draft[index];
    if (!insert && before.isNull === after.isNull && (after.isNull || before.text === after.text)) return;
    const error = fieldError(column, after, insert);
    if (error) throw new Error(error);
    let value: unknown = after.isNull ? null : after.text;
    const kind = columnKind(column.type);
    // Go serializes MySQL DATE/DATETIME as RFC3339 in UTC. Keep fractional precision.
    const hasTimeZone = /\bwith time zone\b|^timestamptz\b/i.test(column.type);
    if (!after.isNull && !hasTimeZone && (kind === 'date' || kind === 'datetime')) {
      const temporal = /^(\d{4}-\d{2}-\d{2})T(\d{2}:\d{2}:\d{2}(?:\.\d+)?)(?:Z|[+]00:00)$/.exec(after.text);
      if (temporal) value = kind === 'date' ? temporal[1] : `${temporal[1]} ${temporal[2]}`;
    }
    Object.defineProperty(values, column.name, { value, enumerable: true });
  });
  return { changes: insert ? [{ op: 'insert', values }] : Object.keys(values).length ? [{ op: 'update', key, values }] : [] };
}
export function rowKey(columns: Column[], original: unknown[], editKey: string[]): Record<string, unknown> {
  const key: Record<string, unknown> = {};
  for (const name of editKey) {
    const index = columns.findIndex(column => column.name === name);
    if (isBinaryValue(original[index])) throw new Error('Binary row keys are read-only.');
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

export function insertableColumn(column: Column): boolean {
  return !column.generated && columnKind(column.type) !== 'binary';
}
export function insertionDraft(columns: Column[]): DraftValue[] {
  return columns.map(column => ({ text: '', isNull: column.nullable && column.defaultValue === undefined, useDefault: !!column.generated || !!column.autoIncrement || column.defaultValue !== undefined || !insertableColumn(column) }));
}
