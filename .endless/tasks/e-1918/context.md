A session that never claimed a task cannot be re-entered.

'session resume ES-963' reports 'no session matches' because ResolveResumeTarget (Go) strips only E-/e-; 'session goto ES-963 --resume' refuses because _resolve_resume has no fallback when active_task_id is NULL; and _match_companions is ES-blind too, so session show/cd/use ES-N fail the same way.
