// Package taskstatus owns the task status vocabulary: the machine slugs, their
// human labels and semantic glyphs, and — the point of the package — every
// curated GROUPING of statuses the system reasons about.
//
// It is the status counterpart to tasktype/sessiontaskrelation, the
// other closed vocabularies that already have an owning package. Status was the
// one that never got it (E-1891): it lived as bare string literals across
// roughly twenty sites in two languages, in four shapes — copies of the whole
// vocabulary, policy subsets, lists buried inside SQL string literals, and
// per-status ordering data.
//
// The failure mode that motivated this is OMISSION, not typos. E-1648 added
// `submitted` and updated four of six whole-vocabulary sites. E-1845 added
// `untriaged` and had to hand-edit every site in all four shapes with nothing
// to catch a miss. The fix is structural rather than a lint rule: every
// grouping is a row in the ONE `groups` map below, so a status cannot be added
// without reading each grouping and deciding whether it belongs. The partition
// invariants in the test file turn the decisions that MUST be made into build
// failures rather than silent wrong answers.
//
// Unlike tasktype, status has no enum table and no integer id — `tasks.status`
// is a TEXT column — so there is no VerifyIntegrity here. Status is a plain
// string alias precisely because every DB read/write and SQL literal is already
// a string; the package adds no conversions at any boundary.
//
// Scope discipline (E-1891): relocating a subset must NOT change which statuses
// it contains, so this package landed as a pure relocation. The one exception
// is documented on Terminal, and it is the pattern working as intended:
// gathering the sets in one place made two of them legibly contradict each
// other, and the contradiction had a resolution that lost nothing.
package taskstatus

import (
	"fmt"
	"sort"
	"strings"
)

// Status is the machine slug stored in `tasks.status`. A plain alias, not a
// defined type: every DB read/write and every SQL literal is already a string,
// so callers need no conversion at the boundary.
type Status = string

// The closed vocabulary, in lifecycle order — the pre-work statuses, then the
// two sign-off gates, then the terminals, then `revisit` and the two
// abandonment states.
//
// The gates are one per lane and they are siblings, which is why they sit
// adjacent: `unverified` asks "does it work", `unreviewed` asks "has the owner
// read it" (E-2016). Neither is terminal, and both hold a dependent.
//
// `blocked` is NOT here (E-2018). It was a status once, and a handful of rows
// still carried it, but blockedness is the `blocked_by` relation and always
// has been: `endless task block` writes a relation and never touches status,
// and the guide's blocking-semantics table computes a dependent's fate from
// its blocker's own status. Shipping both mechanisms meant the duplicate had
// to go stale — a blocker reaching `confirmed` released the relation while
// `status=blocked` sat on the dependent until a human edited it. ED-1572
// proposed writing the rule down and was rejected because it needs no
// decision; what it needed was the data cleaned up, which it now is.
const (
	Untriaged  Status = "untriaged"
	Unplanned  Status = "unplanned"
	Submitted  Status = "submitted"
	Ready      Status = "ready"
	Underway   Status = "underway"
	Unverified Status = "unverified"
	Unreviewed Status = "unreviewed"
	Confirmed  Status = "confirmed"
	Assumed    Status = "assumed"
	Completed  Status = "completed"
	Revisit    Status = "revisit"
	Declined   Status = "declined"
	Obsolete   Status = "obsolete"
	Superseded Status = "superseded"
)

// Group names one curated set of statuses. Adding a Group means adding exactly
// one row to `groups`; adding a Status means revisiting every row.
type Group int

