import { describe, expect, it } from 'vitest';
import { sqlStatementError } from './sql-statement';

describe('single SQL statement validation', () => {
  it.each(['mysql', 'postgres', 'sqlite'])('rejects multiple statements for %s', dialect => {
    expect(sqlStatementError('SELECT 1; SELECT 2;', dialect)).toContain('only one');
    expect(sqlStatementError("SELECT ';', 'it''s;'; /* ; */ -- ;\n", dialect)).toBe('');
    expect(sqlStatementError('; SELECT 1;;', dialect)).toBe('');
    if (dialect !== 'postgres') expect(sqlStatementError('SELECT 1 /* /* nested */ */', dialect)).not.toBe('');
    expect(sqlStatementError('-- comment', dialect)).toContain('Enter one');
    expect(sqlStatementError("SELECT 'unfinished", dialect)).not.toBe('');
  });
  it('supports PostgreSQL dollar quotes and nested comments', () => {
    expect(sqlStatementError('DO $body$ BEGIN PERFORM 1; PERFORM 2; END $body$;', 'postgres')).toBe('');
    expect(sqlStatementError('SELECT 1; /* outer /* inner ; */ ; */ SELECT 2;', 'postgres')).toContain('only one');
  });
  it('supports SQLite trigger bodies', () => {
    const sql = 'CREATE TRIGGER t AFTER INSERT ON a BEGIN UPDATE b SET x=CASE WHEN x=1 THEN 2 ELSE 3 END; SELECT 1; END;';
    expect(sqlStatementError(sql, 'sqlite')).toBe('');
    expect(sqlStatementError(sql + ' SELECT 2;', 'sqlite')).toContain('only one');
  });
  it('supports MySQL delimiters', () => {
    const sql = 'DELIMITER $$\nCREATE PROCEDURE p() BEGIN SELECT 1; SELECT 2; END$$\nDELIMITER ;\n';
    expect(sqlStatementError(sql, 'mysql')).toBe('');
    expect(sqlStatementError(sql + 'SELECT 2;', 'mysql')).toContain('only one');
  });
});
