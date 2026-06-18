package app

import (
	"strings"
	"testing"

	"tdb/internal/result"
)

func TestWrapMongoObjectIDs(t *testing.T) {
	in := `{
  "_id": "65d73f4a8178d088e95d4e9e",
  "ref_id": "6a26bfa9d8113edb1d85e82a",
  "name": "abc",
  "code": "abcdef"
}`
	out := wrapMongoObjectIDs(in)
	if !strings.Contains(out, `"_id": ObjectId("65d73f4a8178d088e95d4e9e")`) {
		t.Fatalf("_id should be wrapped:\n%s", out)
	}
	if !strings.Contains(out, `"ref_id": ObjectId("6a26bfa9d8113edb1d85e82a")`) {
		t.Fatalf("*_id reference should be wrapped:\n%s", out)
	}
	if strings.Contains(out, `"name": ObjectId`) || strings.Contains(out, `"code": ObjectId`) {
		t.Fatalf("non-id / non-24hex values must not be wrapped:\n%s", out)
	}
}

func TestStringIDNotWrapped(t *testing.T) {
	// A custom (non-24-hex) string _id stays a plain string.
	out := wrapMongoObjectIDs(`{"_id": "user-123"}`)
	if strings.Contains(out, "ObjectId") {
		t.Fatalf("non-ObjectId string id must not be wrapped, got %q", out)
	}
}

func TestDataResultLinesWrapsObjectID(t *testing.T) {
	set := result.Set{Documents: []result.Document{
		{ID: "1", Data: map[string]any{"_id": "65d73f4a8178d088e95d4e9e", "n": 1}},
	}}
	lines := dataResultLines(set)
	// dataResultLines now syntax-highlights JSON, so compare against the
	// ANSI-stripped visible text.
	joined := stripANSI(strings.Join(lines, "\n"))
	if !strings.Contains(joined, `ObjectId("65d73f4a8178d088e95d4e9e")`) {
		t.Fatalf("document _id should render as ObjectId():\n%s", joined)
	}
}
