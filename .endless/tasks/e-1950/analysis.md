`endless session status` currently renders three problems in its fault badge area, reported from a live session on 2026-08-11.

## 1. A transient fault is stuck on the badge with no visible way to resolve it

The badge shows:

```
 WARNING  1 warning — ERR-0004 job scheduling row could not be created: "triage-sufficien…
        endless errors show
```

The underlying incident is a single SQLITE_BUSY ("database is locked (5)") while creating the `triage-sufficiency` job scheduling row, first and last seen 2026-08-10T09:49:09 — one occurrence, a day stale, self-healed. By design (`internal/sessionstatuscmd/faultbadge.go`) an incident stays on the badge until `endless errors clear`, but nothing on the badge says that, so the user reads it as a permanent error with no idea how to fix it. The badge names only `endless errors show`, which explains the fault but does not offer any resolution — and `errors show` itself does not mention `errors clear` either.

Needs a decision on the fix: surface the clear command, auto-expire/auto-clear stale self-healed warnings, make single-occurrence SQLITE_BUSY on job scheduling non-faulting (it's retried anyway), or some combination.

## 2. The hint line wastes a whole screen row

`renderFaultBadge` prints a second line containing nothing but the dim string `        endless errors show`. In a status pane every row is scarce. The command hint should live on the badge line itself (right-aligned or appended) rather than consuming its own row.

## 3. The WARNING chip is unreadable

`badgeWarning = "\033[30;43m"` (black on yellow) renders as a low-contrast orange block in the user's terminal — the label is effectively invisible. Reported preference: invert the entire badge row, with a foreground/background pair that is actually readable across light and dark terminal themes.

## Files

- `internal/sessionstatuscmd/faultbadge.go` — badge rendering, colors, hint line
- `internal/sessionstatuscmd/faultbadge_test.go`
- `internal/faults/` — fault store, overview, detail log
- `internal/errorscmd/errors.go` — `errors show` / `clear` / `codes`
- `internal/jobs/run.go` — the ERR-0004 raise site
