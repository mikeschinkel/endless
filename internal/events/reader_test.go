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

// TestReadAllEvents_NoDirAtAll covers a fresh project where the ledger
// directory has not been created yet. Returns empty, no error.
func TestReadAllEvents_NoDirAtAll(t *testing.T) {
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
