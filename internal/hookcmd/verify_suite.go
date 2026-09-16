package hookcmd

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/mikeschinkel/endless/internal/monitor"
	"github.com/mikeschinkel/endless/internal/verify"
)

// E-1916 — the landed-suite gate, in two arms: refuse an EDIT of another
// task's landed verification suite, and refuse a direct RUN of one.
//
// The rule itself is old and was documented in `.endless/tasks/CLAUDE.md` and
// `endless guide orchestration`: a verify suite is a land-time gate for ONE
// task at ONE moment, so after that task lands its result says nothing about
// anyone else's work, and retrofitting it rewrites the record of what was true
// when it landed. Documentation is the wrong layer for it. The rule was broken
// three times in one session (E-1901) by a handoff that named the guide, and
// twice more in E-1889 by a session that had read the same principle in its own
// task's analysis field on its first tool call. Running an existing,
// runnable-looking script does not FEEL like a violation, and editing a suite
// your change just broke feels like fixing a break rather than falsifying a
// record.
//
// So the enforcement lives where the act happens, following the
// blockPlanFileWriteIfApplicable precedent in claude.go: same shape, same class
// of problem — a path that must not be hand-edited.
//
// WHAT THE TWO ARMS ARE WORTH, separately, because neither is redundant:
//
//   - Arm 1 (edit) is the one that must be airtight. In E-1901 the suites were
//     edited via Edit and never run, so a guard living inside the script was
//     inert. It is also what stops the in-file guard line (E-2090's _guard.sh
//     source) being deleted from a landed suite.
//   - Arm 2 (run) is a BACKSTOP. E-2090 put a guard inside every suite and
//     E-2023 put the same refusal inside `endless task verify`, so this arm
//     covers only what neither can reach: a suite checked out from a commit
//     predating the guard, and one hand-authored without it. It should fire
//     rarely; that is the shape of a backstop, not evidence it is unnecessary.
//
// What is deliberately NOT here: `endless task verify <id>` and the `just
// verify <id>` wrapper. E-2023 refuses those inside the runner, where the task
// id is a resolved argument rather than something inferred from a command
// string. One rule implemented twice against the same landed-ness lookup is a
// rule that can drift, and the hook is the worse of the two places to put it.

// suitePathRe extracts the task number from a path INSIDE a per-task
// verification suite directory — verify.SuitesDir/e-NNNN/<anything>.
//
// Built from verify.SuitesDir rather than a literal so the product convention
// has one spelling: a project that moves its suites moves this gate with them.
//
// Three properties it has to have, each of which is a way to get this wrong:
//
//   - It keys on the `<suites-dir>/e-NNNN/` SEGMENT, not on a basename. The
//     project-level `.endless/verify.toml` shares a filename with a task
//     manifest and sits one level up; it carries the shared setup/teardown/seed
//     composing beneath every task's manifest and must stay editable.
//   - Everything inside a suite directory counts, not just verify.sh and
//     verify.toml. A suite may ship a fixture beside itself, and retrofitting
//     the fixture rewrites the record exactly as retrofitting the assertion
//     does.
//   - `_harness.sh` and `_guard.sh` sit directly in the suites dir rather than
//     in an e-NNNN/ directory, and belong to no task. Nothing here has an
//     opinion about them.
//
// Case-insensitive because an uppercase suite directory still resolves:
// verify.Discover compares ids through NormalizeTaskID, so `E-1758/` and
// `e-1758/` both name the same task's suite and the gate must see both.
var suitePathRe = regexp.MustCompile(
	`(?i)(?:^|/)` + regexp.QuoteMeta(verify.SuitesDir) + `/e-(\d+)/.`)

