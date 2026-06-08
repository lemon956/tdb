package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"tdb/internal/config"
	"tdb/internal/db"
	"tdb/internal/db/mongoadapter"
	"tdb/internal/db/redisadapter"
	"tdb/internal/db/sqladapter"
	"tdb/internal/history"
	"tdb/internal/result"
	"tdb/internal/suggest"
)

type Page string

const (
	PageUnlock      Page = "unlock"
	PageConnections Page = "connections"
	PageBrowser     Page = "browser"
	PageData        Page = "data"
	PageQuery       Page = "query"
	PageHistory     Page = "history"
	PageHelp        Page = "help"
)

type Options struct {
	ConfigPath      string
	HistoryPath     string
	IconStyle       IconStyle
	ClipboardWriter io.Writer
	ClipboardCopier ClipboardCopier
}

type Model struct {
	options     Options
	store       *config.Store
	history     *history.Store
	vault       config.Vault
	master      string
	openAdapter func(config.Profile) (db.Adapter, error)

	page                 Page
	input                CommandInput
	unlockCommand        bool   // unlock screen: typing a command (quit-only)
	unlockPasswordDraft  string // password preserved while in unlock command mode
	message              string
	lastCopiedText       string
	form                 *connectionForm
	helpOpen             bool
	errBox               *errorBox
	modal                *modalState
	lastClickID          string
	lastClickAt          time.Time
	width                int
	height               int
	sidebarWidthOverride int
	resizingSidebar      bool
	focus                Focus
	previousFocus        Focus
	modalPreviousFocus   Focus
	hitboxes             HitboxRegistry
	icons                IconSet

	activeProfile       *config.Profile
	adapter             db.Adapter
	databases           []string
	objects             []db.Object
	databaseObjects     map[string][]db.Object
	expandedDBs         map[string]bool
	expandedMeta        map[string]bool
	selectedDB          string
	connectionIndex     int
	databaseIndex       int
	objectIndex         int
	browserCursor       int
	navVerticalOffset   int
	navHorizontalOffset int
	navSearchActive     bool
	navSearchQuery      string
	navSearchMatchIndex int
	target              db.Target
	result              result.Set
	resultView          ResultView
	workspaceMode       workspaceMode
	workspaceTabs       []workspaceTab
	activeTabIndex      int
	nextQueryTabID      int
	queryFieldCache     map[string][]suggest.Field
	helpSearch          string
	redisCursor         uint64
	redisPattern        string
	historyIndex        int
	queryTimeout        time.Duration      // 0 = no timeout (Esc to cancel)
	cancelOp            context.CancelFunc // cancels the in-flight async op
	cursorBlinkOn       bool
	pending             *pendingAction
	loading             loadingState
	nextCmd             tea.Cmd

	sessions      []connSession // one per open connection
	activeSession int           // index into sessions, or -1 for the manager
}

type pendingAction struct {
	Kind      string
	ProfileID string
	Target    db.Target
	Key       db.Key
	Values    map[string]any
}

type errorBox struct {
	Title   string
	Message string
}

func NewModel(options Options) *Model {
	if options.HistoryPath == "" && options.ConfigPath != "" {
		options.HistoryPath = options.ConfigPath + ".history.json"
	}
	iconStyle := options.IconStyle
	if iconStyle == "" {
		iconStyle = ResolveIconStyle(IconDetectOptions{})
	}
	return &Model{
		options:       options,
		store:         config.NewStore(options.ConfigPath),
		history:       history.NewStore(options.HistoryPath),
		openAdapter:   OpenAdapter,
		page:          PageUnlock,
		input:         NewCommandInput(),
		focus:         FocusMain,
		icons:         IconSetForStyle(iconStyle),
		redisPattern:  "*",
		cursorBlinkOn: true,
		activeSession: -1,
		queryTimeout:  defaultQueryTimeout,
	}
}

