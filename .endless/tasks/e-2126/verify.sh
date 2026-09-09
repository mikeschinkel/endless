#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2126 and records what was true when E-2126
# landed. Edit it only if you ARE E-2126. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2126 verification — "Return populated field bodies from task show --json,
# and surface children counts".
#
# WHAT LANDED
#   `endless task show <id> --json` returned `"analysis": null` beside
#   `"analysis_chars": 6503`. The display flags that gate the HUMAN renderer
#   (--analysis, --text, --outcome, --all-fields, --no-description) were also
#   gating the machine format, so a consumer that did not know to pass a flag
#   read a populated task as an empty one. The terminal at least printed
#   `Analysis: 6503 chars (--analysis to display)`; JSON printed nothing, and
#   `null` therefore meant both "empty" and "withheld" — indistinguishable for
#   `description`, which had no `_chars` companion at all.
#
#   --json now carries every body unconditionally; `<field>_chars` is universal,
#   always an integer, 0 when empty; `--brief[=N]` is the opt-in light payload
#   and TRUNCATES rather than omitting; and children are advertised as a count
#   in every format, with the `— Children —` section moved ahead of the long
#   prose that could push it off-screen.
#
# THE CLAIMS
#   C1  THE REPORTED DEFECT IS GONE. A populated analysis comes back as its
#       body from a bare `--json`, with no flag passed.
#   C2  DISPLAY FLAGS BELONG TO THE DISPLAY. No combination of them changes the
#       JSON payload — including --no-description, which used to lose the
#       description with nothing to contradict it.
#   C3  `null` MEANS EXACTLY ONE THING: THE FIELD IS EMPTY. The biconditional
#       `<field>_chars == 0` iff `<field>` is null holds across the flag
#       matrix, and no count key is ever null. This is the fix; the rest is
#       delivery.
#   C4  --brief TRUNCATES, NEVER OMITS. A populated field stays a string, ends
#       in a single `…`, and its `_chars` still reports the STORED length. A
#       field at or under the limit carries no ellipsis.
#   C5  CHILDREN ARE COUNTED EVERYWHERE. children_count / children_by_type in
#       --json, the two key=value lines in --llm, and a `Children:` header line
#       in the human render — in all three shapes, and absent when childless.
#       The child LIST stays gated behind --children.
#   C6  THE `— Children —` SECTION HOLDS SLOT 2, whichever of its conditional
#       neighbours happen to render.
#   C7  THE DEFAULTS A PERSON READS ARE UNCHANGED. The human placeholder is
#       still what a bare `task show` prints, and --llm's default output gained
#       nothing but the children lines.
#
# ISOLATION
#   Every CLI assertion runs the real installed entry point against a throwaway
#   project under a temp HOME/XDG_CONFIG_HOME — its own database, its own git
#   repo. Nothing here reads or writes this worktree's state or any real
#   endless database.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

WT="$(git rev-parse --show-toplevel)" || setup_error "not in a git repo"
cd "${WT}" || setup_error "cannot cd to ${WT}"

TMP="$(mktemp -d)" || setup_error "cannot make a temp dir"
trap 'rm -rf "${TMP}"' EXIT

for f in src/endless/task_cmd.py src/endless/cli.py; do
    [[ -f "${f}" ]] || setup_error "missing ${f}"
done

# ── Fail-fast: this task's own unit tests ─────────────────────────────────────
#
# test_task_show_payload.py is E-2126's own. The other three own assertions this
# change had to invert or could have broken: the two that pinned the JSON gating
# (E-1601/E-1599) and the one that pins --children listing every child (E-1911).
section "Unit tests (fail fast)"

if uv run pytest -q \
        tests/test_task_show_payload.py \
        tests/test_outcome.py \
        tests/test_analysis_show.py \
        tests/test_show_children.py \
        >"${TMP}/py.log" 2>&1; then
    report_pass "pytest — the payload contract, plus the gating tests it inverts"
else
    report_fail "pytest tests/test_task_show_payload.py + test_outcome.py + test_analysis_show.py + test_show_children.py" \
        "exit 0" "$(tail -40 "${TMP}/py.log")"
    summary
fi

# ── A throwaway project, driven through the real CLI ──────────────────────────
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

