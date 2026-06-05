package app

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"tdb/internal/config"
)

type connectionFormField struct {
	Name     string
	Label    string
	Value    string
	Secret   bool
	Required bool
	// Cursor is the byte offset of the editing caret within Value.
	Cursor int
}

type connectionForm struct {
	mode            string
	originalID      string
	selectingDriver bool
	driverIndex     int
	driver          config.Driver
	fields          []connectionFormField
	fieldIndex      int
	readOnly        bool
}

var connectionFormDrivers = []config.Driver{
	config.DriverMySQL,
	config.DriverDoris,
	config.DriverMongo,
	config.DriverRedis,
}

func newConnectionForm() *connectionForm {
	return &connectionForm{mode: "new", selectingDriver: true}
}

func newEditConnectionForm(profile config.Profile) *connectionForm {
	form := &connectionForm{
		mode:            "edit",
		originalID:      profile.ID,
		selectingDriver: false,
		driver:          profile.Driver,
		readOnly:        profile.ReadOnly,
		fields:          connectionFieldsForDriver(profile.Driver),
	}
	form.setFieldValue("id", profile.ID)
	switch profile.Driver {
	case config.DriverMongo:
		form.setFieldValue("uri", profile.URIParams)
		form.setFieldValue("database", profile.Database)
	case config.DriverRedis:
		form.setFieldValue("host", profile.Host)
		form.setFieldValue("port", strconv.Itoa(profile.Port))
		form.setFieldValue("user", profile.User)
		form.setFieldValue("password", profile.Password)
		form.setFieldValue("db", strconv.Itoa(profile.RedisDB))
	default:
		form.setFieldValue("host", profile.Host)
		form.setFieldValue("port", strconv.Itoa(profile.Port))
		form.setFieldValue("user", profile.User)
		form.setFieldValue("password", profile.Password)
		form.setFieldValue("database", profile.Database)
	}
	return form
}

func (f *connectionForm) selectedDriver() config.Driver {
	if f.driverIndex < 0 || f.driverIndex >= len(connectionFormDrivers) {
		f.driverIndex = 0
	}
	return connectionFormDrivers[f.driverIndex]
}

func (f *connectionForm) chooseDriver(driver config.Driver) {
	f.selectingDriver = false
	f.driver = driver
	f.fieldIndex = 0
	f.fields = connectionFieldsForDriver(driver)
}

func (f *connectionForm) moveDriver(delta int) {
	f.driverIndex = (f.driverIndex + delta) % len(connectionFormDrivers)
	if f.driverIndex < 0 {
		f.driverIndex += len(connectionFormDrivers)
	}
}

func (f *connectionForm) moveField(delta int) {
	total := len(f.fields) + 1
	if total <= 0 {
		return
	}
	f.fieldIndex = (f.fieldIndex + delta) % total
	if f.fieldIndex < 0 {
		f.fieldIndex += total
	}
}

func (f *connectionForm) currentField() (*connectionFormField, bool) {
	if f.fieldIndex < 0 || f.fieldIndex >= len(f.fields) {
		return nil, false
	}
	return &f.fields[f.fieldIndex], true
}

func (f *connectionForm) insert(text string) {
	field, ok := f.currentField()
	if !ok {
		return
	}
	text = sanitizeSingleLineInput(text)
	if text == "" {
		return
	}
	c := clamp(field.Cursor, 0, len(field.Value))
	field.Value = field.Value[:c] + text + field.Value[c:]
	field.Cursor = c + len(text)
}

func (f *connectionForm) backspace() {
	field, ok := f.currentField()
	if !ok {
		return
	}
	c := clamp(field.Cursor, 0, len(field.Value))
	if c == 0 {
		return
	}
	prev := queryCursorLeft(field.Value, c)
	field.Value = field.Value[:prev] + field.Value[c:]
	field.Cursor = prev
}

func (f *connectionForm) deleteForward() {
	field, ok := f.currentField()
	if !ok {
		return
	}
	c := clamp(field.Cursor, 0, len(field.Value))
	if c >= len(field.Value) {
		return
	}
	next := queryCursorRight(field.Value, c)
	field.Value = field.Value[:c] + field.Value[next:]
	field.Cursor = c
}

func (f *connectionForm) moveCursorLeft() {
	if field, ok := f.currentField(); ok {
		field.Cursor = queryCursorLeft(field.Value, clamp(field.Cursor, 0, len(field.Value)))
	}
}

func (f *connectionForm) moveCursorRight() {
	if field, ok := f.currentField(); ok {
		field.Cursor = queryCursorRight(field.Value, clamp(field.Cursor, 0, len(field.Value)))
	}
}

