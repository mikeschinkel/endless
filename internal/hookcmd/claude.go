package hookcmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mikeschinkel/endless/internal/docmirror"
	"github.com/mikeschinkel/endless/internal/monitor"
	"github.com/mikeschinkel/endless/internal/sessionstate"
)

func init() {
	// Log to both stderr and a persistent log file
	logDir := filepath.Join(monitor.ConfigDir(), "log")
	os.MkdirAll(logDir, 0755)
	logFile, err := os.OpenFile(
		filepath.Join(logDir, "hook.log"),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND,
		0644,
	)
	if err != nil {
		// Fall back to stderr only
		log.SetOutput(os.Stderr)
	} else {
		log.SetOutput(io.MultiWriter(os.Stderr, logFile))
	}
	log.SetFlags(log.Ldate | log.Ltime)
	log.SetPrefix("endless-go hook: ")
}

type claudePayload struct {
	SessionID      string          `json:"session_id"`
	CWD            string          `json:"cwd"`
	EventName      string          `json:"hook_event_name"`
	ToolName       string          `json:"tool_name,omitempty"`
	ToolInput      json.RawMessage `json:"tool_input,omitempty"`
	TranscriptPath string          `json:"transcript_path,omitempty"`
	Prompt         string          `json:"prompt,omitempty"` // UserPromptSubmit only
	Source         string          `json:"source,omitempty"` // SessionStart: "startup" | "resume" | "clear" | "compact"
	AgentID        string          `json:"agent_id,omitempty"`
	AgentType      string          `json:"agent_type,omitempty"`

	// Stop only. The final assistant text of the turn that is ending. The hooks
	// reference is explicit that a hook needing this must read it here rather
	// than from the transcript file, which lags behind the live turn — so the
	// relay gate (E-1901) compares against this and never parses the transcript.
	LastAssistantMessage string `json:"last_assistant_message,omitempty"`

	// Notification only. Which notification the harness is showing the user —
	// `permission_prompt`, `idle_prompt`, and at least a dozen more (auth,
	// elicitation, quota, agent-team). handleNotification acts on exactly two
	// and ignores the rest; see it for why that is the rule and not a TODO.
	NotificationType string `json:"notification_type,omitempty"`

	// StopFailure only. Which API error ended the turn, and the field the
	// event's own matcher filters on — `rate_limit`, `overloaded`,
	// `max_output_tokens`, `server_error`, `authentication_failed`,
	// `billing_error`, `oauth_org_not_allowed`, `account_on_hold`,
	// `invalid_request`, `model_not_found`, `cloud_credential_error` and
	// `unknown` are the documented vocabulary.
	//
	// handleStopFailure classifies it rather than enumerating it: exactly four
	// values are treated as needing a person and every other value — including
	// one a future Claude Code adds, and an absent field — is transient. See
	// fatalTurnErrorTypes.
	ErrorType string `json:"error_type,omitempty"`

	// Stop only, and UNDOCUMENTED: set when this Stop follows a hook-induced
	// continuation. Used only as a corroborating signal — the relay gate's loop
	// guard is its own bounce counter, because staking a livelock on an
	// unspecified field would be a bug waiting for a Claude Code release.
	StopHookActive bool `json:"stop_hook_active,omitempty"`
}

type toolInputWrite struct {
	FilePath string `json:"file_path"`
	Content  string `json:"content"`
}

type toolInputBash struct {
	Command string `json:"command"`
}

// contextInjection is THE shape for handing Claude a string of context, on
// every event that accepts one: `additionalContext` nested under
// `hookSpecificOutput` alongside the name of the event that fired.
//
// There is no second accepted shape, and this is the whole of E-2001. Until
// that task, SessionStart and UserPromptSubmit emitted a bare top-level
// `{"additionalContext":"…"}` — and every word Endless injected on those two
// events had been silently discarded since the day it was written: the guide
// pointer, the one-shot task list, the per-turn active-task line, E-1917 change
// notices, the pending-message banner and the report-channel rule. PostToolUse
// arrived the whole time, because it already nested.
//
// Why the bare shape failed SILENTLY, which is what made it survive so long:
// the harness picks how to read a hook's stdout from its first character. A
// leading `{` means "parse as JSON", so the plain-text-stdout channel that
// UserPromptSubmit and SessionStart additionally support is NOT a fallback for
// a JSON document the harness fails to recognise. The object parsed, carried no
// field the event honors, and was dropped — no error, no transcript entry, exit
// 0. Nothing downstream could tell "emitted" from "delivered".
//
// Contract: Claude Code hooks reference, "Add context for Claude" ("Return
// `additionalContext` inside `hookSpecificOutput` alongside the event name")
// and "Exit code 0" (the first-character parsing rule).
type contextInjection struct {
	HookSpecificOutput hookContextOutput `json:"hookSpecificOutput"`
}

type hookContextOutput struct {
	HookEventName     string `json:"hookEventName"`
	AdditionalContext string `json:"additionalContext"`
}

// injectContext builds an injection for one event.
//
// Call sites inside an event handler pass payload.EventName rather than a
// literal. The bug this replaces was born of one response type shared across
// four different events; echoing the event that actually fired makes a handler
// structurally incapable of labelling its output with the wrong one.
func injectContext(event, ctx string) contextInjection {
	return contextInjection{
		HookSpecificOutput: hookContextOutput{
			HookEventName:     event,
			AdditionalContext: ctx,
		},
	}
}

// writeContextInjection encodes one injection to stdout. The hook's stdout is a
// single JSON document, so at most one of these may be written per invocation.
func writeContextInjection(event, ctx string) error {
	return json.NewEncoder(os.Stdout).Encode(injectContext(event, ctx))
}

// preToolUseBlock is the JSON block response for PreToolUse (E-1542). decision
// "block" prevents the tool call; reason is shown to Claude; additionalContext
// is injected as a system reminder. The instruction is carried in BOTH fields so
// the block stays reliable even if a given Claude build honors only one — see
// E-1542 §4/§5: the decision+additionalContext interaction needs live
// verification, and the always-works fallback is blockToolUse (stderr + exit 2).
type preToolUseBlock struct {
	Decision           string            `json:"decision"`
	Reason             string            `json:"reason"`
	HookSpecificOutput hookContextOutput `json:"hookSpecificOutput"`
}

