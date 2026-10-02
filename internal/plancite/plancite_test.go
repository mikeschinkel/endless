package plancite_test

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/mikeschinkel/endless/internal/plancite"
)

func TestPaths(t *testing.T) {
	plan := "Edit `internal/monitor/session.go:420` and `src/endless/task_cmd.py`.\n" +
		"The package `internal/plancite` is new; see docs/guide/orchestration.md, then\n" +
		"internal/hookcmd/claude.go:594-650.\n" +
		"Not paths: and/or, E-1993/E-1994, read/write, https://example.com/a.go,\n" +
		"`/usr/local/bin/x.go`, `~/x/y.go`, `.endless/tasks/e-<id>/verify.sh`, `cmd/*.go`.\n" +
		"Duplicate: `internal/plancite`."
	got := plancite.Paths(plan)
	want := []string{
		"docs/guide/orchestration.md",
		"internal/hookcmd/claude.go",
		"internal/monitor/session.go",
		"internal/plancite",
		"src/endless/task_cmd.py",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Paths =\n  %v\nwant\n  %v", got, want)
	}
}

func TestMissing(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal", "kept"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "internal", "kept", "a.go"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	plan := "Touch `internal/kept/a.go`, `internal/kept` and `internal/gone/b.go`; also internal/gone2.go."
	got := plancite.Missing(root, plan)
	want := []string{"internal/gone/b.go", "internal/gone2.go"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Missing = %v, want %v", got, want)
	}
}

func TestMissingNothingCited(t *testing.T) {
	if got := plancite.Missing(t.TempDir(), "Prose with no paths at all."); len(got) != 0 {
		t.Errorf("Missing = %v, want none", got)
	}
}
