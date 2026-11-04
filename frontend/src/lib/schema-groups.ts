import type { components } from './api/schema';
import { matchesObjectFilter } from './object-filter';
import type { ObjectType } from './object-sections';

type DatabaseObject = components['schemas']['DatabaseObject'];
export type SchemaEntry = Pick<DatabaseObject, 'name' | 'displayName' | 'schemaName'>;
export const schemaGroupedTypes: readonly ObjectType[] = ['table', 'view', 'materialized_view', 'trigger', 'procedure', 'function', 'sequence', 'type', 'index'];

export function schemaGroupKey(type: ObjectType, schemaName: string) { return JSON.stringify([type, schemaName]); }
export function schemaGroupOpen(collapsed: boolean | undefined, query: string) { return !!query.trim() || !collapsed; }
export function matchesSchemaEntry(query: string, item: SchemaEntry) {
  return matchesObjectFilter(query, item.displayName ?? item.name, item.name, item.schemaName ?? '');
}

/** Group by explicit catalog identity, never by punctuation in a quoted SQL name. */
export function groupSchemaEntries<T extends SchemaEntry>(entries: readonly T[], schemas: readonly DatabaseObject[], query: string) {
  const groups = new Map<string, { schemaName: string; schema?: DatabaseObject; items: T[] }>();
  for (const schema of schemas) {
    if (schema.type === 'schema' && schema.schemaName !== undefined) groups.set(schema.schemaName, { schemaName: schema.schemaName, schema, items: [] });
  }
  for (const item of entries) {
    const schemaName = item.schemaName ?? '';
    const group = groups.get(schemaName) ?? { schemaName, items: [] };
    group.items.push(item);
    groups.set(schemaName, group);
  }
  return [...groups.values()].sort((a, b) => a.schemaName.localeCompare(b.schemaName)).map(group => ({
    ...group,
    items: group.items.filter(item => matchesSchemaEntry(query, item)),
  })).filter(group => !query.trim() || group.items.length > 0 || matchesObjectFilter(query, group.schemaName, group.schema?.displayName ?? ''));
}