func runClaude(args []string) (err error) {
	// E-1962: on an unsupported harness the whole hook is a no-op — silently,
	// and before stdin is even read.
	//
	// Not merely "skip the report channel". Endless cannot do its job on a
	// harness it does not support, and the half-work is worse than nothing: a
	// Desktop session gets a session row with an empty `process` (no tmux pane),
	// which every pane→session lookup then fails to match, so session-gated
	// commands fail for reasons that have nothing to do with what the user did.
	// E-1505 also records the pollution this caused — a SessionStart from
	// Desktop registering the home directory as a project.
	//
	// Silent and nil, never an error. A hook that logged or exited non-zero
	// would surface as a Claude Code hook failure on every event — turning "we
	// don't support this" into a stream of errors for the user to chase. Nothing
	// is written to stdout either: the hook's stdout is one JSON document, and
	// no output is exactly how a hook says "no action".
	//
	// Today the gate is "what is implemented" (the agentenv allow-list). E-1505
	// is where a harness earns support, and where a project-level override would
	// hang if one is ever wanted.
	if !supportedAgent() {
		return nil
	}

	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return payloadUnreadable(fmt.Errorf("reading stdin: %w", err))
	}

	var payload claudePayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return payloadUnreadable(fmt.Errorf("parsing payload: %w", err))
	}

	// E-1661: label every failure below with the event that fired. The exit
	// code a hook must use to reach the agent is a property of the event, and
	// this is the last frame that knows which one fired — so tagging once, at
	// the single exit, covers every return site below and every one added
	// after this comment. Nil stays nil. See hookExitCode.
	defer func() { err = taggedWithEvent(payload.EventName, payload.SessionID, err) }()

	if payload.CWD == "" {
		return nil
	}

	// Normalize the harness-supplied cwd ONCE, here, before anything reads it
	// (E-2002). Every project path this hook compares it against — the projects
	// row, the worktree root derived from it, the lock paths — comes back from
	// monitor already resolved, because monitor.ProjectPath expands the stored
	// form on the way out. Claude Code reports cwd however the user's shell
	// spelled it, so normalizing at the single entry point is what keeps the
	// project lookup, the E-1586 cwd gate, worktree adoption and the recorded
	// activity row all comparing the same two paths instead of two spellings of
	// one directory.
	//
	// RESOLVED, not stored: cwd is used as a directory throughout this hook.
	// Only the projects column speaks tilde (E-2011).
	resolvedCWD, err := monitor.ResolvedProjectPath(payload.CWD)
	if err != nil {
		return fmt.Errorf("resolving cwd %s: %w", payload.CWD, err)
	}
	payload.CWD = resolvedCWD

	projectID, isRegistered, err := monitor.ProjectIDForPath(payload.CWD)
	if err != nil {
		return dbReadFailed(fmt.Errorf("looking up project for %s: %w", payload.CWD, err))
	}

	// Belt-and-suspenders for E-971's worktree lock release, run before
	// anything that can fail: a leaked lock stops future sessions from
	// claiming the worktree, and every DB error between here and the
	// SessionEnd case below returns early, past the release there.
	// ReleaseWorktreeLock is idempotent (os.Remove swallows ErrNotExist),
	// so doing it twice on the happy path costs nothing. The success log
	// makes leaks diagnosable — its absence in stderr means SessionEnd
	// never ran. (E-1209)
	//
	// It used to read "the worktree-local binary handles SessionEnd's full
	// lifecycle" — true while a worktree pinned its hooks at its own
	// binary and this one self-skipped below. E-2166 removed both, so
	// there is one binary and one lifecycle.
	if payload.EventName == "SessionEnd" {
		if wtPath, err := monitor.FindLockBySessionID(projectID, payload.SessionID); err == nil && wtPath != "" {
			if err := monitor.ReleaseWorktreeLock(wtPath); err != nil {
				log.Printf("SessionEnd lock release at %s: %v", wtPath, err)
			} else {
				log.Printf("released worktree lock at %s for session %s", wtPath, payload.SessionID)
			}
		}
	}

	// Record activity (throttled)
	throttled, err := monitor.ShouldThrottle(projectID, "claude", 2)
	if err != nil {
		return dbReadFailed(fmt.Errorf("reading the activity throttle: %w", err))
	}
	if !throttled {
		sessionCtx := map[string]string{
			"session_id": payload.SessionID,
			"event":      payload.EventName,
		}
		if payload.ToolName != "" {
			sessionCtx["tool_name"] = payload.ToolName
		}
		if err := monitor.RecordActivity(projectID, "claude", payload.CWD, sessionCtx); err != nil {
			return dbWriteFailed(fmt.Errorf("recording activity: %w", err))
		}
	}

	// Per-event session UPSERT (E-1426). Records process + last_activity
	// and creates the row if absent. Runs on every event so a NULL/stale
	// `process` self-heals within one tool call (E-1408 / E-1422), and a
	// pane-reattach is picked up on the next event. Collision invalidation
	// inside TouchSession marks any prior occupant of this pane `ended`.
	if err := monitor.TouchSession(payload.SessionID, "claude", os.Getenv("TMUX_PANE"), projectID); err != nil {
		return dbWriteFailed(fmt.Errorf("touching session: %w", err))
	}

	// The wake (E-2093), beside the touch and before any event-specific
	// branching, because this is the ONE point every turn reaches whatever
	// entry point began it. Not in the UserPromptSubmit handler: a turn begun
	// from a `!` bash-input fires no UserPromptSubmit, so waking there alone
	// would reproduce the stranding bug in a narrower, harder-to-see form.
	//
	// A hook event is the "observed acting" half of monitor.WakeSession's rule
	// — TouchSession cannot assert it, because a sibling shell pane reaches
	// that helper on a session's behalf. Here, the session itself is what
	// fired.
	//
	// Runs for `Stop` too, which then idles the session a few lines below. That
	// is correct rather than a race: the session IS working while it processes
	// Stop, and idle is the state the turn should end in.
	//
	// Non-fatal. A failed wake costs a stale state that the next event fixes;
	// failing the hook over it would take the session down instead.
	if err := monitor.WakeSession(payload.SessionID); err != nil {
		log.Printf("waking session %s: %v", payload.SessionID, err)
	}

	// Publish this session's UUID to the tmux window so sibling shell panes
	// can discover and resolve it under --db sandbox (E-1585). Best-effort,
	// every event, to self-heal after a tmux server restart.
	setTmuxSessionUUID(payload.SessionID)

	// Event-specific handling
	switch payload.EventName {
	case "SessionStart":
		// Track the session from the start — DB errors here are critical.
		// TouchSession at the top of runClaude already recorded process +
		// state='idle' on INSERT; InitSession is retained for the
		// transcript-path side effect path below and a defensive no-op
		// UPDATE if the row exists.
		if err := monitor.InitSession(payload.SessionID, projectID); err != nil {
			return dbWriteFailed(fmt.Errorf("initializing session: %w", err))
		}
		// The opportunistic dead-pane reaper (E-1426) that used to run here was
		// removed by E-1898. It marked rows whose tmux pane it could not see as
		// `ended` and NULLed their binding — judging them against whatever tmux
		// server $TMUX happened to name, which on 2026-08-05 was the wrong one:
		// 59 of 61 live bindings were destroyed in a few batched UPDATEs.
		//
		// Nothing replaces it, because nothing needs to. Ghost rows were only
		// ever a READ problem ("this pane resolves to a dead session"), and that
		// is now answered at read time by JOINing a per-invocation observation
		// against the pane's durable identity. No sweep, so no chance for a
		// sweep to be wrong.
		// The opportunistic stale-worktree reaper (E-1337) that used to run here —
		// and on PreToolUse, PostToolUse, Stop and SessionEnd — was removed by
		// E-2128, and nothing replaces it.
		//
		// Reaping was already happening where it belongs: `worktree land` sweeps
		// after a successful land, because a land is what makes OTHER worktrees
		// reclaimable and the person who caused the reclamation is the one who
		// should see what was reclaimed. These five copies were running that same
		// sweep before and after every tool call in every session, which is what
		// made the exact content comparison unaffordable and forced the reaper onto
		// a deliberately inexact condition 4 between E-2087 and E-2128.
		// Opportunistic notice reaper (E-1917 fix). Drops undelivered notices
		// for sessions that have ended: they never take another turn, so those
		// rows are undeliverable by construction. SessionStart only — the write
		// trigger already excludes ended sessions, so this is cleanup for
		// sessions that ended after their notice was written, and once per
		// session start is frequent enough for that.
		if err := monitor.ReapNoticesForEndedSessions(); err != nil {
			log.Printf("reaping notices for ended sessions: %v", err)
		}
		// E-1983: the spawn-flow auto-bind that used to run HERE, ahead of
		// everything below, is gone. `task spawn` sets @endless_task_id on the
		// window it creates and nothing ever clears it, so once that window
		// outlived its session every later SessionStart in it bound to the
		// spawned task — and under write-once `sessions.task_id` (E-1969) that
		// wrong bind is permanent, because it is the FIRST write on a NULL row
		// and `task bind` can no longer move it.
		//
		// The working directory is now the ONLY thing that binds a session to a
		// task. A spawned worker still binds, because `task spawn` launches it
		// with `tmux new-window -c <worktree>`; a session started anywhere else
		// binds from where it actually is, or not at all. The window option
		// keeps its other jobs (the status line's focal-task fallback, the
		// window name, `endless tmux task`) — it just no longer decides
		// sessions.task_id. See logWindowTaskDisagreement, which records the
		// mismatch this used to act on.
		logWindowTaskDisagreement(projectID, payload)
		// Worktree adoption (E-971 Layer D). If cwd is inside an
		// endless-managed worktree, claim the lock or refuse if
		// already owned by a live session.
		if refusal, err := handleWorktreeAdoption(projectID, payload); err != nil {
			return fmt.Errorf("worktree adoption: %w", err)
		} else if refusal != "" {
			return writeContextInjection(payload.EventName, refusal)
		}
		// Cwd-derived auto-bind (E-1291 / E-1700, now the only bind path —
		// E-1983). Runs after worktree adoption so a refused session is never
		// bound. See maybeCwdBind for the subagent / background-agent
		// exclusions.
		maybeCwdBind(projectID, payload)
		return handleTaskContextInjection(projectID, isRegistered, payload)

	case "UserPromptSubmit":
		// The user typed instead of answering the prompt (E-2091). Non-fatal for
		// clearPromptState's reason — see it.
		clearPromptState(payload)
		// Parse transcript to capture new messages
		monitor.ParseTranscript(payload.SessionID, payload.TranscriptPath)
		// E-1901: a new user turn retires any unconsumed relay checkpoint.
		clearRelayCheckpointForNewTurn(payload)
		// E-1953: reset the per-turn gate state, then read the prompt's sigils —
		// labels against the PRECEDING turn's corpus row, and a `$FULL` license
		// for the turn now starting. Both precede the response so the license is
		// in the DB before Stop reads it back.
		stageReportTurn(payload)
		return handleUserPromptSubmit(projectID, payload, applySigils(payload))

	case "PreToolUse":
		return handlePreToolUse(projectID, isRegistered, payload)

	case "PostToolUse":
		// The tool the prompt was about completed, so the user approved it
		// (E-2091). Non-fatal for clearPromptState's reason — see it.
		clearPromptState(payload)
		return handlePostToolUse(projectID, isRegistered, payload)

	case "Notification":
		return handleNotification(payload)

	case "ExitPlanMode":
		return handleExitPlanMode(projectID, payload)

	case "Stop":
		// Parse transcript before idling — captures the assistant's last response
		monitor.ParseTranscript(payload.SessionID, payload.TranscriptPath)
		// E-1953: the minimizer's Stop gate. Runs BEFORE IdleSession — a blocked
		// turn is not ending, so marking the session idle would be a lie that
		// `session list` and the status line would both render. Returns handled
		// when it has emitted a block response; nothing further may write to
		// stdout after that (the hook's stdout is one JSON document).
		if handled, err := enforceReportGate(projectID, isRegistered, payload); err != nil {
			log.Printf("report gate: %v", err)
		} else if handled {
			return nil
		}
		if err := monitor.IdleSession(payload.SessionID); err != nil {
			return dbWriteFailed(fmt.Errorf("idling session: %w", err))
		}

	case "StopFailure":
		// The OTHER way a turn ends (E-2145). Claude Code fires this INSTEAD OF
		// `Stop` when the turn dies on an API error, so it is the `Stop` case's
		// alternative and not its sequel — everything above would otherwise never
		// run for a failed turn, leaving the session reading `working` on a dead
		// one. Beside `Stop` on purpose: the two end-of-turn events belong where
		// a reader can see they are a pair.
		return handleStopFailure(projectID, payload)

	case "PreCompact":
		// Capture everything before compaction
		monitor.ParseTranscript(payload.SessionID, payload.TranscriptPath)
	case "SessionEnd":
		// Final parse
		monitor.ParseTranscript(payload.SessionID, payload.TranscriptPath)
		// Release any worktree lock owned by this session (E-971 Layer D).
		// Use session-id scan rather than walk-up: the user may have cd'd
		// out before /quit, or the lock may live in a worktree the session
		// claimed but never entered.
		// Defense-in-depth: usually a no-op because the early belt-and-
		// suspenders block (E-1209) already released the lock. Kept as
		// a safety net in case FindLockBySessionID errored at that point.
		if wtPath, err := monitor.FindLockBySessionID(projectID, payload.SessionID); err == nil && wtPath != "" {
			if err := monitor.ReleaseWorktreeLock(wtPath); err != nil {
				// Non-fatal: stale-PID check will reap on next claim attempt.
				log.Printf("releasing worktree lock at %s: %v", wtPath, err)
			}
		}
		if err := monitor.EndSession(payload.SessionID); err != nil {
			return dbWriteFailed(fmt.Errorf("ending session: %w", err))
		}
	}

	return nil
}

// reportChannelRule is the coverage rule, delivered on every SessionStart so the
// reporting contract is always in context rather than depending on the agent
// re-reading the guide (E-1803 Arm 2, rewritten by E-1953).
//
// It states a MECHANIC, not a standard of quality. Every previous version of
// this rule tried to teach the agent what deserves to be said — "a computed fact
// the user cannot derive, XOR a genuine open decision" — and every one of them
// failed the same way: the agent judged its own output in the same breath as
// writing it, and judged generously. The minimizer is a second party, so the
// rule no longer has to describe good output. It only has to get the draft to
// the minimizer.
//
// It used to add "do not pre-summarize the draft", on the reasoning that an
// agent which shortens before submitting has done the minimizer's job badly.
// E-2030 removed that: which agent does the cutting does not matter, and an
// agent that applies the standard itself has achieved the objective, not evaded
// it. What matters is the OUTCOME the user receives. So the rule asks for the
// reply the agent means to send and says nothing about how long it should be.
//
// "Exactly as you would send it" is not that instruction wearing a disguise. It
// asks for the WHOLE reply rather than an excerpt, because the Stop gate
// compares the final message against the minimized draft — a partial draft
// produces a comparison against the wrong artifact.
const reportChannelRule = "Report channel: every reply you send the user goes " +
	"through `endless task report` first. Write the reply you mean to send — " +
	"exactly as you would send it, tables and code blocks and all — to a file, " +
	"then run `endless task report [<task-id>] --draft-file <path>`. The task " +
	"id is optional; omit it when you have nothing claimed. Send that command's " +
	"output as your entire final message, verbatim: no preamble, no additions, " +
	"nothing after it.\n\n" +
	"If it cuts something you needed, `endless task report --raw` prints your " +
	"draft back unchanged; nothing is destroyed.\n\n" +
	"Occasionally the output arrives as two labelled options rather than one " +
	"reply. Send it verbatim exactly as before — the user picks, not you. " +
	"Choosing one yourself destroys the comparison, which is the only place the " +
	"minimizer gets a real counterfactual to learn from.\n\n" +
	"A Stop hook enforces both halves: it blocks a final message that differs " +
	"from the command's output, and it blocks a turn that never ran the command " +
	"at all."

// reportChannelOn reports whether this session runs the report channel. There is
// no code-level switch any more: the live/off decision belongs to
// `.endless/config.json`, because it must be settable per project — so
// Endless's own checkout can opt out while the minimizer prompt is tuned without
// every other project shipping ungated — and it must sit somewhere an agent does
// not edit in the course of normal work, which rules out
// `.claude/settings.json`.
//
// Resolution is nearest-config-wins from cwd (see MinimizerEnabledForCwd), so a
// worktree's own branch state governs its sessions. That is what makes a branch
// which is CHANGING the gate able to exempt itself before it lands.
//
// Unregistered projects are off: Endless does not gate a directory it does not
// track. A project whose path cannot be resolved is off for the same reason.
//
// Used by BOTH the SessionStart rule and the Stop gate, deliberately: a session
// must never be told to use a channel that will not gate it, nor gated without
// having been told.
//
// E-1962: the channel runs only on a SUPPORTED agent harness — Claude Code in a
// terminal, and nothing else today. A Claude Code Desktop session on a gate-on
// project was being handed the reporting rule: an instruction that costs it a
// per-turn round trip and that no Stop hook there will enforce. The harness is
// checked HERE, alongside the config key, so all three consumers (SessionStart
// rule, PostToolUse reinforcement, Stop gate) move together; gating one of them
// would break the told-iff-gated invariant above.
//
// The two axes are independent and both must say yes: `minimizer.enabled` is the
// project's decision, supportedAgent is the product's. Making the harness axis
// project-configurable is deliberately deferred to E-1505 (add support for
// Claude Desktop) — there is no second supported harness to configure until
// then.
func reportChannelOn(projectID int64, isRegistered bool, cwd string) bool {
	if !isRegistered {
		return false
	}
	if !supportedAgent() {
		return false
	}
	root, err := monitor.ProjectPath(projectID)
	if err != nil || root == "" {
		return false
	}
	return monitor.MinimizerEnabledForCwd(cwd, root)
}

func handleTaskContextInjection(projectID int64, isRegistered bool, payload claudePayload) error {
	ctx, err := buildTaskContextInjection(projectID, payload)
	if err != nil {
		return err
	}
	combined := composeSessionStartContext(ctx, reportChannelOn(projectID, isRegistered, payload.CWD))
	// E-1983: SessionStart cannot refuse a session — injected text is the hook's
	// only lever here — so the unbound-in-a-worktree gate explains itself now and
	// PreToolUse enforces it on the first tool call. Leading, because it is the
	// reason nothing else in this session will work until it is answered.
	// Registered-only: an unregistered project has no tasks to bind to.
	if isRegistered {
		if notice, blocked := unboundWorktreeDecision(projectID, payload); blocked {
			combined = strings.TrimSpace(notice + "\n\n" + combined)
		}
	}
	if combined == "" {
		return nil
	}
	return writeContextInjection(payload.EventName, combined)
}

