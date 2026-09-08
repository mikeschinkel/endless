#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-1428 and records what was true when E-1428
# landed. Edit it only if you ARE E-1428. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-1428 verification — `task claim` stops printing a path that is not a `cd`
# target, prints the ones it keeps as aligned labels, and the ghost helper it
# used to teach is gone from the guide as well as from the output.
#
# Before: claim listed `sandbox provisioned: ~/.cache/endless/sandboxes/<wt>`
# as a bullet directly above `worktree created: <wt>`. A reader scanning for
# somewhere to cd took the first path and landed in a cache directory that is
# not a project, after which every endless command failed with "Not in a
# registered project directory" (hit live, 2026-05-19). The block's labels were
# ragged, so the value anyone was actually looking for started at a different
# column on every line. And `eswt` — a worktree-switching shell helper
# `endless shell-init` has never defined — was still written down in four guide
# files as though a reader had it.
#
# After: the sandbox path is reachable on demand through
# `endless worktree sandbox` and printed nowhere by default; claim's and
# spawn's facts are aligned rows; the session id survives on exactly one caller
# path, in the `session goto ES-N` line where the binding is news; and `eswt`
# appears nowhere under docs/ or src/.
#
#   endless task verify E-1428
#
# Exit 0 on all-passed, 1 on any failure, 2 on setup error.

source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

WT="$(git rev-parse --show-toplevel)" || setup_error "not in a git repo"
cd "${WT}" || setup_error "cannot cd to ${WT}"

TMP="$(mktemp -d)" || setup_error "could not create a temp dir"
trap 'rm -rf "${TMP}"' EXIT

# ── 1. fail-fast unit gate ──────────────────────────────────────────────────
# This task's own durable coverage plus the two suites whose assertions the
# output change moves. A failure here makes everything below meaningless, so
# the suite stops rather than reporting a cascade.
section "1. Unit gate (fail fast)"

if uv run pytest -q \
        tests/test_claim_output_format.py \
        tests/test_task_claim_worktree.py \
        tests/test_claim_launches_a_session.py \
        >"${TMP}/py.log" 2>&1; then
    report_pass "pytest — claim/spawn output, worktree creation, the launch path"
else
    report_fail "pytest" "exit 0" "$(tail -40 "${TMP}/py.log")"
    summary
fi

# The retention fixture this task also repaired: pre-existing on main, red
# between 00:00 and 01:30 local because it expressed a calendar bucket as an
# age offset. Nothing to do with claim's output, fixed here because a task
# cannot report a suite it left red.
if go test ./internal/eventcmd/ -run TestEventBackup_EnforcesTieredRetention \
        >"${TMP}/go.log" 2>&1; then
    report_pass "go test — backup retention (the clock-dependent fixture)"
else
    report_fail "go test ./internal/eventcmd/" "exit 0" "$(tail -25 "${TMP}/go.log")"
    summary
fi

# ── the fixture the CLI sections run against ────────────────────────────────
# A registered project of its own, with its own database and cache root, so
# every path below is one this suite created and nothing reads the operator's.
# `worktree sandbox` composes its answer from three things — the worktree dir's
# basename, the enclosing project's self_dev flag, and the cache root — and all
# three are set here rather than inherited.
REPO="${TMP}/repo"
PLAIN="${TMP}/plain"
export XDG_CONFIG_HOME="${TMP}/xdg"
export XDG_CACHE_HOME="${TMP}/cache"
export PATH="${WT}/bin:${PATH}"
mkdir -p "${REPO}" "${PLAIN}" "${XDG_CONFIG_HOME}/endless"

for repo in "${REPO}" "${PLAIN}"; do
    git -C "${repo}" init -q
    git -C "${repo}" config user.email verify@test
    git -C "${repo}" config user.name verify
    git -C "${repo}" commit -q --allow-empty -m "initial commit"
    mkdir -p "${repo}/.endless/worktrees/e-1428"
done
# One project sandboxes its worktrees; the other is any downstream project
# using endless as a tool, and has no sandbox to name.
printf '{"self_dev": true}\n' >"${REPO}/.endless/config.json"
printf '{}\n' >"${PLAIN}/.endless/config.json"
# A second task in the sandboxing project whose sandbox was never provisioned.
mkdir -p "${REPO}/.endless/worktrees/e-1429"
# ...and the one that was.
mkdir -p "${XDG_CACHE_HOME}/endless/sandboxes/e-1428"

