# E-1539 — Verify epic row exists in task_types

## 1. Scope

E-1538 landed the `task_types` FK mirror with `epic` (id=4, slug='epic', label='Epic') in the seed set. This task reduces to verifying the row is present after E-1538's migration ran.

## 2. Verification

```
$ endless sql "SELECT * FROM task_types ORDER BY id"
  id  slug      label
----  --------  --------
   1  task      Task
   2  bug       Bug
   3  research  Research
   4  epic      Epic
```

The epic row exists with the expected (id, slug, label) values matching the Go enum constant `TaskTypeEpic = 4`.

## 3. Out of scope

Every E-1537 child after this one (E-1540 CLI aliases, E-1541 auto-derivation, E-1542 pause-on-revisit hook, E-1543 promotion validation, E-1544 research creation gate, E-1545 guide docs, E-1546 noun-verb surfacing) depends on the epic row existing but does its own work. Each is filed independently.