// composeSessionStartContext prepends the report-channel coverage rule to the
// (possibly empty) one-shot task-list context. The rule ships on every
// SessionStart — even when the task list was already injected on an earlier
// start (resume/compact) — so coverage never depends on the one-shot gate. Pure
// so the composition is unit-testable.
func composeSessionStartContext(taskListCtx string, channelOn bool) string {
	// A project that switched the channel off in .endless/config.json is not told
	// to use it. Returning "" (rather than a rule-shaped string) is what lets
	// handleTaskContextInjection suppress the injection entirely when there is
	// also no task list to deliver.
	if !channelOn {
		return taskListCtx
	}
	if taskListCtx == "" {
		return reportChannelRule
	}
	return reportChannelRule + "\n\n" + taskListCtx
}

// handleUserPromptSubmit composes the per-prompt response. Pieces, any
// subset of which may be present:
//
//  1. Layer 1: first-time full task list (one-shot) OR per-prompt
//     "Active task: E-XXX — <title>." reminder.
func handleUserPromptSubmit(projectID int64, payload claudePayload, sigilNotice string) error {
	var parts []string

	// E-1953: a refused label leads. The user believes they just taught the
	// minimizer something; if the notice were buried under a task list the agent
	// would skip it and the correction would be lost twice over.
	if sigilNotice != "" {
		parts = append(parts, sigilNotice)
	}

	// Layer 1: full list on first injection, single-line reminder thereafter
	if !monitor.HasInjectedContext(payload.SessionID) {
		if ctx, err := buildTaskContextInjection(projectID, payload); err == nil && ctx != "" {
			parts = append(parts, ctx)
		}
	} else {
		if session, err := monitor.GetActiveSession(payload.SessionID); err == nil &&
			session != nil && session.TaskID != nil {
			if h, err := monitor.GetTaskHeadline(*session.TaskID); err == nil && h.Title != "" {
				parts = append(parts, h.Render(*session.TaskID))
			}
		}
	}

	// E-1917: changes made to this session's tasks since its last turn, each
	// delivered exactly once. Appended after the active-task line so a status
	// this session is holding stale is contradicted by the freshest thing in
	// the injection.
	if notices := deliverNotices(projectID, payload); notices != "" {
		parts = append(parts, notices)
	}

	if len(parts) == 0 {
		return nil
	}
	return writeContextInjection(payload.EventName, strings.Join(parts, "\n\n"))
}

// deliverNotices drains this session's undelivered change notices (E-1917) and
// returns them as injectable lines, marking them delivered.
//
// The problem it solves: the user changes a task from the CLI mid-session, the
// agent keeps answering from the status it learned N turns ago, and the two
// proceed on different facts until the user notices and corrects it by hand.
//
// Every failure path returns "" and leaves the notices PENDING rather than
// consuming them, so a transient DB error costs a turn's delay rather than the
// notice itself. The one thing that must never happen is marking a notice
// delivered that was not injected: nothing would ever re-tell the session, and
// the stale belief this exists to correct would survive silently.
func deliverNotices(projectID int64, payload claudePayload) string {
	session, err := monitor.GetActiveSession(payload.SessionID)
	if err != nil || session == nil {
		return ""
	}
	notices, err := monitor.PendingNotices(session.ID)
	if err != nil || len(notices) == 0 {
		return ""
	}

	projectRoot, rootErr := monitor.ProjectPath(projectID)

	var lines []string
	var delivered []int64
	for _, n := range notices {
		rendered, ok := monitor.RenderNotice(n)
		if !ok {
			// Unrenderable JSON: leave it pending rather than silently burn it.
			continue
		}
		lines = append(lines, rendered)
		delivered = append(delivered, n.ID)
		if rootErr == nil {
			monitor.AppendNoticeLog(projectRoot, session.ID, n, rendered)
		}
	}
	if len(lines) == 0 {
		return ""
	}
	if err := monitor.MarkNoticesDelivered(delivered); err != nil {
		// Could not record delivery: inject nothing. Delivering now would mean
		// these notices are shown again on the next turn, and a repeated FYI is
		// exactly the noise that trains an agent to skim the line that matters.
		return ""
	}
	return strings.Join(lines, "\n")
}

// guidePointer is the lead line of the first-injection context (E-1854).
// Endless tells THIS repo's agent to run `endless guide` only because the
// dogfooding repo's committed CLAUDE.md says so; a downstream project that
// uses Endless as a product gets no such pointer, and `setup.py` writes
// nothing into its CLAUDE.md. Shipping the pointer in the injection makes
// the guide reachable by every agent on every project that has the hook
// installed, regardless of that project's CLAUDE.md. Wording is pinned by
// the task — change it there, not here.
const guidePointer = "New to this project? Run `endless guide` to learn the Endless workflow."

// withGuidePointer puts guidePointer at the head of the one-shot context,
// ahead of the task list, separated by a blank line. It leads because it is
// the instruction that makes the rest of the injection actionable: an agent
// that doesn't know the workflow can't do anything useful with a task list.
// A project with no tasks yet still gets the pointer — that is precisely the
// freshly-set-up downstream project this exists for.
func withGuidePointer(taskContext string) string {
	if taskContext == "" {
		return guidePointer
	}
	return guidePointer + "\n\n" + taskContext
}

// buildTaskContextInjection returns the one-shot guide pointer + full task
// list to inject on the first SessionStart/UserPromptSubmit. Returns
// ("", nil) when the session has already received the injection — which is
// what keeps the pointer one-shot rather than a per-prompt nag. Marks the
// session as injected when it produces a non-empty result.
func buildTaskContextInjection(projectID int64, payload claudePayload) (string, error) {
	if monitor.HasInjectedContext(payload.SessionID) {
		return "", nil
	}
	projectName, err := monitor.GetProjectName(projectID)
	if err != nil {
		return "", fmt.Errorf("getting project name: %w", err)
	}
	items, err := monitor.GetActiveTasks(projectID)
	if err != nil {
		return "", fmt.Errorf("getting active tasks: %w", err)
	}
	context := withGuidePointer(monitor.FormatTasks(projectName, items))
	monitor.MarkContextInjected(projectID, payload.SessionID, payload.CWD)
	return context, nil
}

// PARKED (E-2177): the report channel this detector and reportRelayInstruction
// serve is switched off — `minimizer.enabled: false` in project config — pending
// E-2042 ("Make the reply minimizer trustworthy enough to leave enabled"). Its
// implementation was found unworkable and will be revisited before it is
// enabled again. Do not design around this detector's text matching, and do not
// "fix" it, until then.
//
// taskReportRe matches a Bash command that RUNS `endless task report` (E-1803
// Arm 1). It anchors to a command position — string start, line start, or right
// after a `;` / `&` / `|` separator — with an optional path prefix
// (`/usr/local/bin/endless task report`). Anchoring is deliberate: the phrase
// appears constantly as an ARGUMENT in this repo — git commit messages, `echo`,
// `grep` patterns — and matching those would fire the reinforcement on commands
// that never produce a report. A quoted argument is not at a command position,
// so those no longer match; standalone, `&&`-chained, and path-prefixed real
// runs still do. It stays in the hook binary (not a settings.json `if:` matcher)
// so it can't drift per machine. The residual (the phrase following a literal
// `;`/`&`/`|` inside a quoted string) is harmless — this is a nudge, not a gate.
var taskReportRe = regexp.MustCompile(`(?m)(?:^|[;&|])\s*(?:\S*/)?endless\s+task\s+report\b`)

// reportRelayInstruction is the compose-time reinforcement injected right after
// a `task report` run that actually produced output (E-1803 Arm 1, rewritten by
// E-1953).
//
// It restates the contract at the moment of maximum temptation. The agent has
// just watched the minimizer delete most of what it wrote, and the pull toward
// "I'll send its output plus the two sentences it shouldn't have cut" is
// strongest right here. Naming the appeal is what makes that resistible: an
// agent with a legitimate objection and no channel for it will rationalize
// appending instead.
const reportRelayInstruction = "You just ran `endless task report`. Its output " +
	"is now your entire final message. Send it verbatim — no preamble, no " +
	"framing sentence, no additions, nothing after it. A Stop hook compares your " +
	"final message against it.\n\n" +
	"If the minimizer cut something the user genuinely needs, do not paste it " +
	"back. You get ONE appeal per turn: re-run `endless task report " +
	"--draft-file <path>` with a draft that argues for the missing content, and " +
	"send the new output instead. `endless task report --raw` prints your " +
	"original draft unchanged if you need to see what was removed."

// reportRelayResponse builds the PostToolUse reinforcement emitted after a
// `task report` run. Pure (no I/O) so the response shape is unit-testable.
func reportRelayResponse() contextInjection {
	return injectContext("PostToolUse", reportRelayInstruction)
}

// reportRendered reports whether the session now owes a minimized message —
// i.e. whether the `task report` run that just finished actually produced one.
//
// This is the "keyed on a successful render" test (E-1953). It asks the DB
// rather than parsing the tool result because the checkpoint is written by the
// command itself, on the success path only: there is no output shape to pattern
// match, no exit code to trust through a shell pipeline, and no way for a
// `--help` or a failed run to fake it.
//
// Fails closed (returns false): with no resolvable session there is no
// checkpoint, so there is nothing to reinforce, and injecting the instruction
// anyway would tell the agent to relay output that was never sanctioned.
func reportRendered(sessionID string) bool {
	session, err := monitor.GetActiveSession(sessionID)
	if err != nil || session == nil {
		return false
	}
	_, _, found, err := monitor.PendingRelayCheckpoint(session.ID)
	return err == nil && found
}

// handlePostToolUse never infers a state change from the TEXT of a Bash command
// (E-2177). It used to regex the whole command string for a claim or a confirm
// and perform the write itself, so a heredoc or commit message that merely
// named one bound a session into a write-once column or confirmed a task. The
// CLI and the event executor own those writes; `endless task claim` prints its
// own handoff.
func handlePostToolUse(projectID int64, isRegistered bool, payload claudePayload) error {
	// E-1803 Arm 1: reinforce the report channel right after a run that produced
	// something to relay.
	//
	// E-1953 fixed what this branch keys on. It used to fire on the command NAME,
	// so `endless task report --help` injected "now send that output verbatim"
	// after a run that rendered nothing. The reliable signal for "a render
	// succeeded" is the checkpoint the command writes on success: `--help`
	// writes none, a failed run writes none, and a successful one always does.
	// The regex survives only as a cheap prefilter so an ordinary Bash call does
	// not pay for a DB query.
	//
	// reportChannelOn comes FIRST, and is not an optimization. This instruction
	// asserts that "a Stop hook compares your final message against it" — a claim
	// about enforcement, not a request. In a project that set
	// `"report_gate": false` no Stop hook will compare anything, so firing here
	// would state something false to every session in that project (including
	// Endless's own, which is exactly where it was observed). An instruction may
	// outlive its enforcement; a factual claim about enforcement may not.
	if payload.ToolName == "Bash" && reportChannelOn(projectID, isRegistered, payload.CWD) {
		var input toolInputBash
		if err := json.Unmarshal(payload.ToolInput, &input); err == nil &&
			taskReportRe.MatchString(input.Command) && reportRendered(payload.SessionID) {
			return json.NewEncoder(os.Stdout).Encode(reportRelayResponse())
		}
	}

	// Check if a plan file was written
	if payload.ToolName != "Write" {
		return nil
	}

	var input toolInputWrite
	if err := json.Unmarshal(payload.ToolInput, &input); err != nil {
		return nil
	}

	// Check if this is a plan file
	if !isPlanFile(input.FilePath) {
		return nil
	}

	// NOTE: Auto-import disabled. Sessions should use `endless task update <id> --plan-file <file>`
	// to save a task's plan, and `endless task add` to create child items explicitly.
	// Auto-import created duplicate items at the wrong granularity (every bullet became a task item).

	items, err := monitor.GetActiveTasks(projectID)
	if err != nil {
		return dbReadFailed(fmt.Errorf("getting active tasks: %w", err))
	}

	return writeContextInjection(payload.EventName, fmt.Sprintf(
		"Plan file synced to Endless. %d active item(s) tracked.",
		len(items),
	))
}

// writeTools are the only tools that require task registration.
// Everything else (Read, Glob, Grep, Bash, etc.) passes through.
var writeTools = map[string]bool{
	"Write":        true,
	"Edit":         true,
	"NotebookEdit": true,
}

