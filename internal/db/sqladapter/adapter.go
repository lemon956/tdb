package sqladapter

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"

	_ "github.com/go-sql-driver/mysql"

	"tdb/internal/config"
	"tdb/internal/db"
	"tdb/internal/db/sqlutil"
	"tdb/internal/result"
)

var (
	ErrReadOnly   = errors.New("connection is read-only")
	ErrNoDatabase = errors.New("database connection is not initialized")
	ErrMissingKey = errors.New("write operation requires key columns")
)

type Adapter struct {
	profile config.Profile
	db      *sql.DB
}

func NewMySQL(profile config.Profile) (*Adapter, error) {
	conn, err := sql.Open("mysql", sqlutil.BuildMySQLDSN(profile))
	if err != nil {
		return nil, err
	}
	return NewWithDB(profile, conn), nil
}

func NewDoris(profile config.Profile) (*Adapter, error) {
	conn, err := sql.Open("mysql", sqlutil.BuildDorisDSN(profile))
	if err != nil {
		return nil, err
	}
	return NewWithDB(profile, conn), nil
}

func NewWithDB(profile config.Profile, conn *sql.DB) *Adapter {
	return &Adapter{profile: profile, db: conn}
}

func (a *Adapter) Test(ctx context.Context) error {
	if a.db == nil {
		return ErrNoDatabase
	}
	return a.db.PingContext(ctx)
}

func (a *Adapter) ListDatabases(ctx context.Context) ([]string, error) {
	if a.db == nil {
		return nil, ErrNoDatabase
	}
	return listDatabasesOn(ctx, a.db)
}

func listDatabasesOn(ctx context.Context, runner sqlRunner) ([]string, error) {
	rows, err := runner.QueryContext(ctx, "SHOW DATABASES")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	databases := []string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		databases = append(databases, name)
	}
	return databases, rows.Err()
}

// isInternalCatalog reports whether a catalog name refers to Doris's built-in
// catalog (or no catalog at all, for non-Doris drivers).
func isInternalCatalog(catalog string) bool {
	return catalog == "" || strings.EqualFold(catalog, "internal")
}

// ListCatalogs returns the Doris external catalogs visible to the account. Only
// Doris exposes catalogs (SHOW CATALOGS is privilege-filtered by the server, so
// catalogs the account cannot access simply don't appear); every other driver
// returns nil so the UI keeps its flat database list.
func (a *Adapter) ListCatalogs(ctx context.Context) ([]string, error) {
	if a.db == nil {
		return nil, ErrNoDatabase
	}
	if a.profile.Driver != config.DriverDoris {
		return nil, nil
	}
	rows, err := a.db.QueryContext(ctx, "SHOW CATALOGS")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	nameIdx := -1
	for i, name := range columns {
		if strings.Contains(strings.ToLower(name), "catalogname") || strings.Contains(strings.ToLower(name), "catalog_name") {
			nameIdx = i
			break
		}
	}
	if nameIdx < 0 && len(columns) > 1 {
		nameIdx = 1 // SHOW CATALOGS: CatalogId, CatalogName, ...
	}
	if nameIdx < 0 {
		nameIdx = 0
	}
	catalogs := []string{}
	for rows.Next() {
		values := make([]sql.NullString, len(columns))
		pointers := make([]any, len(columns))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err := rows.Scan(pointers...); err != nil {
			return nil, err
		}
		if name := values[nameIdx].String; name != "" {
			catalogs = append(catalogs, name)
		}
	}
	return catalogs, rows.Err()
}

// ListDatabasesInCatalog lists the databases inside a Doris catalog. The
// built-in/internal catalog reuses SHOW DATABASES; an external catalog is
// scoped via SWITCH on a dedicated connection first.
func (a *Adapter) ListDatabasesInCatalog(ctx context.Context, catalog string) ([]string, error) {
	if a.db == nil {
		return nil, ErrNoDatabase
	}
	if isInternalCatalog(catalog) {
		return listDatabasesOn(ctx, a.db)
	}
	conn, err := a.db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if err := useScope(ctx, conn, catalog, ""); err != nil {
		return nil, err
	}
	return listDatabasesOn(ctx, conn)
}

