import { describe, expect, it } from 'vitest';
import { groupSchemaEntries, matchesSchemaEntry, schemaGroupKey, schemaGroupOpen } from './schema-groups';
import type { components } from './api/schema';

type Object = components['schemas']['DatabaseObject'];
const schemas: Object[] = [
  { name: '"billing.data"', type: 'schema', schemaName: 'billing.data', displayName: 'billing.data' },
  { name: 'public', type: 'schema', schemaName: 'public', displayName: 'public' },
  { name: 'empty', type: 'schema', schemaName: 'empty', displayName: 'empty' },
];
const entries: Object[] = [
  { name: '"billing.data"."a.b"', type: 'view', schemaName: 'billing.data', displayName: 'a.b' },
  { name: 'public."billing.data"', type: 'view', schemaName: 'public', displayName: 'billing.data' },
];

describe('schema sidebar groups', () => {
  it('uses explicit raw schema identities without splitting quoted identifiers', () => {
    const groups = groupSchemaEntries(entries, schemas, '');
    expect(groups.find(group => group.schemaName === 'billing.data')?.items.map(item => item.name)).toEqual(['"billing.data"."a.b"']);
    expect(groups.find(group => group.schemaName === 'public')?.items[0]?.name).toBe('public."billing.data"');
    expect(groups.find(group => group.schemaName === 'empty')?.items).toEqual([]);
    expect(groups.find(group => group.schemaName === 'empty')?.schema).toBe(schemas[2]);
  });
  it('matches short names, full opaque identifiers and raw schema names', () => {
    expect(matchesSchemaEntry('a.b', entries[0])).toBe(true);
    expect(matchesSchemaEntry('"billing.data".', entries[0])).toBe(true);
    expect(matchesSchemaEntry('billing.data a.b', entries[0])).toBe(true);
    expect(groupSchemaEntries(entries, schemas, 'billing.data').find(group => group.schemaName === 'billing.data')?.items).toEqual([entries[0]]);
    expect(groupSchemaEntries(entries, schemas, 'empty').map(group => group.schemaName)).toEqual(['empty']);
    expect(groupSchemaEntries(entries, schemas, 'unmatched')).toEqual([]);
  });
  it('keeps catalog ids unchanged and does not infer a missing schema from punctuation', () => {
    const unknown = { name: 'one.two.three' };
    const groups = groupSchemaEntries([unknown], [], '');
    expect(groups[0].schemaName).toBe('');
    expect(groups[0].items[0]).toBe(unknown);
  });
  it('forces matching groups open during search and restores saved collapse afterwards', () => {
    expect(schemaGroupOpen(true, '')).toBe(false);
    expect(schemaGroupOpen(true, 'a.b')).toBe(true);
    expect(schemaGroupOpen(true, '   ')).toBe(false);
    expect(schemaGroupOpen(undefined, '')).toBe(true);
    expect(schemaGroupKey('table', 'a.b')).not.toBe(schemaGroupKey('view', 'a.b'));
    expect(schemaGroupKey('table', 'a/b')).not.toBe(schemaGroupKey('table', 'a.b'));
  });
});