// extractFilePath pulls the target file path out of a write-tool's input.
// Write/Edit use "file_path"; NotebookEdit uses "notebook_path".
func extractFilePath(toolName string, raw json.RawMessage) string {
	var probe struct {
		FilePath     string `json:"file_path"`
		NotebookPath string `json:"notebook_path"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return ""
	}
	if probe.FilePath != "" {
		return probe.FilePath
	}
	return probe.NotebookPath
}

func handlePreToolUse(projectID int64, isRegistered bool, payload claudePayload) error {
	// E-1226: refuse `sqlite3 .endless/...` regardless of registration —
	// the antipattern is file-pattern-specific, not project-state-specific,
	// and the recovery cost (ghost DB files blocking worktree-land) is real.
	if payload.ToolName == "Bash" {
		blockSqliteAgainstEndlessIfApplicable(payload)

		// Worktree removal is refused regardless of registration, and ahead of
		// every other gate. The routes it matches are path- and verb-specific
		// rather than project-state-specific, and a session whose project
		// failed to resolve is if anything MORE likely to reach for a removal,
		// not less. Placed first among the Bash gates because it is the only
		// unconditional one — nothing below it can change the answer.
		blockWorktreeRemovalIfApplicable(payload)
	}

	// No enforcement for unregistered/anonymous projects
	if !isRegistered {
		return nil
	}

	// E-1012: block direct 'git commit' on main's working tree.
	// Independent of writeTools-based enforcement so it fires even when
	// per-task tracking is in 'off' mode.
	if payload.ToolName == "Bash" {
		blockCommitOnMainIfApplicable(payload)

		// E-1916 Arm 2: refuse a direct run of a landed, foreign task's
		// verification suite. Registered-only, and symmetric with Arm 1 below:
		// an unregistered project has no landings to protect, so the gate has
		// nothing to say there — and this way it costs no database read.
		blockLandedSuiteRunIfApplicable(payload)
	}

	// E-1586: cwd-invariant gate for ALL tools (no allowlist). If this session
	// owns a live (claimed) worktree but its cwd has drifted outside it, refuse
	// the tool and direct it to `/cd` so the *default* working directory is the
	// worktree, not main. Runs before the write-tool gate so Bash and every
	// other tool that defaults to cwd are covered, not just file writes.
	enforceClaimedCwd(projectID, payload)

	// E-1542: pause-on-revisit gate. Intercepts a session whose claimed task
	// descends from an epic in status='revisit'. Placed BEFORE the write-tool
	// early-return so it fires for all tool kinds (Read/Bash/Grep too), and runs
	// regardless of tracking_mode — a strategy revisit is coordination, not
	// claim-enforcement.
	enforceRevisitGate(payload)

	// Only enforce the remaining gates on file-writing tools.
	if !writeTools[payload.ToolName] {
		return nil
	}

	// E-1202/E-2137: refuse a direct Write/Edit of a task document mirror
	// (.endless/tasks/e-NNNN/{plan,outcome,analysis}.md) in main OR a worktree.
	// Placed before the worktree gate so a mirror write in main gets the
	// mirror-specific redirect to `endless task update` rather than the generic
	// "edits in main" refusal. Independent of tracking_mode.
	blockDocMirrorWriteIfApplicable(payload)

	// E-1916 Arm 1: refuse an edit of a landed, foreign task's verification
	// suite. Placed beside the plan-file gate for the same reason it is — both
	// name a specific path that must not be hand-edited, and both want their
	// own refusal to arrive ahead of the worktree gate's generic one.
	blockLandedSuiteEditIfApplicable(payload)

	// E-1983: the other half of the cwd invariant. enforceClaimedCwd (above,
	// all-tools) returns early on an UNBOUND session, so a session sitting IN a
	// task worktree while holding no task was the one broken state nothing spoke
	// to. Independent of tracking_mode, like the worktree gate it sits beside.
	//
	// WRITE TOOLS ONLY, and the distinction is the point. enforceClaimedCwd
	// covers all tools because a wrong cwd makes EVERY tool do the wrong thing —
	// Read opens the wrong file, Bash runs in the wrong directory. Here the cwd
	// is RIGHT and nothing is incorrect; what is wrong is that the work will be
	// attributed to no task. Only a write produces work to attribute, so only a
	// write is worth refusing. Reading code in a worktree you have not claimed
	// is an ordinary thing to do and is no longer refused.
	enforceUnboundWorktree(projectID, payload)

	// Worktree gate (E-971 Layer D). Independent of tracking_mode, like
	// E-1012: even with per-task tracking off, edits in main and edits
	// to a worktree owned by another session are refused.
	enforceWorktreeGate(projectID, payload)

	// Check tracking mode
	mode := monitor.GetTrackingMode(projectID)
	if mode != "enforce" {
		return nil
	}

	// The declaration gate (E-2093 rewrote what it asks).
	//
	// It used to admit `state == 'working'` and nothing else. That is a proxy
	// for the question it actually wants answered — "has this session declared
	// what it is working on?" — and the proxy expired at the end of every turn,
	// because `Stop` sets `idle` and (until E-2093) nothing set `working` back.
	// A session that finished one clean turn could never write again.
	//
	// `sessions.task_id` is the durable answer: it is set at claim, write-once
	// under ED-1560, and stays true for the session's lifetime. So the gate
	// admits a session that HOLDS A TASK and is live-and-acting — `working`, or
	// `idle` because its state has not caught up yet. A write from an idle
	// session is by definition mid-turn (writes only happen inside turns), so
	// the state is stale, not the agent. With the TouchSession wake in place
	// this idle arm is belt-and-braces; it is what keeps a wake that is somehow
	// missed from stranding anyone again.
	//
	// Two cases are still refused, and they are the ones the gate exists for:
	// a session that has declared nothing, and `needs_input`, which means a
	// human was asked something and has not answered.
	session, err := monitor.GetActiveSession(payload.SessionID)
	if err != nil {
		session = nil
	}
	if sessionMayWrite(session) {
		return nil
	}

	blockToolUse(declarationRefusal(projectID, session))
	return nil // unreachable, blockToolUse calls os.Exit
}

// sessionMayWrite is the declaration gate's admission rule, stated as the rule
// rather than as a list of states that happened to be enumerated.
//
// A session may write when it has DECLARED what it is working on and is
// live-and-acting. The declaration is `sessions.task_id` — set at claim,
// write-once under ED-1560, true for the session's lifetime. Acting is
// sessionstate.MayWrite, whose membership and its reasons live with the
// vocabulary rather than here.
//
// It reads a NAMED GROUP for a structural reason, not a stylistic one (E-2105).
// This was a `switch` with a silent `default: return false`, so a state added to
// the vocabulary joined the refused set without anyone deciding it should — and
// a session proposing to route Claude Code's permission prompts to `needs_input`
// nearly shipped exactly that, refusing the session's next write after the user
// answered. Asking the registry makes classifying a new state a thing somebody
// has to do, in the open, in one file.
//
// nil (no row could be read) is the undeclared case, as is a row holding no
// task: neither has declared anything, whatever state it is in.
func sessionMayWrite(s *monitor.SessionInfo) bool {
	if s == nil || s.TaskID == nil {
		return false
	}
	return sessionstate.Has(sessionstate.MayWrite, s.State)
}

// declarationRefusal composes the message for a write the declaration gate
// turned down. It exists so the two refusals stay DISTINCT: one message served
// both until E-2093, and it was wrong for one of them — an idle session holding
// a task was told it had "no active work session", which was false, and pointed
// at `task claim`, which refuses on status before it ever reaches the question
// of who holds the task.
//
// The rule both branches obey: name a command that works FROM THE STATE THAT
// PRODUCED THE REFUSAL. Nothing here offers `--force`; repairing a session
// field by demoting a task is the trade E-2093 removed.
//
// `session` is nil when no row could be read at all, which is the same
// undeclared case as a row holding no task.
func declarationRefusal(projectID int64, session *monitor.SessionInfo) string {
	var msg strings.Builder

	if session != nil && session.TaskID != nil {
		// Declared, but in a state that cannot write. Say which state, and do
		// not describe a state that was not checked — asserting `needs_input`
		// unconditionally here would be the same defect in a new place.
		fmt.Fprintf(&msg, "BLOCKED: this session holds E-%d but is in state '%s'.\n",
			*session.TaskID, session.State)
		msg.WriteString("The task IS declared — it is the session state that cannot write.\n\n")
		if session.State == sessionstate.NeedsInput {
			msg.WriteString("`needs_input` means you asked your user something and the answer " +
				"has not arrived.\nAsk again in your reply and wait for it; their next " +
				"message clears this state.\nThere is no command for you to run.\n")
		} else {
			msg.WriteString("End the turn and say so in your reply; your user's next message " +
				"clears this state.\nDo NOT re-claim the task — it is already yours, and " +
				"re-claiming repairs a session\nfield by changing a task's status.\n")
		}
		return msg.String()
	}

	projectName, _ := monitor.GetProjectName(projectID)
	items, _ := monitor.GetActiveTasks(projectID)

	fmt.Fprintf(&msg, "BLOCKED: this session has not declared a task in project '%s'.\n", projectName)
	msg.WriteString("Say what you're working on before changing it.\n\n")

	if len(items) > 0 {
		msg.WriteString("Available tasks:\n")
		limit := 10
		if len(items) < limit {
			limit = len(items)
		}
		for _, item := range items[:limit] {
			fmt.Fprintf(&msg, "  E-%d [%s] %s\n", item.ID, item.Status, item.Text)
		}
		if len(items) > 10 {
			fmt.Fprintf(&msg, "  ... and %d more\n", len(items)-10)
		}
		msg.WriteString("\n")
	}

	msg.WriteString("Run one of:\n")
	msg.WriteString("  endless task claim <id>   — start working on a specific task\n")
	msg.WriteString("  endless task show         — see all available tasks\n")

	return msg.String()
}

// blockToolUse writes an error to stderr and exits with code 2.
// Claude Code interprets exit code 2 as "action blocked" and feeds stderr
// back to Claude as context.
func blockToolUse(message string) {
	fmt.Fprint(os.Stderr, message)
	os.Exit(2)
}

// revisitClearVerbRe matches the user's revisit gate-clearing command so it is
// never blocked by the gate itself (E-1542): `endless task continue`,
// including path- or wrapper-prefixed forms such as `uv run endless task
// continue` or `/usr/local/bin/endless task continue`.
//
// E-1968 dropped `task pause` from the alternation along with the verb. Pausing
// is not an action: it is declining to clear the gate, which leaves the session
// blocked until the epic leaves `revisit` and the gate auto-clears. The verb
// only ever existed to carry an unbind that ED-1560's write-once
// `task_id` forbids.
//
// Anchored at cmdPos since E-2177, so a quoted `endless task continue` — an
// echo, a commit message — no longer lets one tool call past the gate. The one
// new miss is `bash -c "endless task continue"`: the gate blocks it and the
// agent runs the command plainly, which is the safe direction.
var revisitClearVerbRe = regexp.MustCompile(`(?i)` + cmdPos + `\bendless\s+task\s+continue\b`)

// clearsRevisitGate reports whether cmd runs the revisit gate's clearing verb,
// ignoring heredoc bodies (E-2177).
func clearsRevisitGate(cmd string) bool {
	return revisitClearVerbRe.MatchString(stripHeredocs(cmd))
}

// enforceRevisitGate intercepts a session whose claimed task descends from an
// epic currently in status='revisit' (E-1542). On the session's next tool call
// (any tool kind) it blocks and instructs Claude to surface an AskUserQuestion:
// continue under the current plan, or stop and wait for the strategy to be
// re-set. Only "continue" has a verb — `endless task continue` clears the gate;
// stopping means running nothing, and the gate auto-clears when the epic leaves
// `revisit`. No-op when the session has no resolvable active task, and never
// blocks the gate-clearing command itself.
func enforceRevisitGate(payload claudePayload) {
	if instruction, block := revisitGateDecision(payload); block {
		blockToolUseWithDecision(instruction)
	}
}

// revisitGateDecision is the side-effect-free core of enforceRevisitGate: it
// reads (and, when a gate fires for the first time, opens) the session's gate
// state and reports whether the tool call should be blocked and with what
// instruction. It does the gate-row writes (SetRevisitGate / auto-clear) but
// performs no stdout/exit — the caller owns the block emission — so it is unit
// testable against a seeded DB.
func revisitGateDecision(payload claudePayload) (instruction string, block bool) {
	// Never gate the user's own gate-clearing commands.
	if payload.ToolName == "Bash" {
		var input toolInputBash
		if err := json.Unmarshal(payload.ToolInput, &input); err == nil &&
			clearsRevisitGate(input.Command) {
			return "", false
		}
	}

	session, err := monitor.GetActiveSession(payload.SessionID)
	if err != nil || session == nil || session.TaskID == nil {
		return "", false
	}
	taskID := *session.TaskID

	// Already gated: re-check the epic's status before blocking again, so a gate
	// auto-clears the moment the epic leaves revisit.
	if epicID, found, perr := monitor.PendingRevisitGate(session.ID); perr == nil && found {
		if status, serr := monitor.GetTaskStatus(epicID); serr == nil && status != "revisit" {
			_, _ = monitor.ClearRevisitGate(session.ID, "revisit_resolved")
			return "", false
		}
		return revisitPromptInstruction(taskID, epicID), true
	}

	// Not yet gated: look for the nearest revisit epic ancestor.
	epicID, found, err := monitor.NearestRevisitEpicAncestor(taskID)
	if err != nil || !found {
		return "", false
	}
	if serr := monitor.SetRevisitGate(session.ID, epicID); serr != nil {
		log.Printf("set revisit gate for session %d: %v", session.ID, serr)
		return "", false
	}
	return revisitPromptInstruction(taskID, epicID), true
}

// revisitPromptInstruction is the instruction Claude reads when the gate fires.
func revisitPromptInstruction(taskID, epicID int64) string {
	return fmt.Sprintf(
		"Your active task E-%d is a descendant of epic E-%d, which the operator just set to "+
			"status=revisit. The strategy under which this task was planned is being "+
			"reconsidered. Surface this to the user as an AskUserQuestion with two options:\n\n"+
			"  - Continue under the current plan (then call `endless task continue`)\n"+
			"  - Stop working and wait for the strategy to be re-set\n\n"+
			"If the user picks the second option, run NO command: leaving this gate open "+
			"IS the pause. Stop calling tools, say you are paused on E-%d, and wait. The "+
			"gate clears itself the moment the epic leaves revisit.",
		taskID, epicID, taskID,
	)
}

// blockResponse builds the PreToolUse block response carrying the instruction
// in both reason and additionalContext (see preToolUseBlock). Shared by every
// gate that blocks as a JSON decision rather than stderr+exit-2 — the revisit
// gate (E-1542) and the unbound-in-a-worktree gate (E-1983).
func blockResponse(instruction string) preToolUseBlock {
	return preToolUseBlock{
		Decision: "block",
		Reason:   instruction,
		HookSpecificOutput: hookContextOutput{
			HookEventName:     "PreToolUse",
			AdditionalContext: instruction,
		},
	}
}

// blockToolUseWithDecision emits the PreToolUse block response (decision
// "block" + reason + additionalContext) and exits 0. If encoding fails it falls
// back to the always-works stderr+exit-2 form.
func blockToolUseWithDecision(instruction string) {
	if err := json.NewEncoder(os.Stdout).Encode(blockResponse(instruction)); err != nil {
		blockToolUse(instruction)
		return
	}
	os.Exit(0)
}

// latestPlanFile returns the most recently modified *.md under ~/.claude/plans,
// or "" when the directory is unreadable or holds no plan. Claude writes the
// accepted plan there, so the newest entry is the one ExitPlanMode just fired
// for.
func latestPlanFile() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	entries, err := os.ReadDir(filepath.Join(home, ".claude", "plans"))
	if err != nil {
		return ""
	}
	var newest string
	var newestTime int64
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Unix() > newestTime {
			newestTime = info.ModTime().Unix()
			newest = filepath.Join(home, ".claude", "plans", e.Name())
		}
	}
	return newest
}

