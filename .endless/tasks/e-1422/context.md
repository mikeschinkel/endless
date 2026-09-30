Symptom: pane %142 (this conversation, ~2 days old) has no companion JSON in .endless/sessions/ and no session record in DB. Every other recent pane (%148, %157, %159, %163) has exactly one companion; this one is missing.

Result: resolver returns None, E-1401 gate fires, every endless CLI invocation from the pane is blocked.

The SessionStart hook (or whatever creates companion files) failed silently for this conversation when it began.

Today there is none.

Workaround today: ENDLESS_SESSION_ID=<sibling-id> endless ...