func (m *Model) Init() tea.Cmd {
	return tea.Batch(cursorBlinkCmd(), spinnerTickCmd())
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case blinkMsg:
		m.cursorBlinkOn = !m.cursorBlinkOn
		return m, cursorBlinkCmd()
	case spinnerTickMsg:
		// The tick runs continuously; the frame only matters while loading.
		if m.loading.active {
			m.loading.frame++
		}
		return m, spinnerTickCmd()
	case asyncResultMsg:
		msg.apply(m)
		m.finishLoading()
		return m, m.takeCmd()
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	case tea.MouseMsg:
		m.handleMouse(context.Background(), msg)
		return m, m.takeCmd()
	case tea.KeyMsg:
		// Ctrl+C intentionally does NOT quit; the only way to exit is the ":q"
		// command (see handleGlobalCommand).
		// The unlock screen owns its keys: typing the password, plus a quit-only
		// command sub-mode invoked with ":".
		if m.page == PageUnlock {
			if cmd, handled := m.handleUnlockKey(msg); handled {
				return m, cmd
			}
		}
		// While an async DB op runs, Esc aborts it (only the ":q" command quits).
		if m.loading.active && msg.String() == "esc" {
			if m.cancelActiveOp() {
				return m, nil
			}
		}
		// Connection-tab switching (Alt+arrows / Alt+digits) works from anywhere.
		if m.handleConnectionTabKey(msg) {
			return m, m.takeCmd()
		}
		if m.handleOverlayKey(msg) {
			return m, m.takeCmd()
		}
		if m.errBox != nil {
			switch msg.String() {
			case "esc", "enter":
				m.closeErrorBox()
				return m, nil
			}
		}
		if m.helpOpen {
			switch msg.String() {
			case "esc", "enter":
				m.closeHelpPanel()
				return m, nil
			}
		}
		if m.form != nil {
			m.handleConnectionFormKey(context.Background(), msg)
			return m, nil
		}
		if m.modal != nil && m.modal.Kind == modalHistory {
			switch msg.String() {
			case "esc", "enter":
				m.closeModal()
				return m, nil
			case "up", "k":
				m.moveHistorySelection(-1)
				return m, nil
			case "down", "j":
				m.moveHistorySelection(1)
				return m, nil
			}
		}
		if m.handleWorkspaceTabShortcut(msg) {
			return m, nil
		}
		if m.handleWorkspaceVimKey(context.Background(), msg) {
			return m, m.takeCmd()
		}
		switch msg.String() {
		case "esc":
			if m.focus == FocusCommand && !m.navSearchActive {
				m.exitCommandMode()
				return m, nil
			}
			if m.input.SuggestionsVisible() {
				m.input.HideSuggestions()
				return m, nil
			}
			if m.navSearchActive {
				m.clearNavigationSearch()
				return m, nil
			}
			m.back()
		case "tab":
			if m.focus == FocusCommand {
				if m.input.SuggestionsVisible() {
					m.input.NextSuggestion()
				} else {
					m.openCommandSuggestions()
				}
			} else if !m.input.AcceptSuggestion() {
				m.cycleFocus(1)
			}
		case "shift+tab":
			m.cycleFocus(-1)
		case "shift+left":
			if m.navigationSidebarFocused() {
				m.scrollNavigationHorizontal(-4)
			}
		case "shift+right":
			if m.navigationSidebarFocused() {
				m.scrollNavigationHorizontal(4)
			}
		case "ctrl+space":
			m.input.ToggleSuggestions(m.suggestions())
		case "up":
			if m.input.SuggestionsVisible() {
				m.input.PreviousSuggestion()
			} else if m.focus == FocusMain && m.activeWorkspaceResultAvailable() {
				m.scrollActiveWorkspaceRows(-1)
			} else if m.page == PageConnections || m.page == PageBrowser || m.page == PageData || m.page == PageQuery || m.page == PageHistory {
				m.moveSelection(-1)
			}
		case "down":
			if m.input.SuggestionsVisible() {
				m.input.NextSuggestion()
			} else if m.focus == FocusMain && m.activeWorkspaceResultAvailable() {
				m.scrollActiveWorkspaceRows(1)
			} else if m.page == PageConnections || m.page == PageBrowser || m.page == PageData || m.page == PageQuery || m.page == PageHistory {
				m.moveSelection(1)
			}
		case "left":
			if m.focus == FocusMain && m.activeWorkspaceResultAvailable() {
				m.scrollActiveWorkspaceColumns(-1)
			} else if m.page == PageData {
				m.resultView.ScrollColumns(-1, m.resultColumnCount())
				m.syncActiveTabFromModel()
			} else if !m.isTextEntryMode() {
				m.movePanelFocus(-1)
			}
		case "right":
			if m.focus == FocusMain && m.activeWorkspaceResultAvailable() {
				m.scrollActiveWorkspaceColumns(1)
			} else if m.page == PageData {
				m.resultView.ScrollColumns(1, m.resultColumnCount())
				m.syncActiveTabFromModel()
			} else if !m.isTextEntryMode() {
				m.movePanelFocus(1)
			}
		case "pgup":
			if m.focus == FocusMain && m.activeWorkspaceResultAvailable() {
				m.scrollActiveWorkspaceRows(-10)
			} else if m.page == PageData {
				m.resultView.ScrollRows(-10, m.resultRowCount())
				m.syncActiveTabFromModel()
			}
		case "pgdown":
			if m.focus == FocusMain && m.activeWorkspaceResultAvailable() {
				m.scrollActiveWorkspaceRows(10)
			} else if m.page == PageData {
				m.resultView.ScrollRows(10, m.resultRowCount())
				m.syncActiveTabFromModel()
			}
		case "enter":
			if m.focus == FocusCommand && m.input.SuggestionsVisible() {
				m.input.AcceptSuggestion()
				return m, nil
			}
			if m.navSearchActive {
				m.syncNavigationSearchInput()
				m.jumpNavigationSearch(false)
				m.focus = FocusSidebar
				return m, nil
			}
			if m.page == PageHistory && m.input.Value() == "" {
				m.refillSelectedHistory()
				return m, nil
			}
			if !m.isTextEntryMode() && m.input.Value() == "" {
				m.runPageAction(context.Background(), actionOpen)
				return m, m.takeCmd()
			}
			m.HandleLine(context.Background(), m.input.Value())
			m.input.Clear()
			if m.focus == FocusCommand {
				m.restorePreviousFocus()
			}
		case "backspace", "ctrl+h":
			if m.focus == FocusCommand && !m.navSearchActive && m.input.Value() == "" {
				m.exitCommandMode()
				return m, nil
			}
			m.input.Backspace()
			m.syncNavigationSearchInput()
			if m.helpOpen {
				m.syncHelpSearch()
			}
			m.refreshSuggestions()
		default:
			if m.handleRuneKey(context.Background(), msg.String()) {
				return m, m.takeCmd()
			}
			if len(msg.Runes) > 0 && m.isTextEntryMode() {
				m.input.Insert(string(msg.Runes))
				m.syncNavigationSearchInput()
				if m.helpOpen {
					m.syncHelpSearch()
				}
				m.refreshSuggestions()
				return m, nil
			}
			if len(msg.Runes) > 0 {
				m.message = "unsupported shortcut: " + msg.String()
			}
		}
	}
	return m, m.takeCmd()
}

