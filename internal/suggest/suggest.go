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
	Value  string
	Label  string
	Detail string
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

var sqlKeywords = []Suggestion{
	{Value: "SELECT", Detail: "read rows"},
	{Value: "FROM", Detail: "table source"},
	{Value: "WHERE", Detail: "filter rows"},
	{Value: "JOIN", Detail: "join table"},
	{Value: "LEFT JOIN", Detail: "left join table"},
	{Value: "RIGHT JOIN", Detail: "right join table"},
	{Value: "INNER JOIN", Detail: "inner join table"},
	{Value: "INSERT", Detail: "insert rows"},
	{Value: "UPDATE", Detail: "update rows"},
	{Value: "DELETE", Detail: "delete rows"},
	{Value: "ORDER BY", Detail: "sort rows"},
	{Value: "GROUP BY", Detail: "aggregate rows"},
	{Value: "HAVING", Detail: "filter groups"},
	{Value: "LIMIT", Detail: "limit rows"},
	{Value: "OFFSET", Detail: "skip rows"},
	{Value: "DISTINCT", Detail: "unique rows"},
	{Value: "UNION", Detail: "combine results"},
	{Value: "UNION ALL", Detail: "combine keeping dups"},
	{Value: "AS", Detail: "alias"},
	{Value: "ON", Detail: "join condition"},
	{Value: "AND", Detail: "logic and"},
	{Value: "OR", Detail: "logic or"},
	{Value: "NOT", Detail: "logic not"},
	{Value: "IN", Detail: "membership"},
	{Value: "LIKE", Detail: "pattern match"},
	{Value: "BETWEEN", Detail: "range"},
	{Value: "IS NULL", Detail: "null test"},
	{Value: "IS NOT NULL", Detail: "non-null test"},
	{Value: "ASC", Detail: "ascending"},
	{Value: "DESC", Detail: "descending"},
	{Value: "SHOW", Detail: "show metadata"},
	{Value: "DESCRIBE", Detail: "describe table"},
	{Value: "EXPLAIN", Detail: "explain query"},
}

