#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2114 and records what was true when E-2114
# landed. Edit it only if you ARE E-2114. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2114 verification — `endless verb update` corrects a registered verb's
# category and definition in place.
#
# Before: `verb` had add, list and remove and nothing else, so correcting one
# meant remove-then-add. That round trip made you retype the definition,
# restate every --category and remember whether it was --machine-only, and a
# field you got wrong was silently REWRITTEN rather than left alone. It is how
# 'brainstorm' came to be registered as an action verb, which then made
# `task add "Brainstorm …" --type brainstorm` refuse its own title.
#
# After: `verb update <value>` takes --definition, --category (repeatable) and
# --machine-only, writes only the fields passed, and leaves every other field —
# including ones no flag covers — exactly as it found them.
#
# Everything below section 1 drives the real CLI as a subprocess against a real
# git-backed project and a real database in a temp XDG_CONFIG_HOME. Nothing is
# stubbed: the verbs.jsonl files are read back off disk, the refusals are the
# CLI's own exit codes, and the commit on main is read with git.
#
#   endless task verify E-2114
#
# Exit 0 on all-passed, 1 on any failure, 2 on setup error.

source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

WT="$(git rev-parse --show-toplevel)" || setup_error "not in a git repo"
cd "${WT}" || setup_error "cannot cd to ${WT}"

TMP="$(mktemp -d)" || setup_error "could not create a temp dir"
trap 'rm -rf "${TMP}"' EXIT

# ── 1. fail-fast unit gate ──────────────────────────────────────────────────
# The durable coverage lives in tests/, per .endless/tasks/CLAUDE.md:
# test_verb_update.py owns E-2114's layering, matching and validation cases,
# and the two neighbouring gate suites own what a miscategorized verb does to
# task creation — the reason this command exists. A failure there makes every
# drive below meaningless, so the suite stops rather than reporting a cascade.
section "1. Unit gate (fail fast)"

if uv run pytest -q \
        tests/test_verb_update.py \
        tests/test_verb_gate.py \
        tests/test_verb_category_gate.py \
        >"${TMP}/py.log" 2>&1; then
    report_pass "pytest verb update + verb/category gate regression"
else
    report_fail "pytest verb update + verb/category gate" "exit 0" \
        "$(tail -25 "${TMP}/py.log")"
    summary
fi

# Built from THIS worktree rather than taken off PATH: section 2 creates a task
# through the event pipeline, and an installed binary would prove something
# about a different tree.
mkdir -p "${TMP}/bin"
if go build -o "${TMP}/bin/endless-go" ./cmd/endless-go >"${TMP}/go.log" 2>&1; then
    report_pass "go build ./cmd/endless-go (the pipeline section 2 writes through)"
else
    report_fail "go build ./cmd/endless-go" "exit 0" "$(tail -25 "${TMP}/go.log")"
    summary
fi

# ── fixture: a real config dir, a real git project, a real projects row ─────
# XDG_CONFIG_HOME points at ${TMP}/cfg, so the machine layer is ${TMP}/cfg/endless
# and the database beside it. The runner already replaced the real HOME and
# XDG_CONFIG_HOME; this narrows them again to a directory this suite seeds.
CFG_HOME="${TMP}/cfg"
CFG="${CFG_HOME}/endless"
PROJ="${TMP}/proj"
PROJ_VERBS="${PROJ}/.endless/verbs.jsonl"
MACHINE_VERBS="${CFG}/verbs.jsonl"
mkdir -p "${CFG}" "${PROJ}/.endless" "${TMP}/home"

printf '{"name": "e2114"}\n' >"${PROJ}/.endless/config.json"
git -C "${PROJ}" init -q -b main            >/dev/null 2>&1 || setup_error "git init failed"
git -C "${PROJ}" config user.email t@e.com  >/dev/null 2>&1
git -C "${PROJ}" config user.name  T        >/dev/null 2>&1
git -C "${PROJ}" config commit.gpgsign false >/dev/null 2>&1
git -C "${PROJ}" add .endless/config.json   >/dev/null 2>&1
git -C "${PROJ}" commit -qm init            >/dev/null 2>&1 || setup_error "git commit failed"

