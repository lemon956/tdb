package redisadapter

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"tdb/internal/config"
	"tdb/internal/db"
	"tdb/internal/result"
)

var (
	ErrReadOnly   = errors.New("connection is read-only")
	ErrNoClient   = errors.New("redis client is not initialized")
	ErrBadValue   = errors.New("redis value is invalid")
	ErrBadCommand = errors.New("redis command is empty")
)

type ScanRequest struct {
	Cursor  uint64
	Pattern string
	Count   int64
}

type KeyScan struct {
	Keys       []string `json:"keys"`
	NextCursor uint64   `json:"next_cursor"`
	HasMore    bool     `json:"has_more"`
	Pattern    string   `json:"pattern"`
	Count      int64    `json:"count"`
}

type RedisValue struct {
	Key        string        `json:"key"`
	Type       string        `json:"type"`
	TTL        time.Duration `json:"ttl"`
	String     string        `json:"string,omitempty"`
	Hash       map[string]string
	List       []string
	Set        []string
	SortedSet  []redis.Z
	PreviewCap int64 `json:"preview_cap"`
	// Truncated is set when the value was larger than the preview cap.
	Truncated bool `json:"truncated,omitempty"`
}

// maxStringPreview caps how many bytes of a redis string value are fetched, so a
// multi-megabyte value cannot be loaded whole into the TUI.
const maxStringPreview = 64 * 1024

// redisReadOnlyCommands is the allowlist of commands permitted on a read-only
// connection; anything not listed is treated as a write and rejected.
var redisReadOnlyCommands = map[string]bool{
	"GET": true, "MGET": true, "STRLEN": true, "GETRANGE": true, "SUBSTR": true,
	"EXISTS": true, "TYPE": true, "TTL": true, "PTTL": true, "OBJECT": true,
	"HGET": true, "HMGET": true, "HGETALL": true, "HKEYS": true, "HVALS": true, "HLEN": true, "HEXISTS": true, "HSCAN": true, "HSTRLEN": true,
	"LRANGE": true, "LINDEX": true, "LLEN": true, "LPOS": true,
	"SMEMBERS": true, "SISMEMBER": true, "SMISMEMBER": true, "SCARD": true, "SRANDMEMBER": true, "SSCAN": true, "SINTER": true, "SUNION": true, "SDIFF": true,
	"ZRANGE": true, "ZRANGEBYSCORE": true, "ZRANGEBYLEX": true, "ZREVRANGE": true, "ZSCORE": true, "ZMSCORE": true, "ZRANK": true, "ZREVRANK": true, "ZCARD": true, "ZCOUNT": true, "ZSCAN": true,
	"XRANGE": true, "XREVRANGE": true, "XLEN": true, "XREAD": true, "XINFO": true,
	"SCAN": true, "KEYS": true, "RANDOMKEY": true, "DBSIZE": true,
	"INFO": true, "PING": true, "ECHO": true, "TIME": true, "MEMORY": true, "DEBUG": true, "COMMAND": true, "CLIENT": true, "CONFIG": true,
	"BITCOUNT": true, "BITPOS": true, "GETBIT": true, "GEOPOS": true, "GEODIST": true, "GEOSEARCH": true,
	"PFCOUNT": true, "DUMP": true,
}

type Adapter struct {
	profile config.Profile
	client  *redis.Client
}

func New(profile config.Profile) *Adapter {
	return NewWithClient(profile, redis.NewClient(BuildRedisOptions(profile)))
}

func NewWithClient(profile config.Profile, client *redis.Client) *Adapter {
	return &Adapter{profile: profile, client: client}
}

func BuildRedisOptions(profile config.Profile) *redis.Options {
	host := profile.Host
	if host == "" {
		host = "127.0.0.1"
	}
	port := profile.Port
	if port == 0 {
		port = 6379
	}
	return &redis.Options{
		Addr:     host + ":" + strconv.Itoa(port),
		Username: profile.User,
		Password: profile.Password,
		DB:       profile.RedisDB,
	}
}

func NormalizeScanRequest(req ScanRequest) ScanRequest {
	if req.Pattern == "" {
		req.Pattern = "*"
	}
	if req.Count <= 0 {
		req.Count = 100
	}
	if req.Count > 500 {
		req.Count = 500
	}
	return req
}

