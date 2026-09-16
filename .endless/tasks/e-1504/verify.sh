#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-1504 and records what was true when E-1504
# landed. Edit it only if you ARE E-1504. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-1504 verification — "Rename --llm to --agent and add --format <fmt> alias
# across agent-facing commands".
#
# WHAT LANDED
#   `--llm` named the READER's technology rather than the reader, and it sat on
#   15 commands while `--json` sat on 28, with no command able to say which
#   renderings it actually had. The flag surface is now one thing: `--agent`,
#   `--json`, and `--format <fmt>` as the long form of both, derived on every
#   command from the single fact of whether that command HAS an agent
#   rendering. `--llm` is retired — still recognised, never working — and
#   `task deps` / `task relations`, the two reader commands that had an agent
#   view and no machine one, gained `--json`.
#
#   Explicitly not here: agent RENDERINGS for the commands that have only
#   `--json`. That is output design (E-2136, E-2141), not a flag rename.
#
# THE CLAIMS
#   C1  `--llm` IS RETIRED, NOT ALIASED. It never renders, it is never
#       advertised, and it refuses by name with a pointer at `--agent` rather
#       than Click's "No such option" — the E-1000 pattern, because agent
#       muscle memory outlives a rename.
#   C2  `--agent` RENDERS WHAT `--llm` RENDERED. The rename changed the
#       spelling and nothing else about the output.
#   C3  `--format` IS AN EXACT ALIAS, all three values, byte for byte. It is a
#       spelling, not a fourth rendering.
#   C4  `--format` REACHES EVERY COMMAND THAT RENDERS A RESULT, and advertises
#       `agent` if and only if that command has an agent rendering. A command
#       whose `--json` names its INPUT (`session order`, `task import`) is not
#       a renderer and takes no `--format`.
#   C5  A MISSING AGENT RENDERING IS REFUSED BY NAME. `--format agent` where
#       there is none says so and leads with the remedy that works (ED-1584) —
#       never the human rendering handed back as if it were agent-facing.
#   C6  CONTRADICTION COSTS TOKENS. `--json --agent` is refused. Before this,
#       `--llm --json` silently yielded JSON because every renderer tested
#       `as_json` first — a precedence no help text stated.
#   C7  relations/deps GAINED `--json`, carrying the subject and a `links`
#       array that is always present, with `rel` the same token the agent line
#       prints. The two spellings of that command still render identically.
#
# ISOLATION
#   The CLI assertions run the real installed entry point against a throwaway
#   project under a temp HOME/XDG_CONFIG_HOME — its own database, its own git
#   repo. Nothing here reads or writes this worktree's state or any real
#   endless database.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

WT="$(git rev-parse --show-toplevel)" || setup_error "not in a git repo"
cd "${WT}" || setup_error "cannot cd to ${WT}"

TMP="$(mktemp -d)" || setup_error "cannot make a temp dir"
trap 'rm -rf "${TMP}"' EXIT

for f in src/endless/cli.py src/endless/task_cmd.py; do
    [[ -f "${f}" ]] || setup_error "missing ${f}"
done

# ── Fail-fast: this task's own unit tests ────────────────────────────────────
#
# test_output_format.py is E-1504's own, and carries the drift guard that walks
# the whole command tree. test_relations.py owns the rendering that gained a
# JSON mode; test_rowcap.py owns the footer whose `llm=` keyword this renamed.
section "Unit tests (fail fast)"

if uv run pytest -q \
        tests/test_output_format.py \
        tests/test_relations.py \
        tests/test_rowcap.py \
        >"${TMP}/py.log" 2>&1; then
    report_pass "pytest — the flag surface, the relations rendering, the row cap"
else
    report_fail "pytest tests/test_output_format.py + test_relations.py + test_rowcap.py" \
        "exit 0" "$(tail -40 "${TMP}/py.log")"
    summary
fi

# ── A throwaway project, driven through the real CLI ─────────────────────────
section "Setup: an isolated project seeded through the real CLI"

ENDLESS="$(uv run python -c 'import sys, pathlib; print(pathlib.Path(sys.executable).parent / "endless")' 2>/dev/null)"
[[ -x "${ENDLESS}" ]] || setup_error "no endless entry point at ${ENDLESS:-<unresolved>}"

ES_HOME="${TMP}/home"
PROJ="${TMP}/proj"
mkdir -p "${ES_HOME}/.config" "${PROJ}"

