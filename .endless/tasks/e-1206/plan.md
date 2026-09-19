# E-1206 — Record each .endless/db-ledger/ write in git immediately

## Context

Today, `.endless/db-ledger/db-entries-<nodeHex>-NNNNNN.jsonl` is the write-ahead log for the Endless SQLite DB. Every endless CLI mutation appends one JSONL line via the Go `endless-event emit` subprocess (`internal/events/writer.go:68` `Writer.Append` → `O_APPEND` write). The file is **not committed at write time** — it accumulates as dirt on main between `endless worktree land` runs, where `worktree_cmd.AUTO_COMMIT_GLOBS` picks it up and bundles all accumulated lines into a single `Endless: auto-record session activity` commit.

Per E-987's resolution, write-time auto-commit is the correct pattern. E-1208 just shipped that pattern for `verbs.jsonl`. E-1206 applies the same pattern to db-ledger entries.

Volume reality check: the current segment has 1172 lines. In the same period, the git log shows 36 auto-record commits. So today's bundling collapses ~32 events into 1 commit. Without amend, E-1206 would multiply the commit count ~30×. With amend, the count stays bounded between feature commits.

## What's actually being changed

After every successful `Writer.Append`, commit `.endless/db-ledger/<segment>.jsonl` on main. Two write sites in `cmd/endless-event/main.go:148` (pre-allocation branch) and `cmd/endless-event/main.go:202` (update branch).

Per the task description's optimization note: **amend successive ledger commits into a single rolling commit between non-ledger commits**, bounded by the "is this commit pushed?" check.

## Amend strategy (proposed for v1)

Decide between **amend** and **new commit** at each write:

```
HEAD subject starts with "Endless: record ledger entry"
  AND HEAD is not reachable from any origin/* ref
  AND the index has no staged changes outside the ledger segment path
  → git commit -o <segment-path> --amend --no-edit
otherwise
  → git add <segment-path>
    git commit -o <segment-path> -m "Endless: record ledger entry"
```

This guarantees:
- One rolling ledger commit between any two non-ledger commits.
- Never amends a pushed commit (avoids force-push concerns).
- Never bundles unrelated user-staged work into our amend (the "index clean modulo ledger" check).
- Every event becomes part of git history immediately — amend captures the post-append working-tree state.

The check "HEAD is not pushed" uses `git for-each-ref refs/remotes/origin/* --contains HEAD` — empty result means not pushed. If no `origin` exists, treat as "not pushed" (safe locally).

The check "index clean modulo ledger" uses `git diff-index --cached --name-only HEAD -- ':!.endless/db-ledger/*.jsonl'` — empty output means no other paths staged.

## Code surface

**Files to modify:**
- `cmd/endless-event/main.go:148` and `:202` — after `writer.Append(line)` succeeds, call a new helper `commitLedgerSegment(projectRoot, segmentPath)`.
- `internal/events/commit.go` (new file) — `CommitLedgerSegment(projectRoot, segmentRelPath string) error`. Implements the amend-or-new-commit decision using `exec.Command("git", ...)`. Returns error on subprocess failure.
- `internal/events/commit_test.go` (new file) — Go tests covering: first commit (new), second commit (amend), commit after non-ledger commit (new), commit when index has unrelated staged file (new, not amend), commit when HEAD is pushed to fake origin (new, not amend).

**Existing utilities to reuse:**
- `cmd/endless-hook/claude.go:826` `runGitRevParse` — same `exec.Command("git", ...)` pattern; existing house style for git subprocess calls.

**No changes needed:**
- `internal/events/writer.go` — the writer stays git-agnostic. The commit step is layered on top.
- `src/endless/event_bridge.py` — Python's bridge already shells out to `endless-event`; nothing to change there.
- `src/endless/worktree_cmd.py:AUTO_COMMIT_GLOBS` — keep db-ledger in the list for now. E-1272 (the bundled land change) is the one that drops it. Once both ship, land's auto-commit step shrinks; here we just stop *creating* dirt.

## Failure handling

Per E-1208's pattern (Mike: "fail loudly until we know what failure modes look like"):

| Failure                                | Response                                                |
|----------------------------------------|---------------------------------------------------------|
| Project root is not a git repo         | Fail loudly. `endless-event` exits non-zero with a clear message. Mike's projects are all under git; the soften-to-silent-skip path is filed as a follow-up only when beta testers join. |
| `git add` / `git commit` / `git amend` fails | `endless-event` exits non-zero with the git stderr. The JSONL write persists; the next event's commit may amend or new-commit to recover. |
| Concurrent writers on same node        | Cannot happen — segment files are partitioned by node ID (`db-entries-<nodeHex>-...`), so two same-node writers serialize at the OS level on the file. |
| Concurrent writers on different nodes  | Acceptable. Each writes its own segment; each commits its own path. Git's ref-update lock serializes the commits. |

Consistent with E-1208's "fail loudly" stance. Non-git-project tolerance is deferred until beta-tester needs make it concrete; we'll soften the response then.

## Performance note

Each event today costs ~1 subprocess (`endless-event emit`). After E-1206, each event adds 2–3 git subprocesses (HEAD subject check, optional pushed-check, optional index-clean check, then commit-or-amend). At ~20ms per subprocess spawn on macOS, that's ~60–80ms added per event. An `endless task update` emitting 3 events would add ~200ms.

Acceptable for v1. Optimization paths if it bites: (a) batch multiple events into one commit when one CLI invocation emits multiple, (b) move to a long-lived endless-event daemon, (c) use go-git in-process. Defer all of these.

## Verification

1. **First write commits**: From a clean main checkout, `endless task add "Audit the X system"`. Expect: `git log -1 --format=%s` shows `Endless: record ledger entry`; HEAD advanced; .endless/db-ledger/*.jsonl is clean.
2. **Second write amends**: Run another `endless task update <id> --status verify`. Expect: HEAD's commit subject is still `Endless: record ledger entry`; **HEAD's parent did not advance** (HEAD's parent is unchanged; HEAD itself was amended).
3. **Non-ledger commit breaks the chain**: Run a normal commit (e.g., touch a source file and commit). Then `endless task update <id> --status ready`. Expect: new ledger commit appears as a child of the source commit.
4. **Worktree session**: From a worktree, run any endless mutation. Expect: the commit lands on main's HEAD (not the worktree branch).
5. **Dirty main preserves user work**: Stage an unrelated file change (`git add src/foo.py`). Run an endless mutation. Expect: the staged change remains staged; HEAD's ledger commit does NOT include foo.py. The "index clean modulo ledger" gate falls back to a new commit if needed.
6. **Non-git project**: Create a fresh dir (no git init), register as endless project, run a mutation. Expect: `endless-event` exits non-zero with a clear "project root is not a git repo" message. JSONL write happens first (already on disk).
7. **Tests**: `go test ./internal/events/...` — new tests cover the amend/new-commit decision matrix.

## Resolved during planning

- **Amend in v1**: ship.
- **Commit subject**: "Endless: record ledger entry" (uses the registered verb `record`).
- **Non-git project handling**: fail loudly (same as E-1208's pattern). Soften only when beta testers materialize.
- **Pushed-check scope**: `origin/*` only. Mike pushes occasionally; the check is a safety net against silent force-push potential after a manual push.
