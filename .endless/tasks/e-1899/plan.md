# E-1899 — Remove the unused list tree

A removal checklist, not a design. The only risk in this task is deleting the
wrong `--tree`: there are four flags by that name and two identically-named
glyph maps, and the two flags you must NOT touch are the ones actually in use.

> **Amended during implementation.** Three things landed that this plan did not
> anticipate; each is marked ⊕ below.
>
> 1. A **third bare `tree` reference** in `show_plan` — the sort default —
>    which the site table missed. Mechanical.
> 2. A **doc line** in `docs/guide/tasks.md` advertising the removed flag.
> 3. The **second `status_indicators` map was deleted too**, reversing this
>    plan's "Do NOT touch" directive. It turned out to be dead code. See
>    *Reversal* below; filed and closed as E-1924.

## One renderer, two flags

`epic_cmd.list_epics` is a thin wrapper that forwards `tree=tree` into
`task_cmd.show_plan` with `type_filter="epic"`. So `task list --tree` and
`epic list --tree` share a single implementation — there is ONE branch and ONE
glyph map to delete, not two.

## Remove

| Site | What |
|---|---|
| `cli.py:1225` | the `--tree/as_tree` option on `task list` |
| `cli.py:2521` | the `--tree/as_tree` option on `epic list` |
| `cli.py` (both bodies) | the `tree=as_tree` argument passed onward |
| `epic_cmd.list_epics` | the `tree` parameter and its pass-through to `show_plan` |
| `task_cmd.show_plan` | the `tree` parameter |
| `task_cmd.show_plan` | the `else: # Tree output` branch — the `by_id` / `children_of` / `_render` block, ending at the `_render(None, 1)` call |
| `task_cmd.py` ~:1100 | the `status_indicators` map that opens that branch |
| ⊕ `task_cmd.show_plan` | the sort default `if not tree and not sort_by:` → `if not sort_by:` |
| ⊕ `docs/guide/tasks.md:34` | the `endless task list --tree # hierarchical` line |
| ⊕ `task_cmd.next_tasks` ~:1209 | the second `status_indicators` map — see *Reversal* |

After removal, `show_plan`'s `if not tree: _render_flat_table(rows)` collapses
to an unconditional `_render_flat_table(rows)`; the trailing `click.echo()` and
the `N item(s)` summary below the branch stay.

⊕ The site table above missed a third bare `tree` reference: `show_plan` picked
its ORDER BY with `if not tree and not sort_by: sort_by = "id"`, falling through
to `pi.sort_order` for the tree. Removing the parameter leaves `if not sort_by`.
Flat ordering is unchanged — the flat path always defaulted to id. A `NameError`
on the first `task list` after the branch deletion is what surfaced it.

## ⊕ Reversal — the second glyph map went too

This plan told the implementer to preserve the `status_indicators` map in
`next_tasks` as an unrelated bystander. That was wrong: the map is **dead
code**. It is assigned and never read — `next_tasks` renders through
`_render_flat_table`, which has no glyph column. `grep -n 'indicator'
task_cmd.py` returned only the assignment line.

So the resolution is deletion, not preservation. Adding a glyph column to
`task next` to make the map live would be an unrequested behavior change;
`task next` output is byte-for-byte what it was. Filed as E-1924
(`cleans_up` E-1899), then folded into this task's commit and closed obsolete.

Consequence for the framing at the top of this plan: there were two
identically-named glyph maps and **both** are gone. The "don't delete the wrong
one" hazard is now only about the four `--tree` flags.

## Do NOT touch

- **`session status --tree`** (`cli.py:815`) and **`session monitor --tree`**
  (`cli.py:839`). These are the trees actually in use. They are Go-backed
  (`internal/sessionstatuscmd`), reach the renderer through
  `session_status_resolve`, and share nothing with the list tree but the flag
  name.

## Verification — `tests/tasks/e-1899-verify.sh`

Author the script; it is the deliverable the user runs. Hand back exactly one
command:

```
esu && ./tests/tasks/e-1899-verify.sh
```

Copy the harness shape from `tests/tasks/e-1845-verify.sh` (the `report_pass` /
`report_fail` / `assert_*` / `summary` block and the isolated-fixture setup:
throwaway git repo, temp `XDG_CONFIG_HOME`/`XDG_CACHE_HOME`, worktree `bin` on
PATH). E-1623 will retrofit these onto a shared harness later; until then match
the current shape rather than inventing a new one.

**A — fail-fast gate.** `uv run pytest tests/ -q` passes. A removal must not
break an unrelated suite, and there is no point running the rest if it did.
Abort the run on failure.

**B — the flags are gone.** `endless task list --tree` and
`endless epic list --tree` both fail as unknown options, and their `--help`
output no longer lists `--tree`.

**C — the flat table is untouched.** `endless task list` and
`endless epic list` still render, and `--status`, `--phase`, `--sort`, `--llm`,
and `--json` all still behave. Seed a small task tree with parents and children
so a regression in the flat renderer would actually show.

**D — the trees that matter still work.** `endless session status --tree` and
`endless session monitor --tree` still render. This is the check that catches
the one dangerous failure mode of this task: deleting the wrong `--tree`. Assert
against real output, not just an exit code.

**E — ⊕ both glyph maps are gone and `task next` is unchanged.** As written this
check asserted that `endless task next` "still renders its status glyphs" —
a false premise: it never rendered them, which is exactly how the map was found
to be dead. Reframed to the guarantee that actually holds: no `status_indicators`
survives in `task_cmd.py`, nothing reads an `indicator`, and `task next` still
renders its header, column heads, rows, `--json` and `--llm` exactly as before.

**F — no dangling references.** Grep the tree for `tree=` and `as_tree` in
`src/endless`; the only survivors should be the two `session_status_resolve`
call sites. Nothing should still pass `tree` to `show_plan` or `list_epics`.

**⊕ G — project-wide regression.** `go test ./...` passes. The Python suite is
already the section-A gate; this is its Go counterpart, run at the end.

## Not in scope

An interactive TUI tree — what is actually wanted instead of this — is a much
larger piece of work and is deliberately unfiled.

## Note for E-1891

The deleted glyph map is the per-row lookup cited in E-1891's plan as the one
place worth measuring for subprocess cost. Landing this first dissolves that
concern rather than answering it. ⊕ Doubly so: both maps are now gone, so
whichever of the two E-1891 meant, there is nothing left to measure.
