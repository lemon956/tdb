package app

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"tdb/internal/ai"
	"tdb/internal/aichat"
	"tdb/internal/db"
	"tdb/internal/suggest"
)

// aiTurn is one message in an AI conversation. role is "you", "ai", or "err".
type aiTurn struct {
	role string
	text string
}

// aiSession is one conversation, scoped to a connection + database. It carries
// identity (id/title/timestamps) so conversations can be listed, resumed, and
// persisted, plus the SQL blocks from the latest reply (for one-key insertion).
type aiSession struct {
	id        string
	profileID string
	scope     string
	dbLabel   string
	title     string
	turns     []aiTurn
	lastSQLs  []string
	pending   bool
	createdAt time.Time
	updatedAt time.Time
}

// aiListMode is the inline sub-view shown inside the AI chat window (in place of
// the normal chat + input) when the user runs a slash command.
type aiListMode int

const (
	aiListNone aiListMode = iota
	aiListResume
	aiListProvider
	aiListHelp
)

// aiSuggestKind tells the enter handler whether the input dropdown is offering
// slash commands or @table names.
type aiSuggestKind int

const (
	aiSuggestNone aiSuggestKind = iota
	aiSuggestCommand
	aiSuggestMention
)

// aiSlashCommand is one entry in the `/` command palette.
type aiSlashCommand struct {
	name    string   // canonical, e.g. "/new"
	aliases []string // e.g. "/clear"
	detail  string   // shown in the dropdown
}

// aiSlashCommands is the command palette listed when the input starts with `/`.
var aiSlashCommands = []aiSlashCommand{
	{name: "/new", aliases: []string{"/clear"}, detail: "start a new conversation"},
	{name: "/resume", aliases: []string{"/history"}, detail: "resume a past conversation"},
	{name: "/provider", aliases: []string{"/model"}, detail: "choose the AI backend (claude/codex)"},
	{name: "/help", detail: "list AI panel commands"},
}

// aiSessionKey scopes a conversation to the active connection + catalog/database
// so switching databases (or connections) gives an independent chat history.
func (m *Model) aiSessionKey() string {
	return m.activeProfileID() + "\x00" + m.scopeKey(m.selectedCatalog, m.selectedDB)
}

// aiDBLabel is the human-readable database label for the current scope.
func (m *Model) aiDBLabel() string {
	if m.selectedDB == "" {
		return "(no database)"
	}
	if cat := normalizedCatalog(m.selectedCatalog); cat != "" {
		return cat + "." + m.selectedDB
	}
	return m.selectedDB
}

// ensureAISessionsLoaded hydrates m.aiSessions from disk the first time the AI
// panel is used, so past conversations survive restarts.
func (m *Model) ensureAISessionsLoaded() {
	if m.aiLoaded {
		return
	}
	m.aiLoaded = true
	if m.aiSessions == nil {
		m.aiSessions = map[string]*aiSession{}
	}
	if m.aiStore == nil {
		return
	}
	stored, err := m.aiStore.Load()
	if err != nil {
		m.message = "ai history: " + err.Error()
		return
	}
	for _, s := range stored {
		sess := &aiSession{
			id:        s.ID,
			profileID: s.ProfileID,
			scope:     s.Scope,
			dbLabel:   s.DBLabel,
			title:     s.Title,
			createdAt: s.CreatedAt,
			updatedAt: s.UpdatedAt,
		}
		for _, msg := range s.Messages {
			sess.turns = append(sess.turns, aiTurn{role: msg.Role, text: msg.Text})
			if msg.Role == "ai" {
				if blocks := ai.ExtractBlocks(msg.Text, aiFenceLangs(m.activeDriver())); len(blocks) > 0 {
					sess.lastSQLs = blocks
				}
			}
		}
		m.aiSessions[s.ID] = sess
	}
}

// latestSessionForScope returns the most recently updated session for the
// current connection + database, or nil if there are none.
func (m *Model) latestSessionForScope() *aiSession {
	key := m.aiSessionKey()
	var best *aiSession
	for _, s := range m.aiSessions {
		if m.sessionScopeKey(s) != key {
			continue
		}
		if best == nil || s.updatedAt.After(best.updatedAt) {
			best = s
		}
	}
	return best
}

// sessionScopeKey rebuilds the aiSessionKey a session belongs to (profile + scope).
func (m *Model) sessionScopeKey(s *aiSession) string {
	return s.profileID + "\x00" + s.scope
}

func (m *Model) currentAISession() *aiSession {
	m.ensureAISessionsLoaded()
	if m.aiSessions == nil {
		m.aiSessions = map[string]*aiSession{}
	}
	if m.aiActive == nil {
		m.aiActive = map[string]string{}
	}
	key := m.aiSessionKey()
	if id := m.aiActive[key]; id != "" {
		if s := m.aiSessions[id]; s != nil {
			return s
		}
	}
	if s := m.latestSessionForScope(); s != nil {
		m.aiActive[key] = s.id
		return s
	}
	return m.newAISession()
}

