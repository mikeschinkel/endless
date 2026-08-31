#!/usr/bin/env bash
#
# E-2030 verification — the guide no longer instructs a report-gate-off project
# to use the minimizer, and the freelanced "do not pre-summarize" criteria are
# gone from every surface that carried them.
#
# Run from anywhere inside the worktree:
#   esu
#   endless task verify E-2030
#
# WHAT LANDED
#   Two changes, one root cause each.
#
#   1. THE GUIDE WAS NOT GATED; THE HOOK WAS.
#      `reportChannelOn` (internal/hookcmd/claude.go) resolves the report
#      channel from `.endless/config.json`'s `report_gate`, and its own comment
#      states the rule: a session must never be told to use a channel that will
#      not gate it, nor gated without having been told. The Stop gate and the
#      SessionStart rule honour that. The guide did not — it was `cat`ed to
#      stdout, so `docs/guide/index.md`'s step 7 told EVERY session to run
#      `endless task report` and asserted a Stop hook enforced it. On a
#      gate-off project the last clause is simply untrue, and the instruction is
#      exactly the forbidden case.
#
#      Fix: `endless-go template render --file <path>` renders a file that is
#      not one of the embedded templates, and `endless guide` pipes each guide
#      page through it with the conditions from `cli.guide_conditions()`. Go
#      text/template, not a syntax invented in Python — Endless already has one
#      templating language, and the handoff templates already branch on
#      `{{if .report_gate}}`.
#
#   2. "DO NOT PRE-SUMMARIZE" WAS NEVER REQUESTED.
#      A prior session wrote its own standard into the guide, the handoff
#      templates, the SessionStart rule, the bypass bounce, the wind-down nudge
#      and `task report --help`. It told an agent to hand over bloat it had
#      already recognized as bloat, so that the minimizer could be the one to
#      cut it. Which agent does the cutting does not matter; the outcome the
#      user receives does. Removed everywhere.
#
# WHAT THIS SUITE HAS TO PROVE
#   Three failure modes, none of which a source grep alone reaches:
#
#     1. The gate resolves but nothing changes — a `{{if}}` that both branches
#        satisfy, or a condition name absent from the registry, which Go
#        text/template answers with the else-arm and no error. Layer A dials
#        the real `endless guide` against synthetic gate-on and gate-off
#        projects and requires the two to actually differ.
#     2. The markers leak. A false condition that leaves a blank line inside a
#        markdown table ends the table early; an unrendered `{{if}}` reaches
#        the reader as source. Layer B checks every page, both ways.
#     3. The retired criteria come back by reasoning. They are removable
#        because they were never asked for, which is not a fact the next
#        session can re-derive from the code. Layer C pins their absence at
#        every surface, including the RENDERED handoff.
#
# Layers:
#   A. FAIL-FAST — `endless guide` against real gate-on / gate-off projects.
#      If this does not differ, nothing below matters.
#   B. The render mechanism — --file, the condition registry, marker-free
#      output, table integrity.
#   C. The retired criteria, at source and in the rendered handoff.
#   C2. Command help — which is NOT gated, and says so with the live setting —
#      plus the cwd resolution behind it, mirrored from the Go hook.
#   D. Project-wide regression — build, vet, go test, Python suite, guide map.
#
# Output: pass/fail per check, then a summary. Exit 0 all-passed, 1 any failure,
# 2 setup error.

# Refuse a direct run, and pick up the shared harness vocabulary. Sourced as the
# FIRST executable statement so the refusal fires before anything in this file
# runs; every definition below overrides the harness's own, so a suite written
# before the harness existed behaves exactly as it did.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

# ─── globals ────────────────────────────────────────────────────────────────

PASS_COUNT=0
FAIL_COUNT=0
FAILED_TESTS=()

REPO_ROOT=""
TMP_DIR=""
ENDLESS_BIN=""     # the venv's `endless`, run with cwd INSIDE a fixture project
GATE_ON_DIR=""
GATE_OFF_DIR=""
GATE_LEGACY_DIR=""

if [[ -t 1 ]]; then
    GREEN=$'\033[32m'; RED=$'\033[31m'; DIM=$'\033[2m'
    BOLD=$'\033[1m'; RESET=$'\033[0m'
else
    GREEN=""; RED=""; DIM=""; BOLD=""; RESET=""
fi
UNDERLINE="──────────────────────────────────────────────────────────────"

# ─── output ─────────────────────────────────────────────────────────────────

