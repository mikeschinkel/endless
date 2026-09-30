All tables currently declare 'id INTEGER PRIMARY KEY' (without AUTOINCREMENT).

SQLite reuses deleted IDs in this mode —

confirmed 2026-05-17 when E-1403 was reused for a new task immediately after the prior E-1403 was deleted.

ID reuse breaks any cross-reference that survives a delete (outcome text, ledger entries, decisions, plan files).
