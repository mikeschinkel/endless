# E-1628 — Fix worktree land mis-targeting the sandbox DB from a self-dev session

## Context
Discovered while landing E-1542 from a Claude session spawned inside a self-dev
worktree: `just land` failed at the `task.landed` step with a `task_landings`
FK error (787), and only succeeded once `XDG_CONFIG_HOME` was forced to the real
config dir. The landing is a real/main operation, but it was targeting the
per-worktree sandbox DB.

## Root cause (verified 2026-06-23)
The `just land` recipe runs three separate `endless` processes — `endless db
backup`, `endless db apply-change <f>` (per added change file), and `endless
worktree land <id>` (Justfile `land` recipe, ~lines 208-220). None pass `--db`,
so each inherits the invoking shell's `XDG_CONFIG_HOME`. A Claude session spawned
inside a self-dev worktree has that XDG pointed at the sandbox
(`~/.cache/endless/sandboxes/e-NNN`, injected by the worktree's
`.claude/settings.json`), so all three target the **sandbox** DB.

But landing is inherently a real/main operation: the landed task lives only in
the real DB, schema changes belong to the real DB, and the merge is into `main`.
Hard failure: the `task.landed` emit's `task_landings` insert violates the
`task_id` FK because the task isn't in the sandbox; schema-apply also silently
records its `_schema_version` marker in the sandbox. Confirmed fix signal: with
`XDG_CONFIG_HOME=$HOME/.config` the land succeeds end-to-end.

## Fix
Make the land-flow infrastructure commands default to the **real/main DB** when
no explicit `--db` was given — they are "always-main" operations, the Python
analogue of the Go-side `PinMainDB` for hook/channel/tmux (E-1450/E-1429).

At each command's entry, if `config.RESOLVED_CONFIG_DIR is None`, call
`config.apply_db_choice("main")` (it pins `config.main_config_dir()` via
`set_db_context`, so Python reads use the real `DB_PATH` **and**
`go_db_context_args()` then threads `--config-dir <real>` to every downstream
`endless-go` shellout). An explicit `--db main|sandbox` is honored — only force
when unset.

Command entry points to pin:
- `endless worktree land` (`src/endless/cli.py:2160` → `land_worktree` in `worktree_cmd.py`)
- `endless db apply-change`
- `endless db backup`

Putting the default in the commands (not the `just land` recipe) keeps both
`just land` and direct `endless worktree land` invocations correct, so the
behavior ships in the product, not the dev wrapper (per "just is dev-only" /
"ship behavior in product, not machine config").

**Alternative considered & rejected:** have the `just land` recipe pass `--db
main` to its three `endless` calls. Smaller, but leaves direct `endless worktree
land` / `endless db apply-change` invocations exposed and pushes load-bearing
behavior into the dev wrapper instead of the command.

## Out of scope / notes
- E-1474 already made the post-ff-merge `task.landed` emit re-runnable (symptom
  tolerance); this fixes the root cause so the emit doesn't fail to begin with.
- The session resolver returned a sandbox session id (`2`) rather than the
  real-DB id (`722`) from the main checkout. Once the land pins main, session
  resolution also runs against the real DB; confirm during implementation and
  file separately if it persists.

## Tests
- Python unit: invoke `land_worktree` (and the `db apply-change` / `db backup`
  entries) with `RESOLVED_CONFIG_DIR=None` and a sandbox-pointing XDG; assert
  each pins `main_config_dir()` (RESOLVED_CONFIG_DIR becomes the real dir;
  `go_db_context_args()` threads `--config-dir <real>`). Assert an explicit
  `--db sandbox` is NOT overridden.
- Per-task verify script `tests/tasks/e-1628-verify.sh`: from a sandbox-routed
  env (XDG=sandbox, no `--db`), land a throwaway real-DB task and assert the
  `task_landings` row is written to the **real** DB and the FK passes —
  reproducing E-1542's failure on baseline and confirming the fix.

## Critical files
- `src/endless/worktree_cmd.py` (`land_worktree` entry)
- `src/endless/cli.py` (`worktree_land`, `db apply-change`, `db backup` registrations)
- `src/endless/config.py` (reuse `apply_db_choice` / `main_config_dir` / `set_db_context`; no change expected)

## Verification
From a Claude session inside a self-dev worktree (XDG=sandbox), run `just land
E-NNN` with **no** XDG override: the landing records to the real DB, the
`task_landings` FK passes, and `_schema_version` markers land in the real DB.