(
    cd "${PROJ}" &&
    git init -q -b main . &&
    git config user.email verify@example.com &&
    git config user.name verify
) >"${TMP}/git.log" 2>&1 || setup_error "cannot init the throwaway repo: $(cat "${TMP}/git.log")"
(cd "${PROJ}" && git commit -q --allow-empty -m init) >>"${TMP}/git.log" 2>&1 \
    || setup_error "cannot seed the throwaway repo: $(cat "${TMP}/git.log")"

es() {
    ( cd "${PROJ}" &&
      env -u ENDLESS_SESSION_ID -u ENDLESS_DB_CHOICE \
          HOME="${ES_HOME}" \
          XDG_CONFIG_HOME="${ES_HOME}/.config" \
          ENDLESS_AUTO_MIGRATE=1 \
          ENDLESS_NO_TRIAGE=1 \
          "${ENDLESS}" "$@" )
}

if es project register . --name probe --infer >"${TMP}/reg.log" 2>&1; then
    report_pass "registered a throwaway project on its own database"
else
    setup_error "cannot register the throwaway project: $(tail -20 "${TMP}/reg.log")"
fi

es task add "Add the subject task" --no-session >"${TMP}/seed.log" 2>&1 \
    || setup_error "seed failed: $(tail -20 "${TMP}/seed.log")"
es task add "Add the blocking task" --no-session >>"${TMP}/seed.log" 2>&1 \
    || setup_error "seed failed: $(tail -20 "${TMP}/seed.log")"
es task block E-1 --by E-2 --no-session >>"${TMP}/seed.log" 2>&1 \
    || setup_error "seed failed (block): $(tail -20 "${TMP}/seed.log")"

# ── C1: --llm is retired, not aliased ────────────────────────────────────────
section "C1 — --llm is retired: recognised, refused, never advertised"

LLM_OUT="$(es task list --llm 2>&1)"
assert_contains "task list --llm refuses by name" \
    "--llm was renamed to --agent" "${LLM_OUT}"
assert_not_contains "the refusal is a pointer, not Click's 'No such option'" \
    "No such option" "${LLM_OUT}"
assert_contains "the refusal names the new spelling as the remedy" \
    "Re-run with --agent" "${LLM_OUT}"

assert_not_contains "--help never advertises the retired name" \
    "--llm" "$(es task list --help 2>&1)"
assert_not_contains "task show --help never advertises it either" \
    "--llm" "$(es task show --help 2>&1)"

# The retirement reaches every command that carried the flag, not just the ones
# whose options were written out by hand — `task landed` / `task unlanded` take
# theirs from a shared factory.
assert_contains "the retirement reaches the factory-built commands too" \
    "--llm was renamed to --agent" "$(es task landed --llm 2>&1)"
assert_contains "...and the decision commands" \
    "--llm was renamed to --agent" "$(es decision list --llm 2>&1)"

# ── C2/C3: --agent renders it, --format spells it ────────────────────────────
section "C2/C3 — --agent renders, --format is an exact alias"

for cmd_args in "task list" "task relations E-1" "task deps E-1" "decision list"; do
    # shellcheck disable=SC2086
    assert_eq "${cmd_args} --agent == --format agent" \
        "$(es ${cmd_args} --agent 2>&1)" "$(es ${cmd_args} --format agent 2>&1)"
    # shellcheck disable=SC2086
    assert_eq "${cmd_args} --json == --format json" \
        "$(es ${cmd_args} --json 2>&1)" "$(es ${cmd_args} --format json 2>&1)"
    # shellcheck disable=SC2086
    assert_eq "${cmd_args} bare == --format text" \
        "$(es ${cmd_args} 2>&1)" "$(es ${cmd_args} --format text 2>&1)"
done

AGENT_LIST="$(es task list --agent 2>&1)"
assert_contains "--agent still emits the token-efficient rendering" \
    "Add the subject task" "${AGENT_LIST}"
assert_not_contains "...which is not the human table" \
    "─────" "${AGENT_LIST}"

assert_eq "naming the same rendering twice is not a contradiction" \
    "0" "$(es task list --json --format json >/dev/null 2>&1; echo $?)"

# ── C4: --format reaches every renderer, and advertises only what exists ─────
section "C4 — the surface is uniform, and honest about itself"

assert_contains "an agent-facing command advertises all three" \
    "[text|json|agent]" "$(es task list --help 2>&1)"
assert_contains "a command with no agent rendering advertises only two" \
    "[text|json]" "$(es session status --help 2>&1)"
assert_not_contains "...and does not advertise a value it cannot honour" \
    "[text|json|agent]" "$(es session status --help 2>&1)"

assert_contains "relations/deps gained --json" \
    "--json" "$(es task relations --help 2>&1)"

