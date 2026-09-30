Measured against the real ledger: 56 of 92,545 session_messages rows already hold invalid UTF-8 (51 tool_use, 3 user, 2 assistant; Edit 20, Bash 15, Write 8), plus one sessions.summary row. It is actively produced, since Edit and Bash inputs routinely exceed 500 bytes and contain non-ASCII.

It went unnoticed because Python's sqlite3 raises for the entire query on an undecodable column, so it surfaced as session list crashing rather than as bad text;
