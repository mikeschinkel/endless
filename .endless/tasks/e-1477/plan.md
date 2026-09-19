# E-1477 — Render task relations as a single "Links:" section

## Context

Task relations today render as a stack of per-type headers — `Blocks:`, `Blocked by:`,
`Relates to:`, `Cleans up:`, `Replaced by:`, etc. — one heading per relation type, in
three CLI surfaces. That's verbose and forces the reader to scan multiple headings to
learn "what is this task linked to." E-1477 replaces all of it with a single **`Links:`**
section where each entry is the related task id followed by its directional relation type
in parens. The grouped-by-type view is removed entirely; the single Links section is the
only relation format.

The data layer is untouched — `get_all_relations()`, `CANONICAL_DEP_TYPES`,
`RELATION_LABELS`, and `STORED_DEP_TYPES` all stay as-is. This is purely a rendering
change. Directionality is already resolved by `get_all_relations()` (it picks the
display-name based on whether the subject task is the source or target of each row), and
`RELATION_LABELS[name]` already gives the directional human label — we just lowercase it
for the parenthetical.

## Decisions (resolved with user)

- **Three CLI surfaces, two functions, one file** (`src/endless/task_cmd.py`):
  - `detail_item()` serves both `task show` and `task detail` (`detail` is an alias of
    `show`, cli.py:882). Also serves `decision show` (cli.py:1466) — decisions get the
    same Links rendering, no special-casing.
  - `show_relations()` serves both `task deps` and `task relations` (cli.py:1366, 1376).
  - The web UI (`internal/web/*.templ`) only renders `BlockedBy` and is **out of scope** —
    not named in the task. No Go changes.
- **Human view (both `task show`/`detail` AND `task relations`/`deps`): multi-line, rich
  rows, rendered identically.** A cyan `Links:` heading, then one indented line per
  relation — `E-NNNN (relation type) [status] title` — id-ascending, with the status
  colored (green if terminal, else yellow):

  ```
  Links:
    E-1429 (relates to) [confirmed] Some title
    E-1447 (blocks) [ready] Another title
  ```

  Factored into **one shared helper** so the two surfaces can't drift.
- **`--llm` outputs switch to a single compact Links line** (machine output favors
  compactness; user-approved):
  - `task show --llm`: `links=E-1429 (relates to),E-1447 (blocks)` (key=value, comma, no
    space — matches the surrounding `key=value` block).
  - `task relations --llm`: keep the `# Relations for E-NNN` header, then
    `Links: E-1429 (relates to), E-1447 (blocks)`.
- **Parenthetical label = `RELATION_LABELS[name].lower()`** → "relates to", "blocked by",
  "cleans up", "replaced by", matching the spec's example. Leaves `RELATION_LABELS`
  capitalized so `test_cleans_up_in_canonical_registries` stays green.
- **Ordering: id-ascending** across all relations (matches the spec example, where
  `E-1429 (relates to)` precedes `E-1447 (blocks)` despite `relates_to` sorting last in
  `RELATION_DISPLAY_ORDER`).
- **Empty relations:** `detail_item` prints nothing (as today); `show_relations` prints
  `(none)` (as today).

## Files to modify

- `src/endless/task_cmd.py` — the only source file.
- `tests/test_relations.py` — add rendering tests.

## Implementation

### 1. Module-level helpers (near `get_all_relations`)

