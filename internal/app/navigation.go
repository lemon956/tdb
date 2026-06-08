package app

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"tdb/internal/db"
	"tdb/internal/result"
)

type workspaceMode string

const (
	workspacePreview  workspaceMode = "preview"
	workspaceMetadata workspaceMode = "metadata"
)

type navNodeKind string

const (
	navNodeConnection navNodeKind = "connection"
	navNodeDatabase   navNodeKind = "database"
	navNodeObject     navNodeKind = "object"
	navNodeMeta       navNodeKind = "metadata"
)

type navNode struct {
	ID          string
	Kind        navNodeKind
	Label       string
	Database    string
	Object      db.Object
	Target      db.Target
	Depth       int
	DatabaseIdx int
	ObjectIdx   int
}

func (m *Model) ensureNavigationState() {
	if m.icons.Database == "" {
		m.icons = IconSetForStyle(IconStyleUnicode)
	}
	if m.databaseObjects == nil {
		m.databaseObjects = map[string][]db.Object{}
	}
	if m.expandedDBs == nil {
		m.expandedDBs = map[string]bool{}
	}
	if m.expandedMeta == nil {
		m.expandedMeta = map[string]bool{}
	}
	if m.selectedDB != "" && len(m.objects) > 0 {
		m.databaseObjects[m.selectedDB] = m.objects
		if _, known := m.expandedDBs[m.selectedDB]; !known {
			m.expandedDBs[m.selectedDB] = true
		}
	}
}

func (m *Model) browserNodes() []navNode {
	m.ensureNavigationState()
	nodes := make([]navNode, 0, len(m.databases)+len(m.objects))
	objectCount := 0
	dbDepth := 0
	if m.activeProfile != nil {
		// The connection root is rendered by renderConnectionRow; Label here is a
		// plain mirror used only for width/search calculations.
		icon, _ := m.icons.DriverIcon(m.activeProfile.Driver)
		indicator := icon
		if indicator == "" {
			indicator = string(m.activeProfile.Driver)
		}
		label := m.icons.Expanded + " " + indicator + " " + m.activeProfile.ID
		if ep := connectionEndpoint(*m.activeProfile); ep != "" {
			label += " " + ep
		}
		if m.activeProfile.ReadOnly {
			label += " " + m.icons.Lock
		}
		nodes = append(nodes, navNode{
			ID:    "connection:0",
			Kind:  navNodeConnection,
			Label: label,
			Depth: 0,
		})
		dbDepth = 1
	}
	if len(m.databases) == 0 && len(m.objects) > 0 {
		for objectIndex, object := range m.objects {
			target := m.targetForObject(m.selectedDB, object)
			nodes = append(nodes, navNode{
				ID:        fmt.Sprintf("object:%d", objectIndex),
				Kind:      navNodeObject,
				Label:     m.objectNodeLabel(object),
				Database:  m.selectedDB,
				Object:    object,
				Target:    target,
				Depth:     dbDepth,
				ObjectIdx: objectIndex,
			})
		}
		return nodes
	}
	for dbIndex, database := range m.databases {
		expanded := m.expandedDBs[database]
		marker := m.icons.Collapsed
		if expanded {
			marker = m.icons.Expanded
		}
		nodes = append(nodes, navNode{
			ID:          fmt.Sprintf("database:%d", dbIndex),
			Kind:        navNodeDatabase,
			Label:       fmt.Sprintf("%s %s %s", marker, m.icons.Database, database),
			Database:    database,
			Depth:       dbDepth,
			DatabaseIdx: dbIndex,
		})
		if !expanded {
			continue
		}
		objects := m.databaseObjects[database]
		for objectIndex, object := range objects {
			target := m.targetForObject(database, object)
			nodes = append(nodes, navNode{
				ID:          fmt.Sprintf("object:%d", objectCount),
				Kind:        navNodeObject,
				Label:       m.objectNodeLabel(object),
				Database:    database,
				Object:      object,
				Target:      target,
				Depth:       dbDepth + 1,
				DatabaseIdx: dbIndex,
				ObjectIdx:   objectIndex,
			})
			objectCount++
			if m.expandedMeta[targetKey(target)] {
				nodes = append(nodes, navNode{ID: fmt.Sprintf("metadata:%d", objectCount), Kind: navNodeMeta, Label: m.icons.Metadata + " metadata", Database: database, Target: target, Depth: dbDepth + 2, DatabaseIdx: dbIndex, ObjectIdx: objectIndex})
				nodes = append(nodes, navNode{ID: fmt.Sprintf("fields:%d", objectCount), Kind: navNodeMeta, Label: m.icons.Metadata + " fields", Database: database, Target: target, Depth: dbDepth + 2, DatabaseIdx: dbIndex, ObjectIdx: objectIndex})
				nodes = append(nodes, navNode{ID: fmt.Sprintf("indexes:%d", objectCount), Kind: navNodeMeta, Label: m.icons.Metadata + " indexes", Database: database, Target: target, Depth: dbDepth + 2, DatabaseIdx: dbIndex, ObjectIdx: objectIndex})
			}
		}
	}
	return nodes
}