// suiteRunRe matches a DIRECT execution of a script inside a per-task suite
// directory: `./x`, `x`, `bash x`, `sh -e x`, `/abs/path/x`.
//
// The token has to sit at a COMMAND POSITION — the start of the command, or
// just after a pipeline separator — optionally behind a shell interpreter word.
// That is what separates invoking a suite from naming one: `cat`, `grep`,
// `git add` and `echo` all put the path in an argument position, and every one
// of them is ordinary work. Reading a landed suite is allowed; only running it
// is not.
//
// Anchoring this way also spares the documentation of the rule from the rule.
// `.endless/tasks/CLAUDE.md`, the guide and this task's own suite all quote
// these invocations in order to forbid them — and a gate that blocked the
// writing of its own documentation would be discovered on its first day and
// routed around thereafter.
//
// Any `*.sh` under the suite directory, not just `verify.sh`: a suite may split
// a helper out beside itself, and running the helper is running the suite.
//
// It cannot see a run laundered through an indirection — a wrapper script,
// `xargs`, a shell function, `cd <suite-dir> && ./verify.sh`. That is accepted:
// this arm is a guardrail against the reflex, and Arm 1 is the airtight one.
var suiteRunRe = regexp.MustCompile(
	`(?i)(?:^|[;&|\n])[[:blank:]]*` +
		// An optional interpreter word, with optional flags: `bash`, `sh`,
		// `zsh`, `ksh`, `dash`, and their path-qualified forms.
		`(?:(?:[^[:space:]'"|;&]*/)?(?:ba|z|k|da)?sh[[:blank:]]+(?:-[a-z]+[[:blank:]]+)*)?` +
		// The path. A leading directory prefix is optional but must end in `/`,
		// so `my.endless/tasks/e-1/x.sh` is not mistaken for the real thing.
		`(?:[^[:space:]'"|;&]*/)?` + regexp.QuoteMeta(verify.SuitesDir) +
		`/e-(\d+)/[^[:space:]'"|;&]*\.sh\b`)

// suiteTaskFromPath returns the task whose verification suite path lives in, or
// 0 when it is not a suite path at all. Pure.
func suiteTaskFromPath(path string) int64 {
	return firstSubmatchID(suitePathRe, path)
}

// suiteTaskFromCommand returns the task whose verification suite the command
// would directly execute, or 0 when it runs no suite. Pure.
func suiteTaskFromCommand(cmd string) int64 {
	return firstSubmatchID(suiteRunRe, cmd)
}

// firstSubmatchID parses the first capture group as a task number. A group that
// does not parse (an id longer than an int64, say) is "no opinion" rather than
// an error: this gate refuses a mistake somebody is making, and has nothing to
// say about a string it cannot read.
func firstSubmatchID(re *regexp.Regexp, s string) int64 {
	m := re.FindStringSubmatch(s)
	if m == nil {
		return 0
	}
	id, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil || id <= 0 {
		return 0
	}
	return id
}

// blockLandedSuiteEditIfApplicable is Arm 1: refuse a Write/Edit/NotebookEdit
// anywhere inside a landed, foreign task's suite directory.
func blockLandedSuiteEditIfApplicable(payload claudePayload) {
	if msg, block := landedSuiteEditDecision(payload); block {
		blockToolUse(msg)
	}
}

// blockLandedSuiteRunIfApplicable is Arm 2: refuse a Bash call that directly
// executes a landed, foreign task's suite script.
func blockLandedSuiteRunIfApplicable(payload claudePayload) {
	if msg, block := landedSuiteRunDecision(payload); block {
		blockToolUse(msg)
	}
}

// landedSuiteEditDecision and landedSuiteRunDecision are the side-effect-free
// cores of the two arms, split out for the same reason revisitGateDecision is:
// blockToolUse ends the process, so a test can reach everything ABOVE it or
// nothing at all. Keeping the whole decision — payload in, refusal out — on this
// side of the exit means the plumbing each arm does to get to the predicate
// (which field of which tool input, and what a malformed one means) is tested
// too, and not just the predicate it eventually reaches.
func landedSuiteEditDecision(payload claudePayload) (msg string, block bool) {
	return landedSuiteDecision(payload, landedSuiteEdit,
		suiteTaskFromPath(extractFilePath(payload.ToolName, payload.ToolInput)))
}