# `--json` here names how the command reads its INPUT, not how it writes its
# output, so neither command is a renderer and neither takes --format.
assert_not_contains "session order keeps its input --json and gains no --format" \
    "--format" "$(es session order --help 2>&1)"
assert_not_contains "task import likewise" \
    "--format" "$(es task import --help 2>&1)"

# ── C5: a missing agent rendering is refused by name ─────────────────────────
section "C5 — --format agent refuses where there is no agent rendering"

MISS="$(es session status --format agent 2>&1)"
assert_contains "it says the rendering does not exist" \
    "has no agent rendering yet" "${MISS}"
assert_contains "it names the remedy that works" "--format json" "${MISS}"
assert_not_contains "it is not Click's generic choice error" \
    "is not one of" "${MISS}"
assert_not_contains "and it renders nothing — no human view handed back" \
    "Focal" "${MISS}"

# ED-1584: the remedy leads, the rationale trails, because an agent takes the
# first sanctioned option it reads and stops looking.
REMEDY_LINE="$(printf '%s\n' "${MISS}" | grep -n -- "--format json" | head -1 | cut -d: -f1)"
WHY_LINE="$(printf '%s\n' "${MISS}" | grep -n "command by command" | head -1 | cut -d: -f1)"
if [[ -n "${REMEDY_LINE}" && -n "${WHY_LINE}" && "${REMEDY_LINE}" -lt "${WHY_LINE}" ]]; then
    report_pass "the remedy is ordered ahead of the rationale (ED-1584)"
else
    report_fail "the remedy is ordered ahead of the rationale (ED-1584)" \
        "remedy line < rationale line" "remedy=${REMEDY_LINE:-none} rationale=${WHY_LINE:-none}"
fi

BAD="$(es task list --format toon 2>&1)"
assert_contains "an unknown format names the accepted set" \
    "Use text, json or agent." "${BAD}"

# ── C6: contradiction costs tokens ───────────────────────────────────────────
section "C6 — two renderings at once is refused, not silently resolved"

CLASH="$(es task list --json --agent 2>&1)"
assert_contains "the refusal names both spellings" \
    "--agent and --json name two different output renderings" "${CLASH}"
assert_contains "and offers the unambiguous long forms" \
    "--format agent or --format json" "${CLASH}"
assert_not_contains "it did NOT quietly emit JSON, as --llm --json used to" \
    '"id"' "${CLASH}"
assert_eq "a contradiction exits non-zero" \
    "2" "$(es task list --json --agent >/dev/null 2>&1; echo $?)"

# A mixed pair — one short spelling, one long — is the same contradiction.
assert_contains "mixing a short and a long spelling is caught too" \
    "name two different output renderings" "$(es task list --agent --format json 2>&1)"

# ── C7: the relations/deps JSON rendering ────────────────────────────────────
section "C7 — relations/deps carry a machine rendering at last"

REL_JSON="$(es task relations E-1 --json 2>&1)"
probe() {
    printf '%s' "$1" | python3 -c \
        "import json,sys; d=json.load(sys.stdin); print($2)" 2>&1
}

assert_eq "the payload names its subject" "E-1" "$(probe "${REL_JSON}" 'd["id"]')"
assert_eq "one link, to the blocker" "1" "$(probe "${REL_JSON}" 'len(d["links"])')"
assert_eq "carrying the related id" "E-2" "$(probe "${REL_JSON}" 'd["links"][0]["id"]')"
assert_eq "its directional relation" "blocked by" "$(probe "${REL_JSON}" 'd["links"][0]["rel"]')"
assert_eq "and the related task's status" "untriaged" "$(probe "${REL_JSON}" 'd["links"][0]["status"]')"

# E-1185: one fact wears one spelling across the renderings.
assert_contains "the agent line prints the same rel token" \
    "(blocked by)" "$(es task relations E-1 --agent 2>&1)"

# `links` is always present, so `[]` says "no relations" rather than an absent
# key leaving it unsaid.
es task add "Add an unrelated task" --no-session >>"${TMP}/seed.log" 2>&1 \
    || setup_error "seed failed (lonely)"
assert_eq "a task with no relations still carries a links key" \
    "0" "$(probe "$(es task relations E-3 --json 2>&1)" 'len(d["links"])')"

assert_eq "deps and relations are one command under two names (json)" \
    "$(es task deps E-1 --json 2>&1)" "$(es task relations E-1 --json 2>&1)"
assert_eq "...and under the agent rendering" \
    "$(es task deps E-1 --agent 2>&1)" "$(es task relations E-1 --agent 2>&1)"

summary