// objectNodeLabel renders an object's nav label as "<type-icon> <name>", with a
// trailing "(subtype)" only for redis keys (whose icon alone is cryptic).
func (m *Model) objectNodeLabel(object db.Object) string {
	label := m.icons.ObjectIcon(object) + " " + object.Name
	if object.Type == db.ObjectKey && object.SubType != "" {
		label += " (" + object.SubType + ")"
	}
	return label
}

func (m *Model) filterBrowserNodes(nodes []navNode) []navNode {
	query := strings.ToLower(strings.TrimSpace(m.navSearchQuery))
	if query == "" {
		return nodes
	}
	keep := map[int]bool{}
	for i, node := range nodes {
		haystack := strings.ToLower(node.Label + " " + node.Database + " " + node.Object.Name + " " + node.Target.Name)
		if !strings.Contains(haystack, query) {
			continue
		}
		keep[i] = true
		depth := node.Depth
		for j := i - 1; j >= 0 && depth > 0; j-- {
			if nodes[j].Depth < depth {
				keep[j] = true
				depth = nodes[j].Depth
			}
		}
	}
	filtered := make([]navNode, 0, len(keep))
	for i, node := range nodes {
		if keep[i] {
			filtered = append(filtered, node)
		}
	}
	return filtered
}

func (m *Model) syncNavigationSearchInput() {
	if !m.navSearchActive {
		return
	}
	m.navSearchQuery = m.input.Value()
	m.navSearchMatchIndex = -1
}

func (m *Model) clearNavigationSearch() {
	m.navSearchActive = false
	m.navSearchQuery = ""
	m.navSearchMatchIndex = -1
	m.input.Clear()
	m.focus = FocusSidebar
	m.navVerticalOffset = 0
}

type navSearchMatch struct {
	Kind     navNodeKind
	Database string
	Object   db.Object
}

func (m *Model) jumpNavigationSearch(next bool) {
	query := strings.ToLower(strings.TrimSpace(m.navSearchQuery))
	if query == "" {
		m.message = "search query is empty"
		return
	}
	m.ensureNavigationState()
	matches := m.navigationSearchMatches(query)
	if len(matches) == 0 {
		m.message = "no navigation match: " + m.navSearchQuery
		return
	}
	if next {
		m.navSearchMatchIndex++
	} else {
		m.navSearchMatchIndex = 0
	}
	if m.navSearchMatchIndex < 0 {
		m.navSearchMatchIndex = 0
	}
	m.navSearchMatchIndex %= len(matches)
	match := matches[m.navSearchMatchIndex]
	if match.Kind == navNodeObject {
		m.expandedDBs[match.Database] = true
	}
	m.focus = FocusSidebar
	if !m.focusSearchMatch(match) {
		m.message = "navigation match not visible"
		return
	}
	m.message = fmt.Sprintf("match %d/%d: %s", m.navSearchMatchIndex+1, len(matches), m.navSearchQuery)
}