```python
# Statuses that count as "done" for relation-row coloring (E-1477).
_RELATION_TERMINAL_STATUSES = ("confirmed", "assumed", "completed", "declined", "obsolete")


def _flatten_relations(item_id: int) -> list[dict]:
    """Flatten get_all_relations into one id-ascending list, each entry carrying the
    directional, lower-cased relation label, for the unified 'Links:' rendering (E-1477)."""
    flat: list[dict] = []
    for display_name, items in get_all_relations(item_id).items():
        rel = RELATION_LABELS.get(display_name, display_name).lower()
        for d in items:
            flat.append({"id": d["id"], "rel": rel,
                         "status": d["status"], "title": d["title"]})
    flat.sort(key=lambda r: r["id"])
    return flat


def _echo_links_section(item_id: int) -> bool:
    """Emit the unified multi-line 'Links:' section (E-1477): a cyan 'Links:' heading,
    then one indented, colored row per relation (id-ascending) —
    'E-NNN (relation type) [status] title'. Emits nothing and returns False when the
    task has no relations; returns True otherwise. Shared by task show/detail and
    relations/deps so both render identically."""
    links = _flatten_relations(item_id)
    if not links:
        return False
    click.echo(click.style("Links:", fg="cyan"))
    for r in links:
        color = "green" if r["status"] in _RELATION_TERMINAL_STATUSES else "yellow"
        click.echo(
            f"  {task_id_display(r['id'])} ({r['rel']}) "
            f"[{click.style(r['status'], fg=color)}] {r['title']}")
    return True
```

### 2. `detail_item()` — human view (currently lines 2799-2803)

Replace the per-display-name loop with the shared helper. It slots in where the old
relation lines were (between `Parent:` and `Created:`):

```python
_echo_links_section(item_id)
```

Also reduce `col_w` from 13 → 11 and fix its comment (line 2779): the per-type labels
that justified width 13 are gone; the longest remaining padded label is now `Confirmed:`
(10 chars + 1 space). (The `Links:` heading is no longer in the padded label column.)

### 3. `detail_item()` — `--llm` view (currently lines 2753-2756)

```python
links = _flatten_relations(item_id)
if links:
    link_str = ",".join(f"E-{r['id']} ({r['rel']})" for r in links)
    click.echo(f"links={link_str}")
```

### 4. `show_relations()` — human view (currently lines 3693-3715)

Replace the local `terminal` tuple, the per-display-name grouped loop, and the empty
branch with the shared helper:

```python
click.echo()
click.echo(click.style(f"Relations for {task_id_display(item_id)}", fg="green", bold=True))
click.echo(click.style("─" * 30, dim=True))
if not _echo_links_section(item_id):
    click.echo("  (none)")
click.echo()
```

### 5. `show_relations()` — `--llm` view (currently lines 3681-3691)

```python
if llm:
    click.echo(f"# Relations for E-{item_id}")
    links = _flatten_relations(item_id)
    if not links:
        click.echo("(none)")
        return
    link_str = ", ".join(f"E-{r['id']} ({r['rel']})" for r in links)
    click.echo(f"Links: {link_str}")
    return
```

## Tests (`tests/test_relations.py`)

Existing tests are unaffected (`get_all_relations` and `RELATION_LABELS` unchanged). Add:

- **`_flatten_relations`**: seed `a blocks b`, `c blocks a` (→ `a blocked_by c`),
  `a relates_to b`; assert the returned list is id-ascending and labels are lowercased
  directional (`"blocked by"`, `"blocks"`, `"relates to"`).
- **`task show` human** (CliRunner `task show <a>`): output has a `Links:` heading, rows
  `E-b (blocks) [...]` and `E-c (blocked by) [...]` carrying status + title, and no old
  per-type headings (`"Blocks:"`, `"Blocked by:"`).
- **`task show --llm`**: output has a `links=` line, no `blocked_by=` lines.
- **`task relations` human** (CliRunner `task relations <a>`): one `Links:` heading,
  rich rows; no per-type headings.
- **`task relations --llm`**: contains a `Links: ` line with the parenthetical labels.

Use the existing `_add_task` / `_seed_project` / CliRunner-on-`endless.cli:main`
patterns already in the file.

## Verification

1. `just test` (or focused: `uv run pytest tests/test_relations.py -q`) — all green.
2. Live read against a task that has relations, using `--db main` (read-only): find an id
   with multiple links via `endless task relations <id> --db main`, then eyeball:
   - `endless task show <id> --db main` → `Links:` section, multi-line rich rows.
   - `endless task relations <id> --db main` → identical `Links:` section.
   - both with `--llm` → the compact machine Links formats above.
   If no task with varied relations exists, seed one in `--db sandbox`
   (`endless task add ... --db sandbox`, then `endless task link ... --db sandbox`).
