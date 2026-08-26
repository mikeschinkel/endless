package taskstatus

// Transition table for the task status lifecycle (E-2018).
//
// This table is the SOURCE. `docs/status-lifecycle.mmd` is rendered from it by
// `endless-go task-status lifecycle`, and README.md and docs/guide/index.md
// embed that file byte-identically. Drift between the rule and the picture
// stops being something a test detects and becomes something that cannot
// happen — the same inversion `just guide-index` already runs for the guide's
// command cross-reference.
//
// It was authored the other way round first, and that is why it is authored
// this way now. Parsing the hand-written diagram is genuinely deterministic —
// `A --> B: label` is a tiny regular subset — but measuring the diagram showed
// it was never complete enough to derive from: `blocked` had no node at all,
// and `completed` had no INBOUND edge, so a diagram-derived guard would have
// refused the very transition that closed E-1817, the task that filed this one.
// Both gaps were artifacts of hand-maintenance, and generating the picture is
// what retires the whole class.
//
// Two columns the diagram cannot carry, which is the other half of the
// argument:
//
//   - Actor. The diagram encodes it as prose in the edge labels — "user
//     approves", "session claims", "agent submits". Consistent enough to be a
//     grammar, but a grammar in a picture is a DSL wearing mermaid syntax.
//     Here it is a column, and the rendered label is the actor followed by the
//     transition's own predicate, so the prose CANNOT disagree with the rule.
//   - Types. `unverified → confirmed` is the implementation lane; `→ completed`
//     is the findings lane. One diagram, several lifecycles.
//
// # What this table does and does not govern
//
// It governs `task update --status`, which reaches execTaskFieldsUpdated.
// The other status writers each already encode their own single legal edge —
// `task submit`, `task approve`, `task claim`, `task confirm`, `task assume`,
// `task decline` all emit `task.status_changed` or `task.claimed`, and routing
// them through this table is a simplification, not a fix. Epic status is
// DERIVED from children (E-1541) and written directly, so it is not modelled
// here at all: an epic can arrive at any of its ladder's statuses from any
// other, and drawing that would be drawing the derivation algorithm, not a
// lifecycle.
//
// # Authoring rule
//
// Be honest, not aspirational. An edge the CLI takes today belongs here even
// when it reads oddly (declining work that already shipped, for instance, is
// something `task update --status declined` does and E-1956 deliberately left
// alone). The failure mode this accepts is a legal-but-unwritten edge stranding
// a caller; the mitigation is that the diagram is rendered FROM here, so a
// missing edge is visible in the picture everyone already reads rather than
// hidden in a Go literal.

import (
	"fmt"
	"slices"
	"strings"

	"github.com/mikeschinkel/endless/internal/tasktype"
)

// Actor names who may take a transition. It is the SUBJECT of the rendered
// edge label, which is what keeps the picture's prose and the table's rule the
// same fact rather than two facts that must agree.
type Actor int

const (
	// ActorUser is a person deciding: approving, verifying, reopening.
	ActorUser Actor = iota

	// ActorAgent is a Claude session acting on its own judgment about a task —
	// triaging, submitting, believing work done. Distinct from ActorSession,
	// which is about holding the task, not judging it.
	ActorAgent

	// ActorSession is the session that HOLDS the task, moving its own work
	// along. These are the transitions the actor-reality half of the guard
	// polices (internal/events/status_transition.go).
	ActorSession

	// ActorSystem is Endless itself, transitioning a task as a consequence of
	// some other edit — the tier-1 planning exemption, the description-re-spec
	// reset. No human or agent names these statuses; they are inferred.
	ActorSystem
)

// String returns the lowercase slug, which is also the word the rendered
// mermaid label opens with.
func (a Actor) String() string {
	switch a {
	case ActorUser:
		return "user"
	case ActorAgent:
		return "agent"
	case ActorSession:
		return "session"
	case ActorSystem:
		return "system"
	default:
		return fmt.Sprintf("Actor(%d)", int(a))
	}
}