// newAISession creates an empty conversation for the current scope and makes it
// active. It is not persisted until it has at least one message.
func (m *Model) newAISession() *aiSession {
	if m.aiSessions == nil {
		m.aiSessions = map[string]*aiSession{}
	}
	if m.aiActive == nil {
		m.aiActive = map[string]string{}
	}
	now := time.Now()
	s := &aiSession{
		id:        aichat.NewID(),
		profileID: m.activeProfileID(),
		scope:     m.scopeKey(m.selectedCatalog, m.selectedDB),
		dbLabel:   m.aiDBLabel(),
		createdAt: now,
		updatedAt: now,
	}
	m.aiSessions[s.id] = s
	m.aiActive[m.aiSessionKey()] = s.id
	return s
}

// startNewAISession is the `/new` command: open a fresh conversation.
func (m *Model) startNewAISession() {
	m.newAISession()
	m.aiListMode = aiListNone
	m.input.Clear()
	m.input.HideSuggestions()
	m.scrollAIToBottom()
	m.message = "new conversation"
}

// persistAISession writes a conversation to disk, deriving a title from the
// first user turn if it has none.
func (m *Model) persistAISession(s *aiSession) {
	if s == nil || m.aiStore == nil {
		return
	}
	if s.title == "" {
		s.title = aiTitleFrom(s.turns)
	}
	msgs := make([]aichat.Message, 0, len(s.turns))
	for _, t := range s.turns {
		msgs = append(msgs, aichat.Message{Role: t.role, Text: t.text})
	}
	if err := m.aiStore.Upsert(aichat.Session{
		ID:        s.id,
		ProfileID: s.profileID,
		Scope:     s.scope,
		DBLabel:   s.dbLabel,
		Title:     s.title,
		Messages:  msgs,
		CreatedAt: s.createdAt,
		UpdatedAt: s.updatedAt,
	}); err != nil {
		m.message = "ai history: " + err.Error()
	}
}

// aiTitleFrom builds a short single-line title from the first user message.
func aiTitleFrom(turns []aiTurn) string {
	for _, t := range turns {
		if t.role != "you" {
			continue
		}
		title := strings.TrimSpace(strings.ReplaceAll(t.text, "\n", " "))
		if title == "" {
			continue
		}
		if r := []rune(title); len(r) > 40 {
			title = strings.TrimSpace(string(r[:40])) + "…"
		}
		return title
	}
	return "(empty)"
}

// aiSessionsForScope returns the conversations for the current connection +
// database, most recently updated first.
func (m *Model) aiSessionsForScope() []*aiSession {
	m.ensureAISessionsLoaded()
	key := m.aiSessionKey()
	var out []*aiSession
	for _, s := range m.aiSessions {
		if m.sessionScopeKey(s) == key {
			out = append(out, s)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].updatedAt.After(out[j].updatedAt)
	})
	return out
}

func (m *Model) openAIChatModal() {
	m.input.Clear()
	m.aiListMode = aiListNone
	m.currentAISession() // ensure one exists (resume latest or create)
	// Load the current database's tables so @-mention search has candidates even
	// when the user has not expanded the database in the sidebar.
	m.ensureQueryObjectsLoaded(m.selectedCatalog, m.selectedDB)
	m.modal = &modalState{Kind: modalAIChat, Title: "AI assistant"}
	m.focusModal()
	m.scrollAIToBottom()
	m.message = "AI assistant"
}

func (m *Model) resolveAIProvider() (*ai.Provider, error) {
	return ai.Detect(m.vault.AIProvider, nil)
}

// handleAICommand handles `:ai` (open the panel) and `:ai use <claude|codex>`
// (set + persist the preferred backend).
func (m *Model) handleAICommand(args []string) {
	if len(args) == 0 {
		m.openAIChatModal()
		return
	}
	switch strings.ToLower(args[0]) {
	case "use":
		if len(args) < 2 {
			m.message = "usage: ai use <claude|codex>"
			return
		}
		provider := strings.ToLower(args[1])
		if provider != "claude" && provider != "codex" {
			m.message = "ai provider must be claude or codex"
			return
		}
		m.vault.AIProvider = provider
		if m.store != nil {
			if err := m.store.Save(m.master, m.vault); err != nil {
				m.message = "ai provider set (not persisted): " + err.Error()
				return
			}
		}
		m.message = "ai provider: " + provider
	default:
		m.message = "usage: ai [use <claude|codex>]"
	}
}

