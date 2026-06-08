package db

import (
	"context"
	"strings"

	"tdb/internal/config"
	"tdb/internal/result"
)

type ObjectType string

const (
	ObjectTable      ObjectType = "table"
	ObjectView       ObjectType = "view"
	ObjectCollection ObjectType = "collection"
	ObjectKey        ObjectType = "key"
)

type Object struct {
	Name string     `json:"name"`
	Type ObjectType `json:"type"`
	// SubType carries a finer data type when Type alone is not enough, e.g. the
	// redis key data type (string/list/set/hash/zset/stream).
	SubType string `json:"sub_type,omitempty"`
}

type MetadataField struct {
	Name     string `json:"name"`
	Type     string `json:"type,omitempty"`
	Nullable bool   `json:"nullable"`
	Default  string `json:"default,omitempty"`
}

type MetadataIndex struct {
	Name    string   `json:"name"`
	Columns []string `json:"columns"`
	Unique  bool     `json:"unique"`
}

type ObjectMetadata struct {
	Fields     []MetadataField   `json:"fields,omitempty"`
	Indexes    []MetadataIndex   `json:"indexes,omitempty"`
	Attributes map[string]string `json:"attributes,omitempty"`
}

type Scope struct {
	Database string `json:"database"`
	Schema   string `json:"schema,omitempty"`
}

type Target struct {
	Database string     `json:"database"`
	Schema   string     `json:"schema,omitempty"`
	Name     string     `json:"name"`
	Type     ObjectType `json:"type"`
}

// MaxResultRows bounds how many rows any single arbitrary query loads into
// memory, so a huge SELECT cannot freeze or OOM the TUI. Results beyond this are
// dropped and the Set is marked Truncated.
const MaxResultRows = 10000

type Page struct {
	Limit  int `json:"limit"`
	Offset int `json:"offset"`
}

type Query struct {
	Text   string         `json:"text"`
	Filter map[string]any `json:"filter,omitempty"`
}

type Command struct {
	Text     string `json:"text"`
	Database string `json:"database,omitempty"`
}

type Key struct {
	Columns map[string]any `json:"columns"`
	ID      string         `json:"id,omitempty"`
}

type Adapter interface {
	Test(context.Context) error
	ListDatabases(context.Context) ([]string, error)
	ListObjects(context.Context, Scope) ([]Object, error)
	Preview(context.Context, Target, Query, Page) (result.Set, error)
	Insert(context.Context, Target, map[string]any) (result.MutationResult, error)
	Update(context.Context, Target, Key, map[string]any) (result.MutationResult, error)
	Delete(context.Context, Target, Key) (result.MutationResult, error)
	Execute(context.Context, Command) (result.Set, error)
	Close() error
}

type MetadataProvider interface {
	Metadata(context.Context, Target) (ObjectMetadata, error)
}

type Factory func(config.Profile) Adapter

func NewPage(limit, offset int) Page {
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	if offset < 0 {
		offset = 0
	}
	return Page{Limit: limit, Offset: offset}
}

func (t Target) String() string {
	parts := []string{}
	if t.Database != "" {
		parts = append(parts, t.Database)
	}
	if t.Schema != "" {
		parts = append(parts, t.Schema)
	}
	if t.Name != "" {
		parts = append(parts, t.Name)
	}
	return strings.Join(parts, ".")
}
