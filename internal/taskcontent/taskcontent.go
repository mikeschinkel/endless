// Package taskcontent defines the Name Go enum: the closed set of content kinds
// a task can carry, each stored as one row of the task_content table (E-1531).
//
// task_content.name is a TEXT column holding Slug(), the way tasks.status holds
// a taskstatus slug — so, like taskstatus and unlike tasktype, there is no
// mirror table and no VerifyIntegrity. The enum is the vocabulary; the column
// stores its tokens.
//
// THE CONVENTION, which is what is settled — not the list. A content kind is one
// lowercase single-token name, and that one token is used everywhere the kind
// appears: task_content.name, the CLI flag (`--plan`), the document mirror's
// file stem (`plan.md`), and the Agent Folio Format `Name` header. No mapping
// between vocabularies exists, so none can drift.
//
// Adding a kind is adding a constant here, in display order. Nothing else in
// the schema changes: a new name is an INSERT, not a migration. What does not
// follow for free is POLICY — which task types a kind is offered on, what the
// surface word is — and that is deliberately not decided here (E-1991).
//
// The package lives outside internal/events and internal/monitor so both can
// depend on it without a cycle.
package taskcontent

import (
	"fmt"
	"strings"
)

// Name is one content kind a task can carry.
type Name int

// The constants are declared in DISPLAY order, and All returns them that way:
// `task show` renders a task's content sections in this order. Analysis
// precedes Plan because it is the pre-plan design content (E-999); Outcome is
// the deliverable and Reason why the task ended, so they follow the work they
// describe.
const (
	Analysis Name = iota + 1
	Plan
	Outcome
	Reason
	Notes
)

// all is the canonical set in display order.
var all = []Name{Analysis, Plan, Outcome, Reason, Notes}

// slugs is the stored token per kind: task_content.name, the CLI flag, and the
// mirror file stem.
var slugs = map[Name]string{
	Analysis: "analysis",
	Plan:     "plan",
	Outcome:  "outcome",
	Reason:   "reason",
	Notes:    "notes",
}

// labels is the human display string per kind — the heading a task's section
// renders under.
var labels = map[Name]string{
	Analysis: "Analysis",
	Plan:     "Plan",
	Outcome:  "Outcome",
	Reason:   "Reason",
	Notes:    "Notes",
}

// String returns the human label ("Plan"), the heading a section renders
// under. Use Slug for the stored token.
func (n Name) String() string {
	if l, ok := labels[n]; ok {
		return l
	}
	return fmt.Sprintf("Name(%d)", int(n))
}

// Slug returns the stored token ("plan"): task_content.name, the CLI flag and
// the mirror file stem.
func (n Name) Slug() string {
	return slugs[n]
}

// Parse resolves a stored token to its Name. The error names every valid token.
func Parse(s string) (Name, error) {
	for _, n := range all {
		if slugs[n] == s {
			return n, nil
		}
	}
	return 0, fmt.Errorf("taskcontent: invalid content name %q (valid: %s)",
		s, strings.Join(Slugs(), ", "))
}

// All returns every kind in display order.
func All() []Name {
	return append([]Name(nil), all...)
}

// Slugs returns every stored token in display order.
func Slugs() []string {
	out := make([]string, len(all))
	for i, n := range all {
		out[i] = slugs[n]
	}
	return out
}
