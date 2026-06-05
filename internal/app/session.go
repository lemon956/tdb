package app

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"tdb/internal/config"
	"tdb/internal/db"
	"tdb/internal/result"
	"tdb/internal/suggest"
)

// connSession is a full snapshot of one open connection's state. The Model's
// per-connection fields always mirror the active session; switching saves the
// current fields back and loads the target session's fields. This keeps the
// rest of the code reading m.adapter/m.databases/... unchanged.
type connSession struct {
	profile config.Profile
	adapter db.Adapter

	databases       []string
	objects         []db.Object
	databaseObjects map[string][]db.Object
	expandedDBs     map[string]bool
	expandedMeta    map[string]bool
	selectedDB      string
	databaseIndex   int
	objectIndex     int
	browserCursor   int
	navVerticalOffset   int
	navHorizontalOffset int

	target        db.Target
	result        result.Set
	resultView    ResultView
	workspaceMode workspaceMode
	workspaceTabs []workspaceTab
	activeTabIndex int
	nextQueryTabID int
	queryFieldCache map[string][]suggest.Field

	redisCursor  uint64
	redisPattern string

	page  Page
	focus Focus
}

// captureSession copies the active per-connection fields out of the Model.
func (m *Model) captureSession() connSession {
	prof := config.Profile{}
	if m.activeProfile != nil {
		prof = *m.activeProfile
	}
	return connSession{
		profile:             prof,
		adapter:             m.adapter,
		databases:           m.databases,
		objects:             m.objects,
		databaseObjects:     m.databaseObjects,
		expandedDBs:         m.expandedDBs,
		expandedMeta:        m.expandedMeta,
		selectedDB:          m.selectedDB,
		databaseIndex:       m.databaseIndex,
		objectIndex:         m.objectIndex,
		browserCursor:       m.browserCursor,
		navVerticalOffset:   m.navVerticalOffset,
		navHorizontalOffset: m.navHorizontalOffset,
		target:              m.target,
		result:              m.result,
		resultView:          m.resultView,
		workspaceMode:       m.workspaceMode,
		workspaceTabs:       m.workspaceTabs,
		activeTabIndex:      m.activeTabIndex,
		nextQueryTabID:      m.nextQueryTabID,
		queryFieldCache:     m.queryFieldCache,
		redisCursor:         m.redisCursor,
		redisPattern:        m.redisPattern,
		page:                m.page,
		focus:               m.focus,
	}
}

// saveActiveSession writes the Model's per-connection fields back to the active
// session entry.
func (m *Model) saveActiveSession() {
	if m.activeSession < 0 || m.activeSession >= len(m.sessions) {
		return
	}
	m.sessions[m.activeSession] = m.captureSession()
}

// loadSession makes session i active, copying its fields into the Model.
func (m *Model) loadSession(i int) {
	if i < 0 || i >= len(m.sessions) {
		return
	}
	m.activeSession = i
	s := m.sessions[i]
	m.activeProfile = &m.sessions[i].profile
	m.adapter = s.adapter
	m.databases = s.databases
	m.objects = s.objects
	m.databaseObjects = s.databaseObjects
	m.expandedDBs = s.expandedDBs
	m.expandedMeta = s.expandedMeta
	m.selectedDB = s.selectedDB
	m.databaseIndex = s.databaseIndex
	m.objectIndex = s.objectIndex
	m.browserCursor = s.browserCursor
	m.navVerticalOffset = s.navVerticalOffset
	m.navHorizontalOffset = s.navHorizontalOffset
	m.target = s.target
	m.result = s.result
	m.resultView = s.resultView
	m.workspaceMode = s.workspaceMode
	m.workspaceTabs = s.workspaceTabs
	m.activeTabIndex = s.activeTabIndex
	m.nextQueryTabID = s.nextQueryTabID
	m.queryFieldCache = s.queryFieldCache
	m.redisCursor = s.redisCursor
	m.redisPattern = s.redisPattern
	m.page = s.page
	m.focus = s.focus
	m.clearTransientUI()
}

