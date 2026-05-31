package monitor

import (
	"testing"
)

// TestGetTaskTitle_ReturnsTitle pins the happy path: a tasks row's title is
// returned verbatim. GetTaskTitle is the read-side used by the status renderer
// and the suggestion logger; both rely on the literal column value.
func TestGetTaskTitle_ReturnsTitle(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "proj-test-1", "/tmp/proj-test-1")
	want := "Implement the widget"
	if _, err := db.Exec(
		"INSERT INTO tasks (id, project_id, title, status) VALUES (?, ?, ?, ?)",
		77, 1, want, "ready",
	); err != nil {
		t.Fatalf("seed task: %v", err)
	}

	got, err := GetTaskTitle(77)
	if err != nil {
		t.Fatalf("GetTaskTitle: %v", err)
	}
	if got != want {
		t.Errorf("GetTaskTitle = %q, want %q", got, want)
	}
}

// TestGetTaskTitle_MissingRowReturnsEmpty pins the sql.ErrNoRows branch:
// an unknown task id returns "", nil so callers can render an empty
// placeholder without special-casing absence.
func TestGetTaskTitle_MissingRowReturnsEmpty(t *testing.T) {
	withTestDB(t)
	got, err := GetTaskTitle(424242)
	if err != nil {
		t.Fatalf("GetTaskTitle: %v", err)
	}
	if got != "" {
		t.Errorf("GetTaskTitle on missing row = %q, want \"\"", got)
	}
}

// TestRegisterTaskFile_InsertsRow pins the INSERT path: a fresh (task,
// file) pair lands a row in task_files with the supplied session id.
func TestRegisterTaskFile_InsertsRow(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "proj-test-1", "/tmp/proj-test-1")
	if _, err := db.Exec(
		"INSERT INTO tasks (id, project_id, title, status) VALUES (?, ?, ?, ?)",
		88, 1, "t", "ready",
	); err != nil {
		t.Fatalf("seed task: %v", err)
	}

	if err := RegisterTaskFile(88, "sess-X", "/tmp/proj-test-1/foo.go"); err != nil {
		t.Fatalf("RegisterTaskFile: %v", err)
	}

	var sessionID string
	err := db.QueryRow(
		"SELECT first_edited_session_id FROM task_files WHERE task_id=? AND file_path=?",
		88, "/tmp/proj-test-1/foo.go",
	).Scan(&sessionID)
	if err != nil {
		t.Fatalf("read row: %v", err)
	}
	if sessionID != "sess-X" {
		t.Errorf("first_edited_session_id = %q, want sess-X", sessionID)
	}
}

// TestRegisterTaskFile_Idempotent pins INSERT OR IGNORE: a second call with
// the same (task_id, file_path) is a no-op — original first_edited_session_id
// is preserved so the audit trail keeps the discoverer, not the rewriter.
func TestRegisterTaskFile_Idempotent(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "proj-test-1", "/tmp/proj-test-1")
	if _, err := db.Exec(
		"INSERT INTO tasks (id, project_id, title, status) VALUES (?, ?, ?, ?)",
		89, 1, "t", "ready",
	); err != nil {
		t.Fatalf("seed task: %v", err)
	}

	if err := RegisterTaskFile(89, "sess-first", "/tmp/proj-test-1/foo.go"); err != nil {
		t.Fatalf("first register: %v", err)
	}
	if err := RegisterTaskFile(89, "sess-second", "/tmp/proj-test-1/foo.go"); err != nil {
		t.Fatalf("second register: %v", err)
	}

	var n int
	if err := db.QueryRow(
		"SELECT COUNT(*) FROM task_files WHERE task_id=? AND file_path=?",
		89, "/tmp/proj-test-1/foo.go",
	).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Errorf("duplicate rows = %d, want 1", n)
	}

	var sessionID string
	if err := db.QueryRow(
		"SELECT first_edited_session_id FROM task_files WHERE task_id=? AND file_path=?",
		89, "/tmp/proj-test-1/foo.go",
	).Scan(&sessionID); err != nil {
		t.Fatalf("read row: %v", err)
	}
	if sessionID != "sess-first" {
		t.Errorf("first_edited_session_id = %q, want sess-first (preserved)", sessionID)
	}
}

