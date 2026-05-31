package events_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mikeschinkel/endless/internal/events"
)

func TestWriter_Append(t *testing.T) {
	dir := t.TempDir()
	projectRoot := dir

	w, err := events.NewWriter(projectRoot, "a7f3")
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}

	line := []byte(`{"v":1,"ts":"test","kind":"task.created"}`)
	if err := w.Append(line); err != nil {
		t.Fatalf("Append: %v", err)
	}

	// Verify file exists
	segPath := filepath.Join(projectRoot, ".endless", events.LedgerDirName, w.CurrentSegment())
	data, err := os.ReadFile(segPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	if !strings.Contains(string(data), `"kind":"task.created"`) {
		t.Errorf("segment content doesn't contain event: %s", data)
	}
}

func TestWriter_Rotation(t *testing.T) {
	dir := t.TempDir()

	w, err := events.NewWriter(dir, "b2c1")
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	// Override max for testing
	w.SetMaxCount(3)

	line := []byte(`{"v":1}`)
	for i := range 5 {
		if err := w.Append(line); err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
	}

	// Should have rotated: first segment has 3, second has 2
	seg1 := filepath.Join(dir, ".endless", events.LedgerDirName, "db-entries-b2c1-000001.jsonl")
	seg2 := filepath.Join(dir, ".endless", events.LedgerDirName, "db-entries-b2c1-000002.jsonl")

	data1, err := os.ReadFile(seg1)
	if err != nil {
		t.Fatalf("ReadFile seg1: %v", err)
	}
	data2, err := os.ReadFile(seg2)
	if err != nil {
		t.Fatalf("ReadFile seg2: %v", err)
	}

	lines1 := countLines(data1)
	lines2 := countLines(data2)

	if lines1 != 3 {
		t.Errorf("seg1 has %d lines, want 3", lines1)
	}
	if lines2 != 2 {
		t.Errorf("seg2 has %d lines, want 2", lines2)
	}
}

func TestWriter_ExceedsMaxBytes(t *testing.T) {
	dir := t.TempDir()

	w, err := events.NewWriter(dir, "c3d4")
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}

	// Create a line that exceeds 1MB
	bigLine := make([]byte, 1024*1024+1)
	for i := range bigLine {
		bigLine[i] = 'x'
	}

	err = w.Append(bigLine)
	if err == nil {
		t.Fatal("Append should fail for oversized line")
	}
	if !strings.Contains(err.Error(), "exceeds") {
		t.Errorf("error should mention exceeds: %v", err)
	}
}

func TestWriter_ResumeExisting(t *testing.T) {
	dir := t.TempDir()

	// Write 2 events with first writer
	w1, err := events.NewWriter(dir, "d5e6")
	if err != nil {
		t.Fatalf("NewWriter 1: %v", err)
	}
	w1.Append([]byte(`{"v":1,"n":1}`))
	w1.Append([]byte(`{"v":1,"n":2}`))

	// Create new writer, should resume at same segment with count=2
	w2, err := events.NewWriter(dir, "d5e6")
	if err != nil {
		t.Fatalf("NewWriter 2: %v", err)
	}
	w2.Append([]byte(`{"v":1,"n":3}`))

	// All 3 events should be in the same segment
	segPath := filepath.Join(dir, ".endless", events.LedgerDirName, "db-entries-d5e6-000001.jsonl")
	data, err := os.ReadFile(segPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if countLines(data) != 3 {
		t.Errorf("segment has %d lines, want 3", countLines(data))
	}
}

func countLines(data []byte) int {
	n := 0
	for _, b := range data {
		if b == '\n' {
			n++
		}
	}
	return n
}

// TestNewWriter_CreatesLedgerDirAndFirstSegment verifies NewWriter on a
// fresh project root creates .endless/db-ledger/ and reports segment seq=1
// (no append yet — file is created on first Append).
func TestNewWriter_CreatesLedgerDirAndFirstSegment(t *testing.T) {
	dir := t.TempDir()

	w, err := events.NewWriter(dir, "e7f8")
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}

	ledgerDir := filepath.Join(dir, ".endless", events.LedgerDirName)
	info, err := os.Stat(ledgerDir)
	if err != nil {
		t.Fatalf("stat ledger dir: %v", err)
	}
	if !info.IsDir() {
		t.Errorf("ledger path %q is not a directory", ledgerDir)
	}

	want := "db-entries-e7f8-000001.jsonl"
	if w.CurrentSegment() != want {
		t.Errorf("CurrentSegment() = %q, want %q", w.CurrentSegment(), want)
	}
}

