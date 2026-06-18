package mongoadapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"tdb/internal/config"
	"tdb/internal/db"
	"tdb/internal/result"
)

var (
	ErrReadOnly   = errors.New("connection is read-only")
	ErrNoClient   = errors.New("mongo client is not initialized")
	ErrMissingID  = errors.New("mongo write operation requires _id")
	ErrBadCommand = errors.New("mongo execute requires database and collection")
	errNotMongosh = errors.New("not a mongosh command")
)

type Adapter struct {
	profile config.Profile
	client  *mongo.Client
}

func New(profile config.Profile) (*Adapter, error) {
	client, err := mongo.Connect(options.Client().ApplyURI(BuildMongoURI(profile)))
	if err != nil {
		return nil, err
	}
	return NewWithClient(profile, client), nil
}

func NewWithClient(profile config.Profile, client *mongo.Client) *Adapter {
	return &Adapter{profile: profile, client: client}
}

func BuildMongoURI(profile config.Profile) string {
	if strings.HasPrefix(profile.URIParams, "mongodb://") || strings.HasPrefix(profile.URIParams, "mongodb+srv://") {
		return profile.URIParams
	}
	host := profile.Host
	if host == "" {
		host = "127.0.0.1"
	}
	port := profile.Port
	if port == 0 {
		port = 27017
	}
	u := url.URL{
		Scheme: "mongodb",
		Host:   host + ":" + strconv.Itoa(port),
		Path:   "/" + strings.TrimPrefix(profile.Database, "/"),
	}
	if profile.User != "" {
		u.User = url.UserPassword(profile.User, profile.Password)
	}
	values, _ := url.ParseQuery(profile.URIParams)
	if profile.AuthDB != "" && values.Get("authSource") == "" {
		values.Set("authSource", profile.AuthDB)
	}
	u.RawQuery = values.Encode()
	return u.String()
}

func (a *Adapter) Test(ctx context.Context) error {
	if a.client == nil {
		return ErrNoClient
	}
	return a.client.Ping(ctx, nil)
}

func (a *Adapter) ListDatabases(ctx context.Context) ([]string, error) {
	if a.client == nil {
		return nil, ErrNoClient
	}
	return a.client.ListDatabaseNames(ctx, bson.D{})
}

func (a *Adapter) ListObjects(ctx context.Context, scope db.Scope) ([]db.Object, error) {
	if a.client == nil {
		return nil, ErrNoClient
	}
	names, err := a.client.Database(scope.Database).ListCollectionNames(ctx, bson.D{})
	if err != nil {
		return nil, err
	}
	objects := make([]db.Object, 0, len(names))
	for _, name := range names {
		objects = append(objects, db.Object{Name: name, Type: db.ObjectCollection})
	}
	return objects, nil
}

func (a *Adapter) Preview(ctx context.Context, target db.Target, query db.Query, page db.Page) (result.Set, error) {
	if a.client == nil {
		return result.Set{}, ErrNoClient
	}
	filter, err := BuildFilter(query)
	if err != nil {
		return result.Set{}, err
	}
	// Fetch one extra doc so we can tell the UI whether a next page exists.
	findLimit := int64(page.Limit)
	if page.Limit > 0 {
		findLimit = int64(page.Limit + 1)
	}
	cursor, err := a.client.Database(target.Database).Collection(target.Name).Find(
		ctx,
		filter,
		options.Find().SetLimit(findLimit).SetSkip(int64(page.Offset)),
	)
	if err != nil {
		return result.Set{}, err
	}
	defer cursor.Close(ctx)

	set, err := cursorToSet(ctx, cursor, page.Limit)
	if err != nil {
		return result.Set{}, err
	}
	set.HasMore = set.Truncated // the extra doc means another page is available
	set.Truncated = false
	return set, nil
}

// cursorToSet drains a cursor into a document Set, reading at most limit docs
// (limit <= 0 means up to db.MaxResultRows) and marking Truncated when more exist.
func cursorToSet(ctx context.Context, cursor *mongo.Cursor, limit int) (result.Set, error) {
	if limit <= 0 {
		limit = db.MaxResultRows
	}
	docs := []bson.M{}
	truncated := false
	for cursor.Next(ctx) {
		if len(docs) >= limit {
			truncated = true
			break
		}
		var doc bson.M
		if err := cursor.Decode(&doc); err != nil {
			return result.Set{}, err
		}
		docs = append(docs, doc)
	}
	if err := cursor.Err(); err != nil {
		return result.Set{}, err
	}
	set := docsToSet(docs)
	set.Truncated = truncated
	return set, nil
}

