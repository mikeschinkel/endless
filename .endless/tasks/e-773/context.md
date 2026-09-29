Channel commands (beacon/connect/send/inbox/close) all call _resolve_session() which requires a sessions table row.

This forces users to run 'endless task start' or 'endless task chat' before messaging works.
