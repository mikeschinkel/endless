# Plan

Three increments. Each leaves the tree green; each is a commit boundary on the
task branch, not a separate landing. The plan had five; Increments 4 and 5 (the
column drops and the two table rebuilds, and resume picking an instance) moved
to E-2585, which lands after this task so the branches open during it can land
or rebase first.

The whole change is one sentence: **a fact about the harness conversation lives
on `session_instances`; a fact about the work lives on `sessions`.** Every
decision below was tested against that sentence, and several fields the earlier
plan moved turned out, on inspection, to be facts about the work.

## Increment 1 — the harness vocabulary

`platform` is a bare `TEXT` with no constraint in `schema.sql` and a
contradicting `CHECK (platform IN ('claude','codex'))` in `src/endless/db.py`.
ED-1506 governs finite value sets that drive code branching, so it becomes an
enum plus a mirror table.

- `internal/harness` — an int enum mirroring `gatekind`: `Harness int`, explicit
  numeric constants (no iota), `String()` returning the slug, `Label()`,
  `Parse()`, `All()`. The check against the table lives in
  `internal/harness/mirror`, so the enum stays a leaf the migration executable
  may link (agentenv returns it, and refusal imports agentenv).
- `harnesses (id, slug, label)` declared in the migration AND mirrored into
  `schema.sql` (still exec'd directly by tests until E-2021 generates it), its
  rows seeded from the enum, with the fail-closed integrity check wired into
  `monitor/db.go` beside the other mirrors. Where a harness names itself the
  slug is its own name; `harness.Unrecognized` is a row for an agent on a
  harness this binary cannot name, distinct from `Unknown` (no agent, no row).
- `agentenv.ID` becomes `harness.Harness`. It is a string enum today, which is
  the non-compliance ED-1506 exists to prevent; five real call sites.
- `task_landings.landed_by_harness` is dropped. Its one reader, the
  `task_landings_notify_sessions` trigger, left with E-2422, so nothing read it;
  the ledger keeps `actor.harness` on every `task.landed` event.

Existing `platform='claude'` maps to `claude_cli`. `'codex'` is dropped from the
vocabulary: nothing has ever written it, and `internal/hookcmd/codex.go` is a
nine-line "not yet implemented" stub. It returns when Codex does.

Nothing about sessions changes yet. Land-safe on its own.

## Increment 2 — the instance tables, written but not yet authoritative

```sql
CREATE TABLE IF NOT EXISTS session_instances (
    id                INTEGER PRIMARY KEY,
    session_id        INTEGER NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    harness_id        INTEGER NOT NULL REFERENCES harnesses(id),
    first_seen_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S','now')),
    last_event_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S','now')),
    ended_at          TEXT,
    launch_dir        TEXT,
    transcript_offset INTEGER NOT NULL DEFAULT 0,
    UNIQUE (id, harness_id)
);

CREATE TABLE IF NOT EXISTS claude_cli_sessions (
    session_instance_id INTEGER PRIMARY KEY,
    harness_id          INTEGER NOT NULL CHECK (harness_id = 1),
    uuid                TEXT NOT NULL UNIQUE,
    FOREIGN KEY (session_instance_id, harness_id)
        REFERENCES session_instances (id, harness_id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS session_instances_by_session
    ON session_instances (session_id, first_seen_at);
```

`CHECK (harness_id = 1)` names `harness.ClaudeCLI` in a comment beside it. The
composite FK against `UNIQUE (id, harness_id)` is what makes it physically
impossible for a `claude_cli_sessions` row to attach to an instance belonging to
another harness — the reason per-harness identity gets its own table instead of
columns on the base.

**NO `UNIQUE (session_id) WHERE ended_at IS NULL`.** `ended_at` is display-only
and that is settled; the index would make it a constraint predicate over
hook-written data, which amplifies rather than contains the unreliability. See
the analysis.

Backfill, in the same migration: one instance per existing `sessions` row —
`first_seen_at` from `started_at`, `last_event_at` from `last_activity`,
`transcript_offset` carried across, `harness_id` from `platform`, `ended_at` set
where `state='ended'`, `launch_dir` from the earliest `activity.working_dir` for
that row's UUID (NULL where there is none), and a `claude_cli_sessions` row
carrying `sessions.session_id` where it is non-empty.

Nothing reads the new tables yet. Land-safe on its own.

## Increment 3 — resolution moves to the instance

`sessions.session_id` stays in place and keeps being written through this
increment, so it is reversible. It stops being the resolution key and becomes
the UUID of the instance that last fired, so every reader still keyed on it —
Go and Python alike — reaches the conversation running now until E-2585
removes it.

- Every lookup of a UUID resolves `claude_cli_sessions` → `session_instances` →
  `sessions`: `GetActiveSession`, the touch, the bind, the transcript offset.
- `monitor.TouchInstance` (TouchSession wraps it for sibling shells) is
  resolve-then-write in one transaction. A known UUID bumps its instance's
  `last_event_at`, clears a stale `ended_at` unless the event is SessionEnd,
  and keeps `transcript_path` current. An unknown UUID either continues a
  session (below) or mints a session with its first instance. A sessions row
  carrying the UUID with no instance is adopted, not collided with. Nothing
  writes a sessions row without an instance.
- SessionStart asks `monitor.ContinuationOf` before the touch. The candidate is
  the session last seen in the same tmux pane, since a rotation keeps the
  harness process. The harness's own `continued-in` link in the candidate's
  transcript, naming the new UUID, is authoritative; failing that, only
  `source` `clear` is a continuation. A `fork` (`--fork-session`) is a second
  conversation and starts its own session; a `resume` of a UUID Endless never
  saw is no different from a fresh launch. No pane, no candidate: a new
  session.
- A continuation closes the session's open instances (`ended_at`), appends one
  for the new UUID, points `sessions.session_id` at it, and resets the
  session-level offset mirror. Nothing else closes an instance.
- Recorded per instance, on `claude_cli_sessions` because both are Claude Code's
  facts: `start_source` (the SessionStart `source` that created it) and
  `transcript_path` (the payload's, kept current). `launch_dir` is the payload
  cwd at creation, written once. Every SessionStart also logs its `source`.

# Verification

`.endless/tasks/e-2063/`, run only through the runner — `just verify E-2063`
(which routes `--db sandbox`, so the gate exercises the CANDIDATE binary) or
`endless task verify E-2063`. Never executed directly: `_guard.sh` refuses that,
and this suite drives real hooks, which is the case the refusal exists for.

Shape: a `verify.toml` whose first `[[check]]` is a `gotest` runner over the
instance-resolution unit tests — the fail-fast half — plus a `verify.sh` for the
end-to-end hook assertions. It sources `_harness.sh`, opens with the
DO-NOT-EDIT header naming E-2063, uses `section` / `assert_eq` /
`assert_contains`, and calls `summary` last; shipped fixtures come from
`$ENDLESS_VERIFY_DIR`.

Isolation is the runner's temp `HOME` and `XDG_CONFIG_HOME`, not a flag. The
hook still calls `PinMainDB` — a cwd-detected sandbox deliberately does not
suppress the pin (E-1700) — but "main" resolves inside the temp home, so it
lands on a throwaway database. Do not reach for `--config-dir`.

Coverage that must outlive the land is mirrored into the durable Go suite:
the clear-appends-an-instance rule (1) and the per-instance `transcript_offset`
(8) are behaviours nothing else protects once this suite stops being run.

1. A clear appends. Feed SessionStart with a NEW uuid and `source=clear` in a
   pane whose session exists: exactly one `sessions` row before and after, one
   more `session_instances` row after.
2. Resume does not append. Same, `source=resume` with the existing uuid: the
   instance count is unchanged.
3. Only a clear joins. A NEW uuid with `source` `startup`, `fork`, `resume`
   or `compact` in the same pane, and no `continued-in` link, gets its own
   session.
4. The prior instance is closed on a clear; the surviving one has
   `ended_at IS NULL`.
5. A stale `ended_at` blocks nothing. Leave two instances open, insert a third,
   assert it succeeds. The regression guard on the rejected unique index.
6. The composite FK rejects a `claude_cli_sessions` row attached to a
   non-`claude_cli` instance.
7. `mirror.VerifyIntegrity` fails closed on drift between the harness enum and
   `harnesses`, matching the four existing mirrors.
8. `transcript_offset` is per-instance: after a clear the new instance starts at
   0 while the prior keeps its own value. Without this the first event after a
   clear seeks a fresh transcript to the old file's offset — past EOF, or
   mid-line into garbage that `INSERT OR IGNORE ... message_uuid` would hide
   rather than surface.
9. `launch_dir` is written once and survives a simulated `/cd` — two events for
   the same uuid with different `payload.CWD` leave it unchanged.
10. `task_landings.landed_by_harness` is gone.
11. Backfill conservation, asserted as a law rather than a row count: every
    pre-migration `sessions` row has exactly one instance afterward, and every
    pre-migration uuid is still reachable through `claude_cli_sessions`.

Not verifiable by test: whether one open instance per session is actually
maintained in the field. Nothing enforces it at the storage layer, by design —
that is E-1983's invariant gate plus write-once `task_id`.

# End of plan

What follows is a record of how this plan differs from the one it replaced,
written on 2026-08-27 during a field-by-field review with the requester. It is
here because the previous plan's text survives only in the ledger and in
`git log -p` on the plan mirror, and neither of us would ever think to look.

## Already done — removed, not descoped

- **Delete `spawn --new-session`; split `spawn --reopen` into `reopen` and
  `resume`.** Both landed in E-1968. `--reopen`, `--new-session` and
  `--print-decision` are hidden flags on `task_spawn` in cli.py that refuse with a
  pointer to `session goto --resume`; `task reopen` and `session resume` are the
  split verbs. The previous plan's one OPEN question — whether
  `--new-session`'s residue deserved a `--fresh-context` flag — is moot, since
  there is no flag left to reinterpret.
- **Make `resume` and `goto --resume` share ONE resolver.** Already true.
  `_resolve_resume` in session_cmd.py backs both `resume_session` and
  `_resume_new_window_pane`.

## The twelve-column move, decided field by field

The previous plan asserted that twelve columns move to the instance and only
`id`, `project_id`, `task_id`, `epic_id`, `hidden` stay. It also gave a literal
`CREATE TABLE` containing eight columns and never added the other seven, and
Increment 2 named exactly one column to move. Reviewed individually, five of the
twelve are facts about the work:

- **`state`** STAYS. The instance carries `ended_at`, which the analysis settled
  as display-only; `state='ended'` is purely operational — every reader filters
  on it. Putting both on one table gives it two notions of "this ended", one
  ruled operational and one ruled informational, written by different paths and
  free to disagree. Only one instance is live at a time, so a single
  `sessions.state` describes the session accurately.
- **`process_id`** STAYS, and the previous plan had this backwards. A clear
  mints a new uuid and keeps the pane (measured: same `CLAUDE_PID`). Resume
  changes the pane — `session goto --resume` opens a new window, a tmux server
  restart re-identifies the same pane string as a different `processes` row —
  and provably does not mint a uuid. The two events are disjoint: what creates
  an instance does not move the pane, what moves the pane does not create an
  instance. Per-instance storage would record the same value twice and still not
  capture the changes.
- **`started_at`** STAYS, alongside the instance's `first_seen_at`. Immutable
  after INSERT, so a copy cannot drift; and the two answer different questions —
  when work on this task began, versus when this conversation began.
- **`last_activity`** STAYS, alongside `last_event_at`. Mutable, so it can drift,
  but both are bumped by the same `TouchSession` transaction; the alternative is
  a correlated `MAX()` on the hottest listing paths.
- **`last_user_prompt`, `report_bounces`, `report_exempt`, `report_runs`** STAY,
  untouched. The governing rule does not classify them: they are facts about the
  current TURN, not about the work or the conversation, wiped by
  `StageUserPrompt` at the start of the next one. Their fate belongs to the
  minimizer redesign, and this task should not be what decides whether that
  protocol survives. **Load-bearing consequence:** the `UserPromptSubmit` reset
  is now also the only thing keeping one conversation's turn state out of the
  next conversation's first turn. Anyone who moves or conditionalizes it needs
  to know that.
- **`summary`** does not appear because E-2074 removed it.
- **`auto_spawned`** (E-1814) STAYS. Added after this table was first written.
  It records how the SESSION came into being — the auto-spawn job opened it —
  which a clear does not change, and the auto-spawn cap gates on it, so it is
  operational rather than informational. A clear re-fires SessionStart in the
  same window, which still carries `@endless_auto_spawned`, so the value is
  re-asserted identically rather than contested.
- **`focus_task_id`** (E-2188) STAYS, and this one is a judgment call worth
  recording because the schema's own wording argues the other way. It calls the
  column "the task the session touched last — the one its CONVERSATION is on
  now", and by the letter of the governing rule a fact about the conversation
  belongs on the instance. It stays anyway, on the same reasoning as the turn
  state above: it is a display-only current-state pointer that moves freely with
  every touch, nothing gates on it, and only one instance is live at a time, so
  a single value on `sessions` is never wrong about the live conversation. What
  is given up is "which task was instance 1 looking at when it was cleared",
  which nothing asks.

## `sessions.session_id` is dropped, not kept

E-2585 does the drop; this task leaves the column as a bridge.

The previous plan kept it through Increment 2 for reversibility and never said
what became of it. It is dropped: it stores a value the harness rotates at its
own discretion, so a copy on the durable row goes stale by design, and keeping
it as "the current instance's uuid" means every append path must also rewrite
it. e-2074's header predicted the opposite — "E-2063 is about to reuse the same
nullability for harness instances" — which is now wrong; that change file is
history and stays as written.

## `launch_dir` is new, and `transcript_path` came back beside it

Not in the previous plan. `launch_dir` records where an instance began, for
resume to put the user back there.

The previous version of this section said `claude --resume <uuid>` resolves only
within the current directory's project dir, so resuming from elsewhere cannot
find the conversation. This conversation disproves that as stated: it was
resumed from the main checkout while its transcript was filed under its
worktree, on 2.1.301 and again on 2.1.319, and `/cd`'d back each time keeping
its UUID. E-2206's `/cd` on 2.1.300 minted a new UUID instead.

So where the transcript is filed is observed, not derived: the hook payload
names `transcript_path` on every event, and `claude_cli_sessions` keeps it
current. E-1905 dropped `sessions.transcript_path` because one value per
session went stale across a cwd change; per instance, and refreshed from the
payload, it cannot.

## `recap` and `recap_through` are not created

The previous plan's DDL had them. Nothing writes them — E-1906 removed the recap
machinery outright and E-1925 replaced it with an on-demand recap. Speculative
columns on a new table are the kind of thing that is still empty in a year, and
the resume listing distinguishes instances well enough with `first_seen_at`,
`last_event_at`, `launch_dir` and message count.

## The historical collapse moved to E-1983

The previous plan's Increment 3 re-parented the multi-row tasks here. Measured:
45 tasks carry more than one `sessions` row, 127 rows in total. Split by the
launch directory of each row's first hook event, 20 have a single directory
across all rows (plausibly genuine clears, this task's defect) and 25 have more
than one (at least one row is a separate session that was mis-bound, E-1983's
defect). E-1732 is the worked example, and is written up in E-1983's analysis.

A blind "keep the earliest row, make the rest instances" would assert
conversations that never happened on 25 tasks. Both populations need the same
repair, so it is done once, in the task that owns the cause, after both
mechanisms are fixed — per ED-1550(4), search the area for an owning task first.
Nothing here depends on it: historical rows are inert, every row but one per task
being `ended`.

## `platform` became an ED-1506 enum rather than a bare column

The previous plan moved `platform` to a `harness TEXT`. `src/endless/db.py`
carries a `CHECK (platform IN ('claude','codex'))` that `schema.sql` does not,
which is a second declaration of a schema constraint and exactly what ED-1472
put a stop to. ED-1506 governs the value set, so it becomes an int enum plus a
mirror table with a fail-closed integrity check — bringing `agentenv` into
compliance with it on the way. `task_landings.landed_by_harness` was going to
convert too; by implementation it had no reader left, so it was dropped
(2026-11-08).

## Naming

FK columns are the singular table name plus `_id`, matching every FK in
`schema.sql` — `session_instance_id`, never `session_instances_id`. There is no
plural FK column anywhere in the schema. Whatever else ends up pointing at an
instance takes the same form.

## Tables that were going to re-point, and do not

The previous plan sent `session_gates`, `report_labels` and `session_navigations`
to the instance as "per-conversation". All three stay on `sessions(id)`:
`session_gates` is minimizer corpus and the revisit gate, which is being
revisited as a whole; `report_labels` is written only from the sigil parser that
ED-1575 retired and holds zero rows; `session_navigations` records focus moves
between panes, and a clear does not move the pane. `session_navigations` is
being removed entirely by E-2081.

`session_messages` is the only table that re-points, which is the right answer —
it is the only one whose rows are literally the conversation.






