package app

import (
	"context"
	"strings"
	"testing"

	"tdb/internal/db"
	"tdb/internal/result"
)

type pagingAdapter struct {
	lastOffset int
	calls      int
	hasMore    bool
}

func (a *pagingAdapter) Preview(_ context.Context, _ db.Target, _ db.Query, page db.Page) (result.Set, error) {
	a.lastOffset = page.Offset
	a.calls++
	return result.Set{
		Table:   &result.Table{Columns: []result.Column{{Name: "id"}}, Rows: []result.Row{{Values: []any{page.Offset}}}},
		HasMore: a.hasMore,
	}, nil
}

func (a *pagingAdapter) Test(context.Context) error                      { return nil }
func (a *pagingAdapter) ListDatabases(context.Context) ([]string, error) { return nil, nil }
func (a *pagingAdapter) ListObjects(context.Context, db.Scope) ([]db.Object, error) {
	return nil, nil
}
func (a *pagingAdapter) Insert(context.Context, db.Target, map[string]any) (result.MutationResult, error) {
	return result.MutationResult{}, nil
}
func (a *pagingAdapter) Update(context.Context, db.Target, db.Key, map[string]any) (result.MutationResult, error) {
	return result.MutationResult{}, nil
}
func (a *pagingAdapter) Delete(context.Context, db.Target, db.Key) (result.MutationResult, error) {
	return result.MutationResult{}, nil
}
func (a *pagingAdapter) Execute(context.Context, db.Command) (result.Set, error) {
	return result.Set{}, nil
}
func (a *pagingAdapter) Close() error { return nil }

func dataPreviewModel(t *testing.T, hasMore bool) (*Model, *pagingAdapter) {
	t.Helper()
	m := newWorkspaceVimModel(t)
	adapter := &pagingAdapter{hasMore: hasMore}
	m.adapter = adapter
	m.workspaceTabs = []workspaceTab{{
		ID: "data:1", Kind: workspaceTabData, Mode: workspacePreview,
		Target:         db.Target{Database: "app", Name: "users", Type: db.ObjectTable},
		WorkspaceFocus: workspaceFocusResult,
		PreviewHasMore: hasMore,
	}}
	m.activeTabIndex = 0
	return m, adapter
}

func TestPageDataAdvancesOffset(t *testing.T) {
	m, adapter := dataPreviewModel(t, true)

	m.pageData(1)
	runCmd(m, m.takeCmd())

	if adapter.lastOffset != previewPageSize {
		t.Fatalf("Preview offset = %d, want %d", adapter.lastOffset, previewPageSize)
	}
	if got := m.activeWorkspaceTab().PreviewOffset; got != previewPageSize {
		t.Fatalf("tab.PreviewOffset = %d, want %d", got, previewPageSize)
	}

	// Page back to the first page.
	m.pageData(-1)
	runCmd(m, m.takeCmd())
	if m.activeWorkspaceTab().PreviewOffset != 0 {
		t.Fatalf("prev should return to offset 0, got %d", m.activeWorkspaceTab().PreviewOffset)
	}
}

func TestPageDataStopsAtBoundaries(t *testing.T) {
	// No more rows → next is rejected without querying.
	m, adapter := dataPreviewModel(t, false)
	m.pageData(1)
	if m.takeCmd() != nil || adapter.calls != 0 {
		t.Fatalf("next with no more rows should not query")
	}
	if !strings.Contains(m.message, "no more") {
		t.Fatalf("message = %q, want no-more notice", m.message)
	}

	// At offset 0, prev is a no-op.
	m.pageData(-1)
	if m.takeCmd() != nil {
		t.Fatalf("prev at first page should not query")
	}
}

func TestDataStatusLineShowsRowRangeAndMore(t *testing.T) {
	m, _ := dataPreviewModel(t, true)
	m.width = 120
	m.height = 40
	tab := m.activeWorkspaceTab()
	tab.PreviewOffset = 100
	tab.Result = result.Set{Table: &result.Table{Columns: []result.Column{{Name: "id"}}, Rows: []result.Row{{Values: []any{1}}, {Values: []any{2}}}}}
	tab.PreviewHasMore = true

	content := stripANSI(m.workspaceDataContent(*tab))
	if !strings.Contains(content, "rows 101-102") {
		t.Fatalf("status line should show absolute range:\n%s", content)
	}
	if !strings.Contains(content, "+more") {
		t.Fatalf("status line should show +more when another page exists:\n%s", content)
	}
}
