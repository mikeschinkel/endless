# Why harness identity cannot live on `sessions`

`sessions.session_id` holds the harness's own conversation id. The harness
replaces that id at its own discretion, so it is not an identity Endless can
build an invariant on.

Measured, not assumed (2026-08-16):

- A clear mints a new `CLAUDE_CODE_SESSION_ID` — observed `738849b7…` becoming
  `8735f6a4…` — while `CLAUDE_PID` stayed `18201`. Same process, same tmux
  window, same task, new conversation id.
- `claude --resume <uuid>` does NOT mint. Session 1067 kept UUID `70e685df…`
  from 2026-08-12 to 2026-08-16.
- Compaction does NOT mint either. Session 1067 spanned a very long conversation
  that was certainly compacted and never changed id. This matters because
  compaction is automatic and unavoidable; had it rotated the id, any invariant
  keyed on the id would have been unworkable rather than merely leaky.

So: resume preserves, compaction preserves, a clear rotates.

## The anchor has to be an id Endless mints and owns

That is `sessions.id`. Everything the harness issues is a handle it may replace.

Two anchors were considered and rejected, both worth recording so they are not
revisited:

- **`CLAUDE_CODE_SESSION_ID`** — rotates on clear, which is the whole problem.
- **`CLAUDE_PID`** — rejected for three independent reasons, any one fatal. A
  resumed-but-not-running session has no PID at all at resolution time; a closed
  and later resumed session gets a different PID, so the invariant would not
  survive the gap it most needs to; and the OS recycles PIDs, which converts a
  correctness bug into an intermittent one. The general error was reaching for a
  RUNTIME HANDLE as a DURABLE IDENTITY.

## Shape

One `sessions` row per task (durable), with an ordered sequence of harness
instances beneath it. A clear appends an instance. Transcript rows FK to the
instance, not to the session, so a clear simply gives subsequent rows a new
parent and no prior row is rewritten.

Per-harness identity goes in per-harness tables joined by a composite FK against
`UNIQUE (id, harness)` on the base, so a `claude_cli_sessions` row physically
cannot attach to an instance belonging to a different harness. The single-table
alternative (harness identity columns on the base) was REJECTED by the
requester: it bakes one harness's identity model — single value, UUID-shaped,
rotating — into the shared table, and none of that is known to hold for the
next harness. `internal/hookcmd/codex.go` already exists, so the second harness
is not hypothetical.

**`platform` moves off `sessions` onto the instance.** Decided. `sessions` today
carries `platform TEXT NOT NULL DEFAULT 'claude'`; leaving it there would both
duplicate the instance's harness discriminator and make sequential cross-harness
work impossible by construction. The rule settled on: the same task may NOT be
worked by two harnesses concurrently (that is the one-live-instance violation
regardless of harness), but MAY be worked by different harnesses in sequence —
which falls out for free once the Endless session is harness-neutral.

## Ordering and selection

Order instances by `first_seen_at` ASCENDING, never by recency, and show recency
as a column. `first_seen_at` never moves, so instance 1 is always the
pre-clear conversation.

The failure case that forces this: a task runs for a week, is cleared midway,
and the real work lands in instance 2. The user later resumes instance 1 just to
read something, which bumps its recency and makes the half-finished conversation
look like the one to resume. **Recency and relevance diverge exactly when the
stakes are highest.** "Which instance should be resumed" is therefore not
derivable and must not be guessed.

## DECIDED (2026-08-25): no DB constraint; enforce at resume

`ended_at` is DISPLAY ONLY. No `UNIQUE (session_id) WHERE ended_at IS NULL`
index. `resume` refuses when it finds 2+ open instances for a session, lists
them, and lets the user close one. Nothing is ever closed automatically. A stale
`ended_at` renders as "unknown" rather than blocking anything.

This resolves a collision between two positions the requester had both stated:
`ended_at` is informational, AND the uniqueness constraint was wanted. The index
makes `ended_at` a constraint predicate, which is operational by definition, so
the two could not both hold.

The reasoning that settled it, in the requester's words: storage is the more
reliable layer only in the absolute — "when unreliable things are in charge of
writing to storage then the storage itself becomes unreliable." `ended_at` is
written by hooks; hooks fail open by design and do not fire on a crash. An index
over hook-written data does not contain that unreliability, it AMPLIFIES it,
converting "this row is stale" into "this write is refused". The failure mode of
the constraint is strictly worse than the failure mode it was meant to catch.

Consequences that follow and should not be re-litigated:

- The crash case stops mattering. A crashed instance leaves a stale open row;
  nothing reads it operationally, so nothing wedges. `resume` shows it as
  "unknown" alongside the recap and the user picks.
- Auto-close is not needed anywhere, which was the requirement — prior
  auto-close behaviour caused real damage.
- Enforcement of one-live-instance moves entirely to the hook-level invariant
  gate (E-1983) plus write-once `task_id`. Those are now the only things
  preventing genuine concurrency, so they carry more weight than they did when a
  DB constraint was assumed to be the backstop.
- Reuses the resume-disambiguation listing this design already needs; no new
  surface.

