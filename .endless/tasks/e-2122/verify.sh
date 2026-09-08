#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2122 and records what was true when E-2122
# landed. Edit it only if you ARE E-2122. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2122 verification — "Report what git actually said when worktree land's
# rebase fails".
#
# WHAT LANDED
#   `worktree land` rebases in two places (Step 3.7 orphan replay, Step 4 main
#   rebase). Both caught CalledProcessError and called _rebase_conflict_message
#   unconditionally, so ANY non-zero exit from `git rebase` was reported as a
#   content conflict. `git rebase` also exits non-zero when it refuses to START
#   — a dirty worktree, a rebase already in progress, an operational failure —
#   and none of those produce unmerged paths. The report asserted a conflict
#   that never happened, printed "Conflicting files: (none reported)", and
#   offered recovery candidates for a cause it had never established. Git's
#   stderr, which names the real reason, sat in CalledProcessError and was
#   discarded.
#
#   A new dispatcher, _rebase_failure_message, now decides from FACTS rather
#   than from the exit code, and git's stderr is carried into every outcome.
#
# THE CLAIMS
#   C1  A NON-CONFLICT FAILURE IS NOT CALLED A CONFLICT. The observed shape —
#       rebase refuses to start, zero unmerged paths — now reports "rebase
#       failed", quotes git verbatim, and offers NO recovery candidates. The
#       string "(none reported)" that was the tell cannot appear.
#   C2  GIT'S WORDS SURVIVE. The stderr that was discarded is in the message.
#       In the non-conflict case it IS the answer; in the conflict case it is
#       context.
#   C3  A REBASE LAND DID NOT START IS ITS OWN REPORT, AND IS LEFT RUNNING.
#       Its unmerged paths and REBASE_HEAD belong to another operation. Land
#       no longer reads them as this failure's cause, and no longer runs
#       `git rebase --abort` on it — which destroyed a user's in-progress
#       conflict resolution.
#   C4  REBASE_HEAD IS GATED. It is a plain ref, not cleared by every path, so
#       it is consulted only while a rebase is actually in progress. No
#       "failed to replay" line on a rebase that never started.
#   C5  REAL CONFLICTS ARE UNCHANGED. Files named, failing commit named,
#       candidates offered — the E-1417 behaviour this task must not regress.
#   C6  BOTH CALL SITES ARE FIXED. Step 3.7 had the same shape as Step 4;
#       fixing one would leave the other lying.
#
# ISOLATION
#   Every git section builds a throwaway repo under a mktemp dir. Nothing
#   touches this worktree's git state, the main checkout, or any database.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

WT="$(git rev-parse --show-toplevel)" || setup_error "not in a git repo"
cd "${WT}" || setup_error "cannot cd to ${WT}"

TMP="$(mktemp -d)" || setup_error "cannot make a temp dir"
trap 'rm -rf "${TMP}"' EXIT

SRC="${WT}/src/endless/worktree_cmd.py"
[[ -f "${SRC}" ]] || setup_error "missing ${SRC}"

# ── Fail-fast: this task's own unit tests ─────────────────────────────────────
section "Unit tests (fail fast)"

if uv run pytest -q \
        tests/test_worktree_land_rebase_failure.py \
        tests/test_worktree_land_conflict_msg.py \
        >"${TMP}/py.log" 2>&1; then
    report_pass "pytest — rebase failure reporting, and the conflict report it must not regress"
else
    report_fail "pytest tests/test_worktree_land_rebase_failure.py + test_worktree_land_conflict_msg.py" \
        "exit 0" "$(tail -30 "${TMP}/py.log")"
    summary
fi

# ── The driver: builds real repos and reports as the land call sites do ───────
cat >"${TMP}/drive.py" <<'PY'
import json, subprocess, sys, tempfile
from pathlib import Path
sys.path.insert(0, sys.argv[1])
from endless.worktree_cmd import _rebase_failure_message, _rebase_in_progress

