package mdterm

// Table rendering for mdterm (E-1775). goldmark's default parser does not
// recognize GFM tables, so RenderStringWidth registers the table extension; the
// nodes it produces are laid out here.
//
// The layout is width-aware and wraps cells judiciously rather than reflowing
// prose the way glow/glamour does. Its two guiding fixes versus glamour's table
// renderer: (1) a header is never silently truncated — it wraps up to
// maxHeaderRows and, only if it still overflows, is truncated *with* a legend
// mapping it back to its full text; (2) a column is never shrunk below the
// computed minCol floor, and wrapping breaks on whitespace — a token is
// hard-broken only when it alone exceeds the column width. Columns that still
// wrap too tall are grown back out past the screen width (the user pans in
// `less`), capped so at least two columns stay visible.

import (
	"strings"
	"unicode/utf8"

	"github.com/mattn/go-runewidth"
	"github.com/yuin/goldmark/ast"
	xast "github.com/yuin/goldmark/extension/ast"
)

// Tunable layout constants. There is no closed-form optimum; these are balanced
// empirically against real tables (the E-1775 eyeball harness).
const (
	minColDivisor  = 2.5  // minCol = floor(W / (N * minColDivisor))
	maxHeaderRows  = 3    // a header wraps to at most this many rows, then truncates
	targetMaxRows  = 6    // a cell taller than this triggers aspect-relaxation growth
	maxColFraction = 0.65 // soft per-column width cap, as a fraction of the screen width
	colSepWidth    = 3    // visible width of the " │ " column separator
)

// styledRune is one visible rune paired with the SGR sequence active at its
// position. Escapes carry no rune of their own; they only mutate the active
// style. This representation makes width measurement, word-wrapping, and
// re-emission trivial and ANSI-safe (padding math never has to strip escapes).
type styledRune struct {
	r     rune
	style string
}

// legendEntry maps a truncated header's on-screen form to its full text.
type legendEntry struct {
	short string
	full  string
}

// renderTable lays out a GFM table node. An empty table (no header) is skipped.
func (r *renderer) renderTable(t *xast.Table, indent string) {
	var header [][]styledRune
	var rows [][][]styledRune
	for c := t.FirstChild(); c != nil; c = c.NextSibling() {
		switch c.Kind() {
		case xast.KindTableHeader:
			header = r.gatherCells(c)
		case xast.KindTableRow:
			rows = append(rows, r.gatherCells(c))
		}
	}
	if len(header) > 0 {
		r.layoutTable(header, rows, t.Alignments, indent)
	}
}

// layoutTable measures, allocates, and emits a non-empty table.
func (r *renderer) layoutTable(header [][]styledRune, rows [][][]styledRune, aligns []xast.Alignment, indent string) {
	n := len(header)

	// Column-major cell lists (header first) for measurement and growth.
	colCells := make([][][]styledRune, n)
	natural := make([]int, n)
	for j := range n {
		colCells[j] = append(colCells[j], header[j])
		natural[j] = visWidth(header[j])
		for _, row := range rows {
			if j < len(row) {
				colCells[j] = append(colCells[j], row[j])
				natural[j] = max(natural[j], visWidth(row[j]))
			}
		}
	}

	col := allocateColumns(natural, colCells, r.width, runewidth.StringWidth(indent))

	var legend []legendEntry
	r.emitRow(header, col, aligns, indent, true, &legend)
	r.emitRule(col, indent)
	for _, row := range rows {
		r.emitRow(row, col, aligns, indent, false, nil)
	}
	if len(legend) > 0 {
		r.emitLegend(legend, indent)
	}
}

// gatherCells renders every cell of a header/row node to styled runes.
func (r *renderer) gatherCells(row ast.Node) [][]styledRune {
	var cells [][]styledRune
	for c := row.FirstChild(); c != nil; c = c.NextSibling() {
		if c.Kind() == xast.KindTableCell {
			cells = append(cells, parseStyled(r.inlineLine(c)))
		}
	}
	return cells
}

