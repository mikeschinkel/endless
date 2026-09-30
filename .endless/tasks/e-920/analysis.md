New pending_audits table. SessionEnd queries task_files for edits this session with no task association; writes a row if any. SessionStart surfaces uncleared audits as additional_context. CLI: endless audit {list,clear}.

Backstop only; should be empty in steady state once E-917 (drift detection) is enforcing.
