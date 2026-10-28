Decided with the user in the planning conversation:

- Script suites (`verify.sh`) are deprecated: sessions are told to write only a manifest suite (`verify.toml`), and a hook refuses creating a new `verify.sh` for a task that has none on main. The runner still runs existing `verify.sh` suites, so tasks already carrying one can verify and land.
- Every registered project gets `.endless/tasks/CLAUDE.md` by a sweep over the projects table, not by asking users to re-run `endless project init`.
- Endless always overwrites the file. No per-project override for now; the file says so at its top and asks anyone who needs one to open a GitHub issue explaining why. Revisit if users object.

Decided without asking, open to objection:

- The sweep is a recurring runner job modeled on `doc-mirrors` (internal/docsweep), not a one-off script. Always-overwrite only reaches existing copies if something writes again after the text changes; a one-off would have to be remembered and re-run on every change.
- The file's text moves to Go and is embedded there. The job is Go (it reads the projects table, which Go owns), and one copy of the text avoids two that drift. `endless project init` places the file through `endless-go`, so init and the job write identical bytes.
- `_harness.sh` and `_guard.sh` are not shipped to other projects; with script suites deprecated there is no reason to.

Facts that shaped this, checked during planning:

- `project init` has written `.endless/tasks/CLAUDE.md` since E-2023 but never overwrote an existing copy, so projects registered before then never got it (gomion, until the user re-ran init) and every copy keeps whatever text it was first written with.
- `_harness.sh` and `_guard.sh` exist only in the Endless repo, so outside it a `verify.sh` written to the handoff's instructions cannot run.
