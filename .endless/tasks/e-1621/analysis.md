that queries sessions WHERE kind_id=(SELECT id FROM session_kinds WHERE slug='background') AND state='working' AND active_epic_id=<resolved-epic>, prints id/short_id/task/title/started_at.

--epic E-NNNN overrides the auto-resolved epic; --all drops the epic filter entirely (effectively the same as claude agents but endless-side).