// TestRegisterTaskFile_ZeroInputsAreNoOp pins the input-validation branch:
// taskID==0 or empty filePath skip the INSERT and return nil — no row is
// written. Lets call sites fire unconditionally without guarding.
func TestRegisterTaskFile_ZeroInputsAreNoOp(t *testing.T) {
	db := withTestDB(t)

	if err := RegisterTaskFile(0, "sess-X", "/tmp/foo.go"); err != nil {
		t.Fatalf("zero taskID: %v", err)
	}
	if err := RegisterTaskFile(1, "sess-X", ""); err != nil {
		t.Fatalf("empty path: %v", err)
	}

	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM task_files").Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Errorf("task_files row count = %d, want 0 (zero inputs must be no-op)", n)
	}
}

// TestIsFileInTaskScope_RegisteredFileMatches pins the task_files-EXISTS
// branch: a previously-registered (task_id, file_path) pair returns true.
func TestIsFileInTaskScope_RegisteredFileMatches(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "proj-test-1", "/tmp/proj-test-1")
	if _, err := db.Exec(
		"INSERT INTO tasks (id, project_id, title, status) VALUES (?, ?, ?, ?)",
		90, 1, "t", "ready",
	); err != nil {
		t.Fatalf("seed task: %v", err)
	}
	if err := RegisterTaskFile(90, "sess-X", "/tmp/proj-test-1/foo.go"); err != nil {
		t.Fatalf("register: %v", err)
	}

	got, err := IsFileInTaskScope(90, "/tmp/proj-test-1/foo.go")
	if err != nil {
		t.Fatalf("IsFileInTaskScope: %v", err)
	}
	if !got {
		t.Errorf("registered file: got false, want true")
	}
}

// TestIsFileInTaskScope_UnregisteredFileNoTextMentionReturnsFalse pins the
// negative branch: a file neither in task_files nor mentioned in the task's
// title/description/text is out of scope.
func TestIsFileInTaskScope_UnregisteredFileNoTextMentionReturnsFalse(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "proj-test-1", "/tmp/proj-test-1")
	if _, err := db.Exec(
		"INSERT INTO tasks (id, project_id, title, description, text, status) VALUES (?, ?, ?, ?, ?, ?)",
		91, 1, "unrelated title", "unrelated desc", "unrelated text", "ready",
	); err != nil {
		t.Fatalf("seed task: %v", err)
	}

	got, err := IsFileInTaskScope(91, "/tmp/proj-test-1/foo.go")
	if err != nil {
		t.Fatalf("IsFileInTaskScope: %v", err)
	}
	if got {
		t.Errorf("unregistered out-of-scope file: got true, want false")
	}
}

// TestIsFileInTaskScope_BaseNameInTitleMatches pins the text-mention branch:
// a file whose basename appears in the task title is considered in-scope
// even without an explicit task_files registration. Lets users author plans
// that mention "edit foo.go" without manual registration.
func TestIsFileInTaskScope_BaseNameInTitleMatches(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "proj-test-1", "/tmp/proj-test-1")
	if _, err := db.Exec(
		"INSERT INTO tasks (id, project_id, title, status) VALUES (?, ?, ?, ?)",
		92, 1, "refactor foo.go to use generics", "ready",
	); err != nil {
		t.Fatalf("seed task: %v", err)
	}

	got, err := IsFileInTaskScope(92, "/tmp/proj-test-1/foo.go")
	if err != nil {
		t.Fatalf("IsFileInTaskScope: %v", err)
	}
	if !got {
		t.Errorf("basename mention in title: got false, want true")
	}
}

// TestIsFileInTaskScope_ZeroInputsReturnFalse pins the input-validation
// branch: zero taskID or empty path returns (false, nil) without touching
// the DB so the function is safe to call unconditionally.
func TestIsFileInTaskScope_ZeroInputsReturnFalse(t *testing.T) {
	withTestDB(t)

	got, err := IsFileInTaskScope(0, "/tmp/foo.go")
	if err != nil {
		t.Fatalf("zero taskID: %v", err)
	}
	if got {
		t.Errorf("zero taskID returned true, want false")
	}
	got, err = IsFileInTaskScope(1, "")
	if err != nil {
		t.Fatalf("empty path: %v", err)
	}
	if got {
		t.Errorf("empty path returned true, want false")
	}
}
