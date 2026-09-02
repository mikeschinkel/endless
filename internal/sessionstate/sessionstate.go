// Package sessionstate owns the session state vocabulary: the machine slugs
// stored in `sessions.state`, their human labels and semantic glyphs, and every
// curated GROUPING of states the system reasons about.
//
// It is the session-lifecycle counterpart to internal/taskstatus, built to the
// same shape for the same reason (E-2105). Task status got an owning package in
// E-1891; session state did not, and it carried the identical defect at three
// and a half times the scale — roughly seventy non-test sites across Go, Python
// and SQL, in the same four shapes: copies of the whole vocabulary, policy
// subsets, lists buried inside SQL string literals, and per-state ordering data.
//
// The failure mode is OMISSION, and here it has a name. Building E-1976's
// follow-up, a session proposed routing Claude Code's permission-prompt
// Notification to `needs_input` on the evidence that no live transition writes
// that state. A grep supported it; the code refuted it. `hookcmd.sessionMayWrite`
// refuses file writes from a `needs_input` session under `enforce` tracking, so
// the change would have blocked the session's next write after the user
// answered. The gate was a Go `switch` with a silent `default: return false` —
// a new state joined the refused set by default and said nothing. That is the
// structural hole MayWrite closes: the gate now reads a named group, so a state
// cannot be added without someone classifying it.
//
// State is a plain string alias, not a defined type, for taskstatus's reason:
// `sessions.state` is a TEXT column and every SQL literal is already a string,
// so the package adds no conversion at any boundary.
//
// Scope discipline (E-2105): this landed as a pure relocation. No state was
// added, no group's membership differs from the predicate it replaced, and
// `session list` renders byte-for-byte what it rendered before. Where a group's
// membership reads oddly — AwaitsHuman including `idle` — the rule is preserved
// exactly as found and the reading is left to the task that changes it.
package sessionstate

import (
	"fmt"
	"sort"
	"strings"
)

// State is the machine slug stored in `sessions.state`. A plain alias, not a
// defined type: every DB read/write and every SQL literal is already a string,
// so callers need no conversion at the boundary.
type State = string

// The closed vocabulary, in lifecycle order — the two states a live session
// alternates between, the state that means it is waiting on a person, and the
// terminal.
//
// Four members, and they have been four for the whole life of the table.
// Twelve session-related schema changes exist and not one touches this value
// set: sessions have churned through short ids, nullable session_id, active
// epic, kind, gates added and dropped, transcript path, recap columns — and
// these four held throughout. That stability is why this package carries a
// transition TABLE but no generated diagram; see transitions.go.
const (
	Working    State = "working"
	Idle       State = "idle"
	NeedsInput State = "needs_input"
	Ended      State = "ended"
)

// Group names one curated set of states. Adding a Group means adding exactly
// one row to `groups`; adding a State means revisiting every row.
type Group int

const (
	// All is the whole vocabulary in lifecycle order.
	All Group = iota

	// Live is every state a session can still act from — the members list, NOT
	// a negation, and that is the whole point of the group.
	//
	// `state != 'ended'` was the single most common thing anything asked about a
	// session: twenty-nine sites in Go, Python and SQL. Written that way, a
	// state added later silently inherits "live" because nobody wrote it down
	// anywhere. Written as a membership list, adding a state is a decision
	// somebody has to take here, in the open.
	Live

	// MayWrite is the declaration gate's admission set: the states a session
	// holding a task may write files from (hookcmd.sessionMayWrite).
	//
	// This is the group that pays for the package. `idle` is admitted because a
	// write from an idle session is by definition mid-turn — writes only happen
	// inside turns — so the state is stale, not the agent (E-2093). `needs_input`
	// is refused because a human was asked something and has not answered, and
	// `ended` because the session is over. Both refusals are the gate working.
	MayWrite

	// AwaitsHuman is the states in which a session is waiting on a PERSON, in
	// the order the project attention board ranks them (E-1976): blocked
	// mid-turn first, then between turns.
	//
	// `idle` is a member, which reads oddly next to `needs_input` and is
	// preserved exactly as found. The board's actIdle rank says an idle session
	// "is waiting for the user's next prompt, so it is a genuine claim on
	// attention, just a quieter one". That is the rule as it stands; E-2091 owns
	// whether it should change.
	AwaitsHuman

	// DisplayOrder is `session list`'s sort order — working first, then the
	// blocked session that most wants attention, then the quiet ones, then the
	// dead. Read back with Rank, which is what builds the SQL CASE.
	//
	// Deliberately NOT All's order: All is the lifecycle, this is the reading
	// order, and they disagree about where `needs_input` sits.
	DisplayOrder
)

