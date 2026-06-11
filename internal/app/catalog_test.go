package app

import (
	"context"
	"errors"
	"testing"

	"tdb/internal/config"
	"tdb/internal/db"
	"tdb/internal/result"
)

// catalogAdapter is a db.Adapter + db.CatalogProvider double that records the
// scopes it is asked about and returns canned catalogs/databases/objects.
type catalogAdapter struct {
	catalogs     []string
	databases    map[string][]string // catalog -> databases
	objects      map[string][]db.Object
	dbErr        map[string]error // catalog -> error from ListDatabasesInCatalog
	lastObjScope db.Scope
}

func (a *catalogAdapter) Test(context.Context) error { return nil }
func (a *catalogAdapter) ListDatabases(context.Context) ([]string, error) {
	return a.databases["internal"], nil
}
func (a *catalogAdapter) ListObjects(_ context.Context, scope db.Scope) ([]db.Object, error) {
	a.lastObjScope = scope
	key := scope.Catalog
	if key == "" {
		key = "internal"
	}
	return a.objects[key+"."+scope.Database], nil
}
func (a *catalogAdapter) ListCatalogs(context.Context) ([]string, error) { return a.catalogs, nil }
func (a *catalogAdapter) ListDatabasesInCatalog(_ context.Context, catalog string) ([]string, error) {
	if a.dbErr != nil {
		if err, ok := a.dbErr[catalog]; ok {
			return nil, err
		}
	}
	return a.databases[catalog], nil
}
func (a *catalogAdapter) Preview(context.Context, db.Target, db.Query, db.Page) (result.Set, error) {
	return result.Set{}, nil
}
func (a *catalogAdapter) Insert(context.Context, db.Target, map[string]any) (result.MutationResult, error) {
	return result.MutationResult{}, nil
}
func (a *catalogAdapter) Update(context.Context, db.Target, db.Key, map[string]any) (result.MutationResult, error) {
	return result.MutationResult{}, nil
}
func (a *catalogAdapter) Delete(context.Context, db.Target, db.Key) (result.MutationResult, error) {
	return result.MutationResult{}, nil
}
func (a *catalogAdapter) Execute(context.Context, db.Command) (result.Set, error) {
	return result.Set{}, nil
}
func (a *catalogAdapter) Close() error { return nil }

func newCatalogModel(t *testing.T) (*Model, *catalogAdapter) {
	t.Helper()
	m := newWorkspaceVimModel(t)
	m.activeProfile = &config.Profile{ID: "doris", Driver: config.DriverDoris}
	adapter := &catalogAdapter{
		catalogs:  []string{"internal", "jdbc_mysql"},
		databases: map[string][]string{"internal": {"app"}, "jdbc_mysql": {"dw", "ads"}},
		objects: map[string][]db.Object{
			"jdbc_mysql.dw": {{Name: "users", Type: db.ObjectTable}},
		},
	}
	m.adapter = adapter
	return m, adapter
}

func nodeKinds(nodes []navNode) []navNodeKind {
	kinds := make([]navNodeKind, len(nodes))
	for i, n := range nodes {
		kinds[i] = n.Kind
	}
	return kinds
}

// applyRefreshBrowser with no catalogs keeps the flat two-level tree (MySQL).
func TestBrowserStaysTwoLevelWithoutCatalogs(t *testing.T) {
	m := newWorkspaceVimModel(t)
	m.activeProfile = &config.Profile{ID: "mysql", Driver: config.DriverMySQL}
	m.applyRefreshBrowser(nil, "", []string{"app", "sys"}, nil, "", nil, nil)

	if len(m.catalogs) != 0 {
		t.Fatalf("MySQL must not gain a catalog layer, got %v", m.catalogs)
	}
	for _, n := range m.browserNodes() {
		if n.Kind == navNodeCatalog {
			t.Fatal("no catalog nodes should appear without catalogs")
		}
	}
}