// useScope applies a Doris catalog/database context onto a connection. An
// external catalog needs SWITCH; the internal catalog only needs USE.
func useScope(ctx context.Context, runner sqlRunner, catalog, database string) error {
	if !isInternalCatalog(catalog) {
		if _, err := runner.ExecContext(ctx, "SWITCH "+sqlutil.QuoteIdentifier(catalog)); err != nil {
			return err
		}
	}
	if database != "" {
		if _, err := runner.ExecContext(ctx, "USE "+sqlutil.QuoteIdentifier(database)); err != nil {
			return err
		}
	}
	return nil
}

func (a *Adapter) ListObjects(ctx context.Context, scope db.Scope) ([]db.Object, error) {
	if a.db == nil {
		return nil, ErrNoDatabase
	}
	if !isInternalCatalog(scope.Catalog) {
		conn, err := a.db.Conn(ctx)
		if err != nil {
			return nil, err
		}
		defer conn.Close()
		if err := useScope(ctx, conn, scope.Catalog, ""); err != nil {
			return nil, err
		}
		return listObjectsOn(ctx, conn, scope.Database)
	}
	return listObjectsOn(ctx, a.db, scope.Database)
}

func listObjectsOn(ctx context.Context, runner sqlRunner, database string) ([]db.Object, error) {
	query := "SHOW FULL TABLES"
	if database != "" {
		query += " FROM " + sqlutil.QuoteIdentifier(database)
	}
	rows, err := runner.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// SHOW FULL TABLES returns the table name in the first column and the table
	// type in a "Table_type" column. The column count varies by engine (MySQL
	// returns 2, Doris returns more), so scan dynamically and locate the type
	// column by header instead of assuming a fixed shape.
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	typeIdx := -1
	for i, name := range columns {
		if strings.Contains(strings.ToLower(name), "table_type") {
			typeIdx = i
			break
		}
	}

	objects := []db.Object{}
	for rows.Next() {
		values := make([]sql.NullString, len(columns))
		pointers := make([]any, len(columns))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err := rows.Scan(pointers...); err != nil {
			return nil, err
		}
		objectType := db.ObjectTable
		if typeIdx >= 0 && strings.EqualFold(values[typeIdx].String, "VIEW") {
			objectType = db.ObjectView
		}
		objects = append(objects, db.Object{Name: values[0].String, Type: objectType})
	}
	return objects, rows.Err()
}

func (a *Adapter) Preview(ctx context.Context, target db.Target, query db.Query, page db.Page) (result.Set, error) {
	if query.Text != "" {
		return a.Execute(ctx, db.Command{Text: query.Text, Catalog: target.Catalog, Database: target.Database})
	}
	if a.db == nil {
		return result.Set{}, ErrNoDatabase
	}
	// Fetch one extra row so we can tell the UI whether a next page exists.
	probe := page
	if probe.Limit > 0 {
		probe.Limit++
	}
	return a.execScoped(ctx, target.Catalog, target.Database, func(runner sqlRunner) (result.Set, error) {
		rows, err := runner.QueryContext(ctx, BuildPreviewSQL(target, probe))
		if err != nil {
			return result.Set{}, err
		}
		defer rows.Close()
		set, err := rowsToSet(rows, page.Limit)
		if err != nil {
			return result.Set{}, err
		}
		set.HasMore = set.Truncated // the extra row means another page is available
		set.Truncated = false
		return set, nil
	})
}

func (a *Adapter) Metadata(ctx context.Context, target db.Target) (db.ObjectMetadata, error) {
	if a.db == nil {
		return db.ObjectMetadata{}, ErrNoDatabase
	}
	if !isInternalCatalog(target.Catalog) {
		conn, err := a.db.Conn(ctx)
		if err != nil {
			return db.ObjectMetadata{}, err
		}
		defer conn.Close()
		if err := useScope(ctx, conn, target.Catalog, target.Database); err != nil {
			return db.ObjectMetadata{}, err
		}
		return metadataOn(ctx, conn, target)
	}
	return metadataOn(ctx, a.db, target)
}

func metadataOn(ctx context.Context, runner sqlRunner, target db.Target) (db.ObjectMetadata, error) {
	columnsSQL, indexesSQL := BuildMetadataSQL(target)
	fields, err := metadataFields(ctx, runner, columnsSQL)
	if err != nil {
		return db.ObjectMetadata{}, err
	}
	indexes, err := metadataIndexes(ctx, runner, indexesSQL)
	if err != nil {
		return db.ObjectMetadata{}, err
	}
	return db.ObjectMetadata{Fields: fields, Indexes: indexes}, nil
}