func (m *Model) handleRuneKey(ctx context.Context, key string) bool {
	switch key {
	case "?":
		m.openHelpPanel()
		return true
	case "/":
		if !m.isTextEntryMode() && (m.page == PageBrowser || m.page == PageData || m.page == PageQuery) && m.focus == FocusSidebar {
			m.navSearchActive = true
			m.navSearchQuery = ""
			m.input.Clear()
			m.focus = FocusCommand
			m.message = "navigation search"
			return true
		}
	case ":":
		m.navSearchActive = false
		m.focusCommand()
		m.message = "command ready"
		return true
	case "q":
		if !m.isTextEntryMode() {
			m.back()
			return true
		}
	case "h":
		if !m.isTextEntryMode() {
			m.movePanelFocus(-1)
			return true
		}
	case "l":
		if !m.isTextEntryMode() {
			m.movePanelFocus(1)
			return true
		}
	case "H":
		if !m.isTextEntryMode() && m.navigationSidebarFocused() {
			m.scrollNavigationHorizontal(-4)
			return true
		}
	case "L":
		if !m.isTextEntryMode() && m.navigationSidebarFocused() {
			m.scrollNavigationHorizontal(4)
			return true
		}
	case "j":
		if !m.isTextEntryMode() {
			m.moveSelection(1)
			return true
		}
	case "k":
		if !m.isTextEntryMode() {
			m.moveSelection(-1)
			return true
		}
	case "r":
		if m.page == PageHistory && m.input.Value() == "" {
			m.replaySelectedHistory(ctx)
			return true
		}
		if !m.isTextEntryMode() {
			m.runPageAction(ctx, actionRefresh)
			return true
		}
	case "n":
		if m.navSearchActive && !m.isTextEntryMode() {
			m.jumpNavigationSearch(true)
			return true
		}
		if !m.isTextEntryMode() {
			m.runPageAction(ctx, actionNew)
			return true
		}
	case "e":
		if !m.isTextEntryMode() {
			m.runPageAction(ctx, actionEdit)
			return true
		}
	case "d":
		if !m.isTextEntryMode() {
			m.runPageAction(ctx, actionDelete)
			return true
		}
	case "t":
		if !m.isTextEntryMode() {
			m.runPageAction(ctx, actionTest)
			return true
		}
	case "i":
		if !m.isTextEntryMode() {
			m.runPageAction(ctx, actionNew)
			return true
		}
	}
	return false
}

func (m *Model) View() string {
	return m.renderLayout()
}

func (m *Model) legacyView() string {
	var b strings.Builder
	b.WriteString("TDB - terminal database browser\n")
	b.WriteString("Page: " + string(m.page) + "\n")
	if m.message != "" {
		b.WriteString("Status: " + m.message + "\n")
	}
	b.WriteString(strings.Repeat("-", 72) + "\n")

	switch m.page {
	case PageUnlock:
		b.WriteString("Enter master password to unlock or initialize the encrypted config.\n")
		b.WriteString("Password: " + strings.Repeat("*", len(m.input.Value())) + "\n")
	case PageConnections:
		m.viewConnections(&b)
	case PageBrowser:
		m.viewBrowser(&b)
	case PageData:
		m.viewData(&b)
	case PageQuery:
		m.viewQuery(&b)
	case PageHistory:
		m.viewHistory(&b)
	default:
		m.viewHelp(&b)
	}

	if m.pending != nil {
		b.WriteString("\nConfirm " + m.pending.Kind + " with `yes`, cancel with `no`.\n")
		b.WriteString(pendingSummary(m.pending) + "\n")
	}
	if m.page != PageUnlock {
		b.WriteString("\nCommand: " + m.input.Value() + "\n")
		m.viewSuggestions(&b)
		b.WriteString("Type `help` for commands. Esc goes back. :q quits.\n")
	}
	return b.String()
}

func (m *Model) HandleLine(ctx context.Context, line string) {
	line = strings.TrimSpace(line)
	// ":q" quits from anywhere, including the unlock screen where there is no
	// command line (the password input would otherwise swallow it). The command
	// line itself routes the colon-less "q"/"quit"/"exit" via handleGlobalCommand.
	if line == ":q" || line == ":quit" || line == ":exit" {
		m.nextCmd = tea.Quit
		m.message = "bye"
		return
	}
	if m.pending != nil {
		m.handleConfirmation(ctx, strings.ToLower(line))
		return
	}
	if m.page == PageUnlock {
		m.unlock(line)
		return
	}
	if line == "" {
		return
	}
	if m.handleGlobalCommand(ctx, line) {
		return
	}
	if tab := m.activeWorkspaceTab(); tab != nil && tab.Kind == workspaceTabQuery {
		m.executeQueryInActiveTab(ctx, line)
		return
	}
	if m.adapter != nil && looksExecutableStatement(line) {
		m.openQueryWorkspaceTab()
		m.executeQueryInActiveTab(ctx, line)
		return
	}

	switch m.page {
	case PageConnections:
		m.handleConnections(ctx, line)
	case PageBrowser:
		m.handleBrowser(ctx, line)
	case PageData:
		m.handleData(ctx, line)
	case PageQuery:
		m.handleQuery(ctx, line)
	case PageHistory:
		m.handleHistory(ctx, line)
	default:
		m.page = PageConnections
	}
}

