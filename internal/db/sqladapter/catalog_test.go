package sqladapter

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"

	"tdb/internal/config"
	"tdb/internal/db"
)

func TestIsRowStatementWhitespaceAndComments(t *testing.T) {
	rows := []string{
		"SELECT\n  user_id\nFROM t",       // keyword followed by newline (AI formatting)
		"  \n  with x as (select 1)…",     // leading whitespace + WITH
		"-- pick recent\nselect * from t", // leading line comment
		"/* note */ SELECT 1",             // leading block comment
	}
	for _, s := range rows {
		if !isRowStatement(s) {
			t.Fatalf("should be a read statement: %q", s)
		}
	}
	for _, s := range []string{"INSERT INTO t VALUES (1)", "update t set a=1", "delete from t", "  "} {
		if isRowStatement(s) {
			t.Fatalf("should NOT be a read statement: %q", s)
		}
	}
}

// A multi-line SELECT (with an inline comment) must run on a read-only connection
// — the byte-naive classifier used to reject it.
func TestReadOnlyAllowsFormattedSelect(t *testing.T) {
	adapter := NewWithDB(config.Profile{Driver: config.DriverMySQL, ReadOnly: true}, newFakeDB(t).db)
	_, err := adapter.Execute(context.Background(), db.Command{Text: "SELECT\n  a,\n  b\nFROM t -- note\nLIMIT 10"})
	if errors.Is(err, ErrReadOnly) {
		t.Fatalf("a formatted SELECT must not be rejected as read-only: %v", err)
	}
}

func TestIsPipelineXUnsupported(t *testing.T) {
	real := errors.New("Error 1105 (HY000): errCode = 2, detailMessage = (10.40.2.12)[INTERNAL_ERROR]Unsupported exec type in pipelineX: ODBC_SCAN_NODE")
	if !isPipelineXUnsupported(real) {
		t.Fatal("the real Doris pipelineX error should be detected")
	}
	if !isPipelineXUnsupported(errors.New("unsupported in pipelineX, ES_SCAN_NODE present")) {
		t.Fatal("the SCAN_NODE fallback should match")
	}
	if isPipelineXUnsupported(errors.New("syntax error near 'FROM'")) {
		t.Fatal("an unrelated error must not match")
	}
	if isPipelineXUnsupported(nil) {
		t.Fatal("nil must not match")
	}
}

func TestUseScopeEmitsSwitchThenUse(t *testing.T) {
	// External catalog: SWITCH then USE.
	r := &recordingRunner{}
	if err := useScope(context.Background(), r, "jdbc_mysql", "dw"); err != nil {
		t.Fatal(err)
	}
	if len(r.execs) != 2 || !strings.HasPrefix(r.execs[0], "SWITCH ") || !strings.HasPrefix(r.execs[1], "USE ") {
		t.Fatalf("external catalog should SWITCH then USE, got %v", r.execs)
	}
	if !strings.Contains(r.execs[0], "`jdbc_mysql`") {
		t.Fatalf("catalog should be quoted, got %q", r.execs[0])
	}

	// Internal catalog: only USE (no SWITCH).
	r = &recordingRunner{}
	if err := useScope(context.Background(), r, "internal", "dw"); err != nil {
		t.Fatal(err)
	}
	if len(r.execs) != 1 || !strings.HasPrefix(r.execs[0], "USE ") {
		t.Fatalf("internal catalog should only USE, got %v", r.execs)
	}
}

func TestDisablePipelineXEngineTriesVariants(t *testing.T) {
	r := &recordingRunner{}
	disablePipelineXEngine(context.Background(), r)
	if len(r.execs) != 3 {
		t.Fatalf("expected 3 SET attempts, got %v", r.execs)
	}
	if !strings.Contains(r.execs[0], "enable_pipeline_x_engine = false") {
		t.Fatalf("first SET should disable pipelineX, got %q", r.execs[0])
	}
}

