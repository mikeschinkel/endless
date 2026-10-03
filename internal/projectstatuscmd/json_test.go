package projectstatuscmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mikeschinkel/endless/internal/monitor"
)

func decodeDoc(t *testing.T, rows []monitor.ProjectStatusRow) jsonDoc {
	t.Helper()
	var b strings.Builder
	if err := renderJSON(&b, "demo", rows, sortUpdated); err != nil {
		t.Fatalf("renderJSON: %v", err)
	}
	var out jsonDoc
	if err := json.Unmarshal([]byte(b.String()), &out); err != nil {
		t.Fatalf("payload does not parse: %v\n%s", err, b.String())
	}
	return out
}

// TestJSONIsUncappedAndInRenderOrder: every row, in the frame's list order,
// each naming its list and its action.
func TestJSONIsUncappedAndInRenderOrder(t *testing.T) {
	doc := decodeDoc(t, append(mixed(), overflow()...))
	if len(doc.Rows) != len(mixed())+len(overflow()) {
		t.Fatalf("rows = %d, want every row", len(doc.Rows))
	}
	seen := -1
	for _, r := range doc.Rows {
		l := -1
		for i, n := range listNames {
			if n == r.List {
				l = i
			}
		}
		if l < seen {
			t.Fatalf("row E-%d in list %q came after a later list", r.TaskID, r.List)
		}
		seen = l
	}
	if doc.Rows[0].List != "urgent" || doc.Rows[0].Action == "" {
		t.Errorf("first row = %+v", doc.Rows[0])
	}
}

func TestJSONEmptyDocStillParses(t *testing.T) {
	doc := decodeDoc(t, nil)
	if doc.Project != "demo" || doc.Rows == nil || len(doc.Rows) != 0 {
		t.Errorf("empty doc = %+v", doc)
	}
}
