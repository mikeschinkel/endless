package events

import (
	"encoding/json"
	"testing"
	"time"
)

func shippedWrittenEvent(t *testing.T, path, hash string, at time.Time) *Event {
	t.Helper()
	payload, err := json.Marshal(ShippedFileWrittenPayload{Kind: "tasks-rules", SHA256: hash})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return &Event{
		V:       1,
		TS:      kairosTS(at),
		Kind:    KindShippedFileWritten,
		Project: "test",
		Entity:  EntityRef{Type: EntityShippedFile, ID: path},
		Actor:   Actor{Kind: ActorSystem, ID: "user@host"},
		Payload: payload,
	}
}

func TestShippedFileWritten_LatestWriteIsTheRecord(t *testing.T) {
	db := newLandingTestDB(t)
	const path = ".endless/tasks/CLAUDE.md"

	if got, err := RecordedShippedFile(db, "test", path); err != nil || got != "" {
		t.Fatalf("before any write: got %q, %v; want no record", got, err)
	}
	first := shippedWrittenEvent(t, path, "aaa", time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC))
	if err := first.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if _, err := execShippedFileWritten(db, first); err != nil {
		t.Fatalf("exec first: %v", err)
	}
	second := shippedWrittenEvent(t, path, "bbb", time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC))
	if err := replayShippedFileWritten(db, second, &ProjectResult{}); err != nil {
		t.Fatalf("replay second: %v", err)
	}

	got, err := RecordedShippedFile(db, "test", path)
	if err != nil || got != "bbb" {
		t.Fatalf("got %q, %v; want the latest write, bbb", got, err)
	}
	var rows int
	db.QueryRow(`SELECT COUNT(*) FROM shipped_file_writes`).Scan(&rows)
	if rows != 1 {
		t.Errorf("shipped_file_writes holds %d rows, want 1 per path", rows)
	}
	if got, _ := RecordedShippedFile(db, "other", path); got != "" {
		t.Errorf("another project sees this project's record: %q", got)
	}
}

func TestShippedFileWritten_RefusesAnIncompleteRecord(t *testing.T) {
	db := newLandingTestDB(t)
	evt := shippedWrittenEvent(t, ".endless/tasks/CLAUDE.md", "", time.Now())
	if _, err := execShippedFileWritten(db, evt); err == nil {
		t.Fatal("a record with no hash was accepted")
	}
}
