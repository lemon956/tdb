package suggest

import (
	"strings"
	"testing"

	"tdb/internal/config"
)

func ctxSQL(input string, want Want, table string) Context {
	return Context{
		Page:    "query",
		Driver:  config.DriverMySQL,
		Input:   input,
		Objects: []string{"users", "orders"},
		Fields:  []Field{{Name: "id", Type: "int"}, {Name: "email", Type: "varchar"}},
		Want:    want,
		Table:   table,
	}
}

func TestWantTablesLeadsWithObjects(t *testing.T) {
	out := Suggest(ctxSQL("select * from us", WantTables, ""))
	if len(out) == 0 {
		t.Fatal("expected suggestions")
	}
	if out[0].Value != "users" {
		t.Fatalf("after FROM the first suggestion should be a table, got %q", out[0].Value)
	}
	for _, s := range out {
		if s.Value == "id" || s.Value == "email" {
			t.Fatalf("table context should not offer bare fields, got %q", s.Value)
		}
	}
}

func TestWantFieldsQualifiesWithTable(t *testing.T) {
	// Typing the column name "ema" should match the qualified "u.email".
	out := Suggest(ctxSQL("select ema", WantFields, "u"))
	if len(out) == 0 {
		t.Fatal("expected suggestions")
	}
	if out[0].Value != "u.email" {
		t.Fatalf("field suggestion should be qualified as u.email, got %q", out[0].Value)
	}
}

func TestWantNoneSuppresses(t *testing.T) {
	if out := Suggest(ctxSQL("select 1 ", WantNone, "")); len(out) != 0 {
		t.Fatalf("WantNone should yield no suggestions, got %d", len(out))
	}
}

func TestUnqualifiedFieldKeepsTablePrefixInValue(t *testing.T) {
	out := Suggest(ctxSQL("select i", WantFields, "users"))
	var got string
	for _, s := range out {
		if strings.HasSuffix(s.Value, ".id") {
			got = s.Value
		}
	}
	if got != "users.id" {
		t.Fatalf("expected users.id in suggestions, got %q", got)
	}
}

func TestObjectDetailIsDriverAware(t *testing.T) {
	cases := []struct {
		driver config.Driver
		want   string
	}{
		{config.DriverMySQL, "table"},
		{config.DriverDoris, "table"},
		{config.DriverRedis, "key"},
	}
	for _, c := range cases {
		out := Suggest(Context{Driver: c.driver, Input: "us", Objects: []string{"users"}})
		var got string
		for _, s := range out {
			if s.Value == "users" {
				got = s.Detail
			}
		}
		if got != c.want {
			t.Fatalf("%s object detail = %q, want %q", c.driver, got, c.want)
		}
	}
}