func (a *Adapter) Metadata(ctx context.Context, target db.Target) (db.ObjectMetadata, error) {
	if a.client == nil {
		return db.ObjectMetadata{}, ErrNoClient
	}
	cursor, err := a.client.Database(target.Database).Collection(target.Name).Indexes().List(ctx)
	if err != nil {
		return db.ObjectMetadata{}, err
	}
	defer cursor.Close(ctx)

	var docs []bson.M
	if err := cursor.All(ctx, &docs); err != nil {
		return db.ObjectMetadata{}, err
	}
	metadata := IndexDocumentsToMetadata(docs)
	// MongoDB has no fixed schema, so derive field names by sampling documents.
	// Best-effort: a sampling error just leaves Fields empty (indexes still return).
	metadata.Fields = a.sampleFields(ctx, target.Database, target.Name)
	if metadata.Attributes == nil {
		metadata.Attributes = map[string]string{}
	}
	metadata.Attributes["database"] = target.Database
	metadata.Attributes["collection"] = target.Name
	return metadata, nil
}

// fieldSampleSize is how many documents Metadata samples to infer the field set.
// fieldMaxCount/fieldMaxDepth bound the work for collections with sprawling or
// deeply-nested documents.
const (
	fieldSampleSize = 50
	fieldMaxCount   = 200
	fieldMaxDepth   = 3
)