section() { printf '\n%s%s%s\n%s\n' "${BOLD}" "$1" "${RESET}" "${UNDERLINE}"; }
note()    { printf '  %s%s%s\n' "${DIM}" "$1" "${RESET}"; }

report_pass() {
    printf '  %s✓%s %s\n' "${GREEN}" "${RESET}" "$1"
    PASS_COUNT=$((PASS_COUNT + 1))
}

report_fail() {
    printf '  %s✗%s %s\n' "${RED}" "${RESET}" "$1"
    printf '      %sexpected:%s %s\n' "${DIM}" "${RESET}" "$2"
    printf '      %sgot:%s      %s\n' "${DIM}" "${RESET}" "$3"
    FAIL_COUNT=$((FAIL_COUNT + 1))
    FAILED_TESTS+=("$1")
}

summary() {
    printf '\n%sSummary%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
    if [[ "${FAIL_COUNT}" -eq 0 ]]; then
        printf '  %s%d passed%s\n\n  %sALL PASSED%s\n\n' \
            "${GREEN}" "${PASS_COUNT}" "${RESET}" "${GREEN}${BOLD}" "${RESET}"
        return 0
    fi
    printf '  %s%d passed%s, %s%d failed%s\n\n  %sFAILED:%s\n' \
        "${GREEN}" "${PASS_COUNT}" "${RESET}" "${RED}" "${FAIL_COUNT}" "${RESET}" \
        "${RED}${BOLD}" "${RESET}"
    local t
    for t in "${FAILED_TESTS[@]}"; do printf '    - %s\n' "${t}"; done
    printf '\n'
    return 1
}

# ─── assertions ─────────────────────────────────────────────────────────────

# assert_contains DESC HAYSTACK NEEDLE
assert_contains() {
    local desc="$1" hay="$2" needle="$3"
    if [[ "${hay}" == *"${needle}"* ]]; then report_pass "${desc}"; return 0; fi
    report_fail "${desc}" "output contains '${needle}'" \
        "$(printf '%s' "${hay}" | head -3)"
    return 1
}

# assert_lacks DESC HAYSTACK NEEDLE
assert_lacks() {
    local desc="$1" hay="$2" needle="$3"
    if [[ "${hay}" != *"${needle}"* ]]; then report_pass "${desc}"; return 0; fi
    report_fail "${desc}" "output does NOT contain '${needle}'" \
        "$(printf '%s' "${hay}" | grep -F -- "${needle}" | head -2)"
    return 1
}

# assert_refuses DESC EXPECTED_SUBSTRING CMD... — non-zero exit AND a reason.
assert_refuses() {
    local desc="$1" want="$2"; shift 2
    local output rc
    output=$(cd "${REPO_ROOT}" && "$@" 2>&1); rc=$?
    if [[ "${rc}" -ne 0 && "${output}" == *"${want}"* ]]; then
        report_pass "${desc}"; return 0
    fi
    report_fail "${desc}" "non-zero exit mentioning '${want}'" \
        "rc=${rc}: $(printf '%s' "${output}" | tail -3)"
    return 1
}

# assert_no_match DESC PATTERN PATHSPEC... — no TRACKED file may match. `git
# grep` so build output and .git/ can neither mask nor fake a hit.
assert_no_match() {
    local desc="$1" pattern="$2"; shift 2
    local hits
    hits=$(cd "${REPO_ROOT}" && git grep -nE "${pattern}" -- "$@" 2>/dev/null)
    if [[ -z "${hits}" ]]; then report_pass "${desc}"; return 0; fi
    report_fail "${desc}" "no tracked file matches ${pattern}" \
        "$(printf '%s' "${hits}" | head -3)"
    return 1
}

# assert_cmd DESC CMD... — plain exit-0 check for the regression layer.
assert_cmd() {
    local desc="$1"; shift
    local output rc
    output=$(cd "${REPO_ROOT}" && "$@" 2>&1); rc=$?
    if [[ "${rc}" -eq 0 ]]; then report_pass "${desc}"; return 0; fi
    report_fail "${desc}" "exit 0" "$(printf '%s' "${output}" | tail -5)"
    return 1
}

# guide_in DIR SECTION... — run `endless guide` with cwd inside a fixture.
#
# cwd is the whole point: `_report_gate_on()` resolves the ENCLOSING project
# from the working directory, so the fixture's own config.json is what decides.
# That rules out `uv run --directory`, which sets cwd to the repo and would make
# every fixture answer with Endless's own gate-off config.
guide_in() {
    local dir="$1"; shift
    (cd "${dir}" && PATH="${TMP_DIR}/bin:${PATH}" "${ENDLESS_BIN}" guide "$@" 2>&1)
}

