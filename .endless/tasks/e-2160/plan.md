# Plan — every Python refusal and stderr warning names its class in the call

Implements the Python half of the enforcement design in
`docs/research-2026-09-17-refusal-inventory.md`. The rows to convert are the
`lang=py` rows of `docs/research-2026-09-17-refusal-inventory.tsv`: 532
classified (REPORT 80, NO-REPORT 338, CONDITIONAL 114) plus 12 INFO and 24
EXCLUDED.

## Order

After E-2159 (the Go half), which owns the golden rendering and the
`ENDLESS_AUDIENCE` contract. E-1063's freeze on `task_cmd`, `cli`,
`worktree_cmd` and `session_cmd` is not in force, so those files convert
directly.

## Build

1. Extend `agent_help.agent_error` (E-2097) rather than adding a parallel helper.
   `Refusal(click.ClickException)` with class-named factories only:
   `no_report(summary, remedy, guidance)`, `report(summary, decision, guidance,
   human_remedy)`, `report_if(summary, condition, remedy, decision)`,
   `fault(summary)`, `relay(go_stderr)` (adds no directive; keeps Go's verdict
   lines at both ends), `relay_foreign(text, as_=…)`; plus `warn.no_report`,
   `warn.report`, `info`, `passthrough_exit`.
2. `agent_facing()` honours `--agent` (trigger 2), detected in the root group's
   argv pre-scan that already consumes `--agent-view` — one place, not per call
   site. When `agent_facing()` is true, set `ENDLESS_AUDIENCE=agent` so endless-go
   subprocesses render for the agent too.
3. The root group's `main` classifies, once each, the two categories no site
   writes: Click's own usage errors and `ParamType.fail()` → NO-REPORT usage;
   an uncaught exception → fault.
4. The check: `tests/test_refusal_sites.py`, an AST walk of `src/endless/`
   (precedent `tests/test_no_self_dev_ids.py`) failing on any
   `ClickException(`/`UsageError(`/`BadParameter(`/`Refusal(` construction or
   subclass, `click.echo(..., err=True)`, `print(..., file=sys.stderr)`,
   `sys.stderr.write(`, or a non-zero `sys.exit`/`SystemExit`/`ctx.exit` outside
   the helper module. Its failure message states the rule and lists the
   factories.
5. Assert E-2159's golden verdicts render byte-identically here.
6. Convert every Python row in the TSV to its class. *State* CONDITIONALs
   compute the class at the site; *intent* ones use `report_if`; relays of
   endless-go use `relay`.
7. While each message is open, fix the Python message defects the doc lists
   (`_resolve_resume` and `_apply_revisit_intent` tell the user to revive a
   declined task with `--status revisit`, which is not a legal edge — only
   `untriaged` is; `_reopen_task_core` leaves `<status>` unfilled;
   `_require_status_allowed_for_type` and `_refuse_cascade_across_typed_descendants`
   name `--status completed` for research and epics; `_check_task_ownership` says
   "have it release the task", and `task release` is disabled; `_orphan_refusal`
   says task ids are reused; `_migrate_v5` names a backups directory under the
   home directory, not the config directory's `backups` where they are written; and every `just install` / `just land`
   remedy, which does not exist in a foreign project).
8. Make the cause reach the site, or the class is wrong: `_live_sessions`
   returns `[]` on any endless-go failure, so a broken install reads as the
   NO-REPORT "No Claude session matches" — it must raise a fault instead;
   `_worktree_in_use_probe` prefers the probe's stdout, so `worktree drop` quotes
   the reason and drops its cause — include both.

## Decide before implementing (open questions in the doc)

- Q1: should `--format agent` fire the directive like `--agent`? (E-1504 made it
  the long form.)
- Q2: widen trigger 1 beyond the two recognised Claude Code harnesses?
- Q3: accept the traceback → fault classification in the root `main`?
- Q4: warnings the user should act on but that block nothing become inert under
  the rule (doc, *did not fit* item 2) — accept, or give them another channel?

## Verification

- `just test` passes, including the new check.
- Temporarily adding `raise click.ClickException("x")` anywhere in
  `src/endless/` fails the check with a message naming the rule.
- As an agent (`CLAUDE_CODE_ENTRYPOINT=cli`), `endless task add` with a 107-char
  title prints the bracketed verdict carrying the NO-REPORT directive; as a
  human, stderr is byte-identical to today's.
- `endless task update <id> --status confirmed --agent` from an unplanned task
  (a Go refusal relayed by Python) prints Go's verdict unprefixed at both ends.
