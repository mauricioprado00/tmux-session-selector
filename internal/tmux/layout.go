package tmux

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Layout is a node of a tmux window layout, as in #{window_layout} and
// select-layout: a pane, or a split whose children are side by side
// (written {…}) or stacked (written […]), separated by one-cell borders.
type Layout struct {
	W, H, X, Y int
	Pane       int  // the pane's number, as in %7; -1 for a split
	SideBySide bool // for a split: children left to right, else top to bottom
	Children   []*Layout
}

// ParseLayout parses a layout string such as
// "77dd,100x30,0,0{50x30,0,0,0,49x30,51,0,1}".
func ParseLayout(s string) (*Layout, error) {
	_, body, ok := strings.Cut(s, ",")
	if !ok {
		return nil, fmt.Errorf("bad layout %q", s)
	}
	p := &layoutParser{s: body}
	l, err := p.node()
	if err == nil && p.i != len(p.s) {
		err = errors.New("trailing text")
	}
	if err != nil {
		return nil, fmt.Errorf("bad layout %q: %w", s, err)
	}
	return l, nil
}

type layoutParser struct {
	s string
	i int
}

func (p *layoutParser) number() (int, error) {
	start := p.i
	for p.i < len(p.s) && p.s[p.i] >= '0' && p.s[p.i] <= '9' {
		p.i++
	}
	if start == p.i {
		return 0, fmt.Errorf("number expected at %d", start)
	}
	return strconv.Atoi(p.s[start:p.i])
}

func (p *layoutParser) expect(c byte) error {
	if p.i >= len(p.s) || p.s[p.i] != c {
		return fmt.Errorf("%q expected at %d", c, p.i)
	}
	p.i++
	return nil
}

// node parses "WxH,X,Y" followed by ",pane", "{…}" or "[…]".
func (p *layoutParser) node() (*Layout, error) {
	l := &Layout{Pane: -1}
	var err error
	for i, field := range []*int{&l.W, &l.H, &l.X, &l.Y} {
		if i > 0 {
			sep := byte(',')
			if i == 1 {
				sep = 'x'
			}
			if err := p.expect(sep); err != nil {
				return nil, err
			}
		}
		if *field, err = p.number(); err != nil {
			return nil, err
		}
	}
	if p.i >= len(p.s) {
		return nil, errors.New("unexpected end")
	}
	switch p.s[p.i] {
	case ',':
		p.i++
		l.Pane, err = p.number()
		return l, err
	case '{', '[':
		l.SideBySide = p.s[p.i] == '{'
		end := byte(']')
		if l.SideBySide {
			end = '}'
		}
		p.i++
		for {
			c, err := p.node()
			if err != nil {
				return nil, err
			}
			l.Children = append(l.Children, c)
			if p.i < len(p.s) && p.s[p.i] == ',' {
				p.i++
				continue
			}
			return l, p.expect(end)
		}
	}
	return nil, fmt.Errorf("unexpected %q at %d", p.s[p.i], p.i)
}

// String is the layout as select-layout takes it, with its checksum.
func (l *Layout) String() string {
	var b strings.Builder
	l.write(&b)
	return fmt.Sprintf("%04x,%s", layoutChecksum(b.String()), b.String())
}

func (l *Layout) write(b *strings.Builder) {
	fmt.Fprintf(b, "%dx%d,%d,%d", l.W, l.H, l.X, l.Y)
	if len(l.Children) == 0 {
		fmt.Fprintf(b, ",%d", l.Pane)
		return
	}
	open, close := "[", "]"
	if l.SideBySide {
		open, close = "{", "}"
	}
	b.WriteString(open)
	for i, c := range l.Children {
		if i > 0 {
			b.WriteByte(',')
		}
		c.write(b)
	}
	b.WriteString(close)
}

// layoutChecksum is tmux's layout_checksum.
func layoutChecksum(s string) uint16 {
	var sum uint16
	for i := 0; i < len(s); i++ {
		sum = sum>>1 + (sum&1)<<15
		sum += uint16(s[i])
	}
	return sum
}

// Panes are the layout's pane numbers, in order.
func (l *Layout) Panes() []int {
	if len(l.Children) == 0 {
		return []int{l.Pane}
	}
	var panes []int
	for _, c := range l.Children {
		panes = append(panes, c.Panes()...)
	}
	return panes
}

// Fit returns a copy of l resized to w×h at (x, y), each split sharing its
// space among its children in proportion to their old sizes. It fails if
// some pane would get less than one cell.
func (l *Layout) Fit(x, y, w, h int) (*Layout, error) {
	if w < 1 || h < 1 {
		return nil, errors.New("no room for a pane")
	}
	out := &Layout{W: w, H: h, X: x, Y: y, Pane: l.Pane, SideBySide: l.SideBySide}
	if len(l.Children) == 0 {
		return out, nil
	}
	weights := make([]int, len(l.Children))
	for i, c := range l.Children {
		weights[i] = c.H
		if l.SideBySide {
			weights[i] = c.W
		}
	}
	total := h
	if l.SideBySide {
		total = w
	}
	sizes, err := Share(total, weights)
	if err != nil {
		return nil, err
	}
	at := 0
	for i, c := range l.Children {
		var fc *Layout
		if l.SideBySide {
			fc, err = c.Fit(x+at, y, sizes[i], h)
		} else {
			fc, err = c.Fit(x, y+at, w, sizes[i])
		}
		if err != nil {
			return nil, err
		}
		out.Children = append(out.Children, fc)
		at += sizes[i] + 1 // and a border
	}
	return out, nil
}

// Share divides total cells among children in proportion to weights,
// leaving one cell between neighbours for a border. Every child gets at
// least one cell, or it fails.
func Share(total int, weights []int) ([]int, error) {
	n := len(weights)
	room := total - (n - 1)
	if room < n {
		return nil, fmt.Errorf("%d cells cannot hold %d panes", total, n)
	}
	sum := 0
	for _, w := range weights {
		sum += max(1, w)
	}
	// Largest remainder: each child gets the whole cells of its exact share,
	// and the cells left go to those whose share was nearest the next cell.
	sizes := make([]int, n)
	rest := make([]int, n)
	used := 0
	for i, w := range weights {
		sizes[i] = room * max(1, w) / sum
		rest[i] = room * max(1, w) % sum
		used += sizes[i]
	}
	order := make([]int, n)
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool { return rest[order[a]] > rest[order[b]] })
	for k := 0; used < room; k = (k + 1) % n {
		sizes[order[k]]++
		used++
	}
	// Every child needs a cell; take them from the largest.
	for i := range sizes {
		for sizes[i] < 1 {
			big := 0
			for j := range sizes {
				if sizes[j] > sizes[big] {
					big = j
				}
			}
			if sizes[big] <= 1 {
				return nil, fmt.Errorf("%d cells cannot hold %d panes", total, n)
			}
			sizes[big]--
			sizes[i]++
		}
	}
	return sizes, nil
}
