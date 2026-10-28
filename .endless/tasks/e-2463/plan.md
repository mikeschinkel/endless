# Plan: Ship the tasks CLAUDE.md everywhere and make verify.toml the suite format

## Approach

- **Text.** Move the text of `.endless/tasks/CLAUDE.md` out of `src/endless/suite_rules.py` into a Go package as an embedded file, rewritten around `verify.toml`: the files table lists `verify.toml` and `land.toml`; "Writing one" shows a `verify.toml` with a `pytest`/`gotest` check, `as = "agent"` and `tmux = true`; `verify.sh` appears only as a legacy format the runner still runs for suites that already exist. It opens with a note that Endless writes the file and may overwrite local changes, and that anyone who needs to keep changes should open an issue at https://github.com/mikeschinkel/endless/issues saying why.
- **Writer.** One Go function writes the file when it is missing or its bytes differ, exposed as an `endless-go` command. `endless project init` calls it in place of `scaffold_suite_rules`, which is removed.
- **Job.** A runner job modeled on `doc-mirrors` (internal/docsweep) walks every registered project, writes the file where it is missing or differs, and commits it on that project's main checkout through the same commit path `doc-mirrors` uses.
- **Hook.** In internal/hookcmd/verify_suite.go, a new arm refuses a Write, or a recognized Bash write, that would create `.endless/tasks/e-<id>/verify.sh` when the main checkout has no `verify.sh` for that task. The refusal names `verify.toml` and the guide section. Editing an existing `verify.sh` of one's own task is untouched.
- **Handoff.** `handoff_verify` in internal/templatecmd/templates/handoff/_close.tmpl names `.endless/tasks/e-<id>/verify.toml` and drops the `_harness.sh` sentence.
- **Guide.** In docs/guide/orchestration.md the suite-format table presents `verify.toml` alone, with `verify.sh` as legacy; the document-mirror and `land.toml` passages say "beside `verify.toml`"; the `as_agent`/`with_tmux` wording leads with the `verify.toml` keys. docs/guide/reference.md's mirror row likewise.

## Tests

Go tests for the writer (missing, identical, differing), the job over two registered projects, and the hook arm (new `verify.sh` refused, existing one editable, `verify.toml` allowed); a Python test that `project init` places the file; a template test on the handoff text.