const (
	// All is the whole vocabulary in lifecycle order.
	All Group = iota

	// Actionable / NotActionable partition All: work `task next` may offer
	// versus work it must not. They are a partition (asserted in the tests) so
	// a new status cannot land in neither and silently pick a side.
	Actionable
	NotActionable

	// Active is what `task active` lists, in display order.
	Active

	// AwaitsUser is work that has stopped and is waiting on a PERSON, in the
	// order the project attention board ranks them (E-1976): a finished
	// implementation awaiting verification, a delivered outcome awaiting a read,
	// a plan awaiting approval.
	//
	// Deliberately not a slice of NotActionable, though every member is in it:
	// NotActionable answers "may `task next` offer this?", which is also true of
	// `underway` (someone else has it) and `untriaged` (nobody has looked). This
	// group answers a narrower question — "is the ball in the user's court?" —
	// and that is the whole basis on which the board decides a row is worth a
	// line.
	AwaitsUser

	// ClaimPromotes are the statuses `task claim` promotes to `underway` in
	// place. Its complement is `submitted` and `underway` (which the claim gate
	// refuses and the already-claimed case, respectively) plus Settled, which
	// needs --force.
	ClaimPromotes

	// Open is the "there is still work here" set backing the per-project task
	// context: everything from freshly filed through in-flight. Deliberately
	// narrower than !Terminal — `revisit` is not offered as work to pick up.
	Open

	// ChildrenStateOrder is the display order of the non-terminal buckets in an
	// epic's children-state breakdown; the collapsed `terminal` bucket is
	// appended by the renderer. ChildrenStateOrder and Terminal partition All,
	// which is what makes the breakdown's "(N total)" suffix always reconcile.
	ChildrenStateOrder

	// DerivationPrecedence is the epic status-derivation ladder as ordered
	// data: the first member present among an epic's children wins. The
	// algorithm stays in internal/events/epic_derivation.go; only the ordering
	// — the part that must change when a status is added — lives here.
	DerivationPrecedence

	// DescriptionResetFrom are the pre-work statuses from which a material
	// description edit resets a task to `untriaged`. `underway` is deliberately
	// excluded so an edit cannot yank work out from under a live session.
	DescriptionResetFrom

	// PreJudgment means "nobody has decided this task is spec-complete yet" —
	// the statuses from which attaching a plan promotes to `submitted`.
	PreJudgment

	// ReopenRefused carry an explicit decision not to do the work. Reversing
	// one must be a deliberate `task update --status`, never a side effect of
	// a reopen/resume.
	ReopenRefused

	// Reopenable are the statuses `--reopen` / `--revisit` flip to `revisit`.
	Reopenable

	// SessionPending is the "Pending" bucket of the session-status task
	// rollup. With Terminal and Unverified it partitions All.
	SessionPending

	// SetsCompletedAt are the statuses whose arrival stamps
	// `tasks.completed_at`; every other status clears it.
	SetsCompletedAt

	// Settled means the work is over one way or another — shipped or
	// abandoned. Claiming one needs --force, and reaching one clears the tier.
	Settled

	// Shipped means the work reached the verification gate or passed it.
	// Deliberately NOT Terminal: `declined`/`obsolete` are terminal but never
	// shipped, and `unverified` shipped without being terminal. `obsolete` is
	// refused on these.
	Shipped

	// ShippedTerminal is Shipped intersected with Terminal: work that both
	// happened and is finished. It is the population for which "has this
	// task's code reached the base branch?" is a real question, which is what
	// `task unlanded` surveys and what makes `task show` state landedness
	// rather than imply it by omission (E-2095).
	//
	// Neither parent group answers that on its own, and the two exclusions are
	// for opposite reasons. `unverified`/`unreviewed` are Shipped but not
	// finished — their work is SUPPOSED to be sitting on a branch, so reporting
	// it as unlanded would flag the normal state of every task awaiting its
	// user. `declined`/`obsolete` are Terminal but never shipped — there is no
	// code to have landed, so "never" is the expected answer and carries no
	// information.
	ShippedTerminal

	// StickyOverride block epic status derivation: while an epic sits in one,
	// derivation reads its state and does nothing.
	StickyOverride

	// SubmittableFrom are the pre-approval design states `task submit` accepts.
	SubmittableFrom

	// Terminal is finished-or-abandoned work: the collapsed bucket in
	// children-state breakdowns, the rows hidden from listings without --all,
	// the statuses that route to a terminus rather than a verb, and the blocker
	// statuses that release a dependent.
	//
	// That last reading arrived late. E-1891 first relocated two DISAGREEING
	// blocker sets verbatim — `task next` released a dependent on `completed`
	// but kept holding it on `declined`/`obsolete`; the status line's
	// GetActiveBlockers did the exact opposite — on the rule that relocating a
	// subset must not change it. Naming them side by side is what made the
	// disagreement legible, and once legible it had one answer: Terminal is a
	// superset of both, so nothing that released a dependent stopped doing so,
	// and work someone explicitly declined stopped blocking, which is what
	// `endless guide`'s blocking-semantics table has always said. (That table
	// omits `completed` only because it predates E-1240 restoring it as a real
	// terminal status.)
	Terminal

	// VerificationTerminal are the two ways user-testable work finishes:
	// verified by the user, or believed done pending natural use. Together with
	// `unverified` they are VerificationTrack, which the tests assert.
	VerificationTerminal

	// VerificationTrack are the statuses that only user-testable work reaches.
	// research/epic/brainstorm tasks are refused them — they terminate via
	// `completed --outcome` instead.
	VerificationTrack

	// ReviewTrack is VerificationTrack's counterpart: the statuses only
	// findings work reaches, which todo and bugfix tasks are refused (E-2016).
	//
	// One member, and that asymmetry is real rather than an oversight.
	// `unverified` has two terminals of its own because verification has two
	// outcomes worth distinguishing — verified, or believed-done pending use.
	// Review has one: the owner read the outcome and accepted it, which is
	// `completed`. And `completed` cannot join this group, because it is NOT
	// exclusive to findings work — a todo-typed audit finishes there too, on
	// the strength of its title's lead verb rather than its type.
	ReviewTrack
)

