# E-1455 prompt: Use Claude Code env vars as truth for current-pane identification

## Context

**E-1450 (status: ready) addresses the dominant cause** of attribution failures observed during E-1396 verification. E-1450 makes hook-fired writes always target the real DB regardless of sandbox routing, eliminating the cross-DB confusion where a hook wrote a session row to the sandbox DB while the CLI was hitting the real DB. After E-1450 lands, the manual `INSERT INTO sessions ...` workaround used during E-1396 is no longer needed for that case.

What remains is a narrower detection-layer issue, addressed by this task.

## Symptom (post-E-1450)

A Claude Code pane with `CLAUDECODE=1`, `CLAUDE_CODE_SESSION_ID=<uuid>`, `TMUX_PANE=%NNN` in its env can still fail attribution in two scenarios that E-1450 doesn't cover:

1. **First-event timing race**: the user opens a Claude pane and immediately runs an `endless` CLI command before Claude has fired its first hook event. The DB has no row yet, so `session_id_resolve()` returns "not a Claude pane" — even though the env vars unambiguously identify this as a Claude pane with a known session_id.
2. **Unnecessary DB round-trips**: every attribution-requiring CLI invocation queries `_live_sessions()` (which shells out to `endless-session-query list-live`) to identify the current pane, even though the env vars in the same process would answer the question directly.

## Root cause

`src/endless/session_cmd.py:1353-1399` (`session_id_resolve()`) and `src/endless/task_cmd.py:2050-2063` (`_find_sibling_claude_session()`) identify Claude panes by DB query, not by env-var inspection. The env vars Claude Code provides are read only in `src/endless/task_cmd.py:231` (`_running_under_agent()` — a yes/no check, not an identity resolver). The DB is treated as the source of truth for "is this a Claude pane and what's its identity," when in fact the env vars in the current process are a fresher, faster, more authoritative answer for the *current* pane (DB queries remain the right answer for *other* panes).

## Fix: env-vars-as-truth for current-pane identification

Make `CLAUDECODE=1` + `CLAUDE_CODE_SESSION_ID` + `TMUX_PANE` the authoritative signal for "this pane is a Claude session with identity X." The DB becomes a projection of that env-var truth, used only for cross-pane sibling lookup, not for identifying the current pane.

### Changes

1. **`session_cmd.py:session_id_resolve()`**: Check env vars first. If `CLAUDECODE=1` and `CLAUDE_CODE_SESSION_ID` are set, treat the current pane as a Claude pane with that session_id. Return immediately without consulting the DB for current-pane identification.

2. **Lazy DB-row creation**: If the env-identified session is not in the target DB (first-event-timing case), INSERT a row (`session_id`, `platform='claude'`, `process=TMUX_PANE`, `state='needs_input'`, project_id resolved from cwd) using `INSERT OR IGNORE` keyed on `session_id` to avoid races. This both unblocks the current command and seeds the row for subsequent queries / sibling lookups from other panes.

3. **`task_cmd.py:_find_sibling_claude_session()`**: Cross-pane sibling lookup keeps the DB query (a pane cannot read another pane's env vars). But the current pane's own identity short-circuits via env vars before any sibling search runs. Document the precedence: same-pane → env vars; cross-pane → DB.

4. **`task_cmd.py:_running_under_agent()`**: Stays as-is; already env-var based and used only as a boolean predicate.

### Why this composes with E-1450

After E-1450 lands, hooks always write to the real DB. The lazy INSERT in change #2 above lands in whichever DB the CLI is targeting — which, post-E-1450, is the same DB the hook would have written to anyway. So the env-var path produces the same row the hook would produce, just earlier. Subsequent hook events upsert the same row idempotently. No divergence between the two write paths.

### What this does not do

- Doesn't replace `TouchSession()` in the hook — that stays as the primary registration path for sessions whose lifecycle includes a hook event (which is most of them). The env-var fallback only fires when the resolver runs before any hook event.
- Doesn't reintroduce companion JSON files (retired by E-1426). The env vars are the per-pane source of truth; the DB remains the cross-pane source of truth. This is a layering refinement, not a reversal of E-1426's direction.
- Doesn't address attribution from non-Claude contexts (cron, scripts, plain shells). That's E-1444's `--no-session` flag, complementary.

## Verification

1. From a fresh Claude pane with `CLAUDECODE=1` and `CLAUDE_CODE_SESSION_ID` set, before the hook has fired any event:
   - Run `endless session id`.
   - Expected: returns the Claude session UUID; the target DB now has a `sessions` row matching.
2. Run `endless decision add ... --about <existing-task>` from the same pane in the same conditions.
   - Expected: succeeds, decision row attributes to the env-identified session.
3. Concurrency: launch two CLIs in the same pane simultaneously before the hook has fired. Expected: no duplicate-key error (INSERT OR IGNORE); single row in `sessions`.
4. Negative: a non-Claude pane (no `CLAUDECODE`) still gets the existing "not a Claude pane" error. Detection must not over-apply.
5. Regression: existing endless-spawn flow (hook has already fired, row exists in DB) keeps working — env-var path just gets there a step earlier without altering the result.
6. Cross-pane sibling: pane A is env-identified as Claude (and lazy-INSERTed its row in step 1). Pane B in the same tmux window runs `endless task bind <id>`. Expected: pane B finds pane A's session via the DB query and binds correctly. (This verifies that the cross-pane path still works when the same-pane shortcut isn't applicable.)
7. With E-1450 not yet landed: the env-var path still works, but the lazy INSERT lands in the sandbox DB (if XDG_CONFIG_HOME routes there) while the user may have wanted the real DB. Document this as "pre-E-1450 behavior; resolves automatically once E-1450 lands."

## Related

- **E-1450** (ready) — makes hook-fired writes always target the real DB. Addresses the cross-DB symptom directly; E-1455 complements it on the detection side. Land E-1450 first; E-1455 is small enough to follow quickly.
- E-1429 — explicit `--db main|worktree|sandbox` routing for self-dev worktrees. Composes cleanly: env-var detection works regardless of which DB the CLI is pointed at.
- E-1419 — "Fix worktree-created companion files being invisible to main-DB session resolution from worktree cwd." Possibly merges with this task — same family of bug; env-vars-as-truth fix likely subsumes it.
- E-1426 — retirement of companion JSON files in favor of DB-as-truth. This proposal does NOT reintroduce companion files; it adds env-vars as a per-pane truth layer on top of DB-as-truth-for-cross-pane.
- E-1444 — `--no-session` flag for attribution bypass. Different angle on the same pain point; useful for cron / scripts where no Claude env vars exist.
- E-1396 — where this was hit during verification.
