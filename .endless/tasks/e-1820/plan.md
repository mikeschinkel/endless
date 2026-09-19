# Enforce non-empty task description at the event-emit choke point

## Goal
Block the AI agent from creating a task with an empty `description`, while leaving
human/manual creation free to omit it. Enforcement sits at the single
`endless-go event emit` seam, ahead of the JSONL WAL append, so it covers every
path that reaches the ledger.

## Scope (finalized): AI-only
The rule fires only when the `task.created` event is attributed to a live Claude
session — i.e. the event's actor carries a `session_id`. Manual/human creation
(`--no-session` -> `actor.kind=system`, no `session_id`) is exempt by design:
humans may file a bare title, and a background process augments the description
later (separate follow-up, below).

Note the discriminator is *"actor is session-attributed"*, NOT `actor.kind ==
"session"`. A `endless task add` run by the agent emits `actor.kind="cli"` WITH a
`session_id` bound (see `internal/events/event.go` `Actor{Kind, ID, SessionID}`).
Key the gate on `Actor.SessionID` being present.

## Change
1. In `endless-go event emit`, at the validation step that runs BEFORE the JSONL
   append, add a rule:
   - event type == `task.created`, AND
   - actor is session-attributed (`Actor.SessionID` present), AND
   - payload `description` is empty / whitespace-only
   -> reject with a non-zero exit and an agent-optimized stderr message.
2. Message: crafted for an agent reader — name the exact fix (`--description`
   with a short one-line spec) and clarify `--text` is the long-form plan, not a
   substitute. Actionable, no internal jargon.
3. Confirm the Python `task add` surfaces the Go stderr verbatim into the agent's
   tool output (it shells out to `endless-go event emit`). If it wraps or swallows
   the message, fix propagation so the crafted string reaches the agent.

## Must-verify during implementation
- Exact location of the JSONL append vs validation in the emit path — the check
  MUST precede the append (JSONL = WAL invariant; a rejected event must never land
  in the log).
- The precise actor field that marks "live session" (`Actor.SessionID`) and that a
  session-run `endless task add` actually carries it.
- That web / any other frontend also routes `task.created` through
  `endless-go event emit`. If so they inherit the rule automatically; the
  session-attribution condition leaves non-session creators unaffected.

## Out of scope — linked follow-ups (to file)
- Stop-hook sweep tripwire (defensive backstop): at session Stop, detect any
  session-created empty-description task and block/flag. Guards against this gate
  being bypassed or buggy; it is a tripwire on the invariant, not a second
  enforcement of it. Link `relates_to`.
- Background augmentation: a background process that detects human-created
  description-less tasks and has the AI write the description. Separate mechanism
  (worker/cron + AI call). Link `relates_to`.
- Description *quality* / placeholder linting — explicitly deferred.

## Dropped
- Python-CLI-layer pre-check ("approach A"): redundant with the Go choke point —
  same rule, same before-append timing, one layer up. Two copies would drift. Not
  implemented.
