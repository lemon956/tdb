package app

import (
	"context"
	"path/filepath"
	"testing"

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
