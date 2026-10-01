// Package refusal renders every user-facing refusal, warning and notice this
// binary writes to stderr, and — for a reader that is an agent rather than a
// person — states whether the agent must report it (E-2159).
//
// # The rule it encodes
//
// An agent reports a refusal only when it cannot continue without input from
// the user. "You must do X instead of Y" is not such a case: the agent calls
// the command again doing X, and the user never needs to hear about it. The
// failure that opened this line of work (E-2155) was a refusal the agent
// handled correctly and then narrated in its handoff anyway, because nothing
// in the message said not to.
//
// So a class is not optional. There is no constructor that takes a message and
// nothing else: every entry point names a class, which is what makes "did
// anyone decide?" a compile-time question instead of a review question.
//
//	NoReport   the command can be retried differently; say nothing
//	Report     only the user can decide; stop and ask
//	ReportIf   the class turns on something only the agent knows; name both branches
//	Fault      Endless itself broke; tell the user, do not retry
//	Warn       nothing is blocked, but the reader should know
//	Info       neither a refusal nor a warning; plain text, no directive
//
// # Two audiences, one construction
//
// A human gets the message Endless has always printed. An agent gets that
// message bracketed by a one-line verdict carrying the directive (E-2097): a
// pipeline that truncates with head or tail keeps a verdict either way, and the
// two copies are byte-identical so whichever end survives is sufficient alone.
// A one-line message needs no bracket — head and tail both yield it whole — so
// it renders as the verdict alone.
//
// Human-only remedies are the reason HumanRemedy exists. A bypass named in a
// refusal is a bypass an agent will take, which is the opposite of stopping to
// ask, so --force and the destructive git escapes render for a person only.
//
// # Why the package owns stderr
//
// Nothing outside this package may reference os.Stderr — TestNoStderrOutside
// fails the build on any reference in internal/ or cmd/. That check is the
// whole enforcement mechanism: a message can only reach a user through a
// constructor here, and every constructor names a class, so an unclassified
// refusal cannot be written. Warnings that block nothing do not come here at
// all; they go to the errors channel via faults.Record, where the user sees
// them on the session-status badge and the agent spends nothing.
package refusal

import (
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
)

// Class is the answer to "must an agent report this?". Every constructor picks
// one; there is no zero-value default, because "nobody decided" is the state
// this package exists to make unreachable.
type Class string

const (
	ClassNoReport Class = "no-report"
	ClassReport   Class = "report"
	ClassReportIf Class = "report-if"
	ClassFault    Class = "fault"
	ClassWarn     Class = "warn"
	ClassInfo     Class = "info"
)

// The directives, in one place because they are a contract with the agent
// reading them and must not drift between call sites.
//
// noReportDirective says "now or in your summary" deliberately. The handoff is
// where the leak happened: the agent obeyed the refusal in the moment and then
// listed it as something the user should know about at the end of the turn.
const (
	noReportDirective = "Handle this yourself: do not mention this refusal to the user, now or in your summary."
	reportDirective   = "Stop and ask; do not work around it."
	reportIfDirective = "do not mention this refusal."
	faultDirective    = "Endless itself failed: tell the user, and do not retry more than once."
	warnDirective     = "Nothing is blocked. Handle this yourself; do not mention it unless the user asks."
)

// Sentinel marks one line of an agent's scrollback as an Endless verdict
// without the surrounding message. It carries the project name because the
// command alone does not identify us — `task add` is also Taskwarrior's verb —
// and a bare "Error:" greps to noise.
//
// It is the same string the Python half prints (agent_help.ERROR_SENTINEL), so
// a relayed Go refusal and a Python one are one grep.
const Sentinel = "[Endless]"