func (m *Model) handleAIChatModalKey(msg tea.KeyMsg) bool {
	// The inline list views (resume/provider/help) take over the chat window.
	if m.aiListMode != aiListNone {
		return m.handleAIListKey(msg)
	}
	switch msg.String() {
	case "esc":
		if m.input.SuggestionsVisible() {
			m.input.HideSuggestions()
			return true
		}
		m.closeModal()
	case "tab":
		if m.input.SuggestionsVisible() {
			m.input.NextSuggestion()
		}
	case "shift+tab":
		if m.input.SuggestionsVisible() {
			m.input.PreviousSuggestion()
		}
	case "enter":
		switch {
		case m.input.SuggestionsVisible() && m.aiSuggestKind == aiSuggestCommand:
			m.runHighlightedSlash()
		case m.input.SuggestionsVisible():
			m.acceptAIMention()
		case isAISlashLine(m.input.Value()):
			m.runAISlash(m.input.Value())
		default:
			m.submitAIMessage()
		}
	case "ctrl+j":
		m.submitAIMessage()
	case "ctrl+y":
		m.insertAISQL()
	case "pgup":
		m.scrollModal(-m.modalBodyHeight())
	case "pgdown":
		m.scrollModal(m.modalBodyHeight())
	case "up":
		if m.input.SuggestionsVisible() {
			m.input.PreviousSuggestion()
		} else {
			m.scrollModal(-1)
		}
	case "down":
		if m.input.SuggestionsVisible() {
			m.input.NextSuggestion()
		} else {
			m.scrollModal(1)
		}
	case "backspace", "ctrl+h":
		m.input.Backspace()
		m.refreshAIInput()
	default:
		if len(msg.Runes) > 0 {
			m.input.Insert(string(msg.Runes))
			m.refreshAIInput()
		}
	}
	return true
}

// handleAIListKey drives the inline resume/provider/help views.
func (m *Model) handleAIListKey(msg tea.KeyMsg) bool {
	switch m.aiListMode {
	case aiListResume:
		return m.handleAIResumeKey(msg)
	case aiListProvider:
		return m.handleAIProviderKey(msg)
	default: // aiListHelp
		switch msg.String() {
		case "esc", "enter", "q":
			m.aiListMode = aiListNone
		}
		return true
	}
}

func (m *Model) handleAIResumeKey(msg tea.KeyMsg) bool {
	sessions := m.aiSessionsForScope()
	switch msg.String() {
	case "esc", "q":
		m.aiListMode = aiListNone
	case "up", "k":
		m.historyIndex = clamp(m.historyIndex-1, 0, max(0, len(sessions)-1))
	case "down", "j":
		m.historyIndex = clamp(m.historyIndex+1, 0, max(0, len(sessions)-1))
	case "enter":
		if len(sessions) == 0 {
			m.aiListMode = aiListNone
			return true
		}
		chosen := sessions[clamp(m.historyIndex, 0, len(sessions)-1)]
		m.aiActive[m.aiSessionKey()] = chosen.id
		m.aiListMode = aiListNone
		m.scrollAIToBottom()
		m.message = "resumed: " + chosen.title
	case "d":
		if len(sessions) == 0 {
			return true
		}
		m.deleteAISession(sessions[clamp(m.historyIndex, 0, len(sessions)-1)])
		m.historyIndex = clamp(m.historyIndex, 0, max(0, len(m.aiSessionsForScope())-1))
	}
	return true
}

func (m *Model) handleAIProviderKey(msg tea.KeyMsg) bool {
	providers := ai.KnownProviders()
	switch msg.String() {
	case "esc", "q":
		m.aiListMode = aiListNone
	case "up", "k":
		m.historyIndex = clamp(m.historyIndex-1, 0, max(0, len(providers)-1))
	case "down", "j":
		m.historyIndex = clamp(m.historyIndex+1, 0, max(0, len(providers)-1))
	case "enter":
		if len(providers) == 0 {
			m.aiListMode = aiListNone
			return true
		}
		m.setAIProvider(providers[clamp(m.historyIndex, 0, len(providers)-1)])
		m.aiListMode = aiListNone
	}
	return true
}

// deleteAISession removes a conversation from memory and disk; if it was the
// active one, clear the pointer so the next open resumes the latest or starts
// fresh.
func (m *Model) deleteAISession(s *aiSession) {
	if s == nil {
		return
	}
	delete(m.aiSessions, s.id)
	key := m.aiSessionKey()
	if m.aiActive[key] == s.id {
		delete(m.aiActive, key)
	}
	if m.aiStore != nil {
		if err := m.aiStore.Delete(s.id); err != nil {
			m.message = "ai history: " + err.Error()
			return
		}
	}
	m.message = "deleted: " + s.title
}

// setAIProvider records and persists the preferred backend (claude/codex).
func (m *Model) setAIProvider(provider string) {
	m.vault.AIProvider = provider
	if m.store != nil {
		if err := m.store.Save(m.master, m.vault); err != nil {
			m.message = "ai provider set (not persisted): " + err.Error()
			return
		}
	}
	m.message = "ai provider: " + provider
}

// isAISlashLine reports whether v is a bare slash command line (starts with '/').
func isAISlashLine(v string) bool {
	return strings.HasPrefix(strings.TrimSpace(v), "/")
}

// runHighlightedSlash executes the slash command currently highlighted in the
// `/` dropdown.
func (m *Model) runHighlightedSlash() {
	sugg := m.input.Suggestions()
	idx := m.input.SelectedIndex()
	if idx < 0 || idx >= len(sugg) {
		m.input.HideSuggestions()
		return
	}
	m.runAISlash(sugg[idx].Value)
}

