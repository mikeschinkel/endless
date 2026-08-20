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

	"github.com/mikeschinkel/endless/internal/matchers"
	"github.com/mikeschinkel/endless/internal/monitor"
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

func runClaude(args []string) error {
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
	// It also decouples the hook from the CLI refusal added alongside this:
	// autoImportTask shells out to the Python `endless`, which now exits 1 on an
	// unsupported harness. Returning here means that path is never reached
	// there, rather than reached and failing.
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
		return fmt.Errorf("reading stdin: %w", err)
	}

	var payload claudePayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return fmt.Errorf("parsing payload: %w", err)
	}

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
		return fmt.Errorf("looking up project for %s: %w", payload.CWD, err)
	}

	// Belt-and-suspenders for E-971's worktree lock release. The
	// worktree-local binary handles SessionEnd's full lifecycle; if it
	// fails to fire (missing, crashed, misconfigured), the lock leaks
	// and future sessions can't claim the worktree. ReleaseWorktreeLock
	// is idempotent (os.Remove swallows ErrNotExist), so running it in
	// both binaries is safe. Other SessionEnd ops stay single-fire in
	// the worktree-local handler. The success log makes leaks
	// diagnosable — its absence in stderr means SessionEnd never ran.
	// (E-1209)
	if payload.EventName == "SessionEnd" {
		if wtPath, err := monitor.FindLockBySessionID(projectID, payload.SessionID); err == nil && wtPath != "" {
			if err := monitor.ReleaseWorktreeLock(wtPath); err != nil {
				log.Printf("SessionEnd lock release at %s: %v", wtPath, err)
			} else {
				log.Printf("released worktree lock at %s for session %s", wtPath, payload.SessionID)
			}
		}
	}

	if shouldSkipForWorktree(projectID, payload.CWD) {
		return nil
	}

	// Record activity (throttled)
	throttled, err := monitor.ShouldThrottle(projectID, "claude", 2)
	if err != nil {
		return err
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
			return fmt.Errorf("recording activity: %w", err)
		}
	}

	// Background-agent decoration (E-1568). A bg agent dispatched by
	// `task spawn --bg` has a sessions row inserted at dispatch with
	// session_id NULL + short_id (kind_id=2); its real UUID first appears
	// here. Attach the UUID to that row BEFORE TouchSession runs — TouchSession
	// keys on session_id and would otherwise INSERT a second, duplicate row
	// (the dispatch row's session_id is still NULL, so no conflict catches it).
	// Best-effort: a no-match (race: hook fired before the dispatch INSERT
	// committed, or a stray CLAUDE_JOB_DIR per research §7 issue #59848) just
	// falls through to the normal new-row path.
	decorateBgSession(payload)

	// Per-event session UPSERT (E-1426). Records process + last_activity
	// and creates the row if absent. Runs on every event so a NULL/stale
	// `process` self-heals within one tool call (E-1408 / E-1422), and a
	// pane-reattach is picked up on the next event. Collision invalidation
	// inside TouchSession marks any prior occupant of this pane `ended`.
	if err := monitor.TouchSession(payload.SessionID, "claude", os.Getenv("TMUX_PANE"), projectID); err != nil {
		return fmt.Errorf("touching session: %w", err)
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
		// state='needs_input' on INSERT; InitSession is retained for the
		// transcript-path side effect path below and a defensive no-op
		// UPDATE if the row exists.
		if err := monitor.InitSession(payload.SessionID, projectID); err != nil {
			return fmt.Errorf("initializing session: %w", err)
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
		// Opportunistic stale-worktree reaper (E-1337). Removes worktree
		// dirs whose owning task has a landing record past worktree_ttl
		// and no live process holding cwd. Cheap when nothing to reap.
		if err := monitor.ReapWorktreesForProject(projectID); err != nil {
			log.Printf("reaping stale worktrees: %v", err)
		}
		// Opportunistic notice reaper (E-1917 fix). Drops undelivered notices
		// for sessions that have ended: they never take another turn, so those
		// rows are undeliverable by construction. SessionStart only — the write
		// trigger already excludes ended sessions, so this is cleanup for
		// sessions that ended after their notice was written, and once per
		// session start is frequent enough for that.
		if err := monitor.ReapNoticesForEndedSessions(); err != nil {
			log.Printf("reaping notices for ended sessions: %v", err)
		}
		// Spawn-flow auto-bind: when `endless task spawn` launches a new
		// Claude window, it sets `@endless_spawned_by` and pre-claims the
		// task (status flip + worktree creation) before launching. This
		// hook just records the session→task binding (E-1027). Status is
		// NOT flipped here (spawn already did it). Use case 2 (end-user
		// starts Claude directly without spawn) has no spawn marker, so
		// this path doesn't fire.
		// Skipped for Agent-tool subagents — they share the parent's
		// tmux window (and thus its @endless_spawned_by marker) but
		// have their own session_id; binding them would create a
		// phantom co-owner on the spawned task (E-1300).
		spawnBound := false
		if payload.AgentID == "" {
			spawnBound = trySpawnBind(projectID, payload)
		}
		// Worktree adoption (E-971 Layer D). If cwd is inside an
		// endless-managed worktree, claim the lock or refuse if
		// already owned by a live session.
		if refusal, err := handleWorktreeAdoption(projectID, payload); err != nil {
			return fmt.Errorf("worktree adoption: %w", err)
		} else if refusal != "" {
			return writeContextInjection(payload.EventName, refusal)
		}
		// Cwd-based auto-bind fallback (E-1291 / E-1700). Runs after
		// worktree adoption so a refused session is never bound. See
		// maybeCwdBind for the gating rationale (spawn-race recovery,
		// subagent / background-agent exclusions).
		maybeCwdBind(projectID, payload, spawnBound)
		return handleTaskContextInjection(projectID, isRegistered, payload)

	case "UserPromptSubmit":
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
		if err := monitor.ReapWorktreesForProject(projectID); err != nil {
			log.Printf("reaping stale worktrees: %v", err)
		}
		return handlePreToolUse(projectID, isRegistered, payload)

	case "PostToolUse":
		if err := monitor.ReapWorktreesForProject(projectID); err != nil {
			log.Printf("reaping stale worktrees: %v", err)
		}
		return handlePostToolUse(projectID, isRegistered, payload)

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
			return fmt.Errorf("idling session: %w", err)
		}
		if err := monitor.ReapWorktreesForProject(projectID); err != nil {
			log.Printf("reaping stale worktrees: %v", err)
		}
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
			return fmt.Errorf("ending session: %w", err)
		}
		if err := monitor.ReapWorktreesForProject(projectID); err != nil {
			log.Printf("reaping stale worktrees: %v", err)
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
// Which is also why it says "write it in full, do not pre-summarize". An agent
// that shortens before submitting has done the minimizer's job badly and
// destroyed the evidence, and a short draft is exactly what an agent trying to
// look compliant will produce.
const reportChannelRule = "Report channel: every reply you send the user goes " +
	"through `endless task report` first. Write your reply exactly as you mean " +
	"to send it — in full, at whatever length the turn calls for, tables and " +
	"code blocks and all — to a file, then run `endless task report [<task-id>] " +
	"--draft-file <path>`. The task id is optional; omit it when you have " +
	"nothing claimed. Send that command's output as your entire final message, " +
	"verbatim: no preamble, no additions, nothing after it.\n\n" +
	"Do NOT pre-summarize the draft. An adversarial minimizer decides what " +
	"survives, and it can only cut what it is given — trimming first replaces " +
	"its judgment with yours, which is the thing this channel exists to stop. " +
	"If it cuts something you needed, `endless task report --raw` prints your " +
	"draft back unchanged; nothing is destroyed.\n\n" +
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
// Resolution is nearest-config-wins from cwd (see ReportGateEnabledForCwd), so a
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
// The two axes are independent and both must say yes: `report_gate` is the
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
	return monitor.ReportGateEnabledForCwd(cwd, root)
}

