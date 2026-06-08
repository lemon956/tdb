package suggest

import (
	"sort"
	"strings"

	"tdb/internal/config"
)

type Context struct {
	Page    string
	Driver  config.Driver
	Input   string
	Cursor  int
	Objects []string
	Fields  []Field
}

// Field is a column/field of the current table or collection. Type is the
// declared data type (e.g. "varchar(64)", "int", "string") and is shown after
// the field name in the suggestion list.
type Field struct {
	Name string
	Type string
}

type Suggestion struct {
	Value  string `json:"value"`
	Label  string `json:"label,omitempty"`
	Detail string `json:"detail,omitempty"`
}

var pageCommands = map[string][]Suggestion{
	"connections": {
		{Value: "new", Detail: "create connection"},
		{Value: "open", Detail: "open connection"},
		{Value: "test", Detail: "test connection"},
		{Value: "edit", Detail: "edit connection"},
		{Value: "delete", Detail: "delete connection"},
		{Value: "history", Detail: "show history"},
	},
	"browser": {
		{Value: "db", Detail: "select database"},
		{Value: "open", Detail: "open object"},
		{Value: "refresh", Detail: "reload metadata"},
		{Value: "query", Detail: "open query console"},
		{Value: "history", Detail: "show history"},
		{Value: "next", Detail: "next Redis scan page"},
	},
	"data": {
		{Value: "insert", Detail: "insert document or row"},
		{Value: "update", Detail: "update by key"},
		{Value: "delete", Detail: "delete by key"},
		{Value: "refresh", Detail: "reload data"},
		{Value: "query", Detail: "open query console"},
		{Value: "history", Detail: "show history"},
		{Value: "back", Detail: "return"},
	},
	"history": {
		{Value: "back", Detail: "return"},
	},
}

var mongoFragments = []Suggestion{
	{Value: "database", Detail: "database field"},
	{Value: "collection", Detail: "collection field"},
	{Value: "filter", Detail: "filter object"},
	{Value: "limit", Detail: "result limit"},
	{Value: "skip", Detail: "result offset"},
	{Value: "$eq", Detail: "equals"},
	{Value: "$in", Detail: "in array"},
	{Value: "$gt", Detail: "greater than"},
	{Value: "$lt", Detail: "less than"},
}

var mongoMethods = []Suggestion{
	{Value: "find", Detail: "query documents"},
	{Value: "findOne", Detail: "query one document"},
	{Value: "countDocuments", Detail: "count documents"},
	{Value: "aggregate", Detail: "aggregation pipeline"},
	{Value: "insertOne", Detail: "insert one document"},
	{Value: "insertMany", Detail: "insert many documents"},
	{Value: "updateOne", Detail: "update one document"},
	{Value: "updateMany", Detail: "update many documents"},
	{Value: "deleteOne", Detail: "delete one document"},
	{Value: "deleteMany", Detail: "delete many documents"},
	{Value: "replaceOne", Detail: "replace one document"},
	{Value: "distinct", Detail: "distinct values"},
	{Value: "count", Detail: "count (legacy)"},
	{Value: "estimatedDocumentCount", Detail: "fast count"},
	{Value: "findOneAndUpdate", Detail: "find & update"},
	{Value: "findOneAndDelete", Detail: "find & delete"},
	{Value: "findOneAndReplace", Detail: "find & replace"},
	{Value: "bulkWrite", Detail: "bulk operations"},
	{Value: "createIndex", Detail: "create index"},
	{Value: "dropIndex", Detail: "drop index"},
	{Value: "drop", Detail: "drop collection"},
}

var mongoUpdateOperators = []Suggestion{
	{Value: "$set", Detail: "field"},
	{Value: "$unset", Detail: "field"},
	{Value: "$inc", Detail: "field"},
	{Value: "$push", Detail: "array"},
	{Value: "$pull", Detail: "array"},
	{Value: "$setOnInsert", Detail: "field"},
	{Value: "$rename", Detail: "field"},
	{Value: "$mul", Detail: "field"},
	{Value: "$min", Detail: "field"},
	{Value: "$max", Detail: "field"},
	{Value: "$currentDate", Detail: "field"},
	{Value: "$addToSet", Detail: "array"},
	{Value: "$pop", Detail: "array"},
	{Value: "$pullAll", Detail: "array"},
	{Value: "$bit", Detail: "bitwise"},
	{Value: "$", Detail: "array (positional)"},
	{Value: "$[]", Detail: "array (all)"},
	{Value: "$[<identifier>]", Detail: "array (filtered)"},
	{Value: "$each", Detail: "array modifier"},
	{Value: "$position", Detail: "array modifier"},
	{Value: "$slice", Detail: "array modifier"},
	{Value: "$sort", Detail: "array modifier"},
}

