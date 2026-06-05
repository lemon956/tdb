package app

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"

	"tdb/internal/config"
)

func (m *Model) openConnectionForm() {
	m.form = newConnectionForm()
	m.helpOpen = false
	m.modal = &modalState{Kind: modalForm, Title: "Connection"}
	m.focusModal()
	m.message = "choose a database type"
}

func (m *Model) openEditConnectionForm(profile config.Profile) {
	m.form = newEditConnectionForm(profile)
	m.helpOpen = false
	m.modal = &modalState{Kind: modalForm, Title: "Connection"}
	m.focusModal()
	m.message = "editing " + profile.ID
}

func (m *Model) cancelConnectionForm() {
	m.form = nil
	if m.modal != nil && m.modal.Kind == modalForm {
		m.modal = nil
	}
	m.restoreModalFocus()
	m.message = "connection form cancelled"
}

func (m *Model) handleConnectionFormKey(ctx context.Context, msg tea.KeyMsg) {
	if m.form == nil {
		return
	}
	if m.form.selectingDriver {
		m.handleConnectionDriverKey(msg)
		return
	}
	m.handleConnectionFieldKey(ctx, msg)
}

func (m *Model) handleConnectionDriverKey(msg tea.KeyMsg) {
	switch msg.String() {
	case "esc":
		m.cancelConnectionForm()
	case "tab", "down", "right":
		m.form.moveDriver(1)
		m.message = "choose " + string(m.form.selectedDriver())
	case "shift+tab", "up", "left":
		m.form.moveDriver(-1)
		m.message = "choose " + string(m.form.selectedDriver())
	case "enter":
		driver := m.form.selectedDriver()
		m.form.chooseDriver(driver)
		m.message = "creating " + string(driver) + " connection"
	}
}

func (m *Model) handleConnectionFieldKey(ctx context.Context, msg tea.KeyMsg) {
	switch msg.String() {
	case "esc":
		m.cancelConnectionForm()
	case "tab":
		m.form.moveField(1)
	case "shift+tab":
		m.form.moveField(-1)
	case "enter":
		if m.form.fieldIndex >= len(m.form.fields)-1 {
			m.submitConnectionForm(ctx)
			return
		}
		m.form.fieldIndex++
	case "left":
		m.form.moveCursorLeft()
	case "right":
		m.form.moveCursorRight()
	case "home", "ctrl+a":
		m.form.cursorHome()
	case "end", "ctrl+e":
		m.form.cursorEnd()
	case "delete":
		m.form.deleteForward()
	case "backspace", "ctrl+h":
		m.form.backspace()
	case " ":
		if m.form.fieldIndex >= len(m.form.fields) {
			m.form.readOnly = !m.form.readOnly
			return
		}
		m.form.insert(" ")
	default:
		if len(msg.Runes) > 0 {
			m.form.insert(string(msg.Runes))
		}
	}
}

func (m *Model) submitConnectionForm(context.Context) {
	if m.form == nil {
		return
	}
	profile, err := m.form.buildProfile()
	if err != nil {
		m.message = err.Error()
		return
	}
	if m.form.originalID != "" && m.form.originalID != profile.ID {
		m.vault.DeleteProfile(m.form.originalID)
	}
	m.vault.UpsertProfile(profile)
	if err := m.store.Save(m.master, m.vault); err != nil {
		m.message = err.Error()
		return
	}
	m.connectionIndex = m.profileIndex(profile.ID)
	m.form = nil
	if m.modal != nil && m.modal.Kind == modalForm {
		m.modal = nil
	}
	m.restoreModalFocus()
	m.message = "profile saved"
}

func (m *Model) profileIndex(id string) int {
	for i, profile := range m.vault.Profiles {
		if profile.ID == id {
			return i
		}
	}
	return 0
}