// allocateColumns returns the content width of each column.
//
//	Phase 1  everything fits          → natural widths (compact; never expanded)
//	Phase 2  too wide                 → shave the widest column to the minCol floor
//	Phase 3  a column still too tall  → grow it back out (pan territory), capped
func allocateColumns(natural []int, colCells [][][]styledRune, width, indentW int) []int {
	n := len(natural)
	minCol := max(int(float64(width)/(float64(n)*minColDivisor)), 1)
	avail := max(width-indentW-colSepWidth*(n-1), n)

	col := make([]int, n)
	sum := 0
	for j := range col {
		col[j] = natural[j]
		sum += col[j]
	}

	// Phase 2: shrink the currently-widest shrinkable column until we fit.
	for sum > avail {
		bj, bw := -1, -1
		for j := range col {
			// A naturally-narrow column is pinned at its width, never wrapped.
			floor := min(minCol, natural[j])
			if col[j] <= floor {
				continue
			}
			if col[j] > bw {
				bw = col[j]
				bj = j
			}
		}
		if bj < 0 {
			break // every column is at its floor; the table will overflow (pannable)
		}
		col[bj]--
		sum--
	}

	// Phase 3: grow columns that still wrap taller than targetMaxRows, allowing
	// the table to exceed the screen width. Capped so >=2 columns stay visible.
	hardCap := width - minCol
	softCap := int(maxColFraction * float64(width))
	for j := range col {
		colCap := min(natural[j], softCap, hardCap)
		for col[j] < colCap && tallestRows(colCells[j], col[j]) > targetMaxRows {
			col[j]++
		}
	}
	return col
}

// tallestRows is the largest wrapped-line count among a column's cells at width w.
func tallestRows(cells [][]styledRune, w int) (tallest int) {
	tallest = 1
	for _, c := range cells {
		tallest = max(tallest, len(wrapStyled(c, w)))
	}
	return tallest
}

// emitRow wraps every cell to its column width and emits the (possibly
// multi-line) row, cells joined by a dim " │ " separator. Header cells render
// bold and, when they overflow maxHeaderRows, are truncated with a legend entry.
func (r *renderer) emitRow(cells [][]styledRune, col []int, aligns []xast.Alignment, indent string, isHeader bool, legend *[]legendEntry) {
	n := len(col)
	wrapped := make([][][]styledRune, n)
	height := 1
	for j := range n {
		var content []styledRune
		if j < len(cells) {
			content = cells[j]
		}
		styled := content
		if isHeader {
			styled = restyle(content, boldStyle)
		}
		lines := wrapStyled(styled, col[j])
		if isHeader && len(lines) > maxHeaderRows {
			lines = lines[:maxHeaderRows]
			lines[maxHeaderRows-1] = ellipsize(lines[maxHeaderRows-1], col[j])
			*legend = append(*legend, legendEntry{
				short: shortForm(lines),
				full:  plainText(content),
			})
		}
		wrapped[j] = lines
		if len(lines) > height {
			height = len(lines)
		}
	}

	for li := range height {
		var b strings.Builder
		for j := range n {
			if j > 0 {
				b.WriteString(" " + quoteStyle + "│" + reset + " ")
			}
			var lr []styledRune
			if li < len(wrapped[j]) {
				lr = wrapped[j][li]
			}
			al := xast.AlignNone
			if j < len(aligns) {
				al = aligns[j]
			}
			b.WriteString(emitCellLine(lr, col[j], al))
		}
		r.line(indent, b.String())
	}
}

// emitRule emits the dim horizontal rule under the header.
func (r *renderer) emitRule(col []int, indent string) {
	total := 0
	for j, w := range col {
		total += w
		if j < len(col)-1 {
			total += colSepWidth
		}
	}
	r.line(indent, dimStyle+strings.Repeat("─", total))
}

// emitLegend emits the bordered legend box, below the table, listing the full
// text of every header that was truncated.
func (r *renderer) emitLegend(entries []legendEntry, indent string) {
	texts := make([]string, len(entries))
	inner := runewidth.StringWidth("columns") + 2
	for i, e := range entries {
		texts[i] = e.short + " = " + e.full
		inner = max(inner, runewidth.StringWidth(texts[i])+2)
	}
	head := "─ columns "
	r.b.WriteString("\n") // blank line before the box
	r.line(indent, dimStyle+"┌"+head+strings.Repeat("─", inner-runewidth.StringWidth(head))+"┐")
	for _, t := range texts {
		pad := inner - runewidth.StringWidth(t) - 1
		r.line(indent, dimStyle+"│ "+t+strings.Repeat(" ", pad)+"│")
	}
	r.line(indent, dimStyle+"└"+strings.Repeat("─", inner)+"┘")
}

// parseStyled splits a rendered ANSI string into visible runes, each tagged with
// the SGR sequence active when it appears. A reset (ESC[0m) clears the active
// style; any other CSI SGR accumulates onto it — mirroring how the inline
// renderer opens a span and re-emits the ambient style after closing it.
func parseStyled(s string) []styledRune {
	var out []styledRune
	active := ""
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			j := i + 1
			if j < len(s) && s[j] == '[' {
				j++
				for j < len(s) && !(s[j] >= '@' && s[j] <= '~') {
					j++
				}
				if j < len(s) {
					j++ // include the final byte (e.g. 'm')
				}
			}
			esc := s[i:j]
			active += esc
			if esc == reset {
				active = "" // a reset clears all accumulated style
			}
			i = j
			continue
		}
		ru, size := utf8.DecodeRuneInString(s[i:])
		out = append(out, styledRune{r: ru, style: active})
		i += size
	}
	return out
}

