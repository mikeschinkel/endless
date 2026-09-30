Decisions are currently stored as tasks rows with type='decision'.

Sharing the table with implementation tasks means task-only operations (worktree auto-create, task confirm/assume, status flow assuming verify-before-confirmed) get applied to decisions where they don't make sense - observed 2026-05-16 with E-1373 (worktree created just to hold a plan file; 'confirmed' used where 'complete' was correct), prompting band-aid gates E-1375/E-1376.
