# Findings carried over from closed children

## Corpus keeps growing while the gate is off — E-2043 was not needed (2026-08-22)

E-2043 proposed an observe-only mode: the agent keeps running `task report`, the
draft is recorded, nothing is cut or enforced. Obsoleted, because
`session_messages` already holds what it would collect.

Measured on the real database: 28,910 assistant rows and 8,663 user rows,
untruncated (no spike at any round length, so nothing is being capped). The
prompt and the reply are both there. A turn is several assistant rows — 3.34 per
user message — so "the final reply" is the last assistant row before the next
user message, which is work but not a gap.

**The one thing `session_messages` cannot hold is `fetched_context`**: what the
minimizer was shown of the task plan and session status at that moment. E-1975
records it because replay must not re-fetch — the state has moved. It matters for
exactly one thing, and that thing is central: a candidate instruction whose job
is "do not repeat what they will read anyway" cannot be replayed without knowing
what was visible at the time. For fidelity and compression scoring, the triple in
`session_messages` suffices.

**If that capture is ever wanted, do it in the Stop hook, not with an
observe-only mode.** The hook already receives the final reply as
`last_assistant_message`, so it can record prompt, reply and fetch context with
no agent cooperation and no per-turn command. E-2043's mechanism charged the
agent a file write and a command every turn, on a channel that had been switched
off precisely because it was not trusted.

Caveat on that route, already documented in E-1953: `last_assistant_message` is
empty on a tool-only turn, and reportedly on some Claude Code builds.
`session_messages` covers those cases, so the two are complementary rather than
one replacing the other.



## `task report` must be told which database, not pin one — ED-1573 rejected (2026-08-22)

E-1975 landed `config.default_db_to_main()` at `task report`'s entry, so the
command silently uses the main database when given no `--db`. ED-1573 argued for
it and is REJECTED: it conflated which database is correct with who names it.

The Stop hook pins main, so a checkpoint written anywhere else is one the gate
cannot see. That settles WHICH database. It is not an argument for the caller
leaving it implicit — and ED-1561 (accepted) requires an agent to name the
database on every command that reaches one in a self_dev project, because an
agent runs commands in sequence with an inherited environment it did not set and
reports success from an exit code, so implicit routing makes its mistakes silent.
A forgotten flag refuses loudly. That is the better failure.

**The change**

  1. drop `config.default_db_to_main()` from `task report` in `src/endless/cli.py`
  2. have the SessionStart rule say `endless task report --db main …` in self_dev
     projects only

Step 2 is required, not cosmetic: `--db` is refused outside a self_dev project
("this project has a single database, so --db has nothing to select"), so the
instruction cannot be a constant. `reportChannelRule` is emitted through a
function that already resolves the project and branches on whether the channel is
on, so the conditional goes at a site that already conditions.

**Why this is not filed as its own task, and not a reopen**

Not a reopen of E-1975: it has landed four times and carries a 14k analysis, so
reopening it to change three lines forces a reader through a whole plan to find
the new part — the failure ED-1550 rule 2 names.

Not a reopen of E-2030 either. E-2030 last edited this same constant (cc094890)
and shipped correctly; nothing it did is wrong.

Left here as evidence rather than a task because the area is parked, and this is
three lines that whoever picks the epic up should make in passing.

---

# Scope added 2026-09-26: rename `minimizer` to `copyeditor`

Mike's decision: the feature is renamed `copyeditor`, because that describes
what it does far better than `minimizer`. Folded in here rather than filed
separately (ED-1550(4)): this is the task that owns the area, and renaming a
disabled feature just before redesigning it is churn — the redesign is when its
names should be settled. Do the rename as part of this work, not after it.

Footprint measured at filing, as a starting map rather than a checklist:
- Schema: `minimizer_champions`, `minimizer_evals`, `minimizer_state`,
  `minimizer_variants`, plus the `minimizer_evals_type` and
  `minimizer_variants_type` types. A table rename needs a schema change file and
  must keep schema.sql and the change in step (ED-1472).
- Config: the `minimizer` key in project config (`enabled`, `optimizer`), read by
  `MinimizerEnabledForCwd` and listed in `set_cmd.py`'s known fields.
- Code, tests and guide: 53 files under `src/`, `internal/`, `cmd/`, `tests/` and
  `docs/guide/` mention "minimizer" at filing time.

Pre-beta, so no config migration is required for existing `minimizer` keys
unless this task decides otherwise. E-2177 adds a code comment above
`taskReportRe` citing this task and the key as `minimizer.enabled`; update that
comment when the key is renamed.