// sqlCommonFunctions are built-in functions supported by both MySQL and Doris.
var sqlCommonFunctions = []Suggestion{
	// aggregate
	{Value: "COUNT(", Detail: "aggregate"},
	{Value: "SUM(", Detail: "aggregate"},
	{Value: "AVG(", Detail: "aggregate"},
	{Value: "MIN(", Detail: "aggregate"},
	{Value: "MAX(", Detail: "aggregate"},
	{Value: "GROUP_CONCAT(", Detail: "aggregate"},
	{Value: "STDDEV(", Detail: "aggregate"},
	{Value: "VARIANCE(", Detail: "aggregate"},
	// string
	{Value: "CONCAT(", Detail: "string"},
	{Value: "CONCAT_WS(", Detail: "string"},
	{Value: "SUBSTRING(", Detail: "string"},
	{Value: "LEFT(", Detail: "string"},
	{Value: "RIGHT(", Detail: "string"},
	{Value: "LENGTH(", Detail: "string"},
	{Value: "CHAR_LENGTH(", Detail: "string"},
	{Value: "LOWER(", Detail: "string"},
	{Value: "UPPER(", Detail: "string"},
	{Value: "TRIM(", Detail: "string"},
	{Value: "LTRIM(", Detail: "string"},
	{Value: "RTRIM(", Detail: "string"},
	{Value: "REPLACE(", Detail: "string"},
	{Value: "REVERSE(", Detail: "string"},
	{Value: "LPAD(", Detail: "string"},
	{Value: "RPAD(", Detail: "string"},
	{Value: "LOCATE(", Detail: "string"},
	{Value: "INSTR(", Detail: "string"},
	{Value: "REGEXP_REPLACE(", Detail: "string"},
	{Value: "SPLIT_PART(", Detail: "string"},
	// numeric
	{Value: "ABS(", Detail: "numeric"},
	{Value: "CEIL(", Detail: "numeric"},
	{Value: "FLOOR(", Detail: "numeric"},
	{Value: "ROUND(", Detail: "numeric"},
	{Value: "TRUNCATE(", Detail: "numeric"},
	{Value: "MOD(", Detail: "numeric"},
	{Value: "POW(", Detail: "numeric"},
	{Value: "SQRT(", Detail: "numeric"},
	{Value: "EXP(", Detail: "numeric"},
	{Value: "LN(", Detail: "numeric"},
	{Value: "LOG(", Detail: "numeric"},
	{Value: "SIGN(", Detail: "numeric"},
	{Value: "RAND(", Detail: "numeric"},
	{Value: "GREATEST(", Detail: "numeric"},
	{Value: "LEAST(", Detail: "numeric"},
	// date/time
	{Value: "NOW(", Detail: "date"},
	{Value: "CURDATE(", Detail: "date"},
	{Value: "CURTIME(", Detail: "date"},
	{Value: "CURRENT_TIMESTAMP(", Detail: "date"},
	{Value: "DATE(", Detail: "date"},
	{Value: "DATE_FORMAT(", Detail: "date"},
	{Value: "DATE_ADD(", Detail: "date"},
	{Value: "DATE_SUB(", Detail: "date"},
	{Value: "DATEDIFF(", Detail: "date"},
	{Value: "TIMESTAMPDIFF(", Detail: "date"},
	{Value: "EXTRACT(", Detail: "date"},
	{Value: "YEAR(", Detail: "date"},
	{Value: "MONTH(", Detail: "date"},
	{Value: "DAY(", Detail: "date"},
	{Value: "HOUR(", Detail: "date"},
	{Value: "MINUTE(", Detail: "date"},
	{Value: "SECOND(", Detail: "date"},
	{Value: "WEEK(", Detail: "date"},
	{Value: "DAYOFWEEK(", Detail: "date"},
	{Value: "UNIX_TIMESTAMP(", Detail: "date"},
	{Value: "FROM_UNIXTIME(", Detail: "date"},
	{Value: "STR_TO_DATE(", Detail: "date"},
	// conditional / conversion
	{Value: "IF(", Detail: "logic"},
	{Value: "IFNULL(", Detail: "logic"},
	{Value: "NULLIF(", Detail: "logic"},
	{Value: "COALESCE(", Detail: "logic"},
	{Value: "CASE", Detail: "logic"},
	{Value: "CAST(", Detail: "convert"},
	{Value: "CONVERT(", Detail: "convert"},
	// json
	{Value: "JSON_EXTRACT(", Detail: "json"},
	{Value: "JSON_OBJECT(", Detail: "json"},
	{Value: "JSON_ARRAY(", Detail: "json"},
	{Value: "JSON_CONTAINS(", Detail: "json"},
	{Value: "JSON_KEYS(", Detail: "json"},
	{Value: "JSON_LENGTH(", Detail: "json"},
	// window
	{Value: "ROW_NUMBER(", Detail: "window"},
	{Value: "RANK(", Detail: "window"},
	{Value: "DENSE_RANK(", Detail: "window"},
	{Value: "LAG(", Detail: "window"},
	{Value: "LEAD(", Detail: "window"},
	{Value: "FIRST_VALUE(", Detail: "window"},
	{Value: "LAST_VALUE(", Detail: "window"},
	{Value: "NTILE(", Detail: "window"},
	{Value: "OVER(", Detail: "window"},
}

