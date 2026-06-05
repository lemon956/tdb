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
	rows, err := a.db.QueryContext(ctx, "SHOW DATABASES")
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

func (a *Adapter) ListObjects(ctx context.Context, scope db.Scope) ([]db.Object, error) {
	if a.db == nil {
		return nil, ErrNoDatabase
	}
	query := "SHOW FULL TABLES"
	if scope.Database != "" {
		query += " FROM " + sqlutil.QuoteIdentifier(scope.Database)
	}
	rows, err := a.db.QueryContext(ctx, query)
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
			objectType = db.ObjectType("view")
		}
		objects = append(objects, db.Object{Name: values[0].String, Type: objectType})
	}
	return objects, rows.Err()
}

func (a *Adapter) Preview(ctx context.Context, target db.Target, query db.Query, page db.Page) (result.Set, error) {
	if query.Text != "" {
		return a.Execute(ctx, db.Command{Text: query.Text, Database: target.Database})
	}
	if a.db == nil {
		return result.Set{}, ErrNoDatabase
	}
	rows, err := a.db.QueryContext(ctx, BuildPreviewSQL(target, page))
	if err != nil {
		return result.Set{}, err
	}
	defer rows.Close()
	return rowsToSet(rows)
}

func (a *Adapter) Metadata(ctx context.Context, target db.Target) (db.ObjectMetadata, error) {
	if a.db == nil {
		return db.ObjectMetadata{}, ErrNoDatabase
	}
	columnsSQL, indexesSQL := BuildMetadataSQL(target)
	fields, err := a.metadataFields(ctx, columnsSQL)
	if err != nil {
		return db.ObjectMetadata{}, err
	}
	indexes, err := a.metadataIndexes(ctx, indexesSQL)
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
	if a.db == nil {
		return result.Set{}, ErrNoDatabase
	}
	statement := strings.TrimSpace(command.Text)
	if command.Database != "" {
		conn, err := a.db.Conn(ctx)
		if err != nil {
			return result.Set{}, err
		}
		defer conn.Close()
		if _, err := conn.ExecContext(ctx, "USE "+sqlutil.QuoteIdentifier(command.Database)); err != nil {
			return result.Set{}, err
		}
		return a.executeOn(ctx, conn, statement)
	}
	return a.executeOn(ctx, a.db, statement)
}

type sqlRunner interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func (a *Adapter) executeOn(ctx context.Context, runner sqlRunner, statement string) (result.Set, error) {
	if isRowStatement(statement) {
		rows, err := runner.QueryContext(ctx, statement)
		if err != nil {
			return result.Set{}, err
		}
		defer rows.Close()
		return rowsToSet(rows)
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

func rowsToSet(rows *sql.Rows) (result.Set, error) {
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

	for rows.Next() {
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
	return result.Set{Table: &table}, nil
}

func (a *Adapter) metadataFields(ctx context.Context, query string) ([]db.MetadataField, error) {
	rows, err := a.db.QueryContext(ctx, query)
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

func (a *Adapter) metadataIndexes(ctx context.Context, query string) ([]db.MetadataIndex, error) {
	rows, err := a.db.QueryContext(ctx, query)
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
	first, _, _ := strings.Cut(strings.ToLower(statement), " ")
	switch first {
	case "select", "show", "describe", "desc", "explain", "with":
		return true
	default:
		return false
	}
}