func Suggest(ctx Context) []Suggestion {
	input := inputAtCursor(ctx)
	candidates := candidatesFor(ctx, input)
	token := currentToken(input)
	filtered := make([]Suggestion, 0, len(candidates))
	for _, candidate := range candidates {
		if matches(candidate.Value, token) {
			if candidate.Label == "" {
				candidate.Label = candidate.Value
			}
			filtered = append(filtered, candidate)
		}
	}
	// Prefix matches are more relevant than mid-string keyword matches, so order
	// them first while keeping each group's original order.
	if lower := strings.ToLower(token); lower != "" {
		sort.SliceStable(filtered, func(i, j int) bool {
			pi := strings.HasPrefix(strings.ToLower(filtered[i].Value), lower)
			pj := strings.HasPrefix(strings.ToLower(filtered[j].Value), lower)
			return pi && !pj
		})
	}
	return filtered
}

func candidatesFor(ctx Context, input string) []Suggestion {
	// withSQLContext combines the current table's fields/objects with the driver's
	// keyword and function lists (vendored from the upstream syntax libraries).
	withSQLContext := func(keywords, functions []Suggestion) []Suggestion {
		candidates := append([]Suggestion{}, fieldSuggestions(ctx.Fields)...)
		candidates = append(candidates, objectSuggestions(ctx.Objects)...)
		candidates = append(candidates, keywords...)
		candidates = append(candidates, functions...)
		return candidates
	}
	switch ctx.Driver {
	case config.DriverMySQL:
		return withSQLContext(mysqlKeywords, mysqlFunctions)
	case config.DriverDoris:
		return withSQLContext(dorisKeywords, dorisFunctions)
	case config.DriverRedis:
		return append(objectSuggestions(ctx.Objects), redisCommands...)
	case config.DriverMongo:
		return mongoCandidates(ctx, input)
	default:
		if commands, ok := pageCommands[ctx.Page]; ok {
			return commands
		}
		return nil
	}
}

func inputAtCursor(ctx Context) string {
	if ctx.Cursor > 0 && ctx.Cursor < len(ctx.Input) {
		return ctx.Input[:ctx.Cursor]
	}
	return ctx.Input
}

func mongoCandidates(ctx Context, input string) []Suggestion {
	if _, ok := mongoCollectionCompletion(input); ok {
		return objectSuggestions(ctx.Objects)
	}
	if _, ok := mongoMethodCompletion(input); ok {
		return mongoMethods
	}
	if mongoInsideDocument(input) {
		token := currentToken(input)
		if strings.HasPrefix(token, "$") {
			if mongoInsideUpdateDocument(input) {
				return mongoUpdateOperators
			}
			if mongoInsideAggregate(input) {
				ops := append([]Suggestion{}, mongoAggregateStages...)
				ops = append(ops, mongoAggregateExpr...)
				return append(ops, mongoQueryOperators...)
			}
			return mongoQueryOperators
		}
		candidates := append(fieldSuggestions(ctx.Fields), mongoFragments...)
		return append(candidates, append(mongoQueryOperators, mongoUpdateOperators...)...)
	}
	return append(objectSuggestions(ctx.Objects), mongoFragments...)
}

func mongoCollectionCompletion(input string) (string, bool) {
	trimmed := strings.TrimRight(input, " \t\n")
	idx := strings.LastIndex(trimmed, "db.")
	if idx < 0 {
		return "", false
	}
	after := trimmed[idx+len("db."):]
	if strings.Contains(after, ".") || strings.ContainsAny(after, "({[ ") {
		return "", false
	}
	return after, true
}

func mongoMethodCompletion(input string) (string, bool) {
	trimmed := strings.TrimRight(input, " \t\n")
	idx := strings.LastIndex(trimmed, "db.")
	if idx < 0 {
		return "", false
	}
	after := trimmed[idx+len("db."):]
	parts := strings.Split(after, ".")
	if len(parts) != 2 || parts[0] == "" || strings.ContainsAny(parts[1], "({[ ") {
		return "", false
	}
	return parts[1], true
}

func mongoInsideDocument(input string) bool {
	depth := 0
	for _, r := range input {
		switch r {
		case '{':
			depth++
		case '}':
			if depth > 0 {
				depth--
			}
		}
	}
	return depth > 0
}

func mongoInsideAggregate(input string) bool {
	return strings.Contains(input, "aggregate(")
}

func mongoInsideUpdateDocument(input string) bool {
	open := strings.LastIndex(input, "{")
	if open < 0 {
		return false
	}
	return strings.Contains(input[:open], "updateOne(") || strings.Contains(input[:open], "updateMany(")
}

func objectSuggestions(objects []string) []Suggestion {
	out := make([]Suggestion, 0, len(objects))
	for _, object := range objects {
		out = append(out, Suggestion{Value: object, Detail: "collection"})
	}
	return out
}

func fieldSuggestions(fields []Field) []Suggestion {
	out := make([]Suggestion, 0, len(fields))
	for _, field := range fields {
		detail := field.Type
		if detail == "" {
			detail = "field"
		}
		out = append(out, Suggestion{Value: field.Name, Detail: detail})
	}
	return out
}

func matches(value, token string) bool {
	if token == "" {
		return true
	}
	return strings.Contains(strings.ToLower(value), strings.ToLower(token))
}

func currentToken(input string) string {
	input = strings.TrimRight(input, " \t\n")
	start := len(input)
	for start > 0 {
		r := input[start-1]
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '$' {
			start--
			continue
		}
		break
	}
	return input[start:]
}
