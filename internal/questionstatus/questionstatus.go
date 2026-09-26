// Package questionstatus is the vocabulary of task_questions (E-2176): the
// statuses a question can hold, which moves between them are legal, and who
// may be recorded as having answered one.
//
// A leaf package, importing nothing from Endless, so both the event executor
// (which enforces it) and monitor (which reads it) can depend on it without a
// cycle — events already imports monitor.
package questionstatus

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// Status is one task_questions.status value.
type Status = string

const (
	// Open is the only status a question is asked into, and the only one that
	// parks a task (E-1993).
	Open Status = "open"
	// Answered carries an answer and names who gave it.
	Answered Status = "answered"
	// Withdrawn is the asker retracting the question.
	Withdrawn Status = "withdrawn"
	// Invalid is the user rejecting the question's premise instead of
	// answering it.
	Invalid Status = "invalid"
	// Superseded is a question rolled into a later series. It is reachable from
	// Answered as well as Open: answers get folded into an updated plan, and
	// without a terminal state distinct from Answered the old series would keep
	// reading as live.
	Superseded Status = "superseded"
)

// All lists every status in lifecycle order.
var All = []Status{Open, Answered, Withdrawn, Invalid, Superseded}

// transitions is the whole lifecycle: the statuses each status may move to.
// Anything absent is refused. Re-answering an answered question is absent on
// purpose — a changed answer is a new question in a new series, so the record of
// what was first decided survives.
var transitions = map[Status][]Status{
	Open:     {Answered, Withdrawn, Invalid, Superseded},
	Answered: {Superseded},
}

// Validate reports whether s is a known status.
func Validate(s string) error {
	if slices.Contains(All, s) {
		return nil
	}
	return fmt.Errorf("unknown question status %q (want one of %s)",
		s, strings.Join(All, ", "))
}

// CanMove reports whether a question in status from may move to status to.
func CanMove(from, to Status) bool {
	return slices.Contains(transitions[from], to)
}

// From lists the statuses a question may leave to reach to — the guard a
// resolving UPDATE puts in its WHERE clause.
func From(to Status) []Status {
	var out []Status
	for _, from := range All {
		if CanMove(from, to) {
			out = append(out, from)
		}
	}
	return out
}

// UserAnswerer is the answered_by value for a person.
const UserAnswerer = "user"

var sessionAnswererRe = regexp.MustCompile(`^ES-[1-9][0-9]*$`)

// ValidateAnsweredBy accepts `user` or a peer session's `ES-<n>`.
func ValidateAnsweredBy(s string) error {
	if s == UserAnswerer || sessionAnswererRe.MatchString(s) {
		return nil
	}
	return fmt.Errorf("answered_by %q must be %q or a session id like ES-123",
		s, UserAnswerer)
}
