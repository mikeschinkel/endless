Recommended fix: move the dir==root check to the start of each loop iteration so projectRoot is never stat'd for a companion.

— Mike to advise whether to (a) split: reset E-1219 to plan-only and re-commit the impl on this task's branch, or (b) land E-1219 as-is and treat this task as already-completed-within-E-1219.
