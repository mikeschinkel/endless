# Plan

Adding a relation type means the type is usable **everywhere relation types are
used**. The first landing (0c815ed) added `duplicates` / `duplicated_by` to the
vocabulary tables, `task link` / `task unlink`, the `task show` links block, and
the guide. It stopped there because `replaces` — the nearest analogue — stops
there too. That was the wrong yardstick: `replaces` missing a `task add` flag is
a gap in `replaces`, not a specification. This extension makes both types
first-class.

## 1. Relation flags on `task add`, `task update`, `epic add`

Add `--duplicates <id>` and `--replaces <id>` (repeatable, `TASK_ID`) alongside
the existing `--blocks` / `--blocked-by` / `--relates-to` / `--implements` /
`--cleans-up` / `--cleaned-up-by` block.

- `task update` has **no** relation flags today; these are the first. It takes
  `nargs=-1` item ids, so the relation is recorded from each named task.
- Forward direction only. `--duplicated-by` / `--replaced-by` add no capability
  (`task link` already accepts every inverse display name, and either spelling
  resolves to the same stored row), and at file time they are nonsense: a task
  you are creating cannot already be the one superseded.
- These record the **relation only**. `task replace <old> --by <new>` stays the
  status-bearing surface, and the help text says so — otherwise
  `task update E-9 --replaces E-5` silently leaves E-5 open.

## 2. The inline note, matching E-1956's treatment of `replaces`

E-1956's rule: a **terminal** status reads as the end of the story, so where the
story continues elsewhere that fact rides along with the status instead of living
only in `task show`'s links block. A task closed as `obsolete` because it
duplicated another has exactly that problem.

Render `duplicates` the same way, on every surface that already carries
`replaced_by`:

| Surface | Mode | Form |
|---|---|---|
| `task list` | table / tree | `obsolete (duplicates E-1086)` |
| `task list` | `--llm` | `duplicates=E-1086` on the status line |
| `task list` | `--json` | `"duplicates": ["E-1086"]`, ungated |
| `task show` | human | dim note beside `Status:` |
| `task show` | `--llm` / `--json` | as above |
| `session status` | table | appended to the row |
| `session status` | `--json` | `"duplicates": [...]`, ungated |

One token — `duplicates` — in all three renderings, as `replaced_by` is. Reads as
a verb phrase with the row as subject: "E-986 … obsolete (duplicates E-1086)".

**The note sits on the opposite endpoint from `replaces`, deliberately.** Both
annotate the task that is *closed*. For `replaces` that is the relation's
**target** (`new replaces old`); for `duplicates` it is the **source** (`dupe
duplicates keeper`). The two queries are therefore mirror images, and the comments
must say so or it reads as a bug.

Same gates as E-1956, unchanged: human and `--llm` draw it only alongside a
terminal status; `--json` emits it ungated, always present, because JSON is data
and an absent key must never have to be read as "not a duplicate". A row can
carry both notes; they compose rather than one winning.

Go side: `internal/monitor/session_status.go` grows a `duplicatesExpr` beside
`replacedByExpr` and a `Duplicates []int64` row field, rendered in
`internal/sessionstatuscmd`.

## 3. Verification

Extend `tests/tasks/e-1185-verify.sh` and `tests/test_relations.py`; add Go
coverage beside the existing `replaced_by` session-status tests. Project-wide
regression: `just test`, `go build/vet/test ./...`.