func (m *Model) handleGlobalCommand(ctx context.Context, line string) bool {
	parts := splitLine(line)
	if len(parts) == 0 {
		return true
	}
	command := strings.ToLower(parts[0])
	switch command {
	case "help", "?":
		m.openHelpPanel()
	case "new":
		if len(parts) == 1 {
			m.openConnectionForm()
			return true
		}
		m.createProfile(parts[1:])
	case "edit":
		if len(parts) == 1 {
			if profile, ok := m.selectedProfile(); ok {
				m.openEditConnectionForm(profile)
			} else {
				m.message = "no connection selected"
			}
			return true
		}
		if len(parts) == 2 && !strings.Contains(parts[1], "=") {
			if profile, ok := m.vault.GetProfile(parts[1]); ok {
				m.openEditConnectionForm(profile)
			} else {
				m.message = "profile not found"
			}
			return true
		}
		m.editProfile(parts[1:])
	case "delete":
		if len(parts) == 1 {
			if m.page == PageData && m.target.Name != "" {
				m.pending = &pendingAction{Kind: "delete", Target: m.target}
				m.modal = &modalState{Kind: modalConfirm, Title: "Confirm"}
				m.focusModal()
				m.message = "delete requires confirmation"
				return true
			}
			if profile, ok := m.selectedProfile(); ok {
				m.pending = &pendingAction{Kind: "delete-profile", ProfileID: profile.ID}
				m.modal = &modalState{Kind: modalConfirm, Title: "Confirm"}
				m.focusModal()
				m.message = "delete requires confirmation"
			} else {
				m.message = "no connection selected"
			}
			return true
		}
		if _, ok := m.vault.GetProfile(parts[1]); ok {
			m.pending = &pendingAction{Kind: "delete-profile", ProfileID: parts[1]}
			m.modal = &modalState{Kind: modalConfirm, Title: "Confirm"}
			m.focusModal()
			m.message = "delete requires confirmation"
			return true
		}
		return false
	case "open":
		if len(parts) < 2 {
			m.runPageAction(ctx, actionOpen)
			return true
		}
		if _, ok := m.vault.GetProfile(parts[1]); ok || m.page == PageConnections {
			m.openProfile(ctx, parts[1])
			return true
		}
		m.openObject(ctx, parts[1])
	case "test":
		if len(parts) < 2 {
			if profile, ok := m.selectedProfile(); ok {
				m.testProfile(ctx, profile.ID)
			} else {
				m.message = "usage: test <profile-id>"
			}
			return true
		}
		m.testProfile(ctx, parts[1])
	case "history":
		m.openHistoryModal()
	case "query":
		m.openQueryWorkspaceTab()
	case "connections", "profiles":
		m.enterConnectionsManager()
		m.message = "connections"
	case "refresh":
		m.runPageAction(ctx, actionRefresh)
	case "back":
		m.back()
	case "export":
		m.exportResult(parts[1:])
	case "copy":
		m.copyResultAs(parts[1:])
	case "timeout":
		if len(parts) < 2 {
			m.message = fmt.Sprintf("query timeout: %s (0 = none, Esc to cancel)", m.queryTimeout)
			return true
		}
		secs, err := strconv.Atoi(parts[1])
		if err != nil || secs < 0 {
			m.message = "usage: timeout <seconds>  (0 = no timeout)"
			return true
		}
		m.queryTimeout = time.Duration(secs) * time.Second
		if secs == 0 {
			m.message = "query timeout disabled (Esc to cancel)"
		} else {
			m.message = fmt.Sprintf("query timeout set to %ds", secs)
		}
	case "q", "quit", "exit":
		// The only way to exit the program. takeCmd() forwards this to Bubble Tea
		// after HandleLine returns.
		m.nextCmd = tea.Quit
		m.message = "bye"
	case "next":
		m.pageNext(ctx)
	case "prev", "previous":
		m.pagePrev(ctx)
	case "db", "/":
		m.handleBrowser(ctx, line)
	default:
		return false
	}
	return true
}

func looksExecutableStatement(line string) bool {
	lower := strings.ToLower(strings.TrimSpace(line))
	if lower == "" {
		return false
	}
	if strings.HasPrefix(lower, "{") || strings.HasPrefix(lower, "[") {
		return true
	}
	for _, prefix := range []string{
		"select ", "show ", "describe ", "desc ", "explain ",
		"insert ", "update ", "delete ", "with ",
		"get ", "set ", "del ", "hget ", "hgetall ", "scan ", "lrange ",
	} {
		if strings.HasPrefix(lower, prefix) || lower == strings.TrimSpace(prefix) {
			return true
		}
	}
	return false
}

// handleUnlockKey owns all key handling on the password screen. Normally keys
// edit the password; pressing ":" opens a quit-only command sub-mode so the user
// can exit before unlocking (Ctrl+C no longer quits). Only ":q"/"q"/"quit"/"exit"
// are honored there — every other command is denied.
func (m *Model) handleUnlockKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	if m.unlockCommand {
		switch msg.String() {
		case "esc":
			m.exitUnlockCommand()
		case "enter":
			cmd := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(m.input.Value()), ":"))
			switch strings.ToLower(cmd) {
			case "q", "quit", "exit":
				m.nextCmd = tea.Quit
				return tea.Quit, true
			default:
				m.exitUnlockCommand()
				m.message = "locked: only :q is available before unlocking"
			}
		case "backspace", "ctrl+h":
			if m.input.Value() == "" {
				m.exitUnlockCommand()
			} else {
				m.input.Backspace()
			}
		default:
			if len(msg.Runes) > 0 {
				m.input.Insert(string(msg.Runes))
			}
		}
		return nil, true
	}

	switch msg.String() {
	case ":":
		// Enter the quit-only command sub-mode, preserving any typed password.
		m.unlockPasswordDraft = m.input.Value()
		m.input.Clear()
		m.unlockCommand = true
		m.message = "command (locked): only :q to quit, Esc to go back"
	case "enter":
		m.unlock(m.input.Value())
		m.input.Clear()
	case "backspace", "ctrl+h":
		m.input.Backspace()
	case "esc":
		// nothing to go back to before unlocking
	default:
		if len(msg.Runes) > 0 {
			m.input.Insert(string(msg.Runes))
		}
	}
	return nil, true
}

func (m *Model) exitUnlockCommand() {
	m.unlockCommand = false
	m.input.SetValue(m.unlockPasswordDraft)
	m.unlockPasswordDraft = ""
}

func (m *Model) unlock(master string) {
	if master == "" {
		m.message = "master password is required"
		return
	}
	vault, err := m.store.Load(master)
	if err != nil {
		m.message = err.Error()
		return
	}
	m.master = master
	m.vault = vault
	m.page = PageConnections
	m.focus = FocusSidebar
	m.message = "unlocked"
}

