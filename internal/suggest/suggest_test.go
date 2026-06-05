package suggest

import (
	"testing"

	"tdb/internal/config"
)

func TestSuggestSQLKeywordsByCurrentToken(t *testing.T) {
	got := Suggest(Context{Driver: config.DriverMySQL, Input: "sel"})

	assertValues(t, got, []string{"SELECT"})
}

func TestSuggestRedisCommandsByCurrentToken(t *testing.T) {
	got := Suggest(Context{Driver: config.DriverRedis, Input: "hge"})

	assertValues(t, got, []string{"HGET", "HGETALL"})
}

func TestSuggestMongoJSONFragments(t *testing.T) {
	got := Suggest(Context{Driver: config.DriverMongo, Input: "{\"f"})

	assertValues(t, got, []string{"filter"})
}

func TestSuggestPageCommandsWhenNoDriverContext(t *testing.T) {
	got := Suggest(Context{Page: "connections", Input: "op"})

	assertValues(t, got, []string{"open"})
}

func TestSuggestIncludesDatabaseObjects(t *testing.T) {
	got := Suggest(Context{Driver: config.DriverMySQL, Input: "SELECT * FROM u", Objects: []string{"users", "orders"}})

	assertValues(t, got, []string{"users"})
}

func TestSuggestMongoCollectionsAfterDBDot(t *testing.T) {
	got := Suggest(Context{
		Driver:  config.DriverMongo,
		Input:   "db.",
		Objects: []string{"lp_pay_exposure", "users"},
	})

	assertValues(t, got, []string{"lp_pay_exposure", "users"})
}

func TestSuggestMongoMethodsAfterCollectionDot(t *testing.T) {
	got := Suggest(Context{
		Driver:  config.DriverMongo,
		Input:   "db.lp_pay_exposure.",
		Objects: []string{"lp_pay_exposure"},
	})

	assertValues(t, got, []string{"find", "findOne", "countDocuments"})
}

func TestSuggestMongoFieldsAndOperatorsInsideDocument(t *testing.T) {
	fields := []Field{{Name: "room_id", Type: "int"}, {Name: "msg_time", Type: "datetime"}, {Name: "content", Type: "string"}}

	fieldMatches := Suggest(Context{Driver: config.DriverMongo, Input: "db.lp_pay_exposure.find({roo", Fields: fields})
	assertValues(t, fieldMatches, []string{"room_id"})

	queryOps := Suggest(Context{Driver: config.DriverMongo, Input: "db.lp_pay_exposure.find({room_id: {$", Fields: fields})
	assertValues(t, queryOps, []string{"$eq", "$in", "$gt"})

	updateOps := Suggest(Context{Driver: config.DriverMongo, Input: "db.lp_pay_exposure.updateOne({room_id: 1}, {$", Fields: fields})
	assertValues(t, updateOps, []string{"$set", "$unset", "$inc"})
}

func TestSuggestSQLBuiltinFunctions(t *testing.T) {
	got := Suggest(Context{Driver: config.DriverMySQL, Input: "SELECT cou"})
	if !containsValue(got, "COUNT(") {
		t.Fatalf("expected COUNT( function suggestion, got %+v", got)
	}
	got = Suggest(Context{Driver: config.DriverDoris, Input: "SELECT date_for"})
	if !containsValue(got, "DATE_FORMAT(") {
		t.Fatalf("expected DATE_FORMAT( for doris, got %+v", got)
	}
}

func TestSuggestFieldCarriesType(t *testing.T) {
	fields := []Field{{Name: "room_id", Type: "bigint"}, {Name: "content", Type: "text"}}
	got := Suggest(Context{Driver: config.DriverMySQL, Input: "SELECT roo", Fields: fields})
	s, ok := findValue(got, "room_id")
	if !ok {
		t.Fatalf("expected room_id field suggestion, got %+v", got)
	}
	if s.Detail != "bigint" {
		t.Fatalf("field detail = %q, want bigint (type shown after field)", s.Detail)
	}
}

func TestSuggestFieldWithoutTypeFallsBack(t *testing.T) {
	got := Suggest(Context{Driver: config.DriverMongo, Input: "db.c.find({roo", Fields: []Field{{Name: "room_id"}}})
	s, ok := findValue(got, "room_id")
	if !ok || s.Detail != "field" {
		t.Fatalf("expected room_id with fallback detail 'field', got %+v", got)
	}
}

