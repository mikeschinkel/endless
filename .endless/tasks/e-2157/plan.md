# Plan as implemented

Settled with Mike in session, 2026-09-20. The analysis left two questions open
("the thing to settle in the plan"); both are answered here, and the scope grew
on his call from the flag rename to the duplication under it.

## Q1 — does `--db` appear on endless-migrate at all?

**Yes, minus one word.** `--db main` and `--db-dir <dir>` are accepted;
`--db sandbox` is REFUSED, naming why and naming the two flags that do work.

Rejected: `--db-dir` only. That is still a second dialect (a subset), leaves
`endless-migrate --db main` as "unknown command", and would have forced the
Python helper to spell out an absolute path instead of threading the word.

The refusal is the point, not a consolation. The guide teaches `--db
main|sandbox`; the reader who types `sandbox` here is exactly the reader who
learned it there and had no way to know this binary differs.

## Q2 — the transition hazard

**No compatibility window is needed, and none was added.** The hazard the
analysis feared (E-1668's own land: pre-merge Python, post-merge binary) cannot
fire for this change:

  - `worktree land` rebases the branch onto main (Step 4) BEFORE building
    endless-migrate (Step 4.6), so the binary always carries main + branch.
  - `_build_migration_executable` runs only `if schema_changes`. This branch
    adds none, so `_migrate_change` is never invoked during its own land.
  - Afterwards main's Python and every future land's binary both carry E-2157.

`--config-dir` is therefore removed rather than kept as an alias — but it is
recognised and refused by name, so muscle memory gets a one-word fix instead of
"unknown command".

## Q3 (sub-question) — do the two Python helpers merge?

**Delegation with one rewrite,** not a full merge. `migrate_db_context_args`
calls `go_db_context_args` and rewrites `--db sandbox` to `--db-dir <path>`.

A full merge would make a sandbox context a refusal. But migrating a worktree's
sandbox database is legitimate; ED-1571 objects to CWD-DERIVED resolution, not
to sandbox databases. Python owns cwd routing, so it resolves and names the
directory: the binary is told, it does not deduce. The fork is one named rule
instead of a whole second dialect, and the Go refusal backstops any caller that
bypasses the helper.

## Scope grown, on the owner's call

The task row scoped `internal/dbcontext`'s parser. Two larger duplications sat
under it, and both collapsed:

  - **One resolver.** `$HOME/.config/endless` was hand-joined six times
    (`monitor.ForceRealDB`, `mainConfigDir`, `PinMainDB`, `realDBPath`,
    `sandboxcmd.seedWorktree`, and dbcontext's own fallback). `realDBPath`'s
    comment already admitted the copy. All six now call
    `dbcontext.MainConfigDir` / `MainDBPath`.

  - **One parser.** `monitor.ConsumeDBFlags` and
    `dbcontext.ConsumeConfigDirFlag` were two parsers for one vocabulary — the
    task row's own words: "the package is shared, so its parser is the single
    place". dbcontext owns the parse; monitor keeps the `os.Args` wrapper and
    the sandbox resolution. **The split falls exactly where cwd enters**, which
    is what lets endless-migrate link the parse and refuse the one word it
    cannot resolve.

endless-migrate inherits the stricter semantics from the merge (trailing bare
flag, doubled flag — both silently tolerated before, both refused by
endless-go). E-1668 settled that question; merging extends the answer.

## The correction this carries

`Main` is NOT `Default`, and a test now pins the two apart. `ConfigDir` honours
`XDG_CONFIG_HOME`; `MainConfigDir` must not. E-1964 deleted `sandbox bind` — the
PERSISTENT injection — but the per-invocation one survives by design
(`sandboxcmd.Sandbox.Env`, `triagejob`, `minimizerjob`, `triage.py`). A resolver
that collapsed the two would migrate a sandbox during a land and report success:
the wrong-database failure this line of work exists to kill, inside the fix for
it.

## Also folded in

Both found while sweeping the vocabulary, both cheaper than a task row:

  - Two tests in `test_worktree_land_migrate_exec.py` stubbed
    `go_db_context_args` while the function under test calls
    `migrate_db_context_args` — the same wrong-stub that hid the original
    near-miss.
  - `internal/monitor/sandboxpath.go`'s doc claimed Python threads
    `--config-dir` to Go, untrue since E-1668.

## Not done, deliberately

`docs/research-2026-09-17-refusal-inventory.tsv` row 578 records
endless-migrate's old relative-path message. It is E-2155's dated research
artifact (that task is `completed`, and has its own reconciliation pass for rows
that landings move), so it is left alone rather than retrofitted here.