// Transition is one legal edge of the lifecycle.
type Transition struct {
	// From and To are the statuses. Both must be in the vocabulary; the tests
	// assert it, which is what makes a renamed status a build-time failure
	// rather than a silently unreachable state.
	From, To Status

	// Actor is who may take the edge, and the subject of the rendered label.
	Actor Actor

	// Types restricts the edge to certain task types. EMPTY MEANS EVERY TYPE,
	// which is the common case — only the verification lane is type-specific.
	// A new task type therefore inherits every general edge and is excluded
	// only from the lanes that name types explicitly; TestEveryTypeCanFinish
	// is what catches a type left with no way to reach a terminal status.
	Types []tasktype.TaskType

	// Label is the PREDICATE of the rendered edge label — the actor supplies
	// the subject. "approves" renders as "user approves". Never repeat the
	// actor here; TestLabelDoesNotRepeatItsActor refuses it.
	Label string
}

// implementation is the verification lane's type restriction: the two types
// whose deliverable is behavior a user can test. research, epic and brainstorm
// terminate via `completed --outcome` instead (ED-1502, ED-1516, E-1577/E-1579),
// and Python's _TYPE_FORBIDDEN_STATUSES refuses them the same three statuses at
// the CLI boundary with a message that names the remedy.
var implementation = []tasktype.TaskType{tasktype.TaskTypeTask, tasktype.TaskTypeBug}

// review and direct split the FINDINGS lane by type (E-2016). The findings lane
// is for work whose deliverable IS an outcome text; the verification lane
// (unverified → confirmed/assumed, restricted to `implementation`) is its
// complement for work whose deliverable is testable behavior. Every type
// finishes via exactly one of the two — TestEveryTypeFinishesViaExactlyOneLane.
//
// `review` is the two types whose deliverable is a written outcome that nobody
// downstream catches wrong: research and brainstorm. They route through
// `unreviewed`, so a self-declared finish is not the last word. E-1817 is why —
// a research task whose outcome changed materially through five rounds of the
// owner's correction AFTER the session had marked it completed.
//
// `direct` reaches `completed` in one step, and is now epic-only. Epic status is
// DERIVED from children (E-1541) and written directly, so an epic does not
// travel this table in practice; the edge exists so the invariant test sees a
// route to `completed` for it.
//
// E-1658 removed `todo`/`bugfix` from `direct`. The lane used to be verb-gated
// so "an audit typed `todo`" could finish here, but E-1658 gates a task's type
// against its title verb's CATEGORY at creation: a `todo` can no longer carry an
// investigation verb, so an implementation type has no findings deliverable and
// terminates via the verification lane, never `completed`. That makes
// completed-eligibility a TYPE rule rather than a verb one — the verb is a
// creation-time nudge, not a status gate.
var (
	review = []tasktype.TaskType{tasktype.TaskTypeResearch, tasktype.TaskTypeBrainstorm}
	direct = []tasktype.TaskType{tasktype.TaskTypeEpic}
)

// transitionGroup is one readable band of the table. The bands, in order, ARE
// the reading order of the generated diagram — entry, happy path, reopening,
// terminal — and each renders as a `%%` comment above its edges, which is what
// keeps a forty-edge picture legible.
type transitionGroup struct {
	Name        string
	Transitions []Transition
}

// EntryStatus is where a newly filed task starts, and the target of the
// diagram's `[*] -->` edge. `task add` writes it directly, so it is not a
// transition.
const EntryStatus = Untriaged

