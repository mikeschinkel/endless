package taskstatus_test

import (
	"strings"
	"testing"

	"github.com/mikeschinkel/endless/internal/taskstatus"
	"github.com/mikeschinkel/endless/internal/tasktype"
)

// ---------------------------------------------------------------------------
// Structural invariants — the same discipline the group registry runs on.
//
// The table is the SOURCE for both the guard and the rendered diagram, so an
// edge naming a status that does not exist, a state with no way out, or a task
// type with no way to finish are all failures that must surface here rather
// than as a stranded task somebody has to hand-edit out of the database.
// ---------------------------------------------------------------------------

// TestEveryEndpointIsInTheVocabulary is the subset invariant, extended from the
// groups to the table. A typo'd endpoint would silently make an edge
// unreachable, which reads exactly like a missing edge and is much harder to
// find.
func TestEveryEndpointIsInTheVocabulary(t *testing.T) {
	for _, tr := range taskstatus.Transitions() {
		if !taskstatus.Valid(tr.From) {
			t.Errorf("transition %q -> %q: %q is not a status", tr.From, tr.To, tr.From)
		}
		if !taskstatus.Valid(tr.To) {
			t.Errorf("transition %q -> %q: %q is not a status", tr.From, tr.To, tr.To)
		}
	}
}

// TestEveryStatusIsReachableFromEntry walks the table forward from the entry
// status. A status nothing can reach is a status a task can only be put into by
// hand — which is the shape of the defect this whole task exists to close.
func TestEveryStatusIsReachableFromEntry(t *testing.T) {
	seen := map[taskstatus.Status]bool{taskstatus.EntryStatus: true}
	for changed := true; changed; {
		changed = false
		for _, tr := range taskstatus.Transitions() {
			if seen[tr.From] && !seen[tr.To] {
				seen[tr.To] = true
				changed = true
			}
		}
	}
	for _, s := range taskstatus.Get(taskstatus.All) {
		if !seen[s] {
			t.Errorf("status %q is unreachable from %q — nothing can put a task into it",
				s, taskstatus.EntryStatus)
		}
	}
}

// TestEveryNonTerminalStatusHasAWayOut is the other half: a status a task can
// enter and never leave strands the task permanently, since there is no --force.
func TestEveryNonTerminalStatusHasAWayOut(t *testing.T) {
	out := map[taskstatus.Status]int{}
	for _, tr := range taskstatus.Transitions() {
		out[tr.From]++
	}
	for _, s := range taskstatus.Get(taskstatus.All) {
		if taskstatus.Has(taskstatus.Terminal, s) {
			continue
		}
		if out[s] == 0 {
			t.Errorf("status %q has no outbound transition — a task reaching it is stranded", s)
		}
	}
}

// TestEveryTypeCanFinish is what catches a task type left out of the lanes.
// Types default to "every type", so a new type inherits the general edges — but
// the verification lane names its types explicitly, and a type excluded from
// every terminal-bearing lane could reach no end state at all.
func TestEveryTypeCanFinish(t *testing.T) {
	for _, tt := range tasktype.All() {
		seen := map[taskstatus.Status]bool{taskstatus.EntryStatus: true}
		for changed := true; changed; {
			changed = false
			for _, tr := range taskstatus.Transitions() {
				if seen[tr.From] && !seen[tr.To] && tr.AppliesTo(tt) {
					seen[tr.To] = true
					changed = true
				}
			}
		}
		var reached []string
		for _, s := range taskstatus.Get(taskstatus.Terminal) {
			if seen[s] {
				reached = append(reached, s)
			}
		}
		if len(reached) == 0 {
			t.Errorf("a %s task can reach no terminal status — it would never finish", tt)
		}
	}
}

// TestNoDuplicateEdges guards the table against a second row for an edge that
// already exists: two rows for the same From/To differ only in their label and
// actor, so the diagram would draw the edge twice with contradictory prose.
func TestNoDuplicateEdges(t *testing.T) {
	seen := map[string]bool{}
	for _, tr := range taskstatus.Transitions() {
		key := tr.From + "->" + tr.To
		if seen[key] {
			t.Errorf("the table lists %s twice", key)
		}
		seen[key] = true
	}
}

