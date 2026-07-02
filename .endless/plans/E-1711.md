# E-1711 — Excise drift_detection and its orphaned suggestions framework

## Why

- **drift_detection (E-917)** defaulted OFF since it landed and was never enabled;
  its false-positive remedy (`endless task touch --add-file`) was never built (the
  block message points at a nonexistent command); its "task scope" is inferred
  from edit history, so it is non-deterministic. Its original motivation (ad-hoc
  markdown file proliferation) faded as markdown moved into the DB.
- **suggestions (E-918)** is fed ONLY by drift's `SUGGESTION` banner, so it is
  orphaned the moment drift is removed. Deferred, not abandoned — the calibration
  idea may be re-envisioned later; git preserves the implementation.

## Removal scope

**Hook (`internal/hookcmd/claude.go`):**
- the PreToolUse drift check (`IsCheckEnabled("drift_detection")` + `IsFileInTaskScope`)
- `RegisterTaskFile` recording in PostToolUse
- `blockDriftViolation` (the drift block + `SUGGESTION` banner)
- `ScanRecentSuggestions` call on PreToolUse (~line 234)
- `CountOpenSuggestions` / the "N unreviewed suggestions" line on SessionStart
  context injection (~line 362)

**Monitor:**
- `internal/monitor/files.go`: `IsFileInTaskScope`, `RegisterTaskFile` — **keep
  `GetTaskTitle`** (generic, used elsewhere)
- `internal/monitor/suggestions.go`: the whole file (scanner + API)

**CLI:** the `suggestions` command group (`src/endless/cli.py` + `suggestions_cmd.py`).

**Config:** drop `drift_detection` from the `normalize.go` case and the
`internal/config/README.md` row.

**Schema:** remove the `task_files` and `suggestions` `CREATE` statements from
BOTH `internal/schema/schema.sql` and `src/endless/db.py`. Write **NO** DROP
migration — existing empty tables are harmless (single user, unshipped).

**Tests:** remove the drift/suggestion tests (`files_test.go`, suggestions tests,
and any drift cases in the hook tests).

## Finalize

- `/regenerate-guide` — the `suggestions` CLI command is gone, so the guide's
  command cross-reference must be regenerated.
- `just build` + `just test` green.

## Verify

Produce `tests/tasks/e-1711-verify.sh`: assert the symbols/table/config-key/CLI
command are gone, the packages build, and the hook + suggestions-free suites pass.
