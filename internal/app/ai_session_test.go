package app

import (
	"testing"
	"time"
)

// typeAIInput inserts each rune into the AI input through the modal key handler so
// the `/` and `@` dropdown refresh logic runs exactly as it would live.
func typeAIInput(m *Model, s string) {
	for _, r := range s {
		m.handleAIChatModalKey(keyMsg(string(r)))
	}
}

// Submitting a message persists the conversation to disk; reloading the store
// returns it with a title derived from the first user message.
func TestAISessionPersistsAndReloads(t *testing.T) {
	m := newWorkspaceVimModel(t)
	m.selectedDB = "app"
	m.openAIChatModal()
	typeAIInput(m, "how many users are there?")
	m.submitAIMessage() // no AI CLI in test env → records you + err turns, still persists

	stored, err := m.aiStore.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(stored) != 1 {
		t.Fatalf("expected one persisted session, got %d", len(stored))
	}
	if stored[0].Title != "how many users are there?" {
		t.Fatalf("title should come from the first user message, got %q", stored[0].Title)
	}
	if stored[0].Scope != "app" || stored[0].ProfileID != "local" {
		t.Fatalf("session scoped wrong: %+v", stored[0])
	}
}

// `/new` opens a fresh empty conversation while the previous one stays in the
// scope's history.
func TestAISlashNewKeepsHistory(t *testing.T) {
	m := newWorkspaceVimModel(t)
	m.selectedDB = "app"
	m.openAIChatModal()
	typeAIInput(m, "first question")
	m.submitAIMessage()
	first := m.currentAISession().id

	m.runAISlash("/new")

	cur := m.currentAISession()
	if cur.id == first {
		t.Fatal("/new should switch to a different session")
	}
	if len(cur.turns) != 0 {
		t.Fatalf("a new conversation should be empty, got %d turns", len(cur.turns))
	}
	if got := len(m.aiSessionsForScope()); got != 2 {
		t.Fatalf("the old conversation should remain in history, want 2 got %d", got)
	}
}

// Typing `/` shows the command palette; narrowing to `/r` filters to /resume.
func TestAISlashCommandDropdown(t *testing.T) {
	m := newWorkspaceVimModel(t)
	m.selectedDB = "app"
	m.openAIChatModal()

	typeAIInput(m, "/")
	if !m.input.SuggestionsVisible() || m.aiSuggestKind != aiSuggestCommand {
		t.Fatal("typing / should show the command palette")
	}
	values := map[string]bool{}
	for _, s := range m.input.Suggestions() {
		values[s.Value] = true
	}
	for _, want := range []string{"/new", "/resume", "/provider", "/help"} {
		if !values[want] {
			t.Fatalf("command palette missing %s; got %v", want, values)
		}
	}

	typeAIInput(m, "r") // "/r"
	got := m.input.Suggestions()
	if len(got) != 1 || got[0].Value != "/resume" {
		t.Fatalf("/r should filter to /resume, got %+v", got)
	}
}

// `/resume` enters the inline picker; selecting another conversation switches the
// active session and returns to chat.
func TestAISlashResumeSwitchesSession(t *testing.T) {
	m := newWorkspaceVimModel(t)
	m.selectedDB = "app"
	m.aiLoaded = true
	m.aiSessions = map[string]*aiSession{}
	m.aiActive = map[string]string{}
	now := time.Now()
	newer := &aiSession{id: "newer", profileID: "local", scope: "app", title: "newer", updatedAt: now}
	older := &aiSession{id: "older", profileID: "local", scope: "app", title: "older", updatedAt: now.Add(-time.Hour)}
	m.aiSessions["newer"] = newer
	m.aiSessions["older"] = older
	m.aiActive[m.aiSessionKey()] = "newer"
	m.openAIChatModal()

	m.runAISlash("/resume")
	if m.aiListMode != aiListResume {
		t.Fatal("/resume should enter the inline resume view")
	}
	// Sorted newest-first: index 0 = newer, index 1 = older.
	m.handleAIChatModalKey(keyMsg("down"))
	m.handleAIChatModalKey(keyMsg("enter"))

	if m.aiListMode != aiListNone {
		t.Fatal("selecting should return to chat")
	}
	if got := m.aiActive[m.aiSessionKey()]; got != "older" {
		t.Fatalf("resume should activate the chosen session, got %q", got)
	}
}

// `/provider` lists the backends and selecting one records the preference.
func TestAISlashProviderSelects(t *testing.T) {
	m := newWorkspaceVimModel(t)
	m.selectedDB = "app"
	m.openAIChatModal()

	m.runAISlash("/provider")
	if m.aiListMode != aiListProvider {
		t.Fatal("/provider should enter the inline provider view")
	}
	// KnownProviders order is [claude, codex]; move to codex and select it.
	m.handleAIChatModalKey(keyMsg("down"))
	m.handleAIChatModalKey(keyMsg("enter"))

	if m.vault.AIProvider != "codex" {
		t.Fatalf("selecting should set the provider, got %q", m.vault.AIProvider)
	}
	if m.aiListMode != aiListNone {
		t.Fatal("selecting should return to chat")
	}
}

// Conversations are isolated per connection+database: only the current scope's
// sessions are listed.
func TestAISessionsScopedToDatabase(t *testing.T) {
	m := newWorkspaceVimModel(t)
	m.aiLoaded = true
	m.aiSessions = map[string]*aiSession{
		"a": {id: "a", profileID: "local", scope: "app", updatedAt: time.Now()},
		"b": {id: "b", profileID: "local", scope: "other", updatedAt: time.Now()},
	}
	m.aiActive = map[string]string{}

	m.selectedDB = "app"
	got := m.aiSessionsForScope()
	if len(got) != 1 || got[0].id != "a" {
		t.Fatalf("scope app should list only its session, got %+v", got)
	}
	m.selectedDB = "other"
	got = m.aiSessionsForScope()
	if len(got) != 1 || got[0].id != "b" {
		t.Fatalf("scope other should list only its session, got %+v", got)
	}
}
