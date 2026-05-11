# Task Lifecycle: Statuses, Phases, Blocking

## Statuses

| Status        | Meaning                                                                                                       |
|---------------|---------------------------------------------------------------------------------------------------------------|
| `needs_plan`  | Not yet planned — needs design work before implementation. Attach a plan with `task update <id> --text <file>` and the task auto-promotes to `ready`. |
| `ready`       | Planned and ready to implement.                                                                                |
| `in_progress` | A session has claimed the task and is actively working on it. Set automatically by `task claim`.               |
| `verify`      | Implementation done, awaiting verification. **Still blocks dependents.**                                       |
| `confirmed`   | Verified and done. **Unblocks dependents.** Only the user confirms — sessions do not self-confirm.            |
| `assumed`     | Believed complete, will verify when used naturally. **Unblocks dependents.**                                   |
| `blocked`     | Waiting on something else (typically another task or external decision).                                       |
| `revisit`     | Was partially planned but needs re-evaluation before implementation continues.                                 |
| `declined`    | Active decision not to do this. Requires `--reason` (stored as outcome).                                       |
| `obsolete`    | Made irrelevant by other changes.                                                                              |

### When to use `assume` vs `verify`

`verify` says "implementation is done, please check it." `assumed` says "I believe it's done but can't verify it independently."

Use `assumed` when:
- The only way to test the change is by using it in another task (the dependent task IS the test).
- You finished a UI/UX change and can't drive the screen yourself.
- The behavior is observed correct in passing but not exercised in tests.

Set the outcome when assuming: `endless task assume <id> --outcome "What was done and how confidence was established."`

## Phases

| Phase   | Meaning                                                                                                                                  |
|---------|------------------------------------------------------------------------------------------------------------------------------------------|
| `now`   | Current priority.                                                                                                                        |
| `next`  | Up next after current work.                                                                                                              |
| `later` | Future work, not urgent — **committed to do eventually**.                                                                                |
| `maybe` | Considered but not committed — **may or may not be done**. Distinct from `later`: `later` says "we will, just not yet"; `maybe` says "we might". Promote `maybe` directly to `now` or `next` when a decision is made. |

Do not conflate `blocked` with `maybe`. `blocked` means "will do when X resolves"; `maybe` means "might do at all."

## Blocking semantics

When task A is blocked by task B (`endless task block A --by B`):

- B in `verify` → A is **still blocked**. Verify means "needs to be checked" — the work isn't yet trusted.
- B in `confirmed` or `assumed` → A is **unblocked**. The dependency is resolved.
- B in `declined` or `obsolete` → A is **unblocked**. The dependency is moot.

In `task show`, dependencies display as:

- **Needs: E-123** — active blocker (not yet done).
- **Enabled by: E-123** — resolved blocker (done).
- **Enables: E-456** — tasks that depend on this one.

## Lifecycle flow (typical task)

```
needs_plan → ready → in_progress → verify → confirmed
                                         ↘
                                          assumed (when verify isn't practical)
```

Off-ramps:
- `revisit` — plan needs more work; usually returns to `needs_plan` or `ready`.
- `declined` — explicit decision not to do; requires `--reason`.
- `obsolete` — no longer relevant.

## Plan attach auto-promotes

When you attach a full plan to a `needs_plan` task via `task update <id> --text <plan-file>`, Endless promotes status from `needs_plan` → `ready` automatically. You don't need a separate `--status ready` flag.

## See also

- `endless guide tasks` — full CRUD reference
- `endless guide decisions` — when status changes warrant a decision record
- `endless guide fields` — what `text`, `outcome`, etc. are for
