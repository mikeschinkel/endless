#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2184 and records what was true when E-2184
# landed. Edit it only if you ARE E-2184. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2184 verification — land refuses when main gained migrations since the
# branch forked.
#
# THE CLAIMS
#   C1  The built-in check refuses when the branch AND the base both added
#       files under a declared migrations dir since the merge-base, proposes
#       each branch file's new number, and clears once the branch is rebased
#       and renumbered. One side alone, or no declaration, never refuses.
#   C2  .endless/hooks/pre-land.sh can veto with its own summary and block; a
#       non-executable hook refuses rather than being skipped.
#   C3  land renders both through one renderer — a plain line, then a marked
#       block — bracketed for an agent, and fails CLOSED when the gate cannot
#       answer. The gate runs before anything rebases the branch.
#   C4  Endless declares internal/schema/migrations, and a Go test over that
#       directory passes today, and fails on a duplicate version, a gap, an
#       unregistered .go step, a registration with no file, and a
#       NewGoMigration literal that disagrees with its filename.
#
# ISOLATION: unit tests (the guard's faults are planted in a Go test's temp
# dir) and throwaway git repos under a temp dir. Nothing reads or writes a real
# database.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

ROOT=$(git rev-parse --show-toplevel 2>/dev/null) || setup_error "not in a git tree"
cd "${ROOT}" || setup_error "cannot cd to ${ROOT}"
TMP=$(mktemp -d) || setup_error "mktemp failed"
trap 'rm -rf "${TMP}"' EXIT

section "A. Fail-fast: this task's unit tests"
if out=$(go test -count=1 ./internal/landgate/ ./internal/schema/migrations/ \
        ./internal/config/ ./internal/worktreecmd/ 2>&1); then
    report_pass "go test landgate, schema/migrations, config, worktreecmd"
else
    report_fail "go test landgate, schema/migrations, config, worktreecmd" "exit 0" "${out}"
    summary
fi
if out=$(uv run --quiet pytest -q tests/test_worktree_land_migration_gate.py \
        tests/test_worktree_land_schema_apply.py \
        tests/test_worktree_land_migrate_exec.py 2>&1); then
    report_pass "pytest land gate seam + the land tests that stub it"
else
    report_fail "pytest land gate seam + the land tests that stub it" "exit 0" "${out}"
    summary
fi

go build -o "${TMP}/endless-go" ./cmd/endless-go 2>"${TMP}/build.txt" \
    || setup_error "cannot build endless-go: $(tail -5 "${TMP}/build.txt")"
GO_BIN="${TMP}/endless-go"

# ── scratch repos ──────────────────────────────────────────────────
g() { git -C "$1" "${@:2}" >/dev/null 2>&1 || setup_error "git ${*:2} in $1"; }
put() { mkdir -p "$(dirname "$1/$2")"; printf '%s\n' "$3" >"$1/$2"; }
commit() { g "$1" add -A; g "$1" commit -q -m "$2"; }

# fresh <name> <config-json>: a main checkout plus a task worktree cut from it.
fresh() {
    local d="${TMP}/$1"
    MAIN="${d}/main"; WT="${d}/wt"
    mkdir -p "${d}"
    g "${d}" init -q -b main "${MAIN}"
    g "${MAIN}" config user.email t@t.t
    g "${MAIN}" config user.name t
    put "${MAIN}" .endless/config.json "$2"
    put "${MAIN}" db/migrations/00001_base.sql "-- base"
    put "${MAIN}" db/migrations/00002_more.sql "-- more"
    commit "${MAIN}" base
    g "${MAIN}" worktree add -q -b task/1 "${WT}"
}
gate() {
    "${GO_BIN}" worktree land-gate --project "${MAIN}" --worktree "${WT}" \
        --base main --task E-1 2>&1
}
field() { printf '%s' "$1" | python3 -c "import json,sys;v=json.load(sys.stdin);print(eval(sys.argv[1]))" "$2"; }
DIRS='{"name":"p","migrations":{"dirs":["db/migrations"]}}'

section "B. C1 — the built-in migration check, through the built binary"
fresh collide "${DIRS}"
put "${WT}" db/migrations/00003_branch.sql "-- branch"; commit "${WT}" branch
put "${MAIN}" db/migrations/00003_main.sql "-- main";   commit "${MAIN}" main
out=$(gate)
assert_eq "both sides added migrations → refused" "True" "$(field "${out}" 'v["refused"]')"
assert_eq "names what landed on main" "['db/migrations/00003_main.sql']" "$(field "${out}" 'v["landed"]')"
assert_eq "proposes the branch file's new number" "db/migrations/00004_branch.sql" \
    "$(field "${out}" 'v["branch"][0]["to"]')"
