package jobs

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// E-1950: losing a lock race is what a database with concurrent writers does,
// not a fault. The runner used to record a user-visible ERR-0004 warning on the
// FIRST contended scheduling write — which then pinned itself to the
// session-status badge forever, describing a condition that had already healed.

func TestIsBusy_RecognizesSQLiteContention(t *testing.T) {
	busy := []string{
		"jobs; database; query; database is locked (5) (SQLITE_BUSY)",
		"database table is locked",
		"SQLITE_BUSY",
	}
	for _, text := range busy {
		if !isBusy(errors.New(text)) {
			t.Errorf("isBusy(%q) = false, want true", text)
		}
	}

	notBusy := []string{
		"no such table: jobs",
		"disk I/O error",
		"UNIQUE constraint failed: jobs.name",
	}
	for _, text := range notBusy {
		if isBusy(errors.New(text)) {
			t.Errorf("isBusy(%q) = true, want false", text)
		}
	}
}

func TestEnsureRowWithRetry_SucceedsOnTheFirstUncontendedAttempt(t *testing.T) {
	db := newTestDB(t)

	if err := ensureRowWithRetry(db, "triage-sufficiency"); err != nil {
		t.Fatalf("ensureRowWithRetry: %v", err)
	}

	var count int
	if err := db.QueryRow("SELECT count(*) FROM jobs WHERE name = ?", "triage-sufficiency").Scan(&count); err != nil {
		t.Fatalf("count jobs row: %v", err)
	}
	if count != 1 {
		t.Errorf("jobs rows = %d, want 1", count)
	}
}

func TestEnsureRowWithRetry_IsIdempotent(t *testing.T) {
	db := newTestDB(t)

	for i := 0; i < 3; i++ {
		if err := ensureRowWithRetry(db, "triage-sufficiency"); err != nil {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}

	var count int
	if err := db.QueryRow("SELECT count(*) FROM jobs WHERE name = ?", "triage-sufficiency").Scan(&count); err != nil {
		t.Fatalf("count jobs row: %v", err)
	}
	if count != 1 {
		t.Errorf("jobs rows = %d, want 1 after three calls", count)
	}
}

func TestEnsureRowWithRetry_GivesUpPromptlyOnANonBusyError(t *testing.T) {
	db := newTestDB(t)

	if _, err := db.Exec("DROP TABLE jobs"); err != nil {
		t.Fatalf("drop jobs table: %v", err)
	}

	// A missing table is not contention — retrying it would only delay the fault
	// the user actually needs to see.
	start := time.Now()
	err := ensureRowWithRetry(db, "triage-sufficiency")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("ensureRowWithRetry succeeded against a dropped table")
	}
	if !strings.Contains(err.Error(), "no such table") {
		t.Errorf("unexpected error: %v", err)
	}
	if elapsed >= ensureRowBusyBackoff {
		t.Errorf("elapsed = %v, want under one backoff (%v) — it retried a non-busy error",
			elapsed, ensureRowBusyBackoff)
	}
}
