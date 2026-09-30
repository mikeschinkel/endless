package jobs

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

// TestNote_RecordedAndReplacedEachRun: a job's note lands in jobs.last_note and
// in the Outcome, and the next run's note — or its silence — replaces it, so
// the column never describes an older run than the last one.
func TestNote_RecordedAndReplacedEachRun(t *testing.T) {
	db := newTestDB(t)
	note := "nothing eligible"
	job := &fakeJob{
		name:     "noter",
		schedule: Schedule{Interval: time.Hour},
		fn: func(ctx context.Context) error {
			Note(ctx, note)
			return nil
		},
	}
	register(t, job)

	readNote := func() sql.NullString {
		t.Helper()
		var got sql.NullString
		if err := db.QueryRow(`SELECT last_note FROM jobs WHERE name = 'noter'`).Scan(&got); err != nil {
			t.Fatalf("read last_note: %v", err)
		}
		return got
	}

	outcome, err := RunNamed(context.Background(), "noter")
	if err != nil {
		t.Fatalf("RunNamed: %v", err)
	}
	if outcome.Note != "nothing eligible" {
		t.Errorf("Outcome.Note = %q, want %q", outcome.Note, "nothing eligible")
	}
	if got := readNote(); got.String != "nothing eligible" {
		t.Errorf("last_note = %q, want %q", got.String, "nothing eligible")
	}

	note = ""
	if _, err = RunNamed(context.Background(), "noter"); err != nil {
		t.Fatalf("RunNamed: %v", err)
	}
	if got := readNote(); got.Valid {
		t.Errorf("last_note = %q after a run that recorded none, want NULL", got.String)
	}

	statuses, err := Statuses()
	if err != nil {
		t.Fatalf("Statuses: %v", err)
	}
	for _, s := range statuses {
		if s.Name == "noter" && s.LastNote != "" {
			t.Errorf("Status.LastNote = %q, want empty", s.LastNote)
		}
	}
}

// TestNote_OutsideARunIsANoOp lets a job's logic be driven directly in a test.
func TestNote_OutsideARunIsANoOp(t *testing.T) {
	Note(context.Background(), "ignored") // must not panic
}
