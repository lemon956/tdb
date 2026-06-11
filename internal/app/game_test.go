package app

import (
	"context"
	"math/rand"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"tdb/internal/config"
)

func spaceKey() tea.KeyMsg { return tea.KeyMsg{Type: tea.KeySpace} }

func TestConnectionsQResumesSessionOrGame(t *testing.T) {
	// With an open session, q resumes it (back to its browser page).
	m := connModel(t, 1)
	m.sessions = []connSession{{profile: config.Profile{ID: "s0", Driver: config.DriverMySQL}, page: PageBrowser, focus: FocusSidebar}}
	m.activeSession = -1
	m.handleConnectionsKey(context.Background(), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if m.activeSession != 0 || m.page != PageBrowser {
		t.Fatalf("q with a session should resume it: activeSession=%d page=%s", m.activeSession, m.page)
	}

	// With no sessions, q enters the fallback game page.
	m2 := connModel(t, 1)
	m2.sessions = nil
	m2.handleConnectionsKey(context.Background(), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if m2.page != PageGame {
		t.Fatalf("q without a session should open the game, page=%s", m2.page)
	}
}

func TestGameSpaceStartsAndJumps(t *testing.T) {
	m := connModel(t, 0)
	m.page = PageGame
	m.game = newDinoGame()
	m.width = 80
	m.height = 24

	cmd, handled := m.handleGameKey(spaceKey())
	if !handled || cmd == nil {
		t.Fatal("space should start the game and return a tick cmd")
	}
	if !m.game.active {
		t.Fatal("game should be active after start")
	}
	// On the ground, space jumps.
	if m.game.dinoY != 0 {
		t.Fatalf("dino should start on the ground, dinoY=%d", m.game.dinoY)
	}
	m.handleGameKey(spaceKey())
	if m.game.vy <= 0 {
		t.Fatalf("jump should set upward velocity, vy=%d", m.game.vy)
	}
	// advance lifts the dino then gravity brings it back down.
	m.game.advance()
	if m.game.dinoY <= 0 {
		t.Fatalf("dino should be airborne after advance, dinoY=%d", m.game.dinoY)
	}
	for i := 0; i < 12; i++ {
		m.game.advance()
	}
	if m.game.dinoY != 0 {
		t.Fatalf("dino should land again, dinoY=%d", m.game.dinoY)
	}
}

func TestGameCollisionEndsRun(t *testing.T) {
	g := newDinoGame()
	g.active = true
	g.started = true
	g.width = 80
	g.obs = []int{dinoX + 1} // a cactus that moves onto the dino column this frame
	g.dinoY = 0
	g.advance()
	if !g.over || g.active {
		t.Fatalf("a ground-level cactus on the dino column should end the run: over=%v active=%v", g.over, g.active)
	}
}

func TestGameRenderAndKeys(t *testing.T) {
	g := newDinoGame()
	start := stripANSI(g.render(80, 20))
	// Start screen: a framed prompt with the title + SPACE hint, plus the score.
	for _, want := range []string{"SCORE", "T-REX RUNNER", "SPACE"} {
		if !strings.Contains(start, want) {
			t.Fatalf("start render should contain %q:\n%s", want, start)
		}
	}
	if strings.Count(start, "\n") < 5 {
		t.Fatalf("game render should be multi-row:\n%s", start)
	}
	// Game-over screen shows the GAME OVER box.
	g.started, g.over = true, true
	if over := stripANSI(g.render(80, 20)); !strings.Contains(over, "GAME OVER") {
		t.Fatalf("game-over render should show GAME OVER:\n%s", over)
	}

	m := connModel(t, 0)
	m.page = PageGame
	// Esc returns to the connections page.
	if _, ok := m.handleGameKey(tea.KeyMsg{Type: tea.KeyEsc}); !ok || m.page != PageConnections {
		t.Fatalf("esc should return to connections, page=%s", m.page)
	}
	// ':' passes through (handled=false) so command mode can open for :q.
	m.page = PageGame
	if _, ok := m.handleGameKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{':'}}); ok {
		t.Fatal("':' should pass through from the game page")
	}
}

func TestGameSpeedAcceleratesWithScore(t *testing.T) {
	g := newDinoGame()
	g.score = 0
	base := g.frameInterval()
	g.score = 10
	mid := g.frameInterval()
	g.score = 100
	fast := g.frameInterval()
	if !(mid < base) {
		t.Fatalf("interval should shrink as score grows: base=%v mid=%v", base, mid)
	}
	if fast >= mid || fast < 45_000_000 { // 45ms floor in ns
		t.Fatalf("interval should hit the floor (>=45ms), got %v", fast)
	}
}

func TestGameObstacleSpacingVaries(t *testing.T) {
	g := newDinoGame()
	g.rng = rand.New(rand.NewSource(7))
	seen := map[int]bool{}
	for i := 0; i < 30; i++ {
		gp := g.nextGap()
		if gp < minGap || gp > 30 {
			t.Fatalf("gap %d out of range", gp)
		}
		seen[gp] = true
	}
	if len(seen) < 3 {
		t.Fatalf("obstacle spacing should vary, only saw %d distinct gaps", len(seen))
	}
}

func TestGameFirstObstacleComesEarly(t *testing.T) {
	g := newDinoGame()
	if g.spawnIn != firstSpawnIn || g.spawnIn >= 12 {
		t.Fatalf("first obstacle should come early, spawnIn=%d", g.spawnIn)
	}
	g.start(60)
	for i := 0; i < firstSpawnIn+1; i++ {
		g.advance()
	}
	if len(g.obs) == 0 {
		t.Fatal("an obstacle should spawn within the first few frames")
	}
}

func TestDinoHasTwoEyes(t *testing.T) {
	g := newDinoGame()
	g.started, g.active = true, true
	if !strings.Contains(stripANSI(g.render(60, 14)), "••") {
		t.Fatal("dino sprite should show two eyes")
	}
}

// A well-timed jump must clear an obstacle (including the landing frame) — the dino
// stays in place and the cactus passes under it.
func TestGameJumpClearsObstacle(t *testing.T) {
	g := newDinoGame()
	g.started, g.active = true, true
	g.width = 40
	g.spawnIn = 1000 // no new spawns during the test
	g.obs = []int{11}
	g.jump()
	for i := 0; i < 14; i++ {
		g.advance()
		if g.over {
			t.Fatalf("a timed jump should clear the cactus, crashed at frame %d (dinoY=%d obs=%v)", i, g.dinoY, g.obs)
		}
	}
}
