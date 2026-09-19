Endless has two Go binaries that both need to be told which database to open,
and since E-1668 they take different words for it. `endless-go` takes
`--db main|sandbox`, with `--db-dir <path>` as the escape. `cmd/endless-migrate`
(ED-1571) takes `--config-dir <dir>` — the flag E-1668 retired from the other
binary. One product, one concept, two vocabularies.

## Why it matters

The guide teaches `--db`. A reader who learns it and then reaches for the
migration executable is wrong, and nothing tells them so at the point of use.

It also costs a fork in the Python layer: `config.go_db_context_args()` and
`config.migrate_db_context_args()` exist solely because the two binaries
disagree, and every future caller has to know which one it is talking to.

That fork already produced a near-miss. `worktree land` shells to
endless-migrate and threaded `go_db_context_args()`; when E-1668 changed that to
emit `--db main`, endless-migrate could not parse it — `--db` survives its strip,
lands in `argv[1]` where the subcommand belongs, and the migration fails
mid-land. The binary's own test stubbed the helper, so the suite stayed green
while every real land would have broken. One vocabulary removes the class of
mistake, not just the instance.

## The design question this carries

`--db sandbox` resolves from cwd. `internal/dbcontext` is deliberately cwd-free
— ED-1571's whole claim is that a tool which rewrites a schema resolves its
target from what the caller NAMED, never from where it happens to be standing.

So the two binaries cannot take an identical flag set, and the proposal is:

  - `endless-migrate --db main`     → the main database ($HOME-following, no cwd)
  - `endless-migrate --db-dir <p>`  → name a directory outright (today's
                                      `--config-dir`, renamed)
  - `endless-migrate --db sandbox`  → REFUSED, naming why: this executable has
                                      no cwd routing, so there is no "which
                                      sandbox" for it to answer.

That is the same vocabulary minus the one value that would contradict ED-1571,
rather than a second dialect. Whether the refusal is the right call, or whether
`--db` should simply not appear on this binary at all and only `--db-dir`
should, is the thing to settle in the plan.

## Scope

  - `internal/dbcontext`: `ConfigDirFlag` and `ConsumeConfigDirFlag` gain the new
    spelling. The package is shared, so its parser is the single place.
  - `cmd/endless-migrate/main.go`: usage text, package doc, and the
    "Neither --config-dir nor XDG_CONFIG_HOME nor a home directory" error.
  - `src/endless/config.py`: whether the two helpers merge back into one is part
    of the design — they can, if endless-migrate accepts everything
    `go_db_context_args` can emit except `--db sandbox`, and the land always
    pins main.
  - Tests: `internal/dbcontext/dbcontext_test.go`,
    `internal/schemachange/executable_test.go` (3 sites),
    `tests/test_worktree_land_migrate_exec.py`, and the doc comment on
    `runner.DefaultDBPath` in `internal/schema/changes/runner`, which names the
    flag in prose (`grep -rn -- --config-dir internal/schema` finds it).

## The transition hazard, which this task should not repeat

E-1668's own land failed once on exactly this: the `just land` Python process
had imported main's PRE-merge source, advanced main, then emitted the landing
event with the old spelling to a binary built from the new one. Version skew
across a process boundary, for one invocation, in the window the land itself
opens. The ff-merge was idempotent and a re-run recorded it, but the failure
is inherent to changing a cross-process flag contract in a self_dev land.

Decide in the plan how to avoid it: accept both spellings for one landing and
remove the old one in a follow-up, or establish that the land's own event write
must not straddle the merge.

## Not in scope

  - Which executable performs a land-time migration. ED-1571 stands.
  - endless-go's flags. E-1668 settled those.