func (m *Model) navigationSearchMatches(query string) []navSearchMatch {
	matches := []navSearchMatch{}
	if m.activeProfile != nil {
		haystack := strings.ToLower(m.activeProfile.ID + " " + string(m.activeProfile.Driver))
		if strings.Contains(haystack, query) {
			matches = append(matches, navSearchMatch{Kind: navNodeConnection})
		}
	}
	for _, database := range m.databases {
		if strings.Contains(strings.ToLower(database), query) {
			matches = append(matches, navSearchMatch{Kind: navNodeDatabase, Database: database})
		}
		for _, object := range m.databaseObjects[database] {
			haystack := strings.ToLower(database + " " + object.Name + " " + string(object.Type))
			if strings.Contains(haystack, query) {
				matches = append(matches, navSearchMatch{Kind: navNodeObject, Database: database, Object: object})
			}
		}
	}
	return matches
}

func (m *Model) focusSearchMatch(match navSearchMatch) bool {
	for i, node := range m.browserNodes() {
		switch match.Kind {
		case navNodeConnection:
			if node.Kind == navNodeConnection {
				m.browserCursor = i
				m.syncBrowserSelectionFromCursor()
				return true
			}
		case navNodeDatabase:
			if node.Kind == navNodeDatabase && node.Database == match.Database {
				m.browserCursor = i
				m.syncBrowserSelectionFromCursor()
				return true
			}
		case navNodeObject:
			if node.Kind == navNodeObject && node.Database == match.Database && node.Object.Name == match.Object.Name && node.Object.Type == match.Object.Type {
				m.browserCursor = i
				m.syncBrowserSelectionFromCursor()
				return true
			}
		}
	}
	return false
}

func (m *Model) selectedBrowserNode() (navNode, bool) {
	nodes := m.browserNodes()
	if len(nodes) == 0 {
		return navNode{}, false
	}
	if len(m.databases) == 0 && len(m.objects) > 0 {
		rootOffset := 0
		if m.activeProfile != nil {
			rootOffset = 1
		}
		m.browserCursor = clamp(rootOffset+m.objectIndex, 0, len(nodes)-1)
	} else if m.activeProfile != nil && len(m.databases) > 0 && m.browserCursor == 0 {
		m.browserCursor = 1
	}
	m.browserCursor = clamp(m.browserCursor, 0, len(nodes)-1)
	return nodes[m.browserCursor], true
}

func (m *Model) setBrowserCursorByNodeID(id string) {
	for i, node := range m.browserNodes() {
		if node.ID == id {
			m.browserCursor = i
			m.syncBrowserSelectionFromCursor()
			return
		}
	}
}

func (m *Model) visibleBrowserNodes(limit int) ([]navNode, int) {
	nodes := m.browserNodes()
	if len(nodes) == 0 || limit <= 0 || len(nodes) <= limit {
		m.navVerticalOffset = 0
		return nodes, 0
	}
	if m.activeProfile != nil && len(m.databases) > 0 && m.browserCursor == 0 {
		m.browserCursor = 1
	}
	m.browserCursor = clamp(m.browserCursor, 0, len(nodes)-1)
	maxOffset := max(0, len(nodes)-limit)
	offset := clamp(m.navVerticalOffset, 0, maxOffset)
	if m.browserCursor < offset {
		offset = m.browserCursor
	}
	if m.browserCursor >= offset+limit {
		offset = m.browserCursor - limit + 1
	}
	offset = clamp(offset, 0, maxOffset)
	m.navVerticalOffset = offset
	end := min(len(nodes), offset+limit)
	return nodes[offset:end], offset
}

func (m *Model) targetForObject(database string, object db.Object) db.Target {
	targetType := object.Type
	if targetType == "" {
		targetType = db.ObjectTable
	}
	target := db.Target{Database: database, Name: object.Name, Type: targetType}
	if target.Type == db.ObjectKey {
		target.Database = ""
	}
	return target
}

func targetKey(target db.Target) string {
	return target.String() + "|" + string(target.Type)
}

func sameTarget(left, right db.Target) bool {
	return left.Database == right.Database &&
		left.Schema == right.Schema &&
		left.Name == right.Name &&
		left.Type == right.Type
}

