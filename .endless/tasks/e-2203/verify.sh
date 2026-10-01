#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2203 and records what was true when E-2203
# landed. Edit it only if you ARE E-2203. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2203 verification — the rater job, agent-forced ratings on plan attach,
# and approve's refusal wording.
#
#   endless task verify E-2203
#
# A throwaway project in the runner's temp HOME, driven by THIS worktree's
# Python CLI and a freshly built endless-go. A fake `claude` stands in for the
# model, so no check spends a token or depends on a model's judgement; its
# reply comes from $STUB_REPLY and it logs every prompt it is handed.
#
# Exit 0 on all-passed, 1 on any failure, 2 on a setup problem.

source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

WT="$(git rev-parse --show-toplevel)" || setup_error "not in a git repo"
cd "${WT}" || setup_error "cannot cd to ${WT}"

# -P: macOS's mktemp hands back /var/..., a symlink to /private/var/....
TMP="$(cd "$(mktemp -d)" && pwd -P)" || setup_error "could not create a temp dir"
trap 'rm -rf "${TMP}"' EXIT

# ── 0. unit gate (fail fast) ────────────────────────────────────────────────
section "0. Unit suites (fail fast)"

if go test ./internal/raterjob/ ./internal/faults/ ./internal/schema/... \
        ./internal/templatecmd/ >"${TMP}/go.log" 2>&1 \
   && go test ./internal/monitor/ -run 'Rater|Unrated' >>"${TMP}/go.log" 2>&1; then
    report_pass "go test — rater job, claim, queue and context reads, fault catalog, migration"
else
    report_fail "go test (E-2203 packages)" "exit 0" "$(tail -30 "${TMP}/go.log")"
    summary
fi

if uv run pytest -q tests/test_rater.py tests/test_agent_plan_attach_ratings.py \
        tests/test_plan_auto_promote.py tests/test_submit_approve.py \
        tests/test_status_change_audience.py >"${TMP}/py.log" 2>&1; then
    report_pass "pytest — rater parse/unset-axes/fail-open/claim, agent plan-attach refusal, approve wording"
else
    report_fail "pytest (E-2203 tests)" "exit 0" "$(tail -30 "${TMP}/py.log")"
    summary
fi

mkdir -p "${TMP}/bin"
if go build -o "${TMP}/bin/endless-go" ./cmd/endless-go >"${TMP}/build.log" 2>&1; then
    report_pass "go build ./cmd/endless-go (the binary every section drives)"
else
    report_fail "go build ./cmd/endless-go" "exit 0" "$(tail -30 "${TMP}/build.log")"
    summary
fi

# ── fixture ─────────────────────────────────────────────────────────────────
REPO="${TMP}/repo"
STUB="${TMP}/stub"
PROMPTS="${TMP}/prompts"
export XDG_CONFIG_HOME="${HOME}/.config"
export XDG_CACHE_HOME="${TMP}/cache"
unset TMUX TMUX_PANE ENDLESS_SESSION_ID ENDLESS_NO_JOBS ENDLESS_NO_HOOKS 2>/dev/null || true
# Hermetic: whatever Claude session (or none) launched this suite must not
# decide whether a command below runs as an agent. Sections that need an agent
# set one explicitly.
for v in $(env | sed -n 's/^\(CLAUDE[A-Z_]*\)=.*/\1/p'); do unset "${v}"; done
mkdir -p "${STUB}" "${REPO}" "${XDG_CONFIG_HOME}/endless" "${XDG_CACHE_HOME}" "${PROMPTS}"

# The fake model. The prompt is the last argument.
cat >"${STUB}/claude" <<EOF
#!/usr/bin/env bash
printf '%s' "\${!#}" >"${PROMPTS}/\$(date +%s%N)-\$\$"
printf '%b\n' "\${STUB_REPLY:-COMPLEXITY: medium\nRISK: low}"
exit "\${STUB_EXIT:-0}"
EOF
# The job shells `endless`; it must be THIS worktree's CLI.
cat >"${STUB}/endless" <<EOF
#!/bin/sh
exec uv run --quiet --project "${WT}" endless "\$@"
EOF
chmod +x "${STUB}/claude" "${STUB}/endless"
export PATH="${STUB}:${TMP}/bin:${PATH}"