# ─── setup ──────────────────────────────────────────────────────────────────

# make_project DIR GATE [SPELLING] — a directory that resolves as a project with
# the given gate. Deliberately synthetic and outside the repo: this checkout
# now enables the minimizer (E-1975), so BOTH branches need a project that is
# not this one — which is also the PRODUCT case, a project that is not Endless
# reading the same guide.
#
# SPELLING picks the config shape: `minimizer` is E-1975's current one, `legacy`
# is E-1953's `report_gate` key, which must keep working or a project that opted
# out under the old name silently opts back in.
make_project() {
    local dir="$1" gate="$2" spelling="${3:-minimizer}"
    mkdir -p "${dir}/.endless" || return 1
    if [[ "${spelling}" == "legacy" ]]; then
        printf '{"report_gate": %s}\n' "${gate}" > "${dir}/.endless/config.json"
    else
        printf '{"minimizer": {"enabled": %s}}\n' "${gate}" > "${dir}/.endless/config.json"
    fi
}

setup() {
    REPO_ROOT=$(git rev-parse --show-toplevel 2>/dev/null) || {
        printf 'setup error: not inside a git repository\n' >&2
        exit 2
    }
    if [[ ! -f "${REPO_ROOT}/tests/test_guide_conditionals.py" ]]; then
        printf 'setup error: tests/test_guide_conditionals.py not found — is this the E-2030 worktree?\n' >&2
        exit 2
    fi
    for tool in go uv git python3; do
        command -v "${tool}" >/dev/null 2>&1 || {
            printf 'setup error: %s not on PATH\n' "${tool}" >&2
            exit 2
        }
    done

    TMP_DIR=$(mktemp -d) || { printf 'setup error: mktemp failed\n' >&2; exit 2; }
    trap 'rm -rf "${TMP_DIR}"' EXIT

    # Build THIS tree's endless-go into the tempdir rather than the worktree's
    # bin/. The guide render shells out to whatever `endless-go` resolves to, and
    # a globally installed one predating --file would make Layer A fail for a
    # reason that has nothing to do with this source.
    mkdir -p "${TMP_DIR}/bin"
    if ! (cd "${REPO_ROOT}" && go build -o "${TMP_DIR}/bin/endless-go" ./cmd/endless-go) 2>"${TMP_DIR}/build.err"; then
        printf 'setup error: go build endless-go failed:\n%s\n' "$(cat "${TMP_DIR}/build.err")" >&2
        exit 2
    fi

    if ! (cd "${REPO_ROOT}" && uv sync --quiet) 2>"${TMP_DIR}/sync.err"; then
        printf 'setup error: uv sync failed:\n%s\n' "$(cat "${TMP_DIR}/sync.err")" >&2
        exit 2
    fi
    ENDLESS_BIN="${REPO_ROOT}/.venv/bin/endless"
    [[ -x "${ENDLESS_BIN}" ]] || {
        printf 'setup error: %s missing after uv sync\n' "${ENDLESS_BIN}" >&2
        exit 2
    }

    GATE_ON_DIR="${TMP_DIR}/gate-on"
    GATE_OFF_DIR="${TMP_DIR}/gate-off"
    GATE_LEGACY_DIR="${TMP_DIR}/gate-off-legacy"
    make_project "${GATE_ON_DIR}"     true  || exit 2
    make_project "${GATE_OFF_DIR}"    false || exit 2
    make_project "${GATE_LEGACY_DIR}" false legacy || exit 2
}

# ─── layer A: fail-fast — the guide follows the gate ────────────────────────

