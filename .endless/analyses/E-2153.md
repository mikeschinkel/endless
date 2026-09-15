## What happened

I coined "attention board" for `project status` (the snapshot) and `project
monitor` (the live loop), then used it as though it were the product's name. It
reached roughly twenty sites across three landed tasks before Mike caught it
(2026-09-15): "use the terms I know, don't coin novel new terms."

## Why it is worth removing rather than tolerating

It is not a style preference. Three concrete costs:

1. **It names nothing.** A reader meets a phrase that is not a command, not a
   flag, not a package, not a file, and not in any help text, and cannot tell
   whether it is a third surface or a synonym for one they know.
2. **It collapses two things into one.** `project status` and `project monitor`
   are a snapshot and a loop. "Attention board" means either, so every sentence
   using it is ambiguous about which — and some of the comments carrying it are
   in places where that distinction matters.
3. **One instance is user-facing.** `endless-go --help` prints "render the
   project attention board", so the coinage is in output, not just comments.

## The sites

Go source and docs:

- `cmd/endless-go/main.go` — the subcommand doc comment and the `--help` line
- `internal/monitor/project_status.go` — file header and the row type's doc
- `internal/monitor/session.go`
- `internal/projectstatuscmd/board.go`
- `internal/liveview/liveview.go`
- `internal/hookcmd/notification.go`
- `internal/config/config.go` and `internal/config/README.md`
- `internal/monitor/project_status_test.go`,
  `internal/hookcmd/notification_test.go`, `tests/test_project_status.py`,
  `tests/test_cli.py`

Replace with whichever of `project status` or `project monitor` the sentence
actually means, or both where it means the pair. Do not substitute a different
coinage; if a sentence needs a general term, name the two commands.

## Deliberately NOT in scope

The verify suites of landed tasks — `.endless/tasks/e-1976/verify.sh`,
`e-2091/verify.sh`, `e-2105/verify.sh` — keep the wording they landed with.
`.endless/tasks/CLAUDE.md` is explicit: a landed suite records what was true when
its task landed, and retrofitting it to a later change rewrites that history.
A terminology sweep is exactly the kind of later change that rule exists to keep
out. Leave them.

`.endless/plans/E-1976.md` and `.endless/analyses/E-2105.md` likewise: they are
the record of what was planned and reasoned at the time, not live documentation.

## Verification

A sweep asserting the phrase appears nowhere in Go source, Python source, or
`docs/`, with the landed suites and plan/analysis mirrors excluded — and that
`endless-go --help` no longer prints it.

## What the work actually covered (recorded after implementation)

Two things the sweep grew beyond the site list above. Both were cheaper to do
here than to describe in a task row, and neither enlarges the diff beyond the
sentences the coinage was already sitting in.

### One site the site list missed

`internal/sessionstate/sessionstate.go` is not listed above because the grep
that enumerated the sites could not see it: its instance was split across a
comment line wrap —

    // ⚠ for `prompted` is borrowed the same way (E-2091): it is already the attention
    // board's glyph for its waiting rank, …

A line-oriented grep matches within one line, so the phrase was invisible to
the one that found every other site. The verify suite therefore sweeps twice:
once flat, and once over files whose lines have been joined and whose comment
markers have been stripped. The joined sweep is what found this one, after the
flat sweep reported the tree clean.

### The short form, where its antecedent was the phrase

"The board" is used throughout as an ordinary noun for a rendered display, and
a project-wide rename of that word is not this task. But in six places the
short form's ONLY antecedent was a phrase this task deletes, so removing the
phrase alone would have left a pronoun pointing at nothing:

- `cmd/endless-go/main.go` — the `project-window` doc-comment and `--help`
  lines ("the two-pane tmux session the board lives in"), directly beneath the
  `project-status` line that introduced the coinage.
- `internal/faultbadge/faultbadge.go` — "The project board passes its own
  project", the sentence paired with one that names `session status`.
- `internal/projectstatuscmd/board.go` — "The board answers one question",
  immediately under the file header being rewritten.
- `internal/taskstatus/taskstatus.go` — "the whole basis on which the board
  decides a row is worth a line".
