Validation missing in task assume, task confirm, task add --status, and task update --status — all four paths should refuse with a clear error directing to --status completed.

Check should look at the merged value (existing + new), not just the param passed — currently forces redundant --outcome re-pass.

Should render as a '— Outcome —' section displayed AFTER the '— Text —' section.

All three are corrections to existing behavior, not new features.
