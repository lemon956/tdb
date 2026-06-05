package app

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"

	"tdb/internal/config"
)

func TestNewConnectionFormStartsWithDriverSelection(t *testing.T) {
	form := newConnectionForm()

	if !form.selectingDriver {
		t.Fatal("form should start in driver selection")
	}
	if got := form.selectedDriver(); got != config.DriverMySQL {
		t.Fatalf("selected driver = %s, want mysql", got)
	}
	if len(form.fields) != 0 {
		t.Fatalf("fields = %d, want 0 before driver selection", len(form.fields))
	}
}

func TestConnectionFormChoosesMongoURIFields(t *testing.T) {
	form := newConnectionForm()

	form.chooseDriver(config.DriverMongo)

	if form.selectingDriver {
		t.Fatal("form should leave driver selection after choosing mongo")
	}
	if got := form.driver; got != config.DriverMongo {
		t.Fatalf("driver = %s, want mongo", got)
	}
	want := []string{"id", "uri", "database"}
	if len(form.fields) != len(want) {
		t.Fatalf("fields = %d, want %d", len(form.fields), len(want))
	}
	for i, name := range want {
		if form.fields[i].Name != name {
			t.Fatalf("field[%d] = %s, want %s", i, form.fields[i].Name, name)
		}
	}
}

func TestConnectionFormChoosesRedisFields(t *testing.T) {
	form := newConnectionForm()

	form.chooseDriver(config.DriverRedis)

	want := []string{"id", "host", "port", "user", "password", "db"}
	if len(form.fields) != len(want) {
		t.Fatalf("fields = %d, want %d", len(form.fields), len(want))
	}
	for i, name := range want {
		if form.fields[i].Name != name {
			t.Fatalf("field[%d] = %s, want %s", i, form.fields[i].Name, name)
		}
	}
	if !form.fields[4].Secret {
		t.Fatal("password field should be marked secret")
	}
}

func TestConnectionFormBuildsMongoProfileFromURI(t *testing.T) {
	form := newConnectionForm()
	form.chooseDriver(config.DriverMongo)
	form.setFieldValue("id", "mongo-prod")
	form.setFieldValue("uri", "mongodb://user:secret@127.0.0.1:27017/app?authSource=admin")
	form.readOnly = true

	profile, err := form.buildProfile()
	if err != nil {
		t.Fatalf("buildProfile returned error: %v", err)
	}
	if profile.Driver != config.DriverMongo || profile.ID != "mongo-prod" {
		t.Fatalf("profile identity = %+v", profile)
	}
	if profile.URIParams != "mongodb://user:secret@127.0.0.1:27017/app?authSource=admin" {
		t.Fatalf("URIParams = %q", profile.URIParams)
	}
	if profile.Database != "app" {
		t.Fatalf("Database = %q, want app from URI path", profile.Database)
	}
	if !profile.ReadOnly {
		t.Fatal("ReadOnly = false, want true")
	}
}

func TestConnectionFormBuildsRedisProfileFromFields(t *testing.T) {
	form := newConnectionForm()
	form.chooseDriver(config.DriverRedis)
	form.setFieldValue("id", "redis-local")
	form.setFieldValue("host", "127.0.0.1")
	form.setFieldValue("port", "6379")
	form.setFieldValue("user", "default")
	form.setFieldValue("password", "secret")
	form.setFieldValue("db", "2")

	profile, err := form.buildProfile()
	if err != nil {
		t.Fatalf("buildProfile returned error: %v", err)
	}
	if profile.Driver != config.DriverRedis || profile.ID != "redis-local" {
		t.Fatalf("profile identity = %+v", profile)
	}
	if profile.Host != "127.0.0.1" || profile.Port != 6379 || profile.User != "default" || profile.Password != "secret" {
		t.Fatalf("profile connection fields = %+v", profile)
	}
	if profile.RedisDB != 2 {
		t.Fatalf("RedisDB = %d, want 2", profile.RedisDB)
	}
}

func TestNewShortcutOpensConnectionForm(t *testing.T) {
	model := NewModel(Options{ConfigPath: filepath.Join(t.TempDir(), "tdb.enc")})
	model.page = PageConnections

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	got := updated.(*Model)

	if got.form == nil {
		t.Fatal("form = nil, want connection form")
	}
	if !got.form.selectingDriver {
		t.Fatal("form should start by selecting a driver")
	}
	if got.focus != FocusContext {
		t.Fatalf("focus = %s, want context", got.focus)
	}
}

func TestConnectionFormKeyboardCreatesMongoProfile(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "tdb.enc")
	model := NewModel(Options{ConfigPath: configPath})
	model.HandleLine(context.Background(), "master-password")

	model = updateKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	model = updateKey(model, tea.KeyMsg{Type: tea.KeyDown})
	model = updateKey(model, tea.KeyMsg{Type: tea.KeyDown})
	model = updateKey(model, tea.KeyMsg{Type: tea.KeyEnter})
	model = typeIntoModel(model, "mongo-prod")
	model = updateKey(model, tea.KeyMsg{Type: tea.KeyEnter})
	model = typeIntoModel(model, "mongodb://user:secret@127.0.0.1:27017/app?authSource=admin")
	model = updateKey(model, tea.KeyMsg{Type: tea.KeyEnter})
	model = updateKey(model, tea.KeyMsg{Type: tea.KeyEnter})

	if model.form != nil {
		t.Fatal("form should close after saving")
	}
	loaded, err := config.NewStore(configPath).Load("master-password")
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	profile, ok := loaded.GetProfile("mongo-prod")
	if !ok {
		t.Fatal("created mongo profile was not persisted")
	}
	if profile.URIParams != "mongodb://user:secret@127.0.0.1:27017/app?authSource=admin" || profile.Database != "app" {
		t.Fatalf("profile = %+v", profile)
	}
}