// Error is one classified message. It implements error so a site that already
// returns an error up a call chain can carry its class with it instead of
// losing it at the boundary and being re-classified — badly — at the print
// site.
//
// Fields are unexported and set through the constructors and the chainable
// setters below: the invariant is that class is always populated, and an
// exported struct literal is exactly the hole that would let a site skip it.
type Error struct {
	class Class

	// command names the verb for the verdict line — "worktree in-use", not
	// "endless-go worktree in-use". The binary name belongs to the human
	// prefix, which the site already carries in its own text.
	command string

	// summary is ONE line and carries the whole verdict: what happened, the
	// measured numbers, and whether anything changed. Length is not a
	// constraint — head and tail are line-based — but a paragraph is, because a
	// paragraph invites the skimming the verdict exists to defeat.
	summary string

	remedy      string // NO-REPORT / REPORT-IF / WARN: what to do instead
	decision    string // REPORT: what only the user can decide
	condition   string // REPORT-IF: the branch the agent must evaluate
	humanRemedy string // a bypass offered to a person and never to an agent
	detail      string // extra body lines: usage text, a child's own output
	text        string // the human rendering, when it is not summary + remedy

	// audience pins the reader when the environment is not the thing that
	// decides it — see ToAgent and ToHuman. Empty means "ask Agent()".
	audience string

	cause error
}

// NoReport is a refusal the agent resolves alone: remedy says what to do
// instead, and the user never hears about it.
func NoReport(summary, remedy string) *Error {
	return &Error{class: ClassNoReport, summary: summary, remedy: remedy}
}

// Report is a refusal only the user can clear: decision names the judgment
// that is theirs to make, phrased as the thing being decided ("whether to
// remove a worktree a live session is using"), not as an instruction.
func Report(summary, decision string) *Error {
	return &Error{class: ClassReport, summary: summary, decision: decision}
}

// ReportIf is a refusal whose class turns on something the command cannot see —
// usually what the user asked the agent to do. It names both branches and the
// consequence, and the agent, which holds the conversation, decides.
//
// condition is the test ("the user did not ask for the subtree to go"),
// decision the consequence of getting it wrong ("removal cannot be undone"),
// remedy the other branch ("retry with --cascade").
//
// Reach for it only when the command genuinely cannot resolve the condition
// itself. Where it can — from the actor, the call path, process ancestry, or
// state already in hand — resolving it in code and emitting a definite class is
// strictly better than handing the agent a question.
func ReportIf(summary, condition, remedy, decision string) *Error {
	return &Error{
		class:     ClassReportIf,
		summary:   summary,
		condition: condition,
		remedy:    remedy,
		decision:  decision,
	}
}

// Fault is Endless itself breaking. It is also where an unclassified error
// lands: From routes anything that never chose a class here, so the failure
// mode of forgetting is "tell the user", not "stay silent".
func Fault(err error) *Error {
	summary := "Endless hit an unexpected error."
	if err != nil {
		summary = err.Error()
	}
	return &Error{class: ClassFault, summary: summary, cause: err}
}

// Faultf is Fault for a site that formats its own text rather than holding an
// error value.
func Faultf(format string, args ...any) *Error {
	return &Error{class: ClassFault, summary: fmt.Sprintf(format, args...)}
}

// Warn is something the reader should know that blocks nothing.
//
// Most warnings should not be here at all. A warning the user should act on but
// that blocks nothing belongs in the errors channel (faults.Record), where it
// reaches the user through the session-status badge and `endless errors show`
// and costs the agent nothing. Use Warn for the ones that are genuinely about
// the command the reader just ran.
func Warn(summary, remedy string) *Error {
	return &Error{class: ClassWarn, summary: summary, remedy: remedy}
}

// Info is neither a refusal nor a warning: progress notices, a reaped path, a
// count of what was pruned. It renders identically for both audiences and
// carries no directive, because there is nothing to decide and nothing to
// report.
//
// It exists so that a site writing such a line still names a class, which is
// what keeps TestNoStderrOutside enforceable without an exemption list.
func Info(text string) *Error {
	return &Error{class: ClassInfo, summary: text}
}

// Infof is Info for a site that formats its own text.
func Infof(format string, args ...any) *Error {
	return &Error{class: ClassInfo, summary: fmt.Sprintf(format, args...)}
}