func (m *Model) handleConnections(ctx context.Context, line string) {
	parts := splitLine(line)
	if len(parts) == 0 {
		return
	}
	switch parts[0] {
	case "new":
		m.createProfile(parts[1:])
	case "edit":
		m.editProfile(parts[1:])
	case "delete":
		if len(parts) < 2 {
			m.message = "usage: delete <profile-id>"
			return
		}
		m.pending = &pendingAction{Kind: "delete-profile", ProfileID: parts[1]}
		m.message = "delete requires confirmation"
	case "open":
		if len(parts) < 2 {
			m.message = "usage: open <profile-id>"
			return
		}
		m.openProfile(ctx, parts[1])
	case "test":
		if len(parts) < 2 {
			m.message = "usage: test <profile-id>"
			return
		}
		m.testProfile(ctx, parts[1])
	case "history":
		m.page = PageHistory
		m.historyIndex = 0
	default:
		m.message = fmt.Sprintf("unknown command: %s", parts[0])
	}
}

func (m *Model) createProfile(parts []string) {
	if len(parts) == 0 {
		m.message = "usage: new <mysql|doris|mongo|redis> ..."
		return
	}
	driver := config.Driver(parts[0])
	if driver == config.DriverMongo {
		m.createMongoProfile(parts[1:])
		return
	}
	if len(parts) < 7 {
		m.message = "usage: new <mysql|doris|redis> <id> <host> <port> <user> <password> <database|redis-db> [readonly]"
		return
	}
	form := newConnectionForm()
	form.chooseDriver(driver)
	form.setFieldValue("id", parts[1])
	form.setFieldValue("host", parts[2])
	form.setFieldValue("port", parts[3])
	form.setFieldValue("user", parts[4])
	form.setFieldValue("password", parts[5])
	if driver == config.DriverRedis {
		form.setFieldValue("db", parts[6])
	} else {
		form.setFieldValue("database", parts[6])
	}
	if len(parts) > 7 && parts[7] == "readonly" {
		form.readOnly = true
	}
	profile, err := form.buildProfile()
	if err != nil {
		m.message = err.Error()
		return
	}
	m.vault.UpsertProfile(profile)
	m.saveVault("profile saved")
}

func (m *Model) createMongoProfile(parts []string) {
	if len(parts) < 2 {
		m.message = "usage: new mongo <id> <mongodb-uri> [database] [readonly]"
		return
	}
	form := newConnectionForm()
	form.chooseDriver(config.DriverMongo)
	form.setFieldValue("id", parts[0])
	form.setFieldValue("uri", parts[1])
	for _, part := range parts[2:] {
		if part == "readonly" {
			form.readOnly = true
			continue
		}
		form.setFieldValue("database", part)
	}
	profile, err := form.buildProfile()
	if err != nil {
		m.message = err.Error()
		return
	}
	m.vault.UpsertProfile(profile)
	m.saveVault("profile saved")
}

func (m *Model) editProfile(parts []string) {
	if len(parts) < 2 {
		m.message = "usage: edit <profile-id> field=value ..."
		return
	}
	profile, ok := m.vault.GetProfile(parts[0])
	if !ok {
		m.message = "profile not found"
		return
	}
	for _, assignment := range parts[1:] {
		field, value, ok := strings.Cut(assignment, "=")
		if !ok {
			continue
		}
		switch field {
		case "name":
			profile.Name = value
		case "host":
			profile.Host = value
		case "port":
			port, err := strconv.Atoi(value)
			if err != nil {
				m.message = "invalid port: " + value
				return
			}
			profile.Port = port
		case "user":
			profile.User = value
		case "password":
			profile.Password = value
		case "database":
			profile.Database = value
		case "authdb":
			profile.AuthDB = value
		case "redisdb":
			redisDB, err := strconv.Atoi(value)
			if err != nil {
				m.message = "invalid redis db: " + value
				return
			}
			profile.RedisDB = redisDB
		case "readonly":
			profile.ReadOnly = value == "true" || value == "1" || value == "yes"
		}
	}
	m.vault.UpsertProfile(profile)
	m.saveVault("profile updated")
}

func (m *Model) openProfile(ctx context.Context, id string) {
	profile, ok := m.vault.GetProfile(id)
	if !ok {
		m.message = "profile not found"
		return
	}
	// Deduplicate: if this connection is already open, switch to its session.
	if idx := m.sessionIndexForProfile(id); idx >= 0 {
		m.switchSession(idx)
		return
	}
	adapter, err := m.openAdapter(profile)
	if err != nil {
		m.message = err.Error()
		return
	}
	// Open as a new connection session (preserving any current one).
	m.saveActiveSession()
	m.sessions = append(m.sessions, connSession{
		profile:         profile,
		adapter:         adapter,
		selectedDB:      profile.Database,
		databaseObjects: map[string][]db.Object{},
		expandedDBs:     map[string]bool{},
		expandedMeta:    map[string]bool{},
		redisPattern:    "*",
		page:            PageBrowser,
		focus:           FocusSidebar,
	})
	m.loadSession(len(m.sessions) - 1)
	m.refreshBrowser(ctx)
}

func (m *Model) testProfile(ctx context.Context, id string) {
	profile, ok := m.vault.GetProfile(id)
	if !ok {
		m.message = "profile not found"
		return
	}
	adapter, err := m.openAdapter(profile)
	if err != nil {
		m.message = err.Error()
		return
	}
	defer adapter.Close()
	ctx, cancel := m.dbContext(ctx)
	defer cancel()
	if err := adapter.Test(ctx); err != nil {
		m.message = "test failed: " + err.Error()
		return
	}
	m.message = "test ok"
}

