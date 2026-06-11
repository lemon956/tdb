package app

import (
	"fmt"
	"math/rand"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type gameTickMsg struct{}

func gameTickCmd(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(time.Time) tea.Msg { return gameTickMsg{} })
}

// dinoGame is a tiny Chrome-style dinosaur jump game shown on the fallback page
// (PageGame) when you leave the connection picker with no open connections.
type dinoGame struct {
	started bool       // has been started at least once
	active  bool       // currently running (driving ticks)
	over    bool       // crashed
	dinoY   int        // rows above the ground (0 = on the ground)
	vy      int        // vertical velocity
	tick    int        // frame counter
	score   int        // obstacles cleared
	spawnIn int        // frames until the next obstacle
	obs     []int      // obstacle x positions (ground-level cacti)
	width   int        // playfield width
	rng     *rand.Rand // varied obstacle spacing
}

const (
	dinoX        = 6  // dino column
	jumpVel      = 3  // initial upward velocity
	firstSpawnIn = 8  // frames before the very first obstacle
	minGap       = 8  // floor for obstacle spacing
	playRows     = 12 // play-field height in rows
)

func newDinoGame() dinoGame {
	return dinoGame{spawnIn: firstSpawnIn}
}

// enterDinoGame switches to the fallback game page (not running until SPACE).
func (m *Model) enterDinoGame() {
	m.page = PageGame
	m.game = newDinoGame()
	m.clearTransientUI()
	m.message = "no connections — press space to play"
}

func (g *dinoGame) start(width int) {
	*g = newDinoGame()
	g.width = clampWidth(width)
	g.started = true
	g.active = true
	g.rng = rand.New(rand.NewSource(time.Now().UnixNano()))
}

// frameInterval ramps the frame rate up gently as the score grows (slow
// acceleration): 95ms per frame down to a 45ms floor at ~25 points.
func (g *dinoGame) frameInterval() time.Duration {
	d := 95*time.Millisecond - time.Duration(g.score)*2*time.Millisecond
	if d < 45*time.Millisecond {
		d = 45 * time.Millisecond
	}
	return d
}

// nextGap picks a varied spacing until the next obstacle. The window tightens a
// little with the score but always keeps some variation.
func (g *dinoGame) nextGap() int {
	lo, hi := 12, 30
	shift := g.score / 3
	lo = max(minGap, lo-shift)
	hi = max(lo+4, hi-shift)
	if g.rng == nil {
		return (lo + hi) / 2
	}
	return lo + g.rng.Intn(hi-lo+1)
}

func clampWidth(w int) int {
	if w < 24 {
		return 24
	}
	if w > 200 {
		return 200
	}
	return w
}

func (g *dinoGame) jump() {
	if g.active && !g.over && g.dinoY == 0 {
		g.vy = jumpVel
	}
}

// advance steps the game one frame: gravity, obstacle motion, spawn, collision.
func (g *dinoGame) advance() {
	if !g.active || g.over {
		return
	}
	g.tick++

	// Vertical motion.
	g.dinoY += g.vy
	g.vy--
	if g.dinoY <= 0 {
		g.dinoY = 0
		g.vy = 0
	}

	// Move obstacles left; count cleared ones.
	moved := g.obs[:0]
	for _, x := range g.obs {
		nx := x - 1
		if nx == dinoX-1 {
			g.score++
		}
		if nx >= 0 {
			moved = append(moved, nx)
		}
	}
	g.obs = moved

	// Spawn (x is the cactus' left column; the sprite is 3 wide). Spacing varies.
	g.spawnIn--
	if g.spawnIn <= 0 {
		g.obs = append(g.obs, g.width-4)
		g.spawnIn = g.nextGap()
	}

	// Collision: the cactus' trunk overlaps the dino's narrow body hitbox while it is
	// on the ground. The hitbox is intentionally forgiving (2 cells, and only when
	// landed) so a well-timed jump reliably clears the obstacle — the dino stays in
	// place and jumps over it, Chrome-runner style.
	for _, x := range g.obs {
		trunk := x + 1
		if trunk >= dinoX+1 && trunk < dinoX+3 && g.dinoY == 0 {
			g.over = true
			g.active = false
			return
		}
	}
}

