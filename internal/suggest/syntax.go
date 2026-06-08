package suggest

import (
	_ "embed"
	"encoding/json"
	"sort"
	"strings"
)

// Keyword / function / operator / command data vendored from upstream syntax
// libraries via `make syntax` (see internal/suggest/gen). These embedded JSON
// files are committed, so builds and CI need no network.
//
//	Mongo  — github.com/mongodb-js/ace-autocompleter (Apache-2.0)
//	Redis  — github.com/redis/redis-doc commands.json
//	MySQL  — github.com/DTStack/monaco-sql-languages (MIT)
//	Doris  — github.com/apache/doris Builtin*Functions.java (Apache-2.0); keywords reuse MySQL
var (
	//go:embed data/mongo_query.json
	rawMongoQuery []byte
	//go:embed data/mongo_stage.json
	rawMongoStage []byte
	//go:embed data/mongo_expr.json
	rawMongoExpr []byte
	//go:embed data/redis.json
	rawRedis []byte
	//go:embed data/mysql_keywords.json
	rawMySQLKeywords []byte
	//go:embed data/mysql_functions.json
	rawMySQLFunctions []byte
	//go:embed data/doris_functions.json
	rawDorisFunctions []byte
)

var (
	mongoQueryOperators  = loadItems(rawMongoQuery)
	mongoAggregateStages = loadItems(rawMongoStage)
	mongoAggregateExpr   = loadItems(rawMongoExpr)
	redisCommands        = loadItems(rawRedis)
	mysqlKeywords        = loadItems(rawMySQLKeywords)
	mysqlFunctions       = loadItems(rawMySQLFunctions)
	dorisFunctions       = loadItems(rawDorisFunctions)

	// Doris speaks MySQL-compatible SQL, so it reuses the MySQL keyword set plus a
	// few Doris-specific DDL keywords (the Doris grammar has no clean published
	// data file to vendor).
	dorisKeywordExtras = []Suggestion{
		{Value: "DISTRIBUTED", Detail: "keyword"}, {Value: "BUCKETS", Detail: "keyword"},
		{Value: "ROLLUP", Detail: "keyword"}, {Value: "AGGREGATE", Detail: "keyword"},
		{Value: "DUPLICATE", Detail: "keyword"}, {Value: "PROPERTIES", Detail: "keyword"},
		{Value: "HLL", Detail: "keyword"}, {Value: "BITMAP", Detail: "keyword"},
		{Value: "TABLET", Detail: "keyword"}, {Value: "CATALOG", Detail: "keyword"},
	}
	dorisKeywords = dedupSuggestions(append(append([]Suggestion{}, mysqlKeywords...), dorisKeywordExtras...))
)

func loadItems(raw []byte) []Suggestion {
	var items []Suggestion
	if err := json.Unmarshal(raw, &items); err != nil {
		// Embedded data is generated and validated by `make syntax`; a parse
		// failure means a corrupt build artifact.
		panic("suggest: invalid embedded syntax data: " + err.Error())
	}
	return items
}

func dedupSuggestions(items []Suggestion) []Suggestion {
	seen := make(map[string]bool, len(items))
	out := make([]Suggestion, 0, len(items))
	for _, it := range items {
		key := strings.ToLower(it.Value)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, it)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Value < out[j].Value })
	return out
}
