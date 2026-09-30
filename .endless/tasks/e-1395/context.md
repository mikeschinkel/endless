Concrete misattribution observed 2026-05-16: claim from window @84 silently bound the task to session 451 in window @83 because user's tmux focus was on @83.

Every caller of this helper (claim/bind/release/spawn + every emit_event's session_id resolution) inherits the misattribution.