func (a *Adapter) Test(ctx context.Context) error {
	if a.client == nil {
		return ErrNoClient
	}
	return a.client.Ping(ctx).Err()
}

func (a *Adapter) ListDatabases(context.Context) ([]string, error) {
	return []string{strconv.Itoa(a.profile.RedisDB)}, nil
}

func (a *Adapter) ListObjects(ctx context.Context, _ db.Scope) ([]db.Object, error) {
	scan, err := a.ScanKeys(ctx, ScanRequest{Pattern: "*", Count: 100})
	if err != nil {
		return nil, err
	}
	types, err := a.KeyTypes(ctx, scan.Keys)
	if err != nil {
		return nil, err
	}
	objects := make([]db.Object, 0, len(scan.Keys))
	for i, key := range scan.Keys {
		objects = append(objects, db.Object{Name: key, Type: db.ObjectKey, SubType: types[i]})
	}
	return objects, nil
}

// KeyTypes returns the redis data type (string/list/set/hash/zset/stream) for
// each key, fetched in a single pipelined round-trip. The result is index-aligned
// with keys; a failed TYPE yields an empty string for that key.
func (a *Adapter) KeyTypes(ctx context.Context, keys []string) ([]string, error) {
	if a.client == nil {
		return nil, ErrNoClient
	}
	types := make([]string, len(keys))
	if len(keys) == 0 {
		return types, nil
	}
	pipe := a.client.Pipeline()
	cmds := make([]*redis.StatusCmd, len(keys))
	for i, key := range keys {
		cmds[i] = pipe.Type(ctx, key)
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, err
	}
	for i, cmd := range cmds {
		if t, err := cmd.Result(); err == nil && t != "none" {
			types[i] = t
		}
	}
	return types, nil
}

func (a *Adapter) Preview(ctx context.Context, target db.Target, query db.Query, page db.Page) (result.Set, error) {
	if target.Name == "" {
		req := ScanRequest{Pattern: query.Text, Count: int64(page.Limit)}
		scan, err := a.ScanKeys(ctx, req)
		if err != nil {
			return result.Set{}, err
		}
		return result.Set{Value: scan, HasMore: scan.HasMore, NextCursor: strconv.FormatUint(scan.NextCursor, 10)}, nil
	}
	value, err := a.ReadKey(ctx, target.Name)
	if err != nil {
		return result.Set{}, err
	}
	return result.Set{Value: value, Truncated: value.Truncated}, nil
}

func (a *Adapter) Metadata(ctx context.Context, target db.Target) (db.ObjectMetadata, error) {
	value, err := a.ReadKey(ctx, target.Name)
	if err != nil {
		return db.ObjectMetadata{}, err
	}
	return RedisValueMetadata(value), nil
}

func RedisValueMetadata(value RedisValue) db.ObjectMetadata {
	return db.ObjectMetadata{Attributes: map[string]string{
		"key":        value.Key,
		"type":       value.Type,
		"ttl":        value.TTL.String(),
		"length":     strconv.Itoa(redisValueLength(value)),
		"previewCap": strconv.FormatInt(value.PreviewCap, 10),
	}}
}

func redisValueLength(value RedisValue) int {
	switch value.Type {
	case "string":
		return len(value.String)
	case "hash":
		return len(value.Hash)
	case "list":
		return len(value.List)
	case "set":
		return len(value.Set)
	case "zset":
		return len(value.SortedSet)
	default:
		return 0
	}
}

func (a *Adapter) ScanKeys(ctx context.Context, req ScanRequest) (KeyScan, error) {
	if a.client == nil {
		return KeyScan{}, ErrNoClient
	}
	req = NormalizeScanRequest(req)
	keys, next, err := a.client.Scan(ctx, req.Cursor, req.Pattern, req.Count).Result()
	if err != nil {
		return KeyScan{}, err
	}
	return KeyScan{Keys: keys, NextCursor: next, HasMore: next != 0, Pattern: req.Pattern, Count: req.Count}, nil
}

