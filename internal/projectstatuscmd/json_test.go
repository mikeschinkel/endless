package projectstatuscmd

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mikeschinkel/endless/internal/monitor"
)

func decodeDoc(t *testing.T, rows []monitor.ProjectStatusRow) jsonDoc {
	t.Helper()
	var b strings.Builder
	if err := renderJSON(&b, "demo", rows, now); err != nil {
		t.Fatalf("renderJSON: %v", err)
	}
	var out jsonDoc
	if err := json.Unmarshal([]byte(b.String()), &out); err != nil {
		t.Fatalf("payload does not parse: %v\n%s", err, b.String())
	}
	return out
}

// TestJSONIsUncapped pins rowcap.py's rule for machine formats: a consumer
// parsing a silently truncated payload has no footer to read and no way to
// notice, so --json emits every row the query returned.
func TestJSONIsUncapped(t *testing.T) {
	var rows []monitor.ProjectStatusRow
	for i := int64(1); i <= 40; i++ {
		rows = append(rows, taskRow(i, "unverified", time.Duration(i)*time.Hour))
	}
	if got := decodeDoc(t, rows); len(got.Rows) != 40 {
		t.Fatalf("--json emitted %d of 40 rows", len(got.Rows))
	}
}

// TestJSONKeepsRankOrder: the order is information the view computed, and
// dropping it would make the payload strictly less useful than the render.
func TestJSONKeepsRankOrder(t *testing.T) {
	got := decodeDoc(t, []monitor.ProjectStatusRow{
		sessionRow(10, "working", time.Minute, 0),
		taskRow(1, "unverified", time.Hour),
		sessionRow(11, "idle", time.Minute, 0),
	})
	var actions []string
	for _, r := range got.Rows {
		actions = append(actions, r.Action)
	}
	if strings.Join(actions, ",") != "verify,idle,doing" {
		t.Fatalf("rows = %v, want rank order verify,idle,doing", actions)
	}
}

// TestJSONCarriesTheActionAsALabel: not the glyph (which would force every
// consumer to learn the icon vocabulary) and not the enum (whose numeric value
// reorders the moment a rank is inserted).
func TestJSONCarriesTheActionAsALabel(t *testing.T) {
	got := decodeDoc(t, []monitor.ProjectStatusRow{taskRow(1, "unverified", time.Hour)})
	if got.Rows[0].Action != "verify" {
		t.Errorf("action = %q, want the label 'verify'", got.Rows[0].Action)
	}
	if got.Project != "demo" {
		t.Errorf("payload does not name its project: %q", got.Project)
	}
}

// TestJSONAgeIsComputedForTheConsumer: WHICH timestamp a row ages by is a render
// rule — session activity for a session row, task update for a task row — and a
// consumer re-deriving it would have to reimplement that rule to agree with the
// view.
func TestJSONAgeIsComputedForTheConsumer(t *testing.T) {
	got := decodeDoc(t, []monitor.ProjectStatusRow{
		taskRow(1, "unverified", 2*time.Hour),
		{SessionID: 9, SessionState: "idle", SessionActivity: "unparseable"},
	})
	if got.Rows[0].AgeSeconds == nil || *got.Rows[0].AgeSeconds != 7200 {
		t.Errorf("task age = %v, want 7200", got.Rows[0].AgeSeconds)
	}
	// An unreadable clock is null, not a fabricated number — the one honest
	// answer for "we could not tell".
	if got.Rows[1].AgeSeconds != nil {
		t.Errorf("an unreadable timestamp produced an age of %v, want null", *got.Rows[1].AgeSeconds)
	}
}

// TestJSONEmptyDocStillParses: an empty result must be `[]`, never `null` — a
// consumer looping over the payload should not have to special-case nothing.
func TestJSONEmptyDocStillParses(t *testing.T) {
	var b strings.Builder
	if err := renderJSON(&b, "demo", nil, now); err != nil {
		t.Fatalf("renderJSON: %v", err)
	}
	if !strings.Contains(b.String(), `"rows": []`) {
		t.Fatalf("empty doc did not emit an empty array:\n%s", b.String())
	}
}