// From classifies an error that reached a print site without a class.
//
// An *Error passes through with the class its site chose. Anything else is a
// fault: a generic print site cannot know whether the agent may continue, and
// the honest answer to "nobody decided" is "Endless itself failed", not a guess
// at NO-REPORT that teaches the agent to swallow real breakage.
func From(err error) *Error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return Fault(err)
}

// The chainable setters below COPY.
//
// A refusal is often a package-level value — monitor.ErrDBFlagConflict, the
// self-dev-worktree gate — reached through From() at a print site, which then
// names its command and pins its text. Mutating in place would write one
// caller's command onto a value every other caller shares, and in a
// long-running process (the session monitor) the wrong verb would then be
// printed for the rest of its life. The copy is one small struct; the aliasing
// bug it prevents is the kind nobody reproduces.
func (e *Error) with(f func(*Error)) *Error {
	c := *e
	f(&c)
	return &c
}

// Command names the verb this refusal came from, for the verdict line.
func (e *Error) Command(name string) *Error {
	return e.with(func(c *Error) { c.command = name })
}

// Text pins the human rendering, byte for byte, when it is not simply the
// summary followed by the remedy. Use it wherever today's message must not
// change — the flag library's own output, a relayed child's text.
func (e *Error) Text(s string) *Error {
	return e.with(func(c *Error) { c.text = s })
}

// HumanRemedy adds a remedy a person may use and an agent may not. It renders
// for a human and is absent from every agent rendering.
func (e *Error) HumanRemedy(s string) *Error {
	return e.with(func(c *Error) { c.humanRemedy = s })
}

// Detail adds body lines below the message for both audiences — usage text, a
// child process's captured output, a list of what was found.
func (e *Error) Detail(s string) *Error {
	return e.with(func(c *Error) { c.detail = s })
}

// Cause records the underlying error for errors.Is/As, without putting it in
// the message.
func (e *Error) Cause(err error) *Error {
	return e.with(func(c *Error) { c.cause = err })
}

// ToAgent pins this message to the agent rendering whatever the environment
// says, and ToHuman pins it to the human one.
//
// Hook output is the reason both exist. A hook's reader is decided by the hook
// EVENT and the exit path, not by who typed the command that triggered it: a
// PreToolUse refusal at exit 2 is read by the model even when a person is
// sitting at the terminal, while a UserPromptSubmit or SessionStart failure at
// exit 2 — and a Stop failure at exit 1 — is read by the person even though a
// harness is plainly present. Asking Agent() in either place gets the wrong
// answer roughly half the time.
//
// Nothing user-facing may be written on a hook's exit-0 path or from an async
// event at all: that output reaches nobody, so a refusal routed there is a
// refusal nobody ever sees.
func (e *Error) ToAgent() *Error {
	return e.with(func(c *Error) { c.audience = AudienceAgent })
}

// ToHuman pins this message to the human rendering. See ToAgent.
func (e *Error) ToHuman() *Error {
	return e.with(func(c *Error) { c.audience = "human" })
}

// forAgent answers "does this particular message render for an agent?" — the
// pin when there is one, the environment otherwise.
func (e *Error) forAgent() bool {
	switch e.audience {
	case AudienceAgent:
		return true
	case "":
		return Agent()
	}
	return false
}

// Class reports which directive this error carries.
func (e *Error) Class() Class { return e.class }

// Error implements error. It returns the human rendering: an *Error that ends
// up in a %v or a log line reads as the message it was written to be, and the
// verdict is added only when it is actually printed for an agent.
//
// Nil-safe, because a nil *Error is how several gates spell "nothing to
// refuse": they return one alongside a block/allow boolean, and a test printing
// the refusal in its failure message would otherwise panic on the allow case —
// turning a readable assertion failure into a stack trace.
func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	return e.humanText()
}

// Unwrap exposes the cause to errors.Is and errors.As.
func (e *Error) Unwrap() error { return e.cause }

