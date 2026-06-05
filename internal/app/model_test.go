package app

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"tdb/internal/config"
	"tdb/internal/db"
	"tdb/internal/history"
	"tdb/internal/result"
)

func TestModelUnlocksAndCreatesConnectionProfile(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "tdb.enc")
	model := NewModel(Options{ConfigPath: configPath})

	model.HandleLine(context.Background(), "master-password")
	model.HandleLine(context.Background(), "new mysql local 127.0.0.1 3306 root secret app")

	loaded, err := config.NewStore(configPath).Load("master-password")
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	profile, ok := loaded.GetProfile("local")
	if !ok {
		t.Fatal("created profile was not persisted")
	}
	if profile.Driver != config.DriverMySQL || profile.Host != "127.0.0.1" || profile.Database != "app" {
		t.Fatalf("profile = %+v", profile)
	}
}

func TestModelCreatesMongoProfileFromURICommand(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "tdb.enc")
	model := NewModel(Options{ConfigPath: configPath})

	model.HandleLine(context.Background(), "master-password")
	model.HandleLine(context.Background(), "new mongo mongo-prod mongodb://user:secret@127.0.0.1:27017/app?authSource=admin readonly")

	loaded, err := config.NewStore(configPath).Load("master-password")
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	profile, ok := loaded.GetProfile("mongo-prod")
	if !ok {
		t.Fatal("created mongo profile was not persisted")
	}
	if profile.Driver != config.DriverMongo || profile.URIParams != "mongodb://user:secret@127.0.0.1:27017/app?authSource=admin" {
		t.Fatalf("profile = %+v", profile)
	}
	if profile.Database != "app" {
		t.Fatalf("Database = %q, want app", profile.Database)
	}
	if !profile.ReadOnly {
		t.Fatal("ReadOnly = false, want true")
	}
}

func TestModelDeleteConnectionRequiresConfirmation(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "tdb.enc")
	model := NewModel(Options{ConfigPath: configPath})
	model.HandleLine(context.Background(), "master-password")
	model.HandleLine(context.Background(), "new redis cache 127.0.0.1 6379 default secret 0")

	model.HandleLine(context.Background(), "delete cache")
	if _, ok := model.vault.GetProfile("cache"); !ok {
		t.Fatal("profile deleted before confirmation")
	}

	model.HandleLine(context.Background(), "yes")
	if _, ok := model.vault.GetProfile("cache"); ok {
		t.Fatal("profile still exists after confirmation")
	}
}

func TestModelTabAcceptsQuerySuggestion(t *testing.T) {
	model := NewModel(Options{ConfigPath: filepath.Join(t.TempDir(), "tdb.enc")})
	model.activeProfile = &config.Profile{Driver: config.DriverMySQL}
	model.page = PageQuery
	model.input.SetValue("sel")
	model.refreshSuggestions()

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyTab})
	got := updated.(*Model).input.Value()
	if got != "SELECT" {
		t.Fatalf("input = %q, want SELECT", got)
	}
}

func TestModelViewShowsSuggestions(t *testing.T) {
	model := NewModel(Options{ConfigPath: filepath.Join(t.TempDir(), "tdb.enc")})
	model.activeProfile = &config.Profile{Driver: config.DriverRedis}
	model.page = PageQuery
	model.input.SetValue("hge")
	model.refreshSuggestions()

	view := model.View()
	if !strings.Contains(view, "Suggestions:") || !strings.Contains(view, "HGET") {
		t.Fatalf("view did not render suggestions:\n%s", view)
	}
}

func TestModelInitStartsBlinkTick(t *testing.T) {
	model := NewModel(Options{ConfigPath: filepath.Join(t.TempDir(), "tdb.enc")})

	if cmd := model.Init(); cmd == nil {
		t.Fatal("Init returned nil, want cursor blink tick command")
	}
}

func TestBlinkMsgTogglesCursorState(t *testing.T) {
	model := NewModel(Options{ConfigPath: filepath.Join(t.TempDir(), "tdb.enc")})
	model.cursorBlinkOn = true

	updated, cmd := model.Update(blinkMsg{})
	got := updated.(*Model)
	if got.cursorBlinkOn {
		t.Fatal("cursorBlinkOn remained true, want toggle off")
	}
	if cmd == nil {
		t.Fatal("blink update returned nil command, want next tick")
	}
}

