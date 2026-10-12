// Package planitem is the vocabulary of itemized plans and their
// reconciliation (E-2280): what a plan item is, which verdicts a reconciliation
// entry may give it, the kinds a discrepancy question is filed under and their
// rank, and which task types and statuses the rules apply to.
//
// A leaf package, importing nothing from Endless, so the event executor (which
// enforces it), monitor (which reads it) and the gate can all depend on it
// without a cycle — the same reason internal/questionstatus is one.
package planitem

import (
	"fmt"
	"slices"
	"strings"
)

// Item kinds. A deliverable states something the work will make true; an
// exclusion states something the task will not do or change — an "out of
// scope" line made checkable.
const (
	KindDeliverable = "deliverable"
	KindExclusion   = "exclusion"
)

// Kinds lists every item kind.
var Kinds = []string{KindDeliverable, KindExclusion}

// Origins. An item is `grown` when it is added while the task is underway and
// the task already had items; every other item is `planned`.
const (
	OriginPlanned = "planned"
	OriginGrown   = "grown"
)

// States. A withdrawn item stays as a row so its number is never reused.
const (
	StateActive    = "active"
	StateWithdrawn = "withdrawn"
)

// Verdicts a reconciliation entry gives an item.
const (
	VerdictAsPlanned   = "as_planned"
	VerdictDifferently = "differently"
	VerdictNotDone     = "not_done"
	VerdictBlocked     = "blocked"
	VerdictKept        = "kept"
	VerdictNotKept     = "not_kept"
)

// verdictsByKind is which verdicts fit which item kind.
var verdictsByKind = map[string][]string{
	KindDeliverable: {VerdictAsPlanned, VerdictDifferently, VerdictNotDone, VerdictBlocked},
	KindExclusion:   {VerdictKept, VerdictNotKept},
}

// Question kinds, ranked most to least concerning. A discrepancy is filed as an
// open question carrying one of these, so `question list` can sort by rank.
const (
	QuestionExcluded  = "excluded"
	QuestionLess      = "less"
	QuestionBlocked   = "blocked"
	QuestionDifferent = "different"
	QuestionMore      = "more"
)

// QuestionKinds lists the kinds in rank order, most concerning first.
var QuestionKinds = []string{QuestionExcluded, QuestionLess, QuestionBlocked, QuestionDifferent, QuestionMore}

// ItemizedTypes are the task type slugs whose plans must be itemized: a task of
// one cannot be approved, spawned or set to `unverified` without an active
// deliverable item. Any task that has plan items is gated on a current
// reconciliation, whatever its type; these types are the ones that must have
// them. One place: adding the docs type (E-2147) is one entry here.
var ItemizedTypes = []string{"todo", "bugfix"}

// MustItemize reports whether tasks of this type slug must have plan items.
func MustItemize(slug string) bool { return slices.Contains(ItemizedTypes, slug) }

// FrozenStatuses are the task statuses in which items and reconciliations can
// no longer change: from `unverified` on. `revisit` unfreezes them.
var FrozenStatuses = []string{
	"unverified", "unlanded", "confirmed", "assumed", "completed",
	"declined", "obsolete", "superseded",
}

// IsFrozen reports whether a task in this status has frozen items.
func IsFrozen(status string) bool { return slices.Contains(FrozenStatuses, status) }

// GrowsItems reports whether an item added to a task in this status, which
// already has items, is grown rather than planned.
func GrowsItems(status string) bool { return status == "underway" }

// ValidateKind reports whether k is a known item kind.
func ValidateKind(k string) error {
	if slices.Contains(Kinds, k) {
		return nil
	}
	return fmt.Errorf("unknown plan item kind %q (want one of %s)", k, strings.Join(Kinds, ", "))
}

// ValidateVerdict reports whether verdict fits an item of kind.
func ValidateVerdict(kind, verdict string) error {
	allowed := verdictsByKind[kind]
	if slices.Contains(allowed, verdict) {
		return nil
	}
	return fmt.Errorf("verdict %q does not fit a %s item (want one of %s)",
		verdict, kind, strings.Join(allowed, ", "))
}

// IsDiscrepancy reports whether a verdict is a discrepancy: anything other than
// as planned or kept.
func IsDiscrepancy(verdict string) bool {
	return verdict != VerdictAsPlanned && verdict != VerdictKept
}

// QuestionKind returns the question kind a discrepancy verdict is filed under,
// or "" for a verdict that is not a discrepancy. A grown item that is otherwise
// as planned is filed as QuestionMore by the caller, which knows its origin.
func QuestionKind(verdict string) string {
	switch verdict {
	case VerdictNotKept:
		return QuestionExcluded
	case VerdictNotDone:
		return QuestionLess
	case VerdictBlocked:
		return QuestionBlocked
	case VerdictDifferently:
		return QuestionDifferent
	}
	return ""
}

// ValidateQuestionKind reports whether k is a known question kind. Empty is
// allowed: an ordinary question asked with `endless question ask` has none.
func ValidateQuestionKind(k string) error {
	if k == "" || slices.Contains(QuestionKinds, k) {
		return nil
	}
	return fmt.Errorf("unknown question kind %q (want one of %s)", k, strings.Join(QuestionKinds, ", "))
}

// Label renders an item number: P3 inside its task.
func Label(seq int64) string { return fmt.Sprintf("P%d", seq) }

// ParseLabel reads "P3", "p3", "3" or "E-2280.P3" into 3.
func ParseLabel(s string) (int64, error) {
	t := strings.TrimSpace(s)
	if i := strings.LastIndex(t, "."); i >= 0 {
		t = t[i+1:]
	}
	t = strings.TrimPrefix(strings.TrimPrefix(t, "P"), "p")
	var n int64
	if _, err := fmt.Sscanf(t, "%d", &n); err != nil || n <= 0 || fmt.Sprint(n) != t {
		return 0, fmt.Errorf("not a plan item number: %q (want P<n>)", s)
	}
	return n, nil
}
