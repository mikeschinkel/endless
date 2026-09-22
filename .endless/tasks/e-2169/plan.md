Endless has a filing problem measured in its own ledger — ED-1550 records 391
filed against 264 closed over sixty days — and the rules it added put the
judgment on the filing agent, who is the least able to make it: it is mid-task,
it has just found something true, and "this is true" reads to it as "this
deserves a task".

A reviewer that is not the filer, and not under time pressure, can ask the
questions a filer structurally cannot:

- **Does the stated purpose still need serving, or is it already served?**
  Worked example, 2026-09-22: E-2166 was filed to preserve a per-task opt-in for
  running a worktree's hook binary. Nobody asked whether the capability was
  still needed. It was not — the verify suites already drive the real hook
  binary in isolation — so most of the task dissolved on the first person to
  ask. Two sessions had planned inside the framing without testing it.
- **Is the framing of the parent question smuggling in a premise?** E-1972 asked
  "how should hooks choose between two binaries", which presupposes a choice
  worth making.
- **Is this several symptoms of one cause?** ED-1550(3) already says file the
  cause; nothing checks.
- **Does an open task already own the area?** ED-1550(4) says search first;
  nothing verifies the search happened.

Shape is open. Candidates: a background job over newly-filed tasks, a gate on
triage (the routing step that already reads description, parent, siblings and
linked decisions), or a verb a person runs against a subset. Adversarial framing
matters more than placement — a reviewer that starts from "this should probably
be built" will confirm rather than challenge.

Output should be a recommendation with reasons, not a status change: the value
is the argument, and a reviewer that can decline tasks by itself is a new way to
lose work.
