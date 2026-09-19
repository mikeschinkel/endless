# E-1889 — Make reopening your own landed work the documented, tooled norm

Absorbs E-1890 (tooling counterpart). Every decision below is settled; there are
no open questions. Where a choice had a plausible alternative, the rejected one
is recorded so it is not relitigated.

## Problem

Agents reflexively file a new task for a bug they just introduced rather than
reopening the task that introduced it. The result is backlog growth.

A backlog is never free, on any project. Every filed task is a standing claim on
future attention: it must be read, re-read, and triaged past on every pass,
whether or not it is ever worked. A task filed reflexively does not defer the
cost of the discovery — it converts a small immediate cost into a recurring one.

On Endless itself that cost is currently visible as pending worktrees gating a
planned Python-to-Go port, but that is one instance of the cost, not the reason
for it.

Reproduced live in the E-1845 session: four tasks filed during one task, two of
which (E-1888, E-1891) turned out to be a single defect and had to be merged
afterward, and one of which (E-1894) rested on a category error and was retired.

The cause is not agent carelessness. The handoff template instructs exactly this
behavior.

## Settled decisions

| # | Decision | Rejected alternative |
|---|---|---|
| 1 | `task reopen` is aligned to land `revisit`, like the other two reopen paths. Code change, not just docs. | Documenting the three paths' divergence as intentional. |
| 2 | Five templates get the full rewording; `brainstorm` keeps "file as new tasks" (that IS its deliverable) and gains only the root-cause test. | One wording across all six. |
| 3 | Mermaid gains `confirmed`/`assumed`/`completed` → `revisit`. `declined`/`obsolete` stay terminal in the diagram. | Drawing all five reopenable statuses. |
| 4 | Tooling folds into E-1889; E-1890 is absorbed and marked obsolete. | Keeping E-1890 for the recently-landed hint. |
| 5 | Claim promotion gains `revisit`. | Routing `spawn --reopen` through re-approval. |
| 6 | `_REOPEN_TO_REVISIT` narrows to E-1555's set; `declined`/`obsolete` are no longer implicitly revived. | Broadening `task reopen` to accept them. |
| 7 | Handoff uses the four-case bulleted form. | Compact prose; minimal edit. |
| 8 | `revisit` definition broadens to a two-case sentence. | Terse form; form with explicit pointer. |
| 9 | Guide pattern lives in `orchestration.md`, cross-referenced from `tasks.md`. | `tasks.md` only; `orchestration.md` with no pointer. |
| 10 | Verification splits by durability: behavior into the real suites, acceptance into the verify script. | Everything in one verify script. |
| 11 | `session resume --reopen` on `declined`/`obsolete` refuses loudly and names the explicit route. | Silently skipping the flip; a `--force` override. |

## A. Handoff templates

Under `internal` templatecmd templates handoff. Five files carry byte-identical
text at the noted lines: `todo.md.tmpl:12`, `bugfix.md.tmpl:14`,
`epic.md.tmpl:25`, `research.md.tmpl:14`, `respawn.md.tmpl:45`. Replace with:

```
4. Stay focused on E-{{.spawned_id}}. For anything you discover along the way:
   - Could it reasonably be done now, inside the work already underway? Do it —
     note it in the commit message, and add a `discovery` note to
     `endless task report`.
   - Is it a bug in work THIS session landed? Reopen that task
     (`endless task update E-NNN --status revisit`) and fix it there.
   - Otherwise file it (`--cleans-up E-{{.spawned_id}}`) and confirm before
     implementing.
   - Filing more than one? Check whether they share a root cause — file the
     cause, not each symptom.
```

"Report it" is spelled out deliberately: the earlier draft said only "do it,
and report it", which is the kind of vagueness that makes a bullet ignorable.
The two halves do different jobs — the commit message records WHAT changed, the
`discovery` note records THAT the session went beyond its brief. Without the
note the user has to read the diff to learn scope grew. `task report --json`
already accepts `{"kind": "discovery"}` and bounces entries it judges
ceremonial, so that filter — not the agent's judgment of significance — is the
guard against noise.