## Consequences for neighbouring commands

- **`spawn --reopen` collapses into `reopen` + `resume`.** `--reopen` currently
  does three separable things — flip a terminal task status back to workable,
  restore the worktree (reuse, or rebuild off main if reaped), and attach a
  session — plus a fourth path that navigates to a live owner instead of
  spawning. That last path is already `resume`'s behaviour. Split by what
  changes: `reopen` is a task-status verb, `resume` is a session verb that
  restores the worktree and navigates when the session is live.
- **`--new-session` should be deleted, not reinterpreted.** It exists to refuse
  inheriting the prior session's context. Under one-session-per-task it is
  incoherent: "start a new session" would have to mint a second `sessions` row
  for the task. Its only legitimate residue is "do not inject prior context into
  the handoff", which is a handoff-content flag (`--fresh-context`), not a
  session-identity one. The original motivation is unknown; the code comment
  cites E-894, but that task is "Move task display reads from Python to Go" and
  is a pointer to the mechanism, not the origin.

## Facts a planning session should not re-derive

- **Liveness is DERIVED, not stored** (E-1898, `internal/monitor/liveness.go`,
  states include `unbound`). It removed `sessions.process` precisely to stop
  storing liveness. `ended_at` therefore needs a deliberate justification rather
  than being added by reflex — the requester's position is that it is
  informational (which instance to resume), not operational.
- **Liveness cannot be derived inside SQLite.** It requires inspecting processes
  and tmux. A partial index can only reference stored columns, and a trigger
  cannot shell out. So DB-level enforcement REQUIRES some stored open/closed
  state; there is no purely-derived version of option (a).
- **`payload.Source`** (`startup` / `resume` / `clear` / `compact`) is declared
  on `claudePayload` and read NOWHERE. It is the field that distinguishes "the
  same work continuing" from "a different session took this window".
- **Hooks pin to main.** `endless-go hook` calls `PinMainDB`, and a
  cwd-self-detected sandbox deliberately does NOT suppress that pin (E-1700), so
  hook writes always reach the real ledger. Only an explicit `--config-dir`
  overrides it, and only tests pass that. Consequence worth knowing before
  adding any auto-close: a test that drives real hooks is one missing
  `--config-dir` away from mutating real sessions — `tests/tasks/e-1953-verify.sh`
  depends on exactly that flag today.
- Endless deals in tmux WINDOWS, not tmux sessions.

## About the exploratory report

`docs/private/research-2026-08-16-harness-sessions.md` records the wider option
space and is worth reading, with one correction: its "Facts" section states
"`sessions` has exactly one row per task. Period." and "a task is spawned
exactly once". Those are the requester's INTENDED INVARIANTS, not descriptions
of the database. Today, real (non-fixture) tasks carry up to 7 `sessions` rows —
1307 has 7, 1362 and 1322 have 6 — created by `spawn --reopen --new-session`.
Collapsing those into one session row plus N instance rows is migration work
this task owns. No task currently has 2+ NON-ended sessions, so the invariant is
not being violated live; it is simply not yet enforced.





# Field evidence: E-1732's three-session lineage (added from E-2080)

A second, independently observed instance of the defect this task fixes, found
while investigating an unrelated question. It is offered as corroboration, not
as new design input.

E-1732 carries three `sessions` rows, all minted inside half an hour on the
same morning:

| Session | Harness UUID | Lifespan | State |
|---|---|---|---|
| ES-879 | `26ba3f7d` | 05:04:36 → 05:29:54 | ended |
| ES-881 | `ee6eda74` | 05:29:52 → 05:30:15 | ended |
| ES-882 | `9f45bd76` | 05:30:17 → still live | working |

The task was never re-spawned, and no one intended three sessions. ES-881 lived
about twenty seconds and ES-882 was minted two seconds after it ended — the
signature of consecutive clears, each one appending a `sessions` row because
the harness handed Endless a new UUID.

What makes this a better exhibit than the original report is where the
conversation ended up. All of E-1732's actual work is attached to ES-879.
ES-882 — the row that is `working`, the row `task show` surfaces, the row a
human reads as "the session on E-1732" — contains no conversation at all: its
transcript holds only slash commands (`/effort`, `/model`, `/exit`), hook
attachments, and a stub assistant line. Its `Messages: 0` is accurate, not a
capture failure.

So the damage is not merely a duplicate row. Under the current model the
surviving row is the *emptiest* one, and the history is orphaned on a row
marked `ended`. Under the instance model these are one durable session with
three harness instances, and the conversation stays attached to the session
that owns it. Any migration that collapses existing multi-row tasks needs to
preserve that attachment rather than keeping only the newest row.

One further anomaly, unexplained and possibly a separate defect: ES-882's
transcript is filed under the harness's project directory for the MAIN
checkout, while ES-879's is filed under the directory for the task's worktree —
even though `sessions.cwd` for ES-882 records the worktree. Whatever re-minted
the instance appears not to have preserved the working directory the harness
derives that path from. Worth confirming before the migration relies on `cwd`.
