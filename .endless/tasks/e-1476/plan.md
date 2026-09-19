# E-1476 — Unify `--db` into one position-agnostic flag (rename `worktree`→`sandbox`); document in guide

## Context
E-1429 (landed) added a `--db main|worktree` gate: endless CLI commands run inside a self-dev worktree must explicitly choose the real ledger vs the per-worktree sandbox DB. The friction is **intentional** — forcing the choice prevents silently writing to the wrong DB. Two rough edges remain (hit by a spawned session on E-1436, 2026-05-25):

1. **Two `--db` flags** — the root group's `--db` AND `db path`'s own `--db`. They answer the same question ("which DB"), so there should be one.
2. **Position-sensitive** — `--db` is a Click *group* option, so it only parses *before* the subcommand (`endless --db main task show`); the natural `endless task show --db main` fails with "No such option '--db'". That's a cognitive tax (users must know which flags are global) and won't survive the planned Python→Go port (Go CLIs accept flags in any position).

**Outcome:** one `--db`, accepted in any argument position, value renamed `worktree`→`sandbox` (clearer: the throwaway test DB), choice still mandatory (no default, never an env var), documented in the guide.

## Approach
Mirror the Go side's "consume the flag from anywhere at entry" pattern (`monitor.ConsumeDBContextFlag`, internal/monitor/db.go:153) by overriding the Click group's `main()` to pre-extract `--db` from argv before normal parsing. `CliRunner.invoke` calls `cli.main(args=...)` (click/testing.py:501), so this single override makes `--db` position-agnostic for **both** the real process and the test suite — **no pyproject entry-point change, single extraction site**.

