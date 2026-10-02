package hookcmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRenderPrimedResume pins the resume note's three cases: nothing cited,
// everything still there, and drift — where every missing path is named.
func TestRenderPrimedResume(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal", "kept"), 0o755); err != nil {
		t.Fatal(err)
	}

	got := renderPrimedResume(7, "No paths here.", root)
	for _, want := range []string{"E-7", "endless task claim E-7", "cites no repository paths"} {
		if !strings.Contains(got, want) {
			t.Errorf("no-citation note lacks %q:\n%s", want, got)
		}
	}

	got = renderPrimedResume(7, "See `internal/kept`.", root)
	if !strings.Contains(got, "all 1 path(s) the plan cites still exist") {
		t.Errorf("clean note:\n%s", got)
	}

	got = renderPrimedResume(7, "See `internal/kept` and `internal/gone/x.go`.", root)
	for _, want := range []string{"1 of the 2 path(s)", "  - internal/gone/x.go"} {
		if !strings.Contains(got, want) {
			t.Errorf("drift note lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "- internal/kept") {
		t.Errorf("drift note names a path that exists:\n%s", got)
	}
}
