# Retire the cache-directory sandboxes

Named sandboxes — `endless-go sandbox run|enter|init <name>|list|prune|destroy`,
kept under `<cache>/sandboxes/<name>` — go, along with the worktree-reap hook
that still looks for a worktree's sandbox in that directory. The worktree
sandbox (`<worktree>/.endless/sandbox`, `init --mode worktree`, `reset`) and
`claude-settings-repair` stay.

## Go (internal/sandboxcmd, internal/monitor, cmd/endless-go)

- `dispatch.go`: drop the removed verbs from `Run` and `usageText`; `init`
  accepts only `--mode worktree`.
- Delete `run.go`, `enter.go`, `enter_inject.go`, `list.go`, `prune.go`,
  `destroy.go`, `reap.go`, and the named-sandbox parts of `sandbox.go`
  (`sandboxesDir`, `Provision`/`Load`, meta handling, `Env`) and `init.go`.
- For `reapguard.go`, `livewriters.go`, `orphan.go` and `supervisor.go`: delete
  each one whose only callers were removed; keep and note any with a live
  caller. Tests go with the code they test.
- `internal/monitor/reap_worktrees.go`: remove the `ReapSandbox` hook and its
  call; `cmd/endless-go/main.go`: remove its wiring and the removed verbs from
  the header comment.

## Python (src/endless)

- `cli.py`: remove the ENDLESS_SANDBOX refusal and `SANDBOX_SAFE_SUBCOMMANDS`.
- `db.py`: remove the ENDLESS_SANDBOX branch of the error diagnostic.
- Remove or rewrite the tests that exercise them (`tests/test_sandbox_gate.py`
  and the ENDLESS_SANDBOX cases in `tests/test_db_error_diagnostic.py`).
- Comments that name the old `sandbox init && sandbox bind` pair stay as
  history.

## Decision and docs

- Mark ED-1072 obsolete, naming E-2481.
- Remove any guide or CLI text naming a removed subcommand.

## Suite

`.endless/tasks/e-2481/verify.toml`: gotest for internal/sandboxcmd and
internal/monitor, pytest for the touched Python tests, and `sh` checks for the
greps in the items (if E-2419's `sh` checks have landed; otherwise a native
test per grep).
