// Match the server's separators, including dialect-specific quoted bodies.
export function sqlStatementError(source: string, dialect: string): string {
  let count = 0, active = false, delimiter = ';';
  let words: string[] = [], blocks: string[] = [];
  let trigger = false;
  for (let i = 0; i < source.length;) {
    const c = source[i];
    if (/\s/.test(c)) { i++; continue; }
    if (dialect === 'mysql' && !active && !source.slice(source.lastIndexOf('\n', i - 1) + 1, i).trim()) {
      const directive = /^DELIMITER\s+(\S+)[^\S\r\n]*(?:\r?\n|$)/i.exec(source.slice(i));
      if (directive) { delimiter = directive[1]; i += directive[0].length; continue; }
    }
    if (source.startsWith(delimiter, i) && !blocks.length) {
      if (active) count++;
      active = false; words = []; trigger = false; i += delimiter.length; continue;
    }
    if ((source.startsWith('--', i) && (dialect !== 'mysql' || !source[i + 2] || /\s/.test(source[i + 2]))) || (c === '#' && dialect === 'mysql')) {
      while (i < source.length && !/[\r\n]/.test(source[i])) i++;
      continue;
    }
    if (source.startsWith('/*', i)) {
      let depth = 1; i += 2;
      while (i < source.length && depth) {
        if (source.startsWith('/*', i)) {
          if (dialect !== 'postgres') return 'Nested SQL comments are not supported.';
          depth++; i += 2;
        }
        else if (source.startsWith('*/', i)) { depth--; i += 2; }
        else i++;
      }
      if (depth) return 'Complete the SQL comment before executing.';
      continue;
    }
    active = true;
    if (c === "'" || c === '"' || c === '`' || (c === '[' && dialect === 'sqlite')) {
      const quote = c === '[' ? ']' : c;
      let closed = false; i++;
      while (i < source.length) {
        if (source[i] === '\\' && dialect !== 'sqlite') return 'Use doubled quotes instead of backslash escapes.';
        if (source[i++] === quote) {
          if (source[i] === quote) { i++; continue; }
          closed = true; break;
        }
      }
      if (!closed) return 'Complete the quoted SQL value before executing.';
      words.push('<QUOTED>'); continue;
    }
    if (c === '$' && dialect === 'postgres') {
      const tag = /^(?:\$[A-Za-z_][A-Za-z_0-9]*\$|\$\$)/.exec(source.slice(i))?.[0];
      if (tag) {
        const end = source.indexOf(tag, i + tag.length);
        if (end < 0) return 'Complete the dollar-quoted SQL value before executing.';
        i = end + tag.length; words.push('<QUOTED>'); continue;
      }
    }
    const word = /^[A-Za-z_][A-Za-z_0-9$]*/.exec(source.slice(i))?.[0];
    if (word) {
      const boundary = delimiter !== ';' ? word.indexOf(delimiter) : -1;
      const token = boundary > 0 ? word.slice(0, boundary) : word;
      const upper = token.toUpperCase(); words.push(upper); i += token.length;
      if (dialect === 'sqlite' && words[0] === 'CREATE') {
        if (upper === 'TRIGGER') trigger = true;
        if (trigger && (upper === 'BEGIN' || upper === 'CASE')) blocks.push(upper);
        if (trigger && upper === 'END') blocks.pop();
      }
    } else { words.push(c); i++; }
  }
  if (active) count++;
  return count > 1 ? 'Enter only one SQL statement. Execute statements separately.' : count === 0 ? 'Enter one SQL statement.' : '';
}
