package monitor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteCompanion_RoundTrip(t *testing.T) {
	root := t.TempDir()

	c := CompanionFile{
		Harness:          "claude",
		HarnessSessionID: "f41f263e-c708-4c42-af7c-083b5be04943",
		EndlessSessionID: 247,
		PaneID:           "%53",
		CWD:              "/Users/mike/Projects/endless",
		PID:              20545,
		StartedAt:        "2026-04-29T03:51:23Z",
	}

	if err := writeCompanionAtRoot(root, c); err != nil {
		t.Fatalf("write: %v", err)
	}

	want := filepath.Join(root, ".endless", "sessions", "claude-f41f263e-c708-4c42-af7c-083b5be04943.json")
	data, err := os.ReadFile(want)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}

	var got CompanionFile
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got != c {
		t.Errorf("round-trip mismatch:\n got=%+v\nwant=%+v", got, c)
	}
}

func TestWriteCompanion_Overwrites(t *testing.T) {
	root := t.TempDir()

	c1 := CompanionFile{
		Harness:          "claude",
		HarnessSessionID: "abc",
		EndlessSessionID: 1,
		PID:              111,
		StartedAt:        "2026-04-29T01:00:00Z",
		CWD:              "/old",
	}
	c2 := c1
	c2.PID = 222
	c2.CWD = "/new"

	if err := writeCompanionAtRoot(root, c1); err != nil {
		t.Fatalf("write 1: %v", err)
	}
	if err := writeCompanionAtRoot(root, c2); err != nil {
		t.Fatalf("write 2: %v", err)
	}

	target := filepath.Join(root, ".endless", "sessions", "claude-abc.json")
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var got CompanionFile
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.PID != 222 || got.CWD != "/new" {
		t.Errorf("expected overwrite (pid=222, cwd=/new); got pid=%d cwd=%s", got.PID, got.CWD)
	}
}

func TestWriteCompanion_DefaultsStartedAt(t *testing.T) {
	root := t.TempDir()
	c := CompanionFile{
		Harness:          "claude",
		HarnessSessionID: "no-time",
		EndlessSessionID: 1,
		PID:              1,
		CWD:              "/x",
	}
	if err := writeCompanionAtRoot(root, c); err != nil {
		t.Fatalf("write: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, ".endless", "sessions", "claude-no-time.json"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var got CompanionFile
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.StartedAt == "" {
		t.Errorf("StartedAt should default to now, got empty string")
	}
}

func TestWriteCompanion_RequiresHarnessAndID(t *testing.T) {
	root := t.TempDir()
	cases := []CompanionFile{
		{HarnessSessionID: "x"},
		{Harness: "claude"},
		{},
	}
	for i, c := range cases {
		if err := writeCompanionAtRoot(root, c); err == nil {
			t.Errorf("case %d: expected error for missing required fields", i)
		}
	}
}

func TestWriteCompanion_OmitsEmptyOptionalFields(t *testing.T) {
	root := t.TempDir()
	c := CompanionFile{
		Harness:          "claude",
		HarnessSessionID: "min",
		EndlessSessionID: 1,
		PID:              1,
		CWD:              "/x",
		StartedAt:        "2026-04-29T00:00:00Z",
		// PaneID and WorktreePath intentionally empty
	}
	if err := writeCompanionAtRoot(root, c); err != nil {
		t.Fatalf("write: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, ".endless", "sessions", "claude-min.json"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal raw: %v", err)
	}
	if _, ok := raw["pane_id"]; ok {
		t.Errorf("pane_id should be omitted when empty; raw=%v", raw)
	}
	if _, ok := raw["worktree_path"]; ok {
		t.Errorf("worktree_path should be omitted when empty; raw=%v", raw)
	}
}

func TestRemoveCompanion_RemovesFile(t *testing.T) {
	root := t.TempDir()
	c := CompanionFile{
		Harness:          "claude",
		HarnessSessionID: "to-remove",
		EndlessSessionID: 1,
		PID:              1,
		CWD:              "/x",
		StartedAt:        "2026-04-29T00:00:00Z",
	}
	if err := writeCompanionAtRoot(root, c); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := removeCompanionAtRoot(root, "claude", "to-remove"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	target := filepath.Join(root, ".endless", "sessions", "claude-to-remove.json")
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Errorf("expected file gone, stat err=%v", err)
	}
}

func TestRemoveCompanion_IdempotentOnMissing(t *testing.T) {
	root := t.TempDir()
	if err := removeCompanionAtRoot(root, "claude", "never-existed"); err != nil {
		t.Errorf("remove of missing file should be nil, got %v", err)
	}
}

func TestCompanionExists_TrueWhenPresent(t *testing.T) {
	root := t.TempDir()
	c := CompanionFile{
		Harness:          "claude",
		HarnessSessionID: "present",
		EndlessSessionID: 1,
		PID:              1,
		CWD:              "/x",
		StartedAt:        "2026-04-30T00:00:00Z",
	}
	if err := writeCompanionAtRoot(root, c); err != nil {
		t.Fatalf("write: %v", err)
	}
	exists, err := companionExistsAtRoot(root, "claude", "present")
	if err != nil {
		t.Fatalf("exists: %v", err)
	}
	if !exists {
		t.Errorf("expected exists=true for present file")
	}
}

func TestCompanionExists_FalseWhenMissing(t *testing.T) {
	root := t.TempDir()
	exists, err := companionExistsAtRoot(root, "claude", "never-written")
	if err != nil {
		t.Errorf("missing file should not error, got %v", err)
	}
	if exists {
		t.Errorf("expected exists=false for missing file")
	}
}

func TestCompanionExists_FalseWhenSessionsDirMissing(t *testing.T) {
	// New project root with no .endless/sessions/ dir at all.
	root := t.TempDir()
	exists, err := companionExistsAtRoot(root, "claude", "anything")
	if err != nil {
		t.Errorf("missing dir should not error, got %v", err)
	}
	if exists {
		t.Errorf("expected exists=false when sessions dir absent")
	}
}
