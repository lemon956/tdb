package sqladapter

import (
	"context"
	"errors"
	"testing"

	"tdb/internal/config"
	"tdb/internal/db"
)

func TestBuildPreviewSQLQuotesTargetAndPaginates(t *testing.T) {
	target := db.Target{Database: "app", Name: "users", Type: db.ObjectTable}

	got := BuildPreviewSQL(target, db.NewPage(20, 40))
	want := "SELECT * FROM `app`.`users` LIMIT 20 OFFSET 40"
	if got != want {
		t.Fatalf("BuildPreviewSQL() = %q, want %q", got, want)
	}
}

func TestBuildMetadataSQLQuotesTarget(t *testing.T) {
	target := db.Target{Database: "app", Name: "users", Type: db.ObjectTable}

	columns, indexes := BuildMetadataSQL(target)

	if columns != "SHOW FULL COLUMNS FROM `app`.`users`" {
		t.Fatalf("columns SQL = %q", columns)
	}
	if indexes != "SHOW INDEX FROM `app`.`users`" {
		t.Fatalf("indexes SQL = %q", indexes)
	}
}

func TestBuildInsertSQLSortsColumnsAndReturnsArgs(t *testing.T) {
	target := db.Target{Database: "app", Name: "users", Type: db.ObjectTable}

	query, args := BuildInsertSQL(target, map[string]any{"name": "Ada", "id": int64(7)})

	wantQuery := "INSERT INTO `app`.`users` (`id`, `name`) VALUES (?, ?)"
	if query != wantQuery {
		t.Fatalf("query = %q, want %q", query, wantQuery)
	}
	if len(args) != 2 || args[0] != int64(7) || args[1] != "Ada" {
		t.Fatalf("args = %#v, want [7 Ada]", args)
	}
}

func TestBuildUpdateAndDeleteSQLRequireKeyColumns(t *testing.T) {
	target := db.Target{Database: "app", Name: "users", Type: db.ObjectTable}
	key := db.Key{Columns: map[string]any{"id": int64(7)}}

	updateQuery, updateArgs, err := BuildUpdateSQL(target, key, map[string]any{"name": "Grace"})
	if err != nil {
		t.Fatalf("BuildUpdateSQL returned error: %v", err)
	}
	if updateQuery != "UPDATE `app`.`users` SET `name` = ? WHERE `id` = ?" {
		t.Fatalf("update query = %q", updateQuery)
	}
	if len(updateArgs) != 2 || updateArgs[0] != "Grace" || updateArgs[1] != int64(7) {
		t.Fatalf("update args = %#v", updateArgs)
	}

	deleteQuery, deleteArgs, err := BuildDeleteSQL(target, key)
	if err != nil {
		t.Fatalf("BuildDeleteSQL returned error: %v", err)
	}
	if deleteQuery != "DELETE FROM `app`.`users` WHERE `id` = ?" {
		t.Fatalf("delete query = %q", deleteQuery)
	}
	if len(deleteArgs) != 1 || deleteArgs[0] != int64(7) {
		t.Fatalf("delete args = %#v", deleteArgs)
	}
}

func TestAdapterRejectsWritesForReadOnlyProfile(t *testing.T) {
	adapter := NewWithDB(config.Profile{ReadOnly: true}, nil)

	_, err := adapter.Insert(context.Background(), db.Target{Name: "users"}, map[string]any{"id": 1})
	if !errors.Is(err, ErrReadOnly) {
		t.Fatalf("Insert error = %v, want ErrReadOnly", err)
	}
}

func TestExecuteEnforcesReadOnlyForWrites(t *testing.T) {
	a := NewWithDB(config.Profile{ReadOnly: true}, nil)
	if _, err := a.Execute(context.Background(), db.Command{Text: "DELETE FROM t"}); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("DELETE on read-only conn = %v, want ErrReadOnly", err)
	}
	if _, err := a.Execute(context.Background(), db.Command{Text: "DROP TABLE t"}); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("DROP on read-only conn = %v, want ErrReadOnly", err)
	}
	// A read query passes the read-only gate (then fails on the nil connection).
	if _, err := a.Execute(context.Background(), db.Command{Text: "SELECT 1"}); errors.Is(err, ErrReadOnly) {
		t.Fatalf("SELECT on read-only conn must not be rejected as read-only")
	}
}
