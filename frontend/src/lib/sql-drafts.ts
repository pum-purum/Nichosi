import { get, writable } from 'svelte/store';

// Keep unfinished scripts while navigating; never persist SQL to browser storage.
const drafts = writable<Record<string, { sql: string; rowLimit: number }>>({});
export function sqlDraft(connectionId: string) {
  return get(drafts)[connectionId] ?? { sql: 'SELECT 1;', rowLimit: 200 };
}
export function saveSqlDraft(connectionId: string, sql: string, rowLimit: number) {
  drafts.update(current => ({ ...current, [connectionId]: { sql, rowLimit } }));
}
