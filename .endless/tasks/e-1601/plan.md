# E-1601 — Gate large fields in `task show` behind display flags

## Goal

Make `endless task show <id>` a true **snapshot** by default: it never dumps a
large field's full content unless explicitly asked, and when a large field is
hidden it prints a one-line placeholder advertising the flag that reveals it
(e.g. `Outcome: 1275 chars (--outcome to display)` — the number is computed via
`len()`; 1275 is illustrative).

Today the **outcome** field auto-displays in full whenever a task's status is
`declined` or `completed` (`task_cmd.py:3467`), regardless of flags. For a
research/epic task whose deliverable *is* the outcome (e.g. E-1600, ~38KB), this
floods the snapshot and — via `--llm` — burns a large token budget on an
inspection the guide tells agents to run routinely.

## Decisions (resolved with Mike, 2026-06-20)

1. **Suppress uniformly.** Drop the status-based auto-display entirely. Outcome
   shows in full only with `--outcome` (or `--all-fields`). This includes
   `declined`: the decline reason becomes a placeholder too. One predictable
   rule; the reason is one keystroke away.
2. **All three large fields get the treatment.** `text`, `analysis`, and
   `outcome` each render a char-count placeholder when hidden. `description`
   is unchanged (shown by default, `--no-description` hides it with no
   placeholder).
3. **Consistent across all three output modes** — human, `--llm`, `--json`.

No new flags are needed: `--outcome`/`--text`/`--analysis`/`--all-fields`
already exist and already drive `show_outcome`/`show_text`/`show_analysis`.

## Implementation

All changes are in `src/endless/task_cmd.py::detail_item` plus one help-string
edit in `src/endless/cli.py`. No schema, no Go, no migration.

### 1. Human-readable path (`task_cmd.py:3450-3470`)

Replace the four hand-rolled large-text blocks with calls to a small helper so
the full-vs-placeholder logic lives in one place:

```python
def _echo_large_field(title: str, content: str | None, show: bool, flag: str):
    """Render a large text field for `task show`. Full content (titled
    section) when `show`; otherwise a one-line char-count placeholder naming
    the flag that reveals it. No output when the field is empty."""
    if not content:
        return
    click.echo()
    if show:
        click.echo(click.style(f"— {title} —", fg="cyan"))
        click.echo(content)
    else:
        click.echo(
            click.style(f"{title}:", fg="cyan")
            + " "
            + click.style(f"{len(content)} chars", fg="white", bold=True)
            + click.style(f" ({flag} to display)", dim=True)
        )
```

Call order is unchanged (Analysis, then Text, then Outcome — preserving the
E-999 / E-1577 ordering):

```python
# Description keeps its existing default-on behaviour (no placeholder).
if show_description and item["description"] and item["description"] != item["title"]:
    click.echo()
    click.echo(click.style("— Description —", fg="cyan"))
    click.echo(item["description"])

_echo_large_field("Analysis", item["analysis"], show_analysis, "--analysis")
_echo_large_field("Text",     item["text"],     show_text,     "--text")
_echo_large_field("Outcome",  item["outcome"],  show_outcome,  "--outcome")
```

The status check (`item["status"] in ("declined", "completed")`) is **removed**.

### 2. `--llm` path (`task_cmd.py:3380-3417`)

- **Remove** the unconditional top-of-block dump:
  `if item["outcome"]: click.echo(f"outcome={item['outcome']}")` (3386-3387).
- After the `landed=` line, emit a compact char marker for each large field
  that is present but hidden:

  ```python
  for name, content, show in (
      ("analysis", item["analysis"], show_analysis),
      ("text",     item["text"],     show_text),
      ("outcome",  item["outcome"],  show_outcome),
  ):
      if content and not show:
          click.echo(f"{name}_chars={len(content)}")
  ```

- Keep the existing `## Description/Analysis/Text` full sections (gated by their
  show flags) and **add** a matching `## Outcome` section:

  ```python
  if show_outcome and item["outcome"]:
      click.echo(f"\n## Outcome\n{item['outcome']}")
  ```

Net effect: `task show --llm` on E-1600 emits `outcome_chars=NNNNN` instead of
the full deliverable; `task show --llm --outcome` emits the full `## Outcome`.

### 3. `--json` path (`task_cmd.py:3336-3378`)

Gate `outcome` like `text`/`analysis` already are, and add always-present
char counts so the JSON shape stays stable and machine-readable:

```python
"outcome": item["outcome"] if show_outcome else None,
"outcome_chars": len(item["outcome"]) if item["outcome"] else None,
"text_chars": len(item["text"]) if item["text"] else None,
"analysis_chars": len(item["analysis"]) if item["analysis"] else None,
```

(`text` / `analysis` content keys keep their existing show-flag gating.)

### 4. Help text (`cli.py:1038-1039`)

Change the `--outcome` help from
`"Show outcome field (always shown for declined tasks)"`
to
`"Show the full outcome field (hidden by default; a char-count placeholder shows otherwise)"`.

