package db

import (
	"encoding/json"
	"testing"
)

func TestObjectSubTypeRoundTrips(t *testing.T) {
	in := Object{Name: "user:1", Type: ObjectKey, SubType: "hash"}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out Object
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out != in {
		t.Fatalf("round-trip = %+v, want %+v", out, in)
	}
	// SubType is omitted when empty.
	plain, _ := json.Marshal(Object{Name: "users", Type: ObjectTable})
	if string(plain) == "" || containsSub(string(plain)) {
		t.Fatalf("empty SubType should be omitted, got %s", plain)
	}
}

func containsSub(s string) bool {
	for i := 0; i+9 <= len(s); i++ {
		if s[i:i+9] == "sub_type\"" {
			return true
		}
	}
	return false
}

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
