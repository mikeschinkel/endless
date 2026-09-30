Claude's hook-error display truncates around 160 chars.

Current wrap chains print log-timestamp + binary-name + 'claude:' prefix + duplicated paths, eating the window before the root cause.

(2026-05-15 example: the actual 'no such table: projects' was truncated off because the full path appeared twice.)
