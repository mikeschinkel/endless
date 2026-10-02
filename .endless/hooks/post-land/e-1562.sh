#!/usr/bin/env bash
#
# E-1562 post-land: move every `## Justification` section out of task notes into
# the task's own `justification` content.
#
# Endless runs this once, right after E-1562's merge advances main, with:
#   - cwd     = the main checkout
#   - argv[1] = the main checkout's absolute path
#
# Why a script and not a schema change: the move is ledger-native on purpose.
# Each task's move is one ordinary task.fields_updated event, so a rebuild
# replays it like any other change — a schema change would write the database
# behind the ledger's back, and the next rebuild would put the headings back.
# By now the land has rebuilt the installed endless-go from main and the
# editable Python reads main's source, so both halves know the content kind.
#
# Idempotent: a moved section is no longer in notes, so a second run finds
# nothing and says "Moved 0". A task that already holds a justification is
# listed as skipped, never overwritten. Re-running after a partial failure
# finishes the job.

set -u

main() {
    local main_root="${1:?usage: e-1562.sh <main-checkout-path>}"
    cd "${main_root}" || return 1

    echo "e-1562 post-land: moving ## Justification sections out of notes"
    if ! endless db move-section --from notes --to justification \
        --db main --no-session; then
        echo "e-1562 post-land: the move failed; re-run to finish:" \
            "${main_root}/.endless/hooks/post-land/e-1562.sh ${main_root}" >&2
        return 1
    fi
    return 0
}

main "$@"
