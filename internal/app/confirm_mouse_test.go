package app

import (
	"path/filepath"
	"testing"
)

func TestMouseClickCancelClearsPendingAction(t *testing.T) {
	model := NewModel(Options{ConfigPath: filepath.Join(t.TempDir(), "tdb.enc")})
	model.page = PageConnections
	model.pending = &pendingAction{Kind: "delete-profile", ProfileID: "local"}
	model.hitboxes = HitboxRegistry{{ID: "confirm-cancel", X: 3, Y: 3, Width: 10, Height: 1, Focus: FocusContext, Action: actionCancel}}

	got := leftClick(model, 4, 3).(*Model)
	if got.pending != nil {
		t.Fatalf("pending action not cleared: %+v", got.pending)
	}
	if got.message != "cancelled" {
		t.Fatalf("message = %q, want cancelled", got.message)
	}
}

func TestViewRegistersConfirmAndCancelHitboxes(t *testing.T) {
	model := NewModel(Options{ConfigPath: filepath.Join(t.TempDir(), "tdb.enc")})
	model.page = PageConnections
	model.width = 110
	model.height = 30
	model.pending = &pendingAction{Kind: "delete-profile", ProfileID: "local"}

	_ = model.View()

	var hasConfirm, hasCancel bool
	for _, hit := range model.hitboxes {
		hasConfirm = hasConfirm || hit.Action == actionConfirm
		hasCancel = hasCancel || hit.Action == actionCancel
	}
	if !hasConfirm || !hasCancel {
		t.Fatalf("missing confirm/cancel hitboxes: %+v", model.hitboxes)
	}
}
