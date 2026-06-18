package app

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"tdb/internal/config"
	"tdb/internal/db"
)

// Connection-profile management: the `connections` page command verbs (new /
// edit / delete / open / test / history) and the profile create/edit/open/test
// helpers they dispatch to. Split out of model.go to keep the root model file
// focused on the update/view loop.

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
