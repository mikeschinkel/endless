## Go: the base directory in internal/monitor

- A package-level base directory, default "/", read by loadDirRules and ResolveDirectory. endless-go's global argument scan in cmd/endless-go/main.go (the one that finds --db and --db-dir anywhere in os.Args) also finds --base-dir, strips it, and sets the base directory before any subcommand runs.
- resolveRulePath joins every built-in rule path and every overrides-file path under the base directory. The home-and-ancestors rule stops at the base directory.
- ResolveDirectory symlink-resolves the base directory, then refuses a directory outside it with a sentinel error (doterr style) naming both paths. When the base directory is not "/", it refuses a HOME outside the base directory with a second sentinel error.
- test_seam.go gains SetTestBaseDir(dir) (restore func()). TestTempTreeRemovals and FreeTestTempTree are deleted.

## Python: threading the flag

- src/endless/cli.py declares a global --base-dir option, hidden from the human --help and listed by the agent-facing help (agent_help / AgentHelpMixin).
- config.py threads the resolved base directory to every endless-go subprocess next to go_db_context_args(), only when it is not "/".

## Fixtures

- tests/conftest.py's isolated_env sets the base directory to the test's tmp_path (the same way it pins the config dir), keeps HOME inside it, and stops writing disallowed-paths.json; TEMP_TREE_REMOVALS is deleted.
- Go fixtures (withTestDB in internal/monitor/baseline_test.go, SetTestDB, the sessionquerycmd seed helpers) set the base directory to the test's per-test temp directory and HOME inside it. Subtests and helpers that make temp directories outside it are moved under it.
- disallowed_paths_test.go and tests/test_disallowed_projects.py build fake roots (<base>/private/tmp with a relative <base>/tmp link on macOS; <base>/tmp and <base>/var/tmp on Linux) and assert the real built-in rules refuse them, replacing the remove-override workaround.
- E-2369's verify.sh runs its temp-space checks against a fake root instead of writing a remove override.

## Docs

- docs/guide/disallowed-paths.md gains a section on --base-dir, regenerated with `just disallowed-doc` from DisallowedPathsMarkdown.
