import { describe, expect, it } from 'vitest';
import { isViewType, objectSectionsFor, objectSectionType, postgresAccountBadge } from './object-sections';

describe('database object capabilities', () => {
  it('groups PostgreSQL LOGIN and NOLOGIN accounts into Users with a combined count source', () => {
    expect(objectSectionsFor(['role', 'user', 'table'], 'postgres').map(section => section.type))
      .toEqual(['table', 'user']);
    expect(['role', 'user'].map(type => objectSectionType(type as 'role' | 'user', 'postgres')))
      .toEqual(['user', 'user']);
    expect(objectSectionType('role', 'mysql')).toBe('role');
  });
  it('marks only PostgreSQL NOLOGIN accounts as Built-in with an explicit origin caveat', () => {
    const badge = postgresAccountBadge({ name: 'custom_group', type: 'role', details: { 'Login allowed': 'false' } });
    expect(badge?.label).toBe('Built-in');
    expect(badge?.title).toContain('does not imply');
    expect(postgresAccountBadge({ name: 'postgres', type: 'user', details: { 'Login allowed': 'true' } })).toBeNull();
  });
  it('keeps supported empty categories and omits unsupported MySQL events for PostgreSQL', () => {
    const sections = objectSectionsFor(['table', 'view', 'role', 'user', 'materialized_view', 'sequence']);
    expect(sections.map(section => section.type)).toEqual(['table', 'view', 'materialized_view', 'role', 'user', 'sequence']);
    expect(sections.some(section => section.type === 'event' || section.type === 'cron')).toBe(false);
  });
  it('only displays cron when the server advertises it', () => {
    expect(objectSectionsFor(['table', 'cron'])).toContainEqual({ type: 'cron', label: 'Cron jobs' });
    expect(objectSectionsFor(['table'])).toEqual([{ type: 'table', label: 'Tables' }]);
  });
  it('preserves MySQL categories', () => {
    expect(objectSectionsFor(['table', 'view', 'trigger', 'role', 'user', 'procedure', 'function', 'event']).map(section => section.label))
      .toEqual(['Tables', 'Views', 'Triggers', 'Roles', 'Users', 'Procedures', 'Functions', 'Events']);
  });
  it('uses the view data endpoints for both regular and materialized views', () => {
    expect(isViewType('view')).toBe(true);
    expect(isViewType('materialized_view')).toBe(true);
    expect(isViewType('sequence')).toBe(false);
  });
});