## Existing tests to UPDATE (they encode the old behavior)

In `tests/test_outcome.py`:

- `test_task_show_declined_always_shows_outcome` (157) — rename to
  `test_task_show_declined_hides_outcome_by_default` and assert the placeholder:
  output contains `Outcome:` and `(--outcome to display)`, and does **not**
  contain `— Outcome —` nor the full reason text. Add a sibling that passes
  `--outcome` and asserts the full `— Outcome —` section + reason text appear.
- `test_task_show_llm_includes_outcome` (186) — without a flag, assert
  `outcome_chars=` appears and `outcome=llm-mode reason` does **not**; add a
  `--outcome` case asserting `## Outcome` + the reason.
- `test_task_show_json_includes_outcome` (195) — without a flag, assert
  `parsed["outcome"] is None` and `parsed["outcome_chars"] == len(reason)`; add
  a `--outcome` case asserting `parsed["outcome"] == reason`.
- `test_task_show_outcome_flag_renders_outcome` (144) and
  `test_task_show_outcome_section_renders_after_text` (169) — these pass
  `--outcome`/`--text --outcome` and should still pass unchanged; re-run to
  confirm.

## New tests to ADD (`tests/test_outcome.py`)

- Placeholder char count is accurate: set a known-length outcome, no flag,
  assert `Outcome: <len> chars (--outcome to display)` in output.
- `--all-fields` reveals the full outcome section (no placeholder).
- `completed` task without `--outcome` shows the placeholder (regression guard
  for the E-1600 scenario that motivated this).
- `text` and `analysis` placeholders render with correct flag names when those
  fields are set and their flags are absent.

## Verify script — `tests/tasks/e-1601-verify.sh` (deliverable)

Write an ad-hoc per-task verify script following the **same shape** as the
existing `tests/tasks/e-1599-verify.sh` — which already exercises the adjacent
`task show` field-rendering surface, so reuse its scaffolding verbatim:

- `#!/usr/bin/env bash`, `set -u`, TTY-aware color helpers.
- `section` / `report_pass` / `report_fail` / `summary` reporters; exit `0`
  all-passed, `1` any failure, `2` environment error (not a git worktree / no
  `uv`).
- `endless() { uv run endless "$@" --db sandbox; }` so every behavioral check
  runs against the worktree sandbox DB, never the real ledger.
- `add_task_get_id` helper; `assert_contains` / `assert_not_contains` /
  `assert_ordering` assertions. Add a tiny `repeat_a N` helper to build a
  known-length body so char-count assertions are exact.

This is an **ad-hoc per-task script**, not a deliverable of the in-flight
verification-suite epic (E-1596) — it mirrors `e-1599-verify.sh`'s reference
shape, exactly as that script frames itself.

### Checks the script must cover

1. **Default snapshot hides outcome + shows placeholder.** Task with a
   known-length outcome → `task show` (no flags) `contains` `Outcome:`,
   the exact `N chars` count, and `(--outcome to display)`; `not_contains`
   `— Outcome —` nor the outcome body.
2. **`--outcome` reveals the full section.** `contains` `— Outcome —` + body;
   `not_contains` `(--outcome to display)`.
3. **Uniform suppression for declined.** Declined task → default `task show`
   shows the placeholder, `not_contains` `— Outcome —` nor the reason; with
   `--outcome` the reason returns. (Guards the dropped status-based auto-display.)
4. **`text` / `analysis` placeholders.** Set both → default show `contains`
   `Text: <len> chars (--text to display)` and
   `Analysis: <len> chars (--analysis to display)`, bodies absent.
5. **Char-count accuracy.** Outcome of exactly L chars (via `repeat_a L`) →
   output `contains` `Outcome: L chars`.
6. **`--all-fields` reveals everything.** `contains` `— Outcome —`, `— Text —`,
   `— Analysis —`; `not_contains` any `(--… to display)` placeholder.
7. **`--llm` gating.** Default `--llm` `contains` `outcome_chars=L` and
   `not_contains` the full outcome body / `outcome=`; `--llm --outcome`
   `contains` `## Outcome` + body.
8. **`--json` gating.** Default `--json` `contains` `"outcome": null` and
   `"outcome_chars": L`; `--json --outcome` `contains` `"outcome": "<body>"`.
9. **Pytest regression suite passes.** Final `section` runs `just test` (capture
   output) and `report_pass`/`report_fail` on its exit code, so a single
   invocation of the script is the whole gate — the updated/added
   `test_outcome.py` cases plus the black-box checks above, all in one
   ALL PASSED / failure summary.

## Verification

`./tests/tasks/e-1601-verify.sh` is the single gate. Open `esu` into the
worktree and run it; `ALL PASSED` (exit 0) means every check above — including
the pytest suite via check 9 — is green. Any failure prints the expected/got
detail per check for diagnosis.

## Out of scope

- `description` placeholder behavior (stays default-on).
- Any Go `task show` path (the live command is the Python `detail_item`).
- Length-based auto-truncation / "show first N lines" — explicit flag gating is
  the agreed model.