// Verdict is the one-line summary-plus-directive an agent reads. Exported
// because the hook paths hand it to the harness as a block reason rather than
// writing it to stderr.
func (e *Error) Verdict() string {
	var b strings.Builder
	b.WriteString(Sentinel)
	if e.command != "" {
		b.WriteString(" ")
		b.WriteString(e.command)
	}
	b.WriteString(": ")
	b.WriteString(e.withoutRestatedPrefix(oneLine(e.summary)))
	for _, part := range e.directive() {
		if part == "" {
			continue
		}
		b.WriteString(" ")
		b.WriteString(part)
	}
	return b.String()
}

// directive is the class's contribution to the verdict line: the remedy or the
// decision, then the fixed sentence that says what to do about it.
func (e *Error) directive() []string {
	switch e.class {
	case ClassNoReport:
		return []string{sentence(e.remedy), noReportDirective}
	case ClassReport:
		return []string{"This needs the user: " + clause(e.decision) + ".", reportDirective}
	case ClassReportIf:
		return []string{fmt.Sprintf("If %s, stop and ask — %s; otherwise %s and %s",
			clause(e.condition), clause(e.decision), clause(e.remedy), reportIfDirective)}
	case ClassFault:
		return []string{faultDirective}
	case ClassWarn:
		return []string{sentence(e.remedy), warnDirective}
	case ClassInfo:
		return nil
	}
	return []string{faultDirective}
}

// humanText is the message a person sees, and it is today's message unchanged.
//
// Nothing the classification added appears here — not the verdict, not the
// directive, not the remedy, not the decision. Those exist for the agent, and
// the whole design rests on them being ADDITIVE: a person's stderr after this
// task is byte for byte what it was before it, so there is no second thing to
// review and no wording anybody has to re-approve.
//
// The one exception runs the other way. HumanRemedy names a bypass today's
// message already offers, and it is REMOVED for an agent rather than added for
// a person.
func (e *Error) humanText() string {
	// The body is passed through UNTOUCHED when there is no human remedy to
	// join it to. Two messages in this repository begin with a blank line —
	// they have always been printed after one — and an unconditional trim here
	// silently deleted it, which is precisely the kind of change this package
	// promises not to make.
	body := e.body()
	if e.humanRemedy != "" {
		body = joinSentences(body, e.humanRemedy)
	}
	return joinLines(body, e.detail)
}

// body is the message proper: what the site has always printed.
//
// summary doubles as it whenever today's message is already one line, which is
// nearly every Go site. Text is for the rest — a multi-line message whose
// one-line summary is necessarily a compression of it, or output produced by
// something else (the flag library, a child process) that must survive
// verbatim.
func (e *Error) body() string {
	if e.text != "" {
		return e.text
	}
	return e.summary
}

// Render is the message for whichever audience is reading, ready to print.
//
// For an agent, a multi-line message is bracketed by identical verdicts: head
// leaves the first, tail leaves the last, and either is sufficient alone. A
// message that is already one line is returned as the verdict by itself — no
// pipe can split a single line, so a second copy would be noise rather than
// insurance. Info is never bracketed: it carries no verdict to repeat.
func (e *Error) Render() string {
	if e.class == ClassInfo || !e.forAgent() {
		return e.humanText()
	}
	// The human remedy is deliberately absent here: see HumanRemedy.
	body := joinLines(e.body(), e.detail)
	verdict := e.Verdict()
	if !strings.Contains(body, "\n") {
		return verdict
	}
	return verdict + "\n\n" + body + "\n\n" + verdict
}

// Print writes the message to stderr. It does not exit; warnings and notices
// use it, and so does a refusal whose caller owns the exit.
func (e *Error) Print() {
	fmt.Fprintln(os.Stderr, e.Render())
}

// Exit prints the message and ends the process with code.
//
// Exit(0) is promoted to 1 rather than honoured: a refusal that leaves a zero
// status tells a shell caller nothing went wrong, and every caller of this
// package is refusing something.
func (e *Error) Exit(code int) {
	e.Print()
	if code == 0 {
		code = 1
	}
	os.Exit(code)
}

