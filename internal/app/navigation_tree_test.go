package app

import (
	"context"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"tdb/internal/config"
	"tdb/internal/db"
	"tdb/internal/result"
)

func TestNavigationDatabaseHighlightDoesNotRewriteWorkspace(t *testing.T) {
	model := newNavigationTreeModel(t)
	model.databases = []string{"app", "admin"}

	before := model.browserWorkspaceContent()
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyDown})
	after := updated.(*Model).browserWorkspaceContent()

	if after != before {
		t.Fatalf("workspace changed after database highlight\nbefore: %q\nafter:  %q", before, after)
	}
	if strings.Contains(after, "admin") {
		t.Fatalf("workspace = %q, should not show highlighted database name", after)
	}
}

func TestNavigationEnterTogglesDatabaseTree(t *testing.T) {
	model := newNavigationTreeModel(t)
	model.databases = []string{"app"}
	model.adapter = &navigationTreeAdapter{
		objectsByDB: map[string][]db.Object{
			"app": {{Name: "users", Type: db.ObjectTable}},
		},
	}

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(*Model)
	list := got.browserList()

	if !strings.Contains(list, "▾ ◆ app") {
		t.Fatalf("browser list = %q, want expanded database marker", list)
	}
	if !strings.Contains(list, "    ◇ users [table]") {
		t.Fatalf("browser list = %q, want child object under expanded database", list)
	}

	updated, _ = got.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got = updated.(*Model)
	list = got.browserList()
	if !strings.Contains(list, "▸ ◆ app") || strings.Contains(list, "users [table]") {
		t.Fatalf("browser list after collapse = %q, want collapsed database without object", list)
	}
}

func TestNavigationObjectEnterPreviewsThenShowsMetadata(t *testing.T) {
	adapter := &navigationTreeAdapter{
		objectsByDB: map[string][]db.Object{
			"app": {{Name: "users", Type: db.ObjectTable}},
		},
		metadata: db.ObjectMetadata{
			Fields:  []db.MetadataField{{Name: "id", Type: "int", Nullable: false}},
			Indexes: []db.MetadataIndex{{Name: "PRIMARY", Columns: []string{"id"}, Unique: true}},
		},
	}
	model := newNavigationTreeModel(t)
	model.databases = []string{"app"}
	model.adapter = adapter

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated, _ = updated.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated, _ = updated.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(*Model)
	if got.page != PageData || got.target.Name != "users" || adapter.previewCalls != 1 {
		t.Fatalf("page/target/previewCalls = %s/%s/%d, want data/users/1", got.page, got.target.Name, adapter.previewCalls)
	}

	updated, _ = got.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got = updated.(*Model)
	if got.workspaceMode != workspaceMetadata || adapter.metadataCalls != 1 {
		t.Fatalf("workspaceMode/metadataCalls = %s/%d, want metadata/1", got.workspaceMode, adapter.metadataCalls)
	}
	if got.result.Table == nil || got.result.Table.Rows[0].Values[0] != "field" || got.result.Table.Rows[1].Values[0] != "index" {
		t.Fatalf("metadata result = %+v, want field and index rows", got.result.Table)
	}
	if !strings.Contains(got.browserList(), "▪ indexes") {
		t.Fatalf("browser list = %q, want metadata subtree", got.browserList())
	}
}

