# Plan

Two independent halves. Either can land alone; B's two steps must land
together.

---

## A. The handoff / guide wording

**A1. `internal/templatecmd/templates/handoff/_mechanics.tmpl`**, the
`handoff_focus` define, non-brainstorm branch. Rewrite the discovery bullets:

- Bullet 1 states the axis and names the failure mode: the test is **cost and
  reviewer confusion**, not topical kinship. Say outright that "not a pure
  example of this task" is not a reason to file, and that a fix cheaper than
  the task row describing it gets folded in.
- Bullet 1 gains its third obligation: do the work, note it in the commit,
  **and record the grown scope on the task** (`task update <id> --text-file`,
  with `--keep-status` so recording delivered history does not re-spec a
  finished task). This obligation is why B2 exists — see below.
- New branch before "file it": **ask the user whether to file**, with the shape
  in the analysis — what was observed and why it matters to someone not in the
  session, the case for, the case against, and a recommendation. The session
  files or drops on the answer.
- Keep the existing bound: a *large* unrelated change still splits out.

**A2. `docs/guide/tasks.md`**, the "Work you discover mid-task" section. Same
restatement of test 1; add the ask branch as a peer answer; fix the override
clause whose **condition** ("cannot complete the task without") contradicts its
own **example** ("a test that fails for an unrelated reason") — reword around
*a check the session is obliged to run and report*, not around being blocked.

**A3. Tests.** `internal/templatecmd/claim_handoff_test.go` and
`internal/hookcmd/claim_handoff_test.go` assert on brief text; update the
expectations. Add one asserting the ask branch is present, so a future
compression cannot silently drop it again — which is how the override clause
went missing.

---

## B. The status-change surface

**B1. Audience-gate the output** — `task_cmd.py`'s update path.

Human behaviour is unchanged: status line, `auto_revisit` advisory,
`auto_untriage` advisory, all exactly as today. For an agent, print **only the
field changes it asked for**:

- suppress both advisories,
- filter the `status` tuple out of `changes` before `_emit_field_changes`
  when the status entry was *not* requested — i.e. it came from `auto_revisit`
  / `auto_untriage`, not from an explicit `--status`. An explicit
  `--status` is a field the agent asked for and still renders.

Gate on `agent_help.agent_view()`. Do not introduce a new predicate: that
module's history (E-1966, E-2006, E-2097) is three separate folds of competing
spellings of this same question into that one function.

**B2. Stop inferring `revisit` from a plan edit.**

Note the mechanics: `auto_revisit` already fires *only* from
`_REOPENABLE_TERMINAL_STATUSES`, so "don't fire on a terminal status" is not a
narrowing — it removes E-1762's auto-flip entirely. State that plainly rather
than smuggling it in as a tweak.

The argument for removing it: E-1762 was calibrated when a plan edit on a
finished task was rare and therefore suspicious. A1 makes such edits
**routine and benign** — every folded-in scope note is one. Once the common
case inverts, an inference tuned to the old base rate is wrong more often than
right, and it fires a *user-owned* edge: `{From: Assumed, To: Revisit, Actor:
ActorUser, Label: "reopens — shipped work found wrong"}` in
`internal/taskstatus/transitions.go`, with twins from `Confirmed` and
`Completed`. Reopening already has an explicit spelling — `--status revisit` —
and it belongs to the user.

**This is the one judgement call in the plan and the reason it wants approval
rather than just review.** The alternative — keep the auto-flip and lean on
`--keep-status` in A1 — is defensible; it just leaves a trap armed for anyone
who forgets the flag, which is what happened on E-2114.

If removed: delete the `auto_revisit` predicate and its emission, leave the
transition-table edges alone (they are the explicit path), and check whether
`docs/status-lifecycle.mmd` renders a system-actor edge that must be
regenerated with `just lifecycle-index`.

**B3. Tests.** Python: an agent-view update renders field changes without the
unrequested status line or either advisory; a human-view update renders all of
it; an explicit `--status` still renders under agent view. Plus: a plan-text
edit on an `assumed` task leaves it `assumed`. Go: `just lifecycle-check` if
the table or the diagram moves.

---

## Verification

`just test`, `just test-go`, `just build`, `just guide-check`,
`just lifecycle-check`, plus a suite at `.endless/tasks/e-2120/verify.sh`
driving a real `task update` under both audiences.


---

## Delivered — where the work went past the plan

Recorded here per the rule A1 adds, so the task row says what shipped under it.

**B1 covers the tier-1 advance too.** The plan's criterion is that a status
entry is filtered when it "was *not* requested", and it enumerated the two
sources it knew of. `--tier 1` on a pre-work task also writes a status the
agent did not name, so it is filtered under the same test — implemented as
`status is None`, which is the criterion itself rather than a list of
inferences a later one could escape. Asserted in both the unit tests and the
verify suite.

**A1's `--keep-status` is belt-and-braces after B2, not the trap-avoidance the
plan described.** With the auto-revisit gone, recording scope on a finished
task no longer moves it, so the flag is a no-op there. It stays in the
instruction because it is still correct on a task parked at a pre-judgment
status, and a test pins it as a harmless no-op rather than leaving it
undefined.

**B2 needed no diagram regeneration.** The auto-revisit was a Python-side
inference, not a table edge; the `Assumed/Confirmed/Completed → Revisit` edges
it drove are `ActorUser` and stay. `just lifecycle-check` is clean and the
verify suite asserts it.

**Doc surfaces the removal touched**, beyond the two the plan named: the
`--keep-status` inference table and its prose in `docs/guide/tasks.md`, the
same paragraph in `docs/guide/index.md`, the flag's `--help` text in
`cli.py`, and a stale comment in `internal/events/pre_judgment_test.go` that
cited the auto-revisit as the owner of the post-implementation statuses.


**A1 also restores the two clauses the analysis found missing, on Mike's
call.** The analysis's "Secondary" section reported that this partial had
compressed the guide's numbered tests into bullets and kept neither the framing
("filing is one of N answers, not the default") nor the override. A1 did not
schedule either; both are now in the handoff:

- The framing opens the discovery list, so it does not read as a menu of
  equals — ED-1550's arithmetic is the reason the list exists.
- The override is a final bullet, phrased around the obligation to run and
  report rather than around being blocked. `handoff_close` orders every session
  to run the project-wide regression, so a red test the session did not cause
  is a situation the handoff itself creates and had no answer for. That is the
  live case the task came from.

Both are pinned in `internal/templatecmd/claim_handoff_test.go` and in the
verify suite, matched against whitespace-flattened output so a reflow that
changes nothing a session reads cannot fail them.

**Landed on top of E-2123**, which shipped while this was in flight. No textual
conflict: E-2123 changed the six per-type handoff wrappers, this changed the
shared mechanics partial. Its `--text` → `--all-fields` sweep is the `task show`
READ flag; the `--text-file` in this task's fold-in bullet is the write flag and
is correctly untouched. Both changes are visible in one rendered handoff.
