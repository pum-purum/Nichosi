import type { components } from './api/schema';

export type RowFilter = components['schemas']['Filter'];
export const filterOperators: { op: RowFilter['op']; label: string }[] = [
  { op: 'eq', label: '=' }, { op: 'ne', label: '!=' }, { op: 'lt', label: '<' },
  { op: 'lte', label: '<=' }, { op: 'gt', label: '>' }, { op: 'gte', label: '>=' },
  { op: 'between', label: 'BETWEEN' }, { op: 'like', label: 'LIKE' },
  { op: 'in', label: 'IN' }, { op: 'isNull', label: 'IS NULL' }, { op: 'notNull', label: 'IS NOT NULL' },
];
type Token = { value: string; quoted: boolean; identifier: boolean };
function tokenize(text: string): Token[] {
  const result: Token[] = [];
  let i = 0;
  while (i < text.length) {
    if (/\s/.test(text[i])) { i++; continue; }
    const quote = text[i];
    if (quote === '`' || quote === "'" || quote === '"') {
      let value = ''; let closed = false; i++;
      while (i < text.length) {
        const char = text[i++];
        if (char === quote) {
          if (text[i] === quote) { value += quote; i++; continue; }
          closed = true; break;
        }
        if (char === '\\' && quote === '"') {
          const escape = text[i++];
          if (escape === 'u') {
            const hex = text.slice(i, i + 4);
            if (!/^[\da-f]{4}$/i.test(hex)) throw new Error('Invalid Unicode escape.');
            value += String.fromCharCode(parseInt(hex, 16)); i += 4;
          } else {
            const escapes: Record<string, string> = { '"': '"', '\\': '\\', '/': '/', n: '\n', r: '\r', t: '\t', b: '\b', f: '\f' };
            if (!Object.prototype.hasOwnProperty.call(escapes, escape)) throw new Error('Invalid string escape.');
            value += escapes[escape];
          }
        } else value += char;
      }
      if (!closed) throw new Error('Close the quoted value or column name.');
      result.push({ value, quoted: true, identifier: quote === '`' }); continue;
    }
    const match = /^(<=|>=|!=|<>|[=<>(),]|[^\s=<>!(),`'"]+)/.exec(text.slice(i));
    if (!match) throw new Error(`Unexpected character at position ${i + 1}.`);
    result.push({ value: match[0], quoted: false, identifier: false }); i += match[0].length;
  }
  return result;
}

export function parseRowFilter(text: string, columns?: readonly string[]): RowFilter[] {
  const tokens = tokenize(text); let i = 0;
  const keyword = (word: string) => tokens[i] && !tokens[i].quoted && tokens[i].value.toUpperCase() === word;
  const expect = (word: string) => { if (!keyword(word)) throw new Error(`Expected ${word}.`); i++; };
  const scalar = (): string => {
    const token = tokens[i++];
    if (!token || token.identifier || (!token.quoted && /^(AND|OR|NULL|BETWEEN|IN|IS|LIKE|NOT|[(),<>=!]+)$/i.test(token.value))) throw new Error('Enter a value; quote text containing spaces or keywords.');
    return token.value;
  };
  const filters: RowFilter[] = [];
  while (i < tokens.length) {
    let connector: RowFilter['connector'] = 'and';
    if (filters.length) {
      if (keyword('AND')) connector = 'and'; else if (keyword('OR')) connector = 'or'; else throw new Error('Expected AND or OR between conditions.');
      i++;
    }
    const column = tokens[i++];
    if (!column || (column.quoted && !column.identifier) || /^[(),<>=!]+$/.test(column.value)) throw new Error('Choose a column. Use backticks for names with spaces.');
    if (columns && !columns.includes(column.value)) throw new Error(`Unknown column: ${column.value}`);
    let op: RowFilter['op']; let value: unknown;
    const symbol = tokens[i]?.value;
    const comparisons: Record<string, RowFilter['op']> = { '=': 'eq', '!=': 'ne', '<>': 'ne', '<': 'lt', '<=': 'lte', '>': 'gt', '>=': 'gte' };
    if (tokens[i] && !tokens[i].quoted && Object.prototype.hasOwnProperty.call(comparisons, symbol)) { op = comparisons[symbol]; i++; value = scalar(); }
    else if (keyword('IS')) { i++; const not = keyword('NOT'); if (not) i++; expect('NULL'); op = not ? 'notNull' : 'isNull'; }
    else if (keyword('LIKE')) { i++; op = 'like'; value = scalar(); }
    else if (keyword('BETWEEN')) { i++; op = 'between'; const first = scalar(); expect('AND'); value = [first, scalar()]; }
    else if (keyword('IN')) {
      i++; op = 'in'; expect('('); const values = [scalar()];
      while (keyword(',')) { i++; values.push(scalar()); if (values.length > 1000) throw new Error('Use at most 1000 IN values.'); }
      expect(')'); value = values;
    } else throw new Error('Choose an operator: =, !=, <, <=, >, >=, BETWEEN, LIKE, IN, IS NULL or IS NOT NULL.');
    filters.push({ column: column.value, op, connector, ...(value !== undefined ? { value } : {}) });
    if (filters.length > 100) throw new Error('Use at most 100 conditions.');
  }
  return filters;
}

const literal = (value: unknown) => JSON.stringify(String(value ?? ''));
export function formatRowFilter(filters: RowFilter[]): string {
  return filters.map((filter, index) => {
    const start = `${index ? `${(filter.connector ?? 'and').toUpperCase()} ` : ''}\`${filter.column.replace(/`/g, '``')}\` ${filterOperators.find(item => item.op === filter.op)?.label}`;
    if (filter.op === 'isNull' || filter.op === 'notNull') return start;
    if (filter.op === 'between' && Array.isArray(filter.value)) return `${start} ${literal(filter.value[0])} AND ${literal(filter.value[1])}`;
    if (filter.op === 'in' && Array.isArray(filter.value)) return `${start} (${filter.value.map(literal).join(', ')})`;
    return `${start} ${literal(filter.value)}`;
  }).join(' ');
}