func handleExitPlanMode(projectID int64, payload claudePayload) error {
	// The most recently modified file in ~/.claude/plans is the plan just
	// accepted. E-2074 removed sessions.plan_file_path, which used to be
	// consulted first: it was written at PostToolUse/Write and read only here,
	// and the mtime scan below was already the fallback for every session that
	// reached ExitPlanMode without having written through the Write tool. With
	// auto-import disabled (see below) neither branch consumed the path for
	// anything but this presence test, so the column bought a write on every
	// plan-file Write and answered a question the directory already answers.
	if latestPlanFile() == "" {
		return nil
	}

	// NOTE: Auto-import disabled. Sessions save plan text explicitly with
	// `endless task update <id> --plan-file <plan-file>`.
	// See PostToolUse/Write handler for rationale.

	items, err := monitor.GetActiveTasks(projectID)
	if err != nil {
		return dbReadFailed(fmt.Errorf("getting active tasks: %w", err))
	}

	return writeContextInjection(payload.EventName, fmt.Sprintf(
		"Plan accepted and synced to Endless. %d active item(s) tracked.",
		len(items),
	))
}

// gitCommitRe matches `git commit` in ONE command of a Bash call (see
// splitCommands), capturing the path of an optional `git -C <path>`. The prefix
// may not cross a quote, which is cmdPos's rule applied per command: it admits
// `GIT_EDITOR=true git commit` and refuses `echo "git commit"`. Excludes
// `git commit-tree` (the trailing boundary requires whitespace or end).
var gitCommitRe = regexp.MustCompile(`^[^'"]*\bgit\s+(?:-C\s+(` + shellWord + `)\s+)?commit(?:\s|$)`)

// cdRe matches a command that is exactly `cd [path]`, capturing the path.
var cdRe = regexp.MustCompile(`^\s*cd(?:\s+(` + shellWord + `))?\s*$`)

// shellWord is one shell word: single-quoted, double-quoted, or bare.
const shellWord = `'[^']*'|"[^"]*"|[^\s'";&|]+`

// The recognizer for a DB-owned document mirror lives in internal/docmirror —
// the one place the path convention is spelled. This gate was E-1202's, written
// when the only mirror was `.endless/plans/E-NNN.md`; E-2137 consolidated all
// three task kinds into `.endless/tasks/e-NNNN/` and made them main-bound, so
// the gate follows the convention rather than restating it.
//
// Why it matters MORE after the move: `.endless/tasks/e-NNNN/` is also where a
// session writes its own verification suite, so this directory now holds files
// of both kinds side by side. docmirror.TaskDocRe names the database-owned
// stems exactly and nothing else, which is what keeps a
// session's own `verify.sh` writable.

// sqliteEndlessRe matches sqlite3 invocations targeting any path inside
// a .endless/ directory. The character class [^|;&\n] stops the match at
// command-pipeline boundaries; [ /] before \.endless/ ensures the
// pattern is a path component (avoids false-positive on names like
// my.endless/x). Case-insensitive (?i) catches uppercase variants.
//
// Anchored at cmdPos since E-2177. Unanchored, it refused plain MENTIONS — a
// commit message saying never to point the sqlite3 CLI at a .endless/ database
// was refused as though it were one. A leading `cd <path> &&` still matches,
// because `&&` is a command boundary.
var sqliteEndlessRe = regexp.MustCompile(`(?i)` + cmdPos + `\bsqlite3[^|;&\n]*[ /]\.endless/`)

// cmdPos anchors a match to a COMMAND position: the start of the command, or
// just after a pipeline separator or newline, followed by any run of characters
// that contains no quote and no further separator.
//
// The quote exclusion is what makes the difference between INVOKING a removal
// and MENTIONING one. Reaching a verb inside `echo '... endless worktree drop'`
// requires crossing the opening quote, so it does not match; reaching it in
// `cd /tmp && endless worktree drop E-7` does not, so it does. It also admits
// wrapper prefixes for free — `uv run endless …`, `/usr/local/bin/endless …`,
// `./bin/endless-go …` — without enumerating wrappers.
//
// This matters more here than anywhere else in this file: the handoff templates,
// the guide and this task's own verify script all QUOTE these commands in order
// to forbid them. A gate that blocked the writing of its own documentation would
// be discovered on its first day and routed around thereafter.
const cmdPos = `(?:^|[;&|\n])[^'"|;&\n]*`

// worktreeRemovalRes are the routes to a removed worktree. They are matched as
// a set rather than one command because the rule has to name the OUTCOME: a
// prohibition on `drop` alone is honoured by a session that reaches for `reap`,
// or for `git worktree remove`, or for `rm -rf` — and the worktree is just as
// gone. Whichever route the session picks, the recovery cost is the same.
//
// `\b` after the verb, not `($|\s)`, so a trailing flag or id still matches.
var worktreeRemovalRes = []*regexp.Regexp{
	// `endless worktree drop|reap`, and the endless-go spelling.
	regexp.MustCompile(`(?i)` + cmdPos + `\bendless(-go)?\s+worktree\s+(drop|reap)\b`),
	// `git worktree remove`, including `git -C <path> worktree remove`.
	// `prune` is here too: it deletes the admin state that makes a worktree
	// recoverable after its directory is gone.
	regexp.MustCompile(`(?i)` + cmdPos + `\bgit\s+(-C\s+\S+\s+)?worktree\s+(remove|prune)\b`),
	// `rm -r` of a worktree DIRECTORY. The path must end at the worktree
	// segment — `.endless/worktrees/e-123` or with a trailing slash, but not
	// `.endless/worktrees/e-123/build`. Deleting something inside a worktree is
	// ordinary work; a false block there would train the session to route
	// around the gate, which is the one failure this must not have.
	regexp.MustCompile(`(?i)` + cmdPos + `\brm\s+[^'"|;&\n]*-[a-z]*r[a-z]*\s[^'"|;&\n]*\.endless/worktrees/[^\s/'"|;&]+/?(?:$|[\s'"|;&])`),
}

// removesWorktree reports whether cmd runs any route in worktreeRemovalRes.
// Heredoc bodies are stripped first: a line of documentation that begins with a
// removal command is not one (E-2177).
func removesWorktree(cmd string) bool {
	cmd = stripHeredocs(cmd)
	for _, re := range worktreeRemovalRes {
		if re.MatchString(cmd) {
			return true
		}
	}
	return false
}

// blockWorktreeRemovalIfApplicable refuses any Bash call that would remove a
// worktree. Unlike every other gate here it is CATEGORICAL: it does not consult
// cwd, task state, or registration, and it names no bypass.
//
// That is the whole point. The rule used to live only in the handoff prose, as
// "don't run `endless worktree land`/`drop` without asking" — a precondition the
// session had to evaluate. On 2026-08-25 two sessions ten minutes apart removed
// worktrees. Neither OVERRODE the rule; both concluded that something said in
// conversation had satisfied the precondition. One caused real damage. A gate
// with a bypass would reproduce exactly that: a session that can talk itself
// into "I was asked" can equally talk itself into "this is the case the bypass
// is for".
//
// The capability is not lost, only moved off the agent's tool path. Hooks fire
// on a Claude session's Bash tool, so the person running the session removes a
// worktree by typing it in their own shell, and the reaper reclaims stale ones
// as an endless subprocess this hook never sees. Retention is the design; the
// cost of an agent that cannot remove a worktree is a worktree that outlives
// its usefulness for a while, against a defect whose recovery cost is real.
//
// Deliberately NOT scoped to worktree-bound sessions. A session in the main
// checkout removing someone else's worktree does the same damage, and "am I the
// spawning session?" is one more precondition to mis-evaluate.
func blockWorktreeRemovalIfApplicable(payload claudePayload) {
	var input toolInputBash
	if err := json.Unmarshal(payload.ToolInput, &input); err != nil {
		return
	}
	if !removesWorktree(input.Command) {
		return
	}
	blockToolUse(`BLOCKED: refusing to remove a worktree.

Removing a worktree is not something an agent session does — not ` +
		"`endless worktree drop`" + `, not ` + "`endless worktree reap`" + `, not
` + "`git worktree remove`" + `, not ` + "`rm -r`" + ` on the directory.

Retention is the design. Landing keeps the worktree and its branch, so a
reopened task still has one, and stale worktrees are reclaimed automatically
after a grace period. A retained worktree is recovery state, not leftover mess.

If a branch's history has diverged from main, fix the BRANCH in place:

  git -C <worktree> rebase main          # replay the branch on current main
  git -C <worktree> reset --hard main    # discard its commits, keep the worktree

Both leave the directory — and whoever is working in it — intact.

If removal genuinely looks warranted, say so once and stop. Whoever is running
this session removes it themselves; there is no flag here that lets you do it.`)
}

