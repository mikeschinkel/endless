# Route bare CLI invocations to the worktree candidate build

## Why
From a bare (non-Claude) tmux pane inside a self-dev worktree, `endless` (Python) and bare
`endless-go` silently run MAIN's code, so worktree changes can't be exercised without
`PYTHONPATH=src` / `./bin/endless-go`. New worktree flags (e.g. `session next --tree`, E-1684)
error `No such option` until landed. This is the code-routing analog of the DB self-detection
already shipped by E-1368/E-1281/E-1513. Brainstormed in E-1687 (see its outcome for the
decisions and why).

## Approach — pre-parse self-re-exec, gated + loop-guarded, mirroring E-1368

**Fire predicate (reuse existing symbols, don't reinvent):**
- cwd matches the canonical worktree path segment — Python `config._WORKTREE_PATH_RE` in
  `src/endless/config.py` (the `/.endless/worktrees/(e-NNN)` regex); Go's equivalent in
  `cmd/endless-go/main.go`.
- AND `config.project_is_self_dev(project_root)` (`src/endless/config.py`) is true — so
  downstream projects (self_dev unset) never re-exec; only endless's own worktrees route. Same
  opt-in boundary as the sandbox.
- Worktree root = cwd truncated at the regex match; `src = <root>/src`,
  `gobin = <root>/bin/endless-go`.

**Python** — shim at the TOP of `AgentAwareGroup.main()` in `src/endless/cli.py` (the Click
`Group.main` override that already consumes `--db` before Click parses). Running the re-exec
there is *before* argument parsing, so the WORKTREE's Click parses the args and new flags work
(fixes the `No such option` trap). Mechanism: `os.execve(sys.executable, [sys.executable,
sys.argv[0], *argv], env)` with `PYTHONPATH=<worktree>/src` prepended and the loop-guard env
set. Keep `sys.executable` (the uv-tool venv python) so deps still resolve from main's venv;
only the `endless` package source flips to the worktree. Python's candidate is always present
(the checkout's `src/`) — no not-built case.

**Go** — shim at the top of `main()` in `cmd/endless-go/main.go`, alongside the existing E-1368
sandbox self-detection (binary already has cwd-awareness). If cwd is a self-dev worktree and
`os.Executable()` is not already `<worktree>/bin/endless-go`, `syscall.Exec` that binary. If the
candidate binary is absent, emit a loud stderr warning and continue as main (don't block) — the
copy is the worktree-create hook's job.

**Loop guard (both):** an env var, e.g. `_ENDLESS_CANDIDATE_REEXEC=1`, set before exec and
short-circuiting on entry. Symmetric across Python and Go.

**Stderr notice (both):** one dim line on re-exec, e.g. `endless: candidate build (e-NNN)`, so
it's never unclear which code ran. stderr keeps stdout clean for piping.

## Known limitation (do not solve here)
A stale `<worktree>/bin/endless-go` (Go edited but not `just build`) re-execs stale candidate.
The stderr notice shows *which* binary ran but not its freshness. Python has no analog (source
is live). Possible follow-up: mtime compare of Go src vs binary. Don't block on it.

## Verification
Deliver `tests/tasks/e-NNNN-verify.sh` per the `tests/tasks/e-1577-verify.sh` convention
(bash, `--db sandbox` routing, `section`/`report_pass`/`report_fail`/`summary` + `assert_*`
helpers, `ALL PASSED`/`FAILED`, exit 0/1, runnable from anywhere in the worktree). Cover:
- From a bare worktree shell, a worktree-only flag succeeds instead of `No such option`
  (`endless session next --tree`, E-1684) and the candidate stderr notice is printed.
- From the main checkout the same invocation runs MAIN (no re-exec, no notice).
- Bare `endless-go <cmd>` in the worktree reaches `<worktree>/bin/endless-go`; absent-binary
  path prints the loud warning and falls back.
- The re-exec does NOT fire in a downstream (non-self_dev) project worktree.