func handleTaskContextInjection(projectID int64, isRegistered bool, payload claudePayload) error {
	ctx, err := buildTaskContextInjection(projectID, payload)
	if err != nil {
		return err
	}
	combined := composeSessionStartContext(ctx, reportChannelOn(projectID, isRegistered, payload.CWD))
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

// handleUserPromptSubmit composes the per-prompt response. Two pieces,
// any subset may be present:
//
//  1. Pending inter-session message banner (existing fallback).
//  2. Layer 1: first-time full task list (one-shot) OR per-prompt
//     "Active task: E-XXX — <title>." reminder.
func handleUserPromptSubmit(projectID int64, payload claudePayload, sigilNotice string) error {
	var parts []string

	// E-1953: a refused label leads. The user believes they just taught the
	// minimizer something; if the notice were buried under a task list the agent
	// would skip it and the correction would be lost twice over.
	if sigilNotice != "" {
		parts = append(parts, sigilNotice)
	}

	// Pending inter-session messages
	pane := os.Getenv("TMUX_PANE")
	if port, _, _ := monitor.LookupChannelPort(pane); port == 0 {
		if hasMsgs, err := monitor.HasPendingMessages(pane); err == nil && hasMsgs {
			parts = append(parts, "You have pending inter-session messages. Run: endless channel inbox")
		}
	}

	// Layer 1: full list on first injection, single-line reminder thereafter
	if !monitor.HasInjectedContext(payload.SessionID) {
		if ctx, err := buildTaskContextInjection(projectID, payload); err == nil && ctx != "" {
			parts = append(parts, ctx)
		}
	} else {
		if session, err := monitor.GetActiveSession(payload.SessionID); err == nil &&
			session != nil && session.ActiveTaskID != nil {
			if h, err := monitor.GetTaskHeadline(*session.ActiveTaskID); err == nil && h.Title != "" {
				parts = append(parts, h.Render(*session.ActiveTaskID))
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

// claimHandoffResponse wraps a rendered claim handoff (E-1822) in the
// PostToolUse response shape. Pure (no I/O) so the shape is unit-testable
// independent of the rendering.
func claimHandoffResponse(handoff string) contextInjection {
	return injectContext("PostToolUse", handoff)
}

func handlePostToolUse(projectID int64, isRegistered bool, payload claudePayload) error {
	// Detect endless task claim/complete/chat commands and update session state
	claimHandoff, err := handlePostToolUseSession(projectID, payload)
	if err != nil {
		return fmt.Errorf("post tool use session: %w", err)
	}

	// E-1822: a claim into an already-running session gets the per-type handoff
	// a spawned session is born with, folded against the claim's own tool
	// result. Takes precedence over the report reinforcement below — only one
	// JSON object may go to stdout, and a compound command that both claims and
	// reports is the claim's turn.
	if claimHandoff != "" {
		return json.NewEncoder(os.Stdout).Encode(claimHandoffResponse(claimHandoff))
	}

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

	// Record which plan file this session is editing (used by ExitPlanMode)
	if err := monitor.SetPlanFilePath(payload.SessionID, input.FilePath); err != nil {
		return fmt.Errorf("setting plan file path: %w", err)
	}

	// NOTE: Auto-import disabled. Sessions should use `endless task update <id> --text <file>`
	// to save task text, and `endless task add` to create child items explicitly.
	// Auto-import created duplicate items at the wrong granularity (every bullet became a task item).

	items, err := monitor.GetActiveTasks(projectID)
	if err != nil {
		return fmt.Errorf("getting active tasks: %w", err)
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

// Action regexes are loaded from matchers (config files) per call. Cache
// per process to avoid repeating the read for each detection in a hook
// invocation. Per E-970.
const (
	actionStart   = "start"
	actionConfirm = "confirm"
	actionChat    = "chat"
	scopeTask     = "task"

	actionBeacon  = "beacon"
	actionConnect = "connect"
	actionSend    = "send"
	scopeChannel  = "channel"
)

func handlePreToolUse(projectID int64, isRegistered bool, payload claudePayload) error {
	// E-1226: refuse `sqlite3 .endless/...` regardless of registration —
	// the antipattern is file-pattern-specific, not project-state-specific,
	// and the recovery cost (ghost DB files blocking worktree-land) is real.
	if payload.ToolName == "Bash" {
		blockSqliteAgainstEndlessIfApplicable(payload)
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

	// E-1202: refuse a direct Write/Edit of a task plan-file mirror
	// (.endless/plans/E-NNN.md) in main OR a worktree. Placed before the
	// worktree gate so a plan-file write in main gets the plan-specific
	// redirect to `endless task update --text` rather than the generic
	// "edits in main" refusal. Independent of tracking_mode.
	blockPlanFileWriteIfApplicable(payload)

	// Worktree gate (E-971 Layer D). Independent of tracking_mode, like
	// E-1012: even with per-task tracking off, edits in main and edits
	// to a worktree owned by another session are refused.
	enforceWorktreeGate(projectID, payload)

	// Check tracking mode
	mode := monitor.GetTrackingMode(projectID)
	if mode != "enforce" {
		return nil
	}

	// Check for active session
	session, err := monitor.GetActiveSession(payload.SessionID)
	if err == nil && session != nil {
		if session.State == "working" {
			// Check expiration
			if monitor.IsSessionExpired(session, 30) {
				blockToolUse("Your work session has expired due to inactivity.\n\n" +
					"Run `endless task claim <id>` to resume working on a task.\n" +
					"Run `endless task show` to see available tasks.")
			}
			// Active and valid — allow through (per-event TouchSession in
			// runClaude already refreshed last_activity).
			return nil
		}
	}

	// No active session — block with helpful message
	projectName, _ := monitor.GetProjectName(projectID)
	items, _ := monitor.GetActiveTasks(projectID)

	var msg strings.Builder
	fmt.Fprintf(&msg, "BLOCKED: No active work session for project '%s'.\n", projectName)
	msg.WriteString("You must register which task you're working on before making changes.\n\n")

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
	msg.WriteString("  endless task chat         — start a chat-only session (no task tracking)\n")

	blockToolUse(msg.String())
	return nil // unreachable, blockToolUse calls os.Exit
}

// blockToolUse writes an error to stderr and exits with code 2.
// Claude Code interprets exit code 2 as "action blocked" and feeds stderr
// back to Claude as context.
func blockToolUse(message string) {
	fmt.Fprint(os.Stderr, message)
	os.Exit(2)
}

// revisitClearVerbRe matches the user's revisit gate-clearing commands so they
// are never blocked by the gate itself (E-1542): `endless task continue` /
// `endless task pause`, including path- or wrapper-prefixed forms such as
// `uv run endless task continue` or `/usr/local/bin/endless task pause`.
var revisitClearVerbRe = regexp.MustCompile(`(?i)\bendless\s+task\s+(?:continue|pause)\b`)

// enforceRevisitGate intercepts a session whose claimed task descends from an
// epic currently in status='revisit' (E-1542). On the session's next tool call
// (any tool kind) it blocks and instructs Claude to surface an AskUserQuestion:
// continue under the current plan, or pause until the strategy is re-set. The
// user's answer runs `endless task continue` / `endless task pause`, which clear
// the gate. No-op when the session has no resolvable active task, and never
// blocks the gate-clearing commands themselves.
func enforceRevisitGate(payload claudePayload) {
	if instruction, block := revisitGateDecision(payload); block {
		blockToolUseWithRevisitPrompt(instruction)
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
			revisitClearVerbRe.MatchString(input.Command) {
			return "", false
		}
	}

	session, err := monitor.GetActiveSession(payload.SessionID)
	if err != nil || session == nil || session.ActiveTaskID == nil {
		return "", false
	}
	taskID := *session.ActiveTaskID

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
			"  - Pause until the strategy is re-set (then call `endless task pause`)",
		taskID, epicID,
	)
}

// revisitBlockResponse builds the PreToolUse block response carrying the
// instruction in both reason and additionalContext (see preToolUseBlock).
func revisitBlockResponse(instruction string) preToolUseBlock {
	return preToolUseBlock{
		Decision: "block",
		Reason:   instruction,
		HookSpecificOutput: hookContextOutput{
			HookEventName:     "PreToolUse",
			AdditionalContext: instruction,
		},
	}
}

// blockToolUseWithRevisitPrompt emits the PreToolUse block response (decision
// "block" + reason + additionalContext) and exits 0. If encoding fails it falls
// back to the always-works stderr+exit-2 form.
func blockToolUseWithRevisitPrompt(instruction string) {
	if err := json.NewEncoder(os.Stdout).Encode(revisitBlockResponse(instruction)); err != nil {
		blockToolUse(instruction)
		return
	}
	os.Exit(0)
}

// handlePostToolUseSession updates session state from an `endless task ...` Bash
// call. It returns the rendered claim handoff (E-1822) when this call was a
// claim into an already-running session, and "" otherwise; the caller decides
// whether to emit it as PostToolUse additionalContext.
func handlePostToolUseSession(projectID int64, payload claudePayload) (string, error) {
	if payload.ToolName != "Bash" {
		return "", nil
	}

	var input toolInputBash
	if err := json.Unmarshal(payload.ToolInput, &input); err != nil {
		return "", nil
	}

	all, err := matchers.Load(projectID)
	if err != nil {
		log.Printf("loading matchers: %v", err)
		return "", nil
	}

	// Detect: endless task claim <id>
	if re := matchers.ActionRegex(all, actionStart, scopeTask); re != nil {
		if m := re.FindStringSubmatch(input.Command); m != nil {
			taskID, err := strconv.ParseInt(m[1], 10, 64)
			if err != nil {
				return "", nil
			}
			if err := monitor.StartWorkSession(payload.SessionID, projectID, taskID); err != nil {
				return "", fmt.Errorf("starting work session: %w", err)
			}
			// A claim reaching PostToolUse is definitionally the retrofit case:
			// `task spawn` claims before the target session exists, so its claim
			// never runs as a tool call inside the spawned session.
			return claimHandoffContext(projectID, taskID, payload), nil
		}
	}

	// Detect: endless task confirm <id>
	if re := matchers.ActionRegex(all, actionConfirm, scopeTask); re != nil {
		if m := re.FindStringSubmatch(input.Command); m != nil {
			taskID, err := strconv.ParseInt(m[1], 10, 64)
			if err == nil {
				if err := monitor.CompleteTask(payload.SessionID, taskID); err != nil {
					return "", fmt.Errorf("confirming task: %w", err)
				}
			}
			return "", nil
		}
	}

	// Detect: endless task chat
	if re := matchers.ActionRegex(all, actionChat, scopeTask); re != nil && re.MatchString(input.Command) {
		if err := monitor.StartChatSession(payload.SessionID, projectID); err != nil {
			return "", fmt.Errorf("starting chat session: %w", err)
		}
		return "", nil
	}

	// Detect: endless channel beacon/connect/send. last_activity was
	// already refreshed by the per-event TouchSession in runClaude; this
	// block is kept only to short-circuit so unrelated post-tool logic
	// doesn't fire on a channel action.
	for _, action := range []string{actionBeacon, actionConnect, actionSend} {
		if re := matchers.ActionRegex(all, action, scopeChannel); re != nil && re.MatchString(input.Command) {
			return "", nil
		}
	}

	return "", nil
}

func handleExitPlanMode(projectID int64, payload claudePayload) error {
	// Use the plan file path recorded during PostToolUse/Write
	planFile := monitor.GetPlanFilePath(payload.SessionID)

	// Fall back to most recently modified plan file if not recorded
	if planFile == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil
		}
		plansDir := filepath.Join(home, ".claude", "plans")
		entries, err := os.ReadDir(plansDir)
		if err != nil {
			return nil
		}
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
				planFile = filepath.Join(plansDir, e.Name())
			}
		}
	}

	if planFile == "" {
		return nil
	}

	// NOTE: Auto-import disabled. The plan file path is still tracked so sessions
	// can reference it with `endless task update <id> --text <plan-file>`.
	// See PostToolUse/Write handler for rationale.

	items, err := monitor.GetActiveTasks(projectID)
	if err != nil {
		return fmt.Errorf("getting active tasks: %w", err)
	}

	return writeContextInjection(payload.EventName, fmt.Sprintf(
		"Plan accepted and synced to Endless. %d active item(s) tracked.",
		len(items),
	))
}

// gitCommitRe matches common forms of 'git commit' at the start of a command:
// 'git commit', 'git commit -m ...', '  git commit '. Excludes 'git commit-tree'
// (the trailing boundary requires whitespace or end-of-string).
var gitCommitRe = regexp.MustCompile(`^\s*git\s+commit($|\s)`)

// planFileRe matches a task plan-file mirror path .endless/plans/E-NNN.md,
// absolute or repo-relative. Anchored $ plus the E-<digits>.md immediately after
// plans/ excludes any subdir (e.g. a future plans/snapshots/…) and any non-plan
// file in the dir. Plan files are the DB-owned mirror endless writes and commits
// on land; hand-editing them via Write/Edit desyncs tasks.text (the source of
// truth). See blockPlanFileWriteIfApplicable.
var planFileRe = regexp.MustCompile(`(^|/)\.endless/plans/E-\d+\.md$`)

// sqliteEndlessRe matches sqlite3 invocations targeting any path inside
// a .endless/ directory. The character class [^|;&] stops the match at
// command-pipeline boundaries; [ /] before \.endless/ ensures the
// pattern is a path component (avoids false-positive on names like
// my.endless/x). Case-insensitive (?i) catches uppercase variants.
var sqliteEndlessRe = regexp.MustCompile(`(?i)sqlite3[^|;&]*[ /]\.endless/`)

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
	if !sqliteEndlessRe.MatchString(input.Command) {
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

// blockCommitOnMainIfApplicable inspects a Bash tool call. If it's a
// 'git commit' invoked from main's working tree (not a worktree, not
// during an active merge), block with an actionable message. Otherwise
// return silently and let the command run.
func blockCommitOnMainIfApplicable(payload claudePayload) {
	var input toolInputBash
	if err := json.Unmarshal(payload.ToolInput, &input); err != nil {
		return
	}
	if !gitCommitRe.MatchString(input.Command) {
		return
	}
	if payload.CWD == "" {
		return
	}
	inMain, err := isInMainCheckout(payload.CWD)
	if err != nil || !inMain {
		// Not a git repo, git unavailable, or in a worktree — allow.
		return
	}
	if isInActiveMerge(payload.CWD) {
		// Merge in progress; the merge commit is part of completing the merge.
		return
	}

	blockToolUse(`Direct commits to main are highly discouraged when using endless.

main is the integration target. Make changes in a worktree on a per-task
branch, then merge via ` + "`endless worktree land <task-id>`" + `.

If you have an Endless task for this work:
  endless task claim E-NNN          # creates worktree at .endless/worktrees/e-NNN

Or by hand:
  git worktree add -b task/NNN-<slug> .endless/worktrees/e-NNN main
  cd .endless/worktrees/e-NNN
  # ... do work, commit ...
  endless worktree land E-NNN

Bypass (NOT recommended):
  git commit --no-verify`)
}

// blockPlanFileWriteIfApplicable refuses any Write/Edit/NotebookEdit whose
// target is a task plan-file mirror .endless/plans/E-NNN.md — in main's working
// tree OR a worktree's materialized mirror. Plan content lives in tasks.text (the
// source of truth); the .md file is a mirror endless writes and commits during
// worktree-land so humans can read plans on GitHub. A direct tool-write desyncs
// the DB, and if the worktree is later dropped the edit is silently lost. The CLI
// materializer (`endless task update --text`) is a subprocess the hook never sees,
// so the land-produces-plan-markdown flow is unaffected. Independent of
// tracking_mode, like the worktree and commit-on-main gates.
func blockPlanFileWriteIfApplicable(payload claudePayload) {
	path := extractFilePath(payload.ToolName, payload.ToolInput)
	if path == "" || !planFileRe.MatchString(path) {
		return
	}
	blockToolUse(
		"BLOCKED: refusing a direct Write/Edit of a task plan file " +
			"(.endless/plans/E-NNN.md). Plan content lives in tasks.text (the source " +
			"of truth); the file is a mirror endless writes and commits for you so " +
			"humans can see plans when reviewing the repo on GitHub or other Git " +
			"hosts. Editing it directly leaves the DB stale, and if the worktree is " +
			"later dropped the edit is silently lost.\n\n" +
			"Author the plan under .endless/tmp/ (the project-local scratch " +
			"dir), then run:\n" +
			"  endless task update <id> --text-file .endless/tmp/<file>.md\n\n" +
			"(--text-file loads the file's content; --text would store the path " +
			"string itself. Use --text only for inline content.)\n\n" +
			"Never hand-edit or git-commit the plan file yourself.")
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

// decorateBgSession attaches this session's real UUID to a background agent's
// dispatch row (E-1568). A bg agent launched by `claude --bg` runs with
// CLAUDE_JOB_DIR=~/.claude/jobs/<short_id> (research §6); the basename is the
// short_id used as the dispatch handle. monitor.DecorateBgSession UPDATEs the
// matching kind_id=2 row (session_id IS NULL) with payload.SessionID. This must
// run before TouchSession so the row is keyed by its real UUID before any
// generic upsert can insert a duplicate. Best-effort: only acts on SessionStart
// (the one event where the UUID first appears for an undecorated row); a no-
// match or error just logs and lets the normal session-tracking path proceed.
func decorateBgSession(payload claudePayload) {
	if payload.EventName != "SessionStart" {
		return
	}
	jobDir := os.Getenv("CLAUDE_JOB_DIR")
	if jobDir == "" {
		return
	}
	short := filepath.Base(jobDir)
	rows, err := monitor.DecorateBgSession(short, payload.SessionID)
	if err != nil {
		log.Printf("bg-decorate: short_id %s: %v", short, err)
		return
	}
	if rows == 0 {
		log.Printf("bg-decorate: no undecorated bg row for short_id %s (falling through to normal tracking)", short)
		return
	}
	log.Printf("bg-decorate: bound session %s to dispatch row short_id %s", payload.SessionID, short)
}

// tmuxSpawnedBy reads @endless_spawned_by from the current tmux window.
// Set only by `endless task spawn` (carries the spawning session's id or
// a `pid-<n>` fallback for non-Claude spawners). Empty string means this
// window was not created by spawn — callers should skip spawn-flow logic.
// tmuxSpawnedBy is a package var (not a plain func) so tests can stub the tmux
// read to simulate a spawned window whose @endless_task_id read raced (E-1700).
var tmuxSpawnedBy = func() string {
	pane := os.Getenv("TMUX_PANE")
	if pane == "" {
		return ""
	}
	out, err := exec.Command(
		"tmux", "display-message", "-p", "-t", pane, "#{@endless_spawned_by}",
	).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// trySpawnBind attempts the spawn-marker auto-bind and reports whether the
// session was actually bound. Returns false when there is no spawn marker, the
// @endless_task_id window option is not readable (a tmux read race, E-1700), or
// the DB write fails — signaling the caller to fall back to cwd-derived binding.
// Caller must already have screened out subagents.
func trySpawnBind(projectID int64, payload claudePayload) bool {
	if tmuxSpawnedBy() == "" {
		return false
	}
	taskID := tmuxTaskID()
	if taskID <= 0 {
		return false
	}
	snap := monitor.SnapshotSession(payload.SessionID)
	if err := monitor.BindSessionToTask(payload.SessionID, projectID, taskID); err != nil {
		log.Printf("spawn-bind session %s to task %d: %v", payload.SessionID, taskID, err)
		return false
	}
	logSessionBind(payload.SessionID, snap, taskID, monitor.SessionLogSpawnBind, "hookcmd.trySpawnBind")
	return true
}

// logSessionBind records one BindSessionToTask transition in the machine-local
// diagnostic log. new_state is always 'working' — that is what BindSessionToTask
// sets on a successful bind. Best-effort; never returns an error.
func logSessionBind(sessionID string, snap monitor.SessionSnapshot, taskID int64, reason monitor.SessionLogReason, caller string) {
	newTaskID := taskID
	monitor.LogSessionTxn(monitor.SessionTxn{
		SessionGUID:     sessionID,
		ShortID:         snap.ShortID,
		OldState:        snap.State,
		NewState:        "working",
		OldActiveTaskID: snap.ActiveTaskID,
		NewActiveTaskID: &newTaskID,
		Reason:          reason,
		Caller:          caller,
	})
}

// maybeCwdBind runs the cwd-derived SessionStart auto-bind when the spawn-marker
// path did not bind (spawnBound == false). Gating on !spawnBound rather than "no
// spawn marker" (E-1700) is the fix: a spawned window can carry
// @endless_spawned_by while its @endless_task_id read races to empty (or
// BindSessionToTask errors), leaving trySpawnBind a no-op. payload.CWD is the
// worktree for a spawned worker, so this fallback binds reliably and closes the
// gap where active_task_id stayed NULL and the status line showed "claim a task".
//
// Skipped for Agent-tool subagents — they share the parent's cwd but represent
// tool use, not user claim intent; binding them would create a phantom co-owner.
// Skipped for background agents (E-1568): their dispatch row already carries
// active_task_id/active_epic_id, and the tmux-oriented bind is meaningless for a
// headless agent — decorateBgSession is their path. Bind only; task status is
// unchanged.
func maybeCwdBind(projectID int64, payload claudePayload, spawnBound bool) {
	if payload.AgentID != "" || spawnBound || os.Getenv("CLAUDE_JOB_DIR") != "" {
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
// have screened out subagents and the spawn-marker case.
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
	// active_task_id from its cwd worktree — never a re-pointer. A resume
	// (`claude --resume`), /clear, or /compact fires SessionStart with the
	// session's existing binding intact; when the resumed process's cwd is a
	// DIFFERENT task's worktree, overwriting active_task_id would silently steal
	// the session away from the task it belongs to, leaving that task
	// unreachable via `session goto`/`session resume`. Only bind when the
	// session has no active task yet (the E-1291/E-1700 fallback case) or
	// already points at this worktree's task (idempotent).
	if session, err := monitor.GetActiveSession(payload.SessionID); err == nil &&
		session != nil && session.ActiveTaskID != nil && *session.ActiveTaskID != taskID {
		return
	}
	// E-1856: never bind into a worktree a LIVE sibling session already owns.
	// handleWorktreeAdoption refuses this case upstream and short-circuits
	// SessionStart, but the auto-bind must be correct in isolation rather than
	// trusting that call order — otherwise any path that reaches it (or a future
	// re-order) would silently make the incoming session a phantom co-owner of
	// the task, stealing its active_task_id pointer.
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

// resolveParentTaskID determines the parent task ID for auto-import.
// Priority: session's active goal > tmux @endless_task_id > none.
func resolveParentTaskID(sessionID string) *int64 {
	// Check session's active goal first
	session, err := monitor.GetActiveSession(sessionID)
	if err == nil && session != nil && session.ActiveTaskID != nil {
		return session.ActiveTaskID
	}
	// Fall back to tmux window option
	if id := tmuxTaskID(); id > 0 {
		return &id
	}
	return nil
}

func autoImportTask(projectID int64, sessionID, filePath string) error {
	projectName, err := monitor.GetProjectName(projectID)
	if err != nil {
		return err
	}

	args := []string{"task", "import", filePath, "--project", projectName, "--replace"}

	if parentID := resolveParentTaskID(sessionID); parentID != nil {
		args = append(args, "--parent", strconv.FormatInt(*parentID, 10))
	}

	cmd := exec.Command("endless", args...)
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// --- E-971 Layer D: worktree adoption + enforcement helpers ----------------

// osExecutable is a test seam for os.Executable.
var osExecutable = os.Executable

// worktreeOverrideRegistered returns true when the worktree's
// .claude/settings.json references the worktree's own bin/endless-go
// path — i.e. claude-settings-init was run and the override is active.
//
// Why: every worktree inherits the committed .claude/settings.json from
// HEAD (which holds enabledPlugins), so file presence alone is not a
// reliable signal that the hook override is configured. Substring-checking
// the file content for the worktree-specific binary path correctly
// distinguishes the inherited committed file from the regenerated one.
func worktreeOverrideRegistered(worktreeRoot, worktreeBin string) bool {
	data, err := os.ReadFile(filepath.Join(worktreeRoot, ".claude", "settings.json"))
	if err != nil {
		return false
	}
	return strings.Contains(string(data), worktreeBin)
}

// shouldSkipForWorktree returns true when this binary should yield to a
// worktree-local copy of endless-go for the same hook event.
//
// Why: Claude Code merges hook entries across user/project settings scopes
// (concatenate + dedupe, not replace), so a session whose cwd is inside a
// worktree fires both the global binary and the worktree's binary for every
// event. The global yields here so state-mutating handlers don't run twice.
// Asymmetric by design: only the global self-skips; the worktree binary
// handles the event without coordination.
func shouldSkipForWorktree(projectID int64, cwd string) bool {
	if projectID == 0 || cwd == "" {
		return false
	}
	projectRoot, err := monitor.ProjectPath(projectID)
	if err != nil {
		log.Printf("self-skip check: project path lookup failed: %v", err)
		return false
	}
	return shouldSkipForWorktreeAt(cwd, projectRoot)
}

// shouldSkipForWorktreeAt is the filesystem-only inner check, separated so
// unit tests can drive it without a database.
func shouldSkipForWorktreeAt(cwd, projectRoot string) bool {
	worktreeRoot, err := monitor.FindWorktreeRoot(cwd, projectRoot)
	if err != nil {
		log.Printf("self-skip check: find worktree root: %v", err)
		return false
	}
	if worktreeRoot == "" {
		return false
	}
	worktreeBin := filepath.Join(worktreeRoot, "bin", "endless-go")
	if !worktreeOverrideRegistered(worktreeRoot, worktreeBin) {
		// No worktree-level Claude override is configured, so the global is
		// the only binary firing for events here. Nothing to skip and no
		// missing-binary warning to emit.
		return false
	}
	worktreeStat, err := os.Stat(worktreeBin)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			log.Printf("WARN: cwd %s is inside worktree %s but %s does not exist — running global as fallback.", cwd, worktreeRoot, worktreeBin)
		} else {
			log.Printf("self-skip check: stat %s: %v", worktreeBin, err)
		}
		return false
	}
	selfPath, err := osExecutable()
	if err != nil {
		log.Printf("self-skip check: os.Executable: %v", err)
		return false
	}
	selfStat, err := os.Stat(selfPath)
	if err != nil {
		log.Printf("self-skip check: stat self %s: %v", selfPath, err)
		return false
	}
	if os.SameFile(selfStat, worktreeStat) {
		return false
	}
	log.Printf("deferring to %s (cwd %s is inside worktree %s)", worktreeBin, cwd, worktreeRoot)
	return true
}

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
		if session != nil && session.ActiveTaskID != nil {
			if wp, _ := monitor.WorktreePathForTask(projectID, *session.ActiveTaskID); wp != "" {
				redirectHint = fmt.Sprintf(
					"\n\nYour active task E-%d has a worktree at:\n  %s\n\n"+
						"Run `cd %s` in a Bash call (the new cwd persists for\n"+
						"subsequent Bash calls), and use absolute paths under\n"+
						"that directory for Read/Write/Edit.",
					*session.ActiveTaskID, wp, wp)
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
			"  git worktree add -b task/NNN-<slug> .endless/worktrees/e-NNN main" +
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
	if worktreeTaskID != "" && session != nil && session.ActiveTaskID != nil {
		worktreeTaskNum, parseErr := parseEndlessTaskID(worktreeTaskID)
		if parseErr == nil && worktreeTaskNum != *session.ActiveTaskID {
			blockToolUse(fmt.Sprintf(
				"This worktree is bound to %s, but your active task is E-%d.\n\n"+
					"Either switch tasks (no cd needed):\n"+
					"  endless task claim E-%d\n\n"+
					"Or move to the worktree for your active task:\n"+
					"  endless worktree for-task E-%d",
				worktreeTaskID, *session.ActiveTaskID,
				worktreeTaskNum, *session.ActiveTaskID))
		}
	}

	// (c) Session has an active task whose worktree exists, but cwd is
	// not in it. (e.g. `endless task claim E-BBB` ran from inside a
	// session sitting in worktree A.) Layer F's redirection message
	// will guide Claude proactively; here we just refuse.
	if session != nil && session.ActiveTaskID != nil {
		activeWP, _ := monitor.WorktreePathForTask(projectID, *session.ActiveTaskID)
		if activeWP != "" && filepath.Clean(activeWP) != filepath.Clean(worktreePath) {
			blockToolUse(fmt.Sprintf(
				"Your active task E-%d is bound to a different worktree:\n  %s\n\n"+
					"Use absolute paths under that directory for Read/Write/Edit,\n"+
					"and run `cd %s` in a Bash call for shell commands.",
				*session.ActiveTaskID, activeWP, activeWP))
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
	if session == nil || session.ActiveTaskID == nil {
		return
	}
	status, _ := monitor.GetTaskStatus(*session.ActiveTaskID)
	if status == "" || monitor.IsTerminalTaskStatus(status) {
		// Not actively worked (e.g. display-only bind of a done task) — ignore.
		return
	}
	worktreePath, _ := monitor.WorktreePathForTask(projectID, *session.ActiveTaskID)
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
	blockToolUse(cdRedirect(*session.ActiveTaskID, worktreePath, payload.CWD))
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