printf '{"roots": ["%s"]}\n' "${TMP}" >"${XDG_CONFIG_HOME}/endless/config.json"

# E <dir> <args...> — the worktree's CLI, run from <dir>.
E() { local dir="$1"; shift; ( cd "${dir}" && uv run --project "${WT}" endless "$@" 2>&1 ); }

E "${REPO}" project register "${REPO}" --name probe --label Probe \
    --desc d --lang Go --status active >/dev/null 2>&1 \
    || setup_error "could not register the fixture project"
E "${PLAIN}" project register "${PLAIN}" --name plain --label Plain \
    --desc d --lang Go --status active >/dev/null 2>&1 \
    || setup_error "could not register the non-self-dev fixture project"

# ── 2. the sandbox path is retrievable, and only on request ─────────────────
# Deliverable A. Removing a path from the output is only half of it: there is
# one real use for this one — pointing a SQL client at a worktree's database —
# and it has to be answerable without reading the source.
section "2. endless worktree sandbox"

assert_contains "the verb is listed under \`worktree\`" \
    "sandbox" "$(E "${REPO}" worktree --help)"

assert_eq "a task's worktree resolves to its sandbox, as an absolute path" \
    "${XDG_CACHE_HOME}/endless/sandboxes/e-1428" \
    "$(E "${REPO}" worktree sandbox E-1428)"

assert_eq "the bare-number form resolves the same directory" \
    "${XDG_CACHE_HOME}/endless/sandboxes/e-1428" \
    "$(E "${REPO}" worktree sandbox 1428)"

# Every refusal below prints a path NOWHERE, which is the point: a plausible
# directory nothing ever wrote to is worse than an answer of "there is none".
out="$(E "${REPO}" worktree sandbox E-999999)"
assert_contains "a task with no worktree is refused, and named" \
    "No endless-managed worktree for E-999999" "${out}"

out="$(E "${REPO}" worktree sandbox E-1429)"
assert_contains "an unprovisioned sandbox is refused..." \
    "has not been provisioned" "${out}"
assert_contains "...naming the command that provisions it" \
    "sandbox init --mode worktree e-1429" "${out}"

out="$(E "${PLAIN}" worktree sandbox E-1428)"
assert_contains "a project that does not sandbox its worktrees has no sandbox" \
    "does not sandbox its worktrees" "${out}"

out="$(E "${REPO}" worktree sandbox)"
assert_contains "outside a worktree and with no argument, it names the form that works" \
    "endless worktree sandbox E-<id>" "${out}"

# ── 3. nothing prints the cache path any more ───────────────────────────────
# The provisioner is silent on success; `test_provisioning_a_sandbox_is_silent`
# proves that by running it. These two prove the strings themselves are gone,
# so a future edit cannot reintroduce them by copy.
section "3. The cache path is gone from the default output"

assert_eq "no source line prints 'sandbox provisioned'" \
    "" "$(grep -rl 'sandbox provisioned' src/ 2>/dev/null || true)"

assert_eq "no click.echo spells out the sandboxes cache root" \
    "" "$(grep -rn 'click.echo' src/ 2>/dev/null | grep 'cache/endless/sandboxes' || true)"

# ── 4. the ghost helper is gone from the guide ──────────────────────────────
# Deliverable D. `eswt` was proposed, documented and never shipped; the guide
# listed it beside `esu` in four files, one of them calling it "planned, not
# yet shipped" — a framing that stopped being premature and became wrong.
section "4. eswt appears nowhere"

assert_eq "grep -rn eswt docs/ src/ finds nothing" \
    "" "$(grep -rn eswt docs/ src/ 2>/dev/null || true)"

assert_contains "the helper that DOES exist is still documented" \
    '| `esu`' "$(cat docs/guide/orchestration.md)"

# The generated cross-reference is rebuilt from docs/guide/help/*.md, so a
# hand-edit to index.md alone would silently revert on the next
# `just guide-index`. This proves the map files were the thing edited.
if uv run python -m endless.guide_map check >"${TMP}/guide.log" 2>&1; then
    report_pass "guide map check — the index block is in sync with its sources"
else
    report_fail "guide_map check" "exit 0" "$(tail -20 "${TMP}/guide.log")"
fi

summary
