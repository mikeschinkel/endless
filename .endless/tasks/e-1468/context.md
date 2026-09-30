When two Claude session identities share one physical tmux pane (e.g. a worktree-keyed session and a main-checkout-keyed session during a land run from main), the live session is falsely marked ended; it only recovers on its next Stop hook (TouchSession upserts never restore state).

Repro: E-1459 land run from the main checkout spawned queue-operation session a27e5710 on pane %203, flipping live session 510 to ended.
