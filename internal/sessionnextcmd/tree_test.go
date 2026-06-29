package sessionnextcmd

import (
	"strings"
	"testing"
)

// renderForestString builds and renders the forest for a synthetic candidate
// set, so the layering + rendering can be asserted without a DB.
func renderForestString(ids []int64, edges map[int64][]int64, doOrder map[int64]int64) string {
	var b strings.Builder
	renderForest(&b, buildForest(ids, edges, doOrder))
	return b.String()
}

func TestBuildForest(t *testing.T) {
	tests := []struct {
		name    string
		ids     []int64
		edges   map[int64][]int64 // target → blockers (source blocks target)
		doOrder map[int64]int64
		want    string
	}{
		{
			name:  "chain plus independent root",
			ids:   []int64{100, 101, 102, 103},
			edges: map[int64][]int64{101: {100}, 102: {101}},
			want: "" +
				"E-100\n" +
				"└── E-101\n" +
				"    └── E-102\n" +
				"E-103\n",
		},
		{
			name:  "parallel siblings at equal depth",
			ids:   []int64{100, 101, 102, 103},
			edges: map[int64][]int64{101: {100}, 102: {100}, 103: {101}},
			want: "" +
				"E-100\n" +
				"├── E-101\n" +
				"│   └── E-103\n" +
				"└── E-102\n",
		},
		{
			name:  "diamond attaches to lowest-id max-depth blocker",
			ids:   []int64{100, 101, 102, 103},
			edges: map[int64][]int64{101: {100}, 102: {100}, 103: {101, 102}},
			want: "" +
				"E-100\n" +
				"├── E-101\n" +
				"│   └── E-103\n" +
				"└── E-102\n",
		},
		{
			name:    "do_order overrides DAG-derived order",
			ids:     []int64{100, 101, 102, 103},
			edges:   nil,
			doOrder: map[int64]int64{100: 1, 101: 2, 102: 2, 103: 3},
			want: "" +
				"E-100\n" +
				"├── E-101\n" +
				"│   └── E-103\n" +
				"└── E-102\n",
		},
		{
			name: "all independent render as flush-left roots",
			ids:  []int64{100, 101},
			want: "" +
				"E-100\n" +
				"E-101\n",
		},
		{
			name:    "unordered tasks are independent roots under override",
			ids:     []int64{100, 101, 102},
			doOrder: map[int64]int64{100: 1, 101: 2},
			want: "" +
				"E-100\n" +
				"└── E-101\n" +
				"E-102\n",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := renderForestString(tc.ids, tc.edges, tc.doOrder)
			if got != tc.want {
				t.Errorf("forest mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, tc.want)
			}
		})
	}
}