// wrapStyled breaks styled runes into physical lines of at most `width` visible
// columns, breaking at spaces where possible and hard-breaking a single token
// that alone exceeds width. An empty input yields one empty line.
func wrapStyled(rs []styledRune, width int) [][]styledRune {
	if width < 1 {
		width = 1
	}
	var lines [][]styledRune
	var line []styledRune
	lineW := 0
	emit := func() {
		lines = append(lines, line)
		line = nil
		lineW = 0
	}
	appendWord := func(word []styledRune) {
		wordW := visWidth(word)
		// Hard-break a word that is wider than a whole line.
		for wordW > width {
			if lineW > 0 {
				emit()
			}
			chunk, chunkW := takeWidth(word, width)
			line = chunk
			lineW = chunkW
			emit()
			word = word[len(chunk):]
			wordW = visWidth(word)
		}
		if wordW > 0 {
			if lineW > 0 && lineW+1+wordW > width {
				emit()
			}
			if lineW > 0 {
				line = append(line, styledRune{r: ' '})
				lineW++
			}
			line = append(line, word...)
			lineW += wordW
		}
	}

	var word []styledRune
	for _, sr := range rs {
		if sr.r == ' ' || sr.r == '\n' {
			if len(word) > 0 {
				appendWord(word)
				word = nil
			}
			if sr.r == '\n' {
				emit()
			}
			continue
		}
		word = append(word, sr)
	}
	if len(word) > 0 {
		appendWord(word)
	}
	if lineW > 0 || len(lines) == 0 {
		emit()
	}
	return lines
}

// emitCellLine renders one wrapped physical line padded to width w with the
// given alignment. Styling is closed with a reset before any padding so the pad
// is never colored; the caller's line() appends the final line reset.
func emitCellLine(rs []styledRune, w int, align xast.Alignment) string {
	if visWidth(rs) > w {
		rs, _ = takeWidth(rs, w)
	}
	pad := w - visWidth(rs)
	left, right := 0, pad
	switch align {
	case xast.AlignRight:
		left, right = pad, 0
	case xast.AlignCenter:
		left = pad / 2
		right = pad - left
	}
	var b strings.Builder
	b.WriteString(strings.Repeat(" ", left))
	cur := ""
	for _, sr := range rs {
		if sr.style != cur {
			b.WriteString(reset)
			b.WriteString(sr.style)
			cur = sr.style
		}
		b.WriteRune(sr.r)
	}
	if cur != "" {
		b.WriteString(reset)
	}
	b.WriteString(strings.Repeat(" ", right))
	return b.String()
}

// takeWidth returns the longest prefix of rs whose visible width is <= width
// (at least one rune), plus that prefix's width.
func takeWidth(rs []styledRune, width int) (prefix []styledRune, w int) {
	prefix = rs
	for i, sr := range rs {
		rw := runewidth.RuneWidth(sr.r)
		if w+rw > width && i > 0 {
			prefix = rs[:i]
			goto end
		}
		w += rw
	}
end:
	return prefix, w
}

// ellipsize trims the line to leave room for a trailing … within width.
func ellipsize(line []styledRune, width int) []styledRune {
	target := max(width-1, 0)
	kept, _ := takeWidth(line, target)
	out := append([]styledRune(nil), kept...)
	return append(out, styledRune{r: '…'})
}

// restyle prepends an SGR prefix to every rune's active style.
func restyle(rs []styledRune, prefix string) []styledRune {
	out := make([]styledRune, len(rs))
	for i, sr := range rs {
		out[i] = styledRune{r: sr.r, style: prefix + sr.style}
	}
	return out
}

// visWidth is the total visible column width of styled runes.
func visWidth(rs []styledRune) int {
	w := 0
	for _, sr := range rs {
		w += runewidth.RuneWidth(sr.r)
	}
	return w
}

// plainText is the unstyled text of styled runes.
func plainText(rs []styledRune) string {
	var b strings.Builder
	for _, sr := range rs {
		b.WriteRune(sr.r)
	}
	return b.String()
}

// shortForm renders the truncated header lines as a single plain-text token for
// the legend's left-hand side.
func shortForm(lines [][]styledRune) string {
	parts := make([]string, len(lines))
	for i, l := range lines {
		parts[i] = plainText(l)
	}
	return strings.Join(parts, " ")
}
