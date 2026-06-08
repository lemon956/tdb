// Command gen fetches keyword / function / operator / command lists from the
// official or community-maintained syntax libraries for each driver and writes
// them as normalized JSON into internal/suggest/data/*.json (committed to the
// repo and embedded by the suggest package). Run it via `make syntax`.
//
// Sources (operator/keyword/function NAMES are factual data; attributions below):
//   - Mongo:  github.com/mongodb-js/ace-autocompleter      (Apache-2.0)
//   - Redis:  github.com/redis/redis-doc commands.json      (generated from redis/redis)
//   - MySQL:  github.com/DTStack/monaco-sql-languages       (MIT)
//   - Doris:  github.com/apache/doris Builtin*Functions.java(Apache-2.0); keywords reuse MySQL
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Pinned refs — bump these to refresh against newer upstream releases.
const (
	aceRef    = "main"
	redisRef  = "master"
	monacoRef = "main"
	dorisRef  = "master"
)

const (
	aceBase   = "https://raw.githubusercontent.com/mongodb-js/ace-autocompleter/" + aceRef + "/lib/constants/"
	redisURL  = "https://raw.githubusercontent.com/redis/redis-doc/" + redisRef + "/commands.json"
	monacoURL = "https://raw.githubusercontent.com/DTStack/monaco-sql-languages/" + monacoRef + "/src/languages/mysql/mysql.ts"
	dorisBase = "https://raw.githubusercontent.com/apache/doris/" + dorisRef + "/fe/fe-core/src/main/java/org/apache/doris/catalog/"
)

type item struct {
	Value  string `json:"value"`
	Detail string `json:"detail"`
}

func main() {
	outDir := "internal/suggest/data"
	if len(os.Args) > 1 {
		outDir = os.Args[1]
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		fail(err)
	}

	// --- Mongo (ace-autocompleter) ---
	query := aceItems(get(aceBase + "query-operators.js"))
	stage := aceItems(get(aceBase + "stage-operators.js"))
	expr := dedup(append(append(aceItems(get(aceBase+"expression-operators.js")),
		aceItems(get(aceBase+"accumulators.js"))...),
		aceItems(get(aceBase+"conversion-operators.js"))...))
	write(outDir, "mongo_query.json", query)
	write(outDir, "mongo_stage.json", stage)
	write(outDir, "mongo_expr.json", expr)

	// --- Redis (official commands.json) ---
	write(outDir, "redis.json", redisItems(get(redisURL)))

	// --- MySQL (monaco-sql-languages) ---
	ts := get(monacoURL)
	mysqlKeywords := dedup(append(tsArray(ts, "keywords", "keyword"), tsArray(ts, "operators", "keyword")...))
	mysqlFns := sqlFunctions(tsRawArray(ts, "builtinFunctions"))
	write(outDir, "mysql_keywords.json", mysqlKeywords)
	write(outDir, "mysql_functions.json", mysqlFns)

	// --- Doris (apache/doris function registries) ---
	dorisNames := []string{}
	for _, f := range []string{"BuiltinScalarFunctions.java", "BuiltinAggregateFunctions.java", "BuiltinTableValuedFunctions.java"} {
		dorisNames = append(dorisNames, javaFunctionNames(get(dorisBase+f))...)
	}
	write(outDir, "doris_functions.json", sqlFunctions(dorisNames))

	fmt.Println("syntax data written to", outDir)
}

// aceItems extracts {value, meta} pairs from an ace-autocompleter constants .js.
var aceValue = regexp.MustCompile(`value:\s*'([^']+)'`)
var aceMeta = regexp.MustCompile(`meta:\s*'([^']+)'`)

func aceItems(src string) []item {
	values := aceValue.FindAllStringSubmatch(src, -1)
	metas := aceMeta.FindAllStringSubmatch(src, -1)
	out := make([]item, 0, len(values))
	for i, v := range values {
		detail := "operator"
		if i < len(metas) {
			detail = metas[i][1]
		}
		out = append(out, item{Value: v[1], Detail: detail})
	}
	return dedup(out)
}

// redisItems parses redis-doc commands.json (map of command -> {group}).
func redisItems(src string) []item {
	var commands map[string]struct {
		Group string `json:"group"`
	}
	if err := json.Unmarshal([]byte(src), &commands); err != nil {
		fail(fmt.Errorf("redis commands.json: %w", err))
	}
	out := make([]item, 0, len(commands))
	for name, meta := range commands {
		out = append(out, item{Value: strings.ToUpper(name), Detail: meta.Group})
	}
	return dedup(out)
}

// tsRawArray returns the raw single-quoted strings of a `name: [ ... ]` array.
func tsRawArray(src, name string) []string {
	re := regexp.MustCompile(name + `\s*:\s*\[([\s\S]*?)\]`)
	m := re.FindStringSubmatch(src)
	if m == nil {
		fail(fmt.Errorf("monaco array %q not found", name))
	}
	strRe := regexp.MustCompile(`'([^']+)'`)
	out := []string{}
	for _, s := range strRe.FindAllStringSubmatch(m[1], -1) {
		out = append(out, s[1])
	}
	return out
}

func tsArray(src, name, detail string) []item {
	raw := tsRawArray(src, name)
	out := make([]item, 0, len(raw))
	for _, v := range raw {
		out = append(out, item{Value: strings.ToUpper(v), Detail: detail})
	}
	return dedup(out)
}

// sqlFunctions normalizes function names to upper-case with a trailing "(".
func sqlFunctions(names []string) []item {
	out := make([]item, 0, len(names))
	for _, n := range names {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		out = append(out, item{Value: strings.ToUpper(n) + "(", Detail: "fn"})
	}
	return dedup(out)
}

// javaFunctionNames extracts the quoted function names from a Builtin*Functions.java
// registry (the strings passed to scalar(Cls.class, "name", "alias", ...)).
var javaName = regexp.MustCompile(`"([a-z_][a-z0-9_]*)"`)

func javaFunctionNames(src string) []string {
	out := []string{}
	for _, m := range javaName.FindAllStringSubmatch(src, -1) {
		out = append(out, m[1])
	}
	return out
}

func dedup(items []item) []item {
	seen := map[string]bool{}
	out := make([]item, 0, len(items))
	for _, it := range items {
		key := strings.ToLower(it.Value)
		if seen[key] || it.Value == "" {
			continue
		}
		seen[key] = true
		out = append(out, it)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Value < out[j].Value })
	return out
}

func write(dir, name string, items []item) {
	raw, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		fail(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), append(raw, '\n'), 0o644); err != nil {
		fail(err)
	}
	fmt.Printf("  %-22s %d entries\n", name, len(items))
}

func get(url string) string {
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		fail(fmt.Errorf("GET %s: %w", url, err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fail(fmt.Errorf("GET %s: status %d", url, resp.StatusCode))
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		fail(err)
	}
	return string(body)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "gen:", err)
	os.Exit(1)
}
