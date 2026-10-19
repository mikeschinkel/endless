# Name verify.toml as the suite form in handoffs

Rewrite the `handoff_verify` block in internal/templatecmd/templates/handoff/_close.tmpl so a spawned session writes its suite as `.endless/tasks/e-<id>/verify.toml`, with the task's own tests as its first check, and reads `.endless/tasks/CLAUDE.md` before writing it. Replace the template comment's reason for naming verify.sh with the reason for naming the manifest.

`.endless/tasks/CLAUDE.md`'s "Writing one" section is where the handoff sends the session, so it leads with the manifest form: a `verify.toml` example with a `gotest` check and an `sh`/`tap` check, then the harness-based `verify.sh` as the form for a project that carries `_harness.sh`.

Update the handoff render tests for the new text.
