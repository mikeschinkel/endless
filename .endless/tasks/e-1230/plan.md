# E-1230 — Add `task links` alias, drop `task relations` alias

## Goal

Pair the read-side click command with `task link` (writer) so an agent that just typed `task link` to create a relation can find `task links` to read them back. Drop the old `task relations` name to avoid `--help` clutter (three names for one function). Keep `task deps` because agents reflexively reach for it.

## Decisions already made

1. **Three click commands → two.** `task deps` stays, `task relations` is removed, `task links` is added. No deprecation alias kept — backward compat shims pollute `--help` the same way the removed name did.
2. **Output headers stay `Relations for E-NNN` / `# Relations for E-NNN`.** "Relations" is the concept noun even when the command verb is `links`. The inner `Links:` label introduced by E-1477 is unaffected.
3. **Internal `show_relations` function is not renamed.** Internal name, not user-facing. Future cleanup if and when it gains a reason; not part of this task.

## Files

| File | Change |
| --- | --- |
| `src/endless/cli.py` | Rename `@task_cmd.command("relations")` → `"links"`, function `task_relations` → `task_links`. Update docstring. Update `task_deps`'s docstring "Alias of `task relations`" → "Alias of `task links`". |
| `tests/test_relations.py` | Update three E-1477 tests (`test_task_relations_renders_links_section`, `test_task_relations_llm_links_line`, `test_task_relations_none`) to invoke `task links` instead of `task relations`. Test function names can stay (they describe the feature being tested, not the command surface). |

## Out of scope

- Output formatting (settled by E-1477).
- Surfacing parent in the links output (E-1231).
- Renaming `show_relations`, `get_all_relations`, `RELATION_LABELS`, or any other internal identifier.
- Any docs in `docs/private/` (historical snapshots — should not be rewritten).

## Verification

```sh
just install
endless task links E-1230            # works, shows the Relates to entry to E-909
endless task deps E-1230             # same output
endless task relations E-1230        # errors: 'No such command "relations"'
endless task --help | grep -E 'links|deps|relations'
                                     # links + deps present, relations absent
just test                            # all relations tests pass
```

## Outcome (filled by implementer)

_To be written by the spawned session before flipping status to `verify`. Cover: final commit ref, divergences from this plan, follow-up tasks filed._