### 1. `src/endless/cli.py`
- Add `import sys` and a `DBAwareGroup(click.Group)` overriding `main(self, args=None, **extra)`:
  - `argv = list(args) if args is not None else sys.argv[1:]`
  - Scan for `--db <val>` and `--db=<val>` **anywhere**; build a cleaned list without them; capture the value (last-wins, matching Go's loop).
  - If captured: `try: config.apply_db_choice(val) except ValueError as e: raise click.UsageError(str(e))`. If `--db` is the last token with no value → `raise click.UsageError("--db requires a value: main or sandbox")`.
  - `return super().main(args=cleaned, **extra)`
- `@click.group()` → `@click.group(cls=DBAwareGroup)`. **Remove** the `--db` option + `db_choice` param from `main()` and its in-handler `apply_db_choice` block (the override owns it now — single source). Add an `epilog=` documenting `--db main|sandbox` so `endless --help` still surfaces it (the override consumes the flag, so a normal Click option would be vestigial).
- `db path` (cli.py:1912-1941): **remove** its `--db` option/param. New handler:
  - `from endless import config`
  - `if config.RESOLVED_CONFIG_DIR is None: raise click.ClickException("db path needs an explicit --db value: 'endless db path --db=main' or 'endless db path --db=sandbox'.")`
  - `click.echo(str(config.DB_PATH))`
  - Update docstring (drop `--db=worktree`; note it prints the path for the globally-selected `--db`).

### 2. `src/endless/config.py`
- `apply_db_choice` (232): `elif choice == "worktree"` → `"sandbox"`; reword its ValueError to `"--db sandbox only applies inside a self-dev worktree (.endless/worktrees/e-NNN); cwd is not in one"`; the `else: raise ValueError("unknown --db value: ...")` branch is now the real validator (drop `# pragma: no cover`); docstring → `--db main|sandbox`.
- `WORKTREE_DB_REFUSAL` (168-173) — rewrite body (constant name stays):
  ```
  running inside a self-dev worktree requires an explicit --db value (accepted in any position):

    --db main     the real ledger — managing the project
    --db sandbox  this worktree's throwaway test DB — testing endless itself

  Need paths? Run `endless db path --db=main|sandbox`.
  ```
- Fix `--db main|worktree` in the doc comments (~line 21, ~155-160) → `--db main|sandbox`.

### 3. `internal/monitor/db.go`
- `worktreeDBContextRefusal` (~223): user-facing `--db main|worktree` → `--db main|sandbox` (Go has no flag *value* to rename; this string is the only edit). `just build` to refresh the binaries.

### 4. `docs/guide/orchestration.md`
- Add `### Choosing the database (--db)` under `## Worktrees`, after `### Getting into the worktree` (~line 43): required only inside a self-dev worktree of a `worktree_sandbox` project; `main` = real ledger (`~/.config/endless/endless.db`), `sandbox` = this worktree's throwaway DB (`<cache>/endless/sandboxes/worktree-e-<id>/`); mandatory by design (no default, never an env var — an exported var would silently route every later command to the wrong DB); position-agnostic; `endless db path --db=main|sandbox` prints a path without opening the DB.

### 5. `tests/test_db_gate.py`
- Value rename: `apply_db_choice("worktree")`→`"sandbox"` (lines 58, 64); `--db=worktree`→`--db=sandbox` (127, 135); refusal-text asserts (81-82) → `--db main` / `--db sandbox`; tighten `test_db_path_requires_db_flag` to assert "needs an explicit --db".
- New tests (CliRunner now covers both positions via the override):
  - position-agnostic: `invoke(main, ["--db=main","db","path"])` and `invoke(main, ["db","path","--db=main"])` both print the main path (reset `config.RESOLVED_CONFIG_DIR=None` between).
  - `db path` with no `--db` → ClickException "needs an explicit --db".
  - `--db sandbox` outside a worktree → exit≠0, "self-dev worktree".
  - `--db worktree` (old value) → exit≠0, "unknown --db value" (proves no alias).

### Not changed
- `pyproject.toml` entry point (stays `endless.cli:main`).
- `db.py:get_db`, `event_bridge.py`, `session_cmd.py`, `cli.py:serve` — they use `require_db_context`/`go_db_context_args`, unchanged.
- Concept names kept: `sandbox_config_dir`, `worktree_task_id`, `gated_worktree_root`, `WORKTREE_DB_REFUSAL` (name), `worktree_sandbox` config key, `worktree-e-NNN` dir naming.

## Reused functions
`config.apply_db_choice` (single resolver, now `main|sandbox`), `config.set_db_context`, `config.main_config_dir`, `config.sandbox_config_dir`, `config.worktree_task_id`, `config.gated_worktree_root`, `config.require_db_context`, `config.go_db_context_args` — all unchanged except the value rename in `apply_db_choice`. Mirrors `monitor.ConsumeDBContextFlag` (Go).

## Critical files
- `src/endless/cli.py` (DBAwareGroup, group, `db path`)
- `src/endless/config.py` (`apply_db_choice`, `WORKTREE_DB_REFUSAL`, comments)
- `internal/monitor/db.go` (`worktreeDBContextRefusal` string)
- `docs/guide/orchestration.md` (new `--db` subsection)
- `tests/test_db_gate.py`

## Verification
Implement in a worktree (`endless task claim E-1476`); worktree-gated checks run from inside `.endless/worktrees/e-*` of this repo (a `worktree_sandbox` project). `just build` (picks up the Go string + binaries); Python is live via editable install.

From a non-worktree dir:
- `endless --db main db path` / `endless db path --db main` / `endless db path --db=main` → all print `~/.config/endless/endless.db` (position-agnostic).
- `endless db path` → fails "needs an explicit --db".
- `cd /tmp && endless db path --db sandbox` → fails "self-dev worktree".

From inside a self-dev worktree:
- `endless db path --db sandbox` → `<cache>/endless/sandboxes/worktree-e-<id>/endless/endless.db`.
- `endless db path --db worktree` → fails (unknown value; no alias).
- `endless task list` → refusal (sandbox wording, "any position").
- `endless --db main task list` AND `endless task list --db main` → both succeed (position-agnostic through the gate).
- `endless --help` → epilog shows `--db main|sandbox`.

Tests: `just test` (Python) + `go test ./internal/monitor/...` (gate tests; not in `just test-go`) + `just test-go`.
