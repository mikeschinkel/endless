package events

import (
	"fmt"

	"github.com/mikeschinkel/endless/internal/refusal"
)

// ValidPhases is the closed set of recognized task-phase values.
// Anything outside this set is rejected at the events boundary so it
// cannot enter the event log or projection.
var ValidPhases = map[string]bool{
	"urgent": true,
	"now":    true,
	"next":   true,
	"later":  true,
	"maybe":  true,
}

// ValidatePhase returns an error if s is not a recognized phase value.
// Empty string is rejected.
func ValidatePhase(s string) error {
	if !ValidPhases[s] {
		return refusal.NoReport(
			fmt.Sprintf("events: invalid phase %q (valid: urgent, now, next, later, maybe)", s),
			"Pick one of the listed phases and retry")
	}
	return nil
}

// ValidateMaybeParentless rejects a task that is both maybe-phase and
// parented. maybe = uncommitted; parent-child = scope binding, and a parent
// with maybe-phase children has phantom scope — it appears done while
// uncommitted commitments linger inside. Callers pass the effective phase and
// parent (the values the write would produce), so one check covers create,
// field-update, and move. A nil parentID (root task) is always allowed.
func ValidateMaybeParentless(phase string, parentID *int64) error {
	if phase == "maybe" && parentID != nil {
		// The remedy names the relates_to half only. Promoting the task is the
		// other way out, and it raises the user's commitment level for work they
		// filed as uncommitted — not a change an agent makes on its own.
		return refusal.NoReport(
			"events: a maybe-phase task cannot have a parent "+
				"(maybe = uncommitted, parent-child = scope binding); promote it "+
				"or use a relates_to relation instead",
			"Link it with a relates_to relation instead of --parent, and retry")
	}
	return nil
}