func (f *connectionForm) cursorHome() {
	if field, ok := f.currentField(); ok {
		field.Cursor = 0
	}
}

func (f *connectionForm) cursorEnd() {
	if field, ok := f.currentField(); ok {
		field.Cursor = len(field.Value)
	}
}

func (f *connectionForm) setFieldValue(name, value string) bool {
	for i := range f.fields {
		if f.fields[i].Name == name {
			f.fields[i].Value = value
			f.fields[i].Cursor = len(value)
			return true
		}
	}
	return false
}

func (f *connectionForm) fieldValue(name string) string {
	for _, field := range f.fields {
		if field.Name == name {
			return strings.TrimSpace(field.Value)
		}
	}
	return ""
}

func (f *connectionForm) buildProfile() (config.Profile, error) {
	if f.selectingDriver {
		return config.Profile{}, errors.New("choose a driver first")
	}
	if !validDriver(f.driver) {
		return config.Profile{}, fmt.Errorf("unsupported driver %q", f.driver)
	}
	for _, field := range f.fields {
		if field.Required && strings.TrimSpace(field.Value) == "" {
			return config.Profile{}, fmt.Errorf("%s is required", field.Label)
		}
	}
	switch f.driver {
	case config.DriverMongo:
		return f.buildMongoProfile()
	case config.DriverRedis:
		return f.buildRedisProfile()
	default:
		return f.buildSQLProfile()
	}
}

func (f *connectionForm) buildMongoProfile() (config.Profile, error) {
	id := f.fieldValue("id")
	uri := f.fieldValue("uri")
	if !strings.HasPrefix(uri, "mongodb://") && !strings.HasPrefix(uri, "mongodb+srv://") {
		return config.Profile{}, errors.New("mongo URI must start with mongodb:// or mongodb+srv://")
	}
	database := f.fieldValue("database")
	if database == "" {
		parsed, err := url.Parse(uri)
		if err != nil {
			return config.Profile{}, fmt.Errorf("invalid mongo URI: %w", err)
		}
		database = strings.Trim(strings.TrimPrefix(parsed.Path, "/"), "/")
	}
	return config.Profile{
		ID:        id,
		Name:      id,
		Driver:    config.DriverMongo,
		Database:  database,
		URIParams: uri,
		ReadOnly:  f.readOnly,
	}, nil
}

func (f *connectionForm) buildRedisProfile() (config.Profile, error) {
	port, err := strconv.Atoi(f.fieldValue("port"))
	if err != nil {
		return config.Profile{}, errors.New("port must be a number")
	}
	redisDB, err := strconv.Atoi(f.fieldValue("db"))
	if err != nil {
		return config.Profile{}, errors.New("redis db must be a number")
	}
	id := f.fieldValue("id")
	return config.Profile{
		ID:       id,
		Name:     id,
		Driver:   config.DriverRedis,
		Host:     f.fieldValue("host"),
		Port:     port,
		User:     f.fieldValue("user"),
		Password: f.fieldValue("password"),
		RedisDB:  redisDB,
		ReadOnly: f.readOnly,
	}, nil
}

func (f *connectionForm) buildSQLProfile() (config.Profile, error) {
	port, err := strconv.Atoi(f.fieldValue("port"))
	if err != nil {
		return config.Profile{}, errors.New("port must be a number")
	}
	id := f.fieldValue("id")
	return config.Profile{
		ID:       id,
		Name:     id,
		Driver:   f.driver,
		Host:     f.fieldValue("host"),
		Port:     port,
		User:     f.fieldValue("user"),
		Password: f.fieldValue("password"),
		Database: f.fieldValue("database"),
		ReadOnly: f.readOnly,
	}, nil
}

func connectionFieldsForDriver(driver config.Driver) []connectionFormField {
	switch driver {
	case config.DriverMongo:
		return []connectionFormField{
			{Name: "id", Label: "ID", Required: true},
			{Name: "uri", Label: "URI", Required: true},
			{Name: "database", Label: "Database"},
		}
	case config.DriverRedis:
		return []connectionFormField{
			{Name: "id", Label: "ID", Required: true},
			{Name: "host", Label: "Host", Required: true},
			{Name: "port", Label: "Port", Required: true},
			{Name: "user", Label: "User"},
			{Name: "password", Label: "Password", Secret: true},
			{Name: "db", Label: "DB", Required: true},
		}
	default:
		return []connectionFormField{
			{Name: "id", Label: "ID", Required: true},
			{Name: "host", Label: "Host", Required: true},
			{Name: "port", Label: "Port", Required: true},
			{Name: "user", Label: "User", Required: true},
			{Name: "password", Label: "Password", Secret: true},
			{Name: "database", Label: "Database", Required: true},
		}
	}
}
