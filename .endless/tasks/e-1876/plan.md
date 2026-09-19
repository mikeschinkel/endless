# Plan — E-1876: the output-discipline contract and its remaining surfaces

Five parts, one land. Parts 1–4 are independent of anything in flight; part 5
reads rows E-1901 creates and must be implemented after E-1901 lands (this task
is `blocked_by E-1901`).

## Framing decision that makes parts 1–2 buildable

The merged-away tasks described parts 1 and 2 as *definitions* — "a test an
agent can actually apply". A definition with no delivery surface is a document
nobody reads at the moment of composing. So **the rule is delivered as prompt
text on surfaces that already fire**, not as prose in a guide page:

- `report_prompts.NOTE_CHECK` / `QUESTION_CHECK` — the Haiku classifiers that
  already KEEP/DROP each entry at `task report --json` time. This is where the
  discharged-fact test does mechanical work.
- `report_prompts.STEER` (and E-1901's renamed `nothing-to-report`) — the
  compose-time framing.
- The guide gets the human-readable statement so the rule is citable, but the
  guide is the *documentation* of the rule, not its enforcement point.

Consequence: parts 1–2 are largely prompt-text work plus one mechanical check,
which is why they fit in the same task as parts 3–5 rather than dwarfing them.

## Part 1 — the discharged-fact test

**Rule.** A fact is *discharged* — and must not appear in user-facing output —
when it is already recorded somewhere durable the user can reach: a test, a
guard, a verify script, a commit message, or a task field (`outcome`,
`analysis`, `text`, an annotation). The agent introducing a problem and then
fixing it is the paradigm case: the fix is the record, and nothing is left for
the user to decide.

**Boundary against a genuine open decision.** A fact stays sayable when the user
must *act* on it and no artifact will prompt that action: an unresolved choice
blocking the work, or an out-of-band condition (a service down, a credential
expired) with no durable home. The test is *actionability by the user*, not
*importance to the agent*.

**Delivery.** Rewrite `NOTE_CHECK` in `src/endless/report_prompts.py` to make
dischargedness an explicit DROP criterion — today it drops "restates something
already visible in git/task state", which misses the recorded-in-a-test and
fixed-it-myself cases. Add the self-inflicted-and-fixed case by name; it is the
one agents rationalize past.

## Part 2 — veracity

**Rule.** Any fact that *is* surfaced must be verified against live state (git,
the ledger, the file) at the moment of surfacing — never inferred, never
recalled from earlier context. A wrong fact clears a higher bar than an omitted
one: the user must spend effort to disprove it.

**Delivery — prompt.** Both check prompts gain a veracity clause: an entry
asserting state the agent has not just verified is DROP, not KEEP.

**Delivery — mechanical check.** In `report_cmd.py`, before the Haiku pass,
extract `E-\d+` / `ED-\d+` citations from each `--json` note and question and
resolve them against the DB. An unresolvable ID is rejected with the offending
citation named. This catches the cheapest and most common wrong fact — a
hallucinated task reference — without a model call.

Topical-relatedness of a citation and git-contradiction checks are explicitly
**out of scope**: both need judgment, and a false positive here would teach the
agent the check is noise. The rule covers them; only ID-resolution is mechanized.

## Part 3 — `endless task annotate` (the sink)

**Storage.** New table, not the unused `tasks.notes` column — annotations are
append-only rows with their own timestamps and authorship:

```sql
CREATE TABLE IF NOT EXISTS task_annotations (
    id         INTEGER PRIMARY KEY,
    task_id    INTEGER NOT NULL,
    session_id INTEGER,
    body       TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S','now')),
    FOREIGN KEY (task_id)    REFERENCES tasks(id)    ON DELETE CASCADE,
    FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE SET NULL
);
```

Plus `internal/schema/changes/e-1876-task-annotations.sql` for the populated
real DB, matching the E-1901 change-file pattern.

**Command.** `endless task annotate <id> "..."` — append only. No edit, no
delete: an observation sink that can be curated is a second inbox.
`session_id` resolves via the existing `_current_endless_session_id()`,
best-effort.

**Display.** `task show --annotations` only. Decisions taken:

- **`--all-fields` does NOT include annotations.** It reads as "show me
  everything", which is exactly the reading path the sink must stay out of;
  including it would relocate noise to where the user already looks. The flag's
  help text is amended to say so explicitly, since the exclusion is otherwise
  surprising.
- **`--json` DOES include them.** Machine consumers are not the audience being
  protected, and excluding them would make the rows unqueryable in practice.

**Ordering.** Oldest first, each line stamped and attributed to its session.

## Part 4 — the plan-write trigger

Mirror E-1772 exactly. In `src/endless/task_cmd.py`, alongside
`_is_report_wind_down` / `_maybe_emit_report_reminder` (≈ lines 2477–2525), add
`_maybe_emit_plan_written_reminder`, fired when `task update` writes a non-empty
`--text` / `--text-file`.

Reuses E-1772's gate verbatim: fires only under `_running_under_agent()` or
`agent_view_requested()`, so a human editing a plan never sees it.

Wording (a fifth entry, `plan-written`, on the report-prompts surface so it is
tunable without touching source): the plan is saved and the user will read it —
do not recap it, do not summarize what you just wrote. Reply with a genuine open
decision or blocker that must be answered before implementing, or say nothing.

**Fires on `--text` only** — not `--description`, not `--analysis`. The recap
pathology is specific to writing a reviewable plan the user is about to read.

**Plan-mode saves** route through the same `task update --text` path, so they
are covered without a separate hook.

## Part 5 — the monitor surface (after E-1901)

E-1901 Part 2 lands `session_gates.sanctioned_text` + `bounces`, the
`GateKindRelay` kind, and `SetRelayCheckpoint` / `PendingRelayCheckpoint` /
`ClearRelayCheckpoint` / `BumpRelayBounce` in
`internal/monitor/session_gate.go`. Part 5 is a read on those rows.

Add a `LastRelayCheckpoint(sessionID)` accessor returning timestamp and bounce
count, and render it in `internal/sessionstatuscmd/session_status.go` — the
renderer `session monitor` loops over. One line per session:
`last checkpoint: 2m ago` , with the bounce count shown only when non-zero,
since a zero is the expected case and printing it is itself ceremony.

Detection logic lives in Go, not `settings.json`.

**Explicitly dropped from the merged E-1826**: recording the checkpoint (E-1901
Part 2 does it) and a record-only Stop hook (E-1901 blocks by design, so the
sidestep it was built around no longer applies).

## Part 6 — the guide statement

Three subsections in `docs/guide/tasks.md`, inserted under **Reporting to your
user** after the functional-rule paragraph (≈ line 246) and before
`### The FULL STATUS escape hatch`. That paragraph already states the rule;
these are the parts it leaves undefined. Approved wording, to be inserted
as-is:

---

### A fact already recorded is a fact already reported

The functional rule above turns on whether your user **could not already
compute** something. The most common way an agent gets this wrong is surfacing a
fact that is already written down somewhere durable.

A fact is **discharged** — and must not appear in your output — once it lives in
a test, a guard, a verify script, a commit message, or a task field (`outcome`,
`analysis`, `text`, an annotation). The record is the report.

The case agents rationalize past is **the problem you introduced and then
fixed**: the fix is the record, the test you added is the proof, and nothing is
left for your user to decide. Narrating it converts finished work back into
something they have to read.

The boundary is **actionability**, not importance. A fact stays worth saying
when your user must act on it and no artifact will prompt that action — an
unresolved choice blocking the work, or an out-of-band condition (a service
down, a credential expired) with nowhere durable to live. "Important to me" is
not the test; "they must do something, and nothing else will tell them" is.

### A surfaced fact must be true right now

Volume is the cheaper failure. A **confidently wrong** fact costs your user more
than ceremony does: they must spend real effort to disprove it, and they may not
bother — so it can outlive the session.

Anything you do surface must be **verified against live state at the moment you
surface it** — read the file, query the ledger, ask git. Never from memory,
never inferred from earlier in the conversation, where the state may have moved
underneath you. A wrong fact clears a **higher** bar than an omitted one: when
you cannot verify it cheaply, omit it.

### Where the suppressed observation goes

Cutting a fact from your output does not mean discarding it. Genuine
observations that fail the tests above have a home:

```bash
endless task annotate <id> "..."
```

Task-scoped, append-only, and **hidden from the default `task show`** — visible
only under `task show --annotations`. A write-only sink by design: it exists so
you have somewhere to put an observation without putting it in front of your
user, and it stops working the moment it becomes a third place they have to
read. Write it there **instead of** saying it, never in addition.

---

**Ordering constraint.** The third subsection documents `task annotate`, so
part 6 lands with part 3, never ahead of it.

**Drift guard.** The discharged-fact test now exists on two surfaces: this guide
prose and the `NOTE_CHECK` prompt constant. They are worded for different
readers (a human citing the rule vs. a classifier applying it), so they are not
literal copies — but they must not disagree about *what counts as discharged*.
The verify script asserts both enumerate the same five discharge sites (test,
guard, verify script, commit message, task field) by token match, so adding a
site to one without the other fails.

## Verification — `tests/tasks/e-1876-verify.sh`

Single entry point, fail-fast:

- **Python.** `task annotate` appends a row; `task show` omits annotations by
  default AND under `--all-fields`; `--annotations` shows them oldest-first;
  `--json` includes them. The `plan-written` reminder fires on `--text` under
  `CLAUDECODE=1`, and does **not** fire on `--description`, on `--analysis`, or
  for a human invoker. An unresolvable `E-` citation in a `--json` note is
  rejected and names the citation; a resolvable one passes.
- **Go.** `LastRelayCheckpoint` returns the most recent row per session; the
  status renderer omits a zero bounce count and shows a non-zero one.
- **Schema.** The change-file applies cleanly to a populated DB and is
  idempotent.
- **Prompt-surface.** `plan-written` resolves through the project > machine >
  embedded precedence like the existing four.
- **Guide.** The three subsections are present in `docs/guide/tasks.md` under
  **Reporting to your user**, and the drift guard passes: the guide prose and
  the `NOTE_CHECK` constant enumerate the same five discharge sites.

## Sequencing for the implementing session

Parts 1–4 first, in one pass — they share the `report_prompts` and `task_cmd`
surfaces. Part 6 with part 3, since it documents `task annotate`. Part 5 last,
after confirming E-1901 has landed and its column and accessor names match what
this plan assumes; if E-1901 shifted, part 5 follows it rather than the reverse.

## Decisions taken (were open; settled by the user)

1. **`task annotate` takes an explicit task ID.** Most commands already require
   one, so this stays consistent with them. Whether *any* command should default
   to the session's claimed task is a cross-command question that has
   deliberately **not** been filed as a task — if it is ever taken up, it should
   be as one analysis across all commands, not as a special case here.
2. **The guide gets the prose statement, in this task.** It ships together with
   part 3 rather than as a follow-on, so the guide never documents a command
   that does not yet exist. See part 6.
