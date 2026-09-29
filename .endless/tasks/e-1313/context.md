Default isolation_level is deferred, so the implicit BEGIN never commits — mutations rollback on connection close.

Discovered while testing the session_statuses INSERT path for E-1312.

Other helpers in src/endless/db.py already call conn.commit() after mutations — this command was the oversight.
