package tmuxpane

import "testing"

func TestDecideHysteresis(t *testing.T) {
	cases := []struct {
		name    string
		cols    int
		rows    int
		current placement
		want    placement
	}{
		// Visually tall (60×60 chars = 60×120 pixels → visual 0.5) — always top
		{"tall, from top", 60, 60, placementTop, placementTop},
		{"tall, from right", 60, 60, placementRight, placementTop},

		// Visually wide (200×60 → visual ≈ 1.67) — always right
		{"wide, from top", 200, 60, placementTop, placementRight},
		{"wide, from right", 200, 60, placementRight, placementRight},

		// Visually square (120×60 → visual 1.0) — both stick to current
		{"deadband square, from top", 120, 60, placementTop, placementTop},
		{"deadband square, from right", 120, 60, placementRight, placementRight},

		// Just below upper threshold (visual < 1.05) — from top, no flip
		// 125×60 → visual ≈ 1.042
		{"just under flipToRight, from top", 125, 60, placementTop, placementTop},
		// At/above upper threshold (visual = 1.05) — from top, flip right
		// 126×60 → visual 1.05
		{"at flipToRight, from top", 126, 60, placementTop, placementRight},
		// Just above lower threshold (visual > 0.95) — from right, no flip
		// 115×60 → visual ≈ 0.958
		{"just above flipToTop, from right", 115, 60, placementRight, placementRight},
		// Below lower threshold (visual < 0.95) — from right, flip to top
		// 113×60 → visual ≈ 0.942
		{"under flipToTop, from right", 113, 60, placementRight, placementTop},

		// Edge: zero rows shouldn't crash, returns current
		{"zero rows", 100, 0, placementRight, placementRight},
	}
	for _, c := range cases {
		got := decide(c.cols, c.rows, c.current)
		if got != c.want {
			t.Errorf("%s: decide(%d, %d, %v) = %v, want %v",
				c.name, c.cols, c.rows, c.current, got, c.want)
		}
	}
}
