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
		// Clearly tall — always top
		{"tall, from top", 60, 60, placementTop, placementTop},
		{"tall, from right", 60, 60, placementRight, placementTop},

		// Clearly wide — always right
		{"wide, from top", 200, 60, placementTop, placementRight},
		{"wide, from right", 200, 60, placementRight, placementRight},

		// Inside deadband (ratio = 1.5) — both stick to current
		{"deadband 1.5x, from top", 90, 60, placementTop, placementTop},
		{"deadband 1.5x, from right", 90, 60, placementRight, placementRight},

		// Just below upper threshold (ratio < 1.65) — from top, no flip
		{"just under flipToRight, from top", 98, 60, placementTop, placementTop},
		// Just at/above upper threshold — from top, flip right
		{"at flipToRight, from top", 99, 60, placementTop, placementRight},
		// Just above lower threshold (ratio > 1.35) — from right, no flip
		{"just above flipToTop, from right", 82, 60, placementRight, placementRight},
		// Below lower threshold — from right, flip to top
		{"under flipToTop, from right", 80, 60, placementRight, placementTop},

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
