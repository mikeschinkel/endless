#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2213 and records what was true when E-2213
# landed. Edit it only if you ARE E-2213. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2213 — remove two destructive escapes, reroute three warnings.
#
# What this proves, in order:
#
#   0. The task's own tests pass, fail-fast: the orphan-branch refusals, the
#      three warning sites, and the faults catalog against docs/errors.md.
#   A. The two orphan-branch refusals guarding real work offer `git branch -D`
#      to a person and never to an agent — pinned in both directions.
#   B. Each of the three warnings reaches `endless errors show` under its new
#      code, carrying its remedy, as one incident however often it fires.
#
# Run it:
#   endless task verify E-2213

source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

# ENDLESS_VERIFY_DIR is .endless/tasks/e-2213, so the worktree root is three up.
WT="$(cd "${ENDLESS_VERIFY_DIR}/../../.." && pwd)"
[[ -d "${WT}/internal/faults" ]] || setup_error "not a worktree of this project: ${WT}"

TMP="$(mktemp -d)"
trap 'rm -rf "${TMP}"' EXIT

# ─── 0. the task's own tests, fail-fast ─────────────────────────────────────

section "The task's own tests"

faults_out="$(cd "${WT}" && go test ./internal/faults/ 2>&1)" || {
    printf '%s\n' "${faults_out}"
    setup_error "the faults catalog and docs/errors.md disagree"
}
report_pass "internal/faults: WARN-0026..0028 registered, remedies match docs/errors.md"

py_out="$(cd "${WT}" && uv run pytest -q \
    tests/test_worktree_orphan_branch.py \
    tests/test_worktree_create.py \
    tests/test_worktree_land_post_land_script.py \
    tests/test_refusal_sites.py 2>&1)" || {
    printf '%s\n' "${py_out}"
    setup_error "the task's Python tests fail; nothing below would mean anything"
}
report_pass "orphan-branch, worktree-create, post-land and refusal-site tests pass"

# ─── A. branch -D is a person's remedy, never an agent's ────────────────────

section "A. The orphan-branch refusals offer branch -D to a person only"

# Each builder returns (text, human_remedy). Rendered through the real
# refusal, once per audience, so what is compared is what Click prints.
render() {
    (cd "${WT}" && uv run python - "$1" <<'PY'
import sys
from pathlib import Path
from endless import agent_help as a, worktree_cmd as w
which = sys.argv[1]
root = Path("/repo")
if which.startswith("real"):
    text, hr = w._orphan_real_work_msg(9, "task/9", "main", ["src/x.py"], root)
else:
    text, hr = w._orphan_mirror_mismatch_msg(9, "task/9", ["p.md"], root)
a.agent_facing = (lambda: True) if which.endswith("agent") else (lambda: False)
print(a.report("s", "d", text=text, human_remedy=hr).format_message())
PY
    )
}

for kind in real mirror; do
    human="$(render "${kind}-human")"
    agent="$(render "${kind}-agent")"
    assert_contains "${kind}: a person still reads the discard command" \
        "branch -D task/9" "${human}"
    assert_not_contains "${kind}: an agent's copy carries no branch -D" \
        "branch -D" "${agent}"
    assert_contains "${kind}: an agent is told the decision is the user's" \
        "This needs the user" "${agent}"
done

# ─── B. three warnings on the errors channel ────────────────────────────────

section "B. Each warning reaches endless errors show under its code"

# A binary built from this tree, first on PATH — warn.record shells out to
# `endless-go` by name — and a throwaway --db-dir, so nothing reaches either the
# real record or this worktree's sandbox.
go build -o "${TMP}/bin/endless-go" "${WT}/cmd/endless-go" \
    || setup_error "cannot build cmd/endless-go from this tree"
DBDIR="${TMP}/db"
mkdir -p "${DBDIR}"
EN=("${TMP}/bin/endless-go" --db-dir "${DBDIR}")

fire() {
    (cd "${WT}" && PATH="${TMP}/bin:${PATH}" uv run python - "${TMP}" <<'PY' 2>/dev/null
import sys
from pathlib import Path
from endless import config, worktree_cmd as w
T = Path(sys.argv[1])
config.RESOLVED_CONFIG_DIR = T / "db"
proj, wt, main = T / "proj", T / "wt", T / "main"
for d in (proj / ".endless/hooks", wt, main / ".endless/hooks/post-land"):
    d.mkdir(parents=True, exist_ok=True)
(proj / ".endless/hooks/post-worktree-create.sh").write_text("#!/bin/sh\n")
(main / ".endless/hooks/post-land/e-4242.sh").write_text("#!/bin/sh\n")
w._run_post_worktree_create_hook(proj, wt)
w._run_post_land_script(wt, main, "E-4242", "sha", "main")
w._warn_if_companion_disagrees(Path("/x/.endless/worktrees/e-100"),
                               {"task_id": "E-1186"})
PY
    )
}

# Twice: a fingerprinted warning is one incident with a rising count, not a
# new row per command.
fire || setup_error "firing the three warning sites failed"
fire || setup_error "firing the three warning sites a second time failed"

listing="$("${EN[@]}" errors list --all-projects 2>&1)"
assert_contains "three incidents, not six" "3 errors across every project" "${listing}"

check_code() {
    local code="$1" needle="$2" remedy="$3"
    local id
    id="$(awk -v c="${code}" '$2 == c {print $1}' <<<"${listing}")"
    if [[ -z "${id}" ]]; then
        report_fail "${code} reaches the errors channel" "not in: ${listing}"
        return
    fi
    local shown
    shown="$("${EN[@]}" errors show "${id}" 2>&1)"
    assert_contains "${code}: errors show names the code" "${code}" "${shown}"
    assert_contains "${code}: errors show carries the summary" "${needle}" "${shown}"
    assert_contains "${code}: errors show carries the remedy" "${remedy}" "${shown}"
    assert_eq "${code}: fired twice, recorded once with count 2" "2" \
        "$(awk -v c="${code}" '$2 == c {print $4}' <<<"${listing}")"
}

check_code WARN-0026 "post-worktree-create hook" "chmod +x"
check_code WARN-0027 "E-4242's post-land script" "update-index --chmod=+x"
check_code WARN-0028 "task_id=E-1186 disagrees" "legacy \`task_id\` key"

summary
