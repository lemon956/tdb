package app

import "tdb/internal/config"

// Per-driver system prompts steer the AI toward the right dialect, an output
// fence the extractor can pull (see aiFenceLangs), and sensible safety/perf
// guardrails. They are intentionally built-in constants: the schema context
// (buildSchemaContext) supplies the live tables/columns; this supplies the rules.

const aiPromptPreamble = "You are a database assistant embedded in a terminal DB client (TDB). Be concise."

const aiPromptMySQL = aiPromptPreamble + " " +
	"You are connected to a MySQL database. Produce standard MySQL SQL. " +
	"Quote identifiers with backticks when they need it. For preview or exploratory SELECTs, " +
	"add a LIMIT (e.g. LIMIT 100) unless the user asked for an aggregate or a full export. " +
	"For large or partitioned tables, filter in the WHERE clause on an indexed, partition, or time " +
	"column instead of scanning the whole table. The schema context lists each table's partition and " +
	"key columns — use them to build the filter. " +
	"Put each runnable statement in a ```sql fenced code block."

const aiPromptDoris = aiPromptPreamble + " " +
	"You are connected to an Apache Doris warehouse (MySQL-compatible protocol and SQL, OLAP). " +
	"Use Doris SQL. Tables may live in external catalogs — when a table is not in the active catalog, " +
	"reference it as catalog.database.table. " +
	"IMPORTANT: Doris strictly limits how many partitions one query may scan, so EVERY query against a " +
	"partitioned or large fact table MUST include a WHERE predicate on the partition column (usually a " +
	"date/time column) bounding it to a narrow range; never emit a query with no partition/time filter " +
	"(it will fail). The schema context gives each table's partition and key columns — filter on the " +
	"partition column. Prefer filtered or aggregated queries and add a LIMIT for previews. " +
	"Put each runnable statement in a ```sql fenced code block."

const aiPromptMongo = aiPromptPreamble + " " +
	"You are connected to MongoDB. Produce mongo shell (mongosh) commands of the form db.<collection>.<op>(...). " +
	"This client parses a SINGLE method call — do NOT chain (no .find(...).limit(n)). " +
	"Use only: find, findOne, countDocuments, aggregate, insertOne, insertMany, updateOne, updateMany, deleteOne, deleteMany. " +
	"To cap or shape a preview, use aggregate([{$match:{...}},{$project:{...}},{$limit:100}]); " +
	"for targeted reads pass a filter to find. Put each command in a ```js fenced code block."

const aiPromptRedis = aiPromptPreamble + " " +
	"You are connected to Redis. Produce native Redis CLI commands, one per line (e.g. GET key, HGETALL hash, ZRANGE key 0 -1). " +
	"To scan the keyspace use SCAN with MATCH/COUNT instead of KEYS * (which blocks the server). " +
	"Put the commands in a ```redis fenced code block."

const aiPromptReadOnly = " This connection is READ-ONLY — only suggest read commands; " +
	"do not produce INSERT/UPDATE/DELETE/SET/DEL or other writes."

// aiSystemPrompt returns the driver-specific system prompt, with a read-only
// guardrail appended when the connection forbids writes.
func aiSystemPrompt(driver config.Driver, readOnly bool) string {
	var base string
	switch driver {
	case config.DriverDoris:
		base = aiPromptDoris
	case config.DriverMongo:
		base = aiPromptMongo
	case config.DriverRedis:
		base = aiPromptRedis
	default: // mysql and any unknown SQL driver
		base = aiPromptMySQL
	}
	if readOnly {
		base += aiPromptReadOnly
	}
	return base
}

// aiFenceLangs is the extractor's language preference order for a driver, so a
// reply's natural fence (```js for mongo, ```redis for redis) is still pulled by
// ai.ExtractBlocks for one-key insertion. SQL is kept as a fallback in case the
// model labels its block ```sql anyway.
func aiFenceLangs(driver config.Driver) []string {
	switch driver {
	case config.DriverMongo:
		return []string{"js", "javascript", "mongodb", "mongosh", "sql"}
	case config.DriverRedis:
		return []string{"redis", "sh", "shell", "bash", "sql"}
	default: // mysql, doris, unknown
		return []string{"sql"}
	}
}

// profileReadOnly reports whether the active connection forbids writes.
func (m *Model) profileReadOnly() bool {
	return m.activeProfile != nil && m.activeProfile.ReadOnly
}

// aiDialectHint is a short human label for the active driver's command style,
// used in the AI panel's empty-state line.
func aiDialectHint(driver config.Driver) string {
	switch driver {
	case config.DriverMongo:
		return "mongo shell"
	case config.DriverRedis:
		return "Redis commands"
	case config.DriverDoris:
		return "Doris SQL"
	default:
		return "SQL"
	}
}