`respawn.md.tmpl` uses hyphen bullets with no step number; adapt the list marker
to match its surrounding style, keeping the four cases verbatim.

`brainstorm.md.tmpl:16` keeps its existing sentence — for a brainstorm, emitting
tasks and decisions IS the deliverable — and gains one line:

```
   Filing more than one? Check whether they share a root cause — file the
   cause, not each symptom.
```

## B. Canonical status lifecycle

Edit `docs/status-lifecycle.mmd`, then re-sync the three byte-identical copies
in `README.md`, `CLAUDE.md`, and `docs/guide/index.md`. Add:

```
    confirmed --> revisit: shipped work found wrong
    assumed --> revisit: shipped work found wrong
    completed --> revisit: shipped work found wrong
```

Keep the existing `confirmed --> [*]`, `assumed --> [*]`, `completed --> [*]`
edges — those statuses remain ordinary end states; the new edges say the door is
not locked.

`declined` and `obsolete` deliberately gain no edge (decision 6 removes the only
implicit path that revived them).

## C. `revisit` definition

`docs/guide/index.md`, the status table:

```
| `revisit` | Needs re-evaluation before it can proceed — either a partial plan
that no longer holds, or work that shipped and turned out wrong. Reopening your
own landed work lands here. |
```

The mermaid's existing `unplanned --> revisit: needs re-evaluation` label stays
as-is; the new edges carry the shipped-work case.

Note: the same table's `declined` row says "Requires `--reason`". Verified
correct — `--reason` is `task decline`'s flag, stored as outcome. Do not
"fix" it.

## D. Guide pattern — "Fix a bug in your own landed work"

New section in `docs/guide/orchestration.md`, near the existing `--reopen`
material (currently around the `task spawn --reopen` line). It must cover:

1. **The judgment.** A bug in work you just landed is not new work. Reopen the
   task that introduced it rather than filing a peer.
2. **The mechanics.** `task update <id> --status revisit`, then reuse the
   task's existing worktree rather than creating a second one for the same work.
3. **Re-verification.** The task's verify script still exists and still applies;
   re-run it rather than authoring a second one.
4. **The boundary.** When a genuinely separate task IS right — the discovery is
   new work the conversation surfaced, not a defect in what shipped.

One-line cross-reference from `tasks.md` under the status material, pointing
here.

## E. Code — reopen alignment

1. **`_reopen_task_core`** (`src/endless/task_cmd.py`, around the
   `new_status = "ready" if text_present else "unplanned"` line): always
   `revisit`. Keep returning `text_present` — callers use it for the message
   suffix — but stop branching status on it. Update the `reopen_item` docstring,
   which currently says "back to `ready` or `unplanned`".

2. **Five stale help/comment sites** assert the old target and must be updated
   in the same change: `task_cmd.py` at the spawn-reopen docstring, the
   `--reopen` help string, and the two reopen pre-step comments; plus the
   matching `--reopen` help text in `cli.py`.

3. **`_REOPEN_TO_REVISIT`** (`src/endless/session_cmd.py`): narrow to
   `{confirmed, assumed, completed}`, matching `_REOPENABLE_TERMINAL_STATUSES`.
   In `_resolve_resume`, refuse when `--reopen` targets a `declined`/`obsolete`
   task:

   ```
   Error: E-123 is 'declined' — a deliberate decision, not dormant work.
   Reviving it is an explicit act:
       endless task update E-123 --status revisit
   Then resume without --reopen.
   ```

   Follows the loud-failure-on-invalid-state convention used by the verb,
   maybe-parent, and db gates.

4. **Claim promotion** (`internal/monitor/session.go`): add `revisit` to the
   `status IN (...)` set. Without it, `task spawn --reopen` would bind a session
   and leave the task reading as not-started. The background-session gate is a
   separate check and is unchanged — background sessions remain restricted to
   `ready`.

## F. Code — three file-time hints

All three fire from `task add`. **Hints, never refusals** — sometimes a
genuinely separate task IS right, and the distinction needs human judgment.
Follow the existing gate/hint conventions.