cat >"${TMP}/seed.py" <<'PY'
import os
from pathlib import Path
from endless import config
config.set_db_context(Path(os.environ["E2114_CFG"]))
from endless import db
db.execute(
    "INSERT INTO projects (id, name, path, status) VALUES (1, 'e2114', ?, 'active')",
    (os.environ["E2114_PROJ"],),
)
PY

E2114_CFG="${CFG}" E2114_PROJ="${PROJ}" uv run python "${TMP}/seed.py" \
    >"${TMP}/seed.log" 2>&1 \
    || setup_error "could not seed the fixture database: $(tail -5 "${TMP}/seed.log")"

# e — one `endless` invocation from inside the fixture project, against the
# fixture config. ENDLESS_SESSION_ID is cleared so nothing routes to a worktree
# sandbox database, and --project pins uv to THIS worktree's source while cwd
# sits in the temp project.
e() {
    ( cd "${PROJ}" && env -u ENDLESS_SESSION_ID -u CLAUDECODE \
        HOME="${TMP}/home" XDG_CONFIG_HOME="${CFG_HOME}" \
        PATH="${TMP}/bin:${PATH}" \
        uv run --project "${WT}" endless "$@" 2>&1 )
}

# field <file> <verb> <key> — one field of one verb, read straight off a
# verbs.jsonl line, or MISSING when the verb or the field is not there.
field() {
    E2114_FILE="$1" E2114_VERB="$2" E2114_KEY="$3" uv run python - <<'PY' 2>/dev/null
import json, os, sys
path = os.environ["E2114_FILE"]
want, key = os.environ["E2114_VERB"], os.environ["E2114_KEY"]
try:
    lines = open(path).read().splitlines()
except OSError:
    print("MISSING"); sys.exit(0)
for line in lines:
    if not line.strip():
        continue
    entry = json.loads(line)
    if entry.get("value") == want:
        print(json.dumps(entry[key]) if key in entry else "MISSING")
        break
else:
    print("MISSING")
PY
}

head_of() { git -C "${PROJ}" rev-parse HEAD; }

e verb add brainstorm --definition "to generate ideas freely" >"${TMP}/add.log" 2>&1 \
    || setup_error "fixture verb add failed: $(tail -5 "${TMP}/add.log")"

# ── 2. the live case, end to end ────────────────────────────────────────────
# 'brainstorm' registered without --category reads back as an action verb, and
# the E-1658 creation gate refuses a brainstorm-typed task led by it. One
# `verb update` is the whole fix.
section "2. The live case: a verb registered in the wrong category"

out="$(e task add 'Brainstorm the storage layout' --type brainstorm)"
assert_contains "a brainstorm task led by an action-categorized 'brainstorm' is refused" \
    "accepts only investigation verbs" "${out}"

out="$(e verb update brainstorm --category investigation)"
assert_contains "verb update reports the layers and the field it changed" \
    "Updated in project + machine: verb='brainstorm' (category)" "${out}"

out="$(e task add 'Brainstorm the storage layout' --type brainstorm)"
assert_contains "the same task is accepted after the correction" \
    "Brainstorm the storage layout" "${out}"

# ── 3. only what you pass changes ───────────────────────────────────────────
# The property the whole command exists for. Under remove-then-add each of
# these fields had to be retyped, and whatever you failed to retype was
# rewritten rather than left alone.
section "3. Omitted fields are left exactly as they were"

assert_eq "an omitted --definition survives a --category change" \
    '"to generate ideas freely"' "$(field "${PROJ_VERBS}" brainstorm definition)"

e verb update brainstorm --definition "to generate ideas without filtering" >/dev/null
assert_eq "an omitted --category survives a --definition change" \
    '["investigation"]' "$(field "${PROJ_VERBS}" brainstorm category)"
assert_eq "the passed --definition is what landed" \
    '"to generate ideas without filtering"' "$(field "${PROJ_VERBS}" brainstorm definition)"

e verb update brainstorm --category action --category investigation >/dev/null
assert_eq "--category is a set: repeating it builds the dual" \
    '["action", "investigation"]' "$(field "${PROJ_VERBS}" brainstorm category)"