// applyRefreshBrowser with catalogs flattens the internal catalog: its databases
// sit at the top level (no "internal" node), and the external catalog appears as
// a group node below them.
func TestApplyRefreshBrowserCatalogMode(t *testing.T) {
	m, _ := newCatalogModel(t)
	m.applyRefreshBrowser([]string{"internal", "jdbc_mysql"}, "internal", []string{"app"}, nil, "", nil, nil)

	// The internal catalog is flattened, so the active catalog normalizes to "".
	if m.selectedCatalog != "" {
		t.Fatalf("flattened internal should normalize the catalog to empty, got %q", m.selectedCatalog)
	}
	if got := m.databases; len(got) != 1 || got[0] != "app" {
		t.Fatalf("internal databases should be the flat top-level list, got %v", got)
	}
	nodes := m.browserNodes()
	// Below the connection root: a flat database node (internal "app"), then the
	// external jdbc_mysql catalog group.
	if len(nodes) < 3 {
		t.Fatalf("expected database + external catalog nodes, got %v", nodeKinds(nodes))
	}
	if nodes[1].Kind != navNodeDatabase || nodes[1].Database != "app" || nodes[1].Catalog != "" {
		t.Fatalf("internal db should be a flat top-level node, got %+v", nodes[1])
	}
	var sawExternal bool
	for _, n := range nodes {
		if n.Kind == navNodeCatalog {
			if n.Catalog != "jdbc_mysql" {
				t.Fatalf("only external catalogs should be group nodes, got %q", n.Catalog)
			}
			sawExternal = true
		}
	}
	if !sawExternal {
		t.Fatal("the external jdbc_mysql catalog should appear as a group node")
	}
}

// Without any external catalog (only internal), the tree is exactly two-level —
// no catalog group nodes at all.
func TestInternalOnlyStaysFlat(t *testing.T) {
	m, _ := newCatalogModel(t)
	m.applyRefreshBrowser([]string{"internal"}, "internal", []string{"app", "sys"}, nil, "", nil, nil)

	for _, n := range m.browserNodes() {
		if n.Kind == navNodeCatalog {
			t.Fatalf("internal-only Doris must not show any catalog node, got %v", nodeKinds(m.browserNodes()))
		}
	}
	if len(m.externalCatalogs()) != 0 {
		t.Fatalf("no external catalogs expected, got %v", m.externalCatalogs())
	}
}

// The three-level tree renders catalog -> database -> object with correct depth
// and a catalog-qualified target.
func TestBrowserNodesThreeLevel(t *testing.T) {
	m, _ := newCatalogModel(t)
	m.catalogs = []string{"internal", "jdbc_mysql"}
	m.expandedCatalogs = map[string]bool{"jdbc_mysql": true}
	m.catalogDatabases = map[string][]string{"jdbc_mysql": {"dw"}}
	m.expandedDBs = map[string]bool{m.scopeKey("jdbc_mysql", "dw"): true}
	m.databaseObjects = map[string][]db.Object{
		m.scopeKey("jdbc_mysql", "dw"): {{Name: "users", Type: db.ObjectTable}},
	}

	var cat, database, object *navNode
	for i := range m.browserNodes() {
		n := m.browserNodes()[i]
		switch {
		case n.Kind == navNodeCatalog && n.Catalog == "jdbc_mysql":
			c := n
			cat = &c
		case n.Kind == navNodeDatabase && n.Database == "dw":
			d := n
			database = &d
		case n.Kind == navNodeObject && n.Object.Name == "users":
			o := n
			object = &o
		}
	}
	if cat == nil || database == nil || object == nil {
		t.Fatalf("missing a tree level: cat=%v db=%v obj=%v", cat, database, object)
	}
	if database.Depth != cat.Depth+1 || object.Depth != database.Depth+1 {
		t.Fatalf("depths not nested: cat=%d db=%d obj=%d", cat.Depth, database.Depth, object.Depth)
	}
	if object.Catalog != "jdbc_mysql" || object.Target.Catalog != "jdbc_mysql" {
		t.Fatalf("object target should carry the catalog, got %+v", object.Target)
	}
}

// Expanding a catalog lazily loads its databases via the provider; expanding a
// database loads its objects with the catalog-scoped Scope.
func TestToggleCatalogAndDatabaseLazyLoad(t *testing.T) {
	m, adapter := newCatalogModel(t)
	m.catalogs = adapter.catalogs

	m.toggleCatalog(context.Background(), "jdbc_mysql")
	if !m.expandedCatalogs["jdbc_mysql"] {
		t.Fatal("catalog should be expanded")
	}
	if got := m.catalogDatabases["jdbc_mysql"]; len(got) != 2 {
		t.Fatalf("catalog databases should be lazily loaded, got %v", got)
	}
	if m.selectedCatalog != "jdbc_mysql" {
		t.Fatalf("expanding a catalog selects it, got %q", m.selectedCatalog)
	}

	m.toggleDatabase(context.Background(), "jdbc_mysql", "dw")
	if adapter.lastObjScope.Catalog != "jdbc_mysql" || adapter.lastObjScope.Database != "dw" {
		t.Fatalf("ListObjects should be scoped to the catalog, got %+v", adapter.lastObjScope)
	}
	if got := m.databaseObjects[m.scopeKey("jdbc_mysql", "dw")]; len(got) != 1 {
		t.Fatalf("objects should be cached under the scoped key, got %v", got)
	}
}