# The real entry point, on its own database. cwd is the throwaway project, so
# the --db gate resolves there rather than to this self-dev worktree.
es() {
    ( cd "${PROJ}" &&
      env -u ENDLESS_SESSION_ID -u ENDLESS_DB_CHOICE \
          HOME="${ES_HOME}" \
          XDG_CONFIG_HOME="${ES_HOME}/.config" \
          ENDLESS_AUTO_MIGRATE=1 \
          ENDLESS_NO_TRIAGE=1 \
          "${ENDLESS}" "$@" )
}

# probe <json> <expr> — evaluate a Python expression against the parsed payload,
# bound as `d`. A malformed payload fails loudly here rather than as a puzzling
# empty string in an assertion.
probe() {
    printf '%s' "$1" | python3 -c \
        "import json,sys; d=json.load(sys.stdin); print($2)" 2>&1
}

if es project register . --name probe --infer >"${TMP}/reg.log" 2>&1; then
    report_pass "registered a throwaway project on its own database"
else
    setup_error "cannot register the throwaway project: $(tail -20 "${TMP}/reg.log")"
fi

python3 -c "print('A' * 900, end='')" >"${TMP}/analysis.md"
python3 -c "print('T' * 900, end='')" >"${TMP}/text.md"

# EPIC: five children of two types, and every long field populated but outcome.
es task add "Add the parent epic" --type epic --no-session \
    --analysis-file "${TMP}/analysis.md" --text-file "${TMP}/text.md" \
    >"${TMP}/seed.log" 2>&1 || setup_error "seed failed: $(tail -20 "${TMP}/seed.log")"
EPIC="E-1"
es task update "${EPIC}" --description "A description that is not the title" \
    --keep-status --no-session >>"${TMP}/seed.log" 2>&1 \
    || setup_error "seed failed: $(tail -20 "${TMP}/seed.log")"

for n in 1 2 3; do
    es task add "Add child todo ${n}" --type todo --parent "${EPIC}" --no-session \
        >>"${TMP}/seed.log" 2>&1 || setup_error "seed failed (todo ${n})"
done
for n in 1 2; do
    es task add "Fix child bug ${n}" --type bugfix --parent "${EPIC}" --no-session \
        >>"${TMP}/seed.log" 2>&1 || setup_error "seed failed (bugfix ${n})"
done

# LONE: no children, no analysis, no text, no outcome — the all-empty case.
es task add "Add the lonely task" --no-session >>"${TMP}/seed.log" 2>&1 \
    || setup_error "seed failed (lonely)"
LONE="E-7"

EPIC_JSON="$(es task show "${EPIC}" --json)"
LONE_JSON="$(es task show "${LONE}" --json)"

assert_eq "the fixture really is an epic with five children of two types" \
    "5 bugfix:2 todo:3" \
    "$(probe "${EPIC_JSON}" \
        "str(d['children_count']) + ' ' + ' '.join(sorted(f'{k}:{v}' for k, v in d['children_by_type'].items()))")"

# ── C1 — the reported defect ──────────────────────────────────────────────────
section "C1: a bare --json returns the body it used to null out"

assert_eq "analysis comes back as its 900-character body, no flag passed" "900" \
    "$(probe "${EPIC_JSON}" "len(d['analysis'] or '')")"
assert_eq "analysis_chars agrees with it" "900" \
    "$(probe "${EPIC_JSON}" "d['analysis_chars']")"
assert_eq "text comes back too" "900" \
    "$(probe "${EPIC_JSON}" "len(d['text'] or '')")"
assert_eq "description comes back" "A description that is not the title" \
    "$(probe "${EPIC_JSON}" "d['description']")"

# ── C2 — display flags belong to the display ──────────────────────────────────
section "C2: no display flag changes the JSON payload"

for combo in "--all-fields" "--analysis" "--text" "--outcome" "--children" \
             "--no-description"; do
    # --children legitimately ADDS the child list; compare the body fields only.
    flagged="$(es task show "${EPIC}" --json ${combo})"
    assert_eq "${combo} leaves the four bodies and their counts identical" \
        "$(probe "${EPIC_JSON}" "[d[k] for k in ('description','analysis','text','outcome')] + [d[k+'_chars'] for k in ('description','analysis','text','outcome')]")" \
        "$(probe "${flagged}" "[d[k] for k in ('description','analysis','text','outcome')] + [d[k+'_chars'] for k in ('description','analysis','text','outcome')]")"
done

assert_eq "--no-description still hides it from the HUMAN render" "absent" \
    "$(es task show "${EPIC}" --no-description --no-color \
        | grep -q "A description that is not the title" && echo present || echo absent)"

