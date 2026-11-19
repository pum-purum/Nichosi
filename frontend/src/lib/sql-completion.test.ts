import { describe, expect, it } from 'vitest';
import { CompletionContext } from '@codemirror/autocomplete';
import { EditorState } from '@codemirror/state';
import { sql, PostgreSQL } from '@codemirror/lang-sql';
import { tableCompletions } from './sql-completion';

const tables = [{ name: 'public.users', displayName: 'users', schemaName: 'public', rowsEstimate: 0, dataBytes: 0, indexBytes: 0, columns: [] }];
function complete(doc: string) {
  const state = EditorState.create({ doc, extensions: [sql({ dialect: PostgreSQL })] });
  return tableCompletions(new CompletionContext(state, doc.length, false), tables, 'postgres');
}
describe('table completions', () => {
  it('waits for three characters and matches without case sensitivity', () => {
    expect(complete('SELECT * FROM us')).toBeNull();
    expect(complete('SELECT * FROM USE')?.options[0]).toMatchObject({ label: 'users', apply: '"public"."users"' });
    expect(complete('SELECT * FROM xyz')?.options).toEqual([]);
  });
  it('does not suggest tables in strings or comments', () => {
    expect(complete("SELECT 'use")).toBeNull();
    expect(complete('-- use')).toBeNull();
  });
});
