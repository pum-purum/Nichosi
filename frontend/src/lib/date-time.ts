/** Format timestamps using the browser timezone and a stable English format. */
export function browserTimeZoneLabel(value?: string | null): string {
  const date = value ? new Date(value) : new Date();
  if (Number.isNaN(date.getTime())) return '';
  const offset = -date.getTimezoneOffset();
  if (offset === 0) return 'UTC';
  const minutes = Math.abs(offset);
  const remainder = minutes % 60;
  return `UTC${offset > 0 ? '+' : '-'}${Math.floor(minutes / 60)}${remainder ? `:${String(remainder).padStart(2, '0')}` : ''}`;
}

export function formatDateTime(value: string | null): string {
  if (!value) return 'Unavailable';
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  const parts = new Intl.DateTimeFormat('en-GB', {
    day: '2-digit', month: 'short', year: 'numeric',
    hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false,
  }).formatToParts(date);
  const part = (type: Intl.DateTimeFormatPartTypes) => parts.find(p => p.type === type)?.value;
  return `${part('day')} ${part('month')} ${part('year')}, ${part('hour')}:${part('minute')}:${part('second')}`;
}
