package validate

import (
	"testing"

	"tdb/internal/config"
)

func TestValidSQLHasNoIssues(t *testing.T) {
	for _, q := range []string{
		"SELECT id, name FROM users WHERE id = 1",
		"select count(*) from orders o join users u on o.uid = u.id",
		"INSERT INTO t (a, b) VALUES (1, 2)",
		"UPDATE t SET a = 1 WHERE id = 2",
		"SELECT 1; SELECT 2;",
	} {
		if issues := Validate(config.DriverMySQL, q); len(issues) != 0 {
			t.Fatalf("valid SQL %q flagged: %+v", q, issues)
		}
	}
}

// Real-world MySQL/Doris queries must never be flagged: the old grammar parser
// false-positived on perfectly valid SQL (qualified columns, LIMIT, long table
// names), so only the never-false-positive delimiter checks remain. Pure-grammar
// mistakes (e.g. "SELECT FROM WHERE") are intentionally no longer caught.
func TestRealWorldSQLNotFlagged(t *testing.T) {
	for _, driver := range []config.Driver{config.DriverMySQL, config.DriverDoris} {
		for _, q := range []string{
			"SELECT t.user_id, t.head_url, t.nick_name FROM ext_ads_recommend_source_recent_online t LIMIT 10",
			"SELECT a.x, a.y FROM a LIMIT 10",
		} {
			if issues := Validate(driver, q); len(issues) != 0 {
				t.Fatalf("%s flagged valid SQL %q: %+v", driver, q, issues)
			}
		}
	}
}

func TestUnbalancedParensFlagged(t *testing.T) {
	issues := Validate(config.DriverMySQL, "SELECT (1 + 2 FROM t")
	if len(issues) == 0 {
		t.Fatal("expected an unbalanced-delimiter issue")
	}
}

func TestUnterminatedStringFlagged(t *testing.T) {
	issues := Validate(config.DriverRedis, "SET k 'unterminated")
	if len(issues) == 0 {
		t.Fatal("expected an unterminated-string issue")
	}
}

func TestRedisUnknownCommand(t *testing.T) {
	if issues := Validate(config.DriverRedis, "GET key"); len(issues) != 0 {
		t.Fatalf("valid redis command flagged: %+v", issues)
	}
	issues := Validate(config.DriverRedis, "NOTACMD key")
	if len(issues) == 0 {
		t.Fatal("expected unknown-command issue")
	}
}

func TestMongoUnknownMethod(t *testing.T) {
	if issues := Validate(config.DriverMongo, "db.users.find({})"); len(issues) != 0 {
		t.Fatalf("valid mongo call flagged: %+v", issues)
	}
	issues := Validate(config.DriverMongo, "db.users.findz({})")
	if len(issues) == 0 {
		t.Fatal("expected unknown-method issue")
	}
}

func TestDorisDDLNotFalseFlagged(t *testing.T) {
	// Doris-specific DDL isn't DML, so the SQL parser is skipped — no false error.
	q := "CREATE TABLE t (k INT) DISTRIBUTED BY HASH(k) BUCKETS 10"
	if issues := Validate(config.DriverDoris, q); len(issues) != 0 {
		t.Fatalf("doris DDL should not be flagged: %+v", issues)
	}
}