// groups is the ONE map. Every grouping in the system is a row here, so adding
// a status forces a decision about each of them.
//
// Ordered groups (Active, ChildrenStateOrder, DerivationPrecedence, All) are
// written in their meaningful order and read back with Rank. Unordered groups
// are written in lifecycle order for readability only.
var groups = map[Group][]Status{
	All: {
		Untriaged, Unplanned, Submitted, Ready, Underway,
		Unverified, Unreviewed, Confirmed, Assumed, Completed,
		Revisit, Declined, Obsolete, Superseded,
	},
	Actionable:    {Unplanned, Ready, Revisit},
	NotActionable: {Untriaged, Submitted, Underway, Unverified, Unreviewed, Confirmed, Assumed, Completed, Declined, Obsolete, Superseded},
	Active:        {Underway, Unverified, Unreviewed},
	AwaitsUser:    {Unverified, Unreviewed, Submitted},
	ClaimPromotes: {Untriaged, Unplanned, Ready, Revisit},
	Open:          {Untriaged, Unplanned, Submitted, Ready, Underway},
	ChildrenStateOrder: {
		Untriaged, Unplanned, Submitted, Ready, Underway, Revisit, Unverified, Unreviewed,
	},
	DerivationPrecedence: {Underway, Ready, Submitted, Unplanned, Untriaged},
	DescriptionResetFrom: {Untriaged, Unplanned, Submitted, Ready, Revisit},
	PreJudgment:          {Untriaged, Unplanned},
	ReopenRefused:        {Declined, Obsolete, Superseded},
	Reopenable:           {Confirmed, Assumed, Completed},
	ReviewTrack:          {Unreviewed},
	SessionPending:       {Untriaged, Unplanned, Submitted, Ready, Underway, Revisit},
	SetsCompletedAt:      {Confirmed, Completed},
	Settled:              {Unverified, Unreviewed, Confirmed, Assumed, Completed, Declined, Obsolete, Superseded},
	Shipped:              {Unverified, Unreviewed, Confirmed, Assumed, Completed},
	ShippedTerminal:      {Confirmed, Assumed, Completed},
	StickyOverride:       {Revisit, Declined, Obsolete, Superseded},
	SubmittableFrom:      {Untriaged, Unplanned, Revisit},
	Terminal:             {Confirmed, Assumed, Completed, Declined, Obsolete, Superseded},
	VerificationTerminal: {Confirmed, Assumed},
	VerificationTrack:    {Unverified, Confirmed, Assumed},
}

// groupSlugs is the CLI-facing name of each Group. The Python client passes
// these through verbatim and holds no group list of its own, so adding a group
// here needs no client change.
var groupSlugs = map[Group]string{
	All:                  "all",
	Actionable:           "actionable",
	NotActionable:        "not-actionable",
	Active:               "active",
	AwaitsUser:           "awaits-user",
	ClaimPromotes:        "claim-promotes",
	Open:                 "open",
	ChildrenStateOrder:   "children-state-order",
	DerivationPrecedence: "derivation-precedence",
	DescriptionResetFrom: "description-reset-from",
	PreJudgment:          "pre-judgment",
	ReopenRefused:        "reopen-refused",
	Reopenable:           "reopenable",
	ReviewTrack:          "review-track",
	SessionPending:       "session-pending",
	SetsCompletedAt:      "sets-completed-at",
	Settled:              "settled",
	Shipped:              "shipped",
	ShippedTerminal:      "shipped-terminal",
	StickyOverride:       "sticky-override",
	SubmittableFrom:      "submittable-from",
	Terminal:             "terminal",
	VerificationTerminal: "verification-terminal",
	VerificationTrack:    "verification-track",
}

// labels is the human display string per status, mirroring tasktype.Label().
var labels = map[Status]string{
	Untriaged:  "Untriaged",
	Unplanned:  "Unplanned",
	Submitted:  "Submitted",
	Ready:      "Ready",
	Underway:   "Underway",
	Unverified: "Unverified",
	Unreviewed: "Unreviewed",
	Confirmed:  "Confirmed",
	Assumed:    "Assumed",
	Completed:  "Completed",
	Revisit:    "Revisit",
	Declined:   "Declined",
	Obsolete:   "Obsolete",
	Superseded: "Superseded",
}

