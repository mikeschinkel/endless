tasks.notes column exists in the schema but CLI has no --notes flag on add/update — only --description, --text, --analysis, --outcome are settable.

Discovered 2026-06-10 when wanting to add a closing note to E-1552; had to use endless sql --write as a workaround.