func (m *Model) handleBrowser(ctx context.Context, line string) {
	parts := splitLine(line)
	if len(parts) == 0 {
		return
	}
	switch parts[0] {
	case "back":
		m.back()
	case "refresh":
		m.refreshBrowser(ctx)
	case "db":
		if len(parts) < 2 {
			m.message = "usage: db <database>"
			return
		}
		index := indexOfString(m.databases, parts[1])
		if index < 0 {
			m.message = "database not found: " + parts[1]
			return
		}
		m.selectDatabase(ctx, index)
	case "open":
		if len(parts) < 2 {
			m.message = "usage: open <object-or-key>"
			return
		}
		m.openObject(ctx, parts[1])
	case "next":
		m.nextRedisScan(ctx)
	case "/":
		if len(parts) < 2 {
			m.message = "usage: / <redis-pattern>"
			return
		}
		m.redisPattern = parts[1]
		m.redisCursor = 0
		m.nextRedisScan(ctx)
	case "query":
		m.page = PageQuery
	case "history":
		m.page = PageHistory
		m.historyIndex = 0
	default:
		m.message = fmt.Sprintf("unknown command: %s", parts[0])
	}
}

func (m *Model) handleData(ctx context.Context, line string) {
	parts := splitLine(line)
	if len(parts) == 0 {
		return
	}
	switch parts[0] {
	case "back":
		m.page = PageBrowser
	case "query":
		m.page = PageQuery
	case "history":
		m.page = PageHistory
		m.historyIndex = 0
	case "refresh":
		m.openObject(ctx, m.target.Name)
	case "insert":
		values, err := parseJSONMap(strings.TrimSpace(strings.TrimPrefix(line, "insert")))
		if err != nil {
			m.showErrorBox("Insert failed", err)
			return
		}
		m.pending = &pendingAction{Kind: "insert", Target: m.target, Values: values}
	case "update":
		if len(parts) < 3 {
			m.message = "usage: update <id> <json>"
			return
		}
		jsonText := strings.TrimSpace(strings.TrimPrefix(line, "update "+parts[1]))
		values, err := parseJSONMap(jsonText)
		if err != nil {
			m.showErrorBox("Update failed", err)
			return
		}
		m.pending = &pendingAction{Kind: "update", Target: m.target, Key: keyForTarget(m.target, parts[1]), Values: values}
	case "delete":
		if m.target.Type == db.ObjectKey {
			m.pending = &pendingAction{Kind: "delete", Target: m.target}
			return
		}
		if len(parts) < 2 {
			m.message = "usage: delete <id>"
			return
		}
		m.pending = &pendingAction{Kind: "delete", Target: m.target, Key: keyForTarget(m.target, parts[1])}
	default:
		m.message = fmt.Sprintf("unknown command: %s", parts[0])
	}
}

func (m *Model) handleQuery(ctx context.Context, line string) {
	if line == "back" {
		m.back()
		return
	}
	if tab := m.activeWorkspaceTab(); tab != nil && tab.Kind == workspaceTabQuery {
		m.executeQueryInActiveTab(ctx, line)
		return
	}
	if m.adapter == nil {
		m.message = "no active connection"
		return
	}
	start := time.Now()
	opCtx, cancel := m.dbContext(ctx)
	defer cancel()
	res, err := m.adapter.Execute(opCtx, db.Command{Text: line, Database: m.selectedDB})
	status := history.StatusOK
	errText := ""
	if err != nil {
		status = history.StatusError
		errText = err.Error()
		m.showErrorBox("Query failed", err)
	} else {
		m.result = res
		m.resultView.Reset()
		m.page = PageData
		m.message = "query executed"
	}
	m.recordHistory(history.Entry{
		ID:             strconv.FormatInt(time.Now().UnixNano(), 10),
		ProfileID:      m.activeProfileID(),
		Driver:         string(m.activeDriver()),
		Database:       m.selectedDB,
		Action:         history.ActionQuery,
		Statement:      line,
		Status:         status,
		Error:          errText,
		DurationMillis: time.Since(start).Milliseconds(),
		StartedAt:      start.UTC(),
	})
}

func (m *Model) handleHistory(_ context.Context, line string) {
	if line == "back" {
		m.back()
		return
	}
	m.message = "press enter to refill query, r to replay selected history"
}

func (m *Model) handleConfirmation(ctx context.Context, answer string) {
	if m.pending == nil {
		m.message = "nothing to confirm"
		return
	}
	if answer != "yes" && answer != "y" {
		m.pending = nil
		if m.modal != nil && m.modal.Kind == modalConfirm {
			m.modal = nil
		}
		m.restoreModalFocus()
		m.message = "cancelled"
		return
	}
	pending := m.pending
	m.pending = nil
	if m.modal != nil && m.modal.Kind == modalConfirm {
		m.modal = nil
	}
	m.restoreModalFocus()
	switch pending.Kind {
	case "delete-profile":
		if !m.vault.DeleteProfile(pending.ProfileID) {
			m.message = "profile not found"
			return
		}
		if idx := m.sessionIndexForProfile(pending.ProfileID); idx >= 0 {
			m.closeConnectionSession(idx)
		}
		m.saveVault("profile deleted")
	case "insert":
		m.runMutation(ctx, pending, history.ActionInsert)
	case "update":
		m.runMutation(ctx, pending, history.ActionUpdate)
	case "delete":
		m.runMutation(ctx, pending, history.ActionDelete)
	default:
		m.message = "unknown pending action"
	}
}

