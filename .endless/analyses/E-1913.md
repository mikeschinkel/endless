# Root cause

Three auto-transitions can change status during `task update`. `--keep-status`
guards two of them and cannot reach the third.

**Guarded (Python, `src/endless/task_cmd.py`):**

- `auto_revisit` (E-1762, ~line 3993) — a real text edit on a done task flips it
  to `revisit`. Condition opens with `not keep_status`.
- `auto_untriage` (E-1845, ~line 4018) — a material description edit on a
  pre-work task resets it to `untriaged`/`submitted`. Also opens with
  `not keep_status`.

**Unguarded (Go, `internal/events/executor.go:579-594`, `execTaskFieldsUpdated`):**

```go
if textVal, hasText := p.Fields["text"]; hasText {
    if _, statusSet := p.Fields["status"]; !statusSet {
        ...
        if isPreJudgmentStatus(currentStatus) {
            setClauses = append(setClauses, "status = ?")
            args = append(args, "submitted")
        }
    }
}
```

Its only suppression is `!statusSet` — "caller wins" when status is explicit.
`--keep-status` is never serialized into the event, and
`TaskFieldsUpdatedPayload` is just `Fields map[string]any`, so there is no field
in which the intent could travel. The Python comment at task_cmd.py:4031 even
names this split ("left to the executor's plan-attach auto-move: that move only
fires when the update does not set status explicitly") without noticing that
`--keep-status` therefore cannot influence it.

# The documented contract is also too narrow

`cli.py`'s help reads "Keep the current status when editing plan text on a done
task (suppresses the auto-revisit for a typo/formatting-only edit)." That
describes E-1762 only. `endless guide tasks` frames it as the escape hatch for a
"typo- or formatting-only edit". Neither promises the plan-attach case, so part
of this task is a deliberate decision to widen the flag to mean what its name
says: no auto-transition fires.

# Fix options

**(a) Python sends `status = <current>` when `--keep-status` is passed.**
The executor's existing "caller wins" branch then suppresses the promotion, and
the two Python guards already work. No event-vocabulary change, no new payload
field, nothing in `internal/events` to coordinate. Cost: the emitted event
carries a status field whose value equals the old one — a no-op status write in
the ledger. Replays cleanly.

**(b) Add `KeepStatus bool` to `TaskFieldsUpdatedPayload` and honor it in the
executor.** Semantically cleaner — the intent is named rather than encoded as a
degenerate status write. Cost: a new field in the event vocabulary, which is
exactly the surface E-1671's upcasting epic is about to rework, and whose
"Coordination" note asks work to be sequenced so it does not collide on
`internal/events` / `internal/eventcmd`.

**Recommended: (a).** It is the smaller change, it needs no ledger-shape
decision, and it keeps this fix off E-1671's path. If (b) is later wanted, the
upcasting pipeline is the natural place to introduce it.

# Provenance

Found during E-1906 while folding the E-1907 finding into E-1671's text. The
append promoted E-1671 `unplanned` -> `submitted` despite `--keep-status`, and
the status was restored manually.