// runAISlash dispatches a slash command typed in the AI input.
func (m *Model) runAISlash(line string) {
	m.input.HideSuggestions()
	fields := strings.Fields(strings.TrimSpace(line))
	if len(fields) == 0 {
		return
	}
	cmd := resolveAISlash(fields[0])
	switch cmd {
	case "/new":
		m.startNewAISession()
	case "/resume":
		m.input.Clear()
		m.aiListMode = aiListResume
		m.historyIndex = 0
	case "/provider":
		m.input.Clear()
		m.aiListMode = aiListProvider
		m.historyIndex = providerIndex(ai.KnownProviders(), m.vault.AIProvider)
	case "/help":
		m.input.Clear()
		m.aiListMode = aiListHelp
	default:
		m.message = "unknown command " + fields[0] + " — try /help"
	}
}

// resolveAISlash maps a typed command (or alias) to its canonical name.
func resolveAISlash(token string) string {
	token = strings.ToLower(token)
	for _, c := range aiSlashCommands {
		if c.name == token {
			return c.name
		}
		for _, a := range c.aliases {
			if a == token {
				return c.name
			}
		}
	}
	return ""
}

func providerIndex(providers []string, current string) int {
	for i, p := range providers {
		if p == current {
			return i
		}
	}
	return 0
}

// aiMentionPrefix returns the partial word after a trailing '@' in s (e.g.
// "ask @us" → "us", true; "ask @" → "", true), used to drive @-table completion.
func aiMentionPrefix(s string) (string, bool) {
	i := len(s)
	for i > 0 {
		c := s[i-1]
		if c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			i--
			continue
		}
		break
	}
	if i > 0 && s[i-1] == '@' {
		return s[i:], true
	}
	return "", false
}

// refreshAIInput updates the inline dropdown as the user types: a slash-command
// palette when the input starts with '/', else @table completion.
func (m *Model) refreshAIInput() {
	value := m.input.Value()
	if cmds, ok := aiSlashPrefix(value); ok {
		m.refreshAISlash(cmds)
		return
	}
	m.aiSuggestKind = aiSuggestNone
	m.refreshAIMention()
}

// aiSlashPrefix reports whether value is in "slash command" entry mode: it
// starts with '/' and the first token has no space yet. Returns the partial
// command (e.g. "/re").
func aiSlashPrefix(value string) (string, bool) {
	if !strings.HasPrefix(value, "/") {
		return "", false
	}
	if strings.ContainsAny(value, " \t") {
		return "", false // past the command word; stop completing
	}
	return value, true
}

// refreshAISlash shows the command palette filtered by the typed prefix.
func (m *Model) refreshAISlash(prefix string) {
	lp := strings.ToLower(prefix)
	var sugg []suggest.Suggestion
	for _, c := range aiSlashCommands {
		if strings.HasPrefix(c.name, lp) || lp == "/" {
			sugg = append(sugg, suggest.Suggestion{Value: c.name, Detail: c.detail})
		}
	}
	if len(sugg) == 0 {
		m.aiSuggestKind = aiSuggestNone
		m.input.HideSuggestions()
		m.message = "no command matches " + prefix
		return
	}
	m.aiSuggestKind = aiSuggestCommand
	m.input.SetSuggestions(sugg)
}

// refreshAIMention shows table-name suggestions when the input ends with @<word>.
func (m *Model) refreshAIMention() {
	prefix, ok := aiMentionPrefix(m.input.Value())
	if !ok {
		m.input.HideSuggestions()
		return
	}
	m.aiSuggestKind = aiSuggestMention
	lp := strings.ToLower(prefix)
	var sugg []suggest.Suggestion
	for _, o := range m.databaseObjects[m.scopeKey(m.selectedCatalog, m.selectedDB)] {
		if o.Name == "" {
			continue
		}
		if lp == "" || strings.Contains(strings.ToLower(o.Name), lp) {
			sugg = append(sugg, suggest.Suggestion{Value: o.Name, Detail: string(o.Type)})
		}
		if len(sugg) >= 20 {
			break
		}
	}
	if len(sugg) == 0 {
		// Silently: no popup/page switch, just a bottom-right status hint.
		m.aiSuggestKind = aiSuggestNone
		m.input.HideSuggestions()
		if len(m.databaseObjects[m.scopeKey(m.selectedCatalog, m.selectedDB)]) == 0 {
			m.message = "no tables loaded for this database"
		} else {
			m.message = "no table matches @" + prefix
		}
		return
	}
	m.input.SetSuggestions(sugg)
}

// acceptAIMention replaces the @<partial> with the selected @<table> and adds a
// trailing space so the mention is delimited (its schema is attached via
// buildSchemaContext, which parses @table tokens from the message).
func (m *Model) acceptAIMention() {
	if m.input.AcceptSuggestion() {
		m.input.Insert(" ")
	}
	m.input.HideSuggestions()
}

