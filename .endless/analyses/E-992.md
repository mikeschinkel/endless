Currently 'endless session history' requires an explicit session id or Claude UUID prefix, both of which are cryptic and forgettable. Once E-989 lands, 'current session' can be resolved deterministically from the companion file, so the default should be to show history for the current session.

Resolution mirrors 'session show' (E-991):
- No arg: resolve via companion file — calling shell's claude ancestor, or sole claude pane in this tmux window. Error with candidates if ambiguous.
- Explicit id/UUID: behaves as today.

Verification:
- Inside a Claude pane: 'endless session history' shows that session's messages.
- From a sibling pane in tmux with one Claude session: same result.
- Ambiguous (two Claude panes, no arg): errors with candidates.
- Explicit arg path: unchanged from current behavior.

Out of scope: redesigning the history output itself. This is purely about default resolution.