1. **Recently landed.** When `--cleans-up X` names a task that the CURRENT
   session landed within 24h (join `task_landings` on `task_id` + `session_id`,
   filter `landed_at`), print: *"E-X was landed by this session Nh ago —
   consider `task update E-X --status revisit` and fixing it there."*

2. **Backlog pressure.** Print the count of open (`untriaged`/`unplanned`)
   tasks in the project, so the cost of adding one is visible at the moment the
   decision is made.

3. **Same-session root cause.** When this session has already filed at least one
   task (its `surfaced` rows in `session_tasks`), list them and ask whether the
   new one shares a root cause with any — *"file the cause, not the symptoms."*

   This is the mechanism that would have caught E-1888/E-1891. E-1739's
   semantic-dup gate provably would NOT have: those two were different files,
   different tuples, different symptoms, not semantically similar — yet one
   defect. Backlog-wide *similarity* and same-session *common cause* are
   different checks; E-1739 stays independent and is not superseded.

## Verification

**Real suites — permanent invariants:**

- pytest: `task reopen` lands `revisit` from each of the three reopenable
  statuses; `session resume --reopen` refuses on `declined`/`obsolete` and still
  works on the three; each hint fires when its condition holds and stays silent
  otherwise; no hint ever blocks the add.
- go test: claim promotion moves `revisit` → `underway`; the background-session
  gate still refuses non-`ready`.
- Mermaid byte-identity across the three copies belongs here too — it is a
  permanent invariant currently asserted in three separate verify scripts
  (e-1648, e-1832, e-1845). Promote it to a real test as part of this task and
  the copies stop multiplying.

**`tests/tasks/e-1889-verify.sh` — point-in-time acceptance only:**

- the four-case wording is present in all five templates;
- `brainstorm.md.tmpl` retains its own sentence and gained the root-cause line;
- the guide pattern section exists in `orchestration.md` with the cross-reference
  from `tasks.md`;
- the `revisit` table row reads as specified.

## Absorbed / superseded

- **E-1890** — absorbed. Mark obsolete with an outcome naming this task. Its
  recently-landed hint is section F.1 verbatim, including the 24h window and the
  hint-never-refusal constraint.
- **E-1739** — NOT superseded. Different check (see F.3).
- **E-1623** — unaffected, but note that promoting the mermaid assertion into a
  real test removes one of the duplicated blocks it would otherwise retrofit.



---

## Addendum — what actually landed, beyond the plan above

Recorded after the fact so the branch's contents are legible without reading
five commit messages. Everything here landed on E-1889's branch; nothing below
was in the plan when it was approved.

### 1. Rebase onto E-1822's handoff refactor

The plan says deliverable A edits five templates carrying byte-identical text.
Between approval and landing, E-1822 extracted that text into a shared
`handoff_focus` partial in `internal/templatecmd/templates/handoff/_mechanics.tmpl`.
The four-case wording therefore landed in ONE place, not five, and reaches a
sixth consumer the plan never mentioned — `claim.md.tmpl`, the
claimed-into-a-live-session handoff. `respawn.md.tmpl` still carries its own
copy, because E-1822 deliberately left it out of the shared mechanics: a future
edit to this wording needs both files. The reopen command also picked up
`--db main`, matching E-1822's worktree-routing convention.

Equivalent rendered result, better factoring, but the plan's "five files"
framing no longer describes the tree.

### 2. Supervisor test timing (commit 049fcba5)