layer_a() {
    section "A. The rendered guide follows the reading project's report_gate"
    note "real \`endless guide\`, two real projects; a source grep proves nothing here"

    local on off
    on=$(guide_in "${GATE_ON_DIR}")
    off=$(guide_in "${GATE_OFF_DIR}")

    if [[ -z "${on}" || -z "${off}" ]]; then
        report_fail "endless guide renders in both fixtures" \
            "non-empty output from both" \
            "on=${#on} bytes, off=${#off} bytes: $(printf '%s' "${on}${off}" | head -2)"
        return 1
    fi
    report_pass "endless guide renders in both fixtures"

    # THE DEFECT, stated. A gate-off project must not be handed the invocation.
    assert_lacks "gate-off index: no \`--draft-file\` invocation" \
        "${off}" "--draft-file" || return 1
    assert_contains "gate-off index: says the channel is off here" \
        "${off}" '"report_gate": false' || return 1

    # The other half of told-iff-gated: a gate-ON project must still be told.
    assert_contains "gate-on index: still routes through the channel" \
        "${on}" "endless task report <id> --draft-file <path>" || return 1

    # A gate that resolves but changes nothing would pass every check above if
    # the two branches happened to share text.
    if [[ "${on}" == "${off}" ]]; then
        report_fail "the two renderings differ" "different output" "byte-identical"
        return 1
    fi
    report_pass "the two renderings differ"

    # Same claim for the section that carries the whole contract.
    local on_tasks off_tasks
    on_tasks=$(guide_in "${GATE_ON_DIR}" tasks)
    off_tasks=$(guide_in "${GATE_OFF_DIR}" tasks)
    assert_lacks "gate-off tasks: no \`--draft-file\` invocation" \
        "${off_tasks}" "--draft-file" || return 1
    assert_contains "gate-off tasks: what the user is owed does not change" \
        "${off_tasks}" "have to reach" || return 1
    assert_contains "gate-on tasks: the four invariants survive" \
        "${on_tasks}" "survive byte for byte" || return 1

    # Everything DOWNSTREAM of the channel goes with it. E-1975 shipped
    # `endless minimizer` and `endless session turn`, both of which read
    # artifacts only the channel produces; documenting them to a gate-off
    # project is the same defect as step 7, one layer out.
    local page rendered cmd
    for page in tasks sessions; do
        rendered=$(guide_in "${GATE_OFF_DIR}" "${page}")
        for cmd in "endless minimizer" "endless session turn" "task report --raw"; do
            assert_lacks "gate-off ${page}: teaches no \`${cmd}\`" \
                "${rendered}" "${cmd}" || return 1
        done
    done

    # ...and a conditional wrapped one section too wide would pass every check
    # above by deleting what a gate-ON project needs.
    rendered="$(guide_in "${GATE_ON_DIR}" tasks)$(guide_in "${GATE_ON_DIR}" sessions)"
    for cmd in "endless minimizer" "endless session turn" "task report --raw"; do
        assert_contains "gate-on guide keeps \`${cmd}\`" "${rendered}" "${cmd}" || return 1
    done

    # E-1975 renamed the config key. A project that opted out under E-1953's
    # `report_gate` must stay opted out, or the rename silently re-enrolls it.
    assert_lacks "legacy \`report_gate: false\` still turns the guide off" \
        "$(guide_in "${GATE_LEGACY_DIR}")" "--draft-file" || return 1

    return 0
}

# ─── layer B: the render mechanism ──────────────────────────────────────────

