package events_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mikeschinkel/endless/internal/events"
)

// TestReadAllEvents_NewPathOnly is the post-migration steady state.
func TestReadAllEvents_NewPathOnly(t *testing.T) {
	dir := t.TempDir()
	newDir := filepath.Join(dir, ".endless", events.LedgerDirName)
	if err := os.MkdirAll(newDir, 0755); err != nil {
		t.Fatal(err)
	}

	body := `{"v":1,"ts":"5WYM00000001","kind":"task.created","project":"x","entity":{"type":"task","id":"1"},"actor":{"kind":"cli","id":"t"},"payload":{}}` + "\n" +
		`{"v":1,"ts":"5WYM00000002","kind":"task.fields_updated","project":"x","entity":{"type":"task","id":"1"},"actor":{"kind":"cli","id":"t"},"payload":{"fields":{"status":"ready"}}}` + "\n"
	if err := os.WriteFile(filepath.Join(newDir, "db-entries-aaaa-000001.jsonl"), []byte(body), 0644); err != nil {
		t.Fatal(err)
	}

	got, err := events.ReadAllEvents(dir)
	if err != nil {
		t.Fatalf("ReadAllEvents: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("want 2 events, got %d", len(got))
	}
}

// TestReadAllEvents_LegacyPathOnly is the pre-migration state where the
// new binary is being run for the first time but no write has happened yet
// to trigger migration.
func TestReadAllEvents_LegacyPathOnly(t *testing.T) {
	dir := t.TempDir()
	legacyDir := filepath.Join(dir, ".endless", events.LegacyDirName)
	if err := os.MkdirAll(legacyDir, 0755); err != nil {
		t.Fatal(err)
	}

	body := `{"v":1,"ts":"5WYM00000001","kind":"task.created","project":"x","entity":{"type":"task","id":"1"},"actor":{"kind":"cli","id":"t"},"payload":{}}` + "\n"
	if err := os.WriteFile(filepath.Join(legacyDir, "events-aaaa-000001.jsonl"), []byte(body), 0644); err != nil {
		t.Fatal(err)
	}

	got, err := events.ReadAllEvents(dir)
	if err != nil {
		t.Fatalf("ReadAllEvents: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("want 1 event from legacy path, got %d", len(got))
	}
	if got[0].TS != "5WYM00000001" {
		t.Errorf("ts mismatch: got %s", got[0].TS)
	}
}

// TestReadAllEvents_BothPaths exercises the transition window where some
// events live under the new path (already migrated or written by the new
// binary) and others live under the legacy path (e.g., written by an older
// binary in another shell that hasn't been upgraded yet, OR sitting there
// from a refused-mixed-state migration that was manually unblocked).
//
// This is the load-bearing scenario for the "backward-compat read window"
// guarantee in E-1197: a reader must surface ALL events regardless of
// which directory they're in, sorted by kairos timestamp.
func TestReadAllEvents_BothPaths(t *testing.T) {
	dir := t.TempDir()
	newDir := filepath.Join(dir, ".endless", events.LedgerDirName)
	legacyDir := filepath.Join(dir, ".endless", events.LegacyDirName)
	if err := os.MkdirAll(newDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(legacyDir, 0755); err != nil {
		t.Fatal(err)
	}

	// New path: ts=...0001 and ts=...0003
	newBody := `{"v":1,"ts":"5WYM00000001","kind":"task.created","project":"x","entity":{"type":"task","id":"1"},"actor":{"kind":"cli","id":"t"},"payload":{}}` + "\n" +
		`{"v":1,"ts":"5WYM00000003","kind":"task.status_changed","project":"x","entity":{"type":"task","id":"1"},"actor":{"kind":"cli","id":"t"},"payload":{"old_status":"ready","new_status":"in_progress"}}` + "\n"
	if err := os.WriteFile(filepath.Join(newDir, "db-entries-aaaa-000001.jsonl"), []byte(newBody), 0644); err != nil {
		t.Fatal(err)
	}

	// Legacy path: ts=...0002 and ts=...0004 (interleaves chronologically)
	legacyBody := `{"v":1,"ts":"5WYM00000002","kind":"task.fields_updated","project":"x","entity":{"type":"task","id":"1"},"actor":{"kind":"cli","id":"t"},"payload":{"fields":{"status":"ready"}}}` + "\n" +
		`{"v":1,"ts":"5WYM00000004","kind":"task.fields_updated","project":"x","entity":{"type":"task","id":"1"},"actor":{"kind":"cli","id":"t"},"payload":{"fields":{"status":"verify"}}}` + "\n"
	if err := os.WriteFile(filepath.Join(legacyDir, "events-bbbb-000001.jsonl"), []byte(legacyBody), 0644); err != nil {
		t.Fatal(err)
	}

	got, err := events.ReadAllEvents(dir)
	if err != nil {
		t.Fatalf("ReadAllEvents: %v", err)
	}

	// All four events visible
	if len(got) != 4 {
		t.Fatalf("want 4 events across both paths, got %d", len(got))
	}

	// Sorted chronologically (regardless of source path)
	expectedOrder := []string{
		"5WYM00000001", // new
		"5WYM00000002", // legacy
		"5WYM00000003", // new
		"5WYM00000004", // legacy
	}
	for i, want := range expectedOrder {
		if got[i].TS != want {
			t.Errorf("event[%d]: want ts=%s, got ts=%s", i, want, got[i].TS)
		}
	}
}

// TestReadAllEvents_NoDirsAtAll covers a fresh project where neither
// directory has been created yet. Returns empty, no error.
func TestReadAllEvents_NoDirsAtAll(t *testing.T) {
	dir := t.TempDir()
	got, err := events.ReadAllEvents(dir)
	if err != nil {
		t.Fatalf("ReadAllEvents: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("want 0 events, got %d", len(got))
	}
}

// TestReadAllEvents_OnlyWrongPrefix ignores files in either directory whose
// names don't match the expected prefix (e.g., a stray .DS_Store, README,
// or a file that's neither old nor new ledger format).
func TestReadAllEvents_OnlyWrongPrefix(t *testing.T) {
	dir := t.TempDir()
	newDir := filepath.Join(dir, ".endless", events.LedgerDirName)
	if err := os.MkdirAll(newDir, 0755); err != nil {
		t.Fatal(err)
	}
	// Wrong prefix in new dir — must be ignored
	if err := os.WriteFile(filepath.Join(newDir, "random-aaaa-000001.jsonl"), []byte(`{"v":1,"ts":"5WYM00000001"}`+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	// Non-jsonl file — must be ignored
	if err := os.WriteFile(filepath.Join(newDir, "db-entries-aaaa-000002.txt"), []byte(`not jsonl`+"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	got, err := events.ReadAllEvents(dir)
	if err != nil {
		t.Fatalf("ReadAllEvents: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("want 0 events (wrong prefix and extension should be ignored), got %d", len(got))
	}
}
