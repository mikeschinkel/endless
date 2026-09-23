package faults_test

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/mikeschinkel/endless/internal/faults"
)

// The fallback sink (E-1887).
//
// A fault raised BECAUSE the database failed cannot be recorded through the
// database. Until E-1887 it was recorded NOWHERE — faults.Record reached the
// detail log only after a successful index write, so the one failure mode where
// losing the report costs most was the one that lost it.
//
// These tests hold the two halves: the line still lands on disk, and it is
// honestly marked as never having reached a row.

// newUnboundableStore binds the faults package to a DB accessor that fails,
// with a real log directory. That is the state under test: a store whose log is
// writable and whose database is not.
func newBrokenStore(t *testing.T) (logDir string) {
	t.Helper()

	logDir = t.TempDir()
	faults.Bind(
		func() (*sql.DB, error) { return nil, os.ErrPermission },
		func() string { return logDir },
		nil,
	)
	t.Cleanup(func() { faults.Bind(nil, nil, nil) })

	return logDir
}

// readLog returns every decoded line of the detail log, in file order.
func readLog(t *testing.T, logDir string) (lines []faults.Detail) {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(logDir, "errors.jsonl"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatalf("read detail log: %v", err)
	}

	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var detail faults.Detail
		if err = json.Unmarshal([]byte(line), &detail); err != nil {
			t.Fatalf("decode %q: %v", line, err)
		}
		lines = append(lines, detail)
	}

	return lines
}

func TestRecord_WritesTheLogLineWhenTheIndexWriteFails(t *testing.T) {
	logDir := newBrokenStore(t)

	faults.Record(faults.Fault{
		Code:    faults.ErrCodeHookWriteFailed,
		Source:  "hook:claude",
		Summary: "claude hook: touching session: no such column: process_id",
		Detail:  "the whole error",
	})

	lines := readLog(t, logDir)
	if len(lines) != 1 {
		t.Fatalf("wrote %d log lines, want 1 — a fault the database could not "+
			"index must still reach the log", len(lines))
	}

	line := lines[0]
	if !line.Unindexed {
		t.Error("the line is not marked unindexed; a reader cannot tell it from an indexed one")
	}
	if line.FaultID != nil {
		t.Errorf("fault_id = %d, want null — no row was written, so no id exists", *line.FaultID)
	}
	if line.Occurrence != 0 {
		t.Errorf("occurrence = %d, want absent — it is assigned by the write that failed", line.Occurrence)
	}
	if line.IndexError == "" {
		t.Error("index_error is empty; why it could not be indexed is the whole diagnosis here")
	}
	if line.Summary != "claude hook: touching session: no such column: process_id" {
		t.Errorf("summary = %q, want the caller's", line.Summary)
	}
	if line.Code != "ERR-0015" {
		t.Errorf("code = %q, want ERR-0015", line.Code)
	}
}

func TestRecord_HealthyDatabaseIsUnchanged(t *testing.T) {
	_, logDir := newBoundStore(t)

	faults.Record(faults.Fault{
		Code:    faults.ErrCodeJobFailed,
		Source:  "job:evaluate",
		Summary: "job \"evaluate\" failed",
	})

	lines := readLog(t, logDir)
	if len(lines) != 1 {
		t.Fatalf("wrote %d log lines, want 1 — the fallback must not add a second", len(lines))
	}
	if lines[0].Unindexed {
		t.Error("an indexed occurrence is marked unindexed")
	}
	if lines[0].FaultID == nil || *lines[0].FaultID == 0 {
		t.Error("fault_id is absent on an indexed occurrence")
	}
	if lines[0].Occurrence != 1 {
		t.Errorf("occurrence = %d, want 1", lines[0].Occurrence)
	}
	if lines[0].IndexError != "" {
		t.Errorf("index_error = %q, want empty when the index write succeeded", lines[0].IndexError)
	}

	if n := faults.UnindexedCount(); n != 0 {
		t.Errorf("UnindexedCount = %d, want 0 — nothing went unindexed", n)
	}
}

