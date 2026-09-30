internal/templatecmd/templates/handoff/epic.md.tmpl line 24 instructs the epic coordinator to 'fan out via endless task spawn --bg <child-id>'.

Background agents are currently problematic (e.g. no TMUX_PANE) and Mike is avoiding them until verified to work as well as tmux-hosted sessions.
