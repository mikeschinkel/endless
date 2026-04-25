package kairos_test

import (
	"testing"

	"github.com/mikeschinkel/endless/internal/kairos"
)

func TestGenerateNodeID(t *testing.T) {
	id := kairos.GenerateNodeID()
	hex := kairos.FormatNodeID(id)
	if len(hex) != 4 {
		t.Errorf("FormatNodeID length = %d, want 4", len(hex))
	}
	for _, c := range hex {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Errorf("FormatNodeID contains non-hex char: %q", c)
		}
	}
}

func TestGenerateNodeID_Unique(t *testing.T) {
	seen := make(map[uint16]bool)
	for range 100 {
		id := kairos.GenerateNodeID()
		seen[id] = true
	}
	// With 65536 possible values and 100 samples, collisions are possible but
	// getting fewer than 90 unique values would indicate broken randomness.
	if len(seen) < 90 {
		t.Errorf("only %d unique IDs from 100 generations, expected near 100", len(seen))
	}
}

func TestFormatNodeID(t *testing.T) {
	tests := []struct {
		id   uint16
		want string
	}{
		{0x0000, "0000"},
		{0x00ff, "00ff"},
		{0xa7f3, "a7f3"},
		{0xffff, "ffff"},
		{0xABCD, "abcd"},
	}
	for _, tt := range tests {
		got := kairos.FormatNodeID(tt.id)
		if got != tt.want {
			t.Errorf("FormatNodeID(%#04x) = %q, want %q", tt.id, got, tt.want)
		}
	}
}

func TestParseNodeID(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    uint16
		wantErr bool
	}{
		{"lowercase", "a7f3", 0xa7f3, false},
		{"uppercase", "A7F3", 0xa7f3, false},
		{"zeros", "0000", 0x0000, false},
		{"max", "ffff", 0xffff, false},
		{"too short", "a7f", 0, true},
		{"too long", "a7f30", 0, true},
		{"non-hex", "a7g3", 0, true},
		{"empty", "", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := kairos.ParseNodeID(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Errorf("ParseNodeID(%q) = %#04x, want error", tt.input, got)
				}
				return
			}
			if err != nil {
				t.Errorf("ParseNodeID(%q) error: %v", tt.input, err)
				return
			}
			if got != tt.want {
				t.Errorf("ParseNodeID(%q) = %#04x, want %#04x", tt.input, got, tt.want)
			}
		})
	}
}
