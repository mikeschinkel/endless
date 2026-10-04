package projectstatuscmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mikeschinkel/endless/internal/monitor"
)

func writeProjectConfig(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := os.MkdirAll(filepath.Join(dir, ".endless"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".endless", "config.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestListStylesFromConfig: a configured list overrides its default, an
// unconfigured one keeps it, -1 removes a background, and an out-of-range
// index is ignored rather than rendered.
func TestListStylesFromConfig(t *testing.T) {
	dir := writeProjectConfig(t, `{"project_status": {"colors": {
		"urgent": {"bg": 196},
		"other":  {"bg": -1, "fg": 999}
	}}}`)
	st := listStylesFor(dir)
	if st[listUrgent].bg != 196 || st[listUrgent].fg != 232 {
		t.Errorf("urgent = %+v, want bg 196 over the default fg 232", st[listUrgent])
	}
	if st[listEpics] != defaultListStyles[listEpics] {
		t.Errorf("epics = %+v, want the default", st[listEpics])
	}
	if st[listOther].bg != -1 || st[listOther].fg != 232 {
		t.Errorf("other = %+v, want no background and the default fg", st[listOther])
	}

	got := st.colorize("x", monitor.ProjectStatusRow{Phase: "now"}, listOther, 10, true)
	if strings.Contains(got, "48;5;") || strings.Contains(got, "x ") {
		t.Errorf("a list with bg -1 still painted or padded a background: %q", got)
	}
}

func TestListStylesWithoutConfig(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if st := listStylesFor(t.TempDir()); *st != defaultListStyles {
		t.Errorf("no config: %+v, want the defaults", *st)
	}
}
