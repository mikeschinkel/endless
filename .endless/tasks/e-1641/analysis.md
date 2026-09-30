Must NOT be a raw endless sql --write DELETE: sessions(id) is FK-referenced by 7 tables (session_messages, focuses, task_landings, decisions, session_gates, project_next_*), and per E-807/E-808 the event log is authoritative so a projection rebuild would resurrect deleted rows.

Prune at the ledger/compensating-event layer and check FK refs first.
