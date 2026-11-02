package ui

import (
	"math"
	"sort"

	"github.com/mauricioprado00/tmux-session-selector/internal/tmux"
)

// cellRect is the cells of the overview's grid a window takes, corners
// inclusive.
type cellRect struct {
	c0, r0, c1, r1 int
}

func (r cellRect) cols() int { return r.c1 - r.c0 + 1 }
func (r cellRect) rows() int { return r.r1 - r.r0 + 1 }

// Snap points: a window takes 2 cells from size snapTwo, and a 2×2 block
// from snapFour. Six steps of 1.25 reach the block; three reach two cells.
const (
	snapTwo  = 1.9
	snapFour = 3.8
)

// wantCells is how many cells a window of size weight would take.
func wantCells(weight float64) int {
	switch {
	case weight >= snapFour:
		return 4
	case weight >= snapTwo:
		return 2
	}
	return 1
}

// shapes are the columns × rows a window of that many cells may take.
var shapes = map[int][][2]int{1: {{1, 1}}, 2: {{2, 1}, {1, 2}}, 4: {{2, 2}}}

// packWindows places windows, in order, on the free cells of a grid, each
// at the first free cell of order. A grown window takes more cells only
// while there are spare ones: free cells beyond one per window, given to
// the largest windows first. Whatever of its size the cells do not cover
// is returned as its factor, for trackWeights to stretch its columns and
// rows by. cw and ch are the size of a cell on screen, for choosing a shape.
func packWindows(windows []*tmux.Window, free [][]bool, order [][2]int, cw, ch float64, sizes windowSizes) ([]cellRect, []float64) {
	n := len(windows)
	weights := make([]float64, n)
	for i, w := range windows {
		weights[i] = sizes.of(w, false)
	}
	spare := -n
	for _, row := range free {
		for _, f := range row {
			if f {
				spare++
			}
		}
	}
	want := grantCells(weights, spare)
	aspect := func(i int) float64 {
		w := windows[i]
		if w.Width > 0 && w.Height > 0 {
			return float64(w.Width) / float64(w.Height)
		}
		return 80.0 / 24
	}
	rects := packCells(want, free, order, cw, ch, aspect)
	factors := make([]float64, n)
	for i, r := range rects {
		factors[i] = weights[i] // not snapped: all of its size stretches
		if cells := r.cols() * r.rows(); cells > 1 {
			// Snapped: the cells cover up to their number, never less.
			factors[i] = max(1, weights[i]/float64(cells))
		}
	}
	return rects, factors
}

// grantCells is how many cells each window gets, out of spare cells beyond
// one each: what its weight asks for, largest weight first, or as much
// less as is left.
func grantCells(weights []float64, spare int) []int {
	want := make([]int, len(weights))
	byWeight := make([]int, len(weights))
	for i := range weights {
		want[i], byWeight[i] = 1, i
	}
	sort.SliceStable(byWeight, func(a, b int) bool { return weights[byWeight[a]] > weights[byWeight[b]] })
	for _, i := range byWeight {
		cells := wantCells(weights[i])
		for cells > 1 && cells-1 > spare {
			cells /= 2
		}
		want[i] = cells
		spare -= cells - 1
	}
	return want
}

// packCells places windows one by one: window i at the first free cell in
// order, taking want[i] cells in the shape, among those that fit there,
// closest to its aspect (width over height, in screen cells); beside rather
// than below when equal. If no shape of that size fits, it takes fewer. It
// marks the cells it takes as no longer free.
func packCells(want []int, free [][]bool, order [][2]int, cw, ch float64, aspect func(i int) float64) []cellRect {
	rows := len(free)
	fits := func(r cellRect) bool {
		if r.c0 < 0 || r.r0 < 0 || r.r1 >= rows || r.c1 >= len(free[0]) {
			return false
		}
		for y := r.r0; y <= r.r1; y++ {
			for x := r.c0; x <= r.c1; x++ {
				if !free[y][x] {
					return false
				}
			}
		}
		return true
	}
	rects := make([]cellRect, len(want))
	next := 0
	for i := range want {
		for next < len(order) && !free[order[next][1]][order[next][0]] {
			next++
		}
		if next == len(order) {
			break // more windows than cells: cannot happen, the grid fits them
		}
		c, r := order[next][0], order[next][1]
		rect := cellRect{c, r, c, r}
		for cells := want[i]; cells >= 1; cells /= 2 {
			best, found := 0.0, false
			for _, sh := range shapes[cells] {
				// Every placement of the shape that covers the free cell.
				for dy := 0; dy < sh[1]; dy++ {
					for dx := 0; dx < sh[0]; dx++ {
						cand := cellRect{c - dx, r - dy, c - dx + sh[0] - 1, r - dy + sh[1] - 1}
						if !fits(cand) {
							continue
						}
						score := math.Abs(math.Log(float64(sh[0])*cw/(float64(sh[1])*ch)) - math.Log(aspect(i)))
						if !found || score < best-1e-9 {
							rect, best, found = cand, score, true
						}
					}
				}
			}
			if found {
				break
			}
		}
		for y := rect.r0; y <= rect.r1; y++ {
			for x := rect.c0; x <= rect.c1; x++ {
				free[y][x] = false
			}
		}
		rects[i] = rect
	}
	return rects
}

// trackWeights is how wide each of cols columns and how tall each of rows
// rows is, relative to the others: the mean factor of the windows in it,
// or 1 for a column or row with none. Like percentages in an HTML table, a
// window grown past the cells it has stretches its columns and rows, and
// everything else in them with it. Rows marked in skipCols lay out their
// own columns, so they do not count towards the shared ones.
func trackWeights(rects []cellRect, factors []float64, cols, rows int, skipCols []bool) (colW, rowH []float64) {
	colSum, colN := make([]float64, cols), make([]int, cols)
	rowSum, rowN := make([]float64, rows), make([]int, rows)
	for i, r := range rects {
		if skipCols == nil || !skipCols[r.r0] {
			for x := r.c0; x <= r.c1; x++ {
				colSum[x] += factors[i]
				colN[x]++
			}
		}
		for y := r.r0; y <= r.r1; y++ {
			rowSum[y] += factors[i]
			rowN[y]++
		}
	}
	mean := func(sum []float64, count []int) []float64 {
		out := make([]float64, len(sum))
		for i := range sum {
			out[i] = 1
			if count[i] > 0 {
				out[i] = sum[i] / float64(count[i])
			}
		}
		return out
	}
	return mean(colSum, colN), mean(rowSum, rowN)
}
