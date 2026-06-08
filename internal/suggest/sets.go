package suggest

import (
	"strings"

	"tdb/internal/config"
)

// Read-only lookup sets derived from the vendored lists, used by the editor's
// syntax highlighter (so highlighting and completion share one data source).
var (
	mysqlKeywordSet  = valueSet(mysqlKeywords)
	mysqlFunctionSet = functionNameSet(mysqlFunctions)
	dorisKeywordSet  = valueSet(dorisKeywords)
	dorisFunctionSet = functionNameSet(dorisFunctions)
	redisCommandSet  = valueSet(redisCommands)
	mongoMethodSet   = valueSet(mongoMethods)
)

func valueSet(items []Suggestion) map[string]bool {
	out := make(map[string]bool, len(items))
	for _, it := range items {
		out[strings.ToUpper(it.Value)] = true
	}
	return out
}

// functionNameSet keys by the bare upper-case function name (the vendored values
// carry a trailing "(").
func functionNameSet(items []Suggestion) map[string]bool {
	out := make(map[string]bool, len(items))
	for _, it := range items {
		out[strings.ToUpper(strings.TrimSuffix(it.Value, "("))] = true
	}
	return out
}

// SQLKeywordSet returns the upper-case keyword set for an SQL driver.
func SQLKeywordSet(driver config.Driver) map[string]bool {
	if driver == config.DriverDoris {
		return dorisKeywordSet
	}
	return mysqlKeywordSet
}

// SQLFunctionSet returns the upper-case built-in function names for an SQL driver.
func SQLFunctionSet(driver config.Driver) map[string]bool {
	if driver == config.DriverDoris {
		return dorisFunctionSet
	}
	return mysqlFunctionSet
}

// RedisCommandSet returns the upper-case Redis command set.
func RedisCommandSet() map[string]bool { return redisCommandSet }

// MongoMethodSet returns the known collection-method names.
func MongoMethodSet() map[string]bool { return mongoMethodSet }
