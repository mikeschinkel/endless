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

// TestReadAllEvents_MultipleSegmentsConcatenateInTimestampOrder verifies
// that events spread across multiple segment files come back sorted by
// kairos timestamp (lexicographic), regardless of which file they lived in.
func TestReadAllEvents_MultipleSegmentsConcatenateInTimestampOrder(t *testing.T) {
	dir := t.TempDir()
	newDir := filepath.Join(dir, ".endless", events.LedgerDirName)
	if err := os.MkdirAll(newDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Segment 1: ts 1 and 3.
	seg1Body := `{"v":1,"ts":"5WYM00000001","kind":"task.created","project":"x","entity":{"type":"task","id":"1"},"actor":{"kind":"cli","id":"t"},"payload":{}}` + "\n" +
		`{"v":1,"ts":"5WYM00000003","kind":"task.created","project":"x","entity":{"type":"task","id":"3"},"actor":{"kind":"cli","id":"t"},"payload":{}}` + "\n"
	if err := os.WriteFile(filepath.Join(newDir, "db-entries-aaaa-000001.jsonl"), []byte(seg1Body), 0644); err != nil {
		t.Fatal(err)
	}

	// Segment 2: ts 2 and 4.
	seg2Body := `{"v":1,"ts":"5WYM00000002","kind":"task.created","project":"x","entity":{"type":"task","id":"2"},"actor":{"kind":"cli","id":"t"},"payload":{}}` + "\n" +
		`{"v":1,"ts":"5WYM00000004","kind":"task.created","project":"x","entity":{"type":"task","id":"4"},"actor":{"kind":"cli","id":"t"},"payload":{}}` + "\n"
	if err := os.WriteFile(filepath.Join(newDir, "db-entries-aaaa-000002.jsonl"), []byte(seg2Body), 0644); err != nil {
		t.Fatal(err)
	}

	got, err := events.ReadAllEvents(dir)
	if err != nil {
		t.Fatalf("ReadAllEvents: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("want 4 events, got %d", len(got))
	}

	wantIDs := []string{"1", "2", "3", "4"}
	for i, w := range wantIDs {
		if got[i].Entity.ID != w {
			t.Errorf("event[%d].Entity.ID = %q, want %q", i, got[i].Entity.ID, w)
		}
	}
}

// TestReadAllEvents_MalformedLineReturnsError verifies that a syntactically
// invalid JSONL line in a segment produces an error from ReadAllEvents
// (rather than silently dropping the line).
func TestReadAllEvents_MalformedLineReturnsError(t *testing.T) {
	dir := t.TempDir()
	newDir := filepath.Join(dir, ".endless", events.LedgerDirName)
	if err := os.MkdirAll(newDir, 0755); err != nil {
		t.Fatal(err)
	}

	body := `{"v":1,"ts":"5WYM00000001","kind":"task.created","project":"x","entity":{"type":"task","id":"1"},"actor":{"kind":"cli","id":"t"},"payload":{}}` + "\n" +
		`not-valid-json` + "\n"
	if err := os.WriteFile(filepath.Join(newDir, "db-entries-aaaa-000001.jsonl"), []byte(body), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := events.ReadAllEvents(dir)
	if err == nil {
		t.Fatalf("ReadAllEvents should fail on malformed JSONL line")
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
