#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-1794 and records what was true when E-1794
# landed. Edit it only if you ARE E-1794. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-1794 — the inline-content path gate stopped reading slashes as paths.
#
# Both of the gate's rules (E-1744) keyed off "there is a slash in it", so two
# shapes carrying no path at all were refused:
#
#   - a slash-separated token as the whole value — pytest/uv, a runner
#     type/subtype, refused as a mis-passed file;
#   - a bare slash anywhere in prose — "'/' splits family from variant",
#     refused as an absolute path.
#
# Filing E-1789's ED-1535/1536/1537 needed --allow-path workarounds for both. A
# token is now a path on one of three lexical signals and nothing else: it is
# absolute (a leading slash FOLLOWED BY A PATH), it carries an explicit ./ ../
# ~/ prefix, or it ends in a filename extension.
#
# Part 2 is the refusal's advice. --allow-path was the last clause of the last
# sentence, behind "put real content inline" — so an agent that read the whole
# message still rephrased the content to satisfy the gate, distorting the very
# content the gate exists to protect. It now leads. And it no longer offers
# --description-file for a description: that field is one line capped at 1024
# chars, so following the advice earned a second refusal on E-2094.
#
# Run it with the runner, from anywhere inside the worktree:
#
#     endless task verify E-1794
#
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

# The worktree root: this file sits at .endless/tasks/e-1794/, three levels down.
WT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"

command -v uv >/dev/null 2>&1 || setup_error "uv is not on PATH."

# ---------------------------------------------------------------------------
section "A. The gate's unit suite, fail-fast"
# ---------------------------------------------------------------------------
# First and fail-fast: the predicate's own table of cases is the dense proof,
# and the CLI sections below are worth reading only on top of a green one. It is
# also where this task's coverage survives the land — bit rot in a verify suite
# is expected, bit rot in tests/ is a bug.

if py_out="$(cd "$WT" && uv run --project "$WT" python -m pytest -q \
        tests/test_content_flag_gate.py 2>&1)"; then
    report_pass "pytest tests/test_content_flag_gate.py"
else
    report_fail "pytest tests/test_content_flag_gate.py" "all tests pass" \
        "$(printf '%s' "${py_out}" | tail -30)"
    summary
fi

# ─── fixture ────────────────────────────────────────────────────────────────
# A throwaway project in a throwaway config root, the same shape e-2008 uses for
# this gate's sibling. The runner already isolates HOME; this adds a *fresh*
# database per run, so the assertions never inherit a previous run's rows — and
# so nothing here can reach the real one.

TMP=""; PROJ=""

E()   { ( cd "${PROJ}" && uv run --project "${WT}" endless "$@" ); }
Q()   { E sql "$1" --tsv 2>/dev/null; }
RUN() { OUT="$(E "$@" 2>&1)"; RC=$?; return 0; }

setup_fixture() {
    TMP="$(cd "$(mktemp -d)" && pwd -P)"
    PROJ="${TMP}/probe"
    export XDG_CONFIG_HOME="${TMP}/xdg"
    export XDG_CACHE_HOME="${TMP}/cache"
    mkdir -p "${PROJ}" "${TMP}/xdg/endless"

    git -C "${PROJ}" init -q
    git -C "${PROJ}" config user.email verify@test
    git -C "${PROJ}" config user.name verify
    git -C "${PROJ}" commit -q --allow-empty -m "initial commit"

    E project register "${PROJ}" --name probe --label Probe --desc d --lang Go \
        --status active >/dev/null 2>&1

    [[ -n "$(Q "SELECT id FROM projects WHERE name='probe'")" ]] || return 1

    # "Capture" leads because `task add` requires a registered verb, and a fresh
    # database knows the seed set, not the 272 the main one has grown.
    TID="$(E task add "Capture the E-1794 gate fixture content" 2>&1 \
           | grep -oE 'E-[0-9]+' | head -1 | tr -d 'E-')"
    [[ -n "${TID}" ]] || return 1
    return 0
}

teardown_fixture() { [[ -n "${TMP}" && -d "${TMP}" ]] && rm -rf "${TMP}"; }

setup_fixture || { teardown_fixture; setup_error "could not build the isolated fixture."; }
trap teardown_fixture EXIT

# ---------------------------------------------------------------------------
section "B. The two reported cases, end to end"
# ---------------------------------------------------------------------------
# Not-refused is only half of it: the value has to land in the ledger, so each
# case is read back. A gate that stopped raising but ate the content would be
# the E-1626 corruption wearing this task's name.

RUN task update "${TID}" --description 'pytest/uv'
assert_eq "a runner type/subtype as the whole --description is accepted" 0 "${RC}"
assert_eq "…and pytest/uv is exactly what the ledger holds" \
    "pytest/uv" "$(Q "SELECT description FROM live_tasks WHERE id=${TID}")"

