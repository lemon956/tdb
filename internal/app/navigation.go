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
	navNodeCatalog    navNodeKind = "catalog"
	navNodeDatabase   navNodeKind = "database"
	navNodeObject     navNodeKind = "object"
	navNodeMeta       navNodeKind = "metadata"
)

type navNode struct {
	ID          string
	Kind        navNodeKind
	Label       string
	Catalog     string
	Database    string
	Object      db.Object
	Target      db.Target
	Depth       int
	CatalogIdx  int
	DatabaseIdx int
	ObjectIdx   int
}

// scopeKey is the map key for per-(catalog,database) caches. For the internal
// catalog (and non-Doris drivers) it degrades to the bare database name, so
// existing two-level callers/behaviour are unchanged.
func (m *Model) scopeKey(catalog, database string) string {
	if catalog == "" || strings.EqualFold(catalog, "internal") {
		return database
	}
	return catalog + "\x00" + database
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
	if m.expandedCatalogs == nil {
		m.expandedCatalogs = map[string]bool{}
	}
	if m.catalogDatabases == nil {
		m.catalogDatabases = map[string][]string{}
	}
	if m.selectedDB != "" && len(m.objects) > 0 {
		key := m.scopeKey(m.selectedCatalog, m.selectedDB)
		m.databaseObjects[key] = m.objects
		if _, known := m.expandedDBs[key]; !known {
			m.expandedDBs[key] = true
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
	externals := m.externalCatalogs()
	if len(m.databases) == 0 && len(m.objects) > 0 && len(externals) == 0 {
		for objectIndex, object := range m.objects {
			target := m.targetForObject("", m.selectedDB, object)
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
	// The built-in "internal" catalog is flattened: its databases sit at the top
	// level exactly as before catalogs existed. m.databases always holds the
	// internal catalog's databases. Only EXTERNAL catalogs (jdbc/hive/es) get a
	// collapsible catalog group node, so connections without external catalogs
	// look identical to the old two-level tree.
	dbIndex := 0
	for localIdx, database := range m.databases {
		nodes = m.appendDatabaseNode(nodes, "", database, dbIndex, localIdx, dbDepth, &objectCount)
		dbIndex++
	}
	for catIndex, catalog := range externals {
		expanded := m.expandedCatalogs[catalog]
		marker := m.icons.Collapsed
		if expanded {
			marker = m.icons.Expanded
		}
		nodes = append(nodes, navNode{
			ID:         fmt.Sprintf("catalog:%d", catIndex),
			Kind:       navNodeCatalog,
			Label:      fmt.Sprintf("%s %s %s", marker, m.icons.Catalog, catalog),
			Catalog:    catalog,
			Depth:      dbDepth,
			CatalogIdx: catIndex,
		})
		if !expanded {
			continue
		}
		for localIdx, database := range m.catalogDatabases[catalog] {
			nodes = m.appendDatabaseNode(nodes, catalog, database, dbIndex, localIdx, dbDepth+1, &objectCount)
			dbIndex++
		}
	}
	return nodes
}

// externalCatalogs returns the non-internal Doris catalogs. The internal catalog
// is flattened into the top-level database list, so only these get a group node.
func (m *Model) externalCatalogs() []string {
	if len(m.catalogs) == 0 {
		return nil
	}
	out := make([]string, 0, len(m.catalogs))
	for _, catalog := range m.catalogs {
		if !strings.EqualFold(catalog, "internal") {
			out = append(out, catalog)
		}
	}
	return out
}

// appendDatabaseNode emits a database node and, when expanded, its object and
// metadata children. catalog is "" for the flat (non-catalog) layout. dbIndex is
// a tree-global database counter (for unique node IDs); localIdx is the index of
// the database within its own list (catalog's or the flat list).
func (m *Model) appendDatabaseNode(nodes []navNode, catalog, database string, dbIndex, localIdx, depth int, objectCount *int) []navNode {
	key := m.scopeKey(catalog, database)
	expanded := m.expandedDBs[key]
	marker := m.icons.Collapsed
	if expanded {
		marker = m.icons.Expanded
	}
	nodes = append(nodes, navNode{
		ID:          fmt.Sprintf("database:%d", dbIndex),
		Kind:        navNodeDatabase,
		Label:       fmt.Sprintf("%s %s %s", marker, m.icons.Database, database),
		Catalog:     catalog,
		Database:    database,
		Depth:       depth,
		DatabaseIdx: localIdx,
	})
	if !expanded {
		return nodes
	}
	for objectIndex, object := range m.databaseObjects[key] {
		target := m.targetForObject(catalog, database, object)
		nodes = append(nodes, navNode{
			ID:          fmt.Sprintf("object:%d", *objectCount),
			Kind:        navNodeObject,
			Label:       m.objectNodeLabel(object),
			Catalog:     catalog,
			Database:    database,
			Object:      object,
			Target:      target,
			Depth:       depth + 1,
			DatabaseIdx: localIdx,
			ObjectIdx:   objectIndex,
		})
		*objectCount++
		if m.expandedMeta[targetKey(target)] {
			for _, label := range []string{"metadata", "fields", "indexes"} {
				nodes = append(nodes, navNode{ID: fmt.Sprintf("%s:%d", label, *objectCount), Kind: navNodeMeta, Label: m.icons.Metadata + " " + label, Catalog: catalog, Database: database, Target: target, Depth: depth + 2, DatabaseIdx: localIdx, ObjectIdx: objectIndex})
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
	Catalog  string
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
	if match.Catalog != "" {
		m.expandedCatalogs[match.Catalog] = true
	}
	if match.Kind == navNodeObject {
		m.expandedDBs[m.scopeKey(match.Catalog, match.Database)] = true
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
	if len(m.catalogs) > 0 {
		for _, catalog := range m.catalogs {
			if strings.Contains(strings.ToLower(catalog), query) {
				matches = append(matches, navSearchMatch{Kind: navNodeCatalog, Catalog: catalog})
			}
			for _, database := range m.catalogDatabases[catalog] {
				matches = m.appendDatabaseSearchMatches(matches, catalog, database, query)
			}
		}
		return matches
	}
	for _, database := range m.databases {
		matches = m.appendDatabaseSearchMatches(matches, "", database, query)
	}
	return matches
}

func (m *Model) appendDatabaseSearchMatches(matches []navSearchMatch, catalog, database, query string) []navSearchMatch {
	if strings.Contains(strings.ToLower(database), query) {
		matches = append(matches, navSearchMatch{Kind: navNodeDatabase, Catalog: catalog, Database: database})
	}
	for _, object := range m.databaseObjects[m.scopeKey(catalog, database)] {
		haystack := strings.ToLower(database + " " + object.Name + " " + string(object.Type))
		if strings.Contains(haystack, query) {
			matches = append(matches, navSearchMatch{Kind: navNodeObject, Catalog: catalog, Database: database, Object: object})
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
		case navNodeCatalog:
			if node.Kind == navNodeCatalog && node.Catalog == match.Catalog {
				m.browserCursor = i
				m.syncBrowserSelectionFromCursor()
				return true
			}
		case navNodeDatabase:
			if node.Kind == navNodeDatabase && node.Catalog == match.Catalog && node.Database == match.Database {
				m.browserCursor = i
				m.syncBrowserSelectionFromCursor()
				return true
			}
		case navNodeObject:
			if node.Kind == navNodeObject && node.Catalog == match.Catalog && node.Database == match.Database && node.Object.Name == match.Object.Name && node.Object.Type == match.Object.Type {
				m.browserCursor = i
				m.syncBrowserSelectionFromCursor()
				return true
			}
		}
	}
	return false
}

// highlightSidebarForActiveTab moves the sidebar cursor to the database the
// active workspace tab belongs to, so the left tree highlight follows the page
// opened on the right. For data tabs it prefers the matching object node (when
// the database is expanded) and falls back to the database node otherwise. It is
// a no-op for drivers without databases (e.g. Redis, where the tab has no
// database). Call this from tab switch/open events — never from
// syncActiveTabState/bindActiveQueryTabToSelection, which would yank the cursor
// away while the user navigates the sidebar.
func (m *Model) highlightSidebarForActiveTab() {
	tab := m.activeWorkspaceTab()
	if tab == nil {
		return
	}
	var cat, dbName, object string
	switch tab.Kind {
	case workspaceTabData:
		cat = tab.Target.Catalog
		dbName = tab.Target.Database
		object = tab.Target.Name
	case workspaceTabQuery:
		cat = tab.QueryCatalog
		dbName = tab.QueryDatabase
	}
	if dbName == "" {
		return
	}
	m.selectedDB = dbName
	m.selectedCatalog = cat
	key := m.scopeKey(cat, dbName)
	// Align m.objects with the new selectedDB *before* any ensureNavigationState
	// runs (ensureQueryObjectsLoaded triggers one): that heuristic binds m.objects
	// to selectedDB's key, so a stale previous-database m.objects would otherwise be
	// mis-bound here. nil is fine — the len>0 guard then skips the bind.
	m.objects = m.databaseObjects[key]
	// Load the target database's objects (once) and auto-expand it so the followed
	// database reveals its collections/tables.
	m.ensureQueryObjectsLoaded(cat, dbName)
	m.objects = m.databaseObjects[key]
	m.expandedDBs[key] = true
	nodes := m.browserNodes()
	if object != "" {
		for i, node := range nodes {
			if node.Kind == navNodeObject && node.Catalog == cat && node.Database == dbName && node.Object.Name == object {
				m.browserCursor = i
				m.syncBrowserSelectionFromCursor()
				return
			}
		}
	}
	for i, node := range nodes {
		if node.Kind == navNodeDatabase && node.Catalog == cat && node.Database == dbName {
			m.browserCursor = i
			m.syncBrowserSelectionFromCursor()
			return
		}
	}
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

func (m *Model) targetForObject(catalog, database string, object db.Object) db.Target {
	targetType := object.Type
	if targetType == "" {
		targetType = db.ObjectTable
	}
	target := db.Target{Catalog: normalizedCatalog(catalog), Database: database, Name: object.Name, Type: targetType}
	if target.Type == db.ObjectKey {
		target.Database = ""
	}
	return target
}

// normalizedCatalog collapses the built-in catalog to "" so targets in the
// internal catalog (and non-Doris drivers) compare/key identically to the old
// two-level world.
func normalizedCatalog(catalog string) string {
	if strings.EqualFold(catalog, "internal") {
		return ""
	}
	return catalog
}

func targetKey(target db.Target) string {
	return target.String() + "|" + string(target.Type)
}

func sameTarget(left, right db.Target) bool {
	return left.Catalog == right.Catalog &&
		left.Database == right.Database &&
		left.Schema == right.Schema &&
		left.Name == right.Name &&
		left.Type == right.Type
}

func (m *Model) loadObjectsForDatabase(ctx context.Context, catalog, database string) error {
	if m.adapter == nil {
		return fmt.Errorf("no active connection")
	}
	ctx, cancel := m.dbContext(ctx)
	defer cancel()
	objects, err := m.adapter.ListObjects(ctx, db.Scope{Catalog: normalizedCatalog(catalog), Database: database})
	if err != nil {
		return err
	}
	m.ensureNavigationState()
	key := m.scopeKey(catalog, database)
	m.databaseObjects[key] = objects
	if m.selectedDB == database && m.selectedCatalog == catalog {
		m.objects = objects
		m.objectIndex = 0
	}
	return nil
}

// toggleCatalog expands/collapses a Doris catalog, lazily loading its databases
// on first expand. A permission error is swallowed (the catalog stays empty)
// rather than breaking the rest of the tree.
func (m *Model) toggleCatalog(ctx context.Context, catalog string) {
	m.ensureNavigationState()
	if m.expandedCatalogs[catalog] {
		m.expandedCatalogs[catalog] = false
		m.message = "collapsed " + catalog
		return
	}
	if _, loaded := m.catalogDatabases[catalog]; !loaded {
		provider, ok := m.adapter.(db.CatalogProvider)
		if !ok {
			m.message = "catalogs not supported"
			return
		}
		dctx, cancel := m.dbContext(ctx)
		databases, err := provider.ListDatabasesInCatalog(dctx, catalog)
		cancel()
		if err != nil {
			if isPermissionError(err) {
				m.catalogDatabases[catalog] = []string{}
				m.expandedCatalogs[catalog] = true
				m.message = "no access to catalog " + catalog
				return
			}
			m.message = err.Error()
			return
		}
		m.catalogDatabases[catalog] = databases
	}
	// m.databases stays the internal catalog's flat list; an external catalog's
	// databases live only in catalogDatabases[catalog]. Only track selection.
	m.selectedCatalog = catalog
	m.expandedCatalogs[catalog] = true
	m.bindActiveQueryTabToSelection()
	m.message = "expanded " + catalog
}

func (m *Model) toggleDatabase(ctx context.Context, catalog, database string) {
	if database == "" {
		m.message = "no database available"
		return
	}
	m.ensureNavigationState()
	key := m.scopeKey(catalog, database)
	if m.expandedDBs[key] {
		m.expandedDBs[key] = false
		m.message = "collapsed " + database
		return
	}
	// m.databases is left as the internal catalog's flat list; only update the
	// active selection so query context follows the sidebar.
	m.selectedCatalog = catalog
	m.selectedDB = database
	if err := m.loadObjectsForDatabase(ctx, catalog, database); err != nil {
		if isPermissionError(err) {
			m.databaseObjects[key] = []db.Object{}
			m.expandedDBs[key] = true
			m.bindActiveQueryTabToSelection()
			m.message = "no access to " + database
			return
		}
		m.message = err.Error()
		return
	}
	m.expandedDBs[key] = true
	m.objects = m.databaseObjects[key]
	m.objectIndex = 0
	m.bindActiveQueryTabToSelection()
	m.message = "expanded " + database
}

// bindActiveQueryTabToSelection makes the active query tab follow the sidebar's
// current catalog/database — unless the user pinned it via the database picker.
// Called on activation (expand/open), not on cursor movement.
func (m *Model) bindActiveQueryTabToSelection() {
	tab := m.activeWorkspaceTab()
	if tab == nil || tab.Kind != workspaceTabQuery || tab.QueryDatabasePinned {
		return
	}
	if m.selectedDB == "" {
		return
	}
	tab.QueryCatalog = m.selectedCatalog
	tab.QueryDatabase = m.selectedDB
	m.ensureQueryObjectsLoaded(m.selectedCatalog, m.selectedDB)
	m.syncActiveTabState()
}

// isPermissionError reports whether an error is a privilege/access denial, so
// the UI can silently skip the object instead of surfacing a red error.
func isPermissionError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "access denied") ||
		strings.Contains(msg, "denied") ||
		strings.Contains(msg, "permission")
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
		rows = append(rows, result.Row{Values: []any{"field", field.Name, field.Type, nullable, field.Default, field.Comment}})
	}
	for _, index := range metadata.Indexes {
		unique := "NO"
		if index.Unique {
			unique = "YES"
		}
		rows = append(rows, result.Row{Values: []any{"index", index.Name, strings.Join(index.Columns, ","), unique, "", ""}})
	}
	if len(metadata.Attributes) > 0 {
		keys := make([]string, 0, len(metadata.Attributes))
		for key := range metadata.Attributes {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			rows = append(rows, result.Row{Values: []any{"attribute", key, metadata.Attributes[key], "", "", ""}})
		}
	}
	return result.Set{Table: &result.Table{
		Columns: []result.Column{
			{Name: "kind"},
			{Name: "name"},
			{Name: "value"},
			{Name: "flag"},
			{Name: "default"},
			{Name: "comment"},
		},
		Rows: rows,
	}}
}