func (m *Model) loadObjectsForDatabase(ctx context.Context, database string) error {
	if m.adapter == nil {
		return fmt.Errorf("no active connection")
	}
	ctx, cancel := m.dbContext(ctx)
	defer cancel()
	objects, err := m.adapter.ListObjects(ctx, db.Scope{Database: database})
	if err != nil {
		return err
	}
	m.ensureNavigationState()
	m.databaseObjects[database] = objects
	if m.selectedDB == database {
		m.objects = objects
		m.objectIndex = 0
	}
	return nil
}

func (m *Model) toggleDatabase(ctx context.Context, index int) {
	if len(m.databases) == 0 {
		m.message = "no database available"
		return
	}
	m.ensureNavigationState()
	index = clamp(index, 0, len(m.databases)-1)
	database := m.databases[index]
	m.databaseIndex = index
	m.setBrowserCursorByNodeID(fmt.Sprintf("database:%d", index))
	if m.expandedDBs[database] {
		m.expandedDBs[database] = false
		m.message = "collapsed " + database
		return
	}
	m.selectedDB = database
	if err := m.loadObjectsForDatabase(ctx, database); err != nil {
		m.message = err.Error()
		return
	}
	m.expandedDBs[database] = true
	m.objects = m.databaseObjects[database]
	m.objectIndex = 0
	m.message = "expanded " + database
}

func (m *Model) openPreview(ctx context.Context, target db.Target) {
	if m.adapter == nil {
		m.message = "no active connection"
		return
	}
	ctx, cancel := m.dbContext(ctx)
	defer cancel()
	res, err := m.adapter.Preview(ctx, target, db.Query{}, db.NewPage(previewPageSize, 0))
	if err != nil {
		m.message = err.Error()
		return
	}
	m.target = target
	m.result = res
	m.resultView.Reset()
	m.page = PageData
	m.workspaceMode = workspacePreview
	m.openDataWorkspaceTab(target, res, workspacePreview)
	if tab := m.activeWorkspaceTab(); tab != nil {
		tab.PreviewOffset = 0
		tab.PreviewHasMore = res.HasMore
	}
	m.message = "opened " + target.Name
}

func (m *Model) openMetadata(ctx context.Context, target db.Target) {
	provider, ok := m.adapter.(db.MetadataProvider)
	if !ok {
		m.message = "metadata not available"
		return
	}
	ctx, cancel := m.dbContext(ctx)
	defer cancel()
	metadata, err := provider.Metadata(ctx, target)
	if err != nil {
		m.message = err.Error()
		return
	}
	m.target = target
	m.result = metadataResult(metadata)
	m.resultView.Reset()
	m.page = PageData
	m.workspaceMode = workspaceMetadata
	m.openDataWorkspaceTab(target, m.result, workspaceMetadata)
	m.ensureNavigationState()
	m.expandedMeta[targetKey(target)] = true
	m.message = "metadata " + target.Name
}

func metadataResult(metadata db.ObjectMetadata) result.Set {
	rows := []result.Row{}
	for _, field := range metadata.Fields {
		nullable := "NO"
		if field.Nullable {
			nullable = "YES"
		}
		rows = append(rows, result.Row{Values: []any{"field", field.Name, field.Type, nullable, field.Default}})
	}
	for _, index := range metadata.Indexes {
		unique := "NO"
		if index.Unique {
			unique = "YES"
		}
		rows = append(rows, result.Row{Values: []any{"index", index.Name, strings.Join(index.Columns, ","), unique, ""}})
	}
	if len(metadata.Attributes) > 0 {
		keys := make([]string, 0, len(metadata.Attributes))
		for key := range metadata.Attributes {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			rows = append(rows, result.Row{Values: []any{"attribute", key, metadata.Attributes[key], "", ""}})
		}
	}
	return result.Set{Table: &result.Table{
		Columns: []result.Column{
			{Name: "kind"},
			{Name: "name"},
			{Name: "value"},
			{Name: "flag"},
			{Name: "default"},
		},
		Rows: rows,
	}}
}
