package mdterm

import (
	"regexp"
	"strings"
	"testing"
)

var sgrRE = regexp.MustCompile("\x1b\\[[0-9;]*m")

// stripSGR removes ANSI SGR escapes so tests can assert on visible layout.
func stripSGR(s string) string { return sgrRE.ReplaceAllString(s, "") }

// visLines returns the table's rendered lines with ANSI stripped, trailing
// spaces kept (they carry the column padding).
func visLines(src string, width int) []string {
	out := stripSGR(RenderStringWidth(src, width))
	return strings.Split(strings.TrimRight(out, "\n"), "\n")
}

func TestWrapStyledBreaksOnWhitespaceNotMidWord(t *testing.T) {
	// The glow regression: "high" must never become "hig" + "h".
	lines := wrapStyled(parseStyled("high med low"), 4)
	for _, l := range lines {
		if w := visWidth(l); w > 4 {
			t.Fatalf("line exceeds width 4: %q (%d)", plainText(l), w)
		}
	}
	joined := ""
	for _, l := range lines {
		joined += plainText(l) + "|"
	}
	if !strings.Contains(joined, "high") {
		t.Fatalf("word 'high' was broken across lines: %q", joined)
	}
}

func TestWrapStyledHardBreaksOverlongToken(t *testing.T) {
	// A token longer than the column has no whitespace to break on, so it must
	// hard-break (a 128-char filepath can't dictate an impossibly wide column).
	const tok = "/very/long/path/segment/that/exceeds/the/column/width/considerably"
	lines := wrapStyled(parseStyled(tok), 10)
	if len(lines) < 2 {
		t.Fatalf("overlong token was not hard-broken: %d line(s)", len(lines))
	}
	var got string
	for _, l := range lines {
		if w := visWidth(l); w > 10 {
			t.Fatalf("hard-broken line exceeds width 10: %q (%d)", plainText(l), w)
		}
		got += plainText(l)
	}
	if got != tok {
		t.Fatalf("hard-break lost characters: %q != %q", got, tok)
	}
}

// tableWidth is the total rendered width of a column layout (content + chrome).
func tableWidth(col []int) int {
	total := 0
	for _, w := range col {
		total += w
	}
	return total + colSepWidth*(len(col)-1)
}

func TestAllocateTrivialFit(t *testing.T) {
	natural := []int{3, 5, 4}
	col := allocateColumns(natural, natural, 100, 0)
	for j := range natural {
		if col[j] != natural[j] {
			t.Fatalf("col %d: expected natural %d, got %d (should not expand to fill)", j, natural[j], col[j])
		}
	}
}

func TestAllocateShrinksNoColumnBelowMinCol(t *testing.T) {
	width, n := 100, 4
	natural := []int{80, 70, 60, 90} // all far wider than fits
	median := []int{80, 70, 60, 90}  // no outliers → medians == naturals
	minCol := int(float64(width) / (float64(n) * minColDivisor))
	col := allocateColumns(natural, median, width, 0)
	for j := range col {
		if col[j] < minCol {
			t.Fatalf("col %d shrank below minCol %d: %d", j, minCol, col[j])
		}
	}
}

func TestAllocatePinsNaturallyNarrowColumn(t *testing.T) {
	// A 2-wide "ID" column must never be padded up to minCol nor shrunk.
	natural := []int{2, 200}
	col := allocateColumns(natural, natural, 80, 0)
	if col[0] != 2 {
		t.Fatalf("narrow column not pinned at natural width 2: %d", col[0])
	}
}

func TestAllocateNeverExceedsWidth(t *testing.T) {
	// A table must always fit the screen; less -R wraps a wider line unusably.
	width := 100
	natural := []int{400, 400, 400}
	col := allocateColumns(natural, natural, width, 0)
	if tableWidth(col) > width {
		t.Fatalf("table width %d exceeds screen width %d", tableWidth(col), width)
	}
}

