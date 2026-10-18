package tmuxstartcmd

import (
	"slices"
	"testing"

	"github.com/mikeschinkel/endless/internal/tmuxservers"
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

// TestOrderSwaps: the task windows are swapped into recorded order among the
// positions task windows hold; a non-task window keeps its place, and a task
// window the record does not name follows the recorded ones (E-2385).
func TestOrderSwaps(t *testing.T) {
	w := func(id string, task int64) tmuxservers.Window { return tmuxservers.Window{ID: id, TaskID: task} }
	apply := func(wins []tmuxservers.Window, swaps [][2]string) []string {
		ids := make([]string, len(wins))
		for i, x := range wins {
			ids[i] = x.ID
		}
		for _, sw := range swaps {
			a, b := slices.Index(ids, sw[0]), slices.Index(ids, sw[1])
			ids[a], ids[b] = ids[b], ids[a]
		}
		return ids
	}
	for _, tt := range []struct {
		name     string
		wins     []tmuxservers.Window
		recorded []int64
		want     []string
	}{
		{"recreated windows land at the end", []tmuxservers.Window{w("@7", 7), w("@5", 5), w("@9", 9)},
			[]int64{5, 7, 9}, []string{"@5", "@7", "@9"}},
		{"a shell keeps its place", []tmuxservers.Window{w("@7", 7), w("@0", 0), w("@5", 5), w("@9", 9)},
			[]int64{9, 5, 7}, []string{"@9", "@0", "@5", "@7"}},
		{"an unrecorded task window follows", []tmuxservers.Window{w("@3", 3), w("@5", 5), w("@7", 7)},
			[]int64{7, 5}, []string{"@7", "@5", "@3"}},
		{"a second window for a task is unrecorded", []tmuxservers.Window{w("@5b", 5), w("@7", 7), w("@5", 5)},
			[]int64{7, 5}, []string{"@7", "@5b", "@5"}},
		{"already in order", []tmuxservers.Window{w("@5", 5), w("@7", 7)},
			[]int64{5, 7}, []string{"@5", "@7"}},
	} {
		swaps := orderSwaps(tt.wins, tt.recorded)
		if got := apply(tt.wins, swaps); !slices.Equal(got, tt.want) {
			t.Errorf("%s: order after %v = %v, want %v", tt.name, swaps, got, tt.want)
		}
	}
	if swaps := orderSwaps([]tmuxservers.Window{w("@5", 5), w("@7", 7)}, []int64{5, 7}); len(swaps) != 0 {
		t.Errorf("windows already in order were swapped: %v", swaps)
	}
}
