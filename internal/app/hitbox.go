package app

type Hitbox struct {
	ID      string
	X       int
	Y       int
	Width   int
	Height  int
	Focus   Focus
	Action  appAction
	Index   int
	Payload string
}

type HitboxRegistry []Hitbox

func (r HitboxRegistry) Hit(x, y int) (Hitbox, bool) {
	for i := len(r) - 1; i >= 0; i-- {
		box := r[i]
		if x >= box.X && x < box.X+box.Width && y >= box.Y && y < box.Y+box.Height {
			return box, true
		}
	}
	return Hitbox{}, false
}