func TestUnindexed_ReadsOnlyUnindexedLines(t *testing.T) {
	logDir := newBrokenStore(t)

	for i := 0; i < 3; i++ {
		faults.Record(faults.Fault{
			Code:    faults.ErrCodeHookReadFailed,
			Source:  "hook:claude",
			Summary: "claude hook: looking up project: database is locked",
		})
	}

	details, err := faults.Unindexed()
	if err != nil {
		t.Fatalf("Unindexed: %v", err)
	}
	if len(details) != 3 {
		t.Fatalf("Unindexed returned %d, want 3 — one line per OCCURRENCE, since "+
			"nothing deduped them (that is what the index does)", len(details))
	}
	if n := faults.UnindexedCount(); n != 3 {
		t.Errorf("UnindexedCount = %d, want 3", n)
	}

	// An indexed line in the same file must not be picked up. Details() is the
	// reader that owns those, and neither may claim the other's lines.
	if err = os.WriteFile(
		filepath.Join(logDir, "errors.jsonl"),
		append(mustRead(t, logDir), []byte(`{"kind":"fault","fault_id":7,"occurrence":1,"code":"WARN-0001","summary":"indexed"}`+"\n")...),
		0o644,
	); err != nil {
		t.Fatalf("append an indexed line: %v", err)
	}
	if n := faults.UnindexedCount(); n != 3 {
		t.Errorf("UnindexedCount = %d after adding an INDEXED line, want 3", n)
	}
}

func TestClearUnindexed_MovesTheWatermarkAndItPersists(t *testing.T) {
	logDir := newBrokenStore(t)

	faults.Record(faults.Fault{
		Code:    faults.ErrCodeHookFailed,
		Source:  "hook:claude",
		Summary: "claude hook: something went wrong",
	})
	if n := faults.UnindexedCount(); n != 1 {
		t.Fatalf("UnindexedCount = %d before clearing, want 1", n)
	}

	cleared, err := faults.ClearUnindexed()
	if err != nil {
		t.Fatalf("ClearUnindexed: %v", err)
	}
	if cleared != 1 {
		t.Errorf("ClearUnindexed reported %d, want 1", cleared)
	}
	if n := faults.UnindexedCount(); n != 0 {
		t.Errorf("UnindexedCount = %d after clearing, want 0", n)
	}

	// The watermark is a file, not process state: a later invocation must see
	// the same answer or the notice comes back on the next render.
	faults.Bind(
		func() (*sql.DB, error) { return nil, os.ErrPermission },
		func() string { return logDir },
		nil,
	)
	if n := faults.UnindexedCount(); n != 0 {
		t.Errorf("UnindexedCount = %d after a rebind, want 0 — the watermark did not persist", n)
	}

	// A NEW occurrence after the clear is outstanding again. Clearing is an
	// acknowledgement of what was seen, never a mute.
	faults.Record(faults.Fault{
		Code:    faults.ErrCodeHookFailed,
		Source:  "hook:claude",
		Summary: "claude hook: something else went wrong",
	})
	if n := faults.UnindexedCount(); n != 1 {
		t.Errorf("UnindexedCount = %d after a new occurrence, want 1", n)
	}
}

func TestUnindexed_ATruncatedLogResetsTheWatermark(t *testing.T) {
	logDir := newBrokenStore(t)

	faults.Record(faults.Fault{
		Code:    faults.ErrCodeHookFailed,
		Source:  "hook:claude",
		Summary: "first",
	})
	if _, err := faults.ClearUnindexed(); err != nil {
		t.Fatalf("ClearUnindexed: %v", err)
	}

	// The log is replaced by something shorter than the watermark — rotated by
	// hand, restored from a backup, whatever. An offset into a file that no
	// longer has those bytes cannot be honoured, and the forgiving direction is
	// to show an entry twice rather than swallow one.
	if err := os.WriteFile(filepath.Join(logDir, "errors.jsonl"), nil, 0o644); err != nil {
		t.Fatalf("truncate log: %v", err)
	}
	faults.Record(faults.Fault{
		Code:    faults.ErrCodeHookFailed,
		Source:  "hook:claude",
		Summary: "after the truncation",
	})

	if n := faults.UnindexedCount(); n != 1 {
		t.Errorf("UnindexedCount = %d, want 1 — a watermark past the end of the "+
			"log must read as 0 rather than hide everything after it", n)
	}
}