func landedSuiteRunDecision(payload claudePayload) (msg string, block bool) {
	var input toolInputBash
	// A tool input that will not parse is not a run: the harness hands this
	// hook one shape per tool, and a payload it cannot read is a question the
	// gate has no answer to rather than a refusal it should risk.
	if err := json.Unmarshal(payload.ToolInput, &input); err != nil {
		return "", false
	}
	return landedSuiteDecision(payload, landedSuiteRun,
		suiteTaskFromCommand(input.Command))
}

func landedSuiteDecision(payload claudePayload, act landedSuiteAct, taskID int64) (msg string, block bool) {
	if taskID == 0 {
		return "", false
	}
	mine, foreign := foreignLandedSuite(payload, taskID)
	if !foreign {
		return "", false
	}
	return landedSuiteRefusal(act, taskID, mine), true
}

// foreignLandedSuite answers the question both arms ask: is taskID's suite one
// this session must leave alone? It returns the tasks that ARE the session's
// own alongside the verdict, so the refusal can show its work instead of
// asserting a conclusion.
//
// The predicate is a CONJUNCTION and both halves are load-bearing, exactly as
// in the runner's guardOwnTaskOnly:
//
//   - landed, because an unlanded suite is live work whose result is meaningful
//     to whoever is looking at it, and
//   - not the session's own, because a task that landed and was then REOPENED
//     (status `revisit`) is once again the claiming session's task. Reopening
//     genuinely does put a task back in its pre-land window, where its suite has
//     to be runnable and authorable; a gate keyed on landed-ness alone would
//     break the documented path for fixing shipped work that turned out wrong.
//
// It FAILS OPEN on every unanswerable question — a main database that cannot be
// read, a session row that does not resolve. This gate exists to stop a mistake
// somebody is making, not to be a precondition for working, and a false refusal
// would block the one session doing the right thing.
func foreignLandedSuite(payload claudePayload, taskID int64) (mine suiteCaller, foreign bool) {
	own, err := monitor.SuiteOwnershipFor(taskID, payload.CWD)
	if err != nil || !own.Known || !own.Landed {
		return mine, false
	}
	mine = suiteCallerFor(own, payload)
	return mine, !mine.holds(taskID)
}

// suiteCaller is the set of tasks this session may treat as its own, with the
// source of each so a refusal can name where it looked.
type suiteCaller struct {
	tasks   []int64
	sources []string
}

func (c *suiteCaller) add(taskID int64, source string) {
	if taskID <= 0 {
		return
	}
	for _, have := range c.tasks {
		if have == taskID {
			return
		}
	}
	c.tasks = append(c.tasks, taskID)
	c.sources = append(c.sources, source)
}

func (c suiteCaller) holds(taskID int64) bool {
	for _, have := range c.tasks {
		if have == taskID {
			return true
		}
	}
	return false
}

// suiteCallerFor merges every honest answer to "whose task is this session on?"
//
// monitor.SuiteOwnershipFor supplies the two the verify runner resolves — the
// session named by ENDLESS_SESSION_ID, and the worktree the caller is standing
// in. The hook adds the one only a hook has: Claude Code hands it a session id,
// and that session's claimed task is the session's own declaration of what it
// is working on, read from the row rather than inferred from an environment.
//
// The union is deliberate, and it is the fail-open direction. A session whose
// cwd has drifted out of its worktree still owns its claimed task; a session
// working in a worktree whose session row never resolved still owns the task
// that worktree names. Disagreement between the sources is a reason to allow,
// not to refuse work somebody is plainly doing.
func suiteCallerFor(own monitor.SuiteOwnership, payload claudePayload) (c suiteCaller) {
	for i, id := range own.Tasks {
		c.add(id, own.Source[i])
	}
	if id, ok := sessionClaimedTask(payload.SessionID); ok {
		c.add(id, "this session's claimed task")
	}
	return c
}