func (m *Model) submitAIMessage() {
	question := strings.TrimSpace(m.input.Value())
	if question == "" {
		m.message = "type a question first"
		return
	}
	sess := m.currentAISession()
	if sess.pending {
		m.message = "AI is still answering…"
		return
	}
	provider, err := m.resolveAIProvider()
	if err != nil {
		sess.turns = append(sess.turns, aiTurn{role: "you", text: question})
		sess.turns = append(sess.turns, aiTurn{role: "err", text: err.Error() + " — install claude/codex or set `:ai use <provider>`"})
		sess.updatedAt = time.Now()
		m.persistAISession(sess)
		m.input.Clear()
		m.scrollAIToBottom()
		return
	}
	prompt := buildAIPrompt(aiSystemPrompt(m.activeDriver(), m.profileReadOnly()), sess.turns, m.buildSchemaContext(question), question)
	sess.turns = append(sess.turns, aiTurn{role: "you", text: question})
	sess.pending = true
	sess.updatedAt = time.Now()
	m.persistAISession(sess)
	m.input.Clear()
	m.scrollAIToBottom()
	id := sess.id
	m.nextCmd = m.runAsync("AI…", func(ctx context.Context) tea.Msg {
		reply, askErr := provider.Ask(ctx, prompt)
		return asyncResultMsg{apply: func(m *Model) {
			s := m.aiSessions[id]
			if s == nil {
				return
			}
			s.pending = false
			if askErr != nil {
				s.turns = append(s.turns, aiTurn{role: "err", text: askErr.Error()})
			} else {
				s.turns = append(s.turns, aiTurn{role: "ai", text: reply})
				if blocks := ai.ExtractBlocks(reply, aiFenceLangs(m.activeDriver())); len(blocks) > 0 {
					s.lastSQLs = blocks
				}
			}
			s.updatedAt = time.Now()
			m.persistAISession(s)
			m.scrollAIToBottom()
		}}
	})
}

// insertAISQL inserts the AI's SQL into the query editor: directly when there is
// exactly one block, or via a picker when the reply contained several.
func (m *Model) insertAISQL() {
	sess := m.currentAISession()
	switch len(sess.lastSQLs) {
	case 0:
		m.message = "no SQL from the AI yet"
	case 1:
		m.insertSQLText(sess.lastSQLs[0])
	default:
		m.openAISQLPicker(sess.lastSQLs)
	}
}

// insertSQLText drops sql into the active query editor (opening a query tab if
// needed) and closes any open AI overlay.
func (m *Model) insertSQLText(sql string) {
	m.modal = nil
	m.restoreModalFocus()
	tab := m.activeWorkspaceTab()
	if tab == nil || tab.Kind != workspaceTabQuery {
		m.openQueryWorkspaceTab()
		tab = m.activeWorkspaceTab()
	}
	if tab == nil || tab.Kind != workspaceTabQuery {
		m.message = "open a query tab to insert SQL"
		return
	}
	tab.QueryBuffer = sanitizeMultilineInput(sql)
	tab.QueryCursor = len(tab.QueryBuffer)
	tab.VimMode = vimModeInsert
	tab.WorkspaceFocus = workspaceFocusEditor
	m.focus = FocusMain
	m.syncActiveTabState()
	m.message = "SQL inserted into query editor"
}

// openAISQLPicker lets the user choose which SQL block (from a multi-statement
// reply) to insert. It replaces the AI modal in place, preserving its saved
// focus so closing returns to the workspace.
func (m *Model) openAISQLPicker(choices []string) {
	m.aiSQLChoices = choices
	m.historyIndex = 0
	m.modal = &modalState{Kind: modalAISQLPick, Title: "Insert which SQL?"}
	m.setModalScroll(0)
}

func (m *Model) handleAISQLPickKey(msg tea.KeyMsg) bool {
	n := len(m.aiSQLChoices)
	switch msg.String() {
	case "esc":
		m.closeModal()
	case "enter":
		if n > 0 {
			m.insertSQLText(m.aiSQLChoices[clamp(m.historyIndex, 0, n-1)])
		}
	case "up", "k":
		m.historyIndex = clamp(m.historyIndex-1, 0, max(0, n-1))
	case "down", "j":
		m.historyIndex = clamp(m.historyIndex+1, 0, max(0, n-1))
	}
	return true
}

func (m *Model) aiSQLPickContent() string {
	theme := defaultTheme()
	width := max(8, clamp(m.workspaceContentWidth(), 32, 84)-6)
	var b strings.Builder
	for i, sql := range m.aiSQLChoices {
		marker := "  "
		if i == m.historyIndex {
			marker = "> "
		}
		// Highlighted numbered header per choice, then the FULL SQL (wrapped) so
		// statements that differ only deep in the body are still distinguishable.
		header := fmt.Sprintf("%s%d.", marker, i+1)
		b.WriteString(selectedRow(theme, header, i == m.historyIndex) + "\n")
		for _, line := range strings.Split(ansi.Hardwrap(sql, max(1, width-3), false), "\n") {
			b.WriteString("   " + line + "\n")
		}
		b.WriteString("\n")
	}
	b.WriteString(theme.muted.Render("↑↓ select · enter insert · esc cancel"))
	return b.String()
}

