# E-1592 — `endless setup shell-helpers` subcommand

## Context

`endless setup` (`src/endless/setup.py`, wired in `cli.py` ~2049) installs three
integrations as install/remove subcommand pairs — `prompt-hook`, `claude-hook`,
`channel-plugin`. **None install the `esu`/`esp`/`esf` session shell helpers.**
Users install those by hand, and the two docs that explain how disagree: the
`shell-init` command docstring (`cli.py:677`) says static append
(`endless shell-init >> ~/.zshrc`), while the agent guide
(`docs/guide/orchestration.md:126`, `index.md:29`) says `eval "$(endless shell-init)"`.

Goal: a first-class installer, consistent with the existing setup pattern, that
adds the helpers and keeps them current.

## Decisions (made with Mike)

1. **New subcommand pair `setup shell-helpers` / `setup remove-shell-helpers`** —
   parallel to the existing three integrations. NOT folded into `prompt-hook`
   (that command is activity-monitoring; session navigation is a separate
   concern). Two commands for a full setup is acceptable.
2. **Install the `eval` line, not the static snippet.** `endless shell-init` is
   pure static stdout (~30–50ms, no DB I/O), so regenerating it once per shell
   launch is negligible — and it means snippet fixes (e.g. the E-1591 `--db main`
   fix) reach users on next shell with no rc surgery. This removes the
   "find the marker block and replace it manually" friction the docstring laments.
3. **Fail loudly — no PATH guard.** Write a bare `eval "$(endless shell-init)"`
   (NOT `command -v endless && ...`). If `endless` isn't on PATH, the user should
   see the shell-startup error, not silently lose the helpers. Mike's call.

## Change

**`src/endless/setup.py`**
- Add `install_shell_helpers()` / `remove_shell_helpers()`, modeled on
  `setup_prompt_hook` / `remove_prompt_hook` (same `~/.zshrc` target prompt,
  same idempotency check via a marker, same confirm + manual-fallback UX).
- Block to append (marker comment lets remove find it):
  ```sh
  # Endless: session shell helpers (esu/esp/esf)
  eval "$(endless shell-init)"
  ```
- Idempotency: detect an existing install by the marker comment or the
  `eval "$(endless shell-init)"` line.

**`src/endless/cli.py`**
- Register `@setup.command("shell-helpers")` and
  `@setup.command("remove-shell-helpers")`, mirroring the existing pairs.
- Update the `shell_init` docstring (`cli.py:677`) to stop recommending the
  stale static `>>` form and point at `endless setup shell-helpers` (and the
  guarded-free `eval` line for manual installs), reconciling it with the guide.

**Tests** — add a `tests/test_setup_shell_helpers.py` (or extend existing setup
coverage if added later): asserts the block is appended, idempotency (no double
append), and removal. Follow `test_shell_init.py` style.

## Out of scope
- bash support beyond `~/.zshrc` (existing setup is zsh-only; keep parity).
- A combined top-level `endless setup all` runner.

## Verification
1. `endless setup shell-helpers` against a scratch rcfile → appends the marker
   block once; re-running reports already-installed (no duplicate).
2. `endless setup remove-shell-helpers` → removes only the marker block.
3. New shell sourcing the rcfile has `esu`/`esp`/`esf` defined
   (`declare -f esu`); with `endless` removed from PATH the eval errors loudly.
4. `just test` green.