func (m *Model) runMutation(ctx context.Context, pending *pendingAction, action history.Action) {
	start := time.Now()
	var mutation result.MutationResult
	var err error
	opCtx, cancel := m.dbContext(ctx)
	switch action {
	case history.ActionInsert:
		mutation, err = m.adapter.Insert(opCtx, pending.Target, pending.Values)
	case history.ActionUpdate:
		mutation, err = m.adapter.Update(opCtx, pending.Target, pending.Key, pending.Values)
	case history.ActionDelete:
		mutation, err = m.adapter.Delete(opCtx, pending.Target, pending.Key)
	}
	cancel()
	status := history.StatusOK
	errText := ""
	if err != nil {
		status = history.StatusError
		errText = err.Error()
		m.showErrorBox(string(action)+" failed", err)
	} else {
		m.message = fmt.Sprintf("%s ok: %d affected", action, mutation.AffectedRows)
		m.openObject(ctx, pending.Target.Name)
	}
	m.recordHistory(history.Entry{
		ID:             strconv.FormatInt(time.Now().UnixNano(), 10),
		ProfileID:      m.activeProfileID(),
		Driver:         string(m.activeDriver()),
		Action:         action,
		Statement:      pending.Target.String(),
		Status:         status,
		Error:          errText,
		AffectedRows:   mutation.AffectedRows,
		DurationMillis: time.Since(start).Milliseconds(),
		StartedAt:      start.UTC(),
	})
}

func (m *Model) refreshBrowser(context.Context) {
	if m.adapter == nil {
		m.message = "no active connection"
		return
	}
	adapter := m.adapter
	selectedDB := m.selectedDB
	m.nextCmd = m.runAsync("Connecting…", func(ctx context.Context) tea.Msg {
		databases, err := adapter.ListDatabases(ctx)
		loadDB := ""
		var objects []db.Object
		var objErr error
		if err == nil && selectedDB != "" && indexOfString(databases, selectedDB) >= 0 {
			loadDB = selectedDB
			objects, objErr = adapter.ListObjects(ctx, db.Scope{Database: selectedDB})
		}
		return asyncResultMsg{apply: func(m *Model) {
			m.applyRefreshBrowser(databases, err, loadDB, objects, objErr)
		}}
	})
}

func (m *Model) applyRefreshBrowser(databases []string, err error, loadDB string, objects []db.Object, objErr error) {
	if err != nil {
		m.message = err.Error()
		return
	}
	m.databases = databases
	m.objects = nil
	m.databaseIndex = 0
	m.ensureNavigationState()
	if loadDB != "" {
		index := indexOfString(databases, loadDB)
		m.databaseIndex = index
		m.browserCursor = index
		m.expandedDBs[loadDB] = true
		if objErr != nil {
			m.message = objErr.Error()
			return
		}
		m.ensureNavigationState()
		m.databaseObjects[loadDB] = objects
		if m.selectedDB == loadDB {
			m.objects = objects
			m.objectIndex = 0
		}
		m.message = "browser loaded"
		return
	}
	if m.selectedDB != "" && indexOfString(databases, m.selectedDB) < 0 {
		m.selectedDB = ""
	}
	m.browserCursor = 0
	m.message = "select a database"
}

func (m *Model) loadObjects(ctx context.Context) {
	if m.adapter == nil {
		return
	}
	if err := m.loadObjectsForDatabase(ctx, m.selectedDB); err != nil {
		m.message = err.Error()
		return
	}
	m.objectIndex = 0
	m.message = "browser loaded"
}

func (m *Model) selectDatabase(ctx context.Context, index int) {
	m.toggleDatabase(ctx, index)
}

func (m *Model) openObject(ctx context.Context, name string) {
	targetType := db.ObjectTable
	if m.activeDriver() == config.DriverMongo {
		targetType = db.ObjectCollection
	}
	if m.activeDriver() == config.DriverRedis {
		targetType = db.ObjectKey
	}
	target := db.Target{Database: m.selectedDB, Name: name, Type: targetType}
	if targetType == db.ObjectKey {
		target.Database = ""
	}
	m.openPreview(ctx, target)
}

func (m *Model) nextRedisScan(ctx context.Context) {
	scanner, ok := m.adapter.(interface {
		ScanKeys(context.Context, redisadapter.ScanRequest) (redisadapter.KeyScan, error)
	})
	if !ok {
		m.message = "next is only available for redis scans"
		return
	}
	ctx, cancel := m.dbContext(ctx)
	defer cancel()
	scan, err := scanner.ScanKeys(ctx, redisadapter.ScanRequest{Cursor: m.redisCursor, Pattern: m.redisPattern, Count: 100})
	if err != nil {
		m.message = err.Error()
		return
	}
	m.redisCursor = scan.NextCursor
	var keyTypes []string
	if typer, ok := m.adapter.(interface {
		KeyTypes(context.Context, []string) ([]string, error)
	}); ok {
		keyTypes, _ = typer.KeyTypes(ctx, scan.Keys)
	}
	m.objects = make([]db.Object, 0, len(scan.Keys))
	for i, key := range scan.Keys {
		obj := db.Object{Name: key, Type: db.ObjectKey}
		if i < len(keyTypes) {
			obj.SubType = keyTypes[i]
		}
		m.objects = append(m.objects, obj)
	}
	m.message = fmt.Sprintf("redis scan cursor=%d keys=%d", scan.NextCursor, len(scan.Keys))
}

func (m *Model) back() {
	switch m.page {
	case PageData, PageQuery:
		m.page = PageBrowser
		m.focus = FocusSidebar
		m.message = "back to browser"
	case PageBrowser, PageHistory, PageHelp:
		// Leaving a connection's browser returns to the connection manager while
		// keeping the connection open (its tab stays in the header).
		if m.activeSession >= 0 {
			m.enterConnectionsManager()
			m.message = "connections"
			return
		}
		m.page = PageConnections
		m.focus = FocusSidebar
		m.message = "connections"
	default:
		if m.activeSession >= 0 {
			m.enterConnectionsManager()
			m.message = "connections"
			return
		}
		m.message = "already on connections"
	}
}

func (m *Model) saveVault(okMessage string) {
	if err := m.store.Save(m.master, m.vault); err != nil {
		m.message = err.Error()
		return
	}
	m.message = okMessage
}

// defaultQueryTimeout bounds how long a database operation may run before it is
// aborted. It is configurable at runtime via ":timeout <seconds>" (0 = no
// timeout, rely on Esc to cancel).
const defaultQueryTimeout = 30 * time.Second

