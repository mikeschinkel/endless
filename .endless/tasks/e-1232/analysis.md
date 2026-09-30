The verb 'start' suggests 'begin work', which is permissive — but the operation actually enforces exclusive ownership (per E-1203's refusal of concurrent live sessions).

'claim' telegraphs that semantic accurately; users hitting the refusal get an outcome consistent with the verb.

Implementation: rename click command and start_item() → claim_item(); add release click command + release_item() that clears sessions.active_task_id for the current session via event emission, leaves tasks.status unchanged, leaves worktree intact.

No 'start' alias (per no-legacy-code rule) unless explicitly requested.

Update verb registry, CLAUDE.md references, and test names.