// Block ends a PreToolUse hook by refusing the tool call: exit 2, with the
// message on stderr, which is what Claude Code feeds back to the model as the
// block reason.
//
// It pins the agent rendering (see ToAgent) because the reader of a block
// reason is the model by definition — a human at the terminal never sees it.
func Block(e *Error) {
	e.ToAgent().Exit(2)
}

// Passthrough is a child process's own stderr, for a child whose output is
// already classified — a git invocation whose text the user is meant to read
// verbatim, a `claude` process this one execs into.
//
// It is the one sanctioned way to hand os.Stderr out of this package, and it is
// named so that a reviewer sees the choice: "this child's stderr is its own" is
// a decision, not an oversight.
func Passthrough() io.Writer { return os.Stderr }

// binaryPrefixRe matches the "endless-go worktree in-use: " that a Go message
// has always opened with — the binary, optionally the verb, then a colon.
var binaryPrefixRe = regexp.MustCompile(`^endless(?:-go|-migrate|-tmux|-sandbox)?(?:[ \t][a-z0-9][a-z0-9-]*)*: `)

// withoutRestatedPrefix drops from a summary whatever the verdict line has
// already said: the binary prefix, and the command's own name.
//
// Both are right where they are. A Go message has always opened
// "endless-go worktree in-use: ", and a site that names its verb in the
// summary is writing the line a person reads. But the verdict opens
// "[Endless] worktree in-use: " too, and printed adjacent they are the same
// words twice in the one line whose whole value is density.
func (e *Error) withoutRestatedPrefix(s string) string {
	s = withoutBinaryPrefix(s)
	if e.command != "" {
		s = strings.TrimPrefix(s, e.command+": ")
	}
	return s
}

// withoutBinaryPrefix drops that prefix from a summary before it goes into a
// verdict line.
//
// The prefix belongs in the message a person reads, and every converted site
// passes its message through verbatim — which is right, and which is why it is
// still there. But the verdict already opens "[Endless] worktree in-use:", so
// keeping it produced "[Endless] worktree in-use: endless-go worktree in-use:
// --dir is required": the same words twice, in the one line whose value is
// that it is dense.
//
// Stripped here rather than at ~300 call sites, because "do not repeat the
// prefix" is a property of the rendering and not a thing each site should have
// to remember.
func withoutBinaryPrefix(s string) string {
	return binaryPrefixRe.ReplaceAllString(s, "")
}

// oneLine collapses a summary to a single line. The verdict's value is that
// head -1 and tail -1 both yield the whole of it, which an embedded newline
// would quietly destroy.
func oneLine(s string) string {
	s = strings.TrimSpace(s)
	if !strings.Contains(s, "\n") {
		return s
	}
	return strings.Join(strings.Fields(s), " ")
}

// clause strips a trailing period so a fragment reads correctly when embedded
// mid-sentence in a directive.
func clause(s string) string {
	return strings.TrimRight(strings.TrimSpace(s), ".")
}

// sentence gives a fragment a terminating period so the verdict reads as prose
// rather than as a run-on.
func sentence(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if strings.HasSuffix(s, ".") || strings.HasSuffix(s, "!") || strings.HasSuffix(s, "?") {
		return s
	}
	return s + "."
}

// joinSentences joins non-empty parts with a single space.
func joinSentences(parts ...string) string {
	kept := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, " ")
}

// joinLines stacks non-empty parts one per line, with no blank line between
// them. A refusal that prints a message and then a usage block has always
// printed them adjacent, and a blank line inserted here would be a change to
// what a person reads — which this package exists not to make.
func joinLines(parts ...string) string {
	kept := make([]string, 0, len(parts))
	for _, p := range parts {
		// Emptiness is tested on the raw string, not on a trimmed copy: a part
		// that is deliberately whitespace — the blank line some messages open
		// with — is content, and dropping it changes what a person reads.
		if p = strings.TrimRight(p, "\n"); p != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, "\n")
}