// aiContextSummary is the one-line header showing what schema context the AI
// gets: the current database, table count, and any @-mentioned tables (+col count).
func (m *Model) aiContextSummary() string {
	if m.selectedDB == "" {
		return "context: no database selected — pick one in the sidebar"
	}
	loc := m.selectedDB
	if cat := normalizedCatalog(m.selectedCatalog); cat != "" {
		loc = cat + "." + m.selectedDB
	}
	objs := m.databaseObjects[m.scopeKey(m.selectedCatalog, m.selectedDB)]
	summary := fmt.Sprintf("context: %s · %d tables", loc, len(objs))
	var mentionText string
	for _, t := range m.currentAISession().turns {
		if t.role == "you" {
			mentionText += " " + t.text
		}
	}
	mentionText += " " + m.input.Value()
	if mentioned := m.aiMentionedTables(mentionText); len(mentioned) > 0 {
		parts := make([]string, 0, len(mentioned))
		for _, name := range mentioned {
			n := len(strings.Split(m.aiTableColumns(name), ", "))
			if m.aiTableColumns(name) == "" {
				n = 0
			}
			parts = append(parts, fmt.Sprintf("@%s(%d cols)", name, n))
		}
		summary += " · " + strings.Join(parts, ", ")
	}
	if p := m.aiProviderLabel(); p != "" {
		summary += " · " + p
	}
	if conv := m.aiConversationLabel(); conv != "" {
		summary += " · " + conv
	}
	return summary
}

// aiProviderLabel names the AI backend in use, marking when none is installed.
func (m *Model) aiProviderLabel() string {
	if p := strings.TrimSpace(m.vault.AIProvider); p != "" {
		return p
	}
	if avail := ai.Available(nil); len(avail) > 0 {
		return avail[0]
	}
	return "no backend"
}

// aiConversationLabel shows the active conversation's title and its position in
// the scope's history (e.g. "〔count users 1/3〕").
func (m *Model) aiConversationLabel() string {
	sessions := m.aiSessionsForScope()
	active := m.currentAISession()
	title := active.title
	if title == "" {
		title = "new conversation"
	}
	for i, s := range sessions {
		if s.id == active.id {
			return fmt.Sprintf("〔%s %d/%d〕", title, i+1, len(sessions))
		}
	}
	return "〔" + title + "〕"
}

func (m *Model) scrollAIToBottom() {
	if m.modal == nil || m.modal.Kind != modalAIChat {
		return
	}
	// A large offset clamps to the maximum, pinning the view to the latest turn.
	m.setModalScroll(len(contentLines(m.aiChatContent())))
}

// buildSchemaContext gives the AI the current database's tables and the columns
// of every @table the user @-mentioned (in this question or earlier turns), or
// the previewed table when nothing was mentioned.
func (m *Model) buildSchemaContext(question string) string {
	if m.selectedDB == "" {
		return ""
	}
	var b strings.Builder
	loc := m.selectedDB
	if cat := normalizedCatalog(m.selectedCatalog); cat != "" {
		loc = cat + "." + m.selectedDB
	}
	b.WriteString("Driver: " + string(m.activeDriver()) + "\n")
	b.WriteString("Current database: " + loc + "\n")
	if objs := m.databaseObjects[m.scopeKey(m.selectedCatalog, m.selectedDB)]; len(objs) > 0 {
		names := make([]string, 0, len(objs))
		for _, o := range objs {
			if o.Name != "" {
				names = append(names, o.Name)
			}
			if len(names) >= 200 {
				break
			}
		}
		b.WriteString("Tables: " + strings.Join(names, ", ") + "\n")
	}
	// Attach columns of every @table the user mentioned (this question + earlier
	// turns) or, with no mention, the table currently being previewed.
	mentionText := question
	for _, t := range m.currentAISession().turns {
		if t.role == "you" {
			mentionText += " " + t.text
		}
	}
	mentioned := m.aiMentionedTables(mentionText)
	if len(mentioned) == 0 && m.target.Name != "" {
		mentioned = []string{m.target.Name}
	}
	for _, name := range mentioned {
		if cols := m.aiTableColumns(name); cols != "" {
			b.WriteString("Columns of " + name + ": " + cols + "\n")
		}
		if part := m.aiTablePartition(name); part != "" {
			b.WriteString("Partitioning of " + name + ": " + part + " — filter on the partition column to avoid scanning every partition\n")
		}
		if notes := m.aiTableFieldNotes(name); notes != "" {
			b.WriteString("Field notes of " + name + " (use these documented values, don't guess): " + notes + "\n")
		}
	}
	return b.String()
}

// aiMentionedTables returns the distinct @table tokens in question that match a
// known table in the current database (case-insensitive, real name returned).
func (m *Model) aiMentionedTables(question string) []string {
	known := map[string]string{} // lower -> real name
	for _, o := range m.databaseObjects[m.scopeKey(m.selectedCatalog, m.selectedDB)] {
		if o.Name != "" {
			known[strings.ToLower(o.Name)] = o.Name
		}
	}
	var out []string
	seen := map[string]bool{}
	for _, tok := range mentionTokens(question) {
		if real, ok := known[strings.ToLower(tok)]; ok && !seen[real] {
			seen[real] = true
			out = append(out, real)
		}
	}
	return out
}

