# Plan: Default minimizer.enabled to false

## Why
The `task report` requirement is deprecated but not removed. `minimizer.enabled` defaults to true, so a session that enters a project with no `minimizer` key in `.endless/config.json` gets the report-channel instructions and the Stop hook gate. This hits every project that has never set the key. Mike hit it on 2026-10-17 in go-cliutil (E-2289): the PreToolUse worktree gate blocked `endless task report`, and the Stop hook then refused every reply.

## Scope
Flip only the default for `enabled`, from true to false. Leave the `optimizer` default as it is. An explicit `"enabled": true`, the `"minimizer": true` shorthand, and the legacy `"report_gate": true` still turn the channel on. Endless's own `.endless/config.json` already declares `enabled: false`, so it is unaffected.

## Steps
1. Go, `internal/monitor/db.go` `readMinimizer`: change the default to `MinimizerConfig{Enabled: false, Optimizer: true}`. Update the doc comments on `readMinimizer`, `MinimizerEnabled` and `MinimizerConfigForCwd` that say absent means on.
2. Python, `src/endless/config.py`: change the `default` dicts in `project_minimizer_config` and `minimizer_config_declared`, and the outside-any-project fallback in `minimizer_config_for_cwd`, to `{"enabled": False, "optimizer": True}`. Fix the docstrings that say "Absent file, absent key, or unreadable all mean BOTH ON".
3. `src/endless/help_settings.py`: make the default described in help match.
4. `docs/guide/tasks.md`, around the minimizer config section: say the channel is off unless a project sets `"enabled": true`. Regenerate any generated guide index if the edit touches one.
5. Tests:
   - Go `internal/monitor/minimizer_config_test.go`: rename `TestMinimizerEnabled_DefaultsOn`. "no config file", "config without the key", "object naming only the optimizer" and "malformed json" now want false. Rewrite the malformed-json comment: malformed now falls to the default, which is off.
   - Python `tests/test_minimizer_gate_cwd.py`: `test_outside_any_project_defaults_on` now expects off, and the optimizer-only case keeps the optimizer default. Check `tests/test_task_report.py`, `tests/test_guide_conditionals.py` and `tests/test_minimizer_loop.py` for fixtures that rely on the default being on, and give them an explicit `"enabled": true` where the test is about the channel itself.
6. Run the full Go and Python suites.

## Verify
In a scratch dir registered as a project with no `minimizer` key, a new session gets no report-channel SessionStart text, and the Stop hook doesn't demand `task report`.
