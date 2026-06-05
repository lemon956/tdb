package db

import "testing"

func TestPageSanitizesLimitAndOffset(t *testing.T) {
	page := NewPage(-5, -1)
	if page.Limit != 100 || page.Offset != 0 {
		t.Fatalf("page = %+v, want default limit and zero offset", page)
	}

	large := NewPage(2000, 25)
	if large.Limit != 500 || large.Offset != 25 {
		t.Fatalf("large page = %+v, want capped limit and preserved offset", large)
	}
}

func TestTargetStringIncludesScopeAndObject(t *testing.T) {
	target := Target{Database: "app", Schema: "public", Name: "users", Type: ObjectTable}
	if got := target.String(); got != "app.public.users" {
		t.Fatalf("Target.String() = %q, want app.public.users", got)
	}
}