# --no-session: the runner's isolated HOME has no Claude session to attribute a
# write to, so every mutation is recorded as the system's.
E()     { ( cd "${REPO}" && endless --no-session "$@" 2>&1 ); }
AGENT() { ( cd "${REPO}" && CLAUDE_CODE_ENTRYPOINT=cli endless --no-session "$@" 2>&1 ); }
Q()     { E sql "$1" --tsv 2>/dev/null; }
W()     { E sql "$1" --write >/dev/null 2>&1 || setup_error "sql failed: $1"; }
JOBS()  { ( cd "${REPO}" && endless jobs run --job rater 2>&1 ); }
prompts() { ls "${PROMPTS}" | wc -l | tr -d ' '; }
ratings_of() {
    Q "SELECT COALESCE((SELECT slug FROM complexity_levels WHERE id=complexity_id),'-')
              || '/' || COALESCE((SELECT slug FROM risk_levels WHERE id=risk_id),'-')
         FROM tasks WHERE id=$1"
}
status_of() { Q "SELECT status FROM tasks WHERE id=$1"; }

git -C "${REPO}" init -q
git -C "${REPO}" symbolic-ref HEAD refs/heads/main
git -C "${REPO}" config user.email verify@test
git -C "${REPO}" config user.name verify
git -C "${REPO}" commit -q --allow-empty -m "initial commit"
E project register "${REPO}" --name probe --label Probe --desc d --lang Go --status active >/dev/null 2>&1
PROJ="$(Q "SELECT id FROM projects WHERE name='probe'")"
[[ -n "${PROJ}" ]] || setup_error "project did not register"
git -C "${REPO}" add -A >/dev/null 2>&1
git -C "${REPO}" commit -q -m "register" >/dev/null 2>&1 || true

printf '# Plan\n\nRename the --frob flag to --frobnicate.\n' >"${TMP}/plan.md"

# A human files a task with a plan: no ratings asked for, and it is submitted.
# id_from pulls E-NNNN from `task add`'s output.
id_from() { grep -oE 'E-[0-9]+' <<<"$1" | head -1 | tr -d 'E-'; }

# ── A. the live path ────────────────────────────────────────────────────────
section "A. A human-filed task with a plan is rated by one jobs run"

out="$(E task add "Rename the frob flag" --description "Rename the frob flag to frobnicate." --plan-file "${TMP}/plan.md")"
H="$(id_from "${out}")"
[[ -n "${H}" ]] || setup_error "human task add failed: ${out}"
assert_eq "the human's filing is not refused, and lands submitted" "submitted" "$(status_of "${H}")"
assert_eq "it starts unrated" "-/-" "$(ratings_of "${H}")"