// switchSession saves the current session and activates another.
func (m *Model) switchSession(i int) {
	if i < 0 || i >= len(m.sessions) || i == m.activeSession {
		if i == m.activeSession {
			return
		}
		return
	}
	m.saveActiveSession()
	m.loadSession(i)
	m.message = "connection: " + m.sessions[i].profile.ID
}

// sessionIndexForProfile returns the session index for a profile id, or -1.
func (m *Model) sessionIndexForProfile(id string) int {
	for i := range m.sessions {
		if m.sessions[i].profile.ID == id {
			return i
		}
	}
	return -1
}

// enterConnectionsManager leaves the active session (preserving it) and shows
// the connection manager (saved-profile list / new / open).
func (m *Model) enterConnectionsManager() {
	m.saveActiveSession()
	m.activeSession = -1
	m.activeProfile = nil
	m.adapter = nil
	m.databases = nil
	m.objects = nil
	m.databaseObjects = map[string][]db.Object{}
	m.expandedDBs = map[string]bool{}
	m.expandedMeta = map[string]bool{}
	m.selectedDB = ""
	m.databaseIndex = 0
	m.objectIndex = 0
	m.browserCursor = 0
	m.target = db.Target{}
	m.result = result.Set{}
	m.resultView.Reset()
	m.workspaceMode = ""
	m.workspaceTabs = nil
	m.activeTabIndex = 0
	m.nextQueryTabID = 0
	m.queryFieldCache = nil
	m.page = PageConnections
	m.focus = FocusSidebar
	m.clearTransientUI()
}

// clearTransientUI drops modal/overlay state that should not leak across session
// switches.
func (m *Model) clearTransientUI() {
	m.modal = nil
	m.form = nil
	m.helpOpen = false
	m.pending = nil
	m.errBox = nil
	m.navSearchActive = false
	m.input.Clear()
}

// switchSessionRelative moves to the previous/next connection session (wrapping).
func (m *Model) switchSessionRelative(delta int) {
	if len(m.sessions) == 0 {
		return
	}
	if m.activeSession < 0 {
		if delta >= 0 {
			m.switchSession(0)
		} else {
			m.switchSession(len(m.sessions) - 1)
		}
		return
	}
	next := (m.activeSession + delta + len(m.sessions)) % len(m.sessions)
	m.switchSession(next)
}

// handleConnectionTabKey handles Alt+arrows / Alt+1..9 to switch connection
// tabs. It works regardless of the current focus or text-entry mode.
func (m *Model) handleConnectionTabKey(msg tea.KeyMsg) bool {
	if len(m.sessions) == 0 {
		return false
	}
	switch msg.String() {
	case "alt+left":
		m.switchSessionRelative(-1)
		return true
	case "alt+right":
		m.switchSessionRelative(1)
		return true
	}
	if s := msg.String(); strings.HasPrefix(s, "alt+") {
		n := strings.TrimPrefix(s, "alt+")
		if len(n) == 1 && n[0] >= '1' && n[0] <= '9' {
			if idx := int(n[0] - '1'); idx < len(m.sessions) {
				m.switchSession(idx)
			}
			return true
		}
	}
	return false
}

// closeConnectionSession closes one connection's session (and its adapter) and
// activates a neighbouring session, or the manager when none remain.
func (m *Model) closeConnectionSession(i int) {
	if i < 0 || i >= len(m.sessions) {
		return
	}
	closed := m.sessions[i].profile.ID
	if adapter := m.sessions[i].adapter; adapter != nil {
		_ = adapter.Close()
	}
	m.sessions = append(m.sessions[:i], m.sessions[i+1:]...)
	m.activeSession = -1 // force a clean reload below
	if len(m.sessions) == 0 {
		m.enterConnectionsManager()
	} else {
		next := clamp(i, 0, len(m.sessions)-1)
		m.loadSession(next)
	}
	m.message = "closed connection " + closed
}
