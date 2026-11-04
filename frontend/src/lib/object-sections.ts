import type { components } from './api/schema';

export type ObjectType = components['schemas']['ObjectType'];
export const objectSectionLabels: Record<ObjectType, string> = {
  table: 'Tables', view: 'Views', materialized_view: 'Materialized views',
  trigger: 'Triggers', role: 'Roles', user: 'Users', procedure: 'Procedures',
  function: 'Functions', event: 'Events', schema: 'Schemas', sequence: 'Sequences',
  type: 'Types', extension: 'Extensions', index: 'Indexes', cron: 'Cron jobs',
};

type Driver = components['schemas']['Connection']['driver'];

export function objectSectionType(type: ObjectType, driver?: Driver): ObjectType {
  return driver === 'postgres' && type === 'role' ? 'user' : type;
}

export function postgresAccountBadge(object: components['schemas']['DatabaseObject']) {
  if (object.type !== 'role' || object.details?.['Login allowed'] !== 'false') return null;
  return { label: 'Built-in', title: 'NOLOGIN role grouped as Built-in. Login is disabled; this does not imply that PostgreSQL created the role.' };
}

export function objectSectionsFor(types: readonly ObjectType[], driver?: Driver) {
  const groupedTypes = types.map(type => objectSectionType(type, driver));
  return (Object.keys(objectSectionLabels) as ObjectType[])
    .filter(type => groupedTypes.includes(type))
    .map(type => ({ type, label: objectSectionLabels[type] }));
}

export function isViewType(type: ObjectType): type is 'view' | 'materialized_view' {
  return type === 'view' || type === 'materialized_view';
}
