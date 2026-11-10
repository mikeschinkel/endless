## Approach

**Schema.** `verify.Manifest` (internal/verify/manifest.go) and `verify.ProjectConfig` (internal/verify/project.go) each gain `Env map[string]string` under the TOML key `env`, a table of variable names to string values. `verify.Merge` merges the two: the project's table first, then the task's, so a task's value wins for the same key. Loading refuses a key that names `HOME`, an `XDG_*` directory the runner isolates, or any `ENDLESS_*` variable, with an error naming the key and the file.

**Expansion.** In internal/verifycmd, after `prepareEnv` has built the run's environment (person env, isolated HOME, `suiteEnv`), each value is expanded with `os.Expand` against that environment, so `$HOME` is the run's isolated HOME and `$ENDLESS_VERIFY_DIR` is the suite directory. A reference to an unset variable expands to the empty string, as in a shell. Values expand against the environment as it was before the table is applied, so one `env` entry cannot refer to another. The expanded pairs then replace any existing entry for the same key (`overrideEnv`).

**Order.** In the manifest path of `runSuite` (internal/verifycmd/verify.go), `runGoModChecks` moves from before `runSetup` to after it, and receives the environment with `env` applied. Its rows still come first in the summary and the CTRF. Setup, checks and teardown all receive the same environment. The legacy verify.sh path (`runScriptSuite`) is unchanged.

**Docs.** `.endless/tasks/CLAUDE.md` documents `env` beside `setup`/`teardown`, and its built-in go.mod row section says the row runs after setup with the suite's `env`. `docs/guide/orchestration.md`'s verify.toml reference adds the key.

**Tests.** internal/verify: parsing `env` from both files, the merge with the task winning, refusal of each reserved key. internal/verifycmd: a check sees an `env` value with `$HOME` expanded to the run's HOME; the built-in go.mod row runs after setup with the suite's `GOPROXY` — a module requiring a version that only a file proxy written by setup serves passes, and fails without the `env` entry; the built-in rows stay first in the report.