// TestNoSelfEdges pins that a self-transition is handled by TransitionAllowed's
// rule rather than by a row. A row would be dead weight AND would render as a
// self-loop in the diagram, which reads as a real lifecycle step.
func TestNoSelfEdges(t *testing.T) {
	for _, tr := range taskstatus.Transitions() {
		if tr.From == tr.To {
			t.Errorf("the table has a self-edge on %q", tr.From)
		}
	}
}

// TestEveryTransitionHasALabel keeps the generated diagram honest: an unlabeled
// edge is one the reader has to guess the reason for.
func TestEveryTransitionHasALabel(t *testing.T) {
	for _, tr := range taskstatus.Transitions() {
		if strings.TrimSpace(tr.Label) == "" {
			t.Errorf("transition %q -> %q has no label", tr.From, tr.To)
		}
	}
}

// TestLabelDoesNotRepeatItsActor pins the grammar the Actor column exists to
// enforce: the actor is the SUBJECT of the rendered label and the Label is the
// predicate, so "user approves" comes out of Actor=user + Label="approves". A
// label that names an actor itself would render "user user approves" — or,
// worse, "agent user approves", the exact prose/rule disagreement this column
// was added to make impossible.
func TestLabelDoesNotRepeatItsActor(t *testing.T) {
	actors := []string{"user ", "agent ", "session ", "system "}
	for _, tr := range taskstatus.Transitions() {
		for _, a := range actors {
			if strings.HasPrefix(tr.Label, a) {
				t.Errorf("transition %q -> %q labels itself %q — the actor is the subject, supplied by the Actor column",
					tr.From, tr.To, tr.Label)
			}
		}
	}
}

// TestPlanAttachPromotionIsALegalEdge pins the one status write the executor
// INFERS rather than being told: attaching a plan to a pre-judgment task moves
// it to `submitted`. It routes through the same guard as an explicit --status,
// so the table has to admit it or a plan attachment starts failing.
func TestPlanAttachPromotionIsALegalEdge(t *testing.T) {
	for _, from := range taskstatus.Get(taskstatus.PreJudgment) {
		for _, tt := range tasktype.All() {
			if !taskstatus.TransitionAllowed(from, taskstatus.Submitted, tt) {
				t.Errorf("%s -> submitted is refused for a %s task, but attaching a plan does exactly that",
					from, tt)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// TransitionAllowed
// ---------------------------------------------------------------------------

// TestTheReportedCaseIsRefused is the defect, at the table level: a session
// holding one task set an `unplanned`, never-claimed task to `unverified`.
func TestTheReportedCaseIsRefused(t *testing.T) {
	if taskstatus.TransitionAllowed(taskstatus.Unplanned, taskstatus.Unverified, tasktype.TaskTypeTask) {
		t.Error("unplanned -> unverified is allowed; that is the transition this task exists to refuse")
	}
}

func TestTransitionAllowedWalksTheTable(t *testing.T) {
	for _, tr := range taskstatus.Transitions() {
		tt := tasktype.TaskTypeTask
		if len(tr.Types) > 0 {
			tt = tr.Types[0]
		}
		if !taskstatus.TransitionAllowed(tr.From, tr.To, tt) {
			t.Errorf("%q -> %q is in the table but TransitionAllowed says no", tr.From, tr.To)
		}
	}
}

// TestSelfTransitionIsAlwaysAllowed pins the rule --keep-status depends on: it
// pins the CURRENT status into the payload precisely so the executor's
// plan-attach promotion stands down, and refusing that write would break the
// flag outright.
func TestSelfTransitionIsAlwaysAllowed(t *testing.T) {
	for _, s := range taskstatus.Get(taskstatus.All) {
		if !taskstatus.TransitionAllowed(s, s, tasktype.TaskTypeTask) {
			t.Errorf("%q -> %q (a no-op write) is refused", s, s)
		}
	}
}

// TestUnknownFromStatusCanEscape pins the escape hatch. A row holding a status
// the vocabulary no longer has is not IN the lifecycle, so there is no edge to
// check — and with no --force, refusing would strand it permanently.
func TestUnknownFromStatusCanEscape(t *testing.T) {
	if !taskstatus.TransitionAllowed("blocked", taskstatus.Obsolete, tasktype.TaskTypeTask) {
		t.Error("a row holding a retired status cannot be moved to a real one — it is stranded forever")
	}
	if taskstatus.TransitionAllowed("blocked", "nonsense", tasktype.TaskTypeTask) {
		t.Error("the escape hatch let a task escape to a status that does not exist")
	}
}

func TestUnknownToStatusIsRefused(t *testing.T) {
	if taskstatus.TransitionAllowed(taskstatus.Ready, "in_progress", tasktype.TaskTypeTask) {
		t.Error("a status outside the vocabulary was accepted as a destination")
	}
}

// TestTypeRestrictedLaneRefusesTheOtherTypes pins the two lanes: research,
// epic and brainstorm terminate via `completed --outcome` and never enter the
// verification track.
func TestTypeRestrictedLaneRefusesTheOtherTypes(t *testing.T) {
	findings := []tasktype.TaskType{
		tasktype.TaskTypeResearch, tasktype.TaskTypeEpic, tasktype.TaskTypeBrainstorm,
	}
	for _, tt := range findings {
		if taskstatus.TransitionAllowed(taskstatus.Underway, taskstatus.Unverified, tt) {
			t.Errorf("a %s task may reach `unverified`, but it never goes through user verification", tt)
		}
	}
	for _, tt := range []tasktype.TaskType{tasktype.TaskTypeTask, tasktype.TaskTypeBug} {
		if !taskstatus.TransitionAllowed(taskstatus.Underway, taskstatus.Unverified, tt) {
			t.Errorf("a %s task cannot report implementation done", tt)
		}
	}
}

// TestReviewGateIsMandatoryForFindingsTypes is the whole point of E-2016.
// research and brainstorm reach `completed` only THROUGH `unreviewed`; the
// direct edge that used to exist is gone for them, which is what stops a
// session declaring its own outcome finished.
func TestReviewGateIsMandatoryForFindingsTypes(t *testing.T) {
	for _, tt := range []tasktype.TaskType{tasktype.TaskTypeResearch, tasktype.TaskTypeBrainstorm} {
		if taskstatus.TransitionAllowed(taskstatus.Underway, taskstatus.Completed, tt) {
			t.Errorf("a %s task can still jump straight to `completed`, skipping the review gate", tt)
		}
		if taskstatus.TransitionAllowed(taskstatus.Ready, taskstatus.Completed, tt) {
			t.Errorf("a %s task can still jump from `ready` to `completed`, skipping the review gate", tt)
		}
		for _, from := range []string{taskstatus.Underway, taskstatus.Ready} {
			if !taskstatus.TransitionAllowed(from, taskstatus.Unreviewed, tt) {
				t.Errorf("a %s task cannot reach `unreviewed` from %q", tt, from)
			}
		}
		if !taskstatus.TransitionAllowed(taskstatus.Unreviewed, taskstatus.Completed, tt) {
			t.Errorf("a %s task cannot leave `unreviewed` for `completed`", tt)
		}
		// The gate has to be escapable downward too, or a wrong outcome is
		// stuck: E-1817 took five rounds of correction.
		if !taskstatus.TransitionAllowed(taskstatus.Unreviewed, taskstatus.Revisit, tt) {
			t.Errorf("a %s task cannot be sent back from `unreviewed` — a wrong outcome would be stranded", tt)
		}
	}
}

// TestReviewGateRefusesImplementationTypes pins the inverse half. todo and
// bugfix live wholly in the verification lane (gated by `unverified`); the
// findings lane — `unreviewed` AND `completed` — is not theirs. E-1658 removed
// their direct route to `completed`: an implementation type has no findings
// deliverable, so completed-eligibility is a type rule, not a verb one.
func TestReviewGateRefusesImplementationTypes(t *testing.T) {
	for _, tt := range []tasktype.TaskType{tasktype.TaskTypeTask, tasktype.TaskTypeBug} {
		if taskstatus.TransitionAllowed(taskstatus.Underway, taskstatus.Unreviewed, tt) {
			t.Errorf("a %s task may reach `unreviewed`, but it is gated by `unverified`", tt)
		}
		if taskstatus.TransitionAllowed(taskstatus.Underway, taskstatus.Completed, tt) {
			t.Errorf("a %s task may reach `completed`, but implementation types terminate via the verification lane", tt)
		}
	}
}

// TestEveryTypeFinishesViaExactlyOneLane is the invariant the two lanes rest on.
// Every type finishes via EITHER the verification lane (→ unverified →
// confirmed/assumed, for work whose deliverable is testable behavior) OR the
// findings lane (→ completed, for work whose deliverable is an outcome text) —
// never both, never neither. Within the findings lane a type must not reach
// `completed` both through the `unreviewed` gate and around it.
//
// E-1658 corrected the prior invariant (TestFindingsLaneCoversEveryType), which
// required EVERY type to reach `completed`. Implementation types (todo/bugfix)
// legitimately cannot — completed-eligibility is now a type rule, and forcing an
// implementation type to have a route to `completed` was the bug. Enumerating
// tasktype.All is what makes a NEW task type fail here rather than silently
// inherit whichever lane someone edited last.
func TestEveryTypeFinishesViaExactlyOneLane(t *testing.T) {
	for _, tt := range tasktype.All() {
		verification := taskstatus.TransitionAllowed(taskstatus.Underway, taskstatus.Unverified, tt)
		viaGate := taskstatus.TransitionAllowed(taskstatus.Underway, taskstatus.Unreviewed, tt) &&
			taskstatus.TransitionAllowed(taskstatus.Unreviewed, taskstatus.Completed, tt)
		direct := taskstatus.TransitionAllowed(taskstatus.Underway, taskstatus.Completed, tt)
		findings := viaGate || direct
		switch {
		case viaGate && direct:
			t.Errorf("a %s task can reach `completed` both through the gate and around it", tt)
		case verification && findings:
			t.Errorf("a %s task can finish via both the verification and findings lanes", tt)
		case !verification && !findings:
			t.Errorf("a %s task can finish via neither lane — it would never reach a terminal", tt)
		}
	}
}

// ---------------------------------------------------------------------------
// ReachableFrom — the refusal message's second half
// ---------------------------------------------------------------------------

func TestReachableFromIsInVocabularyOrder(t *testing.T) {
	got := taskstatus.ReachableFrom(taskstatus.Unverified, tasktype.TaskTypeTask)
	// `obsolete` is here because E-2144 removed the gate that refused it on
	// shipped work: the axis is whether anything REPLACED the work, not whether
	// it shipped. It sorts last, which is what this test is actually about.
	want := []string{"confirmed", "assumed", "revisit", "declined", "obsolete"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("ReachableFrom(unverified, todo) = %v, want %v", got, want)
	}
}

func TestReachableFromRespectsType(t *testing.T) {
	got := taskstatus.ReachableFrom(taskstatus.Unverified, tasktype.TaskTypeResearch)
	for _, s := range got {
		if s == taskstatus.Confirmed || s == taskstatus.Assumed {
			t.Errorf("ReachableFrom(unverified, research) offers %q, which research never uses", s)
		}
	}
}

func TestReachableFromATerminalStatusIsNotEmptyForReversibleOnes(t *testing.T) {
	// ReopenRefused carry an explicit decision, and reversing one must be a
	// deliberate `task update --status`. That is only true if the table gives
	// them somewhere to go.
	for _, s := range taskstatus.Get(taskstatus.ReopenRefused) {
		if len(taskstatus.ReachableFrom(s, tasktype.TaskTypeTask)) == 0 {
			t.Errorf("%q has no reversal edge, so the documented reversal is impossible", s)
		}
	}
}

// ---------------------------------------------------------------------------
// Rendering
// ---------------------------------------------------------------------------

// TestRenderMermaidIsByteStable is the property the whole generation scheme
// rests on: a randomized iteration order anywhere in the renderer would make
// `just lifecycle-check` fail at random.
func TestRenderMermaidIsByteStable(t *testing.T) {
	first := taskstatus.RenderMermaid()
	for range 20 {
		if got := taskstatus.RenderMermaid(); got != first {
			t.Fatal("RenderMermaid is not byte-stable across calls")
		}
	}
}

// TestRenderMermaidDrawsEveryEdge pins that the picture and the rule are the
// same fact — a rendering that skipped a row would put the guard and the
// diagram back into the disagreement this task closed.
func TestRenderMermaidDrawsEveryEdge(t *testing.T) {
	out := taskstatus.RenderMermaid()
	for _, tr := range taskstatus.Transitions() {
		line := tr.From + " --> " + tr.To + ": " + tr.EdgeLabel()
		if !strings.Contains(out, line) {
			t.Errorf("the rendered diagram is missing %q", line)
		}
	}
}

// TestRenderMermaidDrawsEntryAndTerminals pins the pseudo-states, which come
// from the vocabulary rather than the table and so have no row to lose.
func TestRenderMermaidDrawsEntryAndTerminals(t *testing.T) {
	out := taskstatus.RenderMermaid()
	if !strings.Contains(out, "[*] --> "+taskstatus.EntryStatus) {
		t.Errorf("the rendered diagram has no entry edge into %q", taskstatus.EntryStatus)
	}
	for _, s := range taskstatus.Get(taskstatus.Terminal) {
		if !strings.Contains(out, s+" --> [*]") {
			t.Errorf("the rendered diagram does not terminate %q", s)
		}
	}
}

// TestRenderMermaidMentionsNoRetiredStatus is the vocabulary change made
// visible in the artifact: `blocked` is not a status, so it cannot appear.
func TestRenderMermaidMentionsNoRetiredStatus(t *testing.T) {
	if strings.Contains(taskstatus.RenderMermaid(), "blocked") {
		t.Error("the rendered diagram still names `blocked`, which is not a status")
	}
}

func TestEdgeLabelPutsTheActorFirst(t *testing.T) {
	tr := taskstatus.Transition{
		From: taskstatus.Submitted, To: taskstatus.Ready,
		Actor: taskstatus.ActorUser, Label: "approves",
	}
	if got, want := tr.EdgeLabel(), "user approves"; got != want {
		t.Errorf("EdgeLabel() = %q, want %q", got, want)
	}
}

func TestEdgeLabelNamesTheTypeRestriction(t *testing.T) {
	tr := taskstatus.Transition{
		From: taskstatus.Underway, To: taskstatus.Unverified,
		Actor: taskstatus.ActorSession, Label: "reports implementation done",
		Types: []tasktype.TaskType{tasktype.TaskTypeTask, tasktype.TaskTypeBug},
	}
	if got, want := tr.EdgeLabel(), "session reports implementation done (todo/bugfix)"; got != want {
		t.Errorf("EdgeLabel() = %q, want %q", got, want)
	}
}

func TestActorString(t *testing.T) {
	cases := map[taskstatus.Actor]string{
		taskstatus.ActorUser:    "user",
		taskstatus.ActorAgent:   "agent",
		taskstatus.ActorSession: "session",
		taskstatus.ActorSystem:  "system",
	}
	for a, want := range cases {
		if got := a.String(); got != want {
			t.Errorf("Actor(%d).String() = %q, want %q", int(a), got, want)
		}
	}
}

// TestTransitionsReturnsDefensiveCopy mirrors Get's contract: a caller that
// sorts or truncates the result must not corrupt the table for the guard.
func TestTransitionsReturnsDefensiveCopy(t *testing.T) {
	first := taskstatus.Transitions()
	if len(first) == 0 {
		t.Fatal("Transitions() is empty")
	}
	first[0].To = "CLOBBERED"
	if taskstatus.Transitions()[0].To == "CLOBBERED" {
		t.Error("Transitions returned a live slice: mutating it corrupted the table")
	}
}
