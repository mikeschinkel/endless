package tmuxstartcmd

import (
	"slices"
	"testing"
)

// TestMissingIDs: restore recreates exactly the recorded windows that are not
// open, once each, in recorded order.
func TestMissingIDs(t *testing.T) {
	got := missingIDs([]int64{5, 7, 9, 7}, []int64{7})
	if !slices.Equal(got, []int64{5, 9}) {
		t.Errorf("missingIDs = %v, want [5 9]", got)
	}
	if got = missingIDs(nil, []int64{1}); len(got) != 0 {
		t.Errorf("nothing recorded, yet %v is missing", got)
	}
}
