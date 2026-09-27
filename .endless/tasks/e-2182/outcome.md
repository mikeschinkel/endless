# E-2182 synthesis — stopping worktrees colliding on migration numbers

## Settled

- **Keep incremental migrations; the DB is sacrosanct.** Rebuilding the SQLite projection from the ledger would dissolve the problem, but ledger rebuilds are unproven and need a lot of real-world use first. Not an option for the foreseeable future.
- **Dense integers, strict order, no timestamps (ED-1600).** Integers prove completeness ("we have all of them"); timestamps can't, and `git log` already dates them. goose out-of-order apply stays off, so E-2020's highest-version comparison stays valid (the "compare applied sets" change only mattered under timestamps).
- **The agent resolves a collision at rebase, not land.** `worktree land` refuses when the branch adds a migration and main has gained one since the merge-base. The agent renumbers above main's latest, confirms its migration still holds alongside what landed, resets its sandbox, re-verifies and lands again. Land never rewrites source, so what lands is what was verified.
- **Refusal output serves both readers.** A plain one-line human summary, then a pasteable block for the agent that is still readable to a human (no parameter soup). When an agent runs land (the user may ask it to), add the agent top/bottom lines, as `task show`/`task add` do, so `| head`/`| tail` keep the verdict. Endless already detects agent vs human reliably.
- **Hybrid gate: common case easy, uncommon case possible.** A built-in Go check driven by a project's declared migrations directory (a tool-agnostic git diff, with no knowledge of goose/Alembic/Prisma), plus an optional `.endless/hooks/pre-land.sh` that can veto with its own explanation, rendered by the same refusal renderer.
- **Sandboxes are ephemeral for verify.** goose tracks applied migrations by number, so a renumbered migration would be skipped or double-applied on a sandbox that already ran it. Verify must always start from a fresh sandbox, built into the verify command (not the suite script), through a new user-facing `endless sandbox reset`. Seeding cost is paid every run. Endless commands are the front door: never have callers invoke project hooks directly, so gates added later can't be bypassed.
- **Downstream projects.** Endless should support their migrations at a high level, delegating to the platform's best-practice tool. The gate is already tool-agnostic. goose runs only `.sql` and compiled Go (no shell scripts), so first-class goose support helps Go projects only: a later task. Other platforms wait for user demand and are deliberately not filed.

## Found along the way

- Verify silently lost its fresh DB: `internal/verifycmd/verify.go` still says isolating `XDG_CONFIG_HOME` gives an Endless suite "a fresh DB for free", which stopped being true when E-1964 moved the DB into the worktree sandbox. Freshness was incidental, never designed.
- The XDG phase-out is unfinished: about 24 non-test files still read or set `XDG_CONFIG_HOME`.
- "What goes in a sandbox is the project's business" is a guideline, not a rule. Endless may add standard contents.
- E-1531's landing briefly broke `task show` on main (the CLI queried `task_content` before the Go binary carrying migrations 00006/00007 was installed). `just install` fixed it. This is a stale binary, not a collision, but it's the same family: CLI and migrator out of step after a land.

## Follow-ups

- **E-2184** (now): refuse land when main gained migrations since the branch's rebase. Hybrid gate, dual-audience refusal, plus Endless's own version-uniqueness / no-gap / Go-literal-matches-filename test.
- **E-1608** (re-specced, next): every verify run starts from a fresh sandbox via a new `endless sandbox reset`. Supersedes its earlier "project hook, never in verify" spec.
- **E-2185** (later): first-class goose support for Go projects.
- **E-2186** (next): finish phasing out `XDG_CONFIG_HOME`.
- **ED-1600** (proposed): strict-order integer migrations, collisions resolved by the agent at rebase.