// A permission error while loading a catalog's databases is swallowed: the tree
// stays usable and a non-blocking message is shown.
func TestToggleCatalogPermissionDenied(t *testing.T) {
	m, adapter := newCatalogModel(t)
	m.catalogs = adapter.catalogs
	adapter.dbErr = map[string]error{"jdbc_mysql": errors.New("ERROR 1227 (42000): Access denied; you need the privilege")}

	m.toggleCatalog(context.Background(), "jdbc_mysql")

	if !m.expandedCatalogs["jdbc_mysql"] {
		t.Fatal("a denied catalog should still expand (to an empty list), not stay broken")
	}
	if got, ok := m.catalogDatabases["jdbc_mysql"]; !ok || len(got) != 0 {
		t.Fatalf("a denied catalog should map to an empty database list, got %v ok=%v", got, ok)
	}
	if m.message == "" {
		t.Fatal("a non-blocking message should explain the lack of access")
	}
	// Other catalogs remain browsable.
	m.toggleCatalog(context.Background(), "internal")
	if len(m.catalogDatabases["internal"]) == 0 {
		t.Fatal("an accessible catalog must still load after a denied one")
	}
}

// The active (unpinned) query tab follows the sidebar's catalog/database; a
// pinned tab keeps its database.
func TestQueryTabAutoFollowsSidebar(t *testing.T) {
	m, _ := newCatalogModel(t)
	m.databases = []string{"app"}
	m.openQueryWorkspaceTab()

	// Expanding an internal database binds the active query tab to it.
	m.toggleDatabase(context.Background(), "", "app")
	if tab := m.activeWorkspaceTab(); tab == nil || tab.QueryDatabase != "app" || tab.QueryCatalog != "" {
		t.Fatalf("query tab should follow internal db, got %+v", tab)
	}

	// Selecting an external catalog database carries the catalog too.
	m.toggleDatabase(context.Background(), "jdbc_mysql", "dw")
	if tab := m.activeWorkspaceTab(); tab.QueryDatabase != "dw" || tab.QueryCatalog != "jdbc_mysql" {
		t.Fatalf("query tab should follow external catalog db, got %+v", m.activeWorkspaceTab())
	}

	// Once pinned, sidebar navigation no longer rebinds the tab.
	m.activeWorkspaceTab().QueryDatabasePinned = true
	m.toggleDatabase(context.Background(), "", "app")
	if tab := m.activeWorkspaceTab(); tab.QueryDatabase != "dw" || tab.QueryCatalog != "jdbc_mysql" {
		t.Fatalf("a pinned tab must not be rebound, got %+v", m.activeWorkspaceTab())
	}
}

// The database indicator shows catalog.database only for external catalogs.
func TestWorkspaceDatabaseNameCatalogAware(t *testing.T) {
	m, _ := newCatalogModel(t)
	m.openQueryWorkspaceTab()
	tab := m.activeWorkspaceTab()

	tab.QueryCatalog, tab.QueryDatabase = "", "dw"
	if got := m.workspaceDatabaseName(); got != "dw" {
		t.Fatalf("internal db should show bare name, got %q", got)
	}
	tab.QueryCatalog, tab.QueryDatabase = "jdbc_mysql", "dw"
	if got := m.workspaceDatabaseName(); got != "jdbc_mysql.dw" {
		t.Fatalf("external db should show catalog.database, got %q", got)
	}
}

// Per-connection snapshots preserve the catalog state across connection switches.
func TestSessionRoundTripPreservesCatalogState(t *testing.T) {
	m, _ := newCatalogModel(t)
	m.catalogs = []string{"internal", "jdbc_mysql"}
	m.selectedCatalog = "jdbc_mysql"
	m.expandedCatalogs = map[string]bool{"jdbc_mysql": true}
	m.catalogDatabases = map[string][]string{"jdbc_mysql": {"dw", "ads"}}

	snap := m.captureSession()
	// Wipe live state, then restore.
	m.catalogs = nil
	m.selectedCatalog = ""
	m.expandedCatalogs = map[string]bool{}
	m.catalogDatabases = map[string][]string{}
	m.sessions = []connSession{snap}
	m.activeSession = -1
	m.loadSession(0)

	if len(m.catalogs) != 2 || m.selectedCatalog != "jdbc_mysql" {
		t.Fatalf("catalog identity not restored: %v / %q", m.catalogs, m.selectedCatalog)
	}
	if !m.expandedCatalogs["jdbc_mysql"] || len(m.catalogDatabases["jdbc_mysql"]) != 2 {
		t.Fatalf("catalog expansion/databases not restored: %v / %v", m.expandedCatalogs, m.catalogDatabases)
	}
}