// groups is the ONE map. Every grouping in the system is a row here, so adding
// a state forces a decision about each of them.
//
// Ordered groups (All, DisplayOrder) are written in their meaningful order and
// read back with Rank. Unordered groups are written in lifecycle order for
// readability only.
var groups = map[Group][]State{
	All:          {Working, Idle, NeedsInput, Ended},
	Live:         {Working, Idle, NeedsInput},
	MayWrite:     {Working, Idle},
	AwaitsHuman:  {Idle, NeedsInput},
	DisplayOrder: {Working, NeedsInput, Idle, Ended},
}

// groupSlugs is the CLI-facing name of each Group. The Python client passes
// these through verbatim and holds no group list of its own, so adding a group
// here needs no client change.
var groupSlugs = map[Group]string{
	All:          "all",
	Live:         "live",
	MayWrite:     "may-write",
	AwaitsHuman:  "awaits-human",
	DisplayOrder: "display-order",
}

// labels is the human display string per state, mirroring taskstatus.Label().
// `session list`'s legend is built from these, lowercased by that view — the
// registry owns the words, a surface owns its own casing.
var labels = map[State]string{
	Working:    "Working",
	Idle:       "Idle",
	NeedsInput: "Needs Input",
	Ended:      "Ended",
}

// glyphs is the SEMANTIC glyph per state — no color, no medium-specific
// styling. A surface maps its own palette onto these.
//
// All four move here unchanged from `session_cmd.py`, where E-1914 chose them:
// ⟳ is `session status`'s own "doing" glyph, reused because it already means a
// live session working a task; ‖ avoids ⏸, which means "blocks" in the status
// view; ? and ␥ had no precedent to keep. The reason they are one column wide
// is the reason they exist at all — `needs_input` is eleven characters against
// `idle`'s four, and printing the raw word made every following column ragged.
var glyphs = map[State]string{
	Working:    "⟳",
	Idle:       "‖",
	NeedsInput: "?",
	Ended:      "␥",
}

// UnknownGlyph is what Glyph answers for a state outside the vocabulary — the
// should-never-happen marker, matching the ⁇-for-unknown idiom both existing
// views already use (internal/sessionstatuscmd, projectstatuscmd's actUnknown).
//
// Having one is why Glyph differs from taskstatus.Glyph, which answers "" for an
// unknown status: there, an unknown status has no display form and a renderer
// draws an empty cell; here the empty cell would break a fixed-width column, so
// the vocabulary defines what to draw instead.
const UnknownGlyph = "⁇"

// NoRank is what Rank returns for a state outside the group.
const NoRank = -1

// Get returns the members of g in the group's own order. The result is a
// defensive copy: callers may sort or append to it without corrupting the
// registry.
func Get(g Group) []State {
	members, ok := groups[g]
	if !ok {
		return nil
	}
	out := make([]State, len(members))
	copy(out, members)
	return out
}

// Has reports whether s is a member of g.
func Has(g Group, s State) bool {
	for _, member := range groups[g] {
		if member == s {
			return true
		}
	}
	return false
}

// SQLList renders g for a SQL IN clause: `'working','idle'`.
//
// This is the highest-value accessor in the package, and here more than in
// taskstatus: twenty-nine of the sites this replaced were a state test inside a
// SQL string literal, invisible to the compiler, to a linter, and to any search
// for a Go identifier. States cannot contain a quote (the vocabulary is closed
// and defined above), so no escaping is required.
func SQLList(g Group) string {
	members := groups[g]
	quoted := make([]string, len(members))
	for i, member := range members {
		quoted[i] = "'" + member + "'"
	}
	return strings.Join(quoted, ",")
}

// Rank returns the index of s within an ordered group, or NoRank when s is not
// a member. Used where the ordering itself is the rule — `session list`'s sort
// CASE, the board's attention ranking.
func Rank(g Group, s State) int {
	for i, member := range groups[g] {
		if member == s {
			return i
		}
	}
	return NoRank
}

// Label returns the human display string for s, or "" for an unknown state.
// Mirrors taskstatus.Label().
func Label(s State) string {
	return labels[s]
}

// Glyph returns the semantic glyph for s, and UnknownGlyph for a state outside
// the vocabulary. See UnknownGlyph for why this differs from taskstatus.Glyph.
func Glyph(s State) string {
	if g, ok := glyphs[s]; ok {
		return g
	}
	return UnknownGlyph
}

// Valid reports whether s is a member of the vocabulary.
func Valid(s State) bool {
	return Has(All, s)
}

// Validate returns an error naming the vocabulary when s is not a state.
func Validate(s State) error {
	if Valid(s) {
		return nil
	}
	return fmt.Errorf("sessionstate: invalid state %q (valid: %s)",
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
	return 0, fmt.Errorf("sessionstate: unknown group %q (valid: %s)",
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