summary_line=$(field "${out}" 'v["summary"]')
assert_eq "summary is one line" "1" "$(printf '%s\n' "${summary_line}" | wc -l | tr -d ' ')"
block=$(field "${out}" 'v["block"]')
for want in "git rebase main" "endless sandbox reset" "endless task verify E-1" "endless worktree land E-1"; do
    assert_contains "block tells the agent: ${want}" "${want}" "${block}"
done

g "${WT}" rebase -q main
g "${WT}" mv db/migrations/00003_branch.sql db/migrations/00004_branch.sql
commit "${WT}" renumber
assert_eq "after rebase + renumber → clear" "False" "$(field "$(gate)" 'v["refused"]')"

fresh branchonly "${DIRS}"
put "${WT}" db/migrations/00003_branch.sql "x"; commit "${WT}" branch
put "${MAIN}" README.md "x"; commit "${MAIN}" unrelated
assert_eq "only the branch added a migration → clear" "False" "$(field "$(gate)" 'v["refused"]')"

fresh undeclared '{"name":"p"}'
put "${WT}" db/migrations/00003_branch.sql "x"; commit "${WT}" branch
put "${MAIN}" db/migrations/00003_main.sql "x"; commit "${MAIN}" main
assert_eq "no migrations declaration → no check" "False" "$(field "$(gate)" 'v["refused"]')"

section "C. C2 — the pre-land hook"
fresh hook '{"name":"p"}'
put "${MAIN}" .endless/hooks/pre-land.sh '#!/bin/sh
echo "cannot land $ENDLESS_TASK_ID onto $2: schema freeze"
echo "Wait for the freeze to lift."
exit 3'
chmod +x "${MAIN}/.endless/hooks/pre-land.sh"
out=$(gate)
assert_eq "hook veto → refused by the hook" "hook" "$(field "${out}" 'v["source"]')"
assert_eq "hook's first line is the summary" "cannot land E-1 onto main: schema freeze" \
    "$(field "${out}" 'v["summary"]')"
assert_contains "hook's remaining lines are the block" "Wait for the freeze to lift." \
    "$(field "${out}" 'v["block"]')"
chmod -x "${MAIN}/.endless/hooks/pre-land.sh"
out=$(gate)
assert_eq "non-executable hook → refused, not skipped" "True" "$(field "${out}" 'v["refused"]')"
assert_contains "and says how to fix it" "chmod +x" "$(field "${out}" 'v["block"]')"

section "D. C3 — the land wiring"
wiring=$(uv run --quiet python - <<'EOF'
import inspect, re
from endless import worktree_cmd as w
src = inspect.getsource(w.land_worktree)
gate = src.index("_refuse_if_land_gated(main_root, worktree_path, base_branch, canonical)\n\n        # Step 3.7")
print("gate-before-3.7" if gate < src.index("_drop_orphan_amendable_commits(") else "gate-after-3.7")
print("gate-before-rebase" if gate < src.index('["rebase", base_branch]') else "gate-after-rebase")
print("dry-run-gated" if src.index("_refuse_if_land_gated") < src.index("_rehearse_land_rebase(") else "dry-run-ungated")
EOF
)
assert_contains "the gate runs before Step 3.7's orphan-drop rebase" "gate-before-3.7" "${wiring}"
assert_contains "the gate runs before Step 4's rebase" "gate-before-rebase" "${wiring}"
assert_contains "--dry-run runs the same gate" "dry-run-gated" "${wiring}"

section "E. C4 — Endless's own migrations"
assert_eq "Endless declares internal/schema/migrations" "['internal/schema/migrations']" \
    "$(python3 -c 'import json;print(json.load(open(".endless/config.json"))["migrations"]["dirs"])')"

for t in TestVersions_UniqueContiguousAndConsistent TestVersions_GuardCatchesEachFault; do
    if out=$(go test -count=1 -v -run "^${t}\$" ./internal/schema/migrations/ 2>&1) \
            && [[ "${out}" == *"--- PASS: ${t}"* ]]; then
        report_pass "${t}"
    else
        report_fail "${t}" "--- PASS" "$(printf '%s' "${out}" | tail -20)"
    fi
done

summary
