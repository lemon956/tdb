//! Dino jump game — the circuit-breaker page shown when there are no
//! connections, and reachable via `:game`. Port of the Go `game.go` behavior.

const DINO_COL: u16 = 5;
const JUMP_VELOCITY: f32 = 1.6;
const GRAVITY: f32 = 0.28;
const JUMP_CLEAR_HEIGHT: f32 = 1.0;

pub struct Game {
    pub score: u32,
    pub started: bool,
    pub game_over: bool,
    pub dino_height: f32,
    pub vy: f32,
    pub obstacles: Vec<u16>,
    pub tick: u64,
    pub width: u16,
}

impl Default for Game {
    fn default() -> Game {
        Game {
            score: 0,
            started: false,
            game_over: false,
            dino_height: 0.0,
            vy: 0.0,
            obstacles: Vec::new(),
            tick: 0,
            width: 60,
        }
    }
}

impl Game {
    pub fn new() -> Game {
        Game::default()
    }

    /// Space: start, restart after game-over, or jump.
    pub fn press(&mut self) {
        if !self.started || self.game_over {
            *self = Game {
                started: true,
                width: self.width,
                ..Game::default()
            };
            return;
        }
        if self.dino_height <= f32::EPSILON {
            self.vy = JUMP_VELOCITY;
        }
    }

    pub fn advance(&mut self, width: u16) {
        self.width = width.max(10);
        if !self.started || self.game_over {
            return;
        }
        self.tick += 1;

        // Physics.
        self.dino_height += self.vy;
        self.vy -= GRAVITY;
        if self.dino_height <= 0.0 {
            self.dino_height = 0.0;
            self.vy = 0.0;
        }

        // Move obstacles left; drop those off-screen.
        for x in &mut self.obstacles {
            *x = x.saturating_sub(1);
        }
        self.obstacles.retain(|x| *x > 0);

        // Spawn a new cactus periodically, with spacing.
        let interval = 16 + (rand::random::<u64>() % 12);
        if self.tick.is_multiple_of(interval) {
            self.obstacles.push(self.width.saturating_sub(2));
        }

        // Collision: an obstacle at the dino column while the dino is low.
        if self.dino_height < JUMP_CLEAR_HEIGHT
            && self.obstacles.iter().any(|x| x.abs_diff(DINO_COL) <= 1)
        {
            self.game_over = true;
            return;
        }
        self.score += 1;
    }

    pub fn dino_col(&self) -> u16 {
        DINO_COL
    }
}
