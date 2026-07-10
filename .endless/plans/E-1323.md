You are working on **E-1323** (Add smarter dedup for session_statuses to catch agent-retry near-duplicates within a short window).

## What the bug looks like

Current dedup in `internal/events/session_status.go:isDuplicateOfLatest` is strict byte-equal on every content column against the latest row for the session. This catches the literal "called add twice with the exact same XML" case, but misses the more common case in practice: an agent retries the same status with slight rewording (em-dash replaced, sentence trimmed, an apostrophe escaped differently) and produces two rows the user perceives as identical but the byte-equal check sees as different.

Concrete incident in session 389:

- Row id=7 inserted at `2026-05-14T20:52:48` from a first attempt that the user interrupted.
- Row id=8 inserted at `2026-05-14T20:54:48` from the agent's retry after "Continue."
- The two rows differed by ~169 bytes total (87 in `decisions`, 82 in `summary`) — the agent rewrote sentences during the retry.
- User considered them duplicates; manually deleted row 8 as cleanup.

Strict byte-equal worked as designed; the design was too narrow for the usage pattern that emerged.

## 1. Get oriented

```
endless guide
endless guide sessions
endless task show E-1323 --text
endless task show E-1312                       # original CLI ship
endless task show E-1324                       # related CLI hang (separate task; may interact with retry pattern)
endless task show E-968                        # parent epic
endless task claim E-1323
```

Read the suspect code:

- `internal/events/session_status.go` — `isDuplicateOfLatest` is the function to widen
- `internal/events/session_status.go` — `execSessionStatusRecorded` is the caller; consider where the "near-duplicate" decision belongs (in the dedup helper, or upstream in the handler)

## 2. Do the work

Enter plan mode.

Three direction options to discuss with Mike before implementing:

**A. Time-window-based skip.** If any row exists for this session_id within the last N seconds (configurable, default maybe 60s), refuse the insert with a clear error message: "near-duplicate window in effect; previous status was recorded N seconds ago. Use --force to insert anyway." Cheap; doesn't need similarity computation; gives the agent explicit feedback.

**B. Semantic similarity threshold.** Compute a hash of normalized content (strip whitespace, normalize punctuation) or a Levenshtein/Jaccard score against the previous row. Skip-or-refuse if above threshold. More accurate but more code, and "what's the right threshold" is a tuning question.

**C. Combine: window + similarity.** Within N seconds, require similarity below threshold; outside the window, allow. Belt-and-suspenders.

**D. The "add --amend" approach.** Instead of refusing the second add, treat it as an amendment to the previous row — copy the previous row's content, apply the delta, INSERT a new row that's the merged version. Requires a clear semantics for "what counts as the merged version" per section. Heavier than (A), but handles the case where the agent IS legitimately updating the session's reported state.

Mike's earlier framing favored an explicit `--amend` flow eventually, but that's a bigger lift. **Recommendation for v1: option A (time-window skip) with `--force` escape hatch.** Simplest; clear feedback; doesn't preclude (D) later.

Open design questions for plan-mode chat:

- Default window size — 30 seconds? 60? 5 minutes? Mike's intuition will inform this.
- The error message: what does the agent need to know? (Window remaining, last row id, command to override.)
- Does `--force` exist on the CLI today? If not, add it.
- Interaction with E-1324 (CLI hang fix): if the agent's first attempt hangs and the user interrupts, the agent will retry. The retry should ideally NOT be blocked by a near-duplicate window — but the agent doesn't know that the first call's INSERT actually completed before the hang. Some way to signal "please re-attempt safely" might be needed. (Or just: the time window catches it as duplicate; agent gets a clear message; no harm done.)

Implementation sequence (proposed):

1. Plan-mode discussion confirming direction (A vs others) and window size.
2. Add `--force` flag to `src/endless/cli.py:session_status_add` and thread through `event_bridge.emit_event` (probably as a payload field or a separate flag to `endless-event emit`).
3. Update `isDuplicateOfLatest` (or add `isWithinNearDupWindow`): query for any row in the last N seconds; return true if found AND --force is not set.
4. Update the handler's response: when skipped due to window, return a clear `ExecuteResult{Skipped: true, Markdown: "..."}` with the window-remaining message in the markdown.
5. Update tests: byte-equal-skip still works; window-skip works; --force overrides; outside-window-but-similar inserts.
6. Verify via dogfood (after E-1324 is fixed so the CLI doesn't hang).

## 3. Closing

When implementation is complete:

- `endless task update E-1323 --status verify` with how-to-test commands (sequence: add, immediately re-add same content → window-skip; add, immediately re-add with --force → succeeds).
- Dogfood by recording your own work; verify the window-skip catches your own retry attempts gracefully.

Don't run `endless worktree land` without asking Mike. Don't mark `confirmed` yourself.

Goal: agent retry-after-interrupt patterns produce ONE row, not two. The window-skip's error message is informative enough that the agent knows the prior INSERT succeeded and doesn't need to retry.
