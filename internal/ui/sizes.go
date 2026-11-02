package ui

import (
	"math"
	"strconv"
	"strings"

	"github.com/mauricioprado00/tmux-session-selector/internal/tmux"
)

// winSize is how much room a window asks for in the overview, relative to
// the others, in steps of sizeStep: normal for its place in the even grid or
// the ring, middle for when it is the centre window. Both start at 0, size
// 1; + and - change the one in use for the window in focus by a step.
type winSize struct {
	normal, middle int
}

// windowSizes holds the sizes that differ from 1, by window id.
type windowSizes map[string]winSize

const (
	sizeStep = 1.25
	maxSteps = 6 // 1.25⁶ ≈ 3.8 times as big, or a quarter as big
	// sizeKey prefixes a window's size in the state file: size.@3=2,-1 is
	// two steps bigger normally and one step smaller in the centre.
	sizeKey = "size."
)

// weight is the size that steps stand for.
func weight(steps int) float64 { return math.Pow(sizeStep, float64(steps)) }

// of is how much room window w asks for: its middle size if it is the
// centre window, else its normal size. A nil windowSizes gives 1.
func (s windowSizes) of(w *tmux.Window, centre bool) float64 {
	z := s[w.ID]
	if centre {
		return weight(z.middle)
	}
	return weight(z.normal)
}

// change changes one of window id's sizes by d steps, within maxSteps, or
// with reset sets it back to 1. It returns the new size.
func (s windowSizes) change(id string, centre bool, d int, reset bool) float64 {
	z := s[id]
	v := &z.normal
	if centre {
		v = &z.middle
	}
	*v = max(-maxSteps, min(maxSteps, *v+d))
	if reset {
		*v = 0
	}
	if z == (winSize{}) {
		delete(s, id)
	} else {
		s[id] = z
	}
	return weight(*v)
}

// readSizes reads the sizes saved in state.
func readSizes(state map[string]string) windowSizes {
	sizes := windowSizes{}
	for k, v := range state {
		id, ok := strings.CutPrefix(k, sizeKey)
		if !ok {
			continue
		}
		a, b, _ := strings.Cut(v, ",")
		n, err1 := strconv.Atoi(a)
		c, err2 := strconv.Atoi(b)
		if err1 != nil || err2 != nil {
			continue
		}
		z := winSize{max(-maxSteps, min(maxSteps, n)), max(-maxSteps, min(maxSteps, c))}
		if z != (winSize{}) {
			sizes[id] = z
		}
	}
	return sizes
}

// writeSizes puts sizes into state in place of the ones there, leaving out
// windows not in live: closed windows lose their sizes, as tmux may give
// their ids to new windows.
func writeSizes(state map[string]string, sizes windowSizes, live map[string]bool) {
	for k := range state {
		if strings.HasPrefix(k, sizeKey) {
			delete(state, k)
		}
	}
	for id, z := range sizes {
		if live[id] {
			state[sizeKey+id] = strconv.Itoa(z.normal) + "," + strconv.Itoa(z.middle)
		}
	}
}

// cuts cuts total cells into parts in proportion to weights, and returns
// where each part starts, with total at the end. Equal weights give the
// same cuts as i*total/len(weights).
func cuts(total int, weights []float64) []int {
	n := len(weights)
	c := make([]int, n+1)
	equal := true
	sum := 0.0
	for _, w := range weights {
		sum += w
		equal = equal && w == weights[0]
	}
	acc := 0.0
	for i, w := range weights {
		if equal {
			c[i] = i * total / n
		} else {
			c[i] = int(math.Floor(float64(total)*acc/sum + 1e-9))
		}
		acc += w
	}
	c[n] = total
	return c
}