e verb update brainstorm --category investigation >/dev/null
assert_eq "--category REPLACES the set rather than appending to it" \
    '["investigation"]' "$(field "${PROJ_VERBS}" brainstorm category)"

out="$(e verb update brainstorm --category investigation)"
assert_contains "re-passing the values it already holds is a reported no-op" \
    "Already set (no change)" "${out}"

# ── 4. built-ins: the correction is materialized where it is scoped ─────────
# 'research' ships in DEFAULT_VERBS and is seeded into the MACHINE verbs.jsonl
# on first run, so it is present in a file — following presence would send a
# project-scoped correction machine-wide. The write follows SCOPE instead, and
# the entry it creates carries only the corrected field.
section "4. Correcting a built-in verb"

assert_eq "'research' starts with no project entry" \
    "MISSING" "$(field "${PROJ_VERBS}" research category)"

out="$(e verb update research --category action)"
assert_contains "the created override is named, not left to be discovered" \
    "New override for 'research' in the project layer" "${out}"
assert_eq "the override carries the corrected field" \
    '["action"]' "$(field "${PROJ_VERBS}" research category)"
assert_eq "and carries nothing else" \
    "MISSING" "$(field "${PROJ_VERBS}" research definition)"

out="$(e verb list --json)"
assert_contains "the built-in definition still resolves through the override" \
    '"definition": "to investigate systematically"' \
    "$(printf '%s' "${out}" | tr -d '\n' | grep -o '{[^{]*"value": "research"[^}]*}')"

# ── 5. layer scope and the commit on main ───────────────────────────────────
section "5. Layer scope"

before="$(head_of)"
out="$(e verb update brainstorm --definition "to generate ideas freely" --machine-only)"
assert_eq "--machine-only writes the machine layer alone" \
    "• Updated in machine: verb='brainstorm' (definition)" "${out}"
assert_eq "…leaving the project definition untouched" \
    '"to generate ideas without filtering"' "$(field "${PROJ_VERBS}" brainstorm definition)"
assert_eq "…and main's HEAD where it was" "${before}" "$(head_of)"

before="$(head_of)"
e verb update brainstorm --definition "to generate ideas freely" >/dev/null
assert_eq "a project write commits verbs.jsonl on main under its own subject" \
    "Endless: update verb 'brainstorm'" \
    "$(git -C "${PROJ}" log -1 --format=%s)"
assert_eq "…advancing HEAD" "true" \
    "$([[ "$(head_of)" != "${before}" ]] && echo true || echo false)"
assert_eq "…and leaving the file clean" "" \
    "$(git -C "${PROJ}" status --porcelain -- .endless/verbs.jsonl)"

# The fresh-clone shape: verbs.jsonl arrives with the checkout, so a project
# verb is in the committed project file and absent from this machine's. The
# other layer is kept in step only when it ALREADY carries the verb, so
# correcting one of these must not install it machine-wide.
printf '%s\n' '{"value": "winnow", "definition": "to narrow by discarding"}' >>"${PROJ_VERBS}"
git -C "${PROJ}" add .endless/verbs.jsonl >/dev/null 2>&1
git -C "${PROJ}" commit -qm "clone-shaped verb" >/dev/null 2>&1

out="$(e verb update winnow --category investigation)"
assert_eq "a project-only verb is corrected in the project layer alone" \
    "• Updated in project: verb='winnow' (category)" "${out}"
assert_eq "…and is not copied into the machine layer as a side effect" \
    "MISSING" "$(field "${MACHINE_VERBS}" winnow category)"

# ── 6. refusals ─────────────────────────────────────────────────────────────
# Both are recoverable mistakes, so both name the way out rather than only the
# problem.
section "6. Refusals"

out="$(e verb update nonverbword --category action)"
assert_contains "an unknown verb is refused" "No verb matched" "${out}"
assert_contains "…and pointed at the command that registers one" \
    "endless verb add" "${out}"

out="$(e verb update brainstorm)"
assert_contains "an update with no field to change is refused" \
    "Nothing to update" "${out}"
assert_contains "…naming the flags that would give it one" \
    "--definition and/or --category" "${out}"

summary
