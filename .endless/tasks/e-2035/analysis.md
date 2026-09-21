# A third writer, and the semantics question routing it forces (found 2026-09-05, from E-2030)

`unregister_project()` (src/endless/unregister.py) belongs on step 2's writer
list. Step 2 says to audit rather than trust the list, and this is what the audit
finds: it is not a doc mirror, so a sweep looking for mirror-shaped code misses
it. It writes `"status": "unregistered"` into `.endless/config.json` at
`resolved(row["path"])` — the path the DB row names, not the cwd — and then
deletes the DB row.

Observed damage, 2026-08-21 through 2026-08-23. The write landed in the real
checkout and, because `.endless/config.json` is tracked, a session's `git add`
swept `status: active -> unregistered` into commit 89333179 alongside an
unrelated `cli.py` change. From there:

  * `handlePreToolUse` returns at `if !isRegistered { return nil }`, so the
    commit-on-main block (E-1012), the cwd invariant (E-1586), the revisit gate
    (E-1542) and the write-tool claim gate were all silently off for Endless's
    own repo.
  * `relay_gate` fails open for unregistered projects, so the report channel
    stopped being enforced.
  * `reconcile.py` refuses to re-insert a project whose config says
    `unregistered`, so it could not heal — it was fixed by hand.
  * With the project absent from the DB, `ProjectIDForPath` missed on every walk
    and four worktrees were auto-registered as standalone projects (E-2009).

This is the case Mike's 2026-08-27 restatement was written for: a file that has
to differ per machine and must never ride a land into main.

## The semantics question routing it forces

Routing this write is not enough on its own, and the plan has to answer it to
route correctly. Sandboxing only changes WHERE the value lands; under `--db main`
it still writes machine-local state into a shared, tracked, worktree-inherited
file. Two distinct things are conflated in one `status` field:

  * `unregistered` — "this project is not in THIS machine's DB". Machine-local.
    Not a property of the project, so it does not belong in the tracked config
    under any DB target. `register.py`’s status validation already refuses it (valid values are
    active, paused, archived, idea), so nothing can register back a project
    carrying the status `unregister` wrote.
  * `disabled` — "this project exists and is switched off". A real project
    property, shared, and legitimately belongs in `config.json` alongside
    `paused` and `archived`.

`unregister` reached for the only status field available and got the first by
writing the second's storage. Splitting them is what makes the routing rule
answerable: `disabled` is a committed project fact, registration is not.

## Where each fact goes (settled with Mike 2026-09-06)

`status` leaves `.endless/config.json` entirely. It is the field that made this
possible: one string holding a shared project fact and a per-machine one, in a
file every clone and every worktree inherits.

  * "This repo is not Endless-managed" -> `.endless-ignore` at the REPO ROOT,
    tracked. A fact about the git repo, not about a directory or a machine, so
    everyone cloning should get it. Ignores the whole subtree, which is most of
    its value: one marker excludes a tree of clones. `.endless/IGNORE` was
    considered and dropped -- a marker inside a managed project's tracked
    `.endless/` would propagate to every worktree, which is this bug again one
    directory over.
  * Project lifecycle (`disabled` / `paused` / `archived`) -> the DB, which
    already owns it: `register.py` sets and validates there. config.json's
    `status` only SEEDS the row on insert (`reconcile.py`’s insert path), so dropping it
    costs nothing -- a newly discovered project defaults to active.
  * "Do not let reconcile re-add this on MY machine" -> the user config dir,
    where the ignore list already lives and which `unregister`'s own docstring
    notes it deliberately does not use.

Result: no per-machine state in any tracked file, by construction rather than by
a rule someone has to remember.

## Implement the marker check in Go only

Do NOT build a Python copy with a parity test. Python is being retired, so a twin
is a twin to delete -- and a rule with two implementations is what produced the
guide/hook divergence E-2030 fixed, where Python resolved the report gate from
the enclosing project while Go walked up from cwd. The check belongs in Go; the
Python scanner consults it the way Python already shells out for template
rendering and event emission.



