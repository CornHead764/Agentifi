package pgimport

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

func TestMoneyHundredths(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want int64
	}{
		{"12.50", 1250},
		{"-40.00", -4000},
		{"0.00", 0},
		{"100", 10000},
		{"1.5", 150},
		{"300.100", 30010},
		{"9999999999999.99", 999999999999999},
	} {
		got, err := moneyHundredths(tc.in)
		if err != nil {
			t.Errorf("moneyHundredths(%q): %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("moneyHundredths(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}

	for _, in := range []string{"12.345", "0.001", "-0.005", "100000000000000000000.00", "ten", ""} {
		if got, err := moneyHundredths(in); err == nil {
			t.Errorf("moneyHundredths(%q) = %d, want an error", in, got)
		}
	}
}

func TestDecimalText(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"0.0450000000", "0.045"},
		{"12.0000000000", "12"},
		{"-3.5000000000", "-3.5"},
		{"25.00", "25"},
		{"1234567890.1234567891", "1234567890.1234567891"},
	} {
		got, err := decimalText(tc.in)
		if err != nil {
			t.Errorf("decimalText(%q): %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("decimalText(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	if _, err := decimalText("NaN"); err == nil {
		t.Error("decimalText(NaN) succeeded, want an error")
	}
}

func TestUUIDText(t *testing.T) {
	got, err := uuidText("AAAAAAAA-BBBB-4CCC-8DDD-EEEEEEEEEEEE")
	if err != nil {
		t.Fatal(err)
	}
	if want := "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"; got != want {
		t.Errorf("uuidText = %q, want %q", got, want)
	}
	if _, err := uuidText("not-a-uuid"); err == nil {
		t.Error("uuidText(not-a-uuid) succeeded, want an error")
	}
}

func TestDateText(t *testing.T) {
	got, err := dateText(pgtype.Date{Time: time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC), Valid: true})
	if err != nil {
		t.Fatal(err)
	}
	if got != "2026-01-31" {
		t.Errorf("dateText = %q, want 2026-01-31", got)
	}
	if _, err := dateText(pgtype.Date{InfinityModifier: pgtype.Infinity, Valid: true}); err == nil {
		t.Error("dateText(infinity) succeeded, want an error")
	}
	if _, err := dateText(pgtype.Date{Time: time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC), Valid: true}); err == nil {
		t.Error("dateText(year 10000) succeeded, want an error")
	}
}

func TestTimestampText(t *testing.T) {
	east := time.FixedZone("UTC+2", 2*60*60)
	got, err := timestampText(pgtype.Timestamptz{Time: time.Date(2026, 3, 1, 1, 30, 0, 123456000, east), Valid: true})
	if err != nil {
		t.Fatal(err)
	}
	if want := "2026-02-28T23:30:00.123456Z"; got != want {
		t.Errorf("timestampText = %q, want %q", got, want)
	}
	if _, err := timestampText(pgtype.Timestamptz{InfinityModifier: pgtype.NegativeInfinity, Valid: true}); err == nil {
		t.Error("timestampText(-infinity) succeeded, want an error")
	}
}

func TestJSONText(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{`{"a": 1, "b": [1, 2]}`, `{"a":1,"b":[1,2]}`},
		{`["x", "y"]`, `["x","y"]`},
		{`[]`, `[]`},
		{`{"n": 100.10, "s": "two  spaces"}`, `{"n":100.10,"s":"two  spaces"}`},
	} {
		got, err := jsonText(tc.in)
		if err != nil {
			t.Errorf("jsonText(%q): %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("jsonText(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	if _, err := jsonText(`{"a":`); err == nil {
		t.Error("jsonText of a truncated document succeeded, want an error")
	}
}

func TestClassify(t *testing.T) {
	two, ten := int64(2), int64(10)
	for _, tc := range []struct {
		name   string
		pg     pgType
		sqlite string
		want   kind
	}{
		{"money", pgType{dataType: "numeric", scale: &two}, "INTEGER", kindMoney},
		{"a percentage stored as text", pgType{dataType: "numeric", scale: &two}, "TEXT", kindDecimal},
		{"rate", pgType{dataType: "numeric", scale: &ten}, "TEXT", kindDecimal},
		{"uuid", pgType{dataType: "uuid"}, "TEXT", kindUUID},
		{"uuid array", pgType{dataType: "ARRAY", udtName: "_uuid"}, "TEXT", kindJSON},
		{"integer array", pgType{dataType: "ARRAY", udtName: "_int4"}, "TEXT", kindJSON},
		{"jsonb", pgType{dataType: "jsonb"}, "TEXT", kindJSON},
		{"boolean", pgType{dataType: "boolean"}, "INTEGER", kindBool},
		{"bigint", pgType{dataType: "bigint"}, "INTEGER", kindInteger},
		{"double", pgType{dataType: "double precision"}, "REAL", kindReal},
		{"bytea", pgType{dataType: "bytea"}, "BLOB", kindBlob},
		{"date", pgType{dataType: "date"}, "TEXT", kindDate},
		{"timestamptz", pgType{dataType: "timestamp with time zone"}, "TEXT", kindTimestamp},
		{"varchar", pgType{dataType: "character varying"}, "TEXT", kindText},
	} {
		got, err := classify(tc.pg, tc.sqlite)
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s: kind %d, want %d", tc.name, got, tc.want)
		}
	}

	for _, tc := range []struct {
		name   string
		pg     pgType
		sqlite string
	}{
		{"money at scale 10", pgType{dataType: "numeric", scale: &ten}, "INTEGER"},
		{"numeric as REAL", pgType{dataType: "numeric", scale: &two}, "REAL"},
		{"boolean as TEXT", pgType{dataType: "boolean"}, "TEXT"},
		{"date as INTEGER", pgType{dataType: "date"}, "INTEGER"},
		{"interval", pgType{dataType: "interval"}, "TEXT"},
		{"numeric array", pgType{dataType: "ARRAY", udtName: "_numeric"}, "TEXT"},
	} {
		if got, err := classify(tc.pg, tc.sqlite); err == nil {
			t.Errorf("%s: kind %d, want an error", tc.name, got)
		}
	}
}

func TestValueReadsNullAsNull(t *testing.T) {
	for _, k := range []kind{kindText, kindInteger, kindBool, kindReal, kindMoney, kindDecimal, kindUUID, kindDate, kindTimestamp, kindJSON, kindBlob} {
		got, err := k.value(k.scanTarget())
		if err != nil {
			t.Errorf("kind %d: %v", k, err)
		}
		if got != nil {
			t.Errorf("kind %d: NULL read as %#v", k, got)
		}
	}
}

func TestValueConvertsScannedValues(t *testing.T) {
	for _, tc := range []struct {
		name   string
		kind   kind
		target any
		want   any
	}{
		{"true", kindBool, &pgtype.Bool{Bool: true, Valid: true}, int64(1)},
		{"false", kindBool, &pgtype.Bool{Bool: false, Valid: true}, int64(0)},
		{"integer", kindInteger, &pgtype.Int8{Int64: 30, Valid: true}, int64(30)},
		{"real", kindReal, &pgtype.Float8{Float64: 0.5, Valid: true}, 0.5},
		{"money", kindMoney, &pgtype.Text{String: "250.00", Valid: true}, int64(25000)},
		{"rate", kindDecimal, &pgtype.Text{String: "0.0500000000", Valid: true}, "0.05"},
		{"text", kindText, &pgtype.Text{String: "Example Grocer", Valid: true}, "Example Grocer"},
	} {
		got, err := tc.kind.value(tc.target)
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s: %#v, want %#v", tc.name, got, tc.want)
		}
	}

	blob := []byte{1, 2, 3}
	got, err := kindBlob.value(&blob)
	if err != nil {
		t.Fatal(err)
	}
	if b, ok := got.([]byte); !ok || string(b) != string(blob) {
		t.Errorf("blob read as %#v", got)
	}
}

func TestSelectExpr(t *testing.T) {
	two := int64(2)
	for _, tc := range []struct {
		kind kind
		pg   pgType
		want string
	}{
		{kindMoney, pgType{dataType: "numeric", scale: &two}, `"amount"::text`},
		{kindJSON, pgType{dataType: "ARRAY", udtName: "_uuid"}, `array_to_json("amount")::text`},
		{kindJSON, pgType{dataType: "jsonb"}, `"amount"::text`},
		{kindDate, pgType{dataType: "date"}, `"amount"`},
	} {
		if got := selectExpr(tc.kind, tc.pg, `"amount"`); got != tc.want {
			t.Errorf("selectExpr(%d, %s) = %s, want %s", tc.kind, tc.pg.dataType, got, tc.want)
		}
	}
}