func TestCommandLineRendersBlinkingCursorWhenFocused(t *testing.T) {
	model := NewModel(Options{ConfigPath: filepath.Join(t.TempDir(), "tdb.enc")})
	model.page = PageConnections
	model.focus = FocusCommand
	model.cursorBlinkOn = true
	model.input.SetValue("help")

	line := model.commandLineText()
	if !strings.Contains(line, defaultTheme().cursor.Render(" ")) {
		t.Fatalf("command line missing cursor block: %q", line)
	}
}

func TestHistoryEnterRefillsQueryInput(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "tdb.enc")
	model := NewModel(Options{ConfigPath: configPath})
	model.activeProfile = &config.Profile{ID: "local", Driver: config.DriverMySQL}
	model.page = PageHistory
	model.recordHistoryForTest("SELECT * FROM users")

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(*Model)
	if got.page != PageQuery {
		t.Fatalf("page = %s, want query", got.page)
	}
	if got.input.Value() != "SELECT * FROM users" {
		t.Fatalf("input = %q", got.input.Value())
	}
}

func TestHistoryDownMovesSelection(t *testing.T) {
	model := NewModel(Options{ConfigPath: filepath.Join(t.TempDir(), "tdb.enc")})
	model.activeProfile = &config.Profile{ID: "local", Driver: config.DriverMySQL}
	model.page = PageHistory
	// Distinct fingerprints so they don't merge into one hot entry.
	model.recordHistoryForTest("SELECT 1")
	model.recordHistoryForTest("SELECT name FROM users")

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyDown})
	if got := updated.(*Model).historyIndex; got != 1 {
		t.Fatalf("historyIndex = %d, want 1", got)
	}
}

func TestHistoryReplayExecutesSelectedEntry(t *testing.T) {
	model := NewModel(Options{ConfigPath: filepath.Join(t.TempDir(), "tdb.enc")})
	model.activeProfile = &config.Profile{ID: "local", Driver: config.DriverMySQL}
	model.adapter = &recordingAdapter{}
	model.page = PageHistory
	model.recordHistoryForTest("SELECT 1")

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	got := updated.(*Model)
	if got.page != PageData {
		t.Fatalf("page = %s, want data", got.page)
	}
	if got.result.Table == nil || got.result.Table.CellString(0, 0) != "SELECT 1" {
		t.Fatalf("unexpected replay result: %+v", got.result)
	}
}

type recordingAdapter struct{}

func (a *recordingAdapter) Test(context.Context) error { return nil }
func (a *recordingAdapter) ListDatabases(context.Context) ([]string, error) {
	return nil, nil
}
func (a *recordingAdapter) ListObjects(context.Context, db.Scope) ([]db.Object, error) {
	return nil, nil
}
func (a *recordingAdapter) Preview(context.Context, db.Target, db.Query, db.Page) (result.Set, error) {
	return result.Set{}, nil
}
func (a *recordingAdapter) Insert(context.Context, db.Target, map[string]any) (result.MutationResult, error) {
	return result.MutationResult{}, nil
}
func (a *recordingAdapter) Update(context.Context, db.Target, db.Key, map[string]any) (result.MutationResult, error) {
	return result.MutationResult{}, nil
}
func (a *recordingAdapter) Delete(context.Context, db.Target, db.Key) (result.MutationResult, error) {
	return result.MutationResult{}, nil
}
func (a *recordingAdapter) Execute(_ context.Context, command db.Command) (result.Set, error) {
	return result.Set{Table: &result.Table{
		Columns: []result.Column{{Name: "statement"}},
		Rows:    []result.Row{{Values: []any{command.Text}}},
	}}, nil
}
func (a *recordingAdapter) Close() error { return nil }

func (m *Model) recordHistoryForTest(statement string) {
	m.recordHistory(historyEntryForTest(m.activeProfile.ID, statement))
}

func historyEntryForTest(profileID, statement string) history.Entry {
	return history.Entry{
		ID:        statement,
		ProfileID: profileID,
		Driver:    string(config.DriverMySQL),
		Action:    history.ActionQuery,
		Statement: statement,
		Status:    history.StatusOK,
		StartedAt: time.Now().UTC(),
	}
}