// transitionGroups is the table. Ordered by hand to read well, NOT
// alphabetically: a Go map's iteration order is randomized and the generated
// file must be byte-stable, so an ordered structure is the requirement and a
// slice is the smallest thing that meets it.
var transitionGroups = []transitionGroup{
	{
		Name: "Triage — the description is judged, and routed",
		Transitions: []Transition{
			{From: Untriaged, To: Unplanned, Actor: ActorAgent, Label: "triages — needs a plan"},
			{From: Untriaged, To: Submitted, Actor: ActorAgent, Label: "triages — description is a sufficient spec"},
		},
	},
	{
		Name: "Planning and approval — the two-step gate that makes `ready` mean approved",
		Transitions: []Transition{
			{From: Unplanned, To: Submitted, Actor: ActorAgent, Label: "submits — plan attached, or description sufficient"},
			{From: Submitted, To: Ready, Actor: ActorUser, Label: "approves"},
			{From: Submitted, To: Unplanned, Actor: ActorUser, Label: "sends back — the spec is not sufficient"},
			{From: Revisit, To: Submitted, Actor: ActorAgent, Label: "re-submits"},
		},
	},
	{
		Name: "Planning exemption — a tier-1 task skips both planning and triage",
		Transitions: []Transition{
			{From: Untriaged, To: Ready, Actor: ActorSystem, Label: "advances a tier-1 task"},
			{From: Unplanned, To: Ready, Actor: ActorSystem, Label: "advances a tier-1 task"},
		},
	},
	{
		Name: "Re-spec — a material description edit invalidates triage and approval",
		Transitions: []Transition{
			{From: Unplanned, To: Untriaged, Actor: ActorSystem, Label: "resets on a description re-spec"},
			{From: Submitted, To: Untriaged, Actor: ActorSystem, Label: "resets on a description re-spec"},
			{From: Ready, To: Untriaged, Actor: ActorSystem, Label: "resets on a description re-spec"},
			{From: Revisit, To: Untriaged, Actor: ActorSystem, Label: "resets on a description re-spec"},
			// A re-spec that ATTACHES a plan answers the triage question the
			// reset would have asked, so the pair lands `submitted` instead of
			// bouncing to `untriaged` with a full plan attached. It still costs
			// an approved task its approval — approval was granted against the
			// old description.
			{From: Ready, To: Submitted, Actor: ActorSystem, Label: "resets on a description re-spec that attaches a plan"},
		},
	},
	{
		Name: "Claiming — `task claim` promotes any of these in place",
		Transitions: []Transition{
			{From: Ready, To: Underway, Actor: ActorSession, Label: "claims"},
			{From: Untriaged, To: Underway, Actor: ActorSession, Label: "claims"},
			{From: Unplanned, To: Underway, Actor: ActorSession, Label: "claims"},
			{From: Revisit, To: Underway, Actor: ActorSession, Label: "claims"},
		},
	},
	{
		// `unverified` is the gate, not a toll booth: `task confirm` and `task
		// assume` are both accepted on a task still `underway`, so the direct
		// edges are drawn rather than pretended away. What stays refused is
		// reaching any of the three from a status where no work happened —
		// which is the reported defect.
		Name: "Implementation lane — work whose deliverable is testable behavior",
		Transitions: []Transition{
			{From: Underway, To: Unverified, Actor: ActorSession, Types: implementation, Label: "reports implementation done"},
			{From: Unverified, To: Confirmed, Actor: ActorUser, Types: implementation, Label: "verifies"},
			{From: Unverified, To: Assumed, Actor: ActorAgent, Types: implementation, Label: "believes done, verify on use"},
			{From: Underway, To: Confirmed, Actor: ActorUser, Types: implementation, Label: "verifies work still in flight"},
			{From: Underway, To: Assumed, Actor: ActorAgent, Types: implementation, Label: "believes done, verify on use"},
		},
	},
	{
		// Reachable from `ready` as well as `underway` because findings work
		// needs no worktree to produce.
		//
		// The split is E-2016: research and brainstorm stop at `unreviewed`
		// first, every other type still reaches `completed` in one step. Note
		// that the agent delivers the outcome and the USER accepts it — the
		// actor change across the gate IS the gate, exactly as `unverified` →
		// `confirmed` works one lane over.
		Name: "Findings lane — work whose deliverable IS the outcome text",
		Transitions: []Transition{
			{From: Underway, To: Unreviewed, Actor: ActorAgent, Types: review, Label: "delivers the findings as an outcome"},
			{From: Ready, To: Unreviewed, Actor: ActorAgent, Types: review, Label: "delivers the findings as an outcome"},
			{From: Unreviewed, To: Completed, Actor: ActorUser, Types: review, Label: "reads the outcome and accepts it"},
			{From: Underway, To: Completed, Actor: ActorAgent, Types: direct, Label: "delivers the findings as an outcome"},
			{From: Ready, To: Completed, Actor: ActorAgent, Types: direct, Label: "delivers the findings as an outcome"},
		},
	},
	{
		Name: "Reopening — the work is not settled after all",
		Transitions: []Transition{
			{From: Untriaged, To: Revisit, Actor: ActorAgent, Label: "reopens — needs re-evaluation"},
			{From: Unplanned, To: Revisit, Actor: ActorAgent, Label: "reopens — needs re-evaluation"},
			{From: Submitted, To: Revisit, Actor: ActorAgent, Label: "reopens — needs re-evaluation"},
			{From: Ready, To: Revisit, Actor: ActorAgent, Label: "reopens — needs re-evaluation"},
			{From: Underway, To: Revisit, Actor: ActorSession, Label: "hands the task back"},
			{From: Unverified, To: Revisit, Actor: ActorUser, Label: "reopens — verification failed"},
			{From: Unreviewed, To: Revisit, Actor: ActorUser, Label: "reopens — the outcome needs more work"},
			{From: Confirmed, To: Revisit, Actor: ActorUser, Label: "reopens — shipped work found wrong"},
			{From: Assumed, To: Revisit, Actor: ActorUser, Label: "reopens — shipped work found wrong"},
			{From: Completed, To: Revisit, Actor: ActorUser, Label: "reopens — shipped work found wrong"},
		},
	},
	{
		// Declining shipped work reads oddly and is nonetheless real: E-1956
		// refused `obsolete` on shipped work and deliberately left `declined`
		// alone, because backing out work that ran is a decision someone can
		// legitimately make. Honesty over tidiness — see the authoring rule.
		Name: "Declining — an active decision not to do (or not to keep) the work",
		Transitions: []Transition{
			{From: Untriaged, To: Declined, Actor: ActorUser, Label: "declines"},
			{From: Unplanned, To: Declined, Actor: ActorUser, Label: "declines"},
			{From: Submitted, To: Declined, Actor: ActorUser, Label: "declines"},
			{From: Ready, To: Declined, Actor: ActorUser, Label: "declines"},
			{From: Underway, To: Declined, Actor: ActorUser, Label: "declines"},
			{From: Revisit, To: Declined, Actor: ActorUser, Label: "declines"},
			{From: Unverified, To: Declined, Actor: ActorUser, Label: "declines — the shipped work is not being kept"},
			{From: Unreviewed, To: Declined, Actor: ActorUser, Label: "declines — the shipped work is not being kept"},
			{From: Confirmed, To: Declined, Actor: ActorUser, Label: "declines — the shipped work is not being kept"},
			{From: Assumed, To: Declined, Actor: ActorUser, Label: "declines — the shipped work is not being kept"},
			{From: Completed, To: Declined, Actor: ActorUser, Label: "declines — the shipped work is not being kept"},
		},
	},
	{
		// No inbound edge from a Shipped status, and that is the rule E-1956
		// landed: `obsolete` reads as "never happened", which is simply false
		// of work that ran. The fact to record there is a `replaced_by`
		// relation — `task replace <old> --by <new>`.
		Name: "Obsoleting — made irrelevant before the work ever shipped",
		Transitions: []Transition{
			{From: Untriaged, To: Obsolete, Actor: ActorUser, Label: "retires — it never needed doing"},
			{From: Unplanned, To: Obsolete, Actor: ActorUser, Label: "retires — it never needed doing"},
			{From: Submitted, To: Obsolete, Actor: ActorUser, Label: "retires — it never needed doing"},
			{From: Ready, To: Obsolete, Actor: ActorUser, Label: "retires — it never needed doing"},
			{From: Underway, To: Obsolete, Actor: ActorUser, Label: "retires — it never needed doing"},
			{From: Revisit, To: Obsolete, Actor: ActorUser, Label: "retires — it never needed doing"},
		},
	},
	{
		// The two abandonment states carry an explicit decision, so reversing
		// one has to be an explicit act — never a side effect of a reopen or a
		// resume, which is what taskstatus.ReopenRefused exists to say. Both
		// land back at the entry status rather than teleporting to `ready`: a
		// reconsidered task gets re-triaged like any other.
		Name: "Reversal — reconsidering an abandonment decision",
		Transitions: []Transition{
			{From: Declined, To: Untriaged, Actor: ActorUser, Label: "reconsiders"},
			{From: Obsolete, To: Untriaged, Actor: ActorUser, Label: "reconsiders"},
		},
	},
}

