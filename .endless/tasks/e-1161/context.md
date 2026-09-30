E-1038 (commit 9ea22b3, 2026-05-01) reduced 'endless session use' activation from five ENDLESS_* exports to one (ENDLESS_SESSION_ID + cd). Quote from that commit: 'It will never fail for a stale value. Minimal wins.'

The CODE (src/endless/session_cmd.py, session_use_resolve) correctly emits only the two lines.

Discovered 2026-05-03 when Claude (me) read the cli.py docstring and confidently told Mike that esu exports five vars, propagating the lie. Symptom for users: stale docs make it look like a regression has shipped when it hasn't.
