# Make verify.toml the suite form in all advice

Every new suite, in every project including the endless repo, is a `verify.toml`
(user, Q2). Checks that no native test covers are written as named shell checks
in the manifest, with optional output matchers (user, Q1: D), so no session
hand-writes TAP or copies the harness.

## Manifest: `sh`, `name`, matchers (internal/verify, internal/verifycmd)

```toml
[[check]]
name = "the Claude session variable is unset"
sh   = '''
test "${CLAUDE_SESSION_ID}" = ""
'''

[[check]]
name     = "minimizer --help says enabled defaults off"
sh       = "endless minimizer --help"
contains = "enabled defaults OFF and optimizer defaults ON"
```

- `Check` gains `Name`, `Sh`, `Contains`, `NotContains`, `Equals`.
- An `sh` check resolves to a new shell driver: `sh -c <Sh>` at the worktree
  root with the run's env (so `as` and `tmux` apply unchanged). Its result is
  built in Go from the exit code and the matchers, not parsed from a stream:
  one CTRF test named by `name` (the command when unnamed). A failure's message
  holds the captured stdout and stderr, and for a matcher the expected value.
- `equals` compares stdout with trailing newlines trimmed, as `$(...)` does.
  Matchers read stdout; a check that needs stderr adds `2>&1`.
- Validation in `validateCheck`: `sh` is exclusive with runner, command,
  tests, paths and format; matchers require `sh`.
- `name` applies to every check form and labels its CTRF result.

## Advice

- `internal/templatecmd/templates/handoff/_close.tmpl` `handoff_verify`: the
  suite is `.endless/tasks/e-<id>/verify.toml`, native tests first and `sh`
  checks for the rest; the comment explaining the old wording is rewritten.
- `src/endless/suite_rules.py` `SUITE_RULES`: drop the harness and guard from
  the opening lines and file table; "Writing one" becomes the manifest form
  (native-test check, `sh` check with matchers, `as`/`tmux`); the person
  section's `as_agent`/`with_tmux` text moves to the `as`/`tmux` keys; one
  line: a directory with `_harness.sh` holds legacy script suites, documented
  in that file's header. The module docstring stops naming `_guard.sh` as the
  enforcement for every project.
- `.endless/tasks/_harness.sh` header: the legacy script suite — template,
  helpers, `as_agent`/`with_tmux` — moved from SUITE_RULES.
- `.endless/tasks/CLAUDE.md` in this repo is rewritten to the new SUITE_RULES.
- `docs/guide/orchestration.md`: "One suite per task" says to write a
  manifest, documents `sh`, `name` and the matchers, and marks the script row
  of the two-form table as the endless repo's legacy form.
- "beside that task's own verify.sh" becomes "beside that task's suite files"
  in `task_cmd.py`, `docs/guide/orchestration.md`, `docs/guide/reference.md`
  and the hook message in `internal/hookcmd/claude.go`.
- Runner descriptions (the copy of verify.sh taken at run start, direct-run
  refusal) and comments citing past suites stay.

## Suite

`.endless/tasks/e-2419/verify.toml`: gotest checks for internal/verify and
internal/verifycmd, a pytest check for tests/test_suite_rules.py and the
template test, and `sh` checks for the guide and harness-header text.