layer_b() {
    section "B. The render mechanism"
    note "--file, the condition registry, and output that carries no template source"

    local sample="${TMP_DIR}/sample.md"
    printf 'before\n{{if .report_gate}}ON\n{{else}}OFF\n{{end}}after\n' > "${sample}"

    local out
    out=$(cd "${TMP_DIR}" && "${TMP_DIR}/bin/endless-go" template render --file "${sample}" \
          <<<'{"report_gate": true}' 2>&1)
    assert_contains "--file renders the true branch" "${out}" $'before\nON\nafter'

    out=$(cd "${TMP_DIR}" && "${TMP_DIR}/bin/endless-go" template render --file "${sample}" \
          <<<'{"report_gate": false}' 2>&1)
    assert_contains "--file renders the false branch" "${out}" $'before\nOFF\nafter'

    # --file must not need project context: `endless guide` is often the first
    # command a session runs, and requiring a project to read documentation
    # would withhold the guide exactly when someone is learning what to do.
    # ${TMP_DIR} has no .endless/ of its own.
    if [[ "${out}" == *"project context"* ]]; then
        report_fail "--file needs no project context" "renders anywhere" "${out}"
    else
        report_pass "--file needs no project context"
    fi

    assert_refuses "--file with a template name is refused" \
        "pass no template name" \
        "${TMP_DIR}/bin/endless-go" template render --file "${sample}" handoff/todo
    assert_refuses "--file with --project is refused" \
        "exclusive" \
        "${TMP_DIR}/bin/endless-go" template render --file "${sample}" --project endless
    assert_refuses "a missing --file path fails loudly" \
        "read template file" \
        "${TMP_DIR}/bin/endless-go" template render --file "${TMP_DIR}/nope.md"

    # A condition absent from the registry is answered with the else-arm and no
    # error, so `{{if .report_gat}}` would silently turn the channel off for
    # every reader of that page. This is the check that makes that loud.
    assert_cmd "every guide condition is in cli.guide_conditions()" \
        uv run pytest tests/test_guide_conditionals.py -q

    # `when:` on a COMMAND map file, not just a topic. E-1975's `minimizer` row
    # is the case that forced this; `task report`'s own row is the one shipped
    # here, so the mechanism is exercised by the task that built it.
    local on_index off_index
    on_index=$(guide_in "${GATE_ON_DIR}")
    off_index=$(guide_in "${GATE_OFF_DIR}")
    assert_contains "gate-on index: the \`task report\` command row is present" \
        "${on_index}" '| `task report` |'
    assert_lacks "gate-off index: the \`task report\` command row is gone" \
        "${off_index}" '| `task report` |'
    assert_lacks "gate-off index: the \`minimizer\` command row is gone" \
        "${off_index}" '| `minimizer` |'

    # Marker leakage and table integrity, on the REAL pages under both gates.
    local page rendered bad=0
    for page in index tasks orchestration sessions decisions reference appendix-a; do
        [[ -f "${REPO_ROOT}/docs/guide/${page}.md" ]] || continue
        for gate in "${GATE_ON_DIR}" "${GATE_OFF_DIR}"; do
            rendered=$(guide_in "${gate}" "${page}" 2>/dev/null) \
                || rendered=$(guide_in "${gate}")
            if [[ "${rendered}" == *"{{"* ]]; then
                report_fail "no template source reaches the reader (${page})" \
                    "no '{{' in the rendered page" \
                    "$(printf '%s' "${rendered}" | grep -F '{{' | head -2)"
                bad=1
            fi
        done
    done
    [[ "${bad}" -eq 0 ]] && report_pass "no template source reaches the reader (all pages, both gates)"

    # A false condition that leaves a blank line inside a markdown table ends
    # the table early. Both tables, both gates.
    local gatedir label
    for gatedir in "${GATE_ON_DIR}" "${GATE_OFF_DIR}"; do
        label=$([[ "${gatedir}" == "${GATE_ON_DIR}" ]] && echo "gate-on" || echo "gate-off")
        # A blank line ENDING a table is normal; one BETWEEN two rows is the
        # break. So a blank is only a failure if another row follows it.
        if guide_in "${gatedir}" | awk '
            /^\| (Command|Topic) \|/ { intable = 1; pending = 0; next }
            !intable                 { next }
            /^\|/ && pending         { print "row after a blank line"; exit 1 }
            /^\|/                    { next }
            /^$/                     { pending = 1; next }
                                     { intable = 0; pending = 0 }
        ' | grep -q .; then
            report_fail "${label}: both cross-reference tables stay contiguous" \
                "no blank line between table rows" "a table was broken by a false condition"
        else
            report_pass "${label}: both cross-reference tables stay contiguous"
        fi
    done
}

# ─── layer C: the retired criteria ──────────────────────────────────────────

layer_c() {
    section "C. \"Do not pre-summarize\" is gone"
    note "it was never requested; that is not a fact the next session can re-derive"

    assert_no_match "no source carries the retired directive" \
        'Do NOT pre-summarize|Do not pre-summarize|no pre-summarizing' \
        'docs/guide/*.md' 'src/endless/*.py' 'internal/**/*.go' \
        'internal/templatecmd/templates/**' ':!tests/**' ':!.endless/**'

    # The handoff is the surface a spawned session actually reads, and it is
    # rendered from partials — the source grep above would miss a reintroduction
    # that only appears once the set is assembled.
    local fixture="${TMP_DIR}/handoff-fixture"
    make_project "${fixture}" true || return 0
    local vars rendered gate
    for gate in true false; do
        vars=$(printf '{"spawned_id":9999,"label_prefix":"E-9999","title":"T",' )
        vars+=$(printf '"worktree_path":"/w","branch":"b","child_count":0,')
        vars+=$(printf '"children_state":"none","report_gate":%s,"bg":false}' "${gate}")
        rendered=$(cd "${fixture}" && "${TMP_DIR}/bin/endless-go" template render handoff/todo \
                   <<<"${vars}" 2>&1)
        assert_lacks "rendered handoff (report_gate=${gate}) carries no pre-summarize directive" \
            "${rendered}" "pre-summarize"
    done

    # The gate-off handoff must still say what the gate-on one delegates to the
    # minimizer — otherwise removing the directive removed the standard too.
    assert_contains "gate-off handoff still forbids the negative confirmation" \
        "${rendered}" "no stray files"
}

