# Analysis — a blank status bar is indistinguishable from "no task here"

## The swallow

`internal/tmuxcmd/status_line.go` `runStatusLine` has three exits to
`placeholder()` (lines 27, 40, 52). The one at line 40 is the problem:

    status, err := monitor.GetPaneStatus(pane)
    if err != nil {
        // Real error (DB unreachable, etc.). Render placeholder; stay
        // silent on stderr to avoid log spam during interactive use.
        fmt.Print(placeholder())
        return
    }

`monitor.DB()` has ~7 fail-closed exits behind that single `err`: the
`schema.SQL` apply, five enum `VerifyIntegrity` gates (task_types,
session_kinds, gate_kinds, session_task_relations, nav_via_kinds), and
`guardWorktreeDBContext`. Every one of them renders as the same dim dot as
"this pane has no Endless context", and writes nothing anywhere.

The stderr silence is correct and should stay: the bar re-runs this binary once
per pane every `status-interval` (2s here, ~14 panes), so logging would be a
firehose landing on top of a live TUI.

## Evidence this matters

2026-08-05: the tmux status bar went fully blank twice, machine-wide. It could
not be diagnosed. Post-hoc the resolution path was healthy — 20/20 serial
samples of the exact bar invocation returned the task, and 60/60 under 13-way
concurrency — so the trigger was transient and is now unreproducible. A fault
record would have captured which of the seven exits fired, with its error text.

## Why the fault system fits

`faults.Record` dedupes an open incident in place, so a bar blanking every 2s
across every pane raises ONE incident rather than thousands of rows. That is
precisely the shape this call site needs, and it is why stderr was rejected.
`cmd/endless-go/main.go` already calls `faults.Bind` for every subcommand, so
the recorder is available here with no new wiring.

## Not in scope of the neighbours

- E-1884 converts 42 `log.Printf` sites (hookcmd 23, monitor 11, schema 5,
  channelcmd 3) — sites that write too NOISILY. This is the inverse: a site
  that writes nothing. `runStatusLine` is not among those 42.
- E-698 is `assumed` and landed twice. It did not introduce the swallow, which
  predates it; at most its +78 lines of schema widened the window.