def sh(c, d, check=True):
    return subprocess.run(c, cwd=str(d), capture_output=True, text=True, check=check)

def repo(d):
    d.mkdir(parents=True, exist_ok=True)
    sh(["git", "init", "-q", "-b", "main"], d)
    sh(["git", "config", "user.email", "t@t.t"], d)
    sh(["git", "config", "user.name", "t"], d)
    (d / "f.txt").write_text("base\n")
    sh(["git", "add", "-A"], d); sh(["git", "commit", "-qm", "base"], d)
    return d

def land(d):
    """Exactly the shape both land call sites use."""
    pre = _rebase_in_progress(d)
    try:
        subprocess.run(["git", "rebase", "main"], cwd=str(d),
                       check=True, capture_output=True, text=True)
        return {"failed": False, "msg": "", "pre": pre, "still_running": _rebase_in_progress(d)}
    except subprocess.CalledProcessError as e:
        msg = _rebase_failure_message(d, "main",
                                      phase="rebasing your branch onto main",
                                      stderr=e.stderr, pre_existing=pre)
        if not pre:
            subprocess.run(["git", "rebase", "--abort"], cwd=str(d),
                           capture_output=True, text=True)
        return {"failed": True, "msg": msg, "pre": pre,
                "still_running": _rebase_in_progress(d)}

root = Path(tempfile.mkdtemp())
out = {}

# A — rebase refuses to start: dirty worktree. Zero unmerged paths.
a = repo(root / "a")
sh(["git", "checkout", "-qb", "feature"], a)
(a / "mine.txt").write_text("m\n"); sh(["git", "add", "-A"], a)
sh(["git", "commit", "-qm", "my real work"], a)
sh(["git", "checkout", "-q", "main"], a)
(a / "other.txt").write_text("o\n"); sh(["git", "add", "-A"], a)
sh(["git", "commit", "-qm", "main moved on"], a)
sh(["git", "checkout", "-q", "feature"], a)
(a / "mine.txt").write_text("uncommitted edit\n")
out["dirty"] = land(a)

# B — a rebase already in progress, started by something else.
b = repo(root / "b")
sh(["git", "checkout", "-qb", "other"], b)
(b / "f.txt").write_text("other\n"); sh(["git", "add", "-A"], b)
sh(["git", "commit", "-qm", "UNRELATED COMMIT from another operation"], b)
sh(["git", "checkout", "-q", "main"], b)
(b / "f.txt").write_text("main\n"); sh(["git", "add", "-A"], b)
sh(["git", "commit", "-qm", "main moved on"], b)
sh(["git", "checkout", "-q", "other"], b)
sh(["git", "rebase", "main"], b, check=False)
out["preexisting"] = land(b)
sh(["git", "rebase", "--abort"], b, check=False)

# C — a genuine content conflict.
c = repo(root / "c")
sh(["git", "checkout", "-qb", "feature"], c)
(c / "f.txt").write_text("branch\n"); sh(["git", "add", "-A"], c)
sh(["git", "commit", "-qm", "my real work"], c)
sh(["git", "checkout", "-q", "main"], c)
(c / "f.txt").write_text("main\n"); sh(["git", "add", "-A"], c)
sh(["git", "commit", "-qm", "main moved on"], c)
sh(["git", "checkout", "-q", "feature"], c)
out["conflict"] = land(c)

print(json.dumps(out))
PY

if ! uv run python "${TMP}/drive.py" "${WT}/src" >"${TMP}/out.json" 2>"${TMP}/drive.err"; then
    setup_error "the git driver failed: $(tail -20 "${TMP}/drive.err")"
fi

j() { uv run python -c 'import json,sys; print(json.load(open(sys.argv[1]))[sys.argv[2]][sys.argv[3]])' "${TMP}/out.json" "$1" "$2"; }

DIRTY_MSG="$(j dirty msg)"
PRE_MSG="$(j preexisting msg)"
CONF_MSG="$(j conflict msg)"