# The general rule this task is an instance of (settled with Mike, 2026-09-08)

Mike, restating the principle: anything that has to be configured for testing,
or is read or written during testing, inside the project directory during
self_dev should be written to the sandbox — not to the real checkout.

That is this task's charter stated as a rule rather than as a list of offending
writers. `unregister_project()` is one instance; it will not be the last, because
each new feature that writes something reintroduces the leak at a new call site.
Auditing writers one at a time is how the area got here.

## It is not a self_dev concern — it is a product mechanism

Mike's extension, and the part that changes the shape of the deliverable: ANY
project can have files it needs written to a sandbox rather than its checkout —
test fixtures, generated config, machine-local state. So the user, typically
with their agent's help, must be able to DECLARE which of their paths are
sandbox-resident.

If endless instead hard-codes its own offenders, every downstream project
reinvents the mechanism, badly, and endless's own list is the only one that ever
gets maintained. A declaration is also the same shape E-1964 already settled for
sandbox contents: endless creates the directory, the project's own hook fills
it, and what goes there is the project's business.

## Scoping rule (Mike, 2026-09-08)

- **In self_dev**, files that tests read or write, and machine-local generated
  config, are sandbox-resident.
- **In any other project**, a file gets NO special handling unless the project
  declares it. The default is an ordinary tracked file. Endless does not decide
  for a project which of its files are disposable.

This is what keeps the mechanism from becoming a self_dev special case wearing a
general name.

## What is deliberately NOT part of this

The per-worktree `.claude/settings.json` override is NOT an instance of this
rule, despite looking like one. It must live where Claude Code discovers
settings — the project root's `.claude/` — so it cannot be relocated to a
sandbox at all. Its correct fix is to stop overwriting a tracked file and write
the gitignored local-tier file instead; that work belongs to E-1347, which owns
it and carries the analysis.

Recorded here because the two problems are easy to merge on sight and the merge
attaches the wrong remedy to both: sandbox relocation cannot fix settings.json,
and a gitignored sibling file does not generalize to test-written state.

---

# Addendum — this section predates E-1457; re-read it against settings.local.json (2026-09-21, from E-1972)

Evidence only. No argument about the artifact-root resolver, which this leaves
untouched, and no scope change.

## The mechanism described here is historical

This section says `.claude/settings.json` "is a tracked file that legitimately
needs a different body in every worktree," hidden with
`git update-index --skip-worktree`, and picks a symlink-into-the-sandbox
direction to replace that.

**E-1457** (`cleans_up E-998`, landed 2026-05-24, `16b2832`) already moved the
per-worktree override to **`.claude/settings.local.json`** and added a
`settings.local.json` gitignore rule matching that name at any path. `.gitignore`
records this in its own comments, including that there is intentionally no rule
for `.claude/settings.json` because a change to it on main blocked the rebase in
every live worktree.

So the rebase collisions this section attributes to skip-worktree have a
different cause today, and the symlink direction was chosen against the older
mechanism.

## Measured 2026-09-21

    worktrees with .claude/settings.json:              141
      carrying a "hooks" block:                          1   (e-2122, live session)
      with skip-worktree set on that file:               1

    worktrees with .claude/settings.local.json:        138
      carrying a "hooks" block:                        115
        pinned to their OWN bin/endless-go:            115

The per-worktree body is alive; it is simply in the other file, and that file is
gitignored rather than skip-worktree'd. An earlier version of this addendum
measured only `settings.json`, concluded the per-worktree body was gone, and was
wrong — recorded so the number is not re-derived from it.

## What still holds regardless

**The second bug this section documents is independent of all of the above and
still real:** the generator reads `git show HEAD:.claude/settings.json` from the
WORKTREE BRANCH's HEAD, so any key added to the committed copy after a worktree
forks is silently dropped there. That is a bug about the generator, not about
per-worktree bodies or about which file holds them.

The binary-selection half — which binary those hooks name — is E-2166's
(ED-1595). Cross-referenced so neither task re-derives it.
