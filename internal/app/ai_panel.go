package app

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"tdb/internal/ai"
	"tdb/internal/db"
	"tdb/internal/suggest"
)

// aiTurn is one message in an AI conversation. role is "you", "ai", or "err".
type aiTurn struct {
	role string
	text string
}

// aiSession is the per-database conversation: history plus the SQL blocks from
// the latest reply (for one-key insertion into the query editor).
type aiSession struct {
	turns    []aiTurn
	lastSQLs []string
	pending  bool
}

// aiSessionKey scopes a conversation to the active connection + catalog/database
// so switching databases (or connections) gives an independent chat history.
func (m *Model) aiSessionKey() string {
	return m.activeProfileID() + "\x00" + m.scopeKey(m.selectedCatalog, m.selectedDB)
}

func (m *Model) currentAISession() *aiSession {
	if m.aiSessions == nil {
		m.aiSessions = map[string]*aiSession{}
	}
	key := m.aiSessionKey()
	if m.aiSessions[key] == nil {
		m.aiSessions[key] = &aiSession{}
	}
	return m.aiSessions[key]
}

func (m *Model) openAIChatModal() {
	m.input.Clear()
	m.currentAISession() // ensure it exists
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
		if m.input.SuggestionsVisible() {
			m.acceptAIMention()
		} else {
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
		m.refreshAIMention()
	default:
		if len(msg.Runes) > 0 {
			m.input.Insert(string(msg.Runes))
			m.refreshAIMention()
		}
	}
	return true
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

// refreshAIMention shows table-name suggestions when the input ends with @<word>.
func (m *Model) refreshAIMention() {
	prefix, ok := aiMentionPrefix(m.input.Value())
	if !ok {
		m.input.HideSuggestions()
		return
	}
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
		m.input.Clear()
		m.scrollAIToBottom()
		return
	}
	prompt := buildAIPrompt(sess.turns, m.buildSchemaContext(question), question)
	sess.turns = append(sess.turns, aiTurn{role: "you", text: question})
	sess.pending = true
	m.input.Clear()
	m.scrollAIToBottom()
	key := m.aiSessionKey()
	m.nextCmd = m.runAsync("AI…", func(ctx context.Context) tea.Msg {
		reply, askErr := provider.Ask(ctx, prompt)
		return asyncResultMsg{apply: func(m *Model) {
			s := m.aiSessions[key]
			if s == nil {
				return
			}
			s.pending = false
			if askErr != nil {
				s.turns = append(s.turns, aiTurn{role: "err", text: askErr.Error()})
			} else {
				s.turns = append(s.turns, aiTurn{role: "ai", text: reply})
				if blocks := ai.ExtractSQLBlocks(reply); len(blocks) > 0 {
					s.lastSQLs = blocks
				}
			}
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
	return summary
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

// aiTableColumns returns "col type, col type, …" for a table in the current
// database, fetched via the metadata provider and cached for the session.
func (m *Model) aiTableColumns(name string) string {
	provider, ok := m.adapter.(db.MetadataProvider)
	if !ok {
		return ""
	}
	target := db.Target{Catalog: normalizedCatalog(m.selectedCatalog), Database: m.selectedDB, Name: name, Type: db.ObjectTable}
	key := strings.Join([]string{string(m.activeDriver()), target.Catalog, target.Database, name}, "\x00")
	if m.aiSchemaCache == nil {
		m.aiSchemaCache = map[string]string{}
	}
	if cached, ok := m.aiSchemaCache[key]; ok {
		return cached
	}
	ctx, cancel := m.dbContext(context.Background())
	meta, err := provider.Metadata(ctx, target)
	cancel()
	if err != nil {
		m.aiSchemaCache[key] = "" // cache the miss so a bad name is not re-probed
		return ""
	}
	cols := make([]string, 0, len(meta.Fields))
	for _, f := range meta.Fields {
		if f.Name != "" {
			cols = append(cols, strings.TrimSpace(f.Name+" "+f.Type))
		}
	}
	joined := strings.Join(cols, ", ")
	m.aiSchemaCache[key] = joined
	return joined
}

func buildAIPrompt(history []aiTurn, schema, question string) string {
	var b strings.Builder
	b.WriteString("You are a database assistant embedded in a terminal DB client (TDB). ")
	b.WriteString("Answer concisely. When you produce a SQL statement, put it in a ```sql fenced code block.\n\n")
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
	theme := defaultTheme()
	width := max(8, clamp(m.workspaceContentWidth(), 32, 84)-6)
	sess := m.currentAISession()
	var b strings.Builder
	b.WriteString(theme.muted.Render(m.aiContextSummary()) + "\n\n")
	if len(sess.turns) == 0 {
		b.WriteString(theme.muted.Render("Ask about your schema or for SQL. Type @ to attach a table; ctrl+y inserts the reply's SQL.") + "\n\n")
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
	// @-mention table suggestions, rendered inline below the input.
	if m.input.SuggestionsVisible() {
		for i, s := range m.input.Suggestions() {
			b.WriteString(renderSuggestionRow(theme, s, width, i == m.input.SelectedIndex()) + "\n")
		}
		b.WriteString(theme.muted.Render("tab/⇧tab choose · enter attach"))
	} else {
		b.WriteString(theme.muted.Render("enter send · @table attach · ctrl+y insert SQL · esc close"))
	}
	// Hard-wrap every logical line to the content width (CJK-aware) so each is a
	// single visual row. This keeps the modal's line-count based scroll/height
	// math (visibleModalBody/sliceLines) in sync with what lipgloss renders, so
	// long replies/input never overflow or ghost across the panel.
	var wrapped []string
	for _, line := range strings.Split(b.String(), "\n") {
		wrapped = append(wrapped, strings.Split(ansi.Hardwrap(line, width, false), "\n")...)
	}
	return strings.Join(wrapped, "\n")
}
