package tmuxpane

import "testing"

func TestDecide(t *testing.T) {
	cases := []struct {
		name string
		cols int
		rows int
		want placement
	}{
		// Visually tall (60×60 chars = 60×120 pixels → visual 0.5) — top.
		{"tall", 60, 60, placementTop},
		// Visually wide (200×60 → visual ≈ 1.67) — right.
		{"wide", 200, 60, placementRight},
		// Exactly visually square (120×60 → visual 1.0) — right (>=).
		{"square", 120, 60, placementRight},
		// One col below the visual-1.0 threshold — top.
		{"just under square", 119, 60, placementTop},
		// Zero rows shouldn't crash; treated as top (safe default).
		{"zero rows", 100, 0, placementTop},
	}
	for _, c := range cases {
		got := decide(c.cols, c.rows)
		if got != c.want {
			t.Errorf("%s: decide(%d, %d) = %v, want %v",
				c.name, c.cols, c.rows, got, c.want)
		}
	}
}

func TestRightWidthFor(t *testing.T) {
	cases := []struct {
		name    string
		winCols int
		want    int
	}{
		// At or above 4 × rightCols, the cap matches rightCols.
		{"wide window stays at rightCols", 200, rightCols},
		{"exactly 4×rightCols stays at rightCols", rightCols * 4, rightCols},
		// Below 4 × rightCols, the cap kicks in: width = winCols / 4.
		{"100 cols clamps to 25", 100, 25},
		{"80 cols clamps to 20", 80, 20},
		// Zero / unknown winCols falls back to rightCols.
		{"unknown winCols falls back", 0, rightCols},
	}
	for _, c := range cases {
		got := rightWidthFor(c.winCols)
		if got != c.want {
			t.Errorf("%s: rightWidthFor(%d) = %d, want %d",
				c.name, c.winCols, got, c.want)
		}
	}
}