// Transitions returns every edge in table order. The result is a defensive
// copy, matching Get's contract — though note that a Transition's Types slice
// is shared, so callers must not write through it.
func Transitions() []Transition {
	var out []Transition
	for _, g := range transitionGroups {
		out = append(out, g.Transitions...)
	}
	return out
}

// AppliesTo reports whether t governs a task of type tt. An empty Types means
// every type, which is the common case.
func (t Transition) AppliesTo(tt tasktype.TaskType) bool {
	if len(t.Types) == 0 {
		return true
	}
	return slices.Contains(t.Types, tt)
}

// TransitionAllowed reports whether from → to is an edge of the lifecycle for a
// task of type tt.
//
// A self-transition is always allowed: it changes nothing, and `task update`
// genuinely writes one — --keep-status pins the current status into the payload
// precisely so the executor's plan-attach promotion stands down (E-1913).
//
// A `from` outside the vocabulary is also allowed OUT of. That is not a hole,
// it is the escape hatch: a row holding a status the vocabulary no longer has
// (a pre-E-2018 `blocked`, a hand-edited value) is not IN the lifecycle, so
// there is no edge to check — and refusing would strand the row permanently,
// with no command able to move it back to a status the system knows. `to` is
// still checked, so the escape leads somewhere real.
func TransitionAllowed(from, to Status, tt tasktype.TaskType) bool {
	if from == to {
		return true
	}
	if !Valid(from) {
		return Valid(to)
	}
	for _, t := range Transitions() {
		if t.From == from && t.To == to && t.AppliesTo(tt) {
			return true
		}
	}
	return false
}