ED1537="IANA grammar, two separators: '/' splits family from variant (pytest/uv); '.' nests namespace in the vendor tree (vnd.newclarity.foo)."
RUN task update "${TID}" --description "${ED1537}"
assert_eq "ED-1537's own prose — a named bare slash — is accepted" 0 "${RC}"
assert_contains "…and the bare slash survived into the ledger verbatim" \
    "'/' splits family from variant" \
    "$(Q "SELECT description FROM live_tasks WHERE id=${TID}")"

RUN task update "${TID}" --text 'written as type / subtype'
assert_eq "a spaced slash as an alternative in prose is accepted" 0 "${RC}"
RUN task update "${TID}" --text 'a // comment marker leads a line'
assert_eq "a run of slashes is not a path either" 0 "${RC}"
RUN task update "${TID}" --analysis 'the registry key is docs/plan'
assert_eq "a slash-separated token with no extension is not a path" 0 "${RC}"

# ---------------------------------------------------------------------------
section "C. What must NOT have been relaxed"
# ---------------------------------------------------------------------------
# The narrowing is a hole the moment it lets a mis-passed file through — the
# corruption that lost E-1626/E-1564. Each of the three signals still blocks,
# and the content behind the refusal is still the content that was there.

RUN task update "${TID}" --text /tmp/plan.md
assert_contains "absolute whole value still refused" "received a file path" "${OUT}"
RUN task update "${TID}" --outcome ./o.md
assert_contains "explicit ./ prefix still refused" "received a file path" "${OUT}"
RUN task update "${TID}" --description '~/d.txt'
assert_contains "explicit ~/ prefix still refused" "received a file path" "${OUT}"
RUN task update "${TID}" --text plan.md
assert_contains "bare filename (extension, no slash) still refused" \
    "received a file path" "${OUT}"
RUN task update "${TID}" --analysis docs/guide/index.md
assert_contains "relative path carrying an extension still refused" \
    "received a file path" "${OUT}"

RUN task update "${TID}" --text 'the plan lives at /tmp/x.md, see there'
assert_contains "an absolute path buried in prose still refused" \
    "contains an absolute path" "${OUT}"
RUN task update "${TID}" --text 'the sandbox for this run is at /tmp/sbx today'
assert_contains "an absolute path with no extension still refused" \
    "contains an absolute path" "${OUT}"

assert_eq "…and none of those refusals disturbed the stored description" \
    "${ED1537}" "$(Q "SELECT description FROM live_tasks WHERE id=${TID}")"

RUN task update "${TID}" --text 'refs /opt/corp/spec.md and /tmp/other.md too' \
    --allow-path '^/opt/corp/'
assert_contains "--allow-path is still per-path, not a blanket" \
    "contains an absolute path" "${OUT}"
RUN task update "${TID}" --text 'see /opt/corp/spec.md for the API' \
    --allow-path '^/opt/corp/'
assert_eq "--allow-path still exempts the path it matches" 0 "${RC}"

# ---------------------------------------------------------------------------
section "D. The refusal's advice"
# ---------------------------------------------------------------------------
# The ordering is the finding, not a cosmetic: an agent takes the first
# sanctioned option it is offered, so whichever remedy leads is the one that
# gets used.

RUN task update "${TID}" --text 'the plan is at /tmp/x.md, look there'
TEXT_MSG="${OUT}"
RUN task update "${TID}" --description 'the plan is at /tmp/x.md, look there'
DESC_MSG="${OUT}"

# Assert the later remedy is present FIRST, so truncating the message at it is a
# real cut and the prefix check below cannot pass vacuously.
assert_contains "the message still offers the inline alternative" \
    "Otherwise put real content inline" "${TEXT_MSG}"
assert_contains "--allow-path is offered BEFORE 'put real content inline'" \
    "--allow-path" "${TEXT_MSG%%Otherwise put real content inline*}"

assert_not_contains "a description is never told to load itself from a file" \
    "--description-file" "${DESC_MSG}"
assert_contains "…it is told what a description is instead" \
    "short metadata" "${DESC_MSG}"
assert_contains "…and --allow-path still leads there too" \
    "--allow-path" "${DESC_MSG%%Otherwise put real content inline*}"

assert_contains "a long-form field still gets its file flag" "--text-file" "${TEXT_MSG}"
assert_contains "…still pointed at scratch, as before" ".endless/tmp/" "${TEXT_MSG}"
assert_contains "the refusal still says why absolute paths are refused" \
    "non-portable" "${TEXT_MSG}"

summary