# ── C3 — null means empty, and only empty ─────────────────────────────────────
section "C3: <field>_chars == 0 if and only if <field> is null"

for combo in "" "--all-fields" "--no-description" "--analysis" "--text" \
             "--outcome" "--children" "--brief" "--brief=40" \
             "--all-fields --brief"; do
    for tid in "${EPIC}" "${LONE}"; do
        payload="$(es task show "${tid}" --json ${combo})"
        assert_eq "${tid} ${combo:-(no flags)}: the biconditional holds on all four fields" \
            "True" \
            "$(probe "${payload}" "all((d[k+'_chars'] == 0) == (d[k] is None) for k in ('description','analysis','text','outcome'))")"
        assert_eq "${tid} ${combo:-(no flags)}: no count key is null" "True" \
            "$(probe "${payload}" "all(isinstance(d[k+'_chars'], int) for k in ('description','analysis','text','outcome'))")"
    done
done

assert_eq "an unset outcome is null, not an empty string" "None" \
    "$(probe "${EPIC_JSON}" "d['outcome']")"
assert_eq "and its count is 0, not null" "0" \
    "$(probe "${EPIC_JSON}" "d['outcome_chars']")"

# ── C4 — --brief truncates rather than omitting ───────────────────────────────
section "C4: --brief previews, and a preview is still a string"

BRIEF_JSON="$(es task show "${EPIC}" --json --brief)"
assert_eq "--brief cuts to the 256-character default plus one ellipsis" "257" \
    "$(probe "${BRIEF_JSON}" "len(d['analysis'])")"
assert_eq "the preview ends in a single ellipsis" "True" \
    "$(probe "${BRIEF_JSON}" "d['analysis'].endswith('…') and not d['analysis'].endswith('……')")"
assert_eq "analysis_chars still reports the STORED length, not the preview's" "900" \
    "$(probe "${BRIEF_JSON}" "d['analysis_chars']")"

BRIEF40_JSON="$(es task show "${EPIC}" --json --brief=40)"
assert_eq "--brief=N honours N" "41" \
    "$(probe "${BRIEF40_JSON}" "len(d['analysis'])")"
assert_eq "a field at or under N carries no ellipsis" "False" \
    "$(probe "${BRIEF40_JSON}" "d['description'].endswith('…')")"
assert_eq "a populated field is never null under --brief" "True" \
    "$(probe "${BRIEF40_JSON}" "all(isinstance(d[k], str) for k in ('description','analysis','text'))")"

assert_contains "--brief wins over --all-fields in the human render" "…" \
    "$(es task show "${EPIC}" --all-fields --brief=40 --no-color)"
assert_not_contains "…so the full 900-character analysis is not printed" \
    "$(python3 -c "print('A' * 300, end='')")" \
    "$(es task show "${EPIC}" --all-fields --brief=40 --no-color)"
assert_contains "--brief alone reveals a gated field as a preview" "— Analysis —" \
    "$(es task show "${EPIC}" --brief=40 --no-color)"

assert_contains "--brief=0 is refused, naming the unit" "character count" \
    "$(es task show "${EPIC}" --brief=0 2>&1 || true)"
assert_contains "a swallowed task id names the two fixes, not an integer error" \
    "looks like a task id" \
    "$(es task show --brief "${EPIC}" 2>&1 || true)"

# ── C5 — children counted in every format ─────────────────────────────────────
section "C5: children are advertised as a count everywhere"

assert_eq "--json: children_count" "5" \
    "$(probe "${EPIC_JSON}" "d['children_count']")"
assert_eq "--json: children_by_type is ordered by descending count" \
    "[('todo', 3), ('bugfix', 2)]" \
    "$(probe "${EPIC_JSON}" "list(d['children_by_type'].items())")"
assert_eq "--json: the child LIST stays gated behind --children" "False" \
    "$(probe "${EPIC_JSON}" "'children' in d")"
assert_eq "--json: --children adds it" "5" \
    "$(probe "$(es task show "${EPIC}" --json --children)" "len(d['children'])")"

assert_eq "--json: a childless task says 0, not nothing" "0" \
    "$(probe "${LONE_JSON}" "d['children_count']")"
assert_eq "--json: and {} for the breakdown" "{}" \
    "$(probe "${LONE_JSON}" "d['children_by_type']")"

