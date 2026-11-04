import type { components } from './api/schema';

export type ObjectType = components['schemas']['ObjectType'];
export const objectSectionLabels: Record<ObjectType, string> = {
  table: 'Tables', view: 'Views', materialized_view: 'Materialized views',
  trigger: 'Triggers', role: 'Roles', user: 'Users', procedure: 'Procedures',
  function: 'Functions', event: 'Events', schema: 'Schemas', sequence: 'Sequences',
  type: 'Types', extension: 'Extensions', index: 'Indexes', cron: 'Cron jobs',
};

export function objectSectionsFor(types: readonly ObjectType[]) {
  return (Object.keys(objectSectionLabels) as ObjectType[])
    .filter(type => types.includes(type))
    .map(type => ({ type, label: objectSectionLabels[type] }));
}

export function isViewType(type: ObjectType): type is 'view' | 'materialized_view' {
  return type === 'view' || type === 'materialized_view';
}