- `internal/sessionstate/sessionstate.go` — two further comments beside the
  wrapped instance ("a stale glyph sits on the board", "the board's attention
  ranking", the latter being the coinage re-spelled in reverse).
- `docs/guide/reference.md` — "the live version of the same board", the
  sentence after the definition being removed.

Each now names the command. Bare "board" elsewhere — the filename
`board.go`, the guide's `--limit` paragraph, the Click help for
`project monitor --tmux` — is left alone: it is not the coined term, and a
rename of it is a different change.

## Verification as built

`.endless/tasks/e-2153/verify.sh`, 19 checks in five sections: the packages and
test modules the rewrite touched plus `guide-check`/`lifecycle-check` as a
fail-fast; the two sweeps over every tracked file outside `.endless/`, each
guarded against a vacuous pass; `endless-go --help` built from this tree; every
site still naming `project status` or `project monitor`, so the phrase was
replaced and not merely deleted; and the five excluded records still carrying
the phrase, which is the evidence that the sweep was scoped rather than global.

## Second pass: the short form is the same coinage (2026-09-15)

The section above describes a first pass that removed the two-word phrase and
explicitly LEFT bare "board" — the filename, the guide's `--limit` paragraph,
the Click help for `project monitor --tmux` — on the reasoning that it "is not
the coined term, and a rename of it is a different change."

Mike, on reading that:

> 'board' is no better than 'attention board'. 'monitor' and 'project monitor'
> are the proper terms.

He is right, and the reasoning above was backwards in two ways.

1. **"Board" is not an ordinary noun that happened to sit beside a coinage. It
   is the coinage minus its adjective.** Every cost this task lists applies to
   it unchanged: it names nothing a reader can point at, it collapses `project
   status` and `project monitor` into one word, and it is in output. Dropping
   an adjective does not turn a coined term into English.

2. **The literal phrase is not the scope.** A terminology task is about the
   TERM, not about the string the filer happened to type. This task's own rule
   — "replace each use with whichever command the sentence means" — was already
   general; narrowing it to a grep pattern was the mistake.

The tell was there and was logged instead of acted on: deleting the phrase kept
leaving "the board" pointing at nothing, and six identical repairs got recorded
as a footnote rather than read as the sweep announcing its own scope.

### What the second pass covered

223 bare uses, thirteen Go identifiers and one filename, across ten Go
packages, three Python modules and two guide pages.

Each use is now whichever command the sentence means:

- **`project monitor`, or "the monitor"** where the sentence is about the live
  loop, its pane, or its tmux session — all of `window.go`, `sessionname.go`,
  the `--tmux` help, the `project-window` error message.
- **`project status`** where it is about the snapshot, the ranking rules, or
  the query behind them.
- **A concrete noun the codebase already uses** where the sentence means the
  thing rather than the command: a *frame* (`liveview.Frame` is a real type and
  "one complete frame" was already the phrasing), a *view*, a *row*. These are
  existing vocabulary, not substitute coinages.

Renames, so a reader meets a name that says what it is:

| Old | New |
|---|---|
| `internal/projectstatuscmd/board.go` (+ `board_test.go`) | `render.go` (+ `render_test.go`) |
| `splitBoardArgs` | `splitMonitorArgs` |
| `boardPctOfWindow` | `monitorPctOfWindow` |
| `boardTaskStatuses` | `projectStatusTaskStatuses` |
| `seedBoardTask` / `seedBoardSession` | `seedProjectStatusTask` / `seedProjectStatusSession` |
| `bindBoardPane` | `bindSessionPane` |
| `jsonBoard` / `decodeBoard` | `jsonDoc` / `decodeDoc` |
| `boardRows` | `nonEmptyRows` |
| `TestEmptyBoard`, `TestBoardBadge_*`, `TestJSONEmptyBoardStillParses`, `TestSplitBoardArgsInsertsAboveTheShell` | renamed to match |

User-facing surfaces reached: `project monitor --tmux`'s option help, `project
monitor`'s summary line ("Live board" → "Live monitor"), the `project-window`
error "already holds another project's board", two flag descriptions on
`endless-go project-status`, and `internal/config/README.md` — whose worked
example offered `{{project}}-board` in the one place a reader is about to type
a value of their own.

### What is deliberately NOT swept

**"dashboard".** It is a dictionary word for a live status panel, which is
precisely what "board" standing alone is not, and its uses are almost entirely
in dated design, brief and research documents — records of what was said at the
time, the same class as the plan mirrors. Flagged rather than swept; if it
should go too, that is a separate pass.

The one dated-document edit made here is
`docs/research-2026-06-12-claude-background-agents.md`, where "map its fields to
your task board" became "your task tree" — Endless's actual term. That corrects
a name, not a finding.

### Verification as built

`.endless/tasks/e-2153/verify.sh`, 29 checks. Two additions carry the second
pass: a `SHORT` pattern covering every shape the word took (prose, plural,
camelCase, SHOUTED, filename, config value), and a self-test that pins its
boundaries against literal strings before any sweep is trusted to report an
absence. That self-test earned itself on its first run: under `grep -i` the
camelCase class `[a-z0-9_]Board` folds to `[a-z0-9_]board` and matches the
"hboard" inside "dashboard" — a boundary silently destroyed by the flag meant
to make the check more thorough. The sweeps now run case-sensitive and carry
their casing in the pattern.