func TestNavigationMouseClickSelectsAndDoubleClickExpandsDatabase(t *testing.T) {
	model := newNavigationTreeModel(t)
	model.width = 100
	model.height = 28
	model.databases = []string{"app"}
	model.adapter = &navigationTreeAdapter{
		objectsByDB: map[string][]db.Object{
			"app": {{Name: "users", Type: db.ObjectTable}},
		},
	}

	_ = model.View()
	hit := findRequiredHitbox(t, model.hitboxes, "database:0")
	updated, _ := model.Update(tea.MouseMsg{X: hit.X, Y: hit.Y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	got := updated.(*Model)
	if got.selectedDB != "" || len(got.objects) != 0 {
		t.Fatalf("single click selectedDB/objects = %q/%+v, want highlight only", got.selectedDB, got.objects)
	}

	updated, _ = got.Update(tea.MouseMsg{X: hit.X, Y: hit.Y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	got = updated.(*Model)
	if got.selectedDB != "app" || len(got.objects) != 1 {
		t.Fatalf("double click selectedDB/objects = %q/%+v, want expanded app/users", got.selectedDB, got.objects)
	}
}

func TestNavigationRepeatedClickOnHighlightedRowOpensWithoutDoubleClickTiming(t *testing.T) {
	model := newNavigationTreeModel(t)
	model.width = 100
	model.height = 28
	model.databases = []string{"app"}
	model.adapter = &navigationTreeAdapter{
		objectsByDB: map[string][]db.Object{
			"app": {{Name: "users", Type: db.ObjectCollection}},
		},
	}

	_ = model.View()
	hit := findRequiredHitbox(t, model.hitboxes, "database:0")
	updated, _ := model.Update(tea.MouseMsg{X: hit.X, Y: hit.Y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	model = updated.(*Model)
	model.lastClickAt = time.Now().Add(-time.Second)

	updated, _ = model.Update(tea.MouseMsg{X: hit.X, Y: hit.Y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	got := updated.(*Model)

	if got.selectedDB != "app" || len(got.objects) != 1 {
		t.Fatalf("second click selectedDB/objects = %q/%+v, want expanded app/users", got.selectedDB, got.objects)
	}
}

func TestNavigationHorizontalScrollShortcuts(t *testing.T) {
	model := newNavigationTreeModel(t)
	model.focus = FocusSidebar

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyShiftRight})
	model = updated.(*Model)
	if model.navHorizontalOffset == 0 {
		t.Fatal("shift+right did not increase horizontal offset")
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyShiftLeft})
	model = updated.(*Model)
	if model.navHorizontalOffset != 0 {
		t.Fatalf("shift+left offset = %d, want 0", model.navHorizontalOffset)
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'L'}})
	model = updated.(*Model)
	if model.navHorizontalOffset == 0 {
		t.Fatal("L did not increase horizontal offset")
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'H'}})
	model = updated.(*Model)
	if model.navHorizontalOffset != 0 {
		t.Fatalf("H offset = %d, want 0", model.navHorizontalOffset)
	}
}

func TestNavigationViewRegistersOnlyVisibleNodeHitboxes(t *testing.T) {
	model := newNavigationTreeModel(t)
	model.width = 100
	model.height = 20
	model.databases = []string{"app"}
	model.selectedDB = "app"
	model.expandedDBs = map[string]bool{"app": true}
	objects := make([]db.Object, 0, 5000)
	for i := 0; i < 5000; i++ {
		objects = append(objects, db.Object{Name: "table_" + strconv.Itoa(i), Type: db.ObjectTable})
	}
	model.objects = objects
	model.databaseObjects = map[string][]db.Object{"app": objects}

	_ = model.View()

	nodeHitboxes := 0
	for _, hit := range model.hitboxes {
		if strings.HasPrefix(hit.ID, "database:") || strings.HasPrefix(hit.ID, "object:") {
			nodeHitboxes++
		}
		if hit.ID == "object:4999" {
			t.Fatalf("registered far off-screen hitbox: %+v", hit)
		}
	}
	if nodeHitboxes > 18 {
		t.Fatalf("node hitboxes = %d, want only visible rows", nodeHitboxes)
	}
}

func TestNavigationRendersConnectionDatabaseAndCollectionIcons(t *testing.T) {
	model := newNavigationTreeModel(t)
	model.activeProfile = &config.Profile{ID: "mongo-prod", Driver: config.DriverMongo}
	model.icons = IconSetForStyle(IconStyleUnicode)
	model.databases = []string{"app"}
	model.selectedDB = "app"
	model.expandedDBs = map[string]bool{"app": true}
	model.databaseObjects = map[string][]db.Object{
		"app": {{Name: "users", Type: db.ObjectCollection}},
	}

	got := stripANSI(model.browserList())

	if !strings.Contains(got, "🔌 mongo-prod") {
		t.Fatalf("navigation missing connection icon:\n%s", got)
	}
	if !strings.Contains(got, "  ▾ ◆ app") {
		t.Fatalf("navigation missing database icon or two-space indent:\n%s", got)
	}
	if !strings.Contains(got, "    ◇ users [collection]") {
		t.Fatalf("navigation missing collection icon or four-space indent:\n%s", got)
	}
}

func TestNavigationLongRowsAreClippedAndHorizontalScrollbarIsRendered(t *testing.T) {
	model := newNavigationTreeModel(t)
	model.activeProfile = &config.Profile{ID: "mongo-prod", Driver: config.DriverMongo}
	model.icons = IconSetForStyle(IconStyleUnicode)
	model.databases = []string{"app"}
	model.selectedDB = "app"
	model.expandedDBs = map[string]bool{"app": true}
	longName := "collection_with_a_name_that_is_far_too_long_for_the_sidebar_width"
	model.databaseObjects = map[string][]db.Object{
		"app": {{Name: longName, Type: db.ObjectCollection}},
	}
	model.navHorizontalOffset = 10

	got := stripANSI(model.browserListVisible(5, 24))

	if strings.Contains(got, longName) {
		t.Fatalf("navigation should clip long row instead of rendering full name:\n%s", got)
	}
	if !strings.Contains(got, "◄") || !strings.Contains(got, "►") {
		t.Fatalf("navigation missing horizontal scrollbar:\n%s", got)
	}
	for _, line := range strings.Split(strings.TrimRight(got, "\n"), "\n") {
		if width := lipgloss.Width(line); width > 24 {
			t.Fatalf("line width = %d, want <= 24: %q\nfull:\n%s", width, line, got)
		}
	}
}

func TestNavigationVerticalScrollbarIsRenderedForOverflow(t *testing.T) {
	model := newNavigationTreeModel(t)
	model.activeProfile = &config.Profile{ID: "mongo-prod", Driver: config.DriverMongo}
	model.icons = IconSetForStyle(IconStyleUnicode)
	model.databases = []string{"app"}
	model.selectedDB = "app"
	model.expandedDBs = map[string]bool{"app": true}
	objects := make([]db.Object, 0, 20)
	for i := 0; i < 20; i++ {
		objects = append(objects, db.Object{Name: "collection_" + strconv.Itoa(i), Type: db.ObjectCollection})
	}
	model.databaseObjects = map[string][]db.Object{"app": objects}
	model.browserCursor = 12

	got := stripANSI(model.browserListVisible(6, 28))

	if !strings.Contains(got, "┃") || !strings.Contains(got, "│") {
		t.Fatalf("navigation missing vertical scrollbar:\n%s", got)
	}
	if len(strings.Split(strings.TrimRight(got, "\n"), "\n")) != 6 {
		t.Fatalf("navigation visible line count mismatch:\n%s", got)
	}
}

func TestNavigationStrongCursorOnlyRendersWhenSidebarFocused(t *testing.T) {
	model := newNavigationTreeModel(t)
	model.activeProfile = &config.Profile{ID: "mongo-prod", Driver: config.DriverMongo}
	model.icons = IconSetForStyle(IconStyleUnicode)
	model.databases = []string{"app"}
	model.browserCursor = 1

	model.focus = FocusSidebar
	focused := stripANSI(model.browserList())
	if !strings.Contains(focused, ">   ▸ ◆ app") {
		t.Fatalf("focused navigation should show strong cursor:\n%s", focused)
	}

	model.focus = FocusMain
	unfocused := stripANSI(model.browserList())
	if strings.Contains(unfocused, ">") {
		t.Fatalf("unfocused navigation should not show strong cursor:\n%s", unfocused)
	}
	if !strings.Contains(unfocused, "▸ ◆ app") {
		t.Fatalf("unfocused navigation should keep weak selected row content:\n%s", unfocused)
	}
}

func TestNavigationSlashSearchJumpsWithoutFilteringTree(t *testing.T) {
	model := newNavigationTreeModel(t)
	model.activeProfile = &config.Profile{ID: "mongo-prod", Driver: config.DriverMongo}
	model.databases = []string{"app", "admin"}
	model.selectedDB = "app"
	model.expandedDBs = map[string]bool{"app": true, "admin": true}
	model.databaseObjects = map[string][]db.Object{
		"app":   {{Name: "users", Type: db.ObjectCollection}, {Name: "orders", Type: db.ObjectCollection}},
		"admin": {{Name: "system.profile", Type: db.ObjectCollection}},
	}

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	model = updated.(*Model)
	if !model.navSearchActive || model.focus != FocusCommand {
		t.Fatalf("search active/focus = %v/%s, want true/command", model.navSearchActive, model.focus)
	}
	if got := model.commandLineText(); !strings.HasPrefix(got, "Search:") {
		t.Fatalf("command line = %q, want Search label", got)
	}

	for _, r := range "ord" {
		updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		model = updated.(*Model)
	}
	got := stripANSI(model.browserList())
	for _, wanted := range []string{"mongo-prod", "app", "orders", "users", "admin", "system.profile"} {
		if !strings.Contains(got, wanted) {
			t.Fatalf("search should not filter %q:\n%s", wanted, got)
		}
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(*Model)
	node, ok := model.selectedBrowserNode()
	if !ok || node.Kind != navNodeObject || node.Object.Name != "orders" {
		t.Fatalf("selected after Enter = %+v/%v, want orders object", node, ok)
	}
	if model.focus != FocusSidebar {
		t.Fatalf("focus after search Enter = %s, want sidebar", model.focus)
	}

	model.navSearchQuery = "collection"
	model.navSearchMatchIndex = -1
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	model = updated.(*Model)
	first, _ := model.selectedBrowserNode()
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	model = updated.(*Model)
	second, _ := model.selectedBrowserNode()
	if first.Object.Name == second.Object.Name {
		t.Fatalf("n did not jump to next match: first=%+v second=%+v", first, second)
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = updated.(*Model)
	if model.navSearchActive || model.navSearchQuery != "" {
		t.Fatalf("search active/query = %v/%q, want cleared", model.navSearchActive, model.navSearchQuery)
	}
}

func newNavigationTreeModel(t *testing.T) *Model {
	t.Helper()
	model := NewModel(Options{ConfigPath: filepath.Join(t.TempDir(), "tdb.enc"), IconStyle: IconStyleUnicode})
	model.page = PageBrowser
	model.focus = FocusSidebar
	model.activeProfile = &config.Profile{ID: "mysql", Driver: config.DriverMySQL}
	model.adapter = &navigationTreeAdapter{}
	return model
}

func findRequiredHitbox(t *testing.T, hitboxes HitboxRegistry, id string) Hitbox {
	t.Helper()
	for _, hit := range hitboxes {
		if hit.ID == id {
			return hit
		}
	}
	t.Fatalf("missing hitbox %s in %+v", id, hitboxes)
	return Hitbox{}
}

func stripANSI(value string) string {
	return regexp.MustCompile(`\x1b\[[0-9;:]*[A-Za-z]`).ReplaceAllString(value, "")
}

type navigationTreeAdapter struct {
	objectsByDB   map[string][]db.Object
	metadata      db.ObjectMetadata
	previewCalls  int
	metadataCalls int
}

func (a *navigationTreeAdapter) Test(context.Context) error { return nil }
func (a *navigationTreeAdapter) ListDatabases(context.Context) ([]string, error) {
	return []string{"app"}, nil
}
func (a *navigationTreeAdapter) ListObjects(_ context.Context, scope db.Scope) ([]db.Object, error) {
	return a.objectsByDB[scope.Database], nil
}
func (a *navigationTreeAdapter) Preview(_ context.Context, target db.Target, _ db.Query, _ db.Page) (result.Set, error) {
	a.previewCalls++
	return result.Set{Table: &result.Table{
		Columns: []result.Column{{Name: "id"}},
		Rows:    []result.Row{{Values: []any{1}}},
	}}, nil
}
func (a *navigationTreeAdapter) Metadata(context.Context, db.Target) (db.ObjectMetadata, error) {
	a.metadataCalls++
	return a.metadata, nil
}
func (a *navigationTreeAdapter) Insert(context.Context, db.Target, map[string]any) (result.MutationResult, error) {
	return result.MutationResult{}, nil
}
func (a *navigationTreeAdapter) Update(context.Context, db.Target, db.Key, map[string]any) (result.MutationResult, error) {
	return result.MutationResult{}, nil
}
func (a *navigationTreeAdapter) Delete(context.Context, db.Target, db.Key) (result.MutationResult, error) {
	return result.MutationResult{}, nil
}
func (a *navigationTreeAdapter) Execute(context.Context, db.Command) (result.Set, error) {
	return result.Set{}, nil
}
func (a *navigationTreeAdapter) Close() error { return nil }
