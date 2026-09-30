Staleness detection (0 notes ever generated) and sprawl detection can be reimplemented via filesystem scan if needed.

Remove from: schema.sql, scan.py (scan_documents, check_sprawl), docs_cmd.py (entire command reads this table), status.py (document count).

Keep notes table intact — it has real user data.
