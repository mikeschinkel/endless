# E-1782 — Document `endless task report` + report-by-default posture in `endless guide tasks`

## Context

E-1771 shipped `endless task report <id>` — it computes the facts (status,
follow-ups, children, worktree state) and prints a terse, just-the-facts steering
prompt the agent relays verbatim. E-1773 wired the report-by-default posture and
the `FULL STATUS` bypass into the spawn **handoff templates**. But
`endless guide tasks` — the standing reference every session reads — has **zero**
mention of the command or the posture. A non-spawned session, or any session
consulting the guide rather than a handoff, never learns the command exists. This
task closes that guide-coverage gap. Doc-only; no runtime behavior changes.

## Findings from exploration

- **`endless guide <section>` just cats the markdown** — `guide()` in
  `src/endless/cli.py:528` does `click.echo(target.read_text())` over
  `docs/guide/<section>.md`. So grepping the source `.md` is equivalent to
  grepping the rendered guide.
- **The cross-reference already resolves `task report` → `tasks`.** It inherits
  from `docs/guide/help/task.md` (`section: tasks`); `just guide-scaffold` shows
  `[ <- task] endless task report`, and `endless task report --help` already
  prepends the `endless guide tasks` agent directive. Per the regenerate-guide
  convention ("add a leaf file only when a subcommand belongs to a *different*
  section than its group"), **no new `task-report.md` map file is warranted** —
  `task report` lives in the same `tasks` section as its group. The only real gap
  is the narrative content in `tasks.md`.
- The `--json` anomaly-payload schema is documented canonically in
  `endless task report --help`. Per the task, do **not** duplicate it in the guide.

## Changes

### 1. `docs/guide/tasks.md` — add a `## Reporting to your user` section

Insert immediately after the `## Status transitions` block (ends ~line 178,
before `## Removing and moving`). Match the density of surrounding sections —
a few sentences plus one example invocation. Content covers exactly three points:

1. **The command exists and what it's for** — `endless task report <id>` produces
   an end-of-session / status report: computes status, follow-ups, children, and
   worktree state, and prints a steering prompt to relay **verbatim, plainly, no
   ceremony**. Status-agnostic; does **not** change the task's status.
2. **Report-by-default posture** — end-of-session and status reports route through
   this command and are relayed as-is, not composed as freeform prose. The normal
   path takes **no payload**.
3. **Anomaly escape hatch, by reference only** — `--json` / `--json-file` carry
   genuinely out-of-band notes (anomaly/discovery) or open questions for the user.
   **Point at `endless task report --help`** as the single canonical home for the
   exact payload shape — do NOT inline the schema.

Draft:

```markdown
## Reporting to your user

When you reach a terminal status — or your user asks where the work stands —
generate the end-of-session / status report rather than composing one by hand:

```bash
endless task report <id>
```

It computes the facts it can (status, follow-ups, children, worktree state) and
prints a steering prompt telling you to relay **only those facts, verbatim —
plainly, no ceremony**. It is status-agnostic (run it at whatever status you
reached) and **does not change the task's status**.

**Report by default.** End-of-session and status reports route through this
command and are relayed as-is; don't write them as freeform prose. The normal
path takes **no payload**.

For a genuinely out-of-band note (an anomaly or discovery the command can't
compute) or an open question for the user, pass `--json` / `--json-file`. The
exact payload shape lives in `endless task report --help` — the single canonical
home; read it there rather than duplicating it here.
```

### 2. `docs/guide/index.md` — name the command in the happy path (light touch)

Step 6 currently reads "Report completion to your user with the task ID." Append
a clause naming `endless task report <id>` so the command is discoverable from the
top-level index, not only the tasks section. One sentence; keep it natural.

### 3. Regenerate the guide cross-reference (validation step)

Run the `regenerate-guide` flow — `just guide-index` then `just guide-check`
(exit 0). Because `task report` already inherits to `tasks`, this is expected to
produce **no map diff**; it validates that the generated table and `--help`
directive stay in sync. Do not hand-edit the generated block.

## Verification

Self-contained per-task script `tests/tasks/e-1782-verify.sh` (modeled on the
`tests/tasks/e-1759-verify.sh` harness: git-resolved `REPO_ROOT`, pass/fail
tallies, `set -u`, no touch to real ledger/branch). Checks:

1. **Content** — `docs/guide/tasks.md` contains `endless task report`, the
   `## Reporting to your user` heading, the report-by-default wording (e.g.
   `verbatim`, `no payload`), and the pointer to `endless task report --help`.
2. **No schema duplication** — `tasks.md` does **not** inline the `--json`
   payload keys (e.g. `anomaly|discovery`), proving deferral to `--help`.
3. **Index pointer** — `docs/guide/index.md` happy path names
   `endless task report`.
4. **Cross-reference resolves** — `endless task report --help`, invoked against
   the worktree source (`uv run --directory "$REPO_ROOT" endless task report
   --help`), prints the `endless guide tasks` directive; and `just guide-check`
   exits 0 (map + generated index in sync).

Fold this script into the one-command verify handoff. Doc-only change — no
runtime surface to drive beyond the rendered guide.

## Out of scope

- Handoff-template wiring (done in E-1773).
- Any change to the `endless task report` command itself or its `--help` text.
- A dedicated `task report` row / leaf map file — deliberately omitted;
  inheritance already resolves it to `tasks` and the convention discourages a
  same-section leaf.
