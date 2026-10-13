# Plan: keep Go tests' tmux servers out of the shared socket directory

1. Add one Go test helper that gives a test a private tmux directory. It:
   - sets `TMUX_TMPDIR` to a short per-test directory made with `os.MkdirTemp("", ...)`, because a unix socket path must stay under about 104 bytes, which `t.TempDir()` paths often exceed on macOS;
   - unsets `TMUX` and `TMUX_PANE`, so a `tmux` call can never reach the user's own server;
   - on cleanup, kills the scratch server and removes the directory.

   Put it where both test packages can import it without a production dependency, for example a small internal test-support package. Check for an existing one first.
2. Use the helper in `TestPlacement_LiveTmux` (`internal/spawnlaunchcmd/placement_test.go`) and in `privateTmux` (`internal/sessionmonitorcmd/tag_test.go`). Keep their `-L` names: they now resolve inside the private directory. Drop `tag_test.go`'s manual `os.Remove` of the socket file, since removing the directory covers it.
3. Guard against regressions: add a Go test that fails when any `_test.go` under `internal/` starts tmux with `-L` or `-S` without going through the helper. A source scan, in the style of `internal/refusal`'s `TestNoStderrOutsideThisPackage`, is enough.

## Acceptance

- `just test-go` leaves no new entry in the system tmux socket directory: compare `ls` of it before and after the run.
- `just test-go` is green, including the new guard.


## Grown scope (found while implementing)

- `internal/sessionquerycmd/ensure_claude_id_test.go`: `TestEnsureClaudeID_ReturnsExistingRow` and `TestEnsureClaudeID_LazyCreatesMissingRow` pass `--process`, which resolves through any reachable tmux server. They stamped a server-uuid option onto the caller's live server, and `_LazyCreatesMissingRow` failed when no server was reachable (a no-tmux shell, or under `endless task verify`'s empty private server). Added `tmuxtest.Server` (Private plus a scratch server on the private default socket) and used it in both.
