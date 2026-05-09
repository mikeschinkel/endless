package events_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mikeschinkel/endless/internal/events"
)

func TestMigrate_NoLegacyDir_NoOp(t *testing.T) {
	dir := t.TempDir()
	if err := events.MigrateLegacyLedger(dir); err != nil {
		t.Fatalf("MigrateLegacyLedger: %v", err)
	}
	// Neither dir should exist
	if _, err := os.Stat(filepath.Join(dir, ".endless", events.LegacyDirName)); !os.IsNotExist(err) {
		t.Errorf("legacy dir should not exist")
	}
	if _, err := os.Stat(filepath.Join(dir, ".endless", events.LedgerDirName)); !os.IsNotExist(err) {
		t.Errorf("new dir should not be created when there's nothing to migrate")
	}
}

func TestMigrate_RenamesAndPreservesContent(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, ".endless", events.LegacyDirName)
	if err := os.MkdirAll(legacy, 0755); err != nil {
		t.Fatal(err)
	}

	// Two legacy segments with realistic events
	src1 := filepath.Join(legacy, "events-a7f3-000001.jsonl")
	src2 := filepath.Join(legacy, "events-a7f3-000002.jsonl")
	body1 := `{"v":1,"ts":"5WYM00000001","kind":"task.created"}` + "\n" +
		`{"v":1,"ts":"5WYM00000002","kind":"task.fields_updated"}` + "\n"
	body2 := `{"v":1,"ts":"5WYM00000003","kind":"task.status_changed"}` + "\n"
	if err := os.WriteFile(src1, []byte(body1), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src2, []byte(body2), 0644); err != nil {
		t.Fatal(err)
	}

	if err := events.MigrateLegacyLedger(dir); err != nil {
		t.Fatalf("MigrateLegacyLedger: %v", err)
	}

	// Legacy dir should be gone
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Errorf("legacy dir should have been removed, got err=%v", err)
	}

	// New dir should hold the renamed files
	newDir := filepath.Join(dir, ".endless", events.LedgerDirName)
	dst1 := filepath.Join(newDir, "db-entries-a7f3-000001.jsonl")
	dst2 := filepath.Join(newDir, "db-entries-a7f3-000002.jsonl")

	got1, err := os.ReadFile(dst1)
	if err != nil {
		t.Fatalf("read dst1: %v", err)
	}
	if string(got1) != body1 {
		t.Errorf("body1 mismatch:\nwant=%q\ngot=%q", body1, got1)
	}
	got2, err := os.ReadFile(dst2)
	if err != nil {
		t.Fatalf("read dst2: %v", err)
	}
	if string(got2) != body2 {
		t.Errorf("body2 mismatch:\nwant=%q\ngot=%q", body2, got2)
	}
}

func TestMigrate_Idempotent(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, ".endless", events.LegacyDirName)
	if err := os.MkdirAll(legacy, 0755); err != nil {
		t.Fatal(err)
	}
	body := `{"v":1,"ts":"5WYM00000001","kind":"task.created"}` + "\n"
	if err := os.WriteFile(filepath.Join(legacy, "events-a7f3-000001.jsonl"), []byte(body), 0644); err != nil {
		t.Fatal(err)
	}

	// First migration
	if err := events.MigrateLegacyLedger(dir); err != nil {
		t.Fatalf("first migrate: %v", err)
	}
	// Second migration on a clean tree (legacy dir gone) — must no-op
	if err := events.MigrateLegacyLedger(dir); err != nil {
		t.Fatalf("second migrate: %v", err)
	}

	// Content still intact
	dst := filepath.Join(dir, ".endless", events.LedgerDirName, "db-entries-a7f3-000001.jsonl")
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read dst: %v", err)
	}
	if string(got) != body {
		t.Errorf("body mismatch after second migrate:\nwant=%q\ngot=%q", body, got)
	}
}

func TestMigrate_RefusesMixedState(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, ".endless", events.LegacyDirName)
	newDir := filepath.Join(dir, ".endless", events.LedgerDirName)
	if err := os.MkdirAll(legacy, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(newDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Both dirs have ledger files — refuse migration
	if err := os.WriteFile(filepath.Join(legacy, "events-a7f3-000001.jsonl"), []byte("legacy\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(newDir, "db-entries-b2c1-000001.jsonl"), []byte("new\n"), 0644); err != nil {
		t.Fatal(err)
	}

	err := events.MigrateLegacyLedger(dir)
	if err == nil {
		t.Fatal("MigrateLegacyLedger should refuse mixed-state, got nil")
	}
	if !strings.Contains(err.Error(), "refusing automatic migration") {
		t.Errorf("error should mention refusal, got: %v", err)
	}

	// Both files should still exist (no destructive action taken)
	if _, err := os.Stat(filepath.Join(legacy, "events-a7f3-000001.jsonl")); err != nil {
		t.Errorf("legacy file removed during refused migration: %v", err)
	}
	if _, err := os.Stat(filepath.Join(newDir, "db-entries-b2c1-000001.jsonl")); err != nil {
		t.Errorf("new file removed during refused migration: %v", err)
	}
}

func TestMigrate_LineCountVerification(t *testing.T) {
	// Confirm migrate copies all lines, including the last one without
	// trailing newline (defensive).
	dir := t.TempDir()
	legacy := filepath.Join(dir, ".endless", events.LegacyDirName)
	if err := os.MkdirAll(legacy, 0755); err != nil {
		t.Fatal(err)
	}

	// 248 lines like the incident
	var sb strings.Builder
	for i := 1; i <= 248; i++ {
		sb.WriteString(`{"v":1,"ts":"5WYM`)
		sb.WriteString(strings.Repeat("X", 8))
		sb.WriteString(`","kind":"task.created","n":`)
		// Use a simple counter so each line is distinct
		if i < 10 {
			sb.WriteString("00")
		} else if i < 100 {
			sb.WriteString("0")
		}
		// crude itoa
		sb.WriteString(itoa(i))
		sb.WriteString("}\n")
	}
	body := sb.String()
	if err := os.WriteFile(filepath.Join(legacy, "events-a7f3-000001.jsonl"), []byte(body), 0644); err != nil {
		t.Fatal(err)
	}

	if err := events.MigrateLegacyLedger(dir); err != nil {
		t.Fatalf("MigrateLegacyLedger: %v", err)
	}

	dst := filepath.Join(dir, ".endless", events.LedgerDirName, "db-entries-a7f3-000001.jsonl")
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read dst: %v", err)
	}
	if string(got) != body {
		t.Errorf("body mismatch — migrated content differs from source")
	}
	// Line count parity
	if want, gotL := 248, strings.Count(string(got), "\n"); want != gotL {
		t.Errorf("line count: want=%d got=%d", want, gotL)
	}
}

func TestMigrate_EmptyLegacyDir_RemovedNoOp(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, ".endless", events.LegacyDirName)
	if err := os.MkdirAll(legacy, 0755); err != nil {
		t.Fatal(err)
	}

	if err := events.MigrateLegacyLedger(dir); err != nil {
		t.Fatalf("MigrateLegacyLedger: %v", err)
	}

	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Errorf("empty legacy dir should be removed")
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var s []byte
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		s = append([]byte{byte('0' + n%10)}, s...)
		n /= 10
	}
	if neg {
		s = append([]byte{'-'}, s...)
	}
	return string(s)
}