func (a *Adapter) Insert(ctx context.Context, target db.Target, values map[string]any) (result.MutationResult, error) {
	if err := a.ensureWritable(); err != nil {
		return result.MutationResult{}, err
	}
	query, args := BuildInsertSQL(target, values)
	return a.execMutation(ctx, query, args...)
}

func (a *Adapter) Update(ctx context.Context, target db.Target, key db.Key, values map[string]any) (result.MutationResult, error) {
	if err := a.ensureWritable(); err != nil {
		return result.MutationResult{}, err
	}
	query, args, err := BuildUpdateSQL(target, key, values)
	if err != nil {
		return result.MutationResult{}, err
	}
	return a.execMutation(ctx, query, args...)
}

func (a *Adapter) Delete(ctx context.Context, target db.Target, key db.Key) (result.MutationResult, error) {
	if err := a.ensureWritable(); err != nil {
		return result.MutationResult{}, err
	}
	query, args, err := BuildDeleteSQL(target, key)
	if err != nil {
		return result.MutationResult{}, err
	}
	return a.execMutation(ctx, query, args...)
}

func (a *Adapter) Execute(ctx context.Context, command db.Command) (result.Set, error) {
	statement := strings.TrimSpace(command.Text)
	// Enforce read-only: a non-row statement is a write/DDL and is rejected on a
	// read-only connection (this is the editor/command-line execution path).
	if a.profile.ReadOnly && !isRowStatement(statement) {
		return result.Set{}, ErrReadOnly
	}
	if a.db == nil {
		return result.Set{}, ErrNoDatabase
	}
	return a.execScoped(ctx, command.Catalog, command.Database, func(runner sqlRunner) (result.Set, error) {
		return a.executeOn(ctx, runner, statement)
	})
}

type sqlRunner interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

// execScoped runs fn with the given Doris catalog/database context applied. When
// a catalog or database is set the work runs on a dedicated connection (so the
// SWITCH/USE sticks for that statement); otherwise it uses the shared pool.
//
// On Doris, if fn fails because the new pipelineX engine cannot execute the plan
// (legacy external tables — ENGINE=ELASTICSEARCH/ODBC/MYSQL — yield
// "Unsupported exec type in pipelineX: *_SCAN_NODE"), it transparently disables
// the pipelineX engine on a fresh connection and retries once. Normal queries
// keep running on pipelineX; only the ones that hit the limitation pay the
// fallback, and no external table needs to be modified.
func (a *Adapter) execScoped(ctx context.Context, catalog, database string, fn func(sqlRunner) (result.Set, error)) (result.Set, error) {
	scoped := !isInternalCatalog(catalog) || database != ""
	var (
		set result.Set
		err error
	)
	if scoped {
		set, err = a.runScoped(ctx, catalog, database, false, fn)
	} else {
		set, err = fn(a.db)
	}
	if a.profile.Driver == config.DriverDoris && isPipelineXUnsupported(err) {
		return a.runScoped(ctx, catalog, database, true, fn)
	}
	return set, err
}

// runScoped acquires a dedicated connection, optionally disables the pipelineX
// engine, applies the catalog/database scope, and runs fn on that connection.
func (a *Adapter) runScoped(ctx context.Context, catalog, database string, disablePipelineX bool, fn func(sqlRunner) (result.Set, error)) (result.Set, error) {
	conn, err := a.db.Conn(ctx)
	if err != nil {
		return result.Set{}, err
	}
	defer conn.Close()
	if disablePipelineX {
		disablePipelineXEngine(ctx, conn)
	}
	if err := useScope(ctx, conn, catalog, database); err != nil {
		return result.Set{}, err
	}
	return fn(conn)
}

// disablePipelineXEngine turns off the pipelineX execution engine for the
// session. The variable name changed across Doris versions, so try each and
// ignore "unknown variable" errors.
func disablePipelineXEngine(ctx context.Context, runner sqlRunner) {
	for _, name := range []string{
		"enable_pipeline_x_engine",
		"experimental_enable_pipeline_x_engine",
		"enable_pipeline_engine",
	} {
		_, _ = runner.ExecContext(ctx, "SET "+name+" = false")
	}
}

// isPipelineXUnsupported matches the Doris error raised when the pipelineX
// engine has no executor for a plan node (legacy external-table scans).
func isPipelineXUnsupported(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "unsupported exec type in pipelinex") {
		return true
	}
	return strings.Contains(msg, "pipelinex") && strings.Contains(msg, "scan_node")
}

