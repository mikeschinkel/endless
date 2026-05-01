# E-1014 — `endless session use` with pluggable extension hook

## Background

`endless session cd` (E-990) prints a session's cwd or worktree path so a sibling shell can cd into it. That solves "look at the files," but not the use case it was built for: testing the work-in-progress in a sibling pane.

For endless-the-project, just being in the worktree directory is not enough. The shell still picks up the *installed* `endless` CLI from PATH, which points at main, not the worktree's bin. The user has to manually adjust PATH per-shell to actually exercise the worktree's binaries. Other projects have analogous needs — version managers, virtualenvs, project-specific environment variables.

What's needed is *activation*, not just navigation: a single command that prepares a shell to test a session's work, including whatever per-project setup that requires.

## Design

`endless session use [<session-ref>]` prints shell-evaluable activation. The user runs:

```
eval "$(endless session use)"
```

A helper function (filed separately as E-1015 / shell-init) wraps that boilerplate.

### Resolution

Same rules as `session cd`:

- Explicit numeric → match `endless_session_id`.
- Explicit UUID prefix → match `harness_session_id` startswith.
- No arg, in tmux → auto-resolve to the sole sibling Claude pane in the current tmux window.
- No arg, not in tmux → error; explicit id required.

### Default activation output

Always emitted, regardless of project:

```sh
cd '<worktree_path-or-cwd>'
export ENDLESS_SESSION_ID='247'
export ENDLESS_HARNESS_SESSION_ID='f41f263e-c708-...'
export ENDLESS_HARNESS='claude'
export ENDLESS_PROJECT_ROOT='<project-root>'
export ENDLESS_WORKTREE_PATH='<worktree-or-empty>'
```

`worktree_path` resolution mirrors `session cd`: prefer companion file's `worktree_path`, fall back to `cwd`. (When E-989's hook is upgraded to populate `worktree_path`, this falls into place.)

### Per-project extension hook

If `<project_root>/.endless/extensions/use.sh` exists, run it after the default block. Its stdout is appended to the activation output the user `eval`s.

**Contract for the script:**

- Endless invokes it with these env vars set:
  - `ENDLESS_SESSION_ID`
  - `ENDLESS_HARNESS_SESSION_ID`
  - `ENDLESS_HARNESS`
  - `ENDLESS_PROJECT_ROOT`
  - `ENDLESS_WORKTREE_PATH` (empty if none)
- Script's stdout: shell-evaluable lines (export, alias, source, etc.).
- Script's stderr: surfaced to the user as a warning, but does not fail activation.
- Script's exit code: non-zero is a warning, not a hard failure (degrade gracefully — partial activation is better than none).
- Timeout: hard 5s. Avoid runaway scripts blocking the user's shell prompt.

**Example for endless-the-project's own `extensions/use.sh`:**

```sh
#!/bin/sh
# Make the worktree's built CLI win on PATH and route 'endless' through uv
# inside the worktree, so the Python CLI matches the worktree's source.
echo "export PATH='$ENDLESS_WORKTREE_PATH/bin:$PATH'"
echo "alias endless='uv run --directory $ENDLESS_WORKTREE_PATH endless'"
```

**Example for a Node project:**

```sh
#!/bin/sh
if [ -f "$ENDLESS_WORKTREE_PATH/.nvmrc" ]; then
  echo ". \"$NVM_DIR/nvm.sh\""
  echo "nvm use \"$(cat $ENDLESS_WORKTREE_PATH/.nvmrc)\""
fi
echo "export NPM_CONFIG_PREFIX='$ENDLESS_WORKTREE_PATH/.node_modules/.bin'"
```

### Security

The extension script is *user-authored, in-repo* code. Same trust model as `Makefile`, `package.json` scripts, or `.envrc`. Mitigations endless applies:

- Refuse to run if the script is world-writable.
- Refuse to run if owned by a different user (defense against shared-checkout mishaps).
- Run with the user's own permissions; no privilege escalation.
- No hidden execution: only fires for `endless session use`, never SessionStart hooks or background commands.

These are sanity checks, not a sandbox. The user owns their repo.

## Verification

- `endless session use 247` (explicit) emits the default block plus extension stdout.
- No-arg in tmux with one sibling Claude pane: auto-resolves; activates that session.
- Project without `extensions/use.sh`: only default block emitted; no error.
- Project with a malformed `extensions/use.sh` (exit 1): default block still emitted; warning to stderr.
- Script exceeds 5s timeout: killed; partial activation; warning to stderr.
- Script owned by another user or world-writable: refused with explicit error message.

## Out of scope

- Extension format other than `use.sh` (e.g. JSON declarative). Re-evaluate if shell-script extensibility proves insufficient.
- Sandboxing (containers, seccomp, etc.). User-trust model is sufficient.
- `cd` deprecation — separate task (E-1016).
- Helper function for shellrc — separate task (E-1015).