// mentionTokens extracts @<word> tokens from s (without the leading '@').
func mentionTokens(s string) []string {
	var out []string
	for i := 0; i < len(s); i++ {
		if s[i] != '@' {
			continue
		}
		j := i + 1
		for j < len(s) {
			c := s[j]
			if c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
				j++
				continue
			}
			break
		}
		if j > i+1 {
			out = append(out, s[i+1:j])
		}
		i = j
	}
	return out
}

// aiObjectMetadata fetches (and caches for the session) a table's metadata via
// the provider. A miss caches a nil so a bad name is not re-probed every render.
func (m *Model) aiObjectMetadata(name string) *db.ObjectMetadata {
	provider, ok := m.adapter.(db.MetadataProvider)
	if !ok {
		return nil
	}
	target := db.Target{Catalog: normalizedCatalog(m.selectedCatalog), Database: m.selectedDB, Name: name, Type: db.ObjectTable}
	key := strings.Join([]string{string(m.activeDriver()), target.Catalog, target.Database, name}, "\x00")
	if m.aiMetaCache == nil {
		m.aiMetaCache = map[string]*db.ObjectMetadata{}
	}
	if cached, ok := m.aiMetaCache[key]; ok {
		return cached
	}
	ctx, cancel := m.dbContext(context.Background())
	meta, err := provider.Metadata(ctx, target)
	cancel()
	if err != nil {
		m.aiMetaCache[key] = nil
		return nil
	}
	stored := meta
	m.aiMetaCache[key] = &stored
	return &stored
}

// aiTableColumns returns "col type, col type, …" for a table in the current
// database.
func (m *Model) aiTableColumns(name string) string {
	meta := m.aiObjectMetadata(name)
	if meta == nil {
		return ""
	}
	cols := make([]string, 0, len(meta.Fields))
	for _, f := range meta.Fields {
		if f.Name != "" {
			cols = append(cols, strings.TrimSpace(f.Name+" "+f.Type))
		}
	}
	return strings.Join(cols, ", ")
}

// aiTablePartition returns a one-line summary of a table's partition and key
// columns (e.g. "partition RANGE(event_date); key DUPLICATE KEY(id, event_date)")
// so the AI filters on them to avoid scanning every partition. Empty when the
// table is unpartitioned and has no declared key.
func (m *Model) aiTablePartition(name string) string {
	meta := m.aiObjectMetadata(name)
	if meta == nil || meta.Attributes == nil {
		return ""
	}
	var parts []string
	if p := meta.Attributes["partition"]; p != "" {
		parts = append(parts, "partition "+p)
	}
	if k := meta.Attributes["key"]; k != "" {
		parts = append(parts, "key "+k)
	}
	return strings.Join(parts, "; ")
}

// aiTableFieldNotes surfaces column comments (which often document enum-like
// values, e.g. "status: 1=active 2=closed") so the AI uses real domain values
// instead of guessing. Returns "col — comment; col — comment", empty when no
// column is commented.
func (m *Model) aiTableFieldNotes(name string) string {
	meta := m.aiObjectMetadata(name)
	if meta == nil {
		return ""
	}
	var notes []string
	for _, f := range meta.Fields {
		comment := strings.TrimSpace(strings.ReplaceAll(f.Comment, "\n", " "))
		if f.Name == "" || comment == "" {
			continue
		}
		notes = append(notes, f.Name+" — "+comment)
	}
	return strings.Join(notes, "; ")
}

func buildAIPrompt(system string, history []aiTurn, schema, question string) string {
	var b strings.Builder
	b.WriteString(system)
	b.WriteString("\n\n")
	if schema != "" {
		b.WriteString("Schema context:\n")
		b.WriteString(schema)
		b.WriteString("\n")
	}
	for _, t := range history {
		switch t.role {
		case "you":
			b.WriteString("User: " + t.text + "\n")
		case "ai":
			b.WriteString("Assistant: " + t.text + "\n")
		}
	}
	b.WriteString("User: " + question + "\nAssistant:")
	return b.String()
}

func (m *Model) aiChatContent() string {
	width := max(8, clamp(m.workspaceContentWidth(), 32, 84)-6)
	var body string
	switch m.aiListMode {
	case aiListResume:
		body = m.aiResumeContent()
	case aiListProvider:
		body = m.aiProviderContent()
	case aiListHelp:
		body = m.aiHelpContent()
	default:
		body = m.aiChatBody(width)
	}
	// Hard-wrap every logical line to the content width (CJK-aware) so each is a
	// single visual row. This keeps the modal's line-count based scroll/height
	// math (visibleModalBody/sliceLines) in sync with what lipgloss renders, so
	// long replies/input never overflow or ghost across the panel.
	var wrapped []string
	for _, line := range strings.Split(body, "\n") {
		wrapped = append(wrapped, strings.Split(ansi.Hardwrap(line, width, false), "\n")...)
	}
	return strings.Join(wrapped, "\n")
}

