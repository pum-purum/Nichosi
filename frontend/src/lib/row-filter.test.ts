import { describe, expect, it } from 'vitest';
import { formatRowFilter, parseRowFilter } from './row-filter';

describe('row filters', () => {
  it('distinguishes BETWEEN AND from condition connectors', () => {
    expect(parseRowFilter('age BETWEEN 18 AND 65 OR name LIKE "Ann%" AND deleted IS NULL')).toEqual([
      { column: 'age', op: 'between', value: ['18', '65'], connector: 'and' },
      { column: 'name', op: 'like', value: 'Ann%', connector: 'or' },
      { column: 'deleted', op: 'isNull', connector: 'and' },
    ]);
  });
  it('round trips escaped names, strings, lists and large numeric values', () => {
    const parsed = parseRowFilter('`a``b` IN ("x,y", \'it\'\'s\', "\\n") OR id >= 18446744073709551615 AND `odd name` IS NOT NULL');
    expect(parseRowFilter(formatRowFilter(parsed))).toEqual(parsed);
    expect(parsed[1].value).toBe('18446744073709551615');
  });
  it('preserves empty strings and keyword strings', () => {
    expect(parseRowFilter('name = "" OR name = "OR"')[1].value).toBe('OR');
  });
  it.each(['id =', 'id = NULL', 'id IN ()', 'id BETWEEN 1 OR 2', 'id = 1 OR', '(id = 1)', 'id = 1; DROP TABLE t', 'id = "unterminated', 'id = 1 name = 2', 'id LIKE `column`', 'id constructor x', 'id toString x'])('rejects incomplete or unsupported expressions: %s', expression => {
    expect(() => parseRowFilter(expression)).toThrow();
  });
  it('validates exact column names', () => {
    expect(() => parseRowFilter('missing = 1', ['id'])).toThrow('Unknown column');
    expect(parseRowFilter('`AND` = 1', ['AND'])).toHaveLength(1);
  });
  it('bounds IN values', () => {
    expect(() => parseRowFilter(`id IN (${Array(1001).fill('1').join(',')})`)).toThrow('1000');
  });
  it('bounds condition count', () => {
    expect(() => parseRowFilter(Array(101).fill('id = 1').join(' OR '))).toThrow('100');
  });
});
