package app

import (
	"context"
	"errors"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"tdb/internal/config"
	"tdb/internal/db"
	"tdb/internal/result"
)

// previewPageSize is how many rows a single data-preview page loads.
const previewPageSize = 100

// pageNext advances the current view by one page: the active data preview, or a
// Redis SCAN page when browsing keys.
func (m *Model) pageNext(ctx context.Context) {
	if tab := m.activeWorkspaceTab(); tab != nil && tab.Kind == workspaceTabData && tab.Mode != workspaceMetadata {
		m.pageData(1)
		return
	}
	if m.activeDriver() == config.DriverRedis && m.page == PageBrowser {
		m.nextRedisScan(ctx)
		return
	}
	m.message = "nothing to page"
}

// pagePrev moves the active data preview back one page.
func (m *Model) pagePrev(context.Context) {
	if tab := m.activeWorkspaceTab(); tab != nil && tab.Kind == workspaceTabData && tab.Mode != workspaceMetadata {
		m.pageData(-1)
		return
	}
	m.message = "previous page is only available for data previews"
}

// pageData re-runs the active data preview at a new offset (delta pages from the
// current one). It runs asynchronously so it is cancellable like any query.
func (m *Model) pageData(delta int) {
	tab := m.activeWorkspaceTab()
	if tab == nil || tab.Kind != workspaceTabData || tab.Mode == workspaceMetadata {
		m.message = "pagination is only for data previews"
		return
	}
	if m.adapter == nil {
		m.message = "no active connection"
		return
	}
	if delta > 0 && !tab.PreviewHasMore {
		m.message = "no more rows"
		return
	}
	newOffset := tab.PreviewOffset + delta*previewPageSize
	if newOffset < 0 {
		newOffset = 0
	}
	if newOffset == tab.PreviewOffset {
		m.message = "already on the first page"
		return
	}
	adapter := m.adapter
	target := tab.Target
	tabID := tab.ID
	m.nextCmd = m.runAsync("Loading page…", func(ctx context.Context) tea.Msg {
		res, err := adapter.Preview(ctx, target, db.Query{}, db.NewPage(previewPageSize, newOffset))
		return asyncResultMsg{apply: func(m *Model) {
			m.applyPageData(tabID, res, err, newOffset)
		}}
	})
}

func (m *Model) applyPageData(tabID string, res result.Set, err error, offset int) {
	if errors.Is(err, context.Canceled) {
		m.message = "cancelled"
		return
	}
	idx := m.findWorkspaceTab(tabID)
	if idx < 0 {
		return
	}
	tab := &m.workspaceTabs[idx]
	if err != nil {
		m.message = err.Error()
		return
	}
	tab.Result = res
	tab.PreviewOffset = offset
	tab.PreviewHasMore = res.HasMore
	tab.ResultView.Reset()
	tab.ResultCursorRow = 0
	tab.ResultRow = 0
	tab.ResultColumn = 0
	m.syncActiveTabState()
	m.message = fmt.Sprintf("rows %d-%d", offset+1, offset+resultRowCount(res))
}
