package tui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/sahilm/fuzzy"
)

// row is one line of a picker. Views build rows from their own data and look
// the data back up by key.
type row struct {
	key   string   // stable identity, survives refreshes
	name  string   // left column; the text the filter matches
	cols  []string // right-hand columns, shown dim
	badge string   // short pre-styled marker after the name
	dim   bool
}

type match struct {
	idx int   // index into rows
	hl  []int // byte offsets in name that matched the query
}

// picker is the list half of every view: rows, a fuzzy filter over them, and a
// cursor. It is the same component for projects, repos and pull requests.
type picker struct {
	rows    []row
	matches []match
	query   string
	cursor  int
	offset  int
}

// setRows replaces the rows and keeps the cursor on the same key if it is
// still there, so a background refresh never moves the selection.
func (p *picker) setRows(rows []row) {
	key := p.selectedKey()
	p.rows = rows
	p.refilter()
	p.cursor = min(p.cursor, max(len(p.matches)-1, 0))
	p.selectKey(key)
}

func (p *picker) setQuery(q string) {
	if q == p.query {
		return
	}
	p.query = q
	p.refilter()
	p.cursor, p.offset = 0, 0 // best match first, as in fzf
}

// refilter puts rows containing the query as a substring first, in their
// original order, and looser fuzzy matches after them by score. Ranking purely
// by fuzzy score would favour short names and throw away the order the rows
// came in, which for repos is how recently they were pushed to.
func (p *picker) refilter() {
	p.matches = p.matches[:0]
	if p.query == "" {
		for i := range p.rows {
			p.matches = append(p.matches, match{idx: i})
		}
		return
	}
	q := strings.ToLower(p.query)
	names := make([]string, len(p.rows))
	exact := make([]bool, len(p.rows))
	for i, r := range p.rows {
		names[i] = r.name
		// ToLower can change a string's byte length outside ASCII; offsets are
		// only trusted when it has not.
		if lower := strings.ToLower(r.name); len(lower) == len(r.name) {
			if at := strings.Index(lower, q); at >= 0 {
				exact[i] = true
				hl := make([]int, 0, len(q))
				for j := range r.name[at : at+len(q)] {
					hl = append(hl, at+j)
				}
				p.matches = append(p.matches, match{idx: i, hl: hl})
			}
		}
	}
	for _, m := range fuzzy.Find(p.query, names) {
		if !exact[m.Index] {
			p.matches = append(p.matches, match{idx: m.Index, hl: m.MatchedIndexes})
		}
	}
}

func (p *picker) move(delta int) {
	if len(p.matches) == 0 {
		return
	}
	p.cursor = min(max(p.cursor+delta, 0), len(p.matches)-1)
}

func (p *picker) selected() (row, bool) {
	if p.cursor < 0 || p.cursor >= len(p.matches) {
		return row{}, false
	}
	return p.rows[p.matches[p.cursor].idx], true
}

func (p *picker) selectedKey() string {
	r, _ := p.selected()
	return r.key
}

func (p *picker) selectKey(key string) {
	if key == "" {
		return
	}
	for i, m := range p.matches {
		if p.rows[m.idx].key == key {
			p.cursor = i
			return
		}
	}
}

// view renders height lines of width cells, scrolling to keep the cursor in.
func (p *picker) view(width, height int) []string {
	if p.cursor < p.offset {
		p.offset = p.cursor
	}
	if p.cursor >= p.offset+height {
		p.offset = p.cursor - height + 1
	}
	p.offset = max(min(p.offset, len(p.matches)-height), 0)

	lines := make([]string, 0, height)
	for i := p.offset; i < len(p.matches) && len(lines) < height; i++ {
		m := p.matches[i]
		lines = append(lines, renderRow(p.rows[m.idx], m.hl, i == p.cursor, width))
	}
	return lines
}

const (
	maxColWidth  = 22
	minNameWidth = 24
)

func renderRow(r row, hl []int, selected bool, width int) string {
	bar := "  "
	if selected {
		bar = sAccent.Render("▌") + " "
	}
	// The name gets whatever the right-hand columns leave. They are capped so
	// a long branch name cannot crowd it out, and dropped from the left, least
	// important first, when the pane is still too narrow for both.
	cols := nonEmpty(r.cols)
	for i, c := range cols {
		cols[i] = ansi.Truncate(c, maxColWidth, "…")
	}
	right := strings.Join(cols, "  ")
	nameW := width - 2 - ansi.StringWidth(right) - 2
	for nameW < minNameWidth && len(cols) > 0 {
		cols = cols[1:]
		right = strings.Join(cols, "  ")
		nameW = width - 2 - ansi.StringWidth(right) - 2
	}
	if right == "" {
		nameW = width - 2
	}
	badgeW := ansi.StringWidth(r.badge)
	if badgeW > 0 {
		nameW -= badgeW + 1
	}

	name := highlight(ansi.Truncate(r.name, max(nameW, 1), "…"), hl, selected, r.dim)
	if badgeW > 0 {
		name += " " + r.badge
		nameW += badgeW + 1
	}
	line := bar + fit(name, nameW)
	if right != "" {
		line += "  " + sDim.Render(right)
	}
	return line
}

// highlight styles name, picking out the bytes the query matched.
func highlight(name string, hl []int, selected, dim bool) string {
	base := func(s string) string {
		switch {
		case selected:
			return sBold.Render(s)
		case dim:
			return sDim.Render(s)
		}
		return s
	}
	if len(hl) == 0 {
		return base(name)
	}
	matched := make(map[int]bool, len(hl))
	for _, i := range hl {
		matched[i] = true
	}
	var b strings.Builder
	start := 0
	flush := func(end int, isMatch bool) {
		if end <= start {
			return
		}
		if isMatch {
			b.WriteString(sMatch.Render(name[start:end]))
		} else {
			b.WriteString(base(name[start:end]))
		}
		start = end
	}
	prev := false
	for i := range name { // i walks rune starts, which is what fuzzy reports
		if cur := matched[i]; cur != prev {
			flush(i, prev)
			prev = cur
		}
	}
	flush(len(name), prev)
	return b.String()
}

func nonEmpty(in []string) []string {
	out := in[:0:0]
	for _, s := range in {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}
