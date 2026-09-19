Consolidated 2026-08-25 from three tasks that carried the same sentence — 'Required before projection can rebuild <table>'.

E-910 (sessions): work_started, chat_started, idled, ended, task_completed,
recapped, hidden.
E-911 (projects): registered, updated, renamed, unregistered, purged.
E-912 (notes, conversations, messages): the remaining entity mutations.

Evidence for the scope call, NOT a scope decision — settle it against the tree
at pickup, because this list is a snapshot and the ground moves:

- E-2029 dropped the channel tables, and E-1489 and E-1491 ('Port channel DB
  access', 'Port suggestions DB access') are both obsolete for the same reason.
  So E-912's conversation and message half may have no subject left. Check.
- Sessions are deliberately machine-local runtime state today, and several
  schema comments say so ('session_tasks is live-only session state and is not
  rebuilt from the ledger'). Emitting session events changes that premise, so
  E-910's half is a design question, not just wiring. See E-799's analysis.
- E-1035 (focus events failing the session_id FK on replay) is downstream of
  the same gap and has been folded into E-1041.