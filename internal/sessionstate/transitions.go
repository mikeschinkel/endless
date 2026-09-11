package sessionstate

// Transition table for the session state lifecycle (E-2105).
//
// # Why a table, and why no generated diagram
//
// internal/taskstatus renders a mermaid lifecycle from its table, with a
// regeneration recipe, a drift check and a three-copy sync test. None of that
// machinery is here, and the asymmetry is deliberate.
//
// Task status models a human approval workflow, which keeps acquiring
// distinctions people find worth drawing — thirteen members and counting, with
// per-task-type edges nobody remembers correctly. A picture earns its keep.
// Session state models a process lifecycle written by hooks, bounded by what a
// harness can report: five members, four of which were unchanged across twelve
// schema revisions of the table they live in, and a diagram of them would be
// five boxes.
//
// The TABLE is justified on different grounds, and they do not shrink with the
// member count. Its Trigger column names WHICH CODE performs each write, and
// "what actually writes `needs_input`?" is a question about writers scattered
// across two languages and six files. Getting it wrong is not hypothetical:
// answering it from a grep is what produced the wrong proposal that filed this
// task. That cost is the same at four states or six.
//
// # What this table does and does not govern
//
// Nothing. It is DOCUMENTATION OF THE WRITERS, not a guard — unlike
// taskstatus's table, which governs `task update --status`. There is no
// `endless session set-state`, and there never was: every edge below is taken
// by a hook handler or an event executor reacting to something that already
// happened. A guard would have nothing to refuse.
//
// So the authoring rule is stricter than taskstatus's, not looser. Be honest,
// not aspirational: a row here is a claim that a named function performs that
// write, and the way to add one is to find the write. The verification suite
// re-derives this table from the tree and fails when a row names a site that no
// longer does what it says.
//
// # Sentinels
//
// From is a state, or one of two sentinels. Both are needed because the shapes
// they describe are real: a session row is CREATED in a state (there is no
// prior state to name), and most of these writes are unconditional UPDATEs that
// do not read what was there first.

// NoState is the From of a transition that CREATES the row. It is not a member
// of the vocabulary and never appears in `sessions.state`; it is the absence a
// creating INSERT starts from.
const NoState State = ""

// AnyState is the From of a transition taken from every state without reading
// the current one — an unconditional UPDATE. It is not a member of the
// vocabulary.
//
// It is a real fact about these writers rather than a shorthand: `EndSession`
// does not check what state it is ending, and writing out four rows for it
// would claim a discrimination the code does not make.
const AnyState State = "*"

// Transition is one edge of the session lifecycle, and the code that takes it.
type Transition struct {
	// From and To are states, or (From only) NoState / AnyState. Both must be
	// in the vocabulary otherwise; the tests assert it, which makes a renamed
	// state a build-time failure rather than a silently unreachable state.
	From, To State

	// Trigger names WHAT causes the write and WHICH function performs it, in
	// that order, separated by →. It is the column the table exists for. Where
	// more than one writer takes the same edge they are listed together,
	// semicolon-separated, because they are the same edge — an edge that
	// listed only its first writer would be worse than no table.
	Trigger string
}

// transitions is the table, ordered to read as a lifecycle: the two ways a row
// is created, then the live edges, then the terminal, then the one way back.
//
// `needs_input` is absent from the To column, and that is the point of E-2091's
// half of this table rather than an omission. Two writers used to put it there —
// a row's initial value and the revive CASE — and neither meant a person was
// being waited on; both now write `idle`. Nothing writes `needs_input`, so the
// state means exactly what the declaration gate says it means and nothing else,
// and the rows still carrying it are sessions that registered and never had a
// turn. Those are surfaced on the attention board to be resolved deliberately,
// not migrated away in the dark. TestEveryStateIsWritten names it as the one
// permitted exception.
var transitions = []Transition{
	{
		// E-2091 moved both creating writes from `needs_input` to `idle`. A row
		// that exists and has done nothing is idle; saying it needs input was a
		// claim about a person neither writer is in a position to make.
		From:    NoState,
		To:      Idle,
		Trigger: "`SessionStart` → monitor.InitSession; any hook event → monitor.TouchSession INSERT",
	},
	{
		From:    NoState,
		To:      Working,
		Trigger: "`task claim` → monitor.BindSessionToTask INSERT; `hook claude` chat → monitor.StartChatSession INSERT; `task chat` → task_cmd.start_chat (via the schema default); `sandbox seed-worktree` → sandboxcmd.seedFromWorktree",
	},
	{
		From:    AnyState,
		To:      Working,
		Trigger: "`task claim` → monitor.BindSessionToTask ON CONFLICT; `hook claude` chat → monitor.StartChatSession ON CONFLICT",
	},
	{
		// Narrower than the row above it, and kept separate because the
		// NARROWNESS is the rule (E-2093). `Stop` idles a session at the end of
		// every turn and nothing marked it working again, so a session that
		// completed one clean turn stayed idle for the rest of its life and the
		// declaration gate refused every write it attempted. WakeSession
		// restores a declaration already made — it fires only from `idle`, only
		// for a session holding a task, and never manufactures a claim.
		From:    Idle,
		To:      Working,
		Trigger: "any hook event → monitor.WakeSession",
	},
	{
		// E-2091. Claude Code prompts the user for permission and the session
		// blocks mid-turn. Unconditional, like every other write beside it: the
		// Notification arrives as a fact, and what the row said a moment ago
		// does not change it.
		From:    AnyState,
		To:      Prompted,
		Trigger: "`Notification` (notification_type=permission_prompt) → monitor.PromptSession",
	},
	{
		// The clearing half, and narrow for WakeSession's reason (E-2091): it
		// only ever undoes a state this same feature wrote, so it fires from
		// `prompted` alone and can never demote a session that moved on some
		// other way. Not folded into TouchSession, which never clobbers a live
		// state — making it the exception for one value is how a general helper
		// starts carrying special cases.
		From:    Prompted,
		To:      Working,
		Trigger: "`PostToolUse` (the approved tool completed) and `UserPromptSubmit` (the user typed instead) → monitor.ResumeFromPrompt",
	},
	{
		From:    AnyState,
		To:      Idle,
		Trigger: "`Stop` → monitor.IdleSession; monitor.CompleteTask",
	},
	{
		From:    AnyState,
		To:      Ended,
		Trigger: "`SessionEnd` → monitor.EndSession; the paneless dedup sweep in monitor.BindSessionToTask",
	},
	{
		// The one way back from the terminal (E-1686). An incoming event is
		// proof the session is alive, and since every reader filters on Live an
		// `ended` row that never recovers is a live session gone permanently
		// invisible. It revives to the same neutral state a creating INSERT
		// uses — `idle` since E-2091, `needs_input` before it — and the next
		// lifecycle hook re-derives the rest. For a revived session that holds a
		// task and fired the event itself, that hook is WakeSession on the very
		// same event, which is why `idle` rather than `working` is still the
		// right landing here: the promotion is a separate rule with its own
		// preconditions, not something this write gets to presume.
		From:    Ended,
		To:      Idle,
		Trigger: "any hook event → monitor.TouchSession revive; `task.claimed` → events.execTaskClaimed revive",
	},
}

// Transitions returns the table. The result is a defensive copy, for Get's
// reason: a caller iterating it must not be able to corrupt it for the next.
func Transitions() []Transition {
	out := make([]Transition, len(transitions))
	copy(out, transitions)
	return out
}
