package app

import (
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"tdb/internal/config"
	"tdb/internal/db"
	"tdb/internal/result"
)

func TestHitboxRegistryFindsRegionByCoordinate(t *testing.T) {
	registry := HitboxRegistry{{ID: "open-users", X: 2, Y: 3, Width: 10, Height: 2, Action: actionOpen, Index: 4}}

	hit, ok := registry.Hit(8, 4)
	if !ok {
		t.Fatal("expected hitbox hit")
	}
	if hit.ID != "open-users" || hit.Index != 4 {
		t.Fatalf("hit = %+v", hit)
	}
	if _, ok := registry.Hit(20, 4); ok {
		t.Fatal("unexpected hit outside region")
	}
}

func TestMouseClickConnectionHitboxSelectsConnection(t *testing.T) {
	model := NewModel(Options{ConfigPath: filepath.Join(t.TempDir(), "tdb.enc")})
	model.page = PageConnections
	model.vault.Profiles = []config.Profile{{ID: "one", Driver: config.DriverRedis}, {ID: "two", Driver: config.DriverRedis}}
	model.openAdapter = func(config.Profile) (db.Adapter, error) { return &selectionAdapter{}, nil }
	model.hitboxes = HitboxRegistry{{ID: "connection:1", X: 1, Y: 1, Width: 20, Height: 1, Focus: FocusSidebar, Index: 1}}

	updated, _ := model.Update(tea.MouseMsg{X: 3, Y: 1, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	got := updated.(*Model)
	if got.page != PageConnections || got.connectionIndex != 1 {
		t.Fatalf("page/index = %s/%d, want connections/1", got.page, got.connectionIndex)
	}
}

func TestMouseDoubleClickConnectionHitboxOpensConnection(t *testing.T) {
	model := NewModel(Options{ConfigPath: filepath.Join(t.TempDir(), "tdb.enc")})
	model.page = PageConnections
	model.vault.Profiles = []config.Profile{{ID: "local", Driver: config.DriverRedis}}
	model.openAdapter = func(config.Profile) (db.Adapter, error) { return &selectionAdapter{}, nil }
	model.hitboxes = HitboxRegistry{{ID: "connection:0", X: 1, Y: 1, Width: 20, Height: 1, Focus: FocusSidebar, Index: 0}}

	updated, _ := model.Update(tea.MouseMsg{X: 3, Y: 1, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	updated, _ = updated.Update(tea.MouseMsg{X: 3, Y: 1, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	got := updated.(*Model)
	if got.page != PageBrowser || got.activeProfile.ID != "local" {
		t.Fatalf("page/profile = %s/%+v, want browser/local", got.page, got.activeProfile)
	}
}

func TestMouseWheelScrollsDataResult(t *testing.T) {
	model := NewModel(Options{ConfigPath: filepath.Join(t.TempDir(), "tdb.enc")})
	model.page = PageData
	model.focus = FocusMain
	model.result = manyRowsResult(20)

	updated, _ := model.Update(tea.MouseMsg{X: 10, Y: 4, Button: tea.MouseButtonWheelDown, Action: tea.MouseActionPress})
	if got := updated.(*Model).resultView.RowOffset; got != 1 {
		t.Fatalf("row offset = %d, want 1", got)
	}
}

func TestViewRegistersPanelAndListHitboxes(t *testing.T) {
	model := NewModel(Options{ConfigPath: filepath.Join(t.TempDir(), "tdb.enc")})
	model.page = PageConnections
	model.width = 100
	model.height = 28
	model.vault.Profiles = []config.Profile{{ID: "local", Driver: config.DriverRedis}}

	_ = model.View()

	if _, ok := model.hitboxes.Hit(2, 3); !ok {
		t.Fatalf("expected sidebar hitbox, got none: %+v", model.hitboxes)
	}
	foundConnection := false
	for _, hit := range model.hitboxes {
		if hit.ID == "connection:0" && hit.Action == actionNone {
			foundConnection = true
		}
	}
	if !foundConnection {
		t.Fatalf("missing connection hitbox: %+v", model.hitboxes)
	}
}

func TestViewDoesNotRegisterFooterActionHitboxes(t *testing.T) {
	model := NewModel(Options{ConfigPath: filepath.Join(t.TempDir(), "tdb.enc")})
	model.page = PageConnections
	model.width = 100
	model.height = 28

	_ = model.View()

	for _, hit := range model.hitboxes {
		if strings.HasPrefix(hit.ID, "footer:") {
			t.Fatalf("unexpected footer action hitbox: %+v", hit)
		}
	}
}

func TestViewRegistersConnectionFormHitboxes(t *testing.T) {
	model := NewModel(Options{ConfigPath: filepath.Join(t.TempDir(), "tdb.enc")})
	model.page = PageConnections
	model.width = 100
	model.height = 28
	model.form = newConnectionForm()

	_ = model.View()

	foundMongo := false
	foundCancel := false
	for _, hit := range model.hitboxes {
		if hit.ID == "form-driver:mongo" && hit.Payload == "mongo" {
			foundMongo = true
		}
		if hit.ID == "form:cancel" && hit.Action == actionCancel {
			foundCancel = true
		}
	}
	if !foundMongo || !foundCancel {
		t.Fatalf("form hitboxes mongo=%v cancel=%v all=%+v", foundMongo, foundCancel, model.hitboxes)
	}
}

func TestMouseClickMongoDriverChoosesMongoForm(t *testing.T) {
	model := NewModel(Options{ConfigPath: filepath.Join(t.TempDir(), "tdb.enc")})
	model.page = PageConnections
	model.form = newConnectionForm()
	model.hitboxes = HitboxRegistry{{ID: "form-driver:mongo", X: 1, Y: 1, Width: 12, Height: 1, Focus: FocusContext, Payload: "mongo"}}

	updated, _ := model.Update(tea.MouseMsg{X: 2, Y: 1, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	got := updated.(*Model)
	if got.form == nil || got.form.selectingDriver || got.form.driver != config.DriverMongo {
		t.Fatalf("form = %+v, want chosen mongo", got.form)
	}
}

func manyRowsResult(count int) result.Set {
	rows := make([]result.Row, 0, count)
	for i := 0; i < count; i++ {
		rows = append(rows, result.Row{Values: []any{i}})
	}
	return result.Set{Table: &result.Table{
		Columns: []result.Column{{Name: "id"}},
		Rows:    rows,
	}}
}