assert_contains "--llm: children_count" "children_count=5" \
    "$(es task show "${EPIC}" --llm)"
assert_contains "--llm: children_by_type" "children_by_type=todo:3,bugfix:2" \
    "$(es task show "${EPIC}" --llm)"
assert_not_contains "--llm: nothing emitted for a childless task" "children_count" \
    "$(es task show "${LONE}" --llm)"

assert_contains "human: the mixed-type header line" "5 tasks (3 todo, 2 bugfix)" \
    "$(es task show "${EPIC}" --no-color)"
assert_contains "human: the line survives --children, which only adds the list" \
    "5 tasks (3 todo, 2 bugfix)" "$(es task show "${EPIC}" --children --no-color)"
assert_not_contains "human: omitted entirely on a childless task" "Children:" \
    "$(es task show "${LONE}" --no-color)"
assert_contains "human: --children on a childless task still answers directly" \
    "(none)" "$(es task show "${LONE}" --children --no-color)"

# A single-type parent, for the third shape of the header line.
es task add "Add the single-type parent" --type epic --no-session \
    >>"${TMP}/seed.log" 2>&1 || setup_error "seed failed (single-type parent)"
SOLO="E-8"
for n in 1 2 3; do
    es task add "Add solo child ${n}" --type todo --parent "${SOLO}" --no-session \
        >>"${TMP}/seed.log" 2>&1 || setup_error "seed failed (solo child ${n})"
done
assert_contains "human: the single-type header line names the type, not 'tasks'" \
    "3 todo" "$(es task show "${SOLO}" --no-color)"
assert_not_contains "…and does not pluralize into the mixed form" "3 tasks" \
    "$(es task show "${SOLO}" --no-color)"

# ── C6 — the Children section holds slot 2 ────────────────────────────────────
section "C6: — Children — renders in slot 2, whichever neighbours exist"

# sections <args...> — the `— Title —` headers, in order, space-joined.
sections() {
    es task show "$@" --no-color \
        | sed -n 's/^— \(.*\) —$/\1/p' | tr '\n' ' ' | sed 's/ $//'
}

assert_eq "everything renders: Description Children Analysis Text Outcome order" \
    "Description Children Analysis Text Outcome" \
    "$(es task update "${EPIC}" --outcome "an outcome body" --keep-status --no-session \
        >>"${TMP}/seed.log" 2>&1; sections "${EPIC}" --all-fields)"

assert_eq "Description suppressed: Children still leads the sections" \
    "Children Analysis" \
    "$(sections "${EPIC}" --no-description --analysis --children)"

assert_eq "no long-field flags at all: Description then Children" \
    "Description Children" \
    "$(sections "${EPIC}" --children)"

assert_eq "trailing sections only: Children still precedes them" \
    "Children Text Outcome" \
    "$(sections "${EPIC}" --no-description --text --outcome --children)"

assert_eq "Children as the ONLY section that renders" "Children" \
    "$(sections "${LONE}" --children --no-description)"

# ── C7 — the defaults a person reads are unchanged ────────────────────────────
section "C7: the human and --llm defaults did not move"

assert_contains "the human placeholder still gates the body" \
    "900 chars (--analysis to display)" "$(es task show "${EPIC}" --no-color)"
assert_not_contains "…and the body is still not printed" \
    "$(python3 -c "print('A' * 300, end='')")" "$(es task show "${EPIC}" --no-color)"

# --llm's default, line by line, on the childless task: no children lines, so
# this is the pre-E-2126 output verbatim.
assert_eq "--llm on a childless task emits no children lines and no bodies" \
    "# E-7 Add the lonely task|project=probe|type=todo phase=now status=untriaged" \
    "$(es task show "${LONE}" --llm | grep -v '^created\|^updated\|^touched_by\|^links' | tr '\n' '|' | sed 's/|$//')"

assert_contains "--llm still collapses a populated analysis to a bare count" \
    "analysis_chars=900" "$(es task show "${EPIC}" --llm)"
assert_not_contains "…and --llm's default prints no analysis body" \
    "$(python3 -c "print('A' * 300, end='')")" "$(es task show "${EPIC}" --llm)"
assert_contains "--llm --brief upgrades that count to a readable preview" \
    "## Analysis" "$(es task show "${EPIC}" --llm --brief=40)"
assert_not_contains "…and the bare count gives way to it" "analysis_chars=" \
    "$(es task show "${EPIC}" --llm --brief=40)"

summary
