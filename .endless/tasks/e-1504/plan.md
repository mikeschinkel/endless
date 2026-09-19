# Plan — the flag surface, and the three choices it turned on

Scope is the flag surface, as the description fixes it. No new renderings.

## The surface, counted

`--llm` sits on **15** commands, not the 12 the description names — `task
landed` and `task unlanded` arrived later via E-2095 and take the pair from
the `landing_report_options` factory, which is why a source count of
`@click.option("--llm"` misses them.

- **13 carry `--llm` and `--json`**: `task list`, `task show` (and its
  `detail` alias), `task next`, `task active`, `task recent`, `task landed`,
  `task unlanded`, `task unsettled`, `task search`, `decision list`,
  `decision show`, `epic list`, `epic show`.
- **2 carry `--llm` alone** and gain `--json` here: `task deps`,
  `task relations`.
- **14 carry `--json` alone** and are renderers: `project status`,
  `session show`, `session history`, `session status`, `session list`,
  `session search`, `task next revise`, `worktree list`, `worktree current`,
  `worktree show`, `worktree for-task`, `worktree diagnose`, `verb list`,
  `phrase list`.
- **2 more carry a `--json` that is NOT an output flag** and are therefore not
  part of this surface: `session order --json` parses its SPEC argument as a
  JSON array-of-groups, and `task import --json` names a file to read. The
  description's count of 16 includes both; E-2141's analysis had already
  spotted them, listing `session order`, `task import` and `task next revise`
  as "action-result commands, not readers" — of those three only
  `task next revise` actually renders, and it takes `--format` here.

Total: **30 commands** carry `--format` after this change, which is the same 30
E-2141 counted as "exposing an output flag".

## Three choices the description left open (Mike, 2026-09-16)

**1. `--format` reaches every command with an output flag.** Not only the
agent-facing ones. A command with an agent rendering takes
`--format text|json|agent`; a command without one takes `--format text|json`.
One spelling that works everywhere beats a spelling that works on half the
surface and leaves an agent to learn which half.

**2. A command with no agent rendering does not ADVERTISE `agent`, but still
RECOGNISES it.** Its `--help` metavar reads `[text|json]`, so nothing learns a
value that command cannot honour. Passing `--format agent` there is still
parsed and refused by name, with `--format json` named first as the remedy per
ED-1584 — an agent that guesses gets a pointer, not Click's generic "is not one
of", and never a rendering it cannot read. This needs a custom `ParamType`
rather than `click.Choice`, which cannot separate what it advertises from what
it accepts.

**3. `session status` gets the flag and not the rendering.** E-1504's analysis
argues this sweep must give `session status` an agent view carrying the
ownership signal (`relation`, `in_flight`) in words where the human view prints
`●`/`⟳`. The description, written later, rules agent renderings for the
`--json`-only commands out of scope, and it governs — that work is also
two-language, since `session status` is Go (`internal/sessionstatuscmd`). Nothing had
to move to carry the requirement forward: E-2141 already states it in full,
under its own heading "The requirement that must not be lost", naming the same
`classify()` ordering and the same ES-1196 incident. E-1504's analysis is a
duplicate of a live requirement, not its only copy, so it stays where it is as
this task's own record.

## How the rename is done

`--llm` is **retired, not aliased** — `retired_option("--llm", "--agent")`, the
pattern E-1000 established for `--text` → `--plan`. The flag stays declared and
hidden, so it never works and never degrades to "No such option": an agent
reaching for muscle memory is told where the flag went. That is the removal the
description asks for; a *working* alias is what no-legacy forbids, and this is
not one.

The internal `llm=` keyword renames to `agent=` alongside it, across
`task_cmd`, `decision_cmd`, `epic_cmd`, `session_cmd` and `rowcap`. A rename
that stops at the CLI leaves the codebase carrying the name it just retired.

## Contradiction is refused, not resolved

`--json`, `--agent` and `--format <fmt>` are three spellings of one setting.
Naming two DIFFERENT renderings (`--json --agent`, `--agent --format json`) is
a `UsageError` naming both spellings. Naming the same one twice is fine.

This is a deliberate behaviour change: today `--llm --json` silently yields
JSON, because every renderer tests `as_json` before `llm`. Silently picking a
winner is the silent incorrectness E-2143 exists to end — a wrong guess must
cost tokens.

## Where the machinery lives

`cli.py`, beside `retired_option` and `landing_report_options` — its siblings,
and the reason a separate module was dropped: `outfmt` would have had to import
`retired_option` back out of the module importing it. It holds the `FormatArg`
`ParamType`, `_resolve_output_format`, and the `output_options(*, agent=...)`
decorator factory that declares `--agent` / retired `--llm` / `--json` /
`--format` from the single fact of whether the command has an agent rendering.
Unlike `rowcap`, none of it is renderer-side: it is pure Click surface.

`--format` is resolved to the `agent` / `as_json` booleans the renderers
already take BEFORE the command body runs, so it costs the renderers nothing
and is an alias rather than a third thing to branch on.

The renamed internals reach further than the CLI: the `llm=` keyword is now
`agent=` across `task_cmd`, `decision_cmd`, `epic_cmd`, `session_cmd` and
`rowcap`, and in the test suite's own fixture names.

## Docs

`docs/guide/tasks.md`, `decisions.md`, `index.md` and `reference.md` name
`--llm` in 11 places; all move to `--agent`, and the guide gains `--format`.
`just guide-check` gates this.

## Out of scope, explicitly

Agent renderings for the 15 `--json`-only commands (E-2136 detail-shaped,
E-2141 list-shaped). Landed verify suites under `.endless/tasks/` that assert
`--llm` strings are left alone per `.endless/tasks/CLAUDE.md`.

## Durable coverage

`tests/test_output_format.py` (18 tests), including a **drift guard** that walks
the whole command tree: `--json` without `--format` fails, `--format` without
`--json` fails, `--format` advertises `agent` iff the command has `--agent`, and
every `--agent` command still carries a hidden `--llm` pointer. The two
input-`--json` commands are an explicit named exception, so a third one is a
deliberate act with a test to change rather than a silent omission.

This is the guard the surface lacked: `--llm` reached 15 of 30 commands and
`--json` 28 precisely because nothing asserted they travel together.
