package monitor

import (
	"testing"
)

// TestResumeTarget_RecoveryFields covers the E-1801 recovery inputs the Python
// `session resume --review`/`--reopen` resolver reads off resume-target: the
// task type slug, current status, title, and the latest landing sha.
func TestResumeTarget_RecoveryFields(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "p1", "/p1")
	seedTask(t, db, 10, 1, "Recover me", "confirmed")
	// type_id 1 = todo (seeded by schema.sql).
	if _, err := db.Exec("UPDATE tasks SET type_id = 1 WHERE id = 10"); err != nil {
		t.Fatalf("set type: %v", err)
	}
	insertResumeSession(t, db, 1, ptrInt64(10), ptrStr("uuid-recover-1"), "2026-06-20T00:00:00")

	// Two landings; .landed must resolve to the most recent.
	if _, err := db.Exec(
		`INSERT INTO task_landings (task_id, merge_commit_sha, landed_at)
		 VALUES (10, 'sha-old', '2026-06-21T00:00:00'), (10, 'sha-new', '2026-06-22T00:00:00')`,
	); err != nil {
		t.Fatalf("seed landings: %v", err)
	}

	got, err := ResolveResumeTarget("E-10")
	if err != nil {
		t.Fatalf("ResolveResumeTarget: %v", err)
	}
	if got.TaskType != "todo" {
		t.Errorf("TaskType = %q, want todo", got.TaskType)
	}
	if got.TaskStatus != "confirmed" {
		t.Errorf("TaskStatus = %q, want confirmed", got.TaskStatus)
	}
	if got.TaskTitle != "Recover me" {
		t.Errorf("TaskTitle = %q, want 'Recover me'", got.TaskTitle)
	}
	if got.LandedSHA != "sha-new" {
		t.Errorf("LandedSHA = %q, want sha-new (latest landing)", got.LandedSHA)
	}
}

// TestResumeTarget_EpicTypeSurfaced confirms an epic's type reaches the
// resolver so it can refuse `--review`/`--reopen` with a child-redirect.
func TestResumeTarget_EpicTypeSurfaced(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "p1", "/p1")
	seedTask(t, db, 20, 1, "container", "underway")
	// type_id 4 = epic.
	if _, err := db.Exec("UPDATE tasks SET type_id = 4 WHERE id = 20"); err != nil {
		t.Fatalf("set type: %v", err)
	}
	insertResumeSession(t, db, 1, ptrInt64(20), ptrStr("uuid-epic-1"), "2026-06-20T00:00:00")

	got, err := ResolveResumeTarget("E-20")
	if err != nil {
		t.Fatalf("ResolveResumeTarget: %v", err)
	}
	if got.TaskType != "epic" {
		t.Errorf("TaskType = %q, want epic", got.TaskType)
	}
	if got.LandedSHA != "" {
		t.Errorf("LandedSHA = %q, want empty (never landed)", got.LandedSHA)
	}
}
