package verify

import (
	"strings"
	"testing"
)

func TestExitCodeReport(t *testing.T) {
	cases := []struct {
		name       string
		exit       int
		wantStatus Status
		wantPassed int
		wantFailed int
	}{
		{"zero exit is one passing test", 0, StatusPassed, 1, 0},
		{"non-zero exit is one failing test", 1, StatusFailed, 0, 1},
		{"setup-error exit is a failure too", 2, StatusFailed, 0, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rpt := ExitCodeReport("verify.sh", tc.exit)
			if got := rpt.Results.Summary.Tests; got != 1 {
				t.Fatalf("tests = %d, want exactly 1", got)
			}
			if got := rpt.Results.Tests[0].Status; got != tc.wantStatus {
				t.Errorf("status = %q, want %q", got, tc.wantStatus)
			}
			if rpt.Results.Summary.Passed != tc.wantPassed || rpt.Results.Summary.Failed != tc.wantFailed {
				t.Errorf("summary = %+v, want passed=%d failed=%d",
					rpt.Results.Summary, tc.wantPassed, tc.wantFailed)
			}
		})
	}
}

// A suite that reports nothing must not normalize to a zero-test report: the
// runner reads zero tests as a failure, so a PASSING script with no result
// stream would be reported as failed.
func TestExitCodeReport_NeverZeroTests(t *testing.T) {
	if got := ExitCodeReport("verify.sh", 0).Results.Summary.Tests; got == 0 {
		t.Error("a passing suite normalized to a zero-test report")
	}
}

func TestAppendExitFailure(t *testing.T) {
	t.Run("records an exit the stream did not account for", func(t *testing.T) {
		rpt, err := Normalize(FormatTAP, []byte("ok 1 - a\nok 2 - b\n"))
		if err != nil {
			t.Fatalf("Normalize: %v", err)
		}
		AppendExitFailure(rpt, "verify.sh", 2)
		if rpt.Results.Summary.Failed != 1 {
			t.Fatalf("failed = %d, want 1: %+v", rpt.Results.Summary.Failed, rpt.Results.Summary)
		}
		if got := rpt.Results.Tests[2].Name; !strings.Contains(got, "exited 2") {
			t.Errorf("appended test name = %q, want it to name the exit code", got)
		}
	})

	t.Run("no-op on a clean exit", func(t *testing.T) {
		rpt, _ := Normalize(FormatTAP, []byte("ok 1 - a\n"))
		AppendExitFailure(rpt, "verify.sh", 0)
		if rpt.Results.Summary.Tests != 1 {
			t.Errorf("tests = %d, want 1 — nothing to append on a clean exit", rpt.Results.Summary.Tests)
		}
	})

	t.Run("no-op when the stream already reported a failure", func(t *testing.T) {
		rpt, _ := Normalize(FormatTAP, []byte("not ok 1 - a\n"))
		AppendExitFailure(rpt, "verify.sh", 1)
		if rpt.Results.Summary.Tests != 1 {
			t.Errorf("tests = %d, want 1 — the failure is already reported", rpt.Results.Summary.Tests)
		}
	})
}