// sampleFields infers a collection's field names (including nested fields as
// dotted paths, e.g. "addr.city") by scanning a sample of documents and taking
// the union of their keys. Returns nil on any error — field suggestions are
// best-effort and must never break the metadata probe.
func (a *Adapter) sampleFields(ctx context.Context, database, collection string) []db.MetadataField {
	cursor, err := a.client.Database(database).Collection(collection).Find(
		ctx, bson.M{}, options.Find().SetLimit(fieldSampleSize),
	)
	if err != nil {
		return nil
	}
	defer cursor.Close(ctx)

	seen := map[string]struct{}{}
	for cursor.Next(ctx) {
		var doc bson.M
		if err := cursor.Decode(&doc); err != nil {
			return nil
		}
		collectFieldPaths(doc, "", 0, seen)
		if len(seen) >= fieldMaxCount {
			break
		}
	}
	if err := cursor.Err(); err != nil {
		return nil
	}
	paths := make([]string, 0, len(seen))
	for path := range seen {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	fields := make([]db.MetadataField, 0, len(paths))
	for _, path := range paths {
		fields = append(fields, db.MetadataField{Name: path})
	}
	return fields
}

// collectFieldPaths records each field path of doc into seen, recursing into
// nested documents (bson.M/bson.D) up to fieldMaxDepth and joining keys with
// ".". Arrays are not expanded. prefix is the dotted path of doc's parent.
func collectFieldPaths(doc bson.M, prefix string, depth int, seen map[string]struct{}) {
	for key, value := range doc {
		if len(seen) >= fieldMaxCount {
			return
		}
		path := key
		if prefix != "" {
			path = prefix + "." + key
		}
		seen[path] = struct{}{}
		if depth+1 >= fieldMaxDepth {
			continue
		}
		switch nested := value.(type) {
		case bson.M:
			collectFieldPaths(nested, path, depth+1, seen)
		case bson.D:
			m := make(bson.M, len(nested))
			for _, e := range nested {
				m[e.Key] = e.Value
			}
			collectFieldPaths(m, path, depth+1, seen)
		}
	}
}

func (a *Adapter) Insert(ctx context.Context, target db.Target, values map[string]any) (result.MutationResult, error) {
	if err := a.ensureWritable(); err != nil {
		return result.MutationResult{}, err
	}
	_, err := a.client.Database(target.Database).Collection(target.Name).InsertOne(ctx, values)
	if err != nil {
		return result.MutationResult{}, err
	}
	return result.NewMutationResult(1), nil
}

func (a *Adapter) Update(ctx context.Context, target db.Target, key db.Key, values map[string]any) (result.MutationResult, error) {
	if err := a.ensureWritable(); err != nil {
		return result.MutationResult{}, err
	}
	filter, err := IDFilter(key)
	if err != nil {
		return result.MutationResult{}, err
	}
	res, err := a.client.Database(target.Database).Collection(target.Name).UpdateOne(ctx, filter, bson.M{"$set": values})
	if err != nil {
		return result.MutationResult{}, err
	}
	return result.NewMutationResult(res.ModifiedCount), nil
}

func (a *Adapter) Delete(ctx context.Context, target db.Target, key db.Key) (result.MutationResult, error) {
	if err := a.ensureWritable(); err != nil {
		return result.MutationResult{}, err
	}
	filter, err := IDFilter(key)
	if err != nil {
		return result.MutationResult{}, err
	}
	res, err := a.client.Database(target.Database).Collection(target.Name).DeleteOne(ctx, filter)
	if err != nil {
		return result.MutationResult{}, err
	}
	return result.NewMutationResult(res.DeletedCount), nil
}

func (a *Adapter) Execute(ctx context.Context, command db.Command) (result.Set, error) {
	if shell, err := ParseMongoshCommand(command.Text); err == nil {
		return a.executeMongosh(ctx, command, shell)
	}
	var req struct {
		Database   string         `json:"database"`
		Collection string         `json:"collection"`
		Filter     map[string]any `json:"filter"`
		Limit      int            `json:"limit"`
		Skip       int            `json:"skip"`
	}
	if err := json.Unmarshal([]byte(command.Text), &req); err != nil {
		return result.Set{}, err
	}
	if req.Database == "" {
		req.Database = command.Database
	}
	if req.Database == "" || req.Collection == "" {
		return result.Set{}, ErrBadCommand
	}
	return a.Preview(ctx, db.Target{Database: req.Database, Name: req.Collection, Type: db.ObjectCollection}, db.Query{Filter: req.Filter}, db.NewPage(req.Limit, req.Skip))
}

type MongoshCommand struct {
	Collection string
	Method     string
	Filter     map[string]any
	Update     map[string]any
	Document   map[string]any
	Documents  []any
	Pipeline   []any
	Limit      int
}

func ParseMongoshCommand(text string) (MongoshCommand, error) {
	statement := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(text), ";"))
	if !strings.HasPrefix(statement, "db.") {
		return MongoshCommand{}, errNotMongosh
	}
	rest := strings.TrimPrefix(statement, "db.")
	dot := strings.Index(rest, ".")
	if dot <= 0 {
		return MongoshCommand{}, errNotMongosh
	}
	collection := rest[:dot]
	rest = rest[dot+1:]
	open := strings.Index(rest, "(")
	close := strings.LastIndex(rest, ")")
	if open <= 0 || close < open {
		return MongoshCommand{}, errNotMongosh
	}
	method := rest[:open]
	args := splitMongoshArgs(rest[open+1 : close])
	command := MongoshCommand{Collection: collection, Method: method}
	switch method {
	case "find":
		command.Filter = parseMongoshMapArg(args, 0)
	case "findOne":
		command.Filter = parseMongoshMapArg(args, 0)
		command.Limit = 1
	case "countDocuments":
		command.Filter = parseMongoshMapArg(args, 0)
	case "aggregate":
		command.Pipeline = parseMongoshArrayArg(args, 0)
	case "insertOne":
		command.Document = parseMongoshMapArg(args, 0)
	case "insertMany":
		command.Documents = parseMongoshArrayArg(args, 0)
	case "updateOne", "updateMany":
		command.Filter = parseMongoshMapArg(args, 0)
		command.Update = parseMongoshMapArg(args, 1)
	case "deleteOne", "deleteMany":
		command.Filter = parseMongoshMapArg(args, 0)
	default:
		return MongoshCommand{}, errNotMongosh
	}
	return command, nil
}

func (a *Adapter) executeMongosh(ctx context.Context, command db.Command, shell MongoshCommand) (result.Set, error) {
	if isMongoshMutation(shell.Method) {
		if err := a.ensureWritable(); err != nil {
			return result.Set{}, err
		}
	}
	if a.client == nil {
		return result.Set{}, ErrNoClient
	}
	database := command.Database
	if database == "" {
		database = a.profile.Database
	}
	if database == "" || shell.Collection == "" {
		return result.Set{}, ErrBadCommand
	}
	target := db.Target{Database: database, Name: shell.Collection, Type: db.ObjectCollection}
	collection := a.client.Database(database).Collection(shell.Collection)
	switch shell.Method {
	case "find", "findOne":
		return a.Preview(ctx, target, db.Query{Filter: shell.Filter}, db.NewPage(shell.Limit, 0))
	case "countDocuments":
		count, err := collection.CountDocuments(ctx, bson.M(shell.Filter))
		if err != nil {
			return result.Set{}, err
		}
		return result.Set{Table: &result.Table{
			Columns: []result.Column{{Name: "count"}},
			Rows:    []result.Row{{Values: []any{count}}},
		}}, nil
	case "aggregate":
		cursor, err := collection.Aggregate(ctx, shell.Pipeline)
		if err != nil {
			return result.Set{}, err
		}
		defer cursor.Close(ctx)
		return cursorToSet(ctx, cursor, db.MaxResultRows)
	default:
		return a.executeMongoshMutation(ctx, collection, shell)
	}
}

