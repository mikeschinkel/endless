# Implementation spec — `ES-` refs everywhere + resuming a task-less session

Surfaced while verifying E-1914's reworked `session list`, which made task-less
sessions visible for the first time. ES-963 is the live example: 133 messages,
six `session_tasks` rows (revisited E-1776/E-1797, surfaced E-1801/E-1802/
E-1804/E-1805 — it is the session that designed `session resume` itself), and no
claimed goal. Both attempts to get back into it failed:

```
$ endless session resume ES-963
Error: no session matches "ES-963"

$ endless session goto ES-963 --resume
Error: session 963 has no task worktree to resume into (a background agent that never started?).
```

Two independent defects, plus a third found while inventorying. None is caused by
E-1914 — E-1914 only made the session visible enough to try.

---

## Thread 1 — accept `ES-` at every session-ref entry point

`ES-NNN` is the form `task show` prints under `Created:`/`Touched by:` and the
form `endless guide` tells sessions to prefer, precisely because a bare integer
matching both a session and a task is ambiguous. Today only half the entry points
honor it.

| Entry point | Backing | Reached by | `ES-`? |
|---|---|---|---|
| `_resolve_session` — `src/endless/session_cmd.py:62` | DB | `session history`, `session hide`/`unhide` (incl. `--task`) | yes (E-1914) |
| `_resolve_goto_target` — `session_cmd.py:~2010` | live panes | `session goto` | yes |
| `_match_companions` — `session_cmd.py:1778` | live panes | `session show`, `session cd`, `session use` (via `_resolve_companion`) | **no** |
| `ResolveResumeTarget` — `internal/monitor/resume.go:62` | DB | `session resume`, `session goto --resume` | **no** |

`session goto ES-963` works only because `_resolve_goto_target` strips the prefix
itself and passes bare digits downstream; every resolver it delegates to is still
prefix-blind.

### Fixes

1. **`_match_companions`** — strip a leading `ES-`/`es-` before the `isdigit()`
   branch. Without it the ref falls through to the UUID-prefix branch and matches
   nothing, so `session show ES-963` reports "No Claude session matches".

2. **`ResolveResumeTarget`** — add an `ES-`/`es-` branch routing straight to
   `resumeBySessionID`. It must be session-**explicit**: never fall back to the
   task lookup, mirroring how `E-` is already task-explicit. Stating the id space
   is the entire purpose of the prefix, so `ES-500` must never silently resolve to
   task E-500's session. The bare-integer path keeps its current
   task-first-then-session behavior unchanged.

### Deliberately NOT a shared parser

Go and Python cannot share code, so a "shared session-ref parser" means two
implementations plus a cross-language contract to keep in sync — the exact shape
that let `ES-` go missing on the Go side to begin with. The three resolvers also
have genuinely different policies (session-only vs session-or-task, DB-backed vs
live-pane-backed), so only the lexing is common and it is a few lines each.

The guarantee comes from a **drift test** instead (below): every entry point must
accept `ES-<id>`, enforced by a table a new entry point has to be added to.

---

## Thread 2 — auto-create a task + worktree for a resumed task-less session

`_resolve_resume` (`session_cmd.py:~221`) raises when `active_task_id` is NULL,
because `ResumeTarget.WorktreePath` is only populated for a task. A no-goal
session therefore has nowhere to be resumed into, even though its transcript is
intact and it did real work.

**Resuming into the project root was considered and REJECTED.** The project root
is the main checkout, which this project's own rules forbid editing — it would
drop a live Claude session somewhere it must not work, with no branch and nowhere
to commit. In Endless one session maps to one task, and the fix must honor that
rather than route around it.

**Prompting the user for a title was also considered and REJECTED.** Someone
resuming a task-less session is doing so *because* they do not know what it was
about; asking them to name it asks the one question they cannot answer. Figuring
that out is `session recap`'s job — a separate, additional command (see the
companion task), not a blocker for this one.

So: create the task, claim it, resume into its worktree.

1. **`internal/monitor/resume.go`** — `ResumeTarget` needs the session's
   `project_id` (or resolved project path) available to the Python caller so the
   task can be created in the right project. Fill it unconditionally, not only on
   the task branch; the task branch's `WorktreePath` stays exactly as it is.