// End-to-end: a Doris SELECT that fails with the pipelineX error is retried on a
// connection that disabled the engine, and that retry succeeds. MySQL never
// retries.
func TestExecutePipelineXFallback(t *testing.T) {
	for _, tc := range []struct {
		driver    config.Driver
		wantRetry bool
	}{
		{config.DriverDoris, true},
		{config.DriverMySQL, false},
	} {
		fake := newFakeDB(t)
		adapter := NewWithDB(config.Profile{Driver: tc.driver}, fake.db)

		_, err := adapter.Execute(context.Background(), db.Command{Text: "SELECT * FROM ext_t", Database: "dw"})
		if tc.wantRetry {
			if err != nil {
				t.Fatalf("doris: fallback should make the query succeed, got %v", err)
			}
			if !fake.sawStatement("SET enable_pipeline_x_engine = false") {
				t.Fatalf("doris: a pipelineX-disabling SET should have been issued, saw %v", fake.statements())
			}
		} else {
			if err == nil {
				t.Fatal("mysql: the pipelineX error must surface (no fallback)")
			}
			if fake.sawStatement("SET enable_pipeline_x_engine = false") {
				t.Fatal("mysql: must not attempt the pipelineX fallback")
			}
		}
	}
}

// --- test doubles ---------------------------------------------------------

type fakeResult struct{}

func (fakeResult) LastInsertId() (int64, error) { return 0, nil }
func (fakeResult) RowsAffected() (int64, error) { return 0, nil }

// recordingRunner records ExecContext statements; it satisfies sqlRunner for the
// SWITCH/USE/SET helpers (which never QueryContext).
type recordingRunner struct {
	execs   []string
	queries []string
}

func (r *recordingRunner) ExecContext(_ context.Context, query string, _ ...any) (sql.Result, error) {
	r.execs = append(r.execs, query)
	return fakeResult{}, nil
}

func (r *recordingRunner) QueryContext(_ context.Context, query string, _ ...any) (*sql.Rows, error) {
	r.queries = append(r.queries, query)
	return nil, errors.New("recordingRunner: QueryContext not supported")
}

// fakeDB is a minimal database/sql driver that records statements and emulates
// Doris's pipelineX limitation: a SELECT fails until the connection disables the
// pipelineX engine via SET.
type fakeDB struct {
	db    *sql.DB
	mu    *sync.Mutex
	stmts *[]string
}

var fakeDriverOnce sync.Once

func newFakeDB(t *testing.T) *fakeDB {
	t.Helper()
	fakeDriverOnce.Do(func() { sql.Register("tdb-fake", &fakeDriver{}) })
	shared := &fakeShared{}
	connector := &fakeConnector{shared: shared}
	return &fakeDB{db: sql.OpenDB(connector), mu: &shared.mu, stmts: &shared.stmts}
}

func (f *fakeDB) statements() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), (*f.stmts)...)
}

func (f *fakeDB) sawStatement(s string) bool {
	for _, got := range f.statements() {
		if strings.Contains(got, s) {
			return true
		}
	}
	return false
}

type fakeShared struct {
	mu    sync.Mutex
	stmts []string
}

type fakeDriver struct{}

func (*fakeDriver) Open(string) (driver.Conn, error) { return nil, errors.New("use OpenDB") }

type fakeConnector struct{ shared *fakeShared }

func (c *fakeConnector) Connect(context.Context) (driver.Conn, error) {
	return &fakeConn{shared: c.shared}, nil
}
func (c *fakeConnector) Driver() driver.Driver { return &fakeDriver{} }

type fakeConn struct {
	shared            *fakeShared
	pipelineXDisabled bool
}

func (c *fakeConn) record(q string) {
	c.shared.mu.Lock()
	c.shared.stmts = append(c.shared.stmts, q)
	c.shared.mu.Unlock()
}

func (c *fakeConn) ExecContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Result, error) {
	c.record(query)
	if strings.Contains(query, "enable_pipeline_x_engine = false") {
		c.pipelineXDisabled = true
	}
	return fakeResult{}, nil
}

func (c *fakeConn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	c.record(query)
	if strings.HasPrefix(strings.ToUpper(strings.TrimSpace(query)), "SELECT") && !c.pipelineXDisabled {
		return nil, errors.New("[INTERNAL_ERROR]Unsupported exec type in pipelineX: ODBC_SCAN_NODE")
	}
	return &fakeRows{}, nil
}

func (c *fakeConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("not implemented") }
func (c *fakeConn) Close() error                        { return nil }
func (c *fakeConn) Begin() (driver.Tx, error)           { return nil, errors.New("not implemented") }

type fakeRows struct{ done bool }

func (r *fakeRows) Columns() []string { return []string{"c"} }
func (r *fakeRows) Close() error      { return nil }
func (r *fakeRows) Next(dest []driver.Value) error {
	if r.done {
		return io.EOF
	}
	r.done = true
	dest[0] = "v"
	return nil
}
