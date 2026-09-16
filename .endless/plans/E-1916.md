`docs/guide/orchestration.md` ("A verify suite is a land-time gate, not a
standing regression suite") states three prohibitions:

- Don't run another task's already-landed verify suite.
- Don't edit a landed task's verify suite.
- Coverage that must survive belongs in the project's own test suite.

The rule is documented and was still violated three times in one session
(E-1901), including by a session whose handoff explicitly named
`endless guide orchestration`. It was then violated twice more in E-1889, by a
session that had read the same principle in its own task's analysis field on
its first tool call. Documentation is the wrong layer: a rule that must be read
gets skipped, and the skip does not feel like a violation — running an existing,
runnable-looking script feels like diligence.

Put the enforcement where the act happens, in `internal/hookcmd/claude.go`,
following the `blockPlanFileWriteIfApplicable` precedent already in that file
(same shape, same class of problem: a path that must not be hand-edited).

## Both suite forms, from day one

A suite exists in two forms and the guard must know both, or it goes blind
exactly when the project migrates:

| Form | Path | Run by |
|------|------|--------|
| script (current) | `tests/tasks/e-<n>-verify.sh` | `./tests/tasks/e-<n>-verify.sh` |
| manifest (settled destination) | `.endless/tasks/e-<n>/verify.toml` | `endless task verify <id>` |

Both paths write the id lowercase. The manifest directories were uppercase
until they were renamed and `verify.Discover` was taught to compare ids
normalized (`verify.NormalizeTaskID`); the manifest's own `task` field remains
the canonical `E-NNNN` display form. Because an uppercase directory still
resolves, the id-extraction predicate here must accept either casing rather
than assuming the lowercase convention holds on disk.

Building the manifest arm now is not speculation. Two manifests already exist
and `endless task verify` already discovers and runs them. Shipping a
script-only guard would mean re-opening this task the moment the migration
lands — the precise failure mode this task exists to prevent.

### What must NOT be blocked

- **`.endless/verify.toml` at the project root.** Same filename, one level up,
  and it is not a per-task suite — it carries shared `setup`/`teardown`/`seed`/
  `needs` composing beneath every task manifest. The predicate keys on the
  `.endless/tasks/<id>/` segment, not on the basename.
- **The tests a manifest points AT.** A manifest is a pointer, never a
  re-description: its `[[check]]` entries select native `gotest`/`pytest` tests
  living in `internal/...` and `tests/...`. Those are the project's durable
  suite and must stay freely editable forever. Blocking the manifest must not
  leak into blocking what it names — which is the whole reason the durable-test
  half of the prohibition works.

## Arm 1 — refuse the EDIT (the more damaging violation)

`PreToolUse` on Write/Edit/NotebookEdit: refuse a target matching either suite
form when task NNNN has landed AND is not this session's active task.

This is the arm that matters most. In E-1901 the suites were edited via Edit
without ever being run, so any guard living inside the script itself was inert.
Retrofitting the target string of a landed suite rewrites the record of what was
true when that task landed. In E-1889 the motive was more specific and worth
naming, because it will recur: the session's own change broke a landed suite's
assertion, and it edited the suite to keep it green. That is the failure this
arm must catch — the edit feels like fixing a break, not like falsifying a
record.

## Arm 2 — refuse the RUN

Same handler, Bash tool. ONE invocation shape, refused for a landed, non-active
task: direct execution of a suite script (any of `./x`, `bash x`, `sh x`, an
absolute path).

A failure from a landed suite is meaningless by design, and acting on one
produces exactly the non-problem work E-1909 and E-1910 were (both declined).

### Amended (E-2023 planning, 2026-08-27): the runner clause is dropped

This arm originally also refused `endless task verify <id>` and the `just verify
<id>` wrapper. That clause is REMOVED, because E-2023 puts the same refusal
inside the runner itself, where the task id is known for certain.

The hook and the command are not equally good places for this check, and the
difference decides it. The hook matches a COMMAND STRING and infers the target
from it, so it can refuse an invocation that was never going to run a suite and
miss one spelled a way the pattern did not anticipate — false positives and
false negatives, both. The command already holds the resolved id as an argument;
it cannot be wrong about what it is being asked to verify. With the check in the
command there is no reason to also guess at it in the hook, and one rule
implemented twice against the same landed-ness lookup is a rule that can drift.

Direct execution stays here precisely because it is the case the command cannot
see: nothing routes through the runner, so nothing else is watching.

E-2090 narrows what that has to cover: it prepends a shared `_guard.sh` to every
existing suite script, so a direct run refuses from inside the file itself. Arm 2
is nevertheless RETAINED (Mike, 2026-08-27) as a backstop for the two cases an
in-file guard cannot reach — a suite checked out from a commit predating the
guard, and one hand-authored without it. It is no longer the only guard for those
~200 scripts, and after E-2090 it should be expected to fire rarely; that is the
shape of a backstop, not evidence it is unnecessary.

The interlock is worth naming, because it runs the other way too: the guard lives
INSIDE the file it guards, so Arm 1 — refusing edits to a landed suite — is what
stops the guard line being deleted. Neither arm makes the other redundant.

Note also that E-2023 relocates suites to `.endless/tasks/<id>/verify.sh`. The
path table above records what was true when this plan was written; whichever task
lands second reconciles. The move SIMPLIFIES this arm — both suite forms then sit
under the single `.endless/tasks/<id>/` segment the predicate already keys on for
manifests.

## Landed-ness test

`session-query task-report --id N` already returns a `landed` bool; reuse it
rather than adding a second notion of landed-ness. Carve out the session's own
active task so the live, pre-land gate still runs and can still be authored.

A reopened task (`revisit`, per E-1889) is once again the session's active task,
so the carve-out restores its suite to writable and runnable without a special
case — reopening genuinely does put the task back in its pre-land window.

## Why no directory move, and no per-script retrofit

Moving `tests/tasks/` → `verify/tasks/` was considered and rejected: 123 task
rows and 141 commit messages embed the current path, all of them immutable
records, and `.endless/tasks/<id>/verify.toml` is already the settled
destination as the runner matures — a move would churn the script form on its
way out. A rename also would not have prevented the violation: the scripts were
run because they existed and looked runnable, not because of the directory name.

Because the guard lives in the hook, NO existing suite is touched and every
landed suite of either form is covered the moment it ships. That is the point: a
per-script guard could only be added by editing landed suites, which is the
prohibition itself.

## Honest limits

- This cannot stop an agent from FILING a task about a landed suite — that is a
  judgment call no path pattern catches. E-1909/E-1910's decline reasons are the
  record for that case.
- It cannot stop a run laundered through an indirection the Bash matcher does
  not see (a wrapper script, `xargs`, a shell function). Arm 2 is a guardrail
  against the reflex, not a sandbox; Arm 1 is the arm that must be airtight.

## Verification

Unit tests on the pure predicate, covering both forms:

- path regex matches `tests/tasks/e-<n>-verify.sh` and
  `.endless/tasks/e-<n>/verify.toml`, in either casing (uppercase suite dirs
  still resolve), and not their neighbours;
- the project-root `.endless/verify.toml` is NOT matched;
- a path named by a manifest's `[[check]]` (e.g. `internal/monitor/x_test.go`)
  is NOT matched;
- landed + non-active → block; landed + active → allow; unlanded → allow;
- run-arm command parsing: the three script invocation shapes and
  `endless task verify <id>` / `just verify <id>` block; bare
  `endless task verify` allows;
- the block-response shape.

Those tests are the DURABLE deliverable and live in the Go suite beside the
handler. They are the coverage that has to outlive this task, so they do not
depend on the acceptance suite below existing.

### This task's own acceptance suite

Author it as a **script**:

    tests/tasks/e-1916-verify.sh

**Do NOT author a `.endless/tasks/e-1916/verify.toml`.** The manifest form is
not yet the norm and will not be until the E-1596 epic lands. As of this
writing E-1596 still has open children — E-1606, E-1608, E-1609, E-1614,
E-1623, E-1692, E-1792, E-1793 — including E-1623, which is the task that
retrofits the existing `tests/tasks/*.sh` suites onto the shared harness. Until
that epic closes, the script IS the form.

This is worth stating loudly because every surface an agent checks argues the
opposite, and two sessions have now reached the wrong conclusion from it:

- `endless task verify --help` reads as fully operational and tells you to run
  it before `task confirm`/`task assume`;
- two manifests exist on disk and the command really does run them;
- six E-1596 children sit at `assumed`, which reads as done.

(The guide used to be the loudest of these signals — it documented the manifest
with a full schema. That documentation has since been removed, because the
guide is product documentation for every project using Endless and must not
describe a form that has not shipped.)

All true, and none of it means the migration has happened. A working runner
with two exemplar suites is not a convention; E-1596 closing is.

Once this task lands, its own suite falls under the guard it ships. That is
correct and is worth checking by hand once: after landing, an attempt to edit
or run `tests/tasks/e-1916-verify.sh` from a session that does not hold E-1916
must be refused by the very hook this task added.

Surfaced by E-1901, where all three prohibitions were violated in sequence:
six landed suites run, three edited (reverted in 54df9715), and two non-problem
tasks filed and declined. Re-confirmed by E-1889, where two landed suites were
edited (reverted in that branch before landing) and four were run as a standing
regression gate.









---

## Reconciled at implementation (2026-09-16)

The plan was written before E-2023 and E-2090 landed. Three things it left
open are now settled, and one small thing grew:

**The script form moved, so there is one path predicate, not two.** E-2023
retired `tests/tasks/e-NNNN-verify.sh`; nothing is tracked under `tests/tasks/`
any more, and both suite forms now sit under `.endless/tasks/<id>/`. The plan
said whichever task landed second reconciles, so the legacy arm is not built.
The predicate keys on the `<suites-dir>/e-NNNN/` SEGMENT and therefore covers
everything in a suite directory, fixtures and helpers included — not just
`verify.sh` and `verify.toml`.

**The suites directory is read from `verify.SuitesDir`, never spelled.** Both
matchers are built from that constant, so a project whose suites live elsewhere
moves this gate with them rather than being guarded at a path it does not use.

**Ownership has one more source in the hook than in the runner.**
`monitor.SuiteOwnershipFor` supplies landed-ness (the same `task_landings`
lookup E-2023 uses, so the two cannot drift) and the two caller-tasks it can
resolve from the environment and the checkout. The hook adds the one only a
hook has: the session id Claude Code hands it, whose claimed task is the
session's own declaration. The union is the fail-open direction.

**Folded in, beyond the plan:**

- `monitor.taskReportFacts` now reads landed-ness through `taskHasLanded`
  instead of its own copy of the same query — the plan's "one notion of
  landed-ness", applied to the third copy of it in the package.
- `docs/guide/orchestration.md` names the new enforcement on both prohibitions.
  The guide said the runner refuses a foreign RUN and said nothing at all about
  who refuses an EDIT; after this task both are refused, and a reader who gets
  blocked looks there.

**Placement.** Arm 1 sits immediately after `blockPlanFileWriteIfApplicable`
and before `enforceWorktreeGate`, for the reason the plan-file gate sits there:
the suite-specific refusal must arrive ahead of the worktree gate's generic
"edits in main". Arm 2 sits among the registered-project Bash gates rather than
with the unconditional ones — an unregistered project has no landings to
protect, so the gate has nothing to say there and costs no database read.
