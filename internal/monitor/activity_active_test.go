package monitor

import (
	"database/sql"
	"testing"
	"time"
)

// E-1950: ActiveSecondsSince is the clock the session-status badge ages stale
// warnings against. It has to measure time the USER was present for — an hour
// that passes overnight has shown them nothing, so an age-off keyed to
// wall-clock would fire without ever having been seen.

// seedActivity writes activity rows at the given offsets from base.
func seedActivity(t *testing.T, db *sql.DB, projectID int64, base time.Time, offsets ...time.Duration) {
	t.Helper()
	for _, offset := range offsets {
		stamp := base.Add(offset).UTC().Format("2006-01-02T15:04:05")
		if _, err := db.Exec(
			"INSERT INTO activity (project_id, source, working_dir, created_at) VALUES (?, ?, ?, ?)",
			projectID, "claude", "/tmp", stamp,
		); err != nil {
			t.Fatalf("seed activity at %s: %v", stamp, err)
		}
	}
}

func TestActiveSecondsSince_SumsOnlyNonIdleGaps(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "endless", "/tmp/endless")

	// Two working stretches an hour apart. Wall-clock between the first and last
	// row is ~1h10m; active time is the 5m + 5m the user was actually at the
	// keyboard, because the hour of silence in the middle does not count.
	base := time.Now().UTC().Add(-4 * time.Hour)
	seedActivity(t, db, 1, base,
		0, 1*time.Minute, 3*time.Minute, 5*time.Minute,
		65*time.Minute, 68*time.Minute, 70*time.Minute,
	)

	active, err := ActiveSecondsSince(base.Add(-time.Minute))
	if err != nil {
		t.Fatalf("ActiveSecondsSince: %v", err)
	}

	if active != 10*time.Minute {
		t.Errorf("active = %v, want 10m (the idle hour must not count)", active)
	}
}

func TestActiveSecondsSince_IgnoresRowsBeforeTheCutoff(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "endless", "/tmp/endless")

	base := time.Now().UTC().Add(-4 * time.Hour)
	seedActivity(t, db, 1, base, 0, 1*time.Minute, 2*time.Minute)
	seedActivity(t, db, 1, base, 120*time.Minute, 121*time.Minute)

	// Asking from partway through only counts the later stretch.
	active, err := ActiveSecondsSince(base.Add(60 * time.Minute))
	if err != nil {
		t.Fatalf("ActiveSecondsSince: %v", err)
	}

	if active != time.Minute {
		t.Errorf("active = %v, want 1m (rows before the cutoff must not count)", active)
	}
}

func TestActiveSecondsSince_IsZeroWhenTheMachineWasUnattended(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "endless", "/tmp/endless")

	// This is the case the whole mechanism exists for: a fault fires, the user
	// walks away for a day, and comes back. No active time has passed, so the
	// warning is still waiting for them rather than having expired unseen.
	base := time.Now().UTC().Add(-24 * time.Hour)
	seedActivity(t, db, 1, base)

	active, err := ActiveSecondsSince(base.Add(-time.Minute))
	if err != nil {
		t.Fatalf("ActiveSecondsSince: %v", err)
	}

	if active != 0 {
		t.Errorf("active = %v, want 0 for a single row with no neighbours", active)
	}
}

func TestActiveSecondsSince_CountsTheOpenSegmentUpToNow(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "endless", "/tmp/endless")

	// A session working right now: the stretch since the newest throttled write
	// has to accrue, or a continuously-active user's most recent minutes never do.
	now := time.Now().UTC()
	seedActivity(t, db, 1, now, -2*time.Minute, -1*time.Minute)

	active, err := ActiveSecondsSince(now.Add(-10 * time.Minute))
	if err != nil {
		t.Fatalf("ActiveSecondsSince: %v", err)
	}

	if active < time.Minute {
		t.Errorf("active = %v, want at least 1m (open segment was not counted)", active)
	}
}

func TestActiveSecondsSince_CrossesProjects(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "endless", "/tmp/endless")
	seedProject(t, db, 2, "other", "/tmp/other")

	// "Was the user at the computer" is not a per-repo question. Alternating
	// between two projects is one continuous working stretch.
	base := time.Now().UTC().Add(-4 * time.Hour)
	seedActivity(t, db, 1, base, 0, 2*time.Minute)
	seedActivity(t, db, 2, base, 1*time.Minute, 3*time.Minute)

	active, err := ActiveSecondsSince(base.Add(-time.Minute))
	if err != nil {
		t.Fatalf("ActiveSecondsSince: %v", err)
	}

	if active != 3*time.Minute {
		t.Errorf("active = %v, want 3m across both projects", active)
	}
}
