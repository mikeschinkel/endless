Decide whether the fix is a per-type allowed-verb set, a per-verb allowed-type set, or a softer warn-and-confirm — the verb catalog already carries definitions that could seed the mapping (decide/evaluate/compare for brainstorm; add/build/remove for todo).

Note --type can change after filing, so the check has to run on `task update --type` too, not just `task add`.
