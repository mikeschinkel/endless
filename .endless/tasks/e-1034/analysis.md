Mike (2026-04-30) confirmed 'sidecar' is obsoleted in favor of 'companion file' (per the existing internal/monitor/companion.go convention). The E-971 foundation commit (e291de0) was authored with the right terminology, but several pre-existing references remain.

Files to update:

**Code:**
- cmd/endless-hook/claude.go (snapshot-file metadata uses 'sidecar' in comments and a variable name; rename to 'companion' to match the convention)

**Plans:**
- .endless/plans/E-968.md (multiple references, plus a 'Sidecar format' heading)
- .endless/plans/E-971.md (multiple references, plus a 'Sidecar format (JSON)' heading)
- .endless/plans/E-987.md ('Sidecar metadata for above')
- .endless/plans/E-969.md (multiple references)

Re-grep at pickup time ('grep -rn sidecar') to find current locations.

Mechanical replacement: 'sidecar' -> 'companion file' or 'companion' depending on context (variable names use 'companion'; prose uses 'companion file'). 'Sidecar' (capitalized headings) -> 'Companion file'.

Naming details for clarity:
- The per-session companion file already exists: <project>/.endless/sessions/<harness>-<session-id>.json (E-989, internal/monitor/companion.go)
- The per-worktree companion file is the new one: <worktree>/.endless/worktree.json (E-971 foundation, planned full impl in subsequent layers)
- Both are 'companion files' for their respective entity (session vs worktree)

Verification: 'grep -rn sidecar' across the repo returns only matches in unrelated documentation that legitimately uses the dictionary word.