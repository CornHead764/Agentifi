package pgimport

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"

	"github.com/CornHead764/agentifi/backend/internal/sqlitedb"
)

// kind is how one column's values are read from Postgres and written to
// SQLite.
type kind int

const (
	kindText kind = iota
	kindInteger
	kindBool
	kindReal
	kindMoney
	kindDecimal
	kindUUID
	kindDate
	kindTimestamp
	kindJSON
	kindBlob
)

// pgType is a Postgres column's type as information_schema.columns names it.
type pgType struct {
	dataType string
	udtName  string
	scale    *int64
}

// classify decides a column's kind from its Postgres type and the storage
// class the SQLite schema declares for it, refusing a pair that does not
// agree. A numeric becomes money exactly when the SQLite column is INTEGER,
// so the schema, not a list here, says which decimals are money.
func classify(pg pgType, sqliteType string) (kind, error) {
	k, err := classifyPostgres(pg, sqliteType)
	if err != nil {
		return 0, err
	}
	if want := storageClass(k); sqliteType != want {
		return 0, fmt.Errorf("Postgres %s should be SQLite %s, but the SQLite schema declares %s", pg.dataType, want, sqliteType)
	}
	return k, nil
}

func classifyPostgres(pg pgType, sqliteType string) (kind, error) {
	switch pg.dataType {
	case "text", "character varying", "character":
		return kindText, nil
	case "smallint", "integer", "bigint":
		return kindInteger, nil
	case "boolean":
		return kindBool, nil
	case "double precision", "real":
		return kindReal, nil
	case "uuid":
		return kindUUID, nil
	case "date":
		return kindDate, nil
	case "timestamp with time zone":
		return kindTimestamp, nil
	case "jsonb", "json":
		return kindJSON, nil
	case "bytea":
		return kindBlob, nil
	case "ARRAY":
		switch pg.udtName {
		case "_uuid", "_text", "_varchar", "_int2", "_int4", "_int8":
			return kindJSON, nil
		}
		return 0, fmt.Errorf("Postgres array %s has no SQLite conversion", pg.udtName)
	case "numeric":
		if sqliteType != "INTEGER" {
			return kindDecimal, nil
		}
		if pg.scale == nil || *pg.scale != 2 {
			return 0, fmt.Errorf("a money column must be numeric with scale 2 in Postgres")
		}
		return kindMoney, nil
	}
	return 0, fmt.Errorf("Postgres type %s has no SQLite conversion", pg.dataType)
}

func storageClass(k kind) string {
	switch k {
	case kindInteger, kindBool, kindMoney:
		return "INTEGER"
	case kindReal:
		return "REAL"
	case kindBlob:
		return "BLOB"
	}
	return "TEXT"
}

// selectExpr is the Postgres expression that reads the column quoted. Exact
// decimals are read as text so no value passes through a float.
func selectExpr(k kind, pg pgType, quoted string) string {
	switch {
	case pg.dataType == "ARRAY":
		return "array_to_json(" + quoted + ")::text"
	case k == kindMoney, k == kindDecimal, k == kindUUID, k == kindJSON:
		return quoted + "::text"
	}
	return quoted
}

// scanTarget is a fresh destination for one value of this kind.
func (k kind) scanTarget() any {
	switch k {
	case kindInteger:
		return new(pgtype.Int8)
	case kindBool:
		return new(pgtype.Bool)
	case kindReal:
		return new(pgtype.Float8)
	case kindDate:
		return new(pgtype.Date)
	case kindTimestamp:
		return new(pgtype.Timestamptz)
	case kindBlob:
		return new([]byte)
	}
	return new(pgtype.Text)
}

// value converts a scanned target to what the SQLite column stores: nil, an
// int64, a float64, a string or a []byte.
func (k kind) value(target any) (any, error) {
	switch t := target.(type) {
	case *pgtype.Int8:
		if !t.Valid {
			return nil, nil
		}
		return t.Int64, nil
	case *pgtype.Bool:
		if !t.Valid {
			return nil, nil
		}
		return boolean(t.Bool), nil
	case *pgtype.Float8:
		if !t.Valid {
			return nil, nil
		}
		return t.Float64, nil
	case *pgtype.Date:
		if !t.Valid {
			return nil, nil
		}
		return dateText(*t)
	case *pgtype.Timestamptz:
		if !t.Valid {
			return nil, nil
		}
		return timestampText(*t)
	case *[]byte:
		if *t == nil {
			return nil, nil
		}
		return *t, nil
	case *pgtype.Text:
		if !t.Valid {
			return nil, nil
		}
		switch k {
		case kindMoney:
			return moneyHundredths(t.String)
		case kindDecimal:
			return decimalText(t.String)
		case kindUUID:
			return uuidText(t.String)
		case kindJSON:
			return jsonText(t.String)
		}
		return t.String, nil
	}
	return nil, fmt.Errorf("pgimport: no conversion for %T", target)
}

func boolean(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// moneyHundredths is a money column's decimal text as the integer hundredths
// SQLite stores. A value with a nonzero third decimal place is refused rather
// than rounded.
func moneyHundredths(text string) (int64, error) {
	d, err := decimal.NewFromString(text)
	if err != nil {
		return 0, fmt.Errorf("%q is not a decimal: %w", text, err)
	}
	hundredths := d.Shift(2)
	if !hundredths.IsInteger() {
		return 0, fmt.Errorf("%s has more than two decimal places", text)
	}
	whole := hundredths.BigInt()
	if !whole.IsInt64() {
		return 0, fmt.Errorf("%s does not fit a money column", text)
	}
	return whole.Int64(), nil
}

// decimalText is an exact decimal as the text SQLite stores, written the way
// dbconv.Numeric writes it.
func decimalText(text string) (string, error) {
	d, err := decimal.NewFromString(text)
	if err != nil {
		return "", fmt.Errorf("%q is not a decimal: %w", text, err)
	}
	return d.String(), nil
}

func uuidText(text string) (string, error) {
	id, err := uuid.Parse(text)
	if err != nil {
		return "", fmt.Errorf("%q is not a uuid: %w", text, err)
	}
	return id.String(), nil
}

func dateText(d pgtype.Date) (string, error) {
	if d.InfinityModifier != pgtype.Finite {
		return "", fmt.Errorf("an infinite date has no SQLite form")
	}
	if y := d.Time.Year(); y < 1 || y > 9999 {
		return "", fmt.Errorf("the date %s is outside years 1 to 9999", d.Time.Format(sqlitedb.DateFormat))
	}
	return d.Time.Format(sqlitedb.DateFormat), nil
}

func timestampText(ts pgtype.Timestamptz) (string, error) {
	if ts.InfinityModifier != pgtype.Finite {
		return "", fmt.Errorf("an infinite timestamp has no SQLite form")
	}
	if y := ts.Time.UTC().Year(); y < 1 || y > 9999 {
		return "", fmt.Errorf("the timestamp %s is outside years 1 to 9999", ts.Time.UTC())
	}
	return sqlitedb.FormatTimestamp(ts.Time), nil
}

// jsonText is a JSON document without the whitespace Postgres prints, the
// form the server's own writes take.
func jsonText(text string) (string, error) {
	var out bytes.Buffer
	if err := json.Compact(&out, []byte(text)); err != nil {
		return "", fmt.Errorf("%q is not JSON: %w", text, err)
	}
	return out.String(), nil
}