# ── C1 / C2 ───────────────────────────────────────────────────────────────────
section "C1/C2 — a rebase that refused to start is a failure, not a conflict"

assert_eq "the rebase did fail" "True" "$(j dirty failed)"
assert_not_contains "it is not called a conflict" "rebase conflict" "${DIRTY_MSG}"
assert_not_contains "the empty-file-list tell is gone" "(none reported)" "${DIRTY_MSG}"
assert_not_contains "no conflicting-files block at all" "Conflicting files" "${DIRTY_MSG}"
assert_contains "it is named as a failure" "rebase failed while rebasing your branch onto main" "${DIRTY_MSG}"
assert_contains "and says so explicitly" "NOT a content conflict" "${DIRTY_MSG}"
assert_contains "git's stderr is quoted" "git said:" "${DIRTY_MSG}"
assert_contains "git's actual reason survives" "unstaged changes" "${DIRTY_MSG}"
assert_not_contains "no candidates for an unestablished cause" \
    "the wrong recovery can duplicate or lose work" "${DIRTY_MSG}"
assert_not_contains "no conflict-resolution recipe" "git rebase --continue" "${DIRTY_MSG}"
assert_not_contains "no reset recipe" "reset --hard main" "${DIRTY_MSG}"

# ── C3 / C4 ───────────────────────────────────────────────────────────────────
section "C3/C4 — a rebase land did not start: named, not blamed, not aborted"

assert_eq "detected as pre-existing" "True" "$(j preexisting pre)"
assert_contains "reported as already in progress" "already in progress" "${PRE_MSG}"
assert_contains "land says it did not start one" "land did not begin one" "${PRE_MSG}"
assert_contains "git's stderr is quoted here too" "git said:" "${PRE_MSG}"
assert_not_contains "the other operation's commit is not blamed" \
    "UNRELATED COMMIT" "${PRE_MSG}"
assert_not_contains "REBASE_HEAD is not read when land started nothing" \
    "failed to replay" "${PRE_MSG}"
assert_not_contains "and it is not called a conflict" "rebase conflict" "${PRE_MSG}"
assert_eq "the other rebase is still running — land did not abort it" \
    "True" "$(j preexisting still_running)"
assert_contains "the message says so" "left exactly as it was" "${PRE_MSG}"

# ── C5 ────────────────────────────────────────────────────────────────────────
section "C5 — a genuine conflict still reports as one"

assert_contains "still a conflict" "rebase conflict while rebasing your branch onto main" "${CONF_MSG}"
assert_contains "the conflicting file is named" "f.txt" "${CONF_MSG}"
assert_not_contains "with a real file list, not the tell" "(none reported)" "${CONF_MSG}"
assert_contains "the failing commit is named" "failed to replay" "${CONF_MSG}"
assert_contains "and it is the user's commit" "my real work" "${CONF_MSG}"
assert_contains "candidates are still offered" \
    "the wrong recovery can duplicate or lose work" "${CONF_MSG}"
assert_contains "git's words ride along as context" "git said:" "${CONF_MSG}"

# ── C6 ────────────────────────────────────────────────────────────────────────
section "C6 — both rebase call sites go through the dispatcher"

DISPATCH_CALLS="$(grep -c '_rebase_failure_message(' "${SRC}")"
assert_eq "definition + Step 3.7 + Step 4 = 3 mentions" "3" "${DISPATCH_CALLS}"

assert_eq "no bare CalledProcessError left on either rebase handler" "0" \
    "$(grep -c 'except subprocess.CalledProcessError:$' <(grep -A1 '_git_run(\["rebase"' "${SRC}") || true)"

assert_eq "the abort is guarded at both sites" "2" \
    "$(grep -c 'if not rebase_was_running:' "${SRC}")"
assert_eq "and the pre-state is captured at both sites" "2" \
    "$(grep -c 'rebase_was_running = _rebase_in_progress(' "${SRC}")"

assert_contains "stderr is threaded from the exception, not dropped" \
    "stderr=e.stderr" "$(cat "${SRC}")"

summary
