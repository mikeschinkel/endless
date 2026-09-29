// White-box test for the plan-attach promotion's source predicate (E-1845).
// The predicate is small but load-bearing on both executor paths: at creation
// (`task add --plan`) and on update (`task update --plan`). Getting its set
// wrong either strands planned tasks in a pre-judgment status or silently
// overrides a deliberate one.
package events

import "testing"

func TestIsPreJudgmentStatus(t *testing.T) {
	cases := map[string]bool{
		// Attaching a plan is what answers the open question, so it promotes.
		// `unplanned` is where `task add` files a task with no plan (E-1993).
		"unplanned": true,

		// Already carries a judgment — a plan attachment must not re-decide it.
		"submitted": false,
		"ready":     false,
		"underway":  false,
		"revisit":   false,

		// Post-implementation: a plan attached here records what shipped, and
		// nothing infers a status from it (E-2120 removed E-1762's auto-revisit;
		// reopening is the explicit `--status revisit`, and it is the user's).
		"unverified": false,
		"confirmed":  false,
		"assumed":    false,
		"completed":  false,

		// Deliberate decisions that attaching a plan must never silently undo.
		"declined": false,
		"obsolete": false,
		"blocked":  false,

		// Removed by E-1993; not a status, so never pre-judgment.
		"untriaged": false,
	}
	for status, want := range cases {
		t.Run(status, func(t *testing.T) {
			if got := isPreJudgmentStatus(status); got != want {
				t.Errorf("isPreJudgmentStatus(%q) = %v, want %v", status, got, want)
			}
		})
	}
}