func (a *Adapter) executeMongoshMutation(ctx context.Context, collection *mongo.Collection, shell MongoshCommand) (result.Set, error) {
	// Writes (insert/update/delete/drop) are blocked on read-only connections.
	if err := a.ensureWritable(); err != nil {
		return result.Set{}, err
	}
	var affected int64
	switch shell.Method {
	case "insertOne":
		_, err := collection.InsertOne(ctx, shell.Document)
		if err != nil {
			return result.Set{}, err
		}
		affected = 1
	case "insertMany":
		res, err := collection.InsertMany(ctx, shell.Documents)
		if err != nil {
			return result.Set{}, err
		}
		affected = int64(len(res.InsertedIDs))
	case "updateOne":
		res, err := collection.UpdateOne(ctx, bson.M(shell.Filter), bson.M(shell.Update))
		if err != nil {
			return result.Set{}, err
		}
		affected = res.ModifiedCount
	case "updateMany":
		res, err := collection.UpdateMany(ctx, bson.M(shell.Filter), bson.M(shell.Update))
		if err != nil {
			return result.Set{}, err
		}
		affected = res.ModifiedCount
	case "deleteOne":
		res, err := collection.DeleteOne(ctx, bson.M(shell.Filter))
		if err != nil {
			return result.Set{}, err
		}
		affected = res.DeletedCount
	case "deleteMany":
		res, err := collection.DeleteMany(ctx, bson.M(shell.Filter))
		if err != nil {
			return result.Set{}, err
		}
		affected = res.DeletedCount
	default:
		return result.Set{}, ErrBadCommand
	}
	return mongoMutationSet(affected), nil
}

func isMongoshMutation(method string) bool {
	switch method {
	case "insertOne", "insertMany", "updateOne", "updateMany", "deleteOne", "deleteMany":
		return true
	default:
		return false
	}
}

func mongoMutationSet(affectedRows int64) result.Set {
	return result.Set{Table: &result.Table{
		Columns: []result.Column{{Name: "affected_rows", Type: "INTEGER"}},
		Rows:    []result.Row{{Values: []any{affectedRows}}},
	}}
}

func splitMongoshArgs(text string) []string {
	args := []string{}
	start := 0
	depth := 0
	quote := rune(0)
	for idx, r := range text {
		if quote != 0 {
			if r == quote {
				quote = 0
			}
			continue
		}
		switch r {
		case '\'', '"':
			quote = r
		case '{', '[':
			depth++
		case '}', ']':
			if depth > 0 {
				depth--
			}
		case ',':
			if depth == 0 {
				args = append(args, strings.TrimSpace(text[start:idx]))
				start = idx + 1
			}
		}
	}
	last := strings.TrimSpace(text[start:])
	if last != "" {
		args = append(args, last)
	}
	return args
}

func parseMongoshMapArg(args []string, index int) map[string]any {
	if index >= len(args) || strings.TrimSpace(args[index]) == "" {
		return map[string]any{}
	}
	value, err := parseMongoshValue(args[index])
	if err != nil {
		return map[string]any{}
	}
	if out, ok := value.(map[string]any); ok {
		return out
	}
	return map[string]any{}
}

func parseMongoshArrayArg(args []string, index int) []any {
	if index >= len(args) || strings.TrimSpace(args[index]) == "" {
		return nil
	}
	value, err := parseMongoshValue(args[index])
	if err != nil {
		return nil
	}
	if out, ok := value.([]any); ok {
		return out
	}
	return nil
}

var (
	mongoshKeyPattern       = regexp.MustCompile(`([\{,]\s*)([A-Za-z_$][A-Za-z0-9_$]*)(\s*:)`)
	mongoshObjectIDPattern  = regexp.MustCompile(`[Oo]bject[Ii][dD]\(\s*"([^"]*)"\s*\)`)
	mongoshISODatePattern   = regexp.MustCompile(`ISODate\(\s*"([^"]*)"\s*\)`)
	mongoshNumberLongP      = regexp.MustCompile(`NumberLong\(\s*"?([0-9-]+)"?\s*\)`)
	mongoshNumberDecimalP   = regexp.MustCompile(`NumberDecimal\(\s*"([^"]*)"\s*\)`)
	mongoshNumberIntPattern = regexp.MustCompile(`NumberInt\(\s*"?([0-9-]+)"?\s*\)`)
)