func newFieldEditModel(t *testing.T) *Model {
	t.Helper()
	model := newWorkspaceTabModel(t)
	model.form = newConnectionForm()
	model.form.chooseDriver(config.DriverMySQL)
	model.form.fieldIndex = 0 // "ID" text field
	return model
}

func fieldKey(s string) tea.KeyMsg {
	switch s {
	case "left":
		return tea.KeyMsg{Type: tea.KeyLeft}
	case "right":
		return tea.KeyMsg{Type: tea.KeyRight}
	case "home":
		return tea.KeyMsg{Type: tea.KeyHome}
	case "end":
		return tea.KeyMsg{Type: tea.KeyEnd}
	case "backspace":
		return tea.KeyMsg{Type: tea.KeyBackspace}
	case "delete":
		return tea.KeyMsg{Type: tea.KeyDelete}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
}

func typeInto(m *Model, keys ...string) {
	for _, k := range keys {
		m.handleConnectionFieldKey(context.Background(), fieldKey(k))
	}
}

func activeFormField(m *Model) connectionFormField {
	f, _ := m.form.currentField()
	return *f
}

func TestConnectionFieldInsertsAtCursor(t *testing.T) {
	m := newFieldEditModel(t)
	typeInto(m, "a", "b", "c")  // "abc", cursor at end
	typeInto(m, "left", "left") // cursor between a|bc
	typeInto(m, "X")            // "aXbc"
	if got := activeFormField(m).Value; got != "aXbc" {
		t.Fatalf("Value = %q, want aXbc", got)
	}
}

func TestConnectionFieldBackspaceAndDeleteAtCursor(t *testing.T) {
	m := newFieldEditModel(t)
	typeInto(m, "a", "b", "c", "d") // abcd
	typeInto(m, "left")             // abc|d
	typeInto(m, "backspace")        // ab|d (deletes char before cursor)
	if got := activeFormField(m).Value; got != "abd" {
		t.Fatalf("after backspace Value = %q, want abd", got)
	}
	typeInto(m, "delete") // ab| (deletes char at cursor: 'd')
	if got := activeFormField(m).Value; got != "ab" {
		t.Fatalf("after delete Value = %q, want ab", got)
	}
}

func TestConnectionFieldHomeEnd(t *testing.T) {
	m := newFieldEditModel(t)
	typeInto(m, "h", "i")
	typeInto(m, "home", "Z") // Zhi
	if got := activeFormField(m).Value; got != "Zhi" {
		t.Fatalf("after home insert Value = %q, want Zhi", got)
	}
	typeInto(m, "end", "!") // Zhi!
	if got := activeFormField(m).Value; got != "Zhi!" {
		t.Fatalf("after end insert Value = %q, want Zhi!", got)
	}
}

func TestConnectionFieldCJKCursorRuneSafe(t *testing.T) {
	m := newFieldEditModel(t)
	typeInto(m, "真", "人") // 6 bytes
	field := activeFormField(m)
	if field.Cursor != len(field.Value) {
		t.Fatalf("cursor should be at end, got %d/%d", field.Cursor, len(field.Value))
	}
	typeInto(m, "left") // land on rune boundary before 人
	field = activeFormField(m)
	if field.Cursor != len("真") || !utf8.RuneStart(field.Value[field.Cursor]) {
		t.Fatalf("cursor %d not on a rune boundary", field.Cursor)
	}
	typeInto(m, "backspace") // delete 真 wholly
	if got := activeFormField(m).Value; got != "人" {
		t.Fatalf("CJK backspace Value = %q, want 人", got)
	}
}

func TestConnectionFieldCursorPreservedPerField(t *testing.T) {
	m := newFieldEditModel(t)
	typeInto(m, "a", "b", "c", "home") // field 0 cursor at 0
	m.form.moveField(1)                // next field
	typeInto(m, "x", "y")              // field 1 = "xy"
	m.form.moveField(-1)               // back to field 0
	if got := activeFormField(m).Cursor; got != 0 {
		t.Fatalf("field 0 cursor not preserved, got %d want 0", got)
	}
	typeInto(m, "Z") // insert at preserved home position
	if got := activeFormField(m).Value; got != "Zabc" {
		t.Fatalf("Value = %q, want Zabc", got)
	}
}

func TestConnectionFormRendersCaretAtCursor(t *testing.T) {
	m := newFieldEditModel(t)
	m.cursorBlinkOn = true
	typeInto(m, "a", "b", "left") // a|b
	content := m.connectionFormContent()
	// Caret drawn on 'b' (the char at the cursor), not appended at the end.
	if !strings.Contains(content, m.renderCursorCell("b")) {
		t.Fatalf("caret should highlight the char at the cursor:\n%s", stripANSI(content))
	}
}

func updateKey(model *Model, msg tea.KeyMsg) *Model {
	updated, _ := model.Update(msg)
	return updated.(*Model)
}

func typeIntoModel(model *Model, value string) *Model {
	for _, r := range value {
		model = updateKey(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	return model
}
