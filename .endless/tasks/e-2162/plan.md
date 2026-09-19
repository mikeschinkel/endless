# Plan — a user-only act refuses an agent, in the code rather than by convention

## What this fixes

Some acts are the user's. Endless says so in prose — the lifecycle diagram
labels edges "user approves", "user declines", "user verifies", "user reopens";
the drop refusals say removal is the user's act — but nothing enforces it. An
agent that ignores the wording performs the act anyway, and E-2155's audit found
the messages actively offering the way around: `--force` on every drop refusal,
a raw `git worktree remove`, and the status change that reverses a decline.

Agent detection already exists and is the same rule on both sides:
`agent_env.present()` / `agentenv.Present()`, keyed on the Claude Code
environment. What is missing is using it to refuse.

## Decisions (Mike, 2026-09-18 — settled)

1. **The set**: `worktree drop`, `event rebuild-db --confirm`, `db restore`,
   `project unregister --purge`, and the `setup` commands that write the user's
   shell rc files and global Claude settings. NOT `worktree reap`.
2. **A user-owned status transition is refused for an agent only when it carries
   no outcome** (Mike, 2026-09-18). Refusing them outright was the first draft
   and it broke the workflow it was meant to protect: the user decides, and asks
   the agent to record the decision *and write the outcome prose*. So the line is
   not who types the command, it is whether the user's decision arrives with it.
   An agent taking a user edge must supply a non-empty `--outcome` in the same
   call; a bare status flip is refused.
   What this buys is the RECORD, not a defence: agents changing status in
   problematic ways is not a problem anyone has observed (Mike, 2026-09-18). The
   rule makes every user edge arrive with the reason for it written down, which
   is what a later reader of the task needs and what a bare flip loses. Do not
   implement it as if it were adversarial — no attempt to judge whether the text
   is "real".
3. **No escape hatch** for the commands in the set. No flag, no environment override. Following the no
   `--force` precedent (E-1577/E-1579): an override reached for once becomes
   habit, and a guard that is habitually overridden has stopped meaning
   anything.
4. **Consequence, accepted**: a `!`-prefixed command inside a Claude Code
   session inherits the agent environment, so `! endless db restore` is refused
   too. The refusal must say so plainly — run it from a terminal outside the
   session — because that message is the only thing standing between the user
   and the conclusion that Endless is broken.

## Build

1. **One predicate, one refusal.** A single helper — `user_only_act(what)` — that
   refuses when the environment says an agent, and returns otherwise. It carries
   the refusal text, so all of these read identically and none of them
   re-implements the check. The refusal is REPORT-class under E-2159 (the agent
   cannot continue; the user must run it), and it names the act, not a bypass.
2. **The command half.** Call it first in `worktree drop`,
   `event rebuild-db --confirm`, `db restore`, `project unregister --purge`, and
   the `setup` verbs that write shell rc files or global Claude settings. It runs
   BEFORE any prompt, so an agent gets the refusal instead of today's accidental
   "Aborted!" from a prompt hitting EOF — an accident that reads like a bug and
   depends on stdin rather than on anyone's decision.
3. **The transition half, in the executor.** The rule belongs where it cannot be
   bypassed: beside `ValidateStatusTransition` and `ValidateStatusActor` in
   `internal/events`, which is where E-2018 put the lifecycle guard for the same
   reason. Add the actor to the transition table in
   `internal/taskstatus/transitions.go`, whose edge labels already name it, then
   refuse a `user` edge taken by an agent actor **only when the same event does
   not set a non-empty outcome** (decision 2). Checking the event, not the CLI
   call, is what makes it hold for a direct `endless-go event emit` too.
   The labels are the specification — read them off the table, do not re-derive
   them: `ready` (user approves), `confirmed` (user verifies), `declined`,
   `obsolete`, `superseded`, reopening settled work to `revisit`, and
   `declined`/`obsolete`/`superseded` → `untriaged` (user reconsiders). Note what
   is NOT user-only and must keep working for an agent: `assumed` and an epic's
   `completed` are labelled agent edges, `underway`/`unverified`/`unreviewed` are
   the session's, and `revisit` from a pre-work status or from `underway` is the
   agent handing work back.
   Two commands have no outcome to carry and therefore become user-only in
   practice, which is the intended result and must be stated in their refusals
   rather than discovered: `task approve` takes no options at all — and `ready`
   provably meaning "a human approved" is the whole point of the two-step gate,
   since background sessions pick up only `ready` work — and `decision accept` /
   `reject` likewise take none. E-2047 is the task that would give a decision's
   status change a reason; when it lands, the same outcome-carrying rule applies
   to those verbs, and until then they refuse an agent outright. Do not invent an
   `--outcome` for either one here.
4. **Lifecycle artifact stays in step.** `just lifecycle-index` regenerates the
   diagram from the table; the actor column must not break that generation, and
   the regenerated artifact is committed with the change.
5. **The refusals the audit already classified** keep their text where it is
   still true, minus the bypass: the drop refusals stop offering `--force` and
   `git worktree remove` to an agent (E-2159 renders `human_remedy` for humans
   only), and this guard means an agent never reaches them.
6. **The hook keeps its own guard.** The PreToolUse worktree-removal block is
   unaffected: it catches the shell forms (`git worktree remove`, `rm -r`) that
   never enter the CLI. This adds the CLI half, for harnesses with no hook
   installed.

## Verification

- With `CLAUDE_CODE_ENTRYPOINT=cli` set: `endless worktree drop <id>`,
  `endless db restore`, `endless project unregister --purge`,
  `endless setup claude-hook` and `endless-go event rebuild-db --confirm` each
  refuse naming the act and telling the reader to run it in their own terminal;
  none of them prompts first, and none performs any part of the act.
- With the variable unset, each behaves exactly as today.
- For an agent: `endless task update <id> --status declined` refuses with no
  `--outcome` and succeeds with one; `task approve` and `decision accept` refuse
  either way; `--status unverified`, `--status unreviewed`, `--status assumed`
  and `--status revisit` from `underway` still work, outcome or not.
- The workflow this protects still works end to end: an agent asked to close a
  research task records `--status completed --outcome-file <path>` in one call.
- The refusal is reachable through the event pipeline, not just the CLI: a direct
  `endless-go event emit` of a user-only transition with an agent actor is
  refused too.
- `just test`, `just test-go`, and `just lifecycle-check` pass.
