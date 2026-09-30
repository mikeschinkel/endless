This works for tests that only need a task to exist, but loses fidelity: we're not actually testing that emit_event is called correctly, the event is shaped right, or the project lookup happens.

Better infrastructure options: (1) build a Python-side stub for emit_event that writes the same DB row the Go binary would, configurable via fixture; (2) point the Go binary at the test DB via env var or flag; (3) accept the limitation and document the patterns.
