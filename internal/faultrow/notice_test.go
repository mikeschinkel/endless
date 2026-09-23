package faultrow

import (
	"database/sql"
	"os"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/mikeschinkel/endless/internal/faults"
)

// The unindexed notice (E-1887).
//
// The fault row reads the `errors` table. A fault raised BECAUSE that table
// could not be written reaches no row, so the row that exists to say "something
// is wrong" is structurally blind to the worst case it has. These tests hold
// the one line that closes that.

// bindBrokenStore binds a fault store whose log is writable and whose database
// is not — the state the notice is for.
func bindBrokenStore(t *testing.T) {
	t.Helper()

	logDir := t.TempDir()
	faults.Bind(
		func() (*sql.DB, error) { return nil, os.ErrPermission },
		func() string { return logDir },
		nil,
	)
	t.Cleanup(func() { faults.Bind(nil, nil, nil) })
}

func TestRender_NoticeWhenTheStoreCannotBeRead(t *testing.T) {
	bindBrokenStore(t)

	var out strings.Builder
	Render(&out, 100, false, faults.AllProjects)

	line := strings.TrimRight(out.String(), "\n")
	if line == "" {
		t.Fatal("nothing rendered; an unreadable fault record must not be indistinguishable from a clean one")
	}
	if !strings.Contains(line, "could not be read") {
		t.Errorf("line = %q, want it to say the record could not be read", line)
	}
	if !strings.Contains(line, Hint) {
		t.Errorf("line = %q, want the hint that points at where the rest is", line)
	}
	if strings.Count(out.String(), "\n") != 1 {
		t.Errorf("rendered %d lines, want 1 — a broken database must not take the pane over",
			strings.Count(out.String(), "\n"))
	}
}

func TestRender_NoticeWhenOccurrencesWentUnindexed(t *testing.T) {
	bindBrokenStore(t)

	faults.Record(faults.Fault{
		Code:    faults.ErrCodeHookWriteFailed,
		Source:  "hook:claude",
		Summary: "claude hook: touching session: database is locked",
	})

	var out strings.Builder
	Render(&out, 100, false, faults.AllProjects)

	line := out.String()
	if !strings.Contains(line, "1 in the log") {
		t.Errorf("line = %q, want the outstanding count", line)
	}
}

func TestRender_NoticeCountsUnindexedBesideAHealthyStore(t *testing.T) {
	db := bindFaultStore(t)
	_ = db

	// A store that reads fine now, holding an occurrence from when it did not.
	// The row above is not wrong, it is SHORT — and saying so is the whole job.
	logPath := faults.DetailLogPath()
	if logPath == "" {
		t.Fatal("no detail log path; the store is not bound")
	}
	if err := os.WriteFile(logPath,
		[]byte(`{"kind":"fault","fault_id":null,"unindexed":true,"code":"ERR-0015","summary":"orphan"}`+"\n"),
		0o644); err != nil {
		t.Fatalf("seed the log: %v", err)
	}

	var out strings.Builder
	Render(&out, 100, false, faults.AllProjects)

	line := out.String()
	if !strings.Contains(line, "not indexed") {
		t.Errorf("line = %q, want it to say an occurrence was never indexed", line)
	}
	if strings.Contains(line, "could not be read") {
		t.Errorf("line = %q, must not claim the record is unreadable when it reads fine", line)
	}
}

func TestRender_SilentWhenEverythingIsFine(t *testing.T) {
	bindFaultStore(t)

	var out strings.Builder
	Render(&out, 100, false, faults.AllProjects)

	if out.String() != "" {
		t.Errorf("rendered %q with a healthy, empty store; want nothing at all", out.String())
	}
}

func TestRender_SilentWhenTheStoreIsUnbound(t *testing.T) {
	faults.Bind(nil, nil, nil)

	var out strings.Builder
	Render(&out, 100, false, faults.AllProjects)

	if out.String() != "" {
		t.Errorf("rendered %q with an UNBOUND store; unbound is not unreadable — "+
			"a process that never wired diagnostics has no record to be missing", out.String())
	}
}

func TestRender_BothRowAndNoticeWhenBothApply(t *testing.T) {
	bindFaultStore(t)

	faults.Record(faults.Fault{
		Code:    faults.ErrCodeJobFailed,
		Source:  "job:evaluate",
		Summary: "job \"evaluate\" failed",
	})

	f, err := os.OpenFile(faults.DetailLogPath(), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open log: %v", err)
	}
	if _, err = f.WriteString(`{"kind":"fault","fault_id":null,"unindexed":true,"code":"ERR-0015","summary":"orphan"}` + "\n"); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err = f.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	var out strings.Builder
	Render(&out, 100, false, faults.AllProjects)

	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("rendered %d lines, want 2 (the incident row, then the notice):\n%s",
			len(lines), out.String())
	}
	if !strings.Contains(lines[0], "WARN-0001") {
		t.Errorf("first line = %q, want the incident row", lines[0])
	}
	if !strings.Contains(lines[1], "not indexed") {
		t.Errorf("second line = %q, want the notice", lines[1])
	}
}

func TestNoticeLine_NeverExceedsTheMargin(t *testing.T) {
	// Same invariant the fault row is held to: the printed width never exceeds
	// cols-1, at any width, in colour or out. A wrapped line mis-fits the pane
	// the monitor sized from the newline count.
	for cols := 1; cols <= 140; cols++ {
		for _, color := range []bool{false, true} {
			for _, unreadable := range []bool{false, true} {
				line := noticeLine(3, unreadable, cols, color)
				if width := printedWidth(line); width > cols-1 {
					t.Errorf("cols=%d color=%v unreadable=%v: width %d exceeds %d: %q",
						cols, color, unreadable, width, cols-1, line)
				}
				if strings.Contains(line, "\n") {
					t.Errorf("cols=%d: the notice wrapped", cols)
				}
			}
		}
	}
}

func TestNoticeText_PluralisesTheCount(t *testing.T) {
	if got := noticeText(1, false); !strings.Contains(got, "1 error ") {
		t.Errorf("noticeText(1) = %q, want a singular noun", got)
	}
	if got := noticeText(2, false); !strings.Contains(got, "2 errors ") {
		t.Errorf("noticeText(2) = %q, want a plural noun", got)
	}
}
