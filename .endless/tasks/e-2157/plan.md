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

## Second pass: the inventory this landing falsified

Reopened after landing, on Mike's correction. I had reported the stale
refusal-inventory row and asked whether to file it. ED-1550 rule 2 exempts
"work still live in the session that landed it" and says to reopen when what
shipped is WRONG — and a doc is false BECAUSE of this change, which is this
landing being wrong rather than a new finding on the same subject.

Fixed in `docs/research-2026-09-17-refusal-inventory.tsv`:

  - the relative-path refusal's message, which named `--config-dir` and
    `XDG_CONFIG_HOME` as the things that failed to resolve;
  - four sentinels anchored to `internal/monitor/db.go` that this task moved
    into `internal/dbcontext` (`ErrDBFlagConflict`, `ErrDBFlagNeedsValue`,
    `ErrDBDirFlagNeedsDir`, `ErrUnknownDBValue`), whose notes also said the
    print site was endless-go's alone — now both binaries;
  - two rows ADDED for refusals this task introduced: the retired-`--config-dir`
    message and endless-migrate's `--db sandbox` refusal. An inventory that
    records a binary's refusals and omits its newest is wrong the same way a
    stale row is.

Layer E of the verify suite proves these rows against the source.

## Third pass: the anchors, and the check that keeps them

Mike asked whether I am the employee who says "that's not my job". I had
declined the adjacent fix with a "causation test" I invented — ED-1550 contains
no such rule; its headline is that agents must CLOSE more than they file, and
rule 7 is that closing IS work. The real test, already in my instructions, is
cost: a fix cheaper than the task row describing it gets folded in, bounded only
by size.

Audited all 570 Go rows. Most of what my first detector flagged was detector
error — E-2155 had marked every stale row, using two markers (`RETIRED:` and
`RELOCATE:`) where my regex knew only one. What was genuinely wrong:

  - **Five rows I broke in pass two.** E-2155 anchors a row to the symbol that
    RAISES a refusal, not the one holding the string (`handlePreToolUse` over
    `declarationRefusal`, `Run` over `usage`). I anchored the moved sentinels to
    the VARS. Re-anchored to `ConsumeFlags`, and the endless-migrate row to
    `configDir`.
  - **Two genuine misattributions**, both the same shape — a package-level var
    credited to the function above it. `resolvedPath` → `guardWorktreeDBContext`;
    `ensureAutoRegisteredProject` → `ProjectRootFromCwd`, which
    `ErrNoProjectContext`'s own doc comment states in prose ("returned by
    ProjectRootFromCwd").
  - **Thirteen rows parked on `RELOCATE:`** — E-2155's imperative "find this
    before converting" marker, i.e. open work blocking E-2159's row-by-row
    conversion. All thirteen traced and resolved: ten retired by E-2137, one by
    E-2142, one relocated by E-2148 (whose message shape changed with the
    rename, so the text was updated too).

**The cause fix**: `tests/test_refusal_inventory_anchors.py`. Nothing validated
the anchors before, so a landing that moved a symbol broke rows for free and in
silence — which is how this rotted, and how I re-broke it. Mutation-tested
against all three defect classes.

Scope of that test is deliberately existence, not message location: 212 of the
553 live Go rows carry a paraphrase rather than the literal, and avoiding false
positives on the rest needed a three-hop reachability search that still left
two. A flaky gate gets switched off rather than fixed, so the wrong-but-existing
symbol class stays human judgment and is named as such in the test's docstring.

### Still not done, deliberately

Four rows anchor a refusal to a function that reaches the message text through
two or three hops of delegation (`blockLandedSuiteEditIfApplicable` →
`landedSuiteEditDecision` → `landedSuiteDecision` → `landedSuiteRefusal`;
`guardOwnTaskOnly` → `&ForeignLandedSuite{}` → its `Error()` method). Each was
hand-checked and each is CORRECT under the raiser convention — they are listed
here only so the next audit does not re-flag them.

The 212 paraphrase-message rows are unverifiable by machine and were not
hand-audited. That is a real gap, named rather than papered over: auditing them
is a row-by-row read of the whole inventory, which is E-2159's job, and it is
already doing exactly that pass.