# ─── layer C2: command help, and the resolution behind it ───────────────────

layer_c2() {
    section "C2. Command help names the switch and its live value"
    note "and resolves it the way the Stop hook does, which is where the bug was"

    # A worktree-shaped fixture whose branch config DISAGREES with its project
    # root — the shape a self-dev branch takes when it enables the minimizer for
    # itself, and the shape that exposed the divergence.
    local proj="${TMP_DIR}/wt-proj"
    local wt="${proj}/.endless/worktrees/e-9999"
    mkdir -p "${wt}/.endless" || return 0
    printf '{"name":"wt-proj","minimizer":{"enabled":false}}\n' > "${proj}/.endless/config.json"
    printf '{"minimizer":{"enabled":true,"optimizer":true}}\n'  > "${wt}/.endless/config.json"

    local from_wt from_root
    from_wt=$(cd "${wt}"   && PATH="${TMP_DIR}/bin:${PATH}" "${ENDLESS_BIN}" minimizer --help 2>&1)
    from_root=$(cd "${proj}" && PATH="${TMP_DIR}/bin:${PATH}" "${ENDLESS_BIN}" minimizer --help 2>&1)

    # The help still DESCRIBES the loop wherever it is read — it is reached by
    # typing the command, so hiding it would answer a direct question with
    # silence. What changes is the setting reported beneath it.
    assert_contains "gate-off: help still describes the loop" \
        "${from_root}" "autoresearch loop"
    assert_contains "gate-off: and says the channel is off here" \
        "${from_root}" "minimizer.enabled    false"

    # THE BUG: this used to read the project root, so a worktree that had
    # switched the minimizer ON for itself was told it was off — while the Stop
    # hook, walking up from cwd, held its turns against the channel.
    assert_contains "worktree: help reports the worktree's own value" \
        "${from_wt}" "minimizer.enabled    true"
    assert_contains "worktree: and names the config that actually won" \
        "${from_wt}" "worktrees/e-9999/.endless/config.json"

    # Every Python emitter asks one helper, so the guide must agree with it.
    assert_contains "worktree: the guide agrees with the hook" \
        "$(cd "${wt}" && PATH="${TMP_DIR}/bin:${PATH}" "${ENDLESS_BIN}" guide 2>&1)" \
        "--draft-file"
    assert_lacks "project root: the guide agrees there too" \
        "$(cd "${proj}" && PATH="${TMP_DIR}/bin:${PATH}" "${ENDLESS_BIN}" guide 2>&1)" \
        "--draft-file"

    # `task report --help` carries the same block: on a gate-off project its own
    # "a Stop hook compares your final message against it" is not true.
    assert_contains "task report --help carries the setting too" \
        "$(cd "${proj}" && PATH="${TMP_DIR}/bin:${PATH}" "${ENDLESS_BIN}" task report --help 2>&1)" \
        "minimizer.enabled    false"

    assert_cmd "the Go/Python resolution parity tests" \
        uv run pytest tests/test_minimizer_gate_cwd.py -q
}

# ─── layer D: project-wide regression ───────────────────────────────────────

layer_d() {
    section "D. Project-wide regression"
    note "the guide render moved from a file read to a subprocess; that has neighbours"

    assert_cmd "go build ./..."  go build ./...
    assert_cmd "go vet ./..."    go vet ./...

    # -timeout 20m for the PRE-EXISTING sandboxcmd destroy-test slowness tracked
    # as E-1908, not anything this task introduced. Drop the flag once it lands.
    assert_cmd "go test ./... (-timeout 20m; see E-1908)" go test -timeout 20m ./...

    assert_cmd "just test (Python suite)" just test
    assert_cmd "just guide-check (command -> section map)" just guide-check
}

# ─── main ───────────────────────────────────────────────────────────────────

main() {
    setup
    printf '%sE-2030 — the guide is gated on the reading project'"'"'s report_gate%s\n' \
        "${BOLD}" "${RESET}"
    printf '%s\n' "${UNDERLINE}"
    printf '  cwd:  %s\n' "${REPO_ROOT}"

    if ! layer_a; then
        note "fail-fast: the guide does not follow the gate; skipping later layers"
        summary
        return 1
    fi
    layer_b
    layer_c
    layer_c2
    layer_d

    summary
}

main "$@"