`go test ./...` failed during verification on a machine running several
parallel task sessions: `TestSupervisorSetsProcessGroup` exceeded a 2s
post-signal wait. All three supervisor tests in
`internal/sandboxcmd/sandbox_test.go` asserted timing on top of the behavior
under test. The two shutdown waits moved to a named `shutdownWait` of 10s
(matching `waitFor`'s existing deadline in the same file), and the two
grandchild-reaped checks — single-shot polls taken the instant `Run` returned,
racing an asynchronous signal/reparent/reap — now poll via a shared
`assertReaped`. Assertions unchanged; only the timing assumptions.

Unrelated to reopen semantics. Folded in because it blocked this task's own
verification, and it was one root cause across one file.

### 3. Verify-suite prohibition, violated and reverted (commit c7f3f234)

This branch violated the rule in `docs/guide/orchestration.md` that a per-task
verify suite is a land-time gate, not a standing regression suite:

- `tests/tasks/e-1845-verify.sh` was edited to relax an assertion this task's
  own change to `internal/monitor/session.go` had broken;
- `tests/tasks/e-1872-verify.sh` was edited to track this task's guide rewrite;
- `tests/tasks/e-1889-verify.sh` invoked `e-1872-verify.sh` as a regression
  gate, and four landed suites were run as evidence of correctness.

All reverted before landing. Both scripts are byte-identical to their
pre-E-1889 state and will now FAIL if run, which is correct — they assert
wording their own tasks landed against. `e-1889-verify.sh` section F ends after
`pytest tests/` and carries a comment stating why nothing else belongs there.

The durable fix is E-1916 (hook-level enforcement), whose plan was updated from
this session to cover both suite forms rather than only the `.sh` form.

### 4. Verify suite id casing (commit 1cc9dd43)

Out of scope for reopen semantics; landed here at the user's direction rather
than as a separate task.

`.endless/tasks/E-1758/` was the only Endless path writing a task id uppercase
— `.endless/worktrees/e-1889/` and `tests/tasks/e-1889-verify.sh` are
lowercase. The cause was `verify.Discover`'s exact `eff.Task != id` compare
against the manifest's canonical `task = "E-1758"` field: an internal equality
check was dictating an on-disk convention.

Fixed at the comparison rather than by picking a winner. `verify.NormalizeTaskID`
folds ids; discovery compares normalized and keys the returned map by the
manifest's own value, so a lookup of `E-1758` still hits (the Python CLI always
shells out with `E-<n>`). The two suite dirs were renamed lowercase; their
`task` fields stay `E-NNNN`. The guide now states the convention explicitly.

Two invariants deliberately preserved: relaxing case must not relax identity (a
manifest naming a different task than its directory still fails loudly), and
uppercase dirs still resolve, so the landed suites building
`.endless/tasks/E-1234/` fixtures (e-1611, e-1618, e-1791) are neither touched
nor broken.



### 5. verify.toml removed from the guide (commit 278b4715)

Also out of scope for reopen semantics; landed here at the user's direction
rather than as a separate task.

`docs/guide/orchestration.md` documented the per-task `verify.toml` manifest
with a full schema, a runner section, and (after item 4 above) a casing
convention. The manifest form is not the norm yet and will not be until the
verification-suite epic lands — several of its children are still open,
including the one that retrofits the existing `tests/tasks/*.sh` suites onto
the shared harness.

The guide is product documentation for every project that uses Endless, so
presenting an unshipped form as "the settled form" told every reader to author
something the product does not yet accept. That is not hypothetical: two
sessions, in two different conversations, specified a `verify.toml` deliverable
on the strength of it — this session did so in E-1916's plan and had to be
corrected.

Removed: the manifest schema section, the runner section (it only works against
a manifest), and the `verify.toml` mention in the topic cross-reference. Kept:
the `tests/tasks/e-<id>-verify.sh` convention that actually works today, plus a
one-line "Forthcoming" note that a declarative manifest and its runner are in
progress — no schema, nothing to author against prematurely.

The replacement text carries NO task ids. Endless's own ids are meaningless to
a reader working on another project, so "until <epic> lands" cannot be the
explanation the guide gives; "in progress" is. This applies to the guide
generally, and the guide currently violates it in roughly twenty other places
(`E-`/`ED-` references in orchestration.md, sessions.md, tasks.md,
reference.md) — not addressed here, and not filed, on the user's standing
instruction that task proliferation is a real cost.

Note that item 4's casing paragraph was removed by this commit, since it lived
in the deleted section. The code change from item 4 stands; only its guide
documentation is gone until the manifest form ships.