// aiChatBody renders the normal conversation view (header, turns, input).
func (m *Model) aiChatBody(width int) string {
	theme := defaultTheme()
	sess := m.currentAISession()
	var b strings.Builder
	b.WriteString(theme.muted.Render(m.aiContextSummary()) + "\n\n")
	if len(sess.turns) == 0 {
		hint := fmt.Sprintf("Ask about your schema or for %s. Type / for commands, @ to attach a table; ctrl+y inserts the reply's command.", aiDialectHint(m.activeDriver()))
		b.WriteString(theme.muted.Render(hint) + "\n\n")
	}
	for _, t := range sess.turns {
		var label string
		var style lipgloss.Style
		switch t.role {
		case "you":
			label, style = "you", theme.accent
		case "err":
			label, style = "err", theme.danger
		default:
			label, style = "ai", theme.sectionTitle
		}
		b.WriteString(style.Render(label+":") + "\n")
		b.WriteString(t.text + "\n\n")
	}
	if sess.pending {
		spin := spinnerFrames[m.loading.frame%len(spinnerFrames)]
		b.WriteString(theme.statusWarn.Render(spin+" ai is thinking…") + "\n\n")
	}
	// The input line is wrapped to width-1 so the trailing cursor cell never
	// pushes past the content width.
	input := ansi.Hardwrap(theme.accent.Render("> ")+m.input.Value(), max(1, width-1), false)
	b.WriteString(input + m.activeInputCursor() + "\n")
	// Slash-command or @-mention suggestions, rendered inline below the input.
	if m.input.SuggestionsVisible() {
		for i, s := range m.input.Suggestions() {
			b.WriteString(renderSuggestionRow(theme, s, width, i == m.input.SelectedIndex()) + "\n")
		}
		if m.aiSuggestKind == aiSuggestCommand {
			b.WriteString(theme.muted.Render("tab/⇧tab choose · enter run"))
		} else {
			b.WriteString(theme.muted.Render("tab/⇧tab choose · enter attach"))
		}
	} else {
		b.WriteString(theme.muted.Render("enter send · / commands · @table attach · ctrl+y insert SQL · esc close"))
	}
	return b.String()
}

// aiResumeContent renders the inline conversation picker for the current scope.
func (m *Model) aiResumeContent() string {
	theme := defaultTheme()
	sessions := m.aiSessionsForScope()
	active := m.currentAISession()
	var b strings.Builder
	b.WriteString(theme.sectionTitle.Render("Resume conversation") + theme.muted.Render(" · "+m.aiDBLabel()) + "\n\n")
	if len(sessions) == 0 {
		b.WriteString(theme.muted.Render("No past conversations for this database.") + "\n\n")
		b.WriteString(theme.muted.Render("esc back"))
		return b.String()
	}
	m.historyIndex = clamp(m.historyIndex, 0, len(sessions)-1)
	for i, s := range sessions {
		marker := "  "
		if i == m.historyIndex {
			marker = "> "
		}
		dot := "  "
		if s.id == active.id {
			dot = "● "
		}
		title := s.title
		if title == "" {
			title = "(empty)"
		}
		row := fmt.Sprintf("%s%s%s · %d msgs · %s", marker, dot, title, len(s.turns), relativeTime(s.updatedAt))
		b.WriteString(selectedRow(theme, row, i == m.historyIndex) + "\n")
	}
	b.WriteString("\n" + theme.muted.Render("↑↓ select · enter resume · d delete · esc back"))
	return b.String()
}

// aiProviderContent renders the inline backend picker (claude/codex).
func (m *Model) aiProviderContent() string {
	theme := defaultTheme()
	providers := ai.KnownProviders()
	installed := map[string]bool{}
	for _, p := range ai.Available(nil) {
		installed[p] = true
	}
	var b strings.Builder
	b.WriteString(theme.sectionTitle.Render("AI backend") + "\n\n")
	m.historyIndex = clamp(m.historyIndex, 0, max(0, len(providers)-1))
	for i, p := range providers {
		marker := "  "
		if i == m.historyIndex {
			marker = "> "
		}
		dot := "  "
		if p == m.vault.AIProvider {
			dot = "● "
		}
		note := theme.muted.Render(" (not found on PATH)")
		if installed[p] {
			note = theme.muted.Render(" (installed)")
		}
		b.WriteString(selectedRow(theme, marker+dot+p, i == m.historyIndex) + note + "\n")
	}
	b.WriteString("\n" + theme.muted.Render("↑↓ select · enter use · esc back"))
	return b.String()
}

// aiHelpContent lists the AI panel's slash commands.
func (m *Model) aiHelpContent() string {
	theme := defaultTheme()
	var b strings.Builder
	b.WriteString(theme.sectionTitle.Render("AI panel commands") + "\n\n")
	for _, c := range aiSlashCommands {
		name := c.name
		if len(c.aliases) > 0 {
			name += " (" + strings.Join(c.aliases, ", ") + ")"
		}
		b.WriteString(theme.accent.Render(name) + " — " + c.detail + "\n")
	}
	b.WriteString("\n" + theme.muted.Render("@table attach schema · ctrl+y insert SQL · esc back"))
	return b.String()
}

// relativeTime renders t as a compact age such as "just now", "5m", "3h", "2d".
func relativeTime(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}