// normalizeMongoshExtended turns mongosh shell syntax into MongoDB relaxed
// Extended JSON: unquoted keys are quoted, single quotes become double quotes, and
// shell constructors (ObjectId/ISODate/NumberLong/...) become their $-typed forms
// so bson.UnmarshalExtJSON can decode them into real BSON values.
func normalizeMongoshExtended(text string) string {
	s := mongoshKeyPattern.ReplaceAllString(strings.TrimSpace(text), `${1}"${2}"${3}`)
	s = strings.ReplaceAll(s, "'", `"`)
	// Note: "$$" emits a literal "$" in regexp replacements ("$oid" would be read as
	// a capture-group reference).
	s = mongoshObjectIDPattern.ReplaceAllString(s, `{"$$oid":"$1"}`)
	s = mongoshISODatePattern.ReplaceAllString(s, `{"$$date":"$1"}`)
	s = mongoshNumberLongP.ReplaceAllString(s, `{"$$numberLong":"$1"}`)
	s = mongoshNumberDecimalP.ReplaceAllString(s, `{"$$numberDecimal":"$1"}`)
	s = mongoshNumberIntPattern.ReplaceAllString(s, `$1`)
	return s
}

func parseMongoshValue(text string) (any, error) {
	normalized := normalizeMongoshExtended(text)
	trimmed := strings.TrimSpace(normalized)
	if strings.HasPrefix(trimmed, "[") {
		var arr bson.A
		if err := bson.UnmarshalExtJSON([]byte(trimmed), false, &arr); err != nil {
			return nil, err
		}
		return []any(arr), nil
	}
	var m bson.M
	if err := bson.UnmarshalExtJSON([]byte(trimmed), false, &m); err != nil {
		return nil, err
	}
	return map[string]any(m), nil
}

func (a *Adapter) Close() error {
	if a.client == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return a.client.Disconnect(ctx)
}

func (a *Adapter) ensureWritable() error {
	if a.profile.ReadOnly {
		return ErrReadOnly
	}
	if a.client == nil {
		return ErrNoClient
	}
	return nil
}

func IDFilter(key db.Key) (bson.M, error) {
	var id any
	if key.Columns != nil {
		id = key.Columns["_id"]
	}
	if id == nil {
		id = key.ID
	}
	text, ok := id.(string)
	if ok {
		if text == "" {
			return nil, ErrMissingID
		}
		if objectID, err := bson.ObjectIDFromHex(text); err == nil {
			return bson.M{"_id": objectID}, nil
		}
		return bson.M{"_id": text}, nil
	}
	if id == nil {
		return nil, ErrMissingID
	}
	return bson.M{"_id": id}, nil
}

func BuildFilter(query db.Query) (bson.M, error) {
	if query.Filter != nil {
		return bson.M(query.Filter), nil
	}
	if strings.TrimSpace(query.Text) == "" {
		return bson.M{}, nil
	}
	var filter map[string]any
	if err := json.Unmarshal([]byte(query.Text), &filter); err != nil {
		return nil, fmt.Errorf("parse mongo filter json: %w", err)
	}
	return bson.M(filter), nil
}

func IndexDocumentsToMetadata(docs []bson.M) db.ObjectMetadata {
	indexes := make([]db.MetadataIndex, 0, len(docs))
	for _, doc := range docs {
		name, _ := doc["name"].(string)
		index := db.MetadataIndex{Name: name}
		if unique, ok := doc["unique"].(bool); ok {
			index.Unique = unique
		}
		index.Columns = indexColumns(doc["key"])
		indexes = append(indexes, index)
	}
	return db.ObjectMetadata{Indexes: indexes}
}

func indexColumns(raw any) []string {
	switch value := raw.(type) {
	case bson.D:
		out := make([]string, 0, len(value))
		for _, item := range value {
			out = append(out, item.Key)
		}
		return out
	case bson.M:
		out := make([]string, 0, len(value))
		for key := range value {
			out = append(out, key)
		}
		sort.Strings(out)
		return out
	default:
		return nil
	}
}

func docsToSet(docs []bson.M) result.Set {
	out := result.Set{Documents: make([]result.Document, 0, len(docs))}
	for _, doc := range docs {
		id := fmt.Sprint(doc["_id"])
		out.Documents = append(out.Documents, result.Document{ID: id, Data: map[string]any(doc)})
	}
	return out
}