func (a *Adapter) ReadKey(ctx context.Context, key string) (RedisValue, error) {
	if a.client == nil {
		return RedisValue{}, ErrNoClient
	}
	valueType, err := a.client.Type(ctx, key).Result()
	if err != nil {
		return RedisValue{}, err
	}
	ttl, err := a.client.TTL(ctx, key).Result()
	if err != nil {
		return RedisValue{}, err
	}
	out := RedisValue{Key: key, Type: valueType, TTL: ttl, PreviewCap: 100}
	switch valueType {
	case "string":
		// Cap large strings: fetch only the first maxStringPreview bytes.
		if n, lerr := a.client.StrLen(ctx, key).Result(); lerr == nil && n > maxStringPreview {
			out.String, err = a.client.GetRange(ctx, key, 0, maxStringPreview-1).Result()
			out.Truncated = true
		} else {
			out.String, err = a.client.Get(ctx, key).Result()
		}
	case "hash":
		out.Hash, err = a.client.HGetAll(ctx, key).Result()
		if int64(len(out.Hash)) > out.PreviewCap {
			out.Truncated = true
		}
	case "list":
		out.List, err = a.client.LRange(ctx, key, 0, out.PreviewCap-1).Result()
		if n, lerr := a.client.LLen(ctx, key).Result(); lerr == nil && n > out.PreviewCap {
			out.Truncated = true
		}
	case "set":
		out.Set, err = a.client.SMembers(ctx, key).Result()
		if int64(len(out.Set)) > out.PreviewCap {
			out.Set = out.Set[:out.PreviewCap]
			out.Truncated = true
		}
	case "zset":
		out.SortedSet, err = a.client.ZRangeWithScores(ctx, key, 0, out.PreviewCap-1).Result()
		if n, lerr := a.client.ZCard(ctx, key).Result(); lerr == nil && n > out.PreviewCap {
			out.Truncated = true
		}
	}
	if err != nil && !errors.Is(err, redis.Nil) {
		return RedisValue{}, err
	}
	return out, nil
}

func (a *Adapter) Insert(ctx context.Context, target db.Target, values map[string]any) (result.MutationResult, error) {
	return a.write(ctx, target.Name, values)
}

func (a *Adapter) Update(ctx context.Context, target db.Target, _ db.Key, values map[string]any) (result.MutationResult, error) {
	return a.write(ctx, target.Name, values)
}

func (a *Adapter) Delete(ctx context.Context, target db.Target, key db.Key) (result.MutationResult, error) {
	if err := a.ensureWritable(); err != nil {
		return result.MutationResult{}, err
	}
	if len(key.Columns) == 0 {
		deleted, err := a.client.Del(ctx, target.Name).Result()
		if err != nil {
			return result.MutationResult{}, err
		}
		return result.NewMutationResult(deleted), nil
	}
	affected, err := a.deleteMember(ctx, target.Name, key.Columns)
	if err != nil {
		return result.MutationResult{}, err
	}
	return result.NewMutationResult(affected), nil
}

func (a *Adapter) Execute(ctx context.Context, command db.Command) (result.Set, error) {
	parts := splitCommand(command.Text)
	if len(parts) == 0 {
		return result.Set{}, ErrBadCommand
	}
	// Enforce read-only: only allow known read commands on a read-only connection.
	if a.profile.ReadOnly && !redisReadOnlyCommands[strings.ToUpper(parts[0])] {
		return result.Set{}, ErrReadOnly
	}
	if a.client == nil {
		return result.Set{}, ErrNoClient
	}
	args := make([]any, len(parts))
	for i, part := range parts {
		args[i] = part
	}
	value, err := a.client.Do(ctx, args...).Result()
	if err != nil {
		return result.Set{}, err
	}
	return result.Set{Value: value}, nil
}

