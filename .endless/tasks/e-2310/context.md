Endless already decided that its tmux servers do not share the system tmux socket directory: E-2240 gives them a fixed socket path under `/tmp/endless-<uid>/`, and E-2278 gives each verify run its own private `TMUX_TMPDIR`. The Python tests already do this through `tests/conftest.py`.

Two Go tests still start their scratch servers with `tmux -L <name>`, which resolves inside the shared system socket directory:
- `internal/spawnlaunchcmd/placement_test.go` (`TestPlacement_LiveTmux`)
- `internal/sessionmonitorcmd/tag_test.go`

`tag_test.go` removes its socket file on cleanup (E-1908). `TestPlacement_LiveTmux` does not: it left `endless-e2234-TestPlacement_LiveTmux` behind during a `just test-go` run on 2026-10-12.

Why it matters: liveness scans every socket in that directory on every monitor tick. Since E-2228 a dead socket costs a `connect()` rather than two tmux forks, but leftovers still accumulate there. On 2026-10-12 it held 151 dead sockets; those were deleted by hand in the E-2228 session, so this task only has to stop new ones.