func TestDetails_IgnoresUnindexedLines(t *testing.T) {
	db, logDir := newBoundStore(t)

	faults.Record(faults.Fault{
		Code:    faults.ErrCodeJobFailed,
		Source:  "job:evaluate",
		Summary: "job \"evaluate\" failed",
		Detail:  "indexed detail",
	})

	var id int64
	if err := db.QueryRow(`SELECT id FROM errors`).Scan(&id); err != nil {
		t.Fatalf("read the incident id: %v", err)
	}

	// An unindexed line carrying a null id must not be swept into an incident's
	// detail view; it belongs to no incident.
	f, err := os.OpenFile(filepath.Join(logDir, "errors.jsonl"), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open log: %v", err)
	}
	if _, err = f.WriteString(`{"kind":"fault","fault_id":null,"unindexed":true,"code":"ERR-0015","summary":"orphan"}` + "\n"); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err = f.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	details, err := faults.Details(id)
	if err != nil {
		t.Fatalf("Details: %v", err)
	}
	if len(details) != 1 {
		t.Fatalf("Details returned %d, want 1 — the unindexed line is not this incident's", len(details))
	}
	if details[0].Detail != "indexed detail" {
		t.Errorf("Details returned %q", details[0].Detail)
	}
}

func mustRead(t *testing.T, logDir string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(logDir, "errors.jsonl"))
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	return data
}

func TestUnindexed_WatermarkHoldsAcrossALogThatOutgrowsTheDigestPrefix(t *testing.T) {
	logDir := newBrokenStore(t)

	// Cleared while the log is small, so the watermark's digest covers the
	// whole of it. The read path must keep honouring that digest after the log
	// grows past the bounded prefix it now reads — otherwise every clear would
	// silently come undone on a busy machine, which is the failure mode the
	// bounded read could have introduced.
	faults.Record(faults.Fault{
		Code:    faults.ErrCodeHookFailed,
		Source:  "hook:claude",
		Summary: "first",
	})
	if _, err := faults.ClearUnindexed(); err != nil {
		t.Fatalf("ClearUnindexed: %v", err)
	}

	for i := 0; i < 60; i++ {
		faults.Record(faults.Fault{
			Code:    faults.ErrCodeHookFailed,
			Source:  "hook:claude",
			Summary: "later",
			Detail:  strings.Repeat("x", 200),
		})
	}

	size, err := os.Stat(filepath.Join(logDir, "errors.jsonl"))
	if err != nil {
		t.Fatalf("stat log: %v", err)
	}
	if size.Size() <= 4096 {
		t.Fatalf("log is %d bytes; this test needs it past the digest prefix", size.Size())
	}

	if n := faults.UnindexedCount(); n != 60 {
		t.Errorf("UnindexedCount = %d, want 60 — the cleared first occurrence must "+
			"stay cleared and every later one must be outstanding", n)
	}
}

func TestClearUnindexed_OnALogThatDoesNotExistYet(t *testing.T) {
	newBrokenStore(t)

	// Nothing has ever been recorded. Clearing must be a no-op rather than
	// writing a watermark for a file whose opening bytes cannot be known.
	cleared, err := faults.ClearUnindexed()
	if err != nil {
		t.Fatalf("ClearUnindexed with no log: %v", err)
	}
	if cleared != 0 {
		t.Errorf("cleared %d, want 0", cleared)
	}

	faults.Record(faults.Fault{
		Code:    faults.ErrCodeHookFailed,
		Source:  "hook:claude",
		Summary: "the first one ever",
	})
	if n := faults.UnindexedCount(); n != 1 {
		t.Errorf("UnindexedCount = %d, want 1 — a clear before the log existed must "+
			"not have swallowed the first occurrence recorded after it", n)
	}
}
