export type CellKind = 'null' | 'integer' | 'decimal' | 'text' | 'boolean' | 'enum' | 'set' | 'date' | 'time' | 'datetime' | 'json' | 'binary' | 'uuid' | 'other';

export function columnKind(sqlType: string): CellKind {
  const rawType = sqlType.trim().toLowerCase();
  const type = rawType.replace(/\(.*/, '').trim();
  if (/^tinyint\(1\)/.test(rawType)) return 'boolean';
  if (/^(bool|boolean)$/.test(type)) return 'boolean';
  if (type === 'enum') return 'enum';
  if (type === 'set') return 'set';
  if (/^(tinyint|smallint|mediumint|int|integer|bigint|int2|int4|int8|serial|smallserial|bigserial|year)(\s|$)/.test(type)) return 'integer';
  if (/^(decimal|numeric|number|float|float4|float8|double|real|money)(\s|$)/.test(type)) return 'decimal';
  if (/^(datetime|timestamp|timestamptz)(\s|$)/.test(type)) return 'datetime';
  if (type === 'date') return 'date';
  if (/^(time|timetz|interval)(\s|$)/.test(type)) return 'time';
  if (/^(json|jsonb)$/.test(type)) return 'json';
  if (/^(binary|varbinary|tinyblob|blob|mediumblob|longblob|bytea|bit|varbit)(\s|$)/.test(type)) return 'binary';
  if (type === 'uuid') return 'uuid';
  if (/^(char|character|varchar|nvarchar|nchar|text|tinytext|mediumtext|longtext|citext|clob)(\s|$)/.test(type)) return 'text';
  return 'other';
}

export function isBinaryValue(value: unknown): value is { type: 'blob'; hex: string } {
  return typeof value === 'object' && value !== null && 'type' in value && value.type === 'blob' && 'hex' in value && typeof value.hex === 'string';
}

export function cellValue(value: unknown, sqlType: string) {
  const kind = value === null ? 'null' : isBinaryValue(value) ? 'binary' : columnKind(sqlType);
  // Preserve source strings, including datetime precision and timezone.
  const text = value === null ? 'NULL'
    : isBinaryValue(value) ? `0x${value.hex}`
    : typeof value === 'string' ? value
    : typeof value === 'object' ? JSON.stringify(value)
    : String(value);
  const label = kind === 'null' ? 'SQL NULL' : `${sqlType || 'Unknown type'}: ${text}`;
  return { kind, text, label, numeric: kind === 'integer' || kind === 'decimal' };
}