out="$(JOBS)"
assert_eq "one jobs run rated both axes from the model's reply" "medium/low" "$(ratings_of "${H}")"
assert_eq "rating moved no status" "submitted" "$(status_of "${H}")"
assert_contains "the run's note is the rater's tally" "rated 1 of 1" "$(E jobs list)"
prompt="$(cat "${PROMPTS}"/* 2>/dev/null)"
assert_contains "the model was shown the plan itself" "Rename the --frob flag to --frobnicate." "${prompt}"
assert_contains "and asked for the two rating lines" "COMPLEXITY: <low|medium|high>" "${prompt}"
ev="$(grep -h "\"id\":\"${H}\"" "${REPO}"/.endless/db-ledger/*.jsonl 2>/dev/null | grep '"task.fields_updated"' | tail -1)"
assert_contains "the rating event is the triager's (the class of job)" '"kind":"triager"' "${ev}"
assert_contains "and names the rater job as its provenance" '"job":"rater"' "${ev}"

n="$(prompts)"
JOBS >/dev/null
assert_eq "a rated task is never re-selected: the next run makes no call" "${n}" "$(prompts)"

# ── B. unset axes only ──────────────────────────────────────────────────────
section "B. Only an unset axis is written"

out="$(E task add "Rename the gizmo flag" --description "Rename the gizmo flag." --plan-file "${TMP}/plan.md" --complexity high)"
B="$(id_from "${out}")"
E jobs retry rater >/dev/null
STUB_REPLY='COMPLEXITY: low\nRISK: high' JOBS >/dev/null
assert_eq "complexity a person gave survives; risk comes from the rater" "high/high" "$(ratings_of "${B}")"

# ── C. fail-open ────────────────────────────────────────────────────────────
section "C. No verdict leaves the task unrated and records a fault"

out="$(E task add "Rename the widget flag" --description "Rename the widget flag." --plan-file "${TMP}/plan.md")"
C="$(id_from "${out}")"
E jobs retry rater >/dev/null
out="$(STUB_REPLY='I would rather not say.' JOBS)"
assert_eq "nothing was written" "-/-" "$(ratings_of "${C}")"
assert_eq "the task keeps its status" "submitted" "$(status_of "${C}")"
assert_not_contains "the job itself did not fail (fail-open)" "FAIL" "$(E jobs list | grep -E '^ *rater')"
assert_contains "a WARN-0029 incident names the task" "rater left E-${C} unrated" "$(E errors list)"

out="$(E rater run --task "E-${C}" 2>&1)"; rc=$?
assert_eq "a later run with a usable reply rates it" "medium/low" "$(ratings_of "${C}")"

# ── D. the claim ────────────────────────────────────────────────────────────
section "D. A live claim on a task stops a second run before the model call"

out="$(E task add "Rename the sprocket flag" --description "Rename the sprocket flag." --plan-file "${TMP}/plan.md")"
D="$(id_from "${out}")"
W "INSERT INTO rater_claims (task_id, owner, claimed_at, expires_at)
   VALUES (${D}, 'someone-else', strftime('%Y-%m-%dT%H:%M:%S','now'),
           strftime('%Y-%m-%dT%H:%M:%S','now','+600 seconds'))"
n="$(prompts)"
out="$(E rater run --task "E-${D}")"
assert_contains "the run skips, saying another run holds it" "another rater run holds the claim" "${out}"
assert_eq "and never called the model" "${n}" "$(prompts)"
assert_eq "and wrote nothing" "-/-" "$(ratings_of "${D}")"
W "UPDATE rater_claims SET expires_at = strftime('%Y-%m-%dT%H:%M:%S','now','-1 seconds') WHERE task_id=${D}"
E rater run --task "E-${D}" >/dev/null
assert_eq "a lapsed claim is taken over and the task rated" "medium/low" "$(ratings_of "${D}")"
assert_eq "and the claim is released after the write" "0" "$(Q "SELECT count(*) FROM rater_claims WHERE task_id=${D}")"

# ── E. an agent must rate on plan attach ────────────────────────────────────
section "E. An agent's plan attach is refused until it rates"

before="$(Q "SELECT count(*) FROM tasks")"
out="$(AGENT task add "Rename the doohickey flag" --description "Rename the doohickey flag." --plan-file "${TMP}/plan.md")"; rc=$?
assert_eq "task add --plan by an agent, unrated, exits non-zero" "1" "${rc}"
assert_contains "and says nothing was filed" "Nothing was filed" "${out}"
assert_contains "and names the flags" "--complexity <low|medium|high> --risk <low|medium|high>" "${out}"
assert_eq "no task was filed" "${before}" "$(Q "SELECT count(*) FROM tasks")"

out="$(AGENT task add "Rename the doohickey flag" --description "Rename the doohickey flag." --plan-file "${TMP}/plan.md" --complexity low --risk low)"; rc=$?
A1="$(id_from "${out}")"
assert_eq "with both ratings it files" "0" "${rc}"
assert_eq "straight to submitted" "submitted" "$(status_of "${A1}")"

out="$(AGENT task add "Rename the thingamajig flag" --description "Rename the thingamajig flag.")"
U="$(id_from "${out}")"
out="$(AGENT task update "E-${U}" --plan-file "${TMP}/plan.md" --risk medium)"; rc=$?
assert_eq "task update --plan by an agent, missing complexity, exits non-zero" "1" "${rc}"
assert_contains "and says the plan was saved" "Plan saved" "${out}"
assert_contains "and names the one missing flag through task submit" \
    "endless task submit E-${U} --complexity <low|medium|high>" "${out}"
assert_not_contains "and not the rating it already gave" "--risk <low" "${out}"
assert_eq "the task stays unplanned" "unplanned" "$(status_of "${U}")"
assert_contains "the plan itself was written" "frobnicate" "$(Q "SELECT content FROM task_content WHERE task_id=${U} AND name='plan'")"
assert_eq "the rating given on the refused call was written" "-/medium" "$(ratings_of "${U}")"
out="$(AGENT task submit "E-${U}" --complexity low)"; rc=$?
assert_eq "the named way on works" "submitted" "$(status_of "${U}")"

# ── F. approve addresses the person approving ───────────────────────────────
section "F. Approve's refusal says the ratings were never proposed, flags second"

out="$(E task add "Rename the whatsit flag" --description "Rename the whatsit flag." --plan-file "${TMP}/plan.md")"
F="$(id_from "${out}")"
out="$(E task approve "E-${F}")"; rc=$?
assert_eq "approving an unrated task is refused" "1" "${rc}"
assert_contains "it says they were never proposed" "never proposed" "${out}"
assert_contains "and who proposes them" "the rater job does on its next run" "${out}"
first_rater="$(grep -n 'rater' <<<"${out}" | head -1 | cut -d: -f1)"
first_flag="$(grep -n -- '--complexity' <<<"${out}" | head -1 | cut -d: -f1)"
if [[ -n "${first_rater}" && -n "${first_flag}" && "${first_rater}" -lt "${first_flag}" ]]; then
    report_pass "the flags come second, as the override"
else
    report_fail "the flags come second, as the override" "rater line before --complexity line" "${out}"
fi
assert_eq "the task stays submitted" "submitted" "$(status_of "${F}")"

summary
