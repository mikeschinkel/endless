The two verbs write `tasks.parent_id` through different events: `move` emits `task.moved`, whose executor walks the ancestor chain and refuses a circular reference, while `update` emits `task.fields_updated`, whose executor carries `parent_id` in its allowedFields map and writes it with no such walk.

Reproduced 2026-08-25 — setting E-799 parent to E-1935 while E-1935 parent was still E-799 produced 799 -> 1935 -> 799, and every recursive CTE over the task tree walks that forever.