// dbContext derives a context honoring the configured query timeout (0 = none).
func (m *Model) dbContext(parent context.Context) (context.Context, context.CancelFunc) {
	if m.queryTimeout <= 0 {
		return context.WithCancel(parent)
	}
	return context.WithTimeout(parent, m.queryTimeout)
}

func (m *Model) recordHistory(entry history.Entry) {
	if m.history == nil {
		return
	}
	if err := m.history.Append(entry, nil); err != nil {
		m.message = "history error: " + err.Error()
	}
}

func (m *Model) activeProfileID() string {
	if m.activeProfile == nil {
		return ""
	}
	return m.activeProfile.ID
}

func (m *Model) activeDriver() config.Driver {
	if m.activeProfile == nil {
		return ""
	}
	return m.activeProfile.Driver
}

func (m *Model) refreshSuggestions() {
	if m.page == PageUnlock {
		m.input.HideSuggestions()
		return
	}
	if m.focus == FocusCommand {
		if m.input.SuggestionsVisible() {
			m.input.SetSuggestions(m.commandSuggestions())
		}
		return
	}
	m.input.SetSuggestions(m.suggestions())
}

func (m *Model) suggestions() []suggest.Suggestion {
	driver := m.activeDriver()
	if m.page != PageQuery {
		driver = ""
	}
	return suggest.Suggest(suggest.Context{Page: string(m.page), Driver: driver, Input: m.input.Value()})
}

func (m *Model) resultRowCount() int {
	if m.result.Table != nil {
		return len(m.result.Table.Rows)
	}
	if len(m.result.Documents) > 0 {
		return len(m.result.Documents)
	}
	return 0
}

func (m *Model) resultColumnCount() int {
	if m.result.Table != nil {
		return len(m.result.Table.Columns)
	}
	return 0
}

func OpenAdapter(profile config.Profile) (db.Adapter, error) {
	switch profile.Driver {
	case config.DriverMySQL:
		return sqladapter.NewMySQL(profile)
	case config.DriverDoris:
		return sqladapter.NewDoris(profile)
	case config.DriverMongo:
		return mongoadapter.New(profile)
	case config.DriverRedis:
		return redisadapter.New(profile), nil
	default:
		return nil, fmt.Errorf("unsupported driver %q", profile.Driver)
	}
}

func validDriver(driver config.Driver) bool {
	switch driver {
	case config.DriverMySQL, config.DriverDoris, config.DriverMongo, config.DriverRedis:
		return true
	default:
		return false
	}
}

func indexOfString(values []string, target string) int {
	for i, value := range values {
		if value == target {
			return i
		}
	}
	return -1
}

func keyForTarget(target db.Target, id string) db.Key {
	if target.Type == db.ObjectCollection {
		return db.Key{ID: id}
	}
	return db.Key{Columns: map[string]any{"id": id}}
}

func parseJSONMap(text string) (map[string]any, error) {
	var values map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(text)), &values); err != nil {
		return nil, err
	}
	return values, nil
}

func splitLine(line string) []string {
	parts := []string{}
	var current strings.Builder
	inQuote := false
	for _, r := range strings.TrimSpace(line) {
		switch {
		case r == '"':
			inQuote = !inQuote
		case r == ' ' && !inQuote:
			if current.Len() > 0 {
				parts = append(parts, current.String())
				current.Reset()
			}
		default:
			current.WriteRune(r)
		}
	}
	if current.Len() > 0 {
		parts = append(parts, current.String())
	}
	return parts
}

func historyEntries(store *history.Store, profileID string) []history.Entry {
	if store == nil {
		return nil
	}
	entries, err := store.List(profileID, 20)
	if err != nil {
		return nil
	}
	return entries
}

func queryHistoryEntries(store *history.Store, driver string, limit int) []history.Entry {
	if store == nil {
		return nil
	}
	entries, err := store.ListByDriver(driver, limit)
	if err != nil {
		return nil
	}
	filtered := make([]history.Entry, 0, len(entries))
	for _, entry := range entries {
		if entry.Action == history.ActionQuery && strings.TrimSpace(entry.Statement) != "" {
			filtered = append(filtered, entry)
		}
	}
	return filtered
}

func (m *Model) moveHistorySelection(delta int) {
	entries := historyEntries(m.history, m.activeProfileID())
	if len(entries) == 0 {
		m.historyIndex = 0
		return
	}
	m.historyIndex = clamp(m.historyIndex+delta, 0, len(entries)-1)
}

func (m *Model) selectedHistoryEntry() (history.Entry, bool) {
	entries := historyEntries(m.history, m.activeProfileID())
	if len(entries) == 0 {
		return history.Entry{}, false
	}
	m.historyIndex = clamp(m.historyIndex, 0, len(entries)-1)
	return entries[m.historyIndex], true
}

func (m *Model) refillSelectedHistory() {
	entry, ok := m.selectedHistoryEntry()
	if !ok {
		m.message = "no history entry selected"
		return
	}
	m.input.SetValue(sanitizeSingleLineInput(entry.Statement))
	m.page = PageQuery
	m.message = "history loaded into query"
}

func (m *Model) replaySelectedHistory(ctx context.Context) {
	entry, ok := m.selectedHistoryEntry()
	if !ok {
		m.message = "no history entry selected"
		return
	}
	m.page = PageQuery
	m.handleQuery(ctx, entry.Statement)
}

func defaultHistoryPath(configPath string) string {
	if configPath == "" {
		return filepath.Join(".", "tdb.history.json")
	}
	return configPath + ".history.json"
}

func isMissingConnection(err error) bool {
	return errors.Is(err, sqladapter.ErrNoDatabase) || errors.Is(err, mongoadapter.ErrNoClient) || errors.Is(err, redisadapter.ErrNoClient)
}
