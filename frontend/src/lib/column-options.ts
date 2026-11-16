/** Decode the quoted members reported in MySQL/MariaDB COLUMN_TYPE. */
export function columnOptions(type: string): { kind: 'enum' | 'set'; values: string[] } | null {
  const match = /^\s*(enum|set)\s*\(([\s\S]*)\)\s*$/i.exec(type);
  if (!match) return null;
  const values: string[] = [];
  const source = match[2];
  let index = 0;
  const whitespace = () => { while (/\s/.test(source[index] ?? '') && index < source.length) index++; };
  while (index < source.length) {
    whitespace();
    if (source[index++] !== "'") return null;
    let value = '';
    let closed = false;
    while (index < source.length) {
      const char = source[index++];
      if (char === "'") {
        if (source[index] === "'") { value += "'"; index++; }
        else { closed = true; break; }
      } else if (char === '\\') {
        if (index >= source.length) return null;
        const escaped = source[index++];
        const escapes: Record<string, string> = { '0': '\0', b: '\b', n: '\n', r: '\r', t: '\t', Z: '\x1a' };
        value += escapes[escaped] ?? (escaped === '%' || escaped === '_' ? `\\${escaped}` : escaped);
      } else value += char;
    }
    if (!closed) return null;
    values.push(value);
    whitespace();
    if (index === source.length) break;
    if (source[index++] !== ',') return null;
    whitespace();
    if (index === source.length) return null;
  }
  return values.length ? { kind: match[1].toLowerCase() as 'enum' | 'set', values } : null;
}
