package taskrow

import (
	"testing"

	"github.com/mattn/go-runewidth"
)

// TestGlyphsAreSingleWidth pins the property the fixed-width prefix rests on. A
// two-column glyph shifts every following column on that row only, which reads
// as a corrupt table rather than as a bad glyph choice.
func TestGlyphsAreSingleWidth(t *testing.T) {
	for _, a := range Actions() {
		if w := runewidth.StringWidth(a.Icon()); w != 1 {
			t.Errorf("%s glyph %q measures %d columns, want 1", a.Label(), a.Icon(), w)
		}
	}
}

// TestGlyphsAreDistinct: one glyph, one meaning, or the legend lies.
func TestGlyphsAreDistinct(t *testing.T) {
	seen := map[string]string{}
	for _, a := range Actions() {
		if prev, ok := seen[a.Icon()]; ok {
			t.Errorf("%q is both %s and %s", a.Icon(), prev, a.Label())
		}
		seen[a.Icon()] = a.Label()
		if a.Label() == "" {
			t.Errorf("action %d has no label", a)
		}
	}
}

func TestClassify(t *testing.T) {
	cases := []struct {
		status string
		landed bool
		want   Action
	}{
		{"ready", false, Do},
		{"submitted", false, Review},
		{"unplanned", false, Plan},
		{"revisit", false, Plan},
		{"unverified", false, Verify},
		// E-2156: `unreviewed` wore ⁇ in `session status`, the should-never-happen
		// glyph, until the two views shared this rule.
		{"unreviewed", false, Read},
		{"underway", false, Orphan},
		{"confirmed", false, Done},
		{"confirmed", true, Landed},
		{"underway", true, Landed},
		{"no-such-status", false, Unknown},
	}
	for _, c := range cases {
		if got := Classify(c.status, c.landed); got != c.want {
			t.Errorf("Classify(%q, landed=%v) = %s, want %s", c.status, c.landed, got.Label(), c.want.Label())
		}
	}
}

// TestPrefixIsFixedWidth: the slot holds `E-` plus four digits. A five-digit id
// widens its own row by a column, as it always has in `session status`.
func TestPrefixIsFixedWidth(t *testing.T) {
	for _, id := range []int64{1, 42, 2156, 9999} {
		p := Prefix(Verify, "epic", " ", PadID(id), PhaseChar("urgent", false))
		if w := runewidth.StringWidth(p); w != PrefixWidth {
			t.Errorf("prefix %q measures %d, want %d", p, w, PrefixWidth)
		}
	}
}

func TestFitLegend(t *testing.T) {
	entries := []LegendEntry{{Icon: "☑", Label: "verify"}, {Icon: "◷", Label: "orphan"}}
	normal := "p · ☑ verify  ◷ orphan"
	compact := "p · ☑verify ◷orphan"
	if got := FitLegend("p · ", entries, 100); got != normal {
		t.Errorf("wide: %q, want %q", got, normal)
	}
	if got := FitLegend("p · ", entries, runewidth.StringWidth(compact)); got != compact {
		t.Errorf("narrow: %q, want %q", got, compact)
	}
	if got := FitLegend("p · ", entries, 5); got != normal {
		t.Errorf("too narrow for either: %q, want the normal form, wrapping", got)
	}
}
