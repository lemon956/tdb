package app

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// TestRenderQueryBufferKeepsCJKIntact guards against the byte-by-byte slicing
// regression that wrote broken UTF-8 fragments for multi-byte characters,
// corrupting the terminal and making CJK lines appear to duplicate downward.
func TestRenderQueryBufferKeepsCJKIntact(t *testing.T) {
	model := newWorkspaceTabModel(t)
	model.openQueryWorkspaceTab()
	model.focus = FocusMain
	tab := model.activeWorkspaceTab()
	tab.WorkspaceFocus = workspaceFocusEditor
	tab.VimMode = vimModeNormal
	tab.QueryBuffer = `df.is_human_face AS "真人头像",`
	tab.QueryCursor = 0

	out := stripANSI(model.renderQueryBuffer(*tab))
	if !utf8.ValidString(out) {
		t.Fatalf("rendered buffer contains broken UTF-8: %q", out)
	}
	if !strings.Contains(out, "真人头像") {
		t.Fatalf("rendered buffer lost the CJK alias, got %q", out)
	}
}

// TestQueryCursorPositionCountsDisplayColumns verifies the suggestion-popup
// anchor uses display columns (CJK = 2) rather than raw bytes.
func TestQueryCursorPositionCountsDisplayColumns(t *testing.T) {
	buffer := "a真b"
	row, col := queryCursorPosition(buffer, len(buffer))
	if row != 0 {
		t.Fatalf("row = %d, want 0", row)
	}
	if col != 4 { // 'a'(1) + '真'(2) + 'b'(1)
		t.Fatalf("col = %d, want 4 display columns (not %d bytes)", col, len(buffer))
	}

	multi := "ab\n真"
	row, col = queryCursorPosition(multi, len(multi))
	if row != 1 || col != 2 {
		t.Fatalf("multiline cursor = (row %d, col %d), want (1, 2)", row, col)
	}
}

// TestQueryCursorMovesOnRuneBoundaries ensures h/l style movement never lands
// the byte-offset cursor inside a multi-byte character.
func TestQueryCursorMovesOnRuneBoundaries(t *testing.T) {
	buffer := "x真y"
	// Right across the CJK char: 0 -> 1 (after 'x') -> 4 (after '真', 3 bytes) -> 5.
	cursor := 0
	for _, want := range []int{1, 4, 5, 5} {
		cursor = queryCursorRight(buffer, cursor)
		if cursor != want {
			t.Fatalf("queryCursorRight landed at %d, want %d", cursor, want)
		}
		if cursor < len(buffer) && !utf8.RuneStart(buffer[cursor]) {
			t.Fatalf("cursor %d is inside a multi-byte rune", cursor)
		}
	}
	// Left back across the CJK char.
	for _, want := range []int{4, 1, 0, 0} {
		cursor = queryCursorLeft(buffer, cursor)
		if cursor != want {
			t.Fatalf("queryCursorLeft landed at %d, want %d", cursor, want)
		}
		if cursor < len(buffer) && !utf8.RuneStart(buffer[cursor]) {
			t.Fatalf("cursor %d is inside a multi-byte rune", cursor)
		}
	}
}

// TestQuerySelectionRangeSnapsToRunes ensures a visual selection over CJK text
// yields a byte range that slices whole characters.
func TestQuerySelectionRangeSnapsToRunes(t *testing.T) {
	tab := &workspaceTab{QueryBuffer: "真人头像"}
	tab.SelectionAnchor = 0
	tab.QueryCursor = 3 // start byte of the 2nd rune
	start, end := querySelectionRange(tab)
	if start != 0 {
		t.Fatalf("start = %d, want 0", start)
	}
	// end must be the LAST byte of the 2nd rune (bytes 3,4,5), i.e. 5.
	if end != 5 {
		t.Fatalf("end = %d, want 5 (last byte of 2nd rune)", end)
	}
	if !utf8.ValidString(tab.QueryBuffer[start : end+1]) {
		t.Fatalf("selection slice is not valid UTF-8: %q", tab.QueryBuffer[start:end+1])
	}
}
