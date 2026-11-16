package driver

import (
	"context"
	"database/sql"
	"math/big"
)

// postgresNextIdentity reads a snapshot without consuming a sequence value.
// Cached or concurrent allocations can make this estimate differ from nextval.
func postgresNextIdentity(ctx context.Context, db *sql.DB, oid int64, column string) (*string, error) {
	var schema, name, increment, minimum, maximum string
	var cycle bool
	err := db.QueryRowContext(ctx, `SELECT n.nspname,s.relname,q.seqincrement::text,q.seqmin::text,q.seqmax::text,q.seqcycle
FROM pg_attribute a
JOIN pg_class s ON s.relkind='S' AND (
 (a.attidentity<>'' AND EXISTS(SELECT 1 FROM pg_depend d WHERE d.classid='pg_class'::regclass AND d.objid=s.oid AND d.refclassid='pg_class'::regclass AND d.refobjid=a.attrelid AND d.refobjsubid=a.attnum AND d.deptype='i'))
 OR (a.attidentity='' AND EXISTS(SELECT 1 FROM pg_attrdef f JOIN pg_depend d ON d.classid='pg_attrdef'::regclass AND d.objid=f.oid WHERE f.adrelid=a.attrelid AND f.adnum=a.attnum AND d.refclassid='pg_class'::regclass AND d.refobjid=s.oid)))
JOIN pg_namespace n ON n.oid=s.relnamespace JOIN pg_sequence q ON q.seqrelid=s.oid
WHERE a.attrelid=$1 AND a.attname=$2 LIMIT 1`, oid, column).Scan(&schema, &name, &increment, &minimum, &maximum, &cycle)
	if err != nil {
		return nil, err
	}
	var last string
	var called bool
	err = db.QueryRowContext(ctx, `SELECT last_value::text,is_called FROM `+PostgresQuoteIdent(schema)+"."+PostgresQuoteIdent(name)).Scan(&last, &called)
	if err != nil {
		return nil, err
	}
	value, ok := new(big.Int).SetString(last, 10)
	if !ok {
		return nil, nil
	}
	if called {
		step, ok := new(big.Int).SetString(increment, 10)
		if !ok {
			return nil, nil
		}
		low, ok := new(big.Int).SetString(minimum, 10)
		if !ok {
			return nil, nil
		}
		high, ok := new(big.Int).SetString(maximum, 10)
		if !ok {
			return nil, nil
		}
		value.Add(value, step)
		if value.Cmp(low) < 0 || value.Cmp(high) > 0 {
			if !cycle {
				return nil, nil
			}
			if step.Sign() > 0 {
				value.Set(low)
			} else {
				value.Set(high)
			}
		}
	}
	result := value.String()
	return &result, nil
}
