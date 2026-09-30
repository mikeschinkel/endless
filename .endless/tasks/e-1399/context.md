Until E-1395 fixed _tmux_window_pane_ids (2026-05-16), every emit_event call from a subprocess used tmux's currently-focused pane to resolve sibling Claude sessions, not the caller's window.

Repair is non-trivial: ledger is append-only and the correct attribution may not be recoverable from event payload alone.
