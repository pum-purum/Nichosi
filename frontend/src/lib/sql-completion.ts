import type { CompletionContext, CompletionResult } from '@codemirror/autocomplete';
import { syntaxTree } from '@codemirror/language';
import type { components } from './api/schema';

export function tableCompletions(
  context: CompletionContext,
  tables: components['schemas']['TableInfo'][],
  driver: components['schemas']['Connection']['driver'],
): CompletionResult | null {
  const node = syntaxTree(context.state).resolveInner(context.pos, -1);
  if (/Comment|String/.test(node.name)) return null;
  const word = context.matchBefore(/[\p{L}\p{N}_$]+/u);
  if (!word || [...word.text].length < 3) return null;
  const quote = (name: string) => {
    const delimiter = driver === 'mysql' ? '`' : '"';
    return delimiter + name.split(delimiter).join(delimiter + delimiter) + delimiter;
  };
  return {
    from: word.from,
    options: tables.filter(table => (table.displayName ?? table.name).toLowerCase().startsWith(word.text.toLowerCase())).map(table => ({
      label: table.displayName ?? table.name,
      detail: table.schemaName,
      type: 'type',
      apply: table.schemaName
        ? `${quote(table.schemaName)}.${quote(table.displayName ?? table.name)}`
        : quote(table.name),
    })),
  };
}