2. **`_resolve_resume`** — when `task is None`, create and claim a task rather
   than raising:
   - **Title:** `Auto-resumed task for session ES-NNN`. A deliberate placeholder,
     not a description — it says exactly what it is and claims nothing about
     content it cannot know.
   - **Description:** name what the session already touched (one query against
     `session_tasks`), so the container is not contentless — e.g. "Container task
     auto-created so session ES-963 could be resumed into a worktree. The session
     had already touched: E-1776, E-1797, E-1801, E-1802, E-1804, E-1805."
   - **Status:** created already-claimed, straight to `underway`. It skips triage
     and the approve gate deliberately: a human ran `session resume`, so the
     approval that gate exists to capture already happened interactively. Triage
     could not judge a placeholder description anyway.
   - **Worktree:** reuse `task claim`'s existing worktree-creation path. Do not
     reimplement worktree setup here.
   - Print what happened before exec'ing — the user must see the task id and
     worktree they just acquired.

3. **Idempotency falls out for free, but assert it.** Once the first resume sets
   `sessions.active_task_id`, a second resume of the same session takes the
   ordinary task path and creates nothing. Cover it in the verify script so a
   future refactor cannot start minting a task per resume.

4. **Rewrite the diagnostics.** "(a background agent that never started?)" is
   plainly wrong for a 133-message session; that parenthetical belongs only on the
   no-UUID branch it was written for.

5. **Residual error cases** (keep them errors, and name the UUID so
   `claude --resume <uuid>` by hand is one copy-paste away): the session has no
   `project_id`, or worktree creation fails.

### `--dry-run` — widen the seam and rename it, decided

`session resume` ends in `os.execvp` (`session_cmd.py:432`): the process becomes
Claude, so nothing after it runs and a test cannot inspect the outcome.
`--print-decision` is the existing seam — it stops one line short of the exec and
prints the resolved target as JSON — but `session_cmd.py:405` refuses it unless
`--review`/`--reopen` was passed.

Remove that guard. `decision_out` is **already populated on the plain path**
(`session_cmd.py:213-218` sets `recovered`/`worktree`/`label`), so the guard is the
only thing preventing it from working, and widening it costs nothing. Extend the
emitted dict to carry the resolved uuid, session id, and — for Thread 2 — whether
a task was auto-created and which.

**Name it `--dry-run`, with `--print-decision` kept as a deprecated alias.** Once
the flag works on every path it is no longer printing a *recovery decision*, it is
declining to act — and `endless triage run --dry-run` already establishes that
spelling in this CLI for exactly this meaning ("print what I would do, write
nothing"). Keep the old spelling working so E-1801's verify script and any muscle
memory do not break; it need not be advertised in `--help`.

Verification must never launch Claude.

### Non-goals

- **`--review`/`--reopen` stay task-only.** They rebuild a *task's* dropped
  worktree from its landing sha or branch tip; with no task there is nothing to
  rebuild, which is why that path was never reachable for ES-963.
- **No cwd column on `sessions`.**
- **No transcript-derived title.** That is `session recap`'s job, in its own task.

---

## Verification

Write `tests/tasks/e-1918-verify.sh`, isolated exactly as `e-1914-verify.sh` is
(throwaway git repo as project root under a temp dir, own `XDG_CONFIG_HOME` and
`XDG_CACHE_HOME`, worktree binary on PATH, no real DB/ledger/cache touched).

Checks:

- **The drift test.** For every entry point in the Thread 1 table, `ES-<id>`
  resolves to the same session as the bare `<id>`. Table-driven over the command
  surface (`session show`, `session cd`, `session use`, `session history`,
  `session hide --task`, `session goto`, `session resume`), so adding a new
  session-ref command means adding one row — and forgetting `ES-` fails the suite.
- `ES-<n>` never resolves to task `<n>`'s session, even when a task with that id
  exists and has a session. This is the whole point of the prefix.
- `E-<task>` still resolves task-first; a bare integer still prefers the task and
  falls back to the session. Both unchanged.
- Resuming a task-less session creates exactly one task, titled
  `Auto-resumed task for session ES-NNN`, at `underway`, with a worktree on disk,
  and sets `sessions.active_task_id` to it.
- The auto-created task's description names the tasks the session had touched.
- **Resuming the same session twice creates exactly one task** (idempotency).
- A task-bearing session still resumes into its own worktree and creates nothing.
- The no-`project_id` case still errors, and the error names the UUID.
- `--dry-run` works on the plain resume path (it is the seam every check above
  runs through — no check may exec `claude`), and `--print-decision` still works
  as its alias.

Alongside the script: Go units for `ResolveResumeTarget` (the `ES-`/`E-`/bare
matrix and the project field) and pytest for the two Python resolvers.
