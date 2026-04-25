package kairos_test

import (
	"sort"
	"testing"
	"time"

	"github.com/mikeschinkel/endless/internal/kairos"
)

func TestString_KnownValue(t *testing.T) {
	ts := kairos.New(
		time.Date(2026, 4, 24, 14, 37, 22, 143456000, time.UTC),
		2,
		0xa7f3,
	)
	s := ts.String()
	if len(s) != 15 {
		t.Fatalf("String() length = %d, want 15", len(s))
	}

	parsed, err := kairos.Parse(s)
	if err != nil {
		t.Fatalf("Parse(%q) error: %v", s, err)
	}
	if !ts.Equal(parsed) {
		t.Errorf("round-trip mismatch:\n  original: %v\n  parsed:   %v", ts, parsed)
	}
}

func TestParse_Errors(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"too short", "01234567890123"},
		{"too long", "0123456789012345"},
		{"invalid char", "01234567890123!"},
		{"empty", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := kairos.Parse(tt.input)
			if err == nil {
				t.Errorf("Parse(%q) = nil error, want error", tt.input)
			}
		})
	}
}

func TestRoundTrip(t *testing.T) {
	tests := []struct {
		name    string
		phys    time.Time
		logical uint8
		nodeID  uint16
	}{
		{"epoch", kairos.Epoch, 0, 0},
		{"epoch+1us", kairos.Epoch.Add(time.Microsecond), 0, 0},
		{"typical", time.Date(2026, 4, 24, 14, 37, 22, 143456000, time.UTC), 5, 0xa7f3},
		{"max logical", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), 127, 0xffff},
		{"max node", time.Date(2030, 6, 15, 12, 0, 0, 0, time.UTC), 0, 0xffff},
		{"zero node", time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC), 0, 0},
		{"far future", time.Date(2100, 12, 31, 23, 59, 59, 999999000, time.UTC), 127, 0xbeef},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := kairos.New(tt.phys, tt.logical, tt.nodeID)
			s := ts.String()
			parsed, err := kairos.Parse(s)
			if err != nil {
				t.Fatalf("Parse(%q) error: %v", s, err)
			}
			if !ts.Equal(parsed) {
				t.Errorf("round-trip failed:\n  original: phys=%v logical=%d node=%04x\n  parsed:   phys=%v logical=%d node=%04x",
					ts.Physical(), ts.Logical(), ts.NodeID(),
					parsed.Physical(), parsed.Logical(), parsed.NodeID())
			}
		})
	}
}

func TestLexicographicOrdering(t *testing.T) {
	base := time.Date(2026, 4, 24, 14, 0, 0, 0, time.UTC)

	// Build timestamps in known causal order
	ordered := []kairos.Timestamp{
		kairos.New(base, 0, 0x0001),
		kairos.New(base, 0, 0x0002),                   // same time+logical, higher node
		kairos.New(base, 1, 0x0001),                   // same time, higher logical
		kairos.New(base.Add(time.Microsecond), 0, 0x0001), // later time
		kairos.New(base.Add(time.Second), 0, 0x0001),      // much later time
		kairos.New(base.Add(time.Hour), 127, 0xffff),      // far later, max logical+node
	}

	// Verify Before() ordering
	for i := 0; i < len(ordered)-1; i++ {
		if !ordered[i].Before(ordered[i+1]) {
			t.Errorf("Before: ordered[%d] should be before ordered[%d]", i, i+1)
		}
	}

	// Serialize all, sort as strings, verify same order
	strings := make([]string, len(ordered))
	for i, ts := range ordered {
		strings[i] = ts.String()
	}

	sorted := make([]string, len(strings))
	copy(sorted, strings)
	sort.Strings(sorted)

	for i := range strings {
		if strings[i] != sorted[i] {
			t.Errorf("lexicographic order mismatch at position %d:\n  Before() order: %s\n  string sort:    %s",
				i, strings[i], sorted[i])
		}
	}
}

func TestBeforeAfterEqual(t *testing.T) {
	a := kairos.New(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), 0, 0x0001)
	b := kairos.New(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), 1, 0x0001)

	if !a.Before(b) {
		t.Error("a should be Before b")
	}
	if !b.After(a) {
		t.Error("b should be After a")
	}
	if a.Equal(b) {
		t.Error("a should not Equal b")
	}
	if !a.Equal(a) {
		t.Error("a should Equal itself")
	}
}

func TestIsZero(t *testing.T) {
	var zero kairos.Timestamp
	if !zero.IsZero() {
		t.Error("zero value should be IsZero")
	}
	nonzero := kairos.New(kairos.Epoch, 0, 0)
	if nonzero.IsZero() {
		t.Error("epoch timestamp should not be IsZero")
	}
}

func TestFormat(t *testing.T) {
	ts := kairos.New(
		time.Date(2026, 4, 24, 14, 37, 22, 0, time.UTC),
		0, 0,
	)
	got := ts.Format("2006-01-02 15:04:05")
	want := "2026-04-24 14:37:22"
	if got != want {
		t.Errorf("Format = %q, want %q", got, want)
	}
}
