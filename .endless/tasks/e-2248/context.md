Mike's screenshot, 2026-10-06, of session monitor for the E-1883 session.

Row list: E-2216 had just been spawned to another session (marked F, the focus row) but rendered bright, while E-2217, also spawned elsewhere, rendered dim as expected. A row another session is handling should be dim; possibly the focus highlight overrides the dim style.

Blocker lines below the table: 'E-2216 => E-2247' and 'E-2217 => E-2215' both render the blocker id dim, but both blockers are underway and still blocking, so neither should be dim. The lines appear to reuse the row's 'handled elsewhere' dimming, when what matters there is whether the blocker is still in force.