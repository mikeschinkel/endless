#!/usr/bin/env bash
#
# Shared guard for per-task verification suites. Source it, don't run it:
#
#     source "$(dirname "${BASH_SOURCE[0]}")/../_guard.sh"
#
# _harness.sh sources this as its FIRST act, so a suite using the harness gets
# the guard transitively and nobody has to remember two lines. A suite carries
# it exactly ONCE — directly or through the harness, never both, or the refusal
# doubles.
#
# It refuses in two cases. Neither is a claim the caller makes about itself,
# and that is the whole design. The marker this replaces (ENDLESS_VERIFY_RUN)
# was exactly such a claim: `export ENDLESS_VERIFY_RUN=anything` satisfied it,
# nothing ever read its value, and its presence stood in for enforcement that
# was never built. A check you can satisfy by lying is not a check.
#
#   1. NOT YOURS. A suite may run only from inside its own task's worktree.
#      Both facts come from the running file's own absolute path — a suite at
#      .endless/worktrees/e-102/.endless/tasks/e-101/verify.sh is E-101's
#      suite sitting in E-102's checkout — so defeating this means physically
#      moving files, not setting a variable.
#
#   2. NOT ISOLATED. Ownership alone still lets a suite's OWNER run it by hand
#      with a real $HOME, which is how a suite once wrote a session row into
#      the user's main database. So this asks whether the real
#      config is reachable rather than whether the caller says it isn't. The
#      only way to satisfy it is to actually be isolated — at which point the
#      suite cannot do the damage the check exists to prevent.
#
# Both fail OPEN on a question the path cannot answer. This guard exists to
# stop a mistake somebody is making, not to be a precondition for working: a
# false refusal would block the one person doing the right thing, on the
# machine where the suite was written.
#
# See .endless/tasks/CLAUDE.md for the rules these suites live under.

# The SUITE is the outermost frame, whatever the source depth: the suite sources
# _harness.sh, which sources this. Indexing from the end rather than assuming a
# depth means a suite that sources the guard directly works identically.
_endless_guard_suite_path() {
    local -a src=("${BASH_SOURCE[@]}")
    printf '%s' "${src[$((${#src[@]} - 1))]}"
}

# _endless_guard_refuse prints a refusal and leaves the process. `exit` is right
# even though we are sourced: the caller IS the suite, and there is no
# continuing past a refusal.
_endless_guard_refuse() {
    printf '%s\n' "$@" >&2
    exit 2
}

_endless_guard() {
    local suite dir owner here cfg

    suite="$(_endless_guard_suite_path)"
    dir="$(cd -- "$(dirname -- "${suite}")" 2>/dev/null && pwd -P)" || return 0

    # ── 1. not yours ────────────────────────────────────────────────────────
    # The suite's own task, from the directory it lives in, and the task whose
    # worktree that directory is inside. A path that answers neither question
    # is not one this check has an opinion about: a project that does not use
    # worktrees, and a task whose worktree was reaped, both run their suite
    # from the plain checkout, and the runner already falls back to cwd for
    # exactly those two cases.
    owner=""
    [[ "${dir}" =~ /\.endless/tasks/e-([0-9]+)$ ]] && owner="${BASH_REMATCH[1]}"
    here=""
    [[ "${dir}" =~ /\.endless/worktrees/e-([0-9]+)(/|$) ]] && here="${BASH_REMATCH[1]}"

    if [[ -n "${owner}" && -n "${here}" && "${owner}" != "${here}" ]]; then
        _endless_guard_refuse \
            "refusing to run E-${owner}'s verification suite from E-${here}'s worktree." \
            "" \
            "A verification suite is a land-time proof of ONE task at ONE moment, not a" \
            "regression suite. Its fixtures and assertions were pinned to the tree that" \
            "existed when it landed, so whatever it reports now — pass OR fail — says" \
            "nothing about your work." \
            "" \
            "  this suite belongs to: E-${owner}" \
            "  you are in:            E-${here}'s worktree" \
            "" \
            "Instead:" \
            "  • verify your own task:  endless task verify E-${here}" \
            "  • coverage that must survive a land belongs in the project's own test" \
            "    suite, not in another task's land-time proof." \
            "" \
            "The rules for this directory, in full: .endless/tasks/CLAUDE.md"
    fi

    # ── 2. not isolated ─────────────────────────────────────────────────────
    # `endless task verify` replaces HOME and XDG_CONFIG_HOME with a per-run
    # temp dir, so under the runner neither spelling of the config resolves and
    # this passes. Run by hand, the real one is right there. Both spellings are
    # checked because `--db main` deliberately ignores XDG to escape a
    # worktree's sandbox, so testing only one leaves the other reachable.
    for cfg in "${XDG_CONFIG_HOME:-${HOME:-}/.config}/endless" "${HOME:-}/.config/endless"; do
        [[ -d "${cfg}" ]] || continue
        _endless_guard_refuse \
            "This verification suite must be run through the verify runner:" \
            "" \
            "    endless task verify E-${owner:-<id>}" \
            "" \
            "An Endless config is reachable from here:" \
            "" \
            "    ${cfg}" \
            "" \
            "so this is a direct run, outside the isolation the runner builds. The runner" \
            "replaces HOME and XDG_CONFIG_HOME with a per-run temp dir precisely so a" \
            "suite cannot reach a config or a database that outlives it. One run that" \
            "way wrote into the main database and took down session tracking." \
            "See .endless/tasks/CLAUDE.md."
    done
    return 0
}

_endless_guard