// glyphs is the SEMANTIC glyph per status — no color, no medium-specific
// styling. A surface maps its own palette onto these; `click.style(fg=...)` and
// a CSS class are not shareable, the shape is.
//
// The eight that had a precedent keep it: ◌ ○ ● ◉ ◆ ? come from the status
// indicator map E-1899 deleted with `task list --tree`, and ⚑ (submitted) and
// ☑ (unverified) from the session-status action legend, where they already
// mean review-this and verify-this. The remaining three follow the same logic:
// ✔ is the heavier check for verified-done against ✓ for believed-done, and
// ⊘/⊗ read as "chose not to" / "made irrelevant", and ⊜ (superseded) is the
// third in that family: the same ringed mark, but an equals sign — something
// else now stands in its place.
//
// ☐ (unreviewed) is the deliberate pair to ☑ (unverified): the same box, not
// yet ticked. The two gates are siblings — one asks "does it work", the other
// "has the owner read it" — and the glyphs say so at a glance (E-2016).
var glyphs = map[Status]string{
	Untriaged:  "◌",
	Unplanned:  "○",
	Submitted:  "⚑",
	Ready:      "●",
	Underway:   "◉",
	Unverified: "☑",
	Unreviewed: "☐",
	Confirmed:  "✔",
	Assumed:    "✓",
	Completed:  "◆",
	Revisit:    "?",
	Declined:   "⊘",
	Obsolete:   "⊗",
	Superseded: "⊜",
}

// NoRank is what Rank returns for a status outside the group.
const NoRank = -1

// Get returns the members of g in the group's own order. The result is a
// defensive copy: callers may sort or append to it without corrupting the
// registry.
func Get(g Group) []Status {
	members, ok := groups[g]
	if !ok {
		return nil
	}
	out := make([]Status, len(members))
	copy(out, members)
	return out
}

// Has reports whether s is a member of g.
func Has(g Group, s Status) bool {
	for _, member := range groups[g] {
		if member == s {
			return true
		}
	}
	return false
}

// SQLList renders g for a SQL IN/NOT IN clause: `'untriaged','unplanned'`.
//
// This is the highest-value accessor in the package. A status list inside a SQL
// string literal is invisible to every tool — no compiler, no linter, no search
// for a Go identifier reaches inside it — so those literals rot longest and
// most silently. Statuses cannot contain a quote (the vocabulary is closed and
// defined above), so no escaping is required.
func SQLList(g Group) string {
	members := groups[g]
	quoted := make([]string, len(members))
	for i, member := range members {
		quoted[i] = "'" + member + "'"
	}
	return strings.Join(quoted, ",")
}

// Rank returns the index of s within an ordered group, or NoRank when s is not
// a member. Used where the ordering itself is the rule — the epic-derivation
// ladder, the children-state display order, the `task active` sort.
func Rank(g Group, s Status) int {
	for i, member := range groups[g] {
		if member == s {
			return i
		}
	}
	return NoRank
}

// Label returns the human display string for s, or "" for an unknown status.
// Mirrors tasktype.Label().
func Label(s Status) string {
	return labels[s]
}

// Glyph returns the semantic glyph for s, or "" for an unknown status.
func Glyph(s Status) string {
	return glyphs[s]
}

// Valid reports whether s is a member of the vocabulary.
func Valid(s Status) bool {
	return Has(All, s)
}

// Validate returns an error naming the vocabulary when s is not a status.
func Validate(s Status) error {
	if Valid(s) {
		return nil
	}
	return fmt.Errorf("taskstatus: invalid status %q (valid: %s)",
		s, strings.Join(groups[All], ", "))
}

// GroupSlug returns the CLI-facing name of g, or "" when g is not a group.
func GroupSlug(g Group) string {
	return groupSlugs[g]
}

// ParseGroup resolves a CLI-facing group name. The error names every valid
// group so an agent that guessed wrong is told the whole set in one round trip.
func ParseGroup(slug string) (Group, error) {
	for g, s := range groupSlugs {
		if s == slug {
			return g, nil
		}
	}
	return 0, fmt.Errorf("taskstatus: unknown group %q (valid: %s)",
		slug, strings.Join(AllGroupSlugs(), ", "))
}

// AllGroupSlugs returns every group name, sorted. Used by ParseGroup's error
// and by the subcommand's usage text, so neither can drift from the registry.
func AllGroupSlugs() []string {
	out := make([]string, 0, len(groupSlugs))
	for _, s := range groupSlugs {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// AllGroups returns every Group in declaration order. Used by the structural
// tests, which must enumerate the registry rather than list groups by hand.
func AllGroups() []Group {
	out := make([]Group, 0, len(groups))
	for g := range groups {
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