func (a *Adapter) executeOn(ctx context.Context, runner sqlRunner, statement string) (result.Set, error) {
	if isRowStatement(statement) {
		rows, err := runner.QueryContext(ctx, statement)
		if err != nil {
			return result.Set{}, err
		}
		defer rows.Close()
		return rowsToSet(rows, db.MaxResultRows)
	}
	mutation, err := execMutationOn(ctx, runner, statement)
	if err != nil {
		return result.Set{}, err
	}
	return mutationSet(mutation.AffectedRows), nil
}

func (a *Adapter) Close() error {
	if a.db == nil {
		return nil
	}
	return a.db.Close()
}

func (a *Adapter) ensureWritable() error {
	if a.profile.ReadOnly {
		return ErrReadOnly
	}
	if a.db == nil {
		return ErrNoDatabase
	}
	return nil
}

func (a *Adapter) execMutation(ctx context.Context, query string, args ...any) (result.MutationResult, error) {
	if a.db == nil {
		return result.MutationResult{}, ErrNoDatabase
	}
	return execMutationOn(ctx, a.db, query, args...)
}

func execMutationOn(ctx context.Context, runner sqlRunner, query string, args ...any) (result.MutationResult, error) {
	res, err := runner.ExecContext(ctx, query, args...)
	if err != nil {
		return result.MutationResult{}, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return result.MutationResult{}, err
	}
	return result.NewMutationResult(affected), nil
}

func BuildPreviewSQL(target db.Target, page db.Page) string {
	return "SELECT * FROM " + qualifiedName(target) + sqlutil.LimitOffsetClause(page.Limit, page.Offset)
}

func BuildMetadataSQL(target db.Target) (string, string) {
	name := qualifiedName(target)
	return "SHOW FULL COLUMNS FROM " + name, "SHOW INDEX FROM " + name
}

func BuildInsertSQL(target db.Target, values map[string]any) (string, []any) {
	columns := sortedKeys(values)
	quotedColumns := make([]string, 0, len(columns))
	placeholders := make([]string, 0, len(columns))
	args := make([]any, 0, len(columns))
	for _, column := range columns {
		quotedColumns = append(quotedColumns, sqlutil.QuoteIdentifier(column))
		placeholders = append(placeholders, "?")
		args = append(args, values[column])
	}
	query := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)", qualifiedName(target), strings.Join(quotedColumns, ", "), strings.Join(placeholders, ", "))
	return query, args
}

func BuildUpdateSQL(target db.Target, key db.Key, values map[string]any) (string, []any, error) {
	if len(key.Columns) == 0 {
		return "", nil, ErrMissingKey
	}
	setColumns := sortedKeys(values)
	keyColumns := sortedKeys(key.Columns)
	sets := make([]string, 0, len(setColumns))
	args := make([]any, 0, len(setColumns)+len(keyColumns))
	for _, column := range setColumns {
		sets = append(sets, sqlutil.QuoteIdentifier(column)+" = ?")
		args = append(args, values[column])
	}
	wheres := make([]string, 0, len(keyColumns))
	for _, column := range keyColumns {
		wheres = append(wheres, sqlutil.QuoteIdentifier(column)+" = ?")
		args = append(args, key.Columns[column])
	}
	query := fmt.Sprintf("UPDATE %s SET %s WHERE %s", qualifiedName(target), strings.Join(sets, ", "), strings.Join(wheres, " AND "))
	return query, args, nil
}

func BuildDeleteSQL(target db.Target, key db.Key) (string, []any, error) {
	if len(key.Columns) == 0 {
		return "", nil, ErrMissingKey
	}
	keyColumns := sortedKeys(key.Columns)
	wheres := make([]string, 0, len(keyColumns))
	args := make([]any, 0, len(keyColumns))
	for _, column := range keyColumns {
		wheres = append(wheres, sqlutil.QuoteIdentifier(column)+" = ?")
		args = append(args, key.Columns[column])
	}
	query := fmt.Sprintf("DELETE FROM %s WHERE %s", qualifiedName(target), strings.Join(wheres, " AND "))
	return query, args, nil
}

func qualifiedName(target db.Target) string {
	parts := []string{}
	if target.Database != "" {
		parts = append(parts, sqlutil.QuoteIdentifier(target.Database))
	}
	if target.Schema != "" {
		parts = append(parts, sqlutil.QuoteIdentifier(target.Schema))
	}
	if target.Name != "" {
		parts = append(parts, sqlutil.QuoteIdentifier(target.Name))
	}
	return strings.Join(parts, ".")
}

