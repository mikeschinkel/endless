// Tests for the voiding entry's readers and builders (E-2578): the reader
// locating every line by segment and offset, the projector skipping the lines
// a ledger.voided entry names, and the hand-written voiding's checks.
package events_test

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mikeschinkel/endless/internal/events"
	"github.com/mikeschinkel/endless/internal/kairos"
)

// writeSegment writes raw bytes as one segment under root's ledger directory.
func writeSegment(t *testing.T, root, name string, body string) {
	t.Helper()
	dir := filepath.Join(root, ".endless", events.LedgerDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

const (
	lineA = `{"v":1,"ts":"5WYM00000001","kind":"task.created","project":"x","entity":{"type":"task","id":"1"},"actor":{"kind":"cli","id":"t"},"payload":{"title":"a","phase":"now","status":"ready","type":"todo"}}`
	lineB = `{"v":1,"ts":"5WYM00000002","kind":"task.status_changed","project":"x","entity":{"type":"task","id":"1"},"actor":{"kind":"cli","id":"t"},"payload":{"old_status":"ready","new_status":"underway"}}`
)

// ReadAllEvents stamps every event with its segment and the byte offset its
// line starts at, counting every byte of the file — a blank line included.
func TestReadAllEvents_SegmentAndOffset(t *testing.T) {
	root := t.TempDir()
	body := lineA + "\n" + "\n" + lineB + "\n"
	writeSegment(t, root, "db-entries-aaaa-000001.jsonl", body)

	got, err := events.ReadAllEvents(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("read %d events, want 2", len(got))
	}
	wantB := int64(len(lineA) + 2)
	if got[0].Segment != "db-entries-aaaa-000001.jsonl" || got[0].Offset != 0 {
		t.Errorf("first at %s:%d, want db-entries-aaaa-000001.jsonl:0", got[0].Segment, got[0].Offset)
	}
	if got[1].Offset != wantB {
		t.Errorf("second at offset %d, want %d (the blank line counts)", got[1].Offset, wantB)
	}
	// The offsets are the ones ReadLinesAt accepts.
	lines, err := events.ReadLinesAt(root, "db-entries-aaaa-000001.jsonl", []int64{wantB, 0, wantB})
	if err != nil {
		t.Fatalf("ReadLinesAt: %v", err)
	}
	if len(lines) != 2 || lines[0].Offset != 0 || lines[1].Offset != wantB {
		t.Errorf("ReadLinesAt = %+v; want the two lines, sorted, the duplicate dropped", lines)
	}
	if lines[1].Event.Kind != events.KindTaskStatusChanged || lines[1].Event.Segment != "db-entries-aaaa-000001.jsonl" {
		t.Errorf("second line = %+v", lines[1].Event)
	}
}

// A wrong offset is refused rather than voiding nothing while claiming to.
func TestReadLinesAt_Refusals(t *testing.T) {
	root := t.TempDir()
	writeSegment(t, root, "db-entries-aaaa-000001.jsonl", lineA+"\n"+lineB+"\n")
	writeSegment(t, root, "db-entries-aaaa-000002.jsonl", "not json\n")

	cases := []struct {
		name    string
		segment string
		offsets []int64
		want    string
	}{
		{"mid-line", "db-entries-aaaa-000001.jsonl", []int64{5}, "does not start a line"},
		{"past the end", "db-entries-aaaa-000001.jsonl", []int64{int64(len(lineA) + len(lineB) + 2)}, "does not start a line"},
		{"not an event", "db-entries-aaaa-000002.jsonl", []int64{0}, "is not an event"},
		{"no such segment", "db-entries-aaaa-000009.jsonl", []int64{0}, "no segment"},
		{"a path", "sub/db-entries-aaaa-000001.jsonl", []int64{0}, "is a path"},
		{"no offsets", "db-entries-aaaa-000001.jsonl", nil, "no offsets"},
	}
	for _, tc := range cases {
		_, err := events.ReadLinesAt(root, tc.segment, tc.offsets)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", tc.name, err, tc.want)
		}
	}
}

// A hand-written voiding names the lines' segment and offsets, takes the first
// line's entity, and refuses to void a voiding.
func TestNewVoiding(t *testing.T) {
	root := t.TempDir()
	void := `{"v":1,"ts":"5WYM00000003","kind":"ledger.voided","project":"x","entity":{"type":"task","id":"1"},"actor":{"kind":"cli","id":"t"},"payload":{"segment":"db-entries-aaaa-000001.jsonl","refs":[0]}}`
	writeSegment(t, root, "db-entries-aaaa-000001.jsonl", lineA+"\n"+lineB+"\n"+void+"\n")
	clock := kairos.NewClock(0xaaaa)
	actor := events.Actor{Kind: events.ActorCLI, ID: "mike"}

	lines, err := events.ReadLinesAt(root, "db-entries-aaaa-000001.jsonl", []int64{int64(len(lineA) + 1), 0})
	if err != nil {
		t.Fatal(err)
	}
	evt, err := events.NewVoiding(clock, "x", actor, lines, "never applied")
	if err != nil {
		t.Fatalf("NewVoiding: %v", err)
	}
	if evt.Kind != events.KindLedgerVoided || evt.Entity != (events.EntityRef{Type: events.EntityTask, ID: "1"}) || evt.Actor != actor {
		t.Errorf("voiding = %+v", evt)
	}
	var p events.VoidedPayload
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		t.Fatal(err)
	}
	if p.Segment != "db-entries-aaaa-000001.jsonl" || len(p.Refs) != 2 || p.Refs[0] != 0 || p.Reason != "never applied" || p.Error != "" {
		t.Errorf("payload = %+v", p)
	}

	voidLine, err := events.ReadLinesAt(root, "db-entries-aaaa-000001.jsonl", []int64{int64(len(lineA) + len(lineB) + 2)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := events.NewVoiding(clock, "x", actor, voidLine, "x"); err == nil || !strings.Contains(err.Error(), "itself a voiding") {
		t.Errorf("voiding a voiding: err = %v", err)
	}
}

// The projector reads every ledger.voided entry before replaying and skips the
// lines they name, wherever the voiding sits, counting them in the result.
func TestProjectToTempDB_SkipsVoidedLines(t *testing.T) {
	root := t.TempDir()
	w, err := events.NewWriter(root, "aaaa")
	if err != nil {
		t.Fatal(err)
	}
	// Three lines, then a voiding of the second in a LATER segment.
	appendTaskCreated(t, w, "x", "1", "5WYM00000001", "a")
	changed := []byte(lineB)
	changedOffset, err := w.AppendOffset(changed)
	if err != nil {
		t.Fatal(err)
	}
	appendTaskCreated(t, w, "x", "2", "5WYM00000004", "b")
	first := w.CurrentSegment()
	w.SetMaxCount(1) // the next append rotates
	voidPayload, _ := json.Marshal(events.VoidedPayload{Segment: first, Refs: []int64{changedOffset}, Reason: "test"})
	appendEvent(t, w, events.Event{
		V: events.Version, TS: "5WYM00000005", Kind: events.KindLedgerVoided, Project: "x",
		Entity:  events.EntityRef{Type: events.EntityTask, ID: "1"},
		Actor:   events.Actor{Kind: events.ActorCLI, ID: "t"},
		Payload: voidPayload,
	})
	if w.CurrentSegment() == first {
		t.Fatalf("the voiding did not land in a later segment; the test means to cover that")
	}

	tempPath, res, err := events.ProjectToTempDB(root)
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	defer os.Remove(tempPath)
	if len(res.Errors) != 0 {
		t.Errorf("projection errors: %v", res.Errors)
	}
	if res.EventsSkipped != 1 || res.EventsReplayed != 3 {
		t.Errorf("replayed %d skipped %d; want 3 and 1", res.EventsReplayed, res.EventsSkipped)
	}
	db, err := sql.Open("sqlite", tempPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var status string
	if err := db.QueryRow("SELECT status FROM tasks WHERE id = 1").Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "unplanned" {
		t.Errorf("task 1 status = %q; the voided status change was replayed", status)
	}
	var n int
	if err := db.QueryRow("SELECT count(*) FROM tasks").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("tasks = %d, want 2: the lines around the voided one replay", n)
	}
}