func TestSuggestMySQLVsDorisFunctions(t *testing.T) {
	mysql := Suggest(Context{Driver: config.DriverMySQL, Input: "SELECT appro"})
	if containsValue(mysql, "APPROX_COUNT_DISTINCT(") {
		t.Fatalf("APPROX_COUNT_DISTINCT is Doris-specific, should not appear for MySQL")
	}
	doris := Suggest(Context{Driver: config.DriverDoris, Input: "SELECT appro"})
	if !containsValue(doris, "APPROX_COUNT_DISTINCT(") {
		t.Fatalf("expected APPROX_COUNT_DISTINCT for Doris, got %+v", doris)
	}
	if !containsValue(Suggest(Context{Driver: config.DriverDoris, Input: "SELECT bitmap_un"}), "BITMAP_UNION(") {
		t.Fatalf("expected BITMAP_UNION for Doris")
	}
	// Shared functions appear for both.
	for _, drv := range []config.Driver{config.DriverMySQL, config.DriverDoris} {
		if !containsValue(Suggest(Context{Driver: drv, Input: "SELECT date_ad"}), "DATE_ADD(") {
			t.Fatalf("expected shared DATE_ADD( for %s", drv)
		}
	}
}

func TestSuggestRedisCommandsExpanded(t *testing.T) {
	got := Suggest(Context{Driver: config.DriverRedis, Input: "inc"})
	if !containsValue(got, "INCR") || !containsValue(got, "INCRBY") {
		t.Fatalf("expected INCR/INCRBY, got %+v", got)
	}
	if !containsValue(Suggest(Context{Driver: config.DriverRedis, Input: "zr"}), "ZRANGE") {
		t.Fatalf("expected ZRANGE for prefix zr")
	}
}

func TestSuggestMongoAggregationOperators(t *testing.T) {
	stages := Suggest(Context{Driver: config.DriverMongo, Input: "db.x.aggregate([{ $gr"})
	if !containsValue(stages, "$group") {
		t.Fatalf("expected $group stage inside aggregate, got %+v", stages)
	}
	exprs := Suggest(Context{Driver: config.DriverMongo, Input: "db.x.aggregate([{ $group: { total: { $su"})
	if !containsValue(exprs, "$sum") {
		t.Fatalf("expected $sum expr inside aggregate, got %+v", exprs)
	}
	// A plain find filter must not surface aggregation stages.
	find := Suggest(Context{Driver: config.DriverMongo, Input: "db.x.find({ $"})
	if containsValue(find, "$group") {
		t.Fatalf("$group must not appear in a find filter: %+v", find)
	}
}

func TestSuggestKeywordSubstringMatch(t *testing.T) {
	// Mid-string keyword match: "order" should surface ALONE_ORDER_LOG.
	got := Suggest(Context{Driver: config.DriverMySQL, Input: "SELECT * FROM order", Objects: []string{"ALONE_ORDER_LOG", "users"}})
	if !containsValue(got, "ALONE_ORDER_LOG") {
		t.Fatalf("keyword 'order' should match ALONE_ORDER_LOG, got %+v", got)
	}
	// Prefix matches rank before mid-string matches.
	got = Suggest(Context{Driver: config.DriverMySQL, Input: "SELECT * FROM us", Objects: []string{"campus", "users"}})
	ui, ci := -1, -1
	for i, s := range got {
		if s.Value == "users" {
			ui = i
		}
		if s.Value == "campus" {
			ci = i
		}
	}
	if ui < 0 || ci < 0 || ui > ci {
		t.Fatalf("prefix match 'users' should rank before substring 'campus': users=%d campus=%d", ui, ci)
	}
}

func findValue(suggestions []Suggestion, value string) (Suggestion, bool) {
	for _, s := range suggestions {
		if s.Value == value {
			return s, true
		}
	}
	return Suggestion{}, false
}

func containsValue(suggestions []Suggestion, value string) bool {
	_, ok := findValue(suggestions, value)
	return ok
}

func assertValues(t *testing.T, suggestions []Suggestion, want []string) {
	t.Helper()
	if len(suggestions) < len(want) {
		t.Fatalf("got %d suggestions, want at least %d: %+v", len(suggestions), len(want), suggestions)
	}
	for i, value := range want {
		if suggestions[i].Value != value {
			t.Fatalf("suggestion[%d] = %q, want %q; all=%+v", i, suggestions[i].Value, value, suggestions)
		}
	}
}