// sqliteAgainstEndless reports whether cmd runs sqlite3 against a path inside
// a .endless/ directory, ignoring heredoc bodies (E-2177).
func sqliteAgainstEndless(cmd string) bool {
	return sqliteEndlessRe.MatchString(stripHeredocs(cmd))
}

// blockSqliteAgainstEndlessIfApplicable refuses Bash calls that invoke
// sqlite3 against any path inside a .endless/ directory. Such paths
// rarely exist, and sqlite3 silently creates 0-byte ghost DB files when
// the target is missing — those files then block `endless worktree land`
// until they're cleaned up. (E-1226; recurring agent antipattern.) The
// real DB lives at ~/.config/endless/endless.db; `endless sql` resolves
// it without exposing the path.
func blockSqliteAgainstEndlessIfApplicable(payload claudePayload) {
	var input toolInputBash
	if err := json.Unmarshal(payload.ToolInput, &input); err != nil {
		return
	}
	if !sqliteAgainstEndless(input.Command) {
		return
	}
	blockToolUse(
		"BLOCKED: refusing `sqlite3` against a path inside `.endless/`.\n\n" +
			"The Endless DB lives at `~/.config/endless/endless.db`. " +
			"Running sqlite3 against speculative `.endless/...` paths " +
			"silently creates 0-byte ghost DB files that block " +
			"`endless worktree land`.\n\n" +
			"Use this instead:\n" +
			"  endless sql \"<query>\"             # read-only by default\n" +
			"  endless sql --write \"<query>\"     # mutations require --write\n",
	)
}

// commitRunsOnMain reports whether cmd runs `git commit` in main's working
// tree outside an active merge. cwd is the session's working directory.
//
// It judges the directory the commit RUNS in (E-2177), not the session's cwd.
// Until then the match was anchored to the start of the whole command, so
// `cd <path> && git commit` — the form an agent uses most — was never examined.
// Widening the match alone would have been worse: judged against the session's
// cwd it would refuse a legitimate `cd <worktree> && git commit` from a session
// sitting in main, and still miss `cd <main> && git commit` from one sitting in
// a worktree.
func commitRunsOnMain(cmd, cwd string) bool {
	dir, ok := commitDir(cmd, cwd)
	if !ok || !filepath.IsAbs(dir) {
		return false
	}
	inMain, err := isInMainCheckout(dir)
	if err != nil || !inMain {
		// Not a git repo, git unavailable, or in a worktree — allow.
		return false
	}
	// Merge in progress; the merge commit is part of completing the merge.
	return !isInActiveMerge(dir)
}

// commitDir returns the directory the first `git commit` in cmd runs in, and
// whether cmd commits at all. It starts at cwd, applies each `cd` that runs
// before the commit, in order, then the commit's own `git -C <path>`. Relative
// paths resolve against the directory reached so far; `~` expands.
//
// Heredoc bodies are stripped first, and each command is matched on its own, so
// a commit named in a quoted argument or a heredoc is not a commit.
//
// A `cd` made in an EARLIER Bash call is not in this command's text, and need
// not be: the payload's cwd follows the shell's persisted directory (observed
// 2026-09-26 — after `cd internal` in one call, the next call's recorded cwd was
// `<worktree>/internal`).
func commitDir(cmd, cwd string) (string, bool) {
	dir := cwd
	for _, c := range splitCommands(stripHeredocs(cmd)) {
		if m := gitCommitRe.FindStringSubmatch(c); m != nil {
			if m[1] != "" {
				dir = resolveDir(dir, unquoteWord(m[1]))
			}
			return dir, true
		}
		if m := cdRe.FindStringSubmatch(c); m != nil {
			target := unquoteWord(m[1])
			switch target {
			case "-":
				// The previous directory is not knowable from here; keep ours.
			case "":
				dir = resolveDir(dir, "~")
			default:
				dir = resolveDir(dir, target)
			}
		}
	}
	return "", false
}

// splitCommands splits a Bash command at its unquoted command separators —
// `;`, `&`, `|` and newline — dropping empty pieces, so `a && b` yields a and b.
// Quotes and backslash escapes are honored so a separator inside a commit
// message does not split it.
func splitCommands(cmd string) []string {
	var (
		out   []string
		cur   strings.Builder
		quote rune
		esc   bool
	)
	flush := func() {
		if s := strings.TrimSpace(cur.String()); s != "" {
			out = append(out, s)
		}
		cur.Reset()
	}
	for _, r := range cmd {
		switch {
		case esc:
			esc = false
		case r == '\\' && quote != '\'':
			esc = true
		case quote != 0:
			if r == quote {
				quote = 0
			}
		case r == '\'' || r == '"':
			quote = r
		case r == ';' || r == '&' || r == '|' || r == '\n':
			flush()
			continue
		}
		cur.WriteRune(r)
	}
	flush()
	return out
}

// unquoteWord strips one layer of matching single or double quotes.
func unquoteWord(w string) string {
	if len(w) >= 2 && (w[0] == '\'' || w[0] == '"') && w[len(w)-1] == w[0] {
		return w[1 : len(w)-1]
	}
	return w
}

// resolveDir resolves p against base, expanding a leading `~`. A relative p
// against an empty base stays relative, which commitRunsOnMain treats as
// unknown.
func resolveDir(base, p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		p = filepath.Join(home, strings.TrimPrefix(p, "~"))
	}
	if filepath.IsAbs(p) || base == "" {
		return filepath.Clean(p)
	}
	return filepath.Join(base, p)
}

// blockCommitOnMainIfApplicable inspects a Bash tool call. If it's a
// 'git commit' invoked from main's working tree (not a worktree, not
// during an active merge), block with an actionable message. Otherwise
// return silently and let the command run.
func blockCommitOnMainIfApplicable(payload claudePayload) {
	var input toolInputBash
	if err := json.Unmarshal(payload.ToolInput, &input); err != nil {
		return
	}
	if !commitRunsOnMain(input.Command, payload.CWD) {
		return
	}

	blockToolUse(`Direct commits to main are highly discouraged when using endless.

main is the integration target. Make changes in a worktree on a per-task
branch, then merge via ` + "`endless worktree land <task-id>`" + `.

If you have an Endless task for this work:
  endless task claim E-NNN          # creates worktree at .endless/worktrees/e-NNN

Or by hand:
  git worktree add -b task/NNN .endless/worktrees/e-NNN main
  cd .endless/worktrees/e-NNN
  # ... do work, commit ...
  endless worktree land E-NNN

Bypass (NOT recommended):
  git commit --no-verify`)
}

// blockDocMirrorWriteIfApplicable refuses any Write/Edit/NotebookEdit whose
// target is a task document mirror — `.endless/tasks/e-NNNN/{plan,outcome,
// analysis}.md`, or a legacy `.endless/{plans,outcomes,analyses}/E-NNNN.md` a
// tree has not been swept into the new layout yet. Mirror content lives in the
// `tasks` row (the source of truth); the .md file is written and committed on
// main for you so humans can read it on GitHub. A direct tool-write leaves the
// database stale and is overwritten without warning by the next sweep. The CLI
// writer (`endless task update --plan-file`) is a subprocess the hook never
// sees, so the intended route is unaffected. Independent of tracking_mode, like
// the worktree and commit-on-main gates.
//
// Decision mirrors are deliberately NOT gated here, matching E-1202's scope:
// this fires on the paths an agent actually reaches for while working a task.
func blockDocMirrorWriteIfApplicable(payload claudePayload) {
	path := extractFilePath(payload.ToolName, payload.ToolInput)
	if path == "" {
		return
	}
	if !docmirror.TaskDocRe.MatchString(path) && !docmirror.LegacyTaskDocRe.MatchString(path) {
		return
	}
	blockToolUse(docMirrorBlockMessage())
}

// docMirrorBlockMessage is the refusal blockDocMirrorWriteIfApplicable prints.
// The stems and the --<name>-file flags are read from docmirror.TaskKinds, the
// list the recognizer itself is built from, so the message names exactly the
// files the gate refuses — a new content kind shows up here with no edit.
func docMirrorBlockMessage() string {
	stems := make([]string, len(docmirror.TaskKinds))
	var flags strings.Builder
	for i, k := range docmirror.TaskKinds {
		stems[i] = k.Stem
		fmt.Fprintf(&flags, "  endless task update <id> --%s-file .endless/tmp/<file>.md\n", k.Stem)
	}
	return "BLOCKED: refusing a direct Write/Edit of a task document mirror " +
		"(.endless/tasks/e-NNNN/{" + strings.Join(stems, ",") + "}.md). That content " +
		"lives in the database as the task's content — one row per name — and " +
		"the file is a projection of it that endless writes and commits on main " +
		"for you, so humans can read it when reviewing the repo on GitHub or " +
		"other Git hosts. Editing it directly leaves the database stale, and the " +
		"next sweep rewrites the file from the database without warning.\n\n" +
		"Author the content under .endless/tmp/ (the project-local scratch " +
		"dir), then run one of:\n" +
		flags.String() + "\n" +
		"(the --*-file forms load the file's content; --plan would store the " +
		"path string itself. Use the inline forms only for short content.)\n\n" +
		"Your task's own verify.sh in that same directory IS yours to write — " +
		"only these .md files are the database's.\n\n" +
		"Never hand-edit or git-commit a mirror yourself."
}

// isInMainCheckout returns true if cwd is inside the main checkout of a git
// repository (as opposed to a linked worktree). Detection: --git-dir and
// --git-common-dir return the same path in main, different paths in a worktree.
func isInMainCheckout(cwd string) (bool, error) {
	gitDir, err := runGitRevParse(cwd, "--git-dir")
	if err != nil {
		return false, err
	}
	commonDir, err := runGitRevParse(cwd, "--git-common-dir")
	if err != nil {
		return false, err
	}
	return absFromCwd(cwd, gitDir) == absFromCwd(cwd, commonDir), nil
}

// isInActiveMerge returns true if a merge is currently in progress in cwd's
// repository (detected by the presence of MERGE_MSG in the git directory).
func isInActiveMerge(cwd string) bool {
	gitDir, err := runGitRevParse(cwd, "--git-dir")
	if err != nil {
		return false
	}
	_, err = os.Stat(filepath.Join(absFromCwd(cwd, gitDir), "MERGE_MSG"))
	return err == nil
}

