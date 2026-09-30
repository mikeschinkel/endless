# Refuse writes outside the claimed task's worktree

This task merges the former Bash-writes task with E-1703 (the Write/Edit target
gate); E-1703 is closed as replaced by this one. It builds ONE decision — may
this session write to this path? — and applies it to both Write/Edit/NotebookEdit
targets and the write targets of recognized Bash commands. Parent: E-1712.
Already landed around it: E-1586 (the cwd invariant for all tools) and E-1711
(drift detection removed — there are no drift checks to carry over).

## Decisions (resolved with Mike)

1. **Scope:** only sessions holding a claimed, non-terminal task whose worktree
   exists. Taskless sessions in main keep today's rules and a usable shell.
2. **Unresolvable targets** of a recognized write form (`> "$OUT"`, `$(...)`):
   allow silently, after the static-prefix check.
3. **Unparseable write-capable commands** (`python -c`, `node -e`, `xargs rm`,
   `eval` beyond one level): allowed; the gap is documented in the guide as
   such. File a follow-up only if a real leak is observed.
4. **Always-writable locations** outside the worktree, for Write/Edit AND Bash,
   decided inside `writeTargetDecision` before containment:
   - device files (`/dev/null`, `/dev/stdout`, `/dev/stderr`, `/dev/tty`, `/dev/fd/*`);
   - OS temp dirs: `/tmp`, `/private/tmp`, `/var/folders/...`, and `$TMPDIR`
     (which covers the Claude Code scratchpad);
   - `~/.claude` (Claude Code's own config, plans and memory);
   - Endless's config directory, resolved the way Endless resolves it
     (`$XDG_CONFIG_HOME/endless`, else `~/.config/endless`) — never hard-coded.
5. **Git working-tree mutators** (`restore`, `checkout --`, `reset --hard`,
   `stash pop`, `clean`, `apply`, `pull`) count as writes to the repo they run
   in — **on trial**. Mike expects they may prove too restrictive and wants
   them quickly removable, so:
   - they are behind their own check key, `bash_git_writes`, read through
     `monitor.IsCheckEnabled` and defaulting ON; turning them off is one line,
     `"checks": {"bash_git_writes": false}`, in `.endless/config.json`, with no
     rebuild;
   - their recognition lives in ONE function (e.g. `gitWorktreeWrites`) that
     `bashWriteTargets` calls only when the key is enabled, so removing the
     feature later is deleting that function, its call and its test rows;
   - the refusal message for a git mutator names the key, so whoever hits a
     false positive can see how to switch it off;
   - the guide mentions the key.
6. **Sequencing:** merged. This task builds the shared decision and both gates;
   there is no separate Write/Edit task.

## Merged from E-1703 — the Write/Edit target gate

# E-1703 — Refuse write-tool targets outside the bound task's worktree

E-1703 was conditioned on E-1714 confirming that `enforceClaimedCwd` fires. It did
(E-1714, assumed): the gap is the target-path case, so this is built.

## Gap

Both existing PreToolUse gates are **cwd-based** and neither validates the write
target:
- `enforceWorktreeGate` (E-971) resolves the worktree from `payload.CWD`.
- `enforceClaimedCwd` (E-1586) checks `payload.CWD` against the bound task's
  worktree, and its message even invites "reach another directory by passing an
  explicit absolute path" (in `enforceClaimedCwd`; `enforceWorktreeGate` says the same).

So a claimed session sitting correctly inside its worktree can `Write`/`Edit`
the main checkout via an absolute path with nothing to stop it.

## Behavior

When a session has a **non-terminal claimed task whose worktree exists**, a write
tool (`Write`/`Edit`/`NotebookEdit`) whose resolved target path is **not** inside
that worktree is refused. Reads and Bash to outside stay allowed (reading main is
fine) — only write *targets* are constrained.

Runs **unconditionally** (independent of `tracking_mode`), matching its siblings
`enforceClaimedCwd`/`enforceWorktreeGate`, which both run before the tracking-mode
check. No new `checks.go` key.

## Implementation

- Factor the common preamble out of `enforceClaimedCwd` into a shared helper
  `sessionOwnedWorktree(projectID, payload) → worktreePath` (active session →
  non-terminal active task → `WorktreePathForTask` → lock-owner guard; returns ""
  when any precondition fails).
- New sibling `enforceClaimedWriteTarget(projectID, payload)`, called in the
  write-tools block next to `enforceWorktreeGate` :
  ```go
  if !writeTools[payload.ToolName] { return }
  wt := sessionOwnedWorktree(projectID, payload); if wt == "" { return }
  target := extractFilePath(payload.ToolName, payload.ToolInput)
  if target == "" { return }
  if !filepath.IsAbs(target) { target = filepath.Join(payload.CWD, target) }
  if !pathWithin(wt, target) { blockToolUse(writeTargetRedirect(taskID, wt, target)) }
  ```
  Reuses existing `extractFilePath` / `pathWithin`. `enforceClaimedCwd` keeps its
  single (cwd) purpose and now calls the shared helper too.
- New `writeTargetRedirect(taskID, worktreePath, target)` message (distinct from
  `cdRedirect`, which is about cwd): says the target is outside the worktree, that
  every edit for this task must land inside it, and to re-target the path under the
  worktree — and that if this is genuinely main-work, this isn't the task for it.
  Paths rendered via `tildePath`; literal worktree path kept absolute for paste.

## Message-consistency fix (confirmed)

Reword the "reach another directory by passing an explicit absolute path" lines in
`cdRedirect` (1434) and `enforceWorktreeGate` (1373) to scope that to **reads**:
"...for reads; writes must stay inside the worktree." So the guidance no longer
contradicts this gate.

## No `--force` bypass (decision)

Deliberately no per-write override. It would violate "gates, not guardrails"
(bypasses get taken), and would be taken in exactly the accidental case the gate
targets (the agent always believes its write is intended). There's also no
tool-level flag to hang it on — it would require a stateful "arm the next write"
command that is itself a standing bypass.

**Non-goal / future escape:** the genuine "write outside the worktree" case
(co-developed deps under `../go-pkgs/*`) is better served by a durable,
Mike-reviewed **allow-list of extra writable roots** (per-task/per-project), set
deliberately — not an in-the-moment agent override. Deferred; tie to **E-1085**
if it becomes a real need.

## Boundary (per E-1712)

Fires only when a non-terminal task *with a worktree* is bound. No-task /
terminal-task / unclaimed sessions and deliberate direct-to-main work
(global-config artifacts, ledger hygiene) are exempt, unchanged.

## Tests

- Unit: target in worktree → allow; target in main → block; relative target
  resolved against cwd → block/allow correctly; non-write tool → ignore; no
  claimed task / terminal task / no worktree → ignore; lock owned by another
  session → ignore.
- `tests/tasks/e-1703-verify.sh`: claimed session with cwd in its worktree —
  write-to-main blocked, write-in-worktree allowed, read-main allowed.

Exempt locations (decision 4) replace E-1703's original behavior of refusing
every target outside the worktree — as first planned it would have refused a
Write to /tmp or the scratchpad.

## Where things stand

- `handlePreToolUse` in `internal/hookcmd/claude.go` already has Bash handling,
  but every Bash gate is **verb-specific**, not target-specific:
  `blockSqliteAgainstEndlessIfApplicable`, `blockWorktreeRemovalIfApplicable`,
  `blockCommitOnMainIfApplicable`, `blockLandedSuiteRunIfApplicable`. Bash also
  passes through `enforceClaimedCwd` and `enforceRevisitGate` (all-tools gates).
- Everything after `if !writeTools[payload.ToolName] { return nil }` is
  write-tool-only: `blockDocMirrorWriteIfApplicable`,
  `blockLandedSuiteEditIfApplicable`, `enforceUnboundWorktree`,
  `enforceWorktreeGate`, and the tracking-mode declaration gate
  (`sessionMayWrite` / `declarationRefusal`).
- `enforceWorktreeGate` and `enforceUnboundWorktree` judge **`payload.CWD`**,
  not the write target. The only target-aware checks today are the two
  path-pattern gates (doc mirror, landed suite), each of which reads the target
  via `extractFilePath`.
- The merged Write/Edit half (from E-1703) adds the first *target-location* decision:
  `sessionOwnedWorktree(projectID, payload)` + `enforceClaimedWriteTarget`,
  refusing a write-tool target outside the session's claimed worktree.
- Reusable shell-text helpers already exist: `stripHeredocs`
  (`command_text.go`), `splitCommands`, `unquoteWord`, `resolveDir`, `cdRe`,
  `shellWord`, `cmdPos`, and the cd-tracking walk in `commitDir`.
  No shell-parser dependency is in `go.mod`.

## What the Bash gate enforces

**One rule: a Bash command may not write to a path that a Write/Edit to that
same path would be refused for.** The gate does not invent Bash-specific
policy; it extracts the command's write targets and asks the write-tool gate's
own question about each.

To make "the same decision" literal rather than parallel code, this task extracts a pure, target-keyed decision that both tool
paths call:

```go
// writeScope is everything the decision needs about the session, computed once
// per hook call. Built by newWriteScope(projectID, payload) from
// sessionOwnedWorktree (E-1703), monitor.ProjectPath, monitor.GetActiveSession.
type writeScope struct { ... }

// writeTargetDecision answers: may this session write to target?
// target is absolute and symlink-resolved. Returns the refusal text.
func writeTargetDecision(s writeScope, target string) (msg string, block bool)
```

`writeTargetDecision` composes, in order, the target-keyed checks that exist
for write tools:

1. **Doc mirror** — `docmirror.TaskDocRe` / `docmirror.LegacyTaskDocRe` on the
   target (today inline in `blockDocMirrorWriteIfApplicable`; refactor to a
   path predicate, message unchanged via `docMirrorBlockMessage`).
2. **Landed foreign suite** — `landedSuiteDecision` with
   `suiteTaskFromPath(target)` (today reached via `landedSuiteEditDecision`,
   which only differs in how it gets the path).
3. **Claimed-worktree containment** — E-1703's rule: if `sessionOwnedWorktree`
   returns a worktree and the target is not `pathWithin` it, refuse with
   `writeTargetRedirect`.
4. **Exempt locations** (evaluated before 3; see Decisions): OS temp dirs
   and device files are always writable.

Write/Edit/NotebookEdit then become: `writeTargetDecision(scope,
resolve(extractFilePath(...)))`. Bash becomes: for each target in
`bashWriteTargets(cmd, cwd)`, `writeTargetDecision(scope, target)`; the first
refusal blocks the call. The refusal message is the same text a Write would get,
prefixed with one line naming the Bash construct that was recognized
(e.g. "`sed -i` would write ~/…/main/foo.go").

The **cwd-keyed** gates (`enforceWorktreeGate`, `enforceUnboundWorktree`) and
the declaration gate are NOT applied to Bash by default. E-1586's plan
recorded the reason explicitly: extending the main-checkout / no-task refusal to
Bash makes main unusable from a shell (`just install`, `git pull`,
`endless …`). Whether a taskless session's *recognized* Bash writes into main
should be refused is an owner question below.

Placement: a new `blockBashWriteTargetsIfApplicable(projectID, payload)` in the
registered-project `ToolName == "Bash"` block of `handlePreToolUse`, after
`blockCommitOnMainIfApplicable` and after `enforceClaimedCwd` (so a drifted cwd
gets the `/cd` redirect, not a target refusal). Independent of `tracking_mode`,
like its siblings. New file `internal/hookcmd/bash_writes.go` holds extraction;
the decision lives next to E-1703's code.

## Extracting target paths — heuristic, and stated as such

Shell is not parseable in general without executing it. This gate is a
**mistake-catcher, not a security boundary** — the same stance `stripHeredocs`
documents. It recognizes the forms agents actually type and says nothing about
the rest.

### Lexing

Replace reliance on `splitCommands` for this gate with a small lexer
`lexShell(cmd) []shellToken` that honours single/double quotes and backslash
escapes and emits operator tokens distinctly. `splitCommands` cannot be reused
as-is: it splits on every `&` and `|`, so `cmd &> f`, `cmd >| f`, and `2>&1`
would be cut apart. Pipeline: `stripHeredocs` → `lexShell` → split into simple
commands on `;`, `&&`, `||`, `|`, `|&`, `&`, newline → per command, strip
leading `VAR=val` assignments and wrappers (`env`, `command`, `builtin`, `time`,
`nohup`, `sudo`, `exec`, `xargs`* — see below) → recognize.

Directory tracking: generalize the `commitDir` walk (apply each `cd <path>` in
order; `cd -` keeps the current dir; bare `cd` → `~`) into a shared
`walkCommands(cmd, cwd, fn func(dir string, argv []string, redirs []redir))`,
and make `commitDir` a client of it so both gates agree on "where does this run".
Also honour `git -C <dir>`, `make -C <dir>`, `(cd x && …)` subshells (dir change
scoped to the parens), and `pushd`/`popd`.

### Recognized write forms (v1)

| Form | Target(s) |
|---|---|
| `> f`, `>> f`, `>\| f`, `&> f`, `&>> f`, `N> f`, `N>> f` | `f` (not `>&N`, `N>&M`) |
| `cat > f <<EOF` (heredoc to file) | `f` (operator line survives `stripHeredocs`) |
| `tee [-a] f…` | every non-flag arg |
| `sed -i[SUF]` / `sed -i ''` / `--in-place[=SUF]` | file args after the script (`-e`/`-f` aware) |
| `perl -i[SUF]` / `-pi -e` | file args |
| `mv a… b` | every source and the destination (`-t dir` form too) |
| `cp`, `ln`, `install`, `rsync` | destination (last arg, or `-t dir`) |
| `rm`, `rmdir`, `unlink`, `shred`, `truncate`, `touch`, `mkdir` | every non-flag arg |
| `chmod`, `chown`, `chgrp` | every path arg after the mode/owner |
| `dd of=f` | `f` |
| `gofmt -w`, `goimports -w`, `prettier --write`, `ruff format`, `black` (file args) | file args |
| `curl -o f` / `--output f`, `wget -O f` | `f` |
| `tar -x… -C d` / `unzip -d d` / `patch [-d d] [file]` | `d` or the patched file / cwd |
| `find P… -delete` / `-exec rm …` | each find root `P` |
| `git apply`, `git am`, `git checkout -- …`, `git restore`, `git reset --hard`, `git clean`, `git stash [pop\|apply]`, `git mv`, `git rm`, `git merge`, `git rebase`, `git pull`, `git switch`, `git cherry-pick`, `git revert` | the repo's working-tree top for the effective dir (resolved with `git rev-parse --show-toplevel`, same approach as `isInMainCheckout`) |
| `bash -c S`, `sh -c S`, `zsh -c S`, `eval S` | recurse into `S` once (depth limit 2), dir inherited |

Explicitly **not** writes (never extracted): `git commit` (already governed by
`blockCommitOnMainIfApplicable`; and a worktree commit writes objects into
main's shared `.git`, so treating `.git` as a target would false-positive on
every commit), `git status/log/diff/show/fetch/push/add`, `git worktree …`
(governed by the removal gate), `endless …` and `endless-go …` (subprocesses
own their writes — the ledger, mirrors, `LESSONS.md` — by design), reads of any
kind, and `>`/`<` inside quoted strings or heredoc bodies.

`git add` is judged not a write (index only). `.git/` paths and `.endless/db-ledger/`
are not special-cased by this gate — `endless` owns them via subprocess.

### Resolving targets

Each target is `unquoteWord`-ed, `~`-expanded via `resolveDir`, joined to the
tracked dir, then symlink-resolved via the **nearest existing ancestor**
(`filepath.EvalSymlinks` on the deepest existing parent, rejoin the tail) —
the same symlink concern `unboundWorktreeDecision` handles with
`monitor.ResolvedProjectPath` (E-2002: `/var`→`/private/var`, `/tmp`→`/private/tmp`
on macOS). Without this, the containment test silently answers wrong for
symlinked roots.

### What is NOT recognized, and what happens

A target is **dynamic** when its word contains `$`, a backtick, `$(`, `<(`/`>(`,
or `~user`. A command is **opaque** when it hands work to a program whose writes
live in its own arguments or code.

| Case | Examples | Behaviour (recommended default) |
|---|---|---|
| Dynamic target | `> "$OUT"`, `> $(mktemp)`, `rm -rf "$WT"/x` | Static prefix rule: if the literal prefix before the first `$` is an absolute path (or `~/…`), judge the prefix's directory; otherwise allow. `$HOME`/`${HOME}` expanded from the hook's env (same user); nothing else expanded — the hook's env is not the shell's. |
| Glob target | `rm .endless/tmp/*.md` | Judge the directory of the static prefix (the part before the first `*?[`). |
| Command substitution as a command | `$(foo) > x` | Redirect still recognized; the substitution body is not inspected. |
| Nested interpreters | `python -c "open('f','w')…"`, `node -e`, `ruby -e`, `perl -e` (no `-i`), `awk '{print > "f"}'`, `osascript` | Not parsed; allowed. |
| Scripts / build tools | `./x.sh`, `just build`, `make`, `go build -o`, `npm i`, `uv …` | Not parsed; allowed (they write where they are designed to, and a build writing `bin/` in the worktree is the normal case). |
| Indirect deletion | `xargs rm`, `find … \| xargs …`, `parallel` | Target unknown; allowed. |
| Eval depth > 2 / malformed quoting | unterminated quote | Unparseable; allowed. |

Default posture: **fail open, silently**, on everything the gate cannot resolve
to a concrete path — matching `foreignLandedSuite`'s documented stance ("fails
open on every unanswerable question"). Whether some of these should warn or be
refused is an open question below.

## Failure-mode analysis

**False positives (a legitimate command blocked)** — the costly direction:
a gate that blocks normal work "is discovered on its first day and routed around
thereafter" (the `cmdPos` rationale). Sources and mitigations:

- *Mentions, not invocations*: `echo "use > file"`, commit messages, heredoc
  bodies documenting `sed -i`. Mitigated by the quote-aware lexer and
  `stripHeredocs`; operators inside quotes are words, not redirects.
- *Reads*: `cat f`, `grep`, `<` input redirects, `git diff > /dev/null`. Only
  output operators and listed verbs produce targets; `/dev/null`, `/dev/stdout`,
  `/dev/stderr`, `/dev/tty`, `/dev/fd/*` always exempt.
- *Temp/scratch*: `> /tmp/x`, `mktemp`, the Claude Code scratchpad under
  `/private/tmp/claude-*`, `$TMPDIR`. Exempt (see Decisions).
  Note E-1703 as planned would refuse a Write to these too; the exempt list must
  live in `writeTargetDecision` so both tool paths agree.
- *`git commit` and other git plumbing in a worktree*: shared `.git` lives in
  main. Handled by never treating `.git`/commit as a target.
- *Main-checkout writes the owner does on purpose* (verbs cache committed to
  main per CLAUDE.md, global config artifacts, `.endless/tmp` drafting): only
  refused when the session holds a claimed worktree (E-1703's boundary), and
  taskless sessions are untouched by default.
- *Endless's own subprocesses*: `endless task update …` writes the ledger and
  mirrors in main; excluded because only the literal command text is inspected
  and `endless` is not a recognized write verb.
- *mv/cp from outside into the worktree*: only the destination (and for `mv`,
  the sources being removed) are targets; `cp /main/x ./x` is allowed.

**False negatives (a write leaks through)** — accepted, bounded:
dynamic targets, interpreters, scripts, build tools, `xargs`, obfuscation. These
are exactly the forms nobody types *by accident*, and the gate targets accidents
(an agent `sed -i`-ing the main checkout by absolute path, `cat > ../../main/x`).
The residual gap is documented in the gate's doc comment, as `stripHeredocs`
documents its own.

**Default posture:** refuse only on a *recognized* write form with a
*resolved* target that `writeTargetDecision` refuses; allow everything else
silently. Precision over recall.

**Cost:** extraction is pure string work; the only I/O is one
`sessionOwnedWorktree` lookup (skipped entirely when no target was extracted)
and a `git rev-parse` per git-mutating command. Commands with no recognized
write form pay only the lexer.

## Implementation steps

1. Build E-1703's Write/Edit target gate (see "Merged from E-1703" above) on
   top of `writeScope`, `newWriteScope`, `writeTargetDecision` and
   `resolveWriteTarget`, and route `enforceClaimedWriteTarget`,
   `blockDocMirrorWriteIfApplicable` and `blockLandedSuiteEditIfApplicable`
   through the one decision.
2. `internal/hookcmd/bash_writes.go`: `lexShell`, `walkCommands`,
   `bashWriteTargets(cmd, cwd) []bashWrite` where
   `bashWrite{Path, Construct string; Dynamic bool}`.
3. Refactor `commitDir` onto `walkCommands`; keep `TestCommitDir` green.
4. `blockBashWriteTargetsIfApplicable(projectID, payload)` + call site.
5. Update the "reach another directory by passing an explicit absolute path"
   wording in `cdRedirect` / `enforceWorktreeGate` (E-1703 already scopes it to
   reads) so it also covers shell writes.
6. Guide: one line in the hook-gates section of the guide saying shell writes
   are judged by the same rule as Write/Edit, recognized forms only; run
   `/regenerate-guide` if a command reference changes (none expected).

## Tests

Go unit tests (`internal/hookcmd/bash_writes_test.go`), table-driven:

- `TestLexShell` — quotes, escapes, `&>`, `>|`, `2>&1`, `|&`, heredoc operator
  lines, unterminated quotes.
- `TestBashWriteTargets` — table of `{cmd, cwd} → []{path, construct, dynamic}`.
  At least: each row of the recognized-forms table; `cd X && sed -i … f`
  (dir tracking); `(cd X; > f)` (subshell scoping); `git -C X restore .`;
  `bash -c "echo > f"`; `echo "> f"` → none; `cat <<EOF\n> f\nEOF` → none;
  `cat > f <<'EOF' …` → `f`; `cmd 2>&1 | tee log` → `log`; `> /dev/null` → none;
  `> "$OUT"` → dynamic; `rm ~/x/*.go` → prefix dir; `git commit -m "x > y"` →
  none; `endless task update … --plan-file .endless/tmp/p.md` → none;
  `python -c "open('f','w')"` → none.
- `TestWriteTargetDecision` — `{scope, target} → block/allow`: in own
  worktree → allow; main checkout → block; other task's worktree → block;
  `/tmp`, `/private/tmp/claude-*`, `$TMPDIR` → allow; `/dev/null` → allow;
  doc-mirror path in worktree → block with mirror message; landed foreign suite
  → block; no claimed task / terminal task / lock owned by other live session →
  allow (E-1703 boundary); symlinked root (macOS `/var`) resolves correctly.
- `TestBashWriteGateMatchesWriteGate` — the equivalence property: for a table
  of targets, `writeTargetDecision` via a Write payload and via
  `echo x > <target>` produce the same block/allow and the same message body.
  This is the test that keeps "the same decision" true over time.

Verify suite `.endless/tasks/e-940/verify.sh` (modeled on e-1202's: sources
`_harness.sh`, builds `bin/endless-go`, drives `endless-go hook claude` with
synthetic PreToolUse payloads for a claimed session sitting in its worktree):
- `sed -i 's/a/b/' <main>/README.md` → exit 2, message names the construct.
- `echo x > <main>/foo` and `cat > <main>/foo <<EOF` → blocked.
- `echo x > ./foo` (in worktree), `> /tmp/x`, `> /dev/null` → allowed.
- `git commit -m "a > b"` in the worktree → not blocked by this gate.
- `echo "sed -i x <main>/f"` → allowed (mention, not invocation).
- A Write to the same main path → blocked with the same body (equivalence).
- `go test ./internal/hookcmd/ -run 'BashWrite|LexShell|WriteTargetDecision'` passes.