// TestNewWriter_DistinctNodesProduceDistinctSegments verifies that two
// NewWriter calls for different node hexes against the same project root
// yield independent segment files (one segment per node).
func TestNewWriter_DistinctNodesProduceDistinctSegments(t *testing.T) {
	dir := t.TempDir()

	w1, err := events.NewWriter(dir, "aaaa")
	if err != nil {
		t.Fatalf("NewWriter aaaa: %v", err)
	}
	w2, err := events.NewWriter(dir, "bbbb")
	if err != nil {
		t.Fatalf("NewWriter bbbb: %v", err)
	}

	if w1.CurrentSegment() == w2.CurrentSegment() {
		t.Errorf("expected distinct segments for distinct nodes, got %q == %q",
			w1.CurrentSegment(), w2.CurrentSegment())
	}

	line := []byte(`{"v":1,"ts":"test"}`)
	if err := w1.Append(line); err != nil {
		t.Fatalf("w1.Append: %v", err)
	}
	if err := w2.Append(line); err != nil {
		t.Fatalf("w2.Append: %v", err)
	}

	seg1 := filepath.Join(dir, ".endless", events.LedgerDirName, w1.CurrentSegment())
	seg2 := filepath.Join(dir, ".endless", events.LedgerDirName, w2.CurrentSegment())
	if _, err := os.Stat(seg1); err != nil {
		t.Errorf("seg1 missing: %v", err)
	}
	if _, err := os.Stat(seg2); err != nil {
		t.Errorf("seg2 missing: %v", err)
	}
}

// TestNewWriter_AppendRoundTripsThroughReadAllEvents verifies the
// Writer.Append → ReadAllEvents handshake: a real Event marshaled and
// appended via Writer.Append must come back through ReadAllEvents as the
// same event.
func TestNewWriter_AppendRoundTripsThroughReadAllEvents(t *testing.T) {
	dir := t.TempDir()

	w, err := events.NewWriter(dir, "f9a1")
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}

	payload, err := json.Marshal(events.TaskCreatedPayload{
		Title: "Round-trip", Phase: "now", Status: "needs_plan", Type: "task",
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	evt := events.Event{
		V:       events.Version,
		TS:      "5WYM00000001",
		Kind:    events.KindTaskCreated,
		Project: "test",
		Entity:  events.EntityRef{Type: events.EntityTask, ID: "42"},
		Actor:   events.Actor{Kind: events.ActorCLI, ID: "tester"},
		Payload: payload,
	}
	line, err := json.Marshal(evt)
	if err != nil {
		t.Fatalf("marshal evt: %v", err)
	}
	if err := w.Append(line); err != nil {
		t.Fatalf("Append: %v", err)
	}

	got, err := events.ReadAllEvents(dir)
	if err != nil {
		t.Fatalf("ReadAllEvents: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("ReadAllEvents returned %d events, want 1", len(got))
	}
	if got[0].Kind != events.KindTaskCreated {
		t.Errorf("Kind = %q, want %q", got[0].Kind, events.KindTaskCreated)
	}
	if got[0].Entity.ID != "42" {
		t.Errorf("Entity.ID = %q, want %q", got[0].Entity.ID, "42")
	}
	if got[0].TS != "5WYM00000001" {
		t.Errorf("TS = %q, want %q", got[0].TS, "5WYM00000001")
	}
}