// sessionClaimedTask is the session's own declaration of what it is working
// on, and a test seam — the same shape as osExecutable in claude.go. The
// conjunction this feeds is the part of the gate worth testing against a real
// database, and stubbing the session half is what lets that test seed a
// landings table without also standing up the whole schema.
var sessionClaimedTask = func(sessionID string) (int64, bool) {
	session, err := monitor.GetActiveSession(sessionID)
	if err != nil || session == nil || session.TaskID == nil {
		return 0, false
	}
	return *session.TaskID, true
}

// landedSuiteAct distinguishes the two refusals. They are kept DISTINCT rather
// than sharing one message because the motive differs and the alternative
// differs with it: a run wants a result, an edit wants an assertion changed,
// and telling an agent trying to do one the reason for the other is how a rule
// gets read as boilerplate and rationalized past.
type landedSuiteAct int

const (
	landedSuiteEdit landedSuiteAct = iota
	landedSuiteRun
)

// landedSuiteRefusal composes the block message. Pure, so the shape is testable
// without a database and without exiting the process.
func landedSuiteRefusal(act landedSuiteAct, taskID int64, mine suiteCaller) string {
	var b strings.Builder

	switch act {
	case landedSuiteEdit:
		fmt.Fprintf(&b, "BLOCKED: refusing to edit E-%d's verification suite — "+
			"it has landed, and it is not yours.\n\n", taskID)
		b.WriteString("A verification suite records what was true when ITS task landed. " +
			"Retrofitting\nit to a later change rewrites that record — and if the later " +
			"change is yours, what\nyou would be writing down is that your change did not " +
			"break anything.\n\n")
		b.WriteString("If your change breaks an assertion in a landed suite, leave the " +
			"suite alone. Its\nowner's own run will tell them, on their schedule, with " +
			"their context.\n\n")
	case landedSuiteRun:
		fmt.Fprintf(&b, "BLOCKED: refusing to run E-%d's verification suite — "+
			"it has landed, and it is not yours.\n\n", taskID)
		b.WriteString("A verification suite is a land-time proof of ONE task at ONE " +
			"moment, not a\nregression suite and not a smoke test. Its fixtures and " +
			"assertions were pinned to\nthe tree that existed when it landed, so whatever " +
			"it reports now — pass OR fail —\nsays nothing about your work. Acting on a " +
			"failure in it means changing working code\nto satisfy a check that no longer " +
			"describes it.\n\n")
	}

	switch len(mine.tasks) {
	case 0:
		b.WriteString("  yours:     nothing — no session task, and this is not a task worktree\n")
	default:
		for i, id := range mine.tasks {
			label := "  yours:    "
			if i > 0 {
				label = "            "
			}
			fmt.Fprintf(&b, "%s E-%d (%s)\n", label, id, mine.sources[i])
		}
	}
	fmt.Fprintf(&b, "  this one:  E-%d (landed)\n\n", taskID)

	b.WriteString("Instead:\n")
	switch len(mine.tasks) {
	case 0:
		b.WriteString("  • work in your own task's worktree, on your own task's suite.\n")
	default:
		fmt.Fprintf(&b, "  • your own suite is %s/e-%d/, and it is yours to write and run:\n",
			verify.SuitesDir, mine.tasks[0])
		fmt.Fprintf(&b, "        endless task verify E-%d\n", mine.tasks[0])
	}
	b.WriteString("  • coverage that must survive a land belongs in the project's own test\n" +
		"    suite, where it is protected after the task that wrote it is done.\n\n")
	fmt.Fprintf(&b, "The rules for this directory, in full: %s/CLAUDE.md\n", verify.SuitesDir)

	return b.String()
}