// mysqlFunctions are MySQL-specific built-ins.
var mysqlFunctions = []Suggestion{
	{Value: "FIELD(", Detail: "string"},
	{Value: "ELT(", Detail: "string"},
	{Value: "FIND_IN_SET(", Detail: "string"},
	{Value: "MAKE_SET(", Detail: "string"},
	{Value: "SUBSTRING_INDEX(", Detail: "string"},
	{Value: "INET_ATON(", Detail: "network"},
	{Value: "INET_NTOA(", Detail: "network"},
	{Value: "UUID(", Detail: "misc"},
	{Value: "MD5(", Detail: "hash"},
	{Value: "SHA2(", Detail: "hash"},
	{Value: "JSON_PRETTY(", Detail: "json"},
	{Value: "JSON_MERGE_PATCH(", Detail: "json"},
	{Value: "JSON_SET(", Detail: "json"},
	{Value: "LAST_INSERT_ID(", Detail: "misc"},
}

// dorisFunctions are Doris-specific built-ins (bitmap/HLL/array analytics).
var dorisFunctions = []Suggestion{
	{Value: "APPROX_COUNT_DISTINCT(", Detail: "aggregate"},
	{Value: "BITMAP_UNION(", Detail: "bitmap"},
	{Value: "BITMAP_COUNT(", Detail: "bitmap"},
	{Value: "BITMAP_UNION_COUNT(", Detail: "bitmap"},
	{Value: "TO_BITMAP(", Detail: "bitmap"},
	{Value: "BITMAP_HASH(", Detail: "bitmap"},
	{Value: "BITMAP_AND(", Detail: "bitmap"},
	{Value: "BITMAP_OR(", Detail: "bitmap"},
	{Value: "HLL_UNION_AGG(", Detail: "hll"},
	{Value: "HLL_CARDINALITY(", Detail: "hll"},
	{Value: "HLL_HASH(", Detail: "hll"},
	{Value: "ARRAY(", Detail: "array"},
	{Value: "ARRAY_CONTAINS(", Detail: "array"},
	{Value: "ARRAY_MAP(", Detail: "array"},
	{Value: "ARRAY_FILTER(", Detail: "array"},
	{Value: "ARRAY_SORT(", Detail: "array"},
	{Value: "ARRAY_SIZE(", Detail: "array"},
	{Value: "ARRAYS_OVERLAP(", Detail: "array"},
	{Value: "ELEMENT_AT(", Detail: "array"},
	{Value: "EXPLODE(", Detail: "array"},
	{Value: "MASK(", Detail: "string"},
	{Value: "WIDTH_BUCKET(", Detail: "numeric"},
}

