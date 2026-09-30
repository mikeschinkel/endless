Three lists in one pane, no separators: urgent (untruncated), then epics in now/next (untruncated), then everything else in now/next — the only truncated list, taking whatever pane height remains.

All reverse-chronological, with --sort taking updated (default) or id.

Each task appears once, in the highest list it qualifies for.

project status truncates nothing at all and gains --later.

Rows reuse session status rendering minus the age column and minus the session-relative glyphs, keeping the action and phase glyphs; sessions are still consulted so a live session shows as a different glyph, but no session is ever a row.

Removes the seven-rank grouping, the per-rank cap, --limit/--no-limit and --all.

Interim by intent: a scrolling TUI is expected to replace it.
