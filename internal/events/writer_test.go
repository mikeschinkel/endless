package events_test

import (
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
	segPath := filepath.Join(projectRoot, ".endless", "events", w.CurrentSegment())
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
	seg1 := filepath.Join(dir, ".endless", "events", "events-b2c1-000001.jsonl")
	seg2 := filepath.Join(dir, ".endless", "events", "events-b2c1-000002.jsonl")

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
	segPath := filepath.Join(dir, ".endless", "events", "events-d5e6-000001.jsonl")
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
