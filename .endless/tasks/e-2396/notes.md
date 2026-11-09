## Evidence from a rerun (2026-10-18, after E-2217 landed)

The full `just test-go` and `just test`, run from E-2217's worktree with HOME
and XDG_CONFIG_HOME pointed at a fresh temp dir, left `<HOME>/.config/endless/log/cfgstore.log`
behind and nothing else. So at least one test resolves the DEFAULT home
config (the go-cfgstore logger), but this run opened no database there. The
branch carried no unapplied migration by then, so the run that migrated main
(04:16:50Z) may have taken another path: reproduce with a decoy home holding
a database one version behind the branch, as the plan's step 1 says.

Running the suites this way is a workable stopgap until this lands: a temp
HOME, with GOCACHE/GOMODCACHE/GOPATH/UV_CACHE_DIR pinned to their real
values, and the temp dir symlink-resolved (`pwd -P`), or
TestProjectPath_TildeExpandsToHome fails on the macOS temp-dir symlink.



## Evidence from a second rerun (2026-11-09, E-2217 rebased on main)

The same isolated run (temp HOME, Go paths pinned, plus GOENV, since Go's env
file also lives under HOME) showed two more traces of the default config:
- After the pytest suite, `<HOME>/.config/endless` existed in the temp home,
  so a Python test still writes there. The verify harness's guard then
  refused the next script as a direct run.
- `internal/hookcmd` TestAutoBindFromCwd_MarksAutoSpawned panics with
  "Must call cfgstore.SetLogger() with a *slog.Logger before reaching this
  check", reached through monitor.livenessReady and OtherTaskHolders. A test
  that reaches liveness reads config without setting it up.
