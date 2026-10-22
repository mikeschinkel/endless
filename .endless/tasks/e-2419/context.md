Agents are still told to write a harness-based `verify.sh`, which only the endless repo can run: `_harness.sh` and `_guard.sh` exist only there (E-2349's EQ-84 settled that they are not installed in other projects; `verify.toml` is the suite form for PRODUCT). go-cfgstore's E-2206 had to fall back to a `verify.toml` with a self-contained `sh` check.

The advice that still names `verify.sh` or the harness, found while planning E-2349:
- the handoff template `internal/templatecmd/templates/handoff/_close.tmpl` (what E-2413 asked for);
- `SUITE_RULES` in `src/endless/suite_rules.py`, the source of every project's `.endless/tasks/CLAUDE.md`: both its "Writing one" section and its opening lines naming `_harness.sh` and `_guard.sh`;
- `docs/guide/orchestration.md` and `docs/guide/reference.md`;
- CLI text in `task_cmd.py`, `session_cmd.py`, `unlanded.py` and `doc_mirror.py`; some of it may describe the runner, which still runs both forms, so each needs reading rather than a blanket edit.

The endless repo keeps `_harness.sh` and `_guard.sh`: 338 of its existing suites source them, and reopening one needs them.

Delivering the changed `.endless/tasks/CLAUDE.md` into existing projects is E-2349's (project init), not this task's.