var redisCommands = []Suggestion{
	{Value: "GET", Detail: "read string"},
	{Value: "SET", Detail: "write string"},
	{Value: "DEL", Detail: "delete keys"},
	{Value: "SCAN", Detail: "cursor scan keys"},
	{Value: "HGET", Detail: "read hash field"},
	{Value: "HGETALL", Detail: "read hash"},
	{Value: "HSET", Detail: "write hash field"},
	{Value: "LRANGE", Detail: "read list range"},
	{Value: "LPUSH", Detail: "push list left"},
	{Value: "RPUSH", Detail: "push list right"},
	{Value: "SMEMBERS", Detail: "read set members"},
	{Value: "SADD", Detail: "add set member"},
	{Value: "ZADD", Detail: "add sorted-set member"},
	{Value: "ZRANGE", Detail: "read sorted-set range"},
	{Value: "TTL", Detail: "read ttl"},
	{Value: "EXPIRE", Detail: "set ttl"},
	// keys
	{Value: "EXISTS", Detail: "key"},
	{Value: "TYPE", Detail: "key"},
	{Value: "KEYS", Detail: "key"},
	{Value: "RENAME", Detail: "key"},
	{Value: "PERSIST", Detail: "key"},
	{Value: "PTTL", Detail: "key"},
	{Value: "PEXPIRE", Detail: "key"},
	{Value: "DUMP", Detail: "key"},
	{Value: "RANDOMKEY", Detail: "key"},
	{Value: "UNLINK", Detail: "key"},
	// string
	{Value: "INCR", Detail: "string"},
	{Value: "DECR", Detail: "string"},
	{Value: "INCRBY", Detail: "string"},
	{Value: "DECRBY", Detail: "string"},
	{Value: "INCRBYFLOAT", Detail: "string"},
	{Value: "APPEND", Detail: "string"},
	{Value: "GETSET", Detail: "string"},
	{Value: "MGET", Detail: "string"},
	{Value: "MSET", Detail: "string"},
	{Value: "SETEX", Detail: "string"},
	{Value: "SETNX", Detail: "string"},
	{Value: "PSETEX", Detail: "string"},
	{Value: "STRLEN", Detail: "string"},
	{Value: "GETRANGE", Detail: "string"},
	{Value: "SETRANGE", Detail: "string"},
	// hash
	{Value: "HDEL", Detail: "hash"},
	{Value: "HEXISTS", Detail: "hash"},
	{Value: "HKEYS", Detail: "hash"},
	{Value: "HVALS", Detail: "hash"},
	{Value: "HLEN", Detail: "hash"},
	{Value: "HINCRBY", Detail: "hash"},
	{Value: "HMGET", Detail: "hash"},
	{Value: "HSETNX", Detail: "hash"},
	{Value: "HSCAN", Detail: "hash"},
	// list
	{Value: "LPOP", Detail: "list"},
	{Value: "RPOP", Detail: "list"},
	{Value: "LLEN", Detail: "list"},
	{Value: "LINDEX", Detail: "list"},
	{Value: "LSET", Detail: "list"},
	{Value: "LREM", Detail: "list"},
	{Value: "LINSERT", Detail: "list"},
	{Value: "RPOPLPUSH", Detail: "list"},
	{Value: "BLPOP", Detail: "list"},
	// set
	{Value: "SREM", Detail: "set"},
	{Value: "SCARD", Detail: "set"},
	{Value: "SISMEMBER", Detail: "set"},
	{Value: "SPOP", Detail: "set"},
	{Value: "SRANDMEMBER", Detail: "set"},
	{Value: "SINTER", Detail: "set"},
	{Value: "SUNION", Detail: "set"},
	{Value: "SDIFF", Detail: "set"},
	{Value: "SSCAN", Detail: "set"},
	// sorted set
	{Value: "ZREM", Detail: "zset"},
	{Value: "ZSCORE", Detail: "zset"},
	{Value: "ZRANK", Detail: "zset"},
	{Value: "ZREVRANK", Detail: "zset"},
	{Value: "ZREVRANGE", Detail: "zset"},
	{Value: "ZRANGEBYSCORE", Detail: "zset"},
	{Value: "ZCARD", Detail: "zset"},
	{Value: "ZCOUNT", Detail: "zset"},
	{Value: "ZINCRBY", Detail: "zset"},
	{Value: "ZSCAN", Detail: "zset"},
	// bitmap / hll / pubsub
	{Value: "SETBIT", Detail: "bitmap"},
	{Value: "GETBIT", Detail: "bitmap"},
	{Value: "BITCOUNT", Detail: "bitmap"},
	{Value: "PFADD", Detail: "hll"},
	{Value: "PFCOUNT", Detail: "hll"},
	{Value: "PFMERGE", Detail: "hll"},
	{Value: "PUBLISH", Detail: "pubsub"},
	{Value: "SUBSCRIBE", Detail: "pubsub"},
	// server
	{Value: "DBSIZE", Detail: "server"},
	{Value: "INFO", Detail: "server"},
	{Value: "FLUSHDB", Detail: "server"},
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

var mongoQueryOperators = []Suggestion{
	{Value: "$eq", Detail: "compare"},
	{Value: "$in", Detail: "compare"},
	{Value: "$gt", Detail: "compare"},
	{Value: "$gte", Detail: "compare"},
	{Value: "$lt", Detail: "compare"},
	{Value: "$lte", Detail: "compare"},
	{Value: "$ne", Detail: "compare"},
	{Value: "$nin", Detail: "compare"},
	{Value: "$and", Detail: "logic"},
	{Value: "$or", Detail: "logic"},
	{Value: "$not", Detail: "logic"},
	{Value: "$nor", Detail: "logic"},
	{Value: "$exists", Detail: "element"},
	{Value: "$type", Detail: "element"},
	{Value: "$regex", Detail: "evaluation"},
	{Value: "$expr", Detail: "evaluation"},
	{Value: "$mod", Detail: "evaluation"},
	{Value: "$text", Detail: "evaluation"},
	{Value: "$where", Detail: "evaluation"},
	{Value: "$all", Detail: "array"},
	{Value: "$elemMatch", Detail: "array"},
	{Value: "$size", Detail: "array"},
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
	{Value: "$each", Detail: "array modifier"},
}

// mongoAggregateStages are pipeline stages for aggregate([...]).
var mongoAggregateStages = []Suggestion{
	{Value: "$match", Detail: "stage"},
	{Value: "$group", Detail: "stage"},
	{Value: "$project", Detail: "stage"},
	{Value: "$sort", Detail: "stage"},
	{Value: "$limit", Detail: "stage"},
	{Value: "$skip", Detail: "stage"},
	{Value: "$unwind", Detail: "stage"},
	{Value: "$lookup", Detail: "stage"},
	{Value: "$count", Detail: "stage"},
	{Value: "$addFields", Detail: "stage"},
	{Value: "$facet", Detail: "stage"},
	{Value: "$bucket", Detail: "stage"},
	{Value: "$replaceRoot", Detail: "stage"},
	{Value: "$sortByCount", Detail: "stage"},
	{Value: "$sample", Detail: "stage"},
	{Value: "$out", Detail: "stage"},
	{Value: "$merge", Detail: "stage"},
}

// mongoAggregateExpr are expression operators used inside aggregation stages.
var mongoAggregateExpr = []Suggestion{
	{Value: "$sum", Detail: "expr"},
	{Value: "$avg", Detail: "expr"},
	{Value: "$min", Detail: "expr"},
	{Value: "$max", Detail: "expr"},
	{Value: "$first", Detail: "expr"},
	{Value: "$last", Detail: "expr"},
	{Value: "$push", Detail: "expr"},
	{Value: "$addToSet", Detail: "expr"},
	{Value: "$concat", Detail: "expr"},
	{Value: "$toUpper", Detail: "expr"},
	{Value: "$toLower", Detail: "expr"},
	{Value: "$substr", Detail: "expr"},
	{Value: "$cond", Detail: "expr"},
	{Value: "$ifNull", Detail: "expr"},
	{Value: "$switch", Detail: "expr"},
	{Value: "$add", Detail: "expr"},
	{Value: "$subtract", Detail: "expr"},
	{Value: "$multiply", Detail: "expr"},
	{Value: "$divide", Detail: "expr"},
	{Value: "$map", Detail: "expr"},
	{Value: "$filter", Detail: "expr"},
	{Value: "$arrayElemAt", Detail: "expr"},
	{Value: "$dateToString", Detail: "expr"},
	{Value: "$year", Detail: "expr"},
	{Value: "$month", Detail: "expr"},
	{Value: "$dayOfMonth", Detail: "expr"},
	{Value: "$toString", Detail: "expr"},
	{Value: "$toInt", Detail: "expr"},
	{Value: "$toDouble", Detail: "expr"},
	{Value: "$toDate", Detail: "expr"},
	{Value: "$convert", Detail: "expr"},
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
	withSQLContext := func(driverFns []Suggestion) []Suggestion {
		candidates := append([]Suggestion{}, fieldSuggestions(ctx.Fields)...)
		candidates = append(candidates, objectSuggestions(ctx.Objects)...)
		candidates = append(candidates, sqlKeywords...)
		candidates = append(candidates, sqlCommonFunctions...)
		candidates = append(candidates, driverFns...)
		return candidates
	}
	switch ctx.Driver {
	case config.DriverMySQL:
		return withSQLContext(mysqlFunctions)
	case config.DriverDoris:
		return withSQLContext(dorisFunctions)
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
