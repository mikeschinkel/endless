The hook's Stop handler calls EndSession which sets state=ended.

Between Claude turns, _resolve_session() can't find an active session. This makes every msg command fail unless you re-run plan start.

Issue #2 from inter-session testing.

Issue #7 (needing plan start before every msg) is a symptom.