// Line-drawn sprites (no emoji, per the project's icon rules). The dino faces
// right; its legs row alternates each frame for a running animation. The cactus is
// a small two-armed saguaro whose base sits on the ground.
var (
	dinoRows    = []string{"  ┌──┐", "┌─┤••│", "└┬──┬┘"} // head with two eyes; bottom row = legs
	dinoLegsB   = "└─╵─╵┘"                               // alternate legs frame
	cactusRows  = []string{"╷ ╷", "╰┳╯"}                 // bottom row sits on the ground
	groundGlyph = '▁'
)

// render draws the playfield into a string. width/height are the body dimensions.
func (g *dinoGame) render(width, height int) string {
	w := clampWidth(width)
	g.width = w
	rows := playRows
	if rows > height-1 {
		rows = max(6, height-1)
	}
	grid := make([][]rune, rows)
	for r := range grid {
		grid[r] = make([]rune, w)
		for c := range grid[r] {
			grid[r][c] = ' '
		}
	}
	groundY := rows - 1
	theme := defaultTheme()

	// Ground line.
	for c := 0; c < w; c++ {
		grid[groundY][c] = groundGlyph
	}
	// Cacti: bottom row sits on the ground line.
	for _, x := range g.obs {
		putSpriteRows(grid, groundY, x, cactusRows)
	}
	// Dino: bottom (legs) row sits at groundY - dinoY; legs animate while running.
	frame := append([]string(nil), dinoRows...)
	if g.active && !g.over && g.tick%2 == 0 {
		frame[len(frame)-1] = dinoLegsB
	}
	putSpriteRows(grid, groundY-g.dinoY, dinoX, frame)
	// Score, top-right.
	score := fmt.Sprintf("SCORE %d", g.score)
	putSprite(grid, 0, max(0, w-len([]rune(score))-1), score)

	var b strings.Builder
	for r := 0; r < rows; r++ {
		line := string(grid[r])
		if r == groundY {
			line = theme.muted.Render(line)
		}
		b.WriteString(line)
		if r < rows-1 {
			b.WriteString("\n")
		}
	}
	playfield := b.String()

	// Centered framed prompt for the start / game-over screens.
	if !g.started || g.over {
		var body string
		if g.over {
			body = theme.danger.Render("GAME OVER") + "\n\n" +
				fmt.Sprintf("Score  %d", g.score) + "\n" +
				theme.muted.Render("SPACE retry · Esc back")
		} else {
			body = theme.accent.Render("T-REX RUNNER") + "\n\n" +
				"Press SPACE to start" + "\n" +
				theme.muted.Render("Esc back · :q quit")
		}
		box := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(theme.accentColor).
			Padding(0, 2).
			Align(lipgloss.Center).
			Render(body)
		playfield = placeOverlayCenter(padBlock(playfield, w, rows), box)
	}
	return playfield
}

func putSprite(grid [][]rune, row, col int, sprite string) {
	if row < 0 || row >= len(grid) {
		return
	}
	for i, r := range []rune(sprite) {
		c := col + i
		if c >= 0 && c < len(grid[row]) {
			grid[row][c] = r
		}
	}
}

// putSpriteRows places a multi-row sprite so its last row lands on bottomRow and
// the rest stack upward.
func putSpriteRows(grid [][]rune, bottomRow, col int, rows []string) {
	for i := len(rows) - 1; i >= 0; i-- {
		putSprite(grid, bottomRow-(len(rows)-1-i), col, rows[i])
	}
}

// handleGameKey drives the fallback game page. Returns (cmd, handled); handled is
// false for ':' so the global handler can open command mode (for :q).
func (m *Model) handleGameKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	switch msg.String() {
	case " ", "spacebar":
		if !m.game.active || m.game.over {
			m.game.start(m.gameWidth())
			return gameTickCmd(m.game.frameInterval()), true
		}
		m.game.jump()
		return nil, true
	case "esc", "q":
		m.page = PageConnections
		m.focus = FocusSidebar
		m.message = "connections"
		return nil, true
	case ":":
		return nil, false // let the global handler open command mode (for :q)
	}
	return nil, true // swallow everything else
}

// gameWidth is the body width available to the game.
func (m *Model) gameWidth() int {
	w := m.width
	if w <= 0 {
		w = 100
	}
	return clampWidth(w - 4)
}