func runGitRevParse(cwd, arg string) (string, error) {
	cmd := exec.Command("git", "rev-parse", arg)
	cmd.Dir = cwd
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func absFromCwd(cwd, path string) string {
	p := path
	if !filepath.IsAbs(p) {
		p = filepath.Join(cwd, p)
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return abs
}

func isPlanFile(path string) bool {
	lower := strings.ToLower(path)
	if strings.Contains(lower, "/.claude/plans/") {
		return true
	}
	if strings.HasSuffix(lower, "/plan.md") {
		return true
	}
	return false
}

// tmuxTaskID reads @endless_task_id from the current tmux window.
// Returns 0 if not in tmux or not set.
// tmuxTaskID is a package var (not a plain func) so tests can stub the tmux
// read to simulate the SessionStart marker-read race (E-1700).
var tmuxTaskID = func() int64 {
	pane := os.Getenv("TMUX_PANE")
	if pane == "" {
		return 0
	}
	out, err := exec.Command(
		"tmux", "display-message", "-p", "-t", pane, "#{@endless_task_id}",
	).Output()
	if err != nil {
		return 0
	}
	val := strings.TrimSpace(string(out))
	if val == "" {
		return 0
	}
	id, err := strconv.ParseInt(val, 10, 64)
	if err != nil {
		return 0
	}
	return id
}

// setTmuxSessionUUID publishes the Claude session UUID to the current tmux
// window as the @endless_session_uuid window option (E-1585). Window options
// are shared by every pane in the window, so a sibling shell pane (which has no
// CLAUDECODE / CLAUDE_CODE_SESSION_ID env of its own) can read this to discover
// the Claude session it sits next to and resolve/populate the session row in
// its --db sandbox context. Best-effort: no-op when not in tmux or sessionID is
// empty; tmux errors are ignored. Called every event so the option self-heals
// after a tmux server restart, mirroring TouchSession's per-event upsert.
func setTmuxSessionUUID(sessionID string) {
	pane := os.Getenv("TMUX_PANE")
	if pane == "" || sessionID == "" {
		return
	}
	_ = exec.Command(
		"tmux", "set", "-w", "-t", pane, "@endless_session_uuid", sessionID,
	).Run()
}

// logWindowTaskDisagreement records — without acting on it — a SessionStart
// whose tmux window names a different task than its working directory does.
//
// This is what is left of trySpawnBind (E-1983). Acting on the window option is
// precisely the bug: `task spawn` writes @endless_task_id on the window it
// creates, nothing ever clears it, and a window that outlives its session hands
// the next session a stale claim. Observing it is still worth doing, because the
// disagreement is the fingerprint of a reused spawn window and it took three
// tasks to identify it from the outside; a log line spends nothing and names it
// on sight.
//
// Silent when the window has no claim (the ordinary hand-opened window), when
// the project does not resolve, and when the two agree — which is every spawned
// worker, since spawn opens the window with `-c <worktree>`.
func logWindowTaskDisagreement(projectID int64, payload claudePayload) {
	windowTask := tmuxTaskID()
	if windowTask <= 0 {
		return
	}
	projectRoot, err := monitor.ProjectPath(projectID)
	if err != nil {
		return
	}
	cwdTask := resolveCwdTaskID(projectRoot, payload.CWD)
	if cwdTask == windowTask {
		return
	}
	log.Printf(
		"session %s: tmux window says task E-%d but cwd %s says %s — cwd wins; "+
			"the window option is stale, and `endless session resume <ref>` "+
			"rewrites it on the pane it relaunches in",
		payload.SessionID, windowTask, payload.CWD, taskIDOrNone(cwdTask),
	)
}

// taskIDOrNone renders a resolveCwdTaskID result for a log line: "E-NNN", or
// "no task" for the 0 that means cwd is not inside a task worktree.
func taskIDOrNone(taskID int64) string {
	if taskID <= 0 {
		return "no task"
	}
	return fmt.Sprintf("E-%d", taskID)
}

// logSessionBind records one BindSessionToTask transition in the machine-local
// diagnostic log. new_state is always `working` — that is what BindSessionToTask
// sets on a successful bind. Best-effort; never returns an error.
func logSessionBind(sessionID string, snap monitor.SessionSnapshot, taskID int64, reason monitor.SessionLogReason, caller string) {
	newTaskID := taskID
	monitor.LogSessionTxn(monitor.SessionTxn{
		SessionGUID: sessionID,
		OldState:    snap.State,
		NewState:    sessionstate.Working,
		OldTaskID:   snap.TaskID,
		NewTaskID:   &newTaskID,
		Reason:      reason,
		Caller:      caller,
	})
}

// maybeCwdBind runs the cwd-derived SessionStart auto-bind. Since E-1983 it is
// the ONLY path that writes sessions.task_id at SessionStart: payload.CWD is the
// worktree for a spawned worker (`tmux new-window -c <worktree>`) and the
// directory the user actually started in for everyone else, which makes it the
// one signal that cannot be stale. It used to be gated on the spawn-marker path
// having declined to bind (E-1700's !spawnBound); there is no longer a
// spawn-marker path to defer to.
//
// Skipped for Agent-tool subagents — they share the parent's cwd but represent
// tool use, not user claim intent; binding them would create a phantom co-owner.
// Skipped for background agents (E-1568): their dispatch row already carries
// task_id/epic_id, and the tmux-oriented bind is meaningless for a headless
// agent. Bind only; task status is unchanged.
func maybeCwdBind(projectID int64, payload claudePayload) {
	if payload.AgentID != "" || os.Getenv("CLAUDE_JOB_DIR") != "" {
		return
	}
	autoBindFromCwd(projectID, payload)
}

// autoBindFromCwd implements the E-1291 cwd-based SessionStart auto-bind.
// If payload.CWD is inside an endless worktree, read the companion
// file's task_id and bind the session. Best-effort: any failure along
// the way (no project root, no worktree, missing companion, malformed
// task_id, DB write error) results in no binding — the user can still
// run `endless task claim` to bind explicitly. Caller must already
// have screened out subagents.
func autoBindFromCwd(projectID int64, payload claudePayload) {
	projectRoot, err := monitor.ProjectPath(projectID)
	if err != nil {
		return
	}
	taskID := resolveCwdTaskID(projectRoot, payload.CWD)
	if taskID <= 0 {
		return
	}
	// E-1856: the auto-bind is a fallback to fill an UNBOUND session's
	// task_id from its cwd worktree — never a re-pointer. A resume
	// (`claude --resume`), /clear, or /compact fires SessionStart with the
	// session's existing binding intact; when the resumed process's cwd is a
	// DIFFERENT task's worktree, overwriting task_id would silently steal
	// the session away from the task it belongs to, leaving that task
	// unreachable via `session goto`/`session resume`. Only bind when the
	// session has no active task yet (the E-1291/E-1700 fallback case) or
	// already points at this worktree's task (idempotent).
	if session, err := monitor.GetActiveSession(payload.SessionID); err == nil &&
		session != nil && session.TaskID != nil && *session.TaskID != taskID {
		return
	}
	// E-1856: never bind into a worktree a LIVE sibling session already owns.
	// handleWorktreeAdoption refuses this case upstream and short-circuits
	// SessionStart, but the auto-bind must be correct in isolation rather than
	// trusting that call order — otherwise any path that reaches it (or a future
	// re-order) would silently make the incoming session a phantom co-owner of
	// the task, stealing its task_id pointer.
	if worktreeOwnedByLiveOther(projectRoot, payload.CWD, payload.SessionID) {
		return
	}
	snap := monitor.SnapshotSession(payload.SessionID)
	if err := monitor.BindSessionToTask(payload.SessionID, projectID, taskID); err != nil {
		return
	}
	logSessionBind(payload.SessionID, snap, taskID, monitor.SessionLogCwdBind, "hookcmd.autoBindFromCwd")
}

// worktreeOwnedByLiveOther reports whether the worktree containing cwd holds a
// worktree lock owned by a DIFFERENT, still-alive session. It gates the cwd
// auto-bind (E-1856). Returns false when cwd is not inside a worktree, the lock
// is absent or stale, or the lock is held by selfSession — none of which
// represent a live sibling owner to defer to.
func worktreeOwnedByLiveOther(projectRoot, cwd, selfSession string) bool {
	worktreeRoot, err := monitor.FindWorktreeRoot(cwd, projectRoot)
	if err != nil || worktreeRoot == "" {
		return false
	}
	lock, err := monitor.ReadWorktreeLock(worktreeRoot)
	if err != nil || lock == nil {
		return false
	}
	if lock.SessionID == selfSession {
		return false
	}
	return !monitor.IsWorktreeLockStale(lock)
}

// resolveCwdTaskID walks up from cwd looking for an endless worktree
// directory and returns its task ID (encoded in the directory name) as
// int64. Pure filesystem; no DB access, no side effects — safe to test
// without infrastructure.
//
// E-1301: derives the task ID from the path convention
// (`.endless/worktrees/e-NNN`), not from the companion file's task_id
// field. The companion's mere existence is the "endless-managed marker"
// (FindWorktreeRoot's check); the path encodes the identity.
//
// Returns 0 when cwd isn't inside an endless worktree, or when the
// worktree path doesn't follow the convention.
func resolveCwdTaskID(projectRoot, cwd string) int64 {
	wtRoot, err := monitor.FindWorktreeRoot(cwd, projectRoot)
	if err != nil || wtRoot == "" {
		return 0
	}
	tidStr := monitor.TaskIDFromWorktreePath(wtRoot)
	if tidStr == "" {
		return 0
	}
	taskID, err := parseEndlessTaskID(tidStr)
	if err != nil || taskID <= 0 {
		return 0
	}
	return taskID
}

// --- E-971 Layer D: worktree adoption + enforcement helpers ----------------

// handleWorktreeAdoption is called from SessionStart. It walks up from
// payload.CWD to find a worktree companion, then either claims the lock
// (case A: unowned or stale, or self-re-entry idempotent), or returns a
// refusal message (case A: owned by another live session). Returns ("", nil)
// when there is nothing to adopt (case B: cwd is in main, foreign, or
// elsewhere) — the caller proceeds normally.
func handleWorktreeAdoption(projectID int64, payload claudePayload) (string, error) {
	projectRoot, err := monitor.ProjectPath(projectID)
	if err != nil {
		return "", fmt.Errorf("project path: %w", err)
	}
	worktreePath, err := monitor.FindWorktreeRoot(payload.CWD, projectRoot)
	if err != nil {
		return "", fmt.Errorf("find worktree root: %w", err)
	}
	if worktreePath == "" {
		// Case B: cwd is in main, in a foreign worktree, or unrelated.
		// Layer D does not auto-create. PreToolUse will block edits
		// when they're attempted and provide a helpful message.
		return "", nil
	}

	// Case A: cwd is inside an endless-managed worktree.
	existing, err := monitor.ReadWorktreeLock(worktreePath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("read existing lock: %w", err)
	}

	tryClaim := func() error {
		newLock := monitor.WorktreeLock{
			SessionID: payload.SessionID,
			PID:       os.Getppid(),
			TmuxPane:  os.Getenv("TMUX_PANE"),
			ClaimedAt: time.Now().UTC().Format(time.RFC3339),
		}
		return monitor.ClaimWorktreeLock(worktreePath, newLock)
	}

	switch {
	case existing == nil:
		// No lock — claim it.
		if err := tryClaim(); err != nil && !errors.Is(err, os.ErrExist) {
			return "", fmt.Errorf("claim worktree lock: %w", err)
		}
		// If a parallel session raced and won, fall through to next
		// SessionStart; we don't loop here.
		return "", nil

	case existing.SessionID == payload.SessionID:
		// Idempotent re-entry (Claude resume). Nothing to do.
		return "", nil

	case monitor.IsWorktreeLockStale(existing):
		// Stale lock — release and reclaim.
		if err := monitor.ReleaseWorktreeLock(worktreePath); err != nil {
			return "", fmt.Errorf("release stale lock: %w", err)
		}
		if err := tryClaim(); err != nil && !errors.Is(err, os.ErrExist) {
			return "", fmt.Errorf("claim worktree lock after stale release: %w", err)
		}
		return "", nil

	default:
		// Owned by a live session. Refuse with an actionable message.
		return fmt.Sprintf(
			"This worktree is already owned by session %s (PID %d).\n\n"+
				"Open a new shell in a different worktree (or in main) and start\n"+
				"a Claude session there. The owning session must end before this\n"+
				"worktree can be reclaimed.\n\n"+
				"  endless worktree current\n"+
				"  endless worktree list",
			existing.SessionID, existing.PID), nil
	}
}

// enforceWorktreeGate runs the four PreToolUse worktree checks. Any
// violation calls blockToolUse (which exits the process with code 2).
// Returns silently if all checks pass; the caller then continues with
// existing tracking-mode and session enforcement.
func enforceWorktreeGate(projectID int64, payload claudePayload) {
	projectRoot, err := monitor.ProjectPath(projectID)
	if err != nil {
		// Without a project root we cannot evaluate; let the call proceed
		// and rely on existing checks.
		return
	}
	worktreePath, _ := monitor.FindWorktreeRoot(payload.CWD, projectRoot)
	session, _ := monitor.GetActiveSession(payload.SessionID)

	if worktreePath == "" {
		// cwd has no worktree companion. Distinguish main from foreign.
		inMain, _ := isInMainCheckout(payload.CWD)
		if !inMain {
			// Foreign or unrelated tree — leave it alone; existing checks apply.
			return
		}
		var redirectHint string
		if session != nil && session.TaskID != nil {
			if wp, _ := monitor.WorktreePathForTask(projectID, *session.TaskID); wp != "" {
				redirectHint = fmt.Sprintf(
					"\n\nYour active task E-%d has a worktree at:\n  %s\n\n"+
						"Run `cd %s` in a Bash call (the new cwd persists for\n"+
						"subsequent Bash calls), and use absolute paths under\n"+
						"that directory for Read/Write/Edit.",
					*session.TaskID, wp, wp)
			}
		}
		blockToolUse("Edits in main are highly discouraged when using endless.\n\n" +
			"main is the integration target — every edit ideally should go through\n" +
			"a worktree.\n\n" +
			"If you do not yet have an active task, create one and start it:\n" +
			"  endless task add \"<title>\"\n" +
			"  endless task claim E-NNN          # auto-creates the worktree\n\n" +
			"If you already have an active task without a worktree:\n" +
			"  endless task claim E-NNN          # idempotent; creates if missing\n\n" +
			"Or create the worktree by hand or via `endless pivot` (when available):\n" +
			"  git worktree add -b task/NNN .endless/worktrees/e-NNN main" +
			redirectHint)
		return
	}

	// We are inside an endless-managed worktree. Three checks.

	// (a) Lock-owner check: refuse if the lock is owned by a different session.
	lock, err := monitor.ReadWorktreeLock(worktreePath)
	if err == nil && lock != nil && lock.SessionID != payload.SessionID {
		ownerHint := fmt.Sprintf("session %s (PID %d)", lock.SessionID, lock.PID)
		if monitor.IsWorktreeLockStale(lock) {
			ownerHint += " [stale]"
		}
		blockToolUse(fmt.Sprintf(
			"This worktree is owned by %s, not this session.\n\n"+
				"Restart this Claude session inside this worktree (a fresh SessionStart\n"+
				"reclaims a stale lock), or move to a different worktree.\n\n"+
				"  endless worktree current\n"+
				"  endless worktree list",
			ownerHint))
	}

	// (b) Task mismatch: worktree's identity (from path convention,
	// E-1301) != session's active task.
	worktreeTaskID := monitor.TaskIDFromWorktreePath(worktreePath)
	if worktreeTaskID != "" && session != nil && session.TaskID != nil {
		worktreeTaskNum, parseErr := parseEndlessTaskID(worktreeTaskID)
		if parseErr == nil && worktreeTaskNum != *session.TaskID {
			blockToolUse(fmt.Sprintf(
				"This worktree is bound to %s, but your active task is E-%d.\n\n"+
					"Either switch tasks (no cd needed):\n"+
					"  endless task claim E-%d\n\n"+
					"Or move to the worktree for your active task:\n"+
					"  endless worktree for-task E-%d",
				worktreeTaskID, *session.TaskID,
				worktreeTaskNum, *session.TaskID))
		}
	}

	// (c) Session has an active task whose worktree exists, but cwd is
	// not in it. (e.g. `endless task claim E-BBB` ran from inside a
	// session sitting in worktree A.) Layer F's redirection message
	// will guide Claude proactively; here we just refuse.
	if session != nil && session.TaskID != nil {
		activeWP, _ := monitor.WorktreePathForTask(projectID, *session.TaskID)
		if activeWP != "" && filepath.Clean(activeWP) != filepath.Clean(worktreePath) {
			blockToolUse(fmt.Sprintf(
				"Your active task E-%d is bound to a different worktree:\n  %s\n\n"+
					"Use absolute paths under that directory for Read/Write/Edit,\n"+
					"and run `cd %s` in a Bash call for shell commands.",
				*session.TaskID, activeWP, activeWP))
		}
	}
}

// enforceClaimedCwd implements the E-1586 cwd invariant: a session actively
// working a task must have its working directory inside that task's worktree.
// When cwd has drifted out — to main or another tree — any tool is refused with
// a `/cd` directive so the *default* working directory becomes the worktree, not
// main. Unlike enforceWorktreeGate this is not limited to write tools (Bash and
// everything else default to cwd too).
//
// Keyed on the active task's status, not on lock ownership: the worktree lock is
// claimed by a SessionStart *inside* the worktree (worktree adoption), which
// does not fire for the common "claim in main, then /cd" flow — so requiring the
// lock would leave that flow ungated. Instead we gate any non-terminal active
// task (the status `claim` sets is underway), which excludes a display-only
// `bind` of a done task and a landed/retained worktree (both terminal). The lock
// is consulted only to *avoid* redirecting into a worktree another live session
// owns.
func enforceClaimedCwd(projectID int64, payload claudePayload) {
	session, _ := monitor.GetActiveSession(payload.SessionID)
	if session == nil || session.TaskID == nil {
		return
	}
	status, _ := monitor.GetTaskStatus(*session.TaskID)
	if status == "" || monitor.IsTerminalTaskStatus(status) {
		// Not actively worked (e.g. display-only bind of a done task) — ignore.
		return
	}
	worktreePath, _ := monitor.WorktreePathForTask(projectID, *session.TaskID)
	if worktreePath == "" {
		// No worktree to anchor cwd to (e.g. a not-yet-claimed task).
		return
	}
	if lock, err := monitor.ReadWorktreeLock(worktreePath); err == nil && lock != nil &&
		lock.SessionID != payload.SessionID && !monitor.IsWorktreeLockStale(lock) {
		// A different live session owns this worktree — don't redirect into it.
		return
	}
	if pathWithin(worktreePath, payload.CWD) {
		// cwd is already the worktree (or a descendant) — invariant holds.
		return
	}
	blockToolUse(cdRedirect(*session.TaskID, worktreePath, payload.CWD))
}

// unboundWorktreeApplies reports whether the unbound-in-a-worktree gate has
// anything to say about this tool call: write tools only.
//
// Stated here rather than left to the call site's position in handlePreToolUse.
// The gate shipped blocking every tool kind, which was a strength nobody chose
// on purpose and which made reading code in an unclaimed worktree impossible;
// an invariant that narrow should be visible in the function that enforces it,
// not inferred from where it happens to be called. Same reasoning as
// autoBindFromCwd's own guards, which are deliberately correct in isolation
// rather than trusting call order.
//
// SessionStart does NOT go through here — it carries no tool name and wants the
// explanation regardless, so it calls unboundWorktreeDecision directly.
func unboundWorktreeApplies(payload claudePayload) bool {
	return writeTools[payload.ToolName]
}

// enforceUnboundWorktree refuses a WRITE by a session sitting in a task worktree
// while holding no task (E-1983). Mirrors enforceRevisitGate's shape: the
// decision is separate and pure enough to unit-test, the block itself is the
// JSON decision:"block" response.
func enforceUnboundWorktree(projectID int64, payload claudePayload) {
	if !unboundWorktreeApplies(payload) {
		return
	}
	if instruction, block := unboundWorktreeDecision(projectID, payload); block {
		blockToolUseWithDecision(instruction)
	}
}

// unboundWorktreeDecision reports whether this tool call must be blocked because
// the session's cwd says "task worktree" while the session holds no task, and
// carries the message explaining it.
//
// This is the one state Decisions 1 and 2 of E-1983 leave genuinely wrong, and
// until now it was silent: with the window option no longer able to bind, a
// session whose cwd bind did not happen simply has no task, and every later
// `endless` command answers for the wrong session or none. It is the mirror of
// enforceClaimedCwd, which blocks when a session HOLDS a task and its cwd has
// drifted out of that task's worktree — and which returns early on
// session.TaskID == nil, leaving exactly this half uncovered.
//
// Deliberately NOT blocked, each for its own reason:
//
//   - Agent-tool subagents and background agents. Both share a cwd with someone
//     else and are deliberately never bound (E-1300, E-1568), so "unbound" is
//     their correct state, not a breach. Screened FIRST: without this the gate
//     would block every subagent tool call in every worktree, which is the worst
//     false positive available here.
//   - cwd in the main checkout, or any path that is not a task worktree. An
//     unbound session in main is the correct outcome of the cwd-only rule, not a
//     failure. enforceWorktreeGate already covers the different case of a
//     session that HOLDS a task while sitting in main.
//   - A tree outside the registered project that happens to be worktree-shaped.
//   - No project resolved — enforceWorktreeGate's own precedent: without a
//     project root we cannot evaluate, so the call proceeds.
//
// It needs no escape hatch. The gate used to block every tool call, so it had to
// carve out `task claim` / `task bind` — and recognizing those from command text
// is how a heredoc or a quoted string could release it. Refusing only writes
// removes the carve-out entirely: the remedy is a Bash call, and Bash was never
// blocked.
func unboundWorktreeDecision(projectID int64, payload claudePayload) (string, bool) {
	if payload.AgentID != "" || os.Getenv("CLAUDE_JOB_DIR") != "" {
		return "", false
	}
	// The trigger is a pure regex on the path (E-1301's convention), NOT the
	// .endless/worktree.json walk — which matters, because the walk failing is
	// one of the things that puts a session here.
	taskRef := monitor.TaskIDFromWorktreePath(payload.CWD)
	if taskRef == "" {
		return "", false
	}
	if session, err := monitor.GetActiveSession(payload.SessionID); err != nil ||
		session == nil || session.TaskID != nil {
		return "", false
	}
	projectRoot, err := monitor.ProjectPath(projectID)
	if err != nil || projectRoot == "" {
		return "", false
	}
	// Both sides RESOLVED before the containment test. ProjectPath hands back the
	// resolved form and payload.CWD is whatever the harness reported, so
	// comparing them as they arrive answers "not in this project" for every
	// project reached through a symlink — /var and /tmp on macOS, and any
	// symlinked parent (E-2002). That silent false NEGATIVE is the same class of
	// bug Decision 2 fixes one function over.
	cwd, err := monitor.ResolvedProjectPath(payload.CWD)
	if err != nil {
		return "", false
	}
	if !pathWithin(projectRoot, cwd) {
		return "", false
	}
	return unboundWorktreeInstruction(projectRoot, cwd, taskRef), true
}

// unboundWorktreeInstruction builds the gate's message. It names WHICH step of
// the cwd bind failed, because the fixes differ and a session that is only told
// "you are unbound" has to go find that out itself.
func unboundWorktreeInstruction(projectRoot, cwd, taskRef string) string {
	var diagnosis string
	switch wtRoot, err := monitor.FindWorktreeRoot(cwd, projectRoot); {
	case err != nil:
		diagnosis = fmt.Sprintf(
			"the walk up from your working directory for `.endless/worktree.json`\n"+
				"failed: %v", err)
	case wtRoot == "":
		diagnosis = fmt.Sprintf(
			"no `.endless/worktree.json` was found walking up from\n  %s\nto the project root\n  %s\n"+
				"so nothing identified this directory as a managed worktree.",
			tildePath(cwd), tildePath(projectRoot))
	case monitor.TaskIDFromWorktreePath(wtRoot) == "":
		diagnosis = fmt.Sprintf(
			"the worktree root found\n  %s\ndoes not follow the `.endless/worktrees/e-NNN` "+
				"naming convention,\nso it names no task.", tildePath(wtRoot))
	default:
		diagnosis = "the worktree resolved, but the bind was declined — most often\n" +
			"because another live session already holds this worktree's lock\n" +
			"(`endless worktree current` names it)."
	}
	return fmt.Sprintf(
		"You are working inside %s's worktree, but this session holds no task.\n\n"+
			"A session's task comes from its working directory and nothing else, so\n"+
			"being unbound here means that resolution failed.\n\n"+
			"Why it failed: %s\n\n"+
			"Bind before continuing — either of these is one command:\n\n"+
			"  endless task claim %s        # claim it and start work\n"+
			"  endless task bind %s         # bind without changing task status\n\n"+
			"Writes are refused until one of them succeeds; reading and shell\n"+
			"commands are not, so you can run either of the above right now. If you\n"+
			"only came to read, carry on — an unbound session is only a problem when\n"+
			"it produces work that lands under no task.",
		taskRef, diagnosis, taskRef, taskRef)
}

// cdRedirect builds the E-1586 block message: cwd has drifted out of the
// session's owned worktree, so direct Claude to move its working directory back
// with `/cd`. Display paths render home-relative; the literal `/cd <path>` stays
// absolute for paste-safety.
func cdRedirect(taskID int64, worktreePath, cwd string) string {
	return fmt.Sprintf(
		"Your working directory is %s, but you have task E-%d claimed and its "+
			"worktree is %s.\n\n"+
			"Move Claude's working directory into the worktree so edits and shell "+
			"commands default to it, not main:\n\n"+
			"  /cd %s\n\n"+
			"After /cd every tool defaults to the worktree; you can still reach "+
			"another directory by passing an explicit absolute path.",
		tildePath(cwd), taskID, tildePath(worktreePath), worktreePath)
}

// tildePath renders an absolute path home-relative (~/...) for display in hook
// messages, falling back to the raw path when it is not under $HOME.
func tildePath(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	if p == home {
		return "~"
	}
	if strings.HasPrefix(p, home+string(os.PathSeparator)) {
		return "~" + p[len(home):]
	}
	return p
}

// pathWithin reports whether child is parent or a descendant of it.
func pathWithin(parent, child string) bool {
	rel, err := filepath.Rel(filepath.Clean(parent), filepath.Clean(child))
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

// parseEndlessTaskID parses an "E-NNN" task identifier into its numeric
// component. Accepts case-insensitive prefixes ("e-808", "E-808") and
// bare numbers ("808"). Returns an error on anything else.
func parseEndlessTaskID(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty task id")
	}
	if len(s) >= 2 && (s[0] == 'E' || s[0] == 'e') && s[1] == '-' {
		s = s[2:]
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse task id %q: %w", s, err)
	}
	return n, nil
}
