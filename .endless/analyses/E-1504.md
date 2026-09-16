## session status: the sweep must not drop the ownership signal

Noted 2026-09-11 from E-2111's session, which hit the failure this prevents.

`session status` exposes `--json` and no text flag, so this sweep is what gives
it an agent-facing view at all. That view has a requirement the other commands
do not: **who owns each row is carried by a GLYPH, and only by a glyph.**

`classify()` in `internal/sessionstatuscmd` tests `IsFocal` before `InFlight`,
so a row renders `●` when it is this session's own task and `⟳` when a
non-focal row is in flight — meaning another session is working it. That single
character is the whole signal. The JSON already carries it structurally as
`is_focal`, `in_flight` and `relation`; the text view must carry an equivalent
in words.

WHY THIS MATTERS. A session that cannot tell its own work from another
session's reports the other session's work back to the user, who then receives
the same item twice — once here and once from the session that actually owns it.
That happened: a `/whats-left` report told Mike to confirm E-2128 and to spawn
E-1881, both of which belonged to ES-1199. The glyph was on screen and was
misread. In a text view where the glyph is simply gone, it is not misreadable,
it is unavailable.

Concretely, the agent view of a row needs enough to answer "is this mine?" —
`relation` (claimed vs surfaced) and `in_flight` at minimum. The owning session
id is NOT needed for the render: a user navigates to a session by task id, and
an agent only needs to know the row is not its own. `--json` may carry the id
even though the text view does not.

This is also the clearest evidence for `--agent` over `--plain`. Rendering
"another session is working this" where the human view prints `⟳` is not
stripping chrome; it is rendering for a reader who cannot interpret a glyph.