// ReachableFrom returns the statuses a task of type tt may legally move to
// from `from`, in vocabulary order. It backs the refusal message: the caller's
// next move belongs in the refusal, not in the docs.
func ReachableFrom(from Status, tt tasktype.TaskType) []Status {
	reachable := map[Status]bool{}
	for _, t := range Transitions() {
		if t.From == from && t.AppliesTo(tt) {
			reachable[t.To] = true
		}
	}
	var out []Status
	for _, s := range groups[All] {
		if reachable[s] {
			out = append(out, s)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Rendering
// ---------------------------------------------------------------------------

// mermaidIndent is the four spaces every generated diagram line carries, which
// is the indentation the hand-written file already used.
const mermaidIndent = "    "

// RenderMermaid returns the generated body of docs/status-lifecycle.mmd: the
// `stateDiagram-v2` declaration, the entry edge, every transition grouped and
// commented, and the terminal edges. The hand-written editorial preamble is NOT
// here — it lives in the file, above the generated markers, exactly as
// `just guide-index` leaves index.md's prose alone.
//
// Byte-stable by construction: everything it walks is an ordered slice, and it
// consults no map iteration.
func RenderMermaid() string {
	var b strings.Builder
	b.WriteString("stateDiagram-v2\n")
	b.WriteString(mermaidIndent + "[*] --> " + EntryStatus + "\n")

	for _, g := range transitionGroups {
		b.WriteString("\n")
		b.WriteString(mermaidIndent + "%% " + g.Name + "\n")
		for _, t := range g.Transitions {
			b.WriteString(mermaidIndent + t.From + " --> " + t.To + ": " + t.EdgeLabel() + "\n")
		}
	}

	b.WriteString("\n")
	b.WriteString(mermaidIndent + "%% Terminal — the work is over, one way or another\n")
	for _, s := range groups[Terminal] {
		b.WriteString(mermaidIndent + s + " --> [*]\n")
	}
	return b.String()
}

// EdgeLabel renders the label the diagram carries: the actor as subject, the
// transition's predicate, and the type restriction when there is one. This is
// the whole point of the Actor column — the prose is DERIVED from the rule, so
// a label claiming "user approves" on an edge only a session may take is not a
// thing that can be written.
func (t Transition) EdgeLabel() string {
	label := t.Actor.String() + " " + t.Label
	if len(t.Types) == 0 {
		return label
	}
	slugs := make([]string, len(t.Types))
	for i, tt := range t.Types {
		slugs[i] = tt.String()
	}
	return label + " (" + strings.Join(slugs, "/") + ")"
}