func TestAllocateReclaimsOutlierColumnTowardMedian(t *testing.T) {
	// One long value inflates col 1's natural width, but its median is small; the
	// content column (col 2) is what needs the room. The median pass must shrink
	// col 1 well below its natural so col 2 gets more than col 1.
	width := 100
	natural := []int{17, 28, 250} // col1 natural 28 driven by a single outlier
	median := []int{15, 9, 90}    // col1 typically ~9 wide
	col := allocateColumns(natural, median, width, 0)
	if tableWidth(col) > width {
		t.Fatalf("table width %d exceeds screen width %d", tableWidth(col), width)
	}
	if col[1] >= col[2] {
		t.Fatalf("outlier column %d not reclaimed: col1=%d should be << col2=%d", 1, col[1], col[2])
	}
}

func TestTableParsedNotMangled(t *testing.T) {
	src := "| A | B |\n|---|---|\n| 1 | 2 |\n"
	lines := visLines(src, 80)
	if len(lines) < 3 {
		t.Fatalf("table collapsed instead of rendering rows: %q", lines)
	}
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "│") {
		t.Fatalf("no column separator — table not rendered as a table: %q", joined)
	}
	if !strings.Contains(joined, "─") {
		t.Fatalf("no header rule — table not rendered as a table: %q", joined)
	}
}

func TestHeaderNotTruncatedWhenItFits(t *testing.T) {
	src := "| Frequency |\n|---|\n| high |\n| med |\n"
	out := RenderStringWidth(src, 100)
	if !strings.Contains(stripSGR(out), "Frequency") {
		t.Fatalf("header 'Frequency' should render in full: %q", stripSGR(out))
	}
	if strings.Contains(out, "…") {
		t.Fatalf("header should not be truncated when it fits: %q", stripSGR(out))
	}
	// And the sub-word regression: "high" stays whole.
	if !strings.Contains(stripSGR(out), "high") {
		t.Fatalf("value 'high' should render whole: %q", stripSGR(out))
	}
}

func TestHeaderTruncationEmitsLegend(t *testing.T) {
	// Three narrow columns squeeze the long header below the width where it can
	// fit in maxHeaderRows lines, forcing truncation + a legend.
	const fullHdr = "Alpha Beta Gamma Delta Epsilon Zeta"
	src := "| " + fullHdr + " | Content One Two | Content Four Six |\n" +
		"|---|---|---|\n| x | y | z |\n"
	out := stripSGR(RenderStringWidth(src, 20))
	if !strings.Contains(out, "…") {
		t.Fatalf("expected header truncation ellipsis at narrow width: %q", out)
	}
	if !strings.Contains(out, "┌─ columns") {
		t.Fatalf("expected legend box: %q", out)
	}
	if !strings.Contains(out, fullHdr) {
		t.Fatalf("legend must carry the full header text: %q", out)
	}
}

func TestColumnAlignment(t *testing.T) {
	src := "| L | R |\n|:--|--:|\n| a | 1 |\n"
	lines := visLines(src, 40)
	// Data row: left cell "a" flush left, right cell "1" flush right (padded left).
	var data string
	for _, l := range lines {
		if strings.HasPrefix(l, "a") {
			data = l
			break
		}
	}
	if data == "" {
		t.Fatalf("data row not found: %q", lines)
	}
	parts := strings.Split(data, "│")
	if len(parts) != 2 {
		t.Fatalf("expected 2 columns: %q", data)
	}
	right := parts[1]
	if !strings.HasSuffix(strings.TrimRight(right, " "), "1") {
		t.Fatalf("right-aligned cell should end at the digit with no trailing pad: %q", right)
	}
	if !strings.HasPrefix(strings.TrimLeft(right, " "), "1") || right[len(right)-1] != '1' {
		t.Fatalf("right column not right-aligned: %q", right)
	}
}

func TestEveryTableLineEndsWithReset(t *testing.T) {
	src := "| A | B |\n|---|---|\n| 1 | 2 |\n"
	out := RenderStringWidth(src, 80)
	for line := range strings.SplitSeq(strings.TrimRight(out, "\n"), "\n") {
		if !strings.HasSuffix(line, reset) {
			t.Fatalf("line does not end with SGR reset (less -R needs this): %q", line)
		}
	}
}
