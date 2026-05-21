package tmuxpane

import "testing"

func TestDecide(t *testing.T) {
	cases := []struct {
		name string
		cols int
		rows int
		want placement
	}{
		// Visually tall — top.
		{"tall", 60, 60, placementTop},
		// Visually wide — right.
		{"wide", 200, 60, placementRight},
		// Exactly visually square — right (>= threshold).
		{"square", 120, 60, placementRight},
		// Just under visually square — top.
		{"just under square", 119, 60, placementTop},
		// Zero rows is a safe default to top.
		{"zero rows", 100, 0, placementTop},
		// Below the min-width threshold, force top even if aspect favors right.
		// 63 cols × 8 rows → aspect 3.94 would normally be right, but cols < 64.
		{"below min-width forces top", 63, 8, placementTop},
		// At the min-width threshold, right placement is admissible (and aspect agrees).
		{"at min-width with right aspect", minWindowColsForRight, 30, placementRight},
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
		{"wide window stays at rightCols", rightCols*4 + 100, rightCols},
		{"exactly 4×rightCols stays at rightCols", rightCols * 4, rightCols},
		// Below 4 × rightCols, the cap kicks in: width = winCols / 4.
		{"100 cols clamps to 25", 100, 25},
		{"80 cols clamps to 20", 80, 20},
		// At the minWindowColsForRight floor, returns minRightWidth.
		{"at min-window floor returns min-right-width",
			minWindowColsForRight, minRightWidth},
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