func sortedKeys(values map[string]any) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// rowsToSet materializes rows, reading at most limit rows. If the result has more
// than limit rows, the extra rows are not read and the Set is marked Truncated
// (limit <= 0 means no cap). This bounds memory for arbitrary queries.
func rowsToSet(rows *sql.Rows, limit int) (result.Set, error) {
	columns, err := rows.Columns()
	if err != nil {
		return result.Set{}, err
	}
	columnTypes, _ := rows.ColumnTypes()
	table := result.Table{Columns: make([]result.Column, len(columns))}
	for i, name := range columns {
		columnType := ""
		if i < len(columnTypes) {
			columnType = columnTypes[i].DatabaseTypeName()
		}
		table.Columns[i] = result.Column{Name: name, Type: columnType}
	}

	truncated := false
	for rows.Next() {
		if limit > 0 && len(table.Rows) >= limit {
			truncated = true // there is at least one more row than we kept
			break
		}
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err := rows.Scan(pointers...); err != nil {
			return result.Set{}, err
		}
		table.Rows = append(table.Rows, result.Row{Values: values})
	}
	if err := rows.Err(); err != nil {
		return result.Set{}, err
	}
	return result.Set{Table: &table, Truncated: truncated}, nil
}

func metadataFields(ctx context.Context, runner sqlRunner, query string) ([]db.MetadataField, error) {
	rows, err := runner.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []db.MetadataField{}
	for rows.Next() {
		values, err := scanRowMap(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, db.MetadataField{
			Name:     values["Field"],
			Type:     values["Type"],
			Nullable: strings.EqualFold(values["Null"], "YES"),
			Default:  values["Default"],
		})
	}
	return out, rows.Err()
}

func metadataIndexes(ctx context.Context, runner sqlRunner, query string) ([]db.MetadataIndex, error) {
	rows, err := runner.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	byName := map[string]*db.MetadataIndex{}
	order := []string{}
	for rows.Next() {
		values, err := scanRowMap(rows)
		if err != nil {
			return nil, err
		}
		name := values["Key_name"]
		if name == "" {
			continue
		}
		index := byName[name]
		if index == nil {
			index = &db.MetadataIndex{Name: name, Unique: values["Non_unique"] == "0"}
			byName[name] = index
			order = append(order, name)
		}
		if column := values["Column_name"]; column != "" {
			index.Columns = append(index.Columns, column)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]db.MetadataIndex, 0, len(order))
	for _, name := range order {
		out = append(out, *byName[name])
	}
	return out, nil
}

func scanRowMap(rows *sql.Rows) (map[string]string, error) {
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	values := make([]any, len(columns))
	pointers := make([]any, len(columns))
	for i := range values {
		pointers[i] = &values[i]
	}
	if err := rows.Scan(pointers...); err != nil {
		return nil, err
	}
	out := make(map[string]string, len(columns))
	for i, column := range columns {
		out[column] = sqlValueString(values[i])
	}
	return out, nil
}

func sqlValueString(value any) string {
	if value == nil {
		return ""
	}
	if bytes, ok := value.([]byte); ok {
		return string(bytes)
	}
	return fmt.Sprint(value)
}

func mutationSet(affectedRows int64) result.Set {
	return result.Set{Table: &result.Table{
		Columns: []result.Column{{Name: "affected_rows", Type: "INTEGER"}},
		Rows:    []result.Row{{Values: []any{affectedRows}}},
	}}
}

func isRowStatement(statement string) bool {
	statement = stripLeadingSQLComments(statement)
	fields := strings.Fields(strings.ToLower(statement))
	if len(fields) == 0 {
		return false
	}
	switch fields[0] {
	case "select", "show", "describe", "desc", "explain", "with":
		return true
	default:
		return false
	}
}

// stripLeadingSQLComments removes leading whitespace and `-- line` / `/* block */`
// comments so the first real keyword can be classified (AI-formatted SQL often
// starts with a comment or a newline after the keyword).
func stripLeadingSQLComments(s string) string {
	for {
		s = strings.TrimLeft(s, " \t\r\n")
		switch {
		case strings.HasPrefix(s, "--"):
			if i := strings.IndexByte(s, '\n'); i >= 0 {
				s = s[i+1:]
			} else {
				return ""
			}
		case strings.HasPrefix(s, "/*"):
			if i := strings.Index(s, "*/"); i >= 0 {
				s = s[i+2:]
			} else {
				return ""
			}
		default:
			return s
		}
	}
}
