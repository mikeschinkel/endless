package verify

import (
	"fmt"
)

// ExitCodeReport builds the one-test CTRF report for a suite that ran to
// completion but emitted no normalizable result stream — the exit code is the
// only outcome it reported, so the report says exactly that and no more.
//
// It is the floor beneath every richer normalizer, not a competitor to them: a
// suite that emits TAP gets per-assertion results, and a suite that emits
// nothing still lands in the same CTRF envelope with the same pass/fail
// semantics instead of being reported as "0 tests" (which the runner reads as a
// failure and which tells the reader nothing).
func ExitCodeReport(name string, exit int) (rpt *Report) {
	var t Test

	t = Test{Name: name, Status: StatusPassed}
	if exit != 0 {
		t.Status = StatusFailed
		t.Message = fmt.Sprintf("exited %d", exit)
	}
	rpt = &Report{}
	rpt.Results.Tool.Name = "exit-code"
	rpt.Results.Tests = []Test{t}
	rpt.Results.Summary = newSummary(rpt.Results.Tests)
	return rpt
}

// AppendExitFailure records a non-zero exit that the suite's own result stream
// did not account for. A suite that emits TAP and then dies in teardown — or
// aborts a section after reporting some passes — has a true exit code and a
// report that does not mention it, and a report that disagrees with the exit
// code is worse than either alone. Appending is deliberate: the reported
// assertions are still true, they are simply not the whole story.
//
// A no-op when exit is zero or the report already carries a failure, so the
// common cases add nothing.
func AppendExitFailure(rpt *Report, name string, exit int) {
	if rpt == nil || exit == 0 || rpt.Results.Summary.Failed > 0 {
		return
	}
	rpt.Results.Tests = append(rpt.Results.Tests, Test{
		Name:    fmt.Sprintf("%s exited %d", name, exit),
		Status:  StatusFailed,
		Message: "the suite reported no failure, but its exit code is non-zero",
	})
	rpt.Results.Summary = newSummary(rpt.Results.Tests)
}