func (a *Adapter) Close() error {
	if a.client == nil {
		return nil
	}
	return a.client.Close()
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

func (a *Adapter) write(ctx context.Context, key string, values map[string]any) (result.MutationResult, error) {
	if err := a.ensureWritable(); err != nil {
		return result.MutationResult{}, err
	}
	kind := strings.ToLower(asString(values["type"]))
	var err error
	var affected int64 = 1
	switch kind {
	case "string":
		err = a.client.Set(ctx, key, asString(values["value"]), ttlFromValues(values)).Err()
	case "hash":
		fields := fieldsFromValues(values)
		if len(fields) == 0 {
			return result.MutationResult{}, ErrBadValue
		}
		affected, err = a.client.HSet(ctx, key, fields).Result()
	case "list":
		if index, ok := asInt64(values["index"]); ok {
			err = a.client.LSet(ctx, key, index, asString(values["value"])).Err()
		} else {
			affected, err = a.client.RPush(ctx, key, asString(values["value"])).Result()
		}
	case "set":
		affected, err = a.client.SAdd(ctx, key, membersFromValues(values)...).Result()
	case "zset":
		member := asString(values["member"])
		score, _ := asFloat64(values["score"])
		affected, err = a.client.ZAdd(ctx, key, redis.Z{Member: member, Score: score}).Result()
	default:
		return result.MutationResult{}, ErrBadValue
	}
	if err != nil {
		return result.MutationResult{}, err
	}
	return result.NewMutationResult(affected), nil
}

func (a *Adapter) deleteMember(ctx context.Context, key string, columns map[string]any) (int64, error) {
	kind := strings.ToLower(asString(columns["type"]))
	switch kind {
	case "hash":
		return a.client.HDel(ctx, key, asString(columns["field"])).Result()
	case "list":
		index, ok := asInt64(columns["index"])
		if !ok {
			return 0, ErrBadValue
		}
		marker := "__tdb_deleted_" + strconv.FormatInt(time.Now().UnixNano(), 10)
		if err := a.client.LSet(ctx, key, index, marker).Err(); err != nil {
			return 0, err
		}
		return a.client.LRem(ctx, key, 1, marker).Result()
	case "set":
		return a.client.SRem(ctx, key, asString(columns["member"])).Result()
	case "zset":
		return a.client.ZRem(ctx, key, asString(columns["member"])).Result()
	default:
		return 0, ErrBadValue
	}
}

func ttlFromValues(values map[string]any) time.Duration {
	seconds, ok := asInt64(values["ttl_seconds"])
	if !ok || seconds <= 0 {
		return 0
	}
	return time.Duration(seconds) * time.Second
}

func fieldsFromValues(values map[string]any) map[string]any {
	out := map[string]any{}
	if fields, ok := values["fields"].(map[string]any); ok {
		for key, value := range fields {
			out[key] = value
		}
	}
	field := asString(values["field"])
	if field != "" {
		out[field] = asString(values["value"])
	}
	return out
}

func membersFromValues(values map[string]any) []any {
	if members, ok := values["members"].([]any); ok {
		return members
	}
	if stringsList, ok := values["members"].([]string); ok {
		out := make([]any, len(stringsList))
		for i, member := range stringsList {
			out[i] = member
		}
		return out
	}
	member := asString(values["member"])
	if member == "" {
		member = asString(values["value"])
	}
	if member == "" {
		return nil
	}
	return []any{member}
}

func asString(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case fmt.Stringer:
		return typed.String()
	case nil:
		return ""
	default:
		return fmt.Sprint(typed)
	}
}

func asInt64(value any) (int64, bool) {
	switch typed := value.(type) {
	case int:
		return int64(typed), true
	case int64:
		return typed, true
	case float64:
		return int64(typed), true
	case string:
		parsed, err := strconv.ParseInt(typed, 10, 64)
		return parsed, err == nil
	default:
		return 0, false
	}
}

func asFloat64(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case string:
		parsed, err := strconv.ParseFloat(typed, 64)
		return parsed, err == nil
	default:
		return 0, false
	}
}

func splitCommand(command string) []string {
	fields := []string{}
	var current strings.Builder
	inQuote := false
	for _, r := range strings.TrimSpace(command) {
		switch {
		case r == '"':
			inQuote = !inQuote
		case r == ' ' && !inQuote:
			if current.Len() > 0 {
				fields = append(fields, current.String())
				current.Reset()
			}
		default:
			current.WriteRune(r)
		}
	}
	if current.Len() > 0 {
		fields = append(fields, current.String())
	}
	return fields
}
