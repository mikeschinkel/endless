package verifycmd

import (
	"io"
	"os"
	"strings"
	"testing"
)

// E-2243: each run writes its own report, so a later run never overwrites the
// failed report a person is diagnosing from.
func TestRun_EachRunWritesItsOwnReport(t *testing.T) {
	enterScriptSuite(t, "E-11", "#!/usr/bin/env bash\nexit 1\n")

	for i := 0; i < 2; i++ {
		if _, err := run("E-11", false); err != nil {
			t.Fatalf("run %d: %v", i+1, err)
		}
	}

	reports := cachedReports(t, "E-11")
	if len(reports) != 2 {
		t.Fatalf("got %d reports after two runs, want 2: %v", len(reports), reports)
	}
	for _, r := range reports {
		if !strings.HasSuffix(r, ReportFileSuffix) {
			t.Errorf("report %q does not end in %s", r, ReportFileSuffix)
		}
	}
}

// E-2243: `CTRF:` names where a run's report ended up. A failed run's report
// stays in the cache, so the runner names it; a passed run's report is about to
// be moved by `endless task verify`, which names where it lands, so the runner
// prints nothing that would point at a file about to be gone.
func TestRun_CTRFLineOnlyOnAFailingRun(t *testing.T) {
	cases := []struct {
		name string
		body string
		want bool
	}{
		{"passing run prints no CTRF line", "#!/usr/bin/env bash\nexit 0\n", false},
		{"failing run names its cache report", "#!/usr/bin/env bash\nexit 1\n", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			enterScriptSuite(t, "E-12", tc.body)
			out := captureStdout(t, func() {
				if _, err := run("E-12", false); err != nil {
					t.Fatalf("run: %v", err)
				}
			})
			want := 0
			if tc.want {
				want = 1
			}
			if got := strings.Count(out, "CTRF: "); got != want {
				t.Fatalf("CTRF lines = %d, want %d; output:\n%s", got, want, out)
			}
			if tc.want && !strings.Contains(out, ReportFileSuffix) {
				t.Errorf("CTRF line does not name the report file:\n%s", out)
			}
		})
	}
}

// captureStdout runs fn with os.Stdout redirected and returns what it printed.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	orig := os.Stdout
	os.Stdout = w
	done := make(chan string)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	defer func() { os.Stdout = orig }()
	fn()
	_ = w.Close()
	os.Stdout = orig
	return <-done
}
