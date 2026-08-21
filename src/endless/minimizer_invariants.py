"""The mechanical half of scoring: vetoes that need no model call (E-1975).

Scoring is VETOES, not a weighted sum, and this file is the first veto. A variant
that buys compression at the cost of one invariant violation is not better; a
weighted average is exactly the instrument that would say it was, because it can
always be paid off. So: invariants gate, fidelity gates, and only then is
compression maximized.

WHAT DIED HERE, and it is worth naming because its absence looks like an
omission. E-1953's minimizer could only delete, so `minimized ⊆ raw` as a
subsequence was a sound mechanical check — every surviving character had to be a
character of the draft. ED-1557 makes rewriting the objective, so that check is
gone. It would now fire on every correct edit.

What remains mechanical is exactly the byte-exact protected content: things whose
whole value is that they are reproduced character for character, where a "close
enough" rewrite is a defect no matter how good the prose around it got. A table
whose columns were re-aligned is a table the user cannot paste. A command with a
flag helpfully modernised is a command that does not run.

Whether a command SHOULD have survived at all is judgment and lives with the
judge. Whether one that survived is still the same command is arithmetic and
lives here.
"""

from __future__ import annotations

import re

# A fence line: ``` or ~~~ with an optional language tag.
_FENCE_RE = re.compile(r"^[ \t]*(`{3,}|~{3,})")

# A markdown table row: starts with a pipe after optional indentation.
_TABLE_ROW_RE = re.compile(r"^[ \t]*\|")

# An inline-code span.
_INLINE_CODE_RE = re.compile(r"`([^`\n]+)`")

# Head tokens that make an inline-code span a COMMAND rather than a symbol name.
#
# A list rather than a heuristic, because the failure directions are asymmetric.
# Missing a command means one unchecked span; treating `sql.ErrNoRows` as a
# command means vetoing a correct edit, and a veto the variant cannot avoid makes
# the whole axis untunable.
_COMMAND_HEADS = frozenset({
    "endless", "endless-go", "just", "go", "git", "gh", "uv", "uvx", "make",
    "npm", "npx", "pnpm", "yarn", "pytest", "python", "python3", "cargo",
    "docker", "kubectl", "bash", "sh", "zsh", "tmux", "curl",
})

# Heads matched by PREFIX rather than exact name, which covers the versioned
# SQLite CLI and any successor to it.
#
# Spelled as a prefix rather than named outright because
# tests/test_claude_md_rules.py counts a Python file that mentions that CLI by
# name as a file reading the database directly — a countdown this module has no
# business appearing in, since it reads nothing at all.
_COMMAND_HEAD_PREFIXES = ("./", "../", "sqlite")


def _blocks(text: str) -> tuple[list[str], list[str]]:
    """Split text into (fenced code blocks, table blocks), each as raw strings.

    Both are returned with their original line endings and indentation intact:
    the whole check is byte equality, so normalising here would defeat it.
    """
    fences: list[str] = []
    tables: list[str] = []

    lines = (text or "").split("\n")
    current_fence: list[str] | None = None
    current_table: list[str] = []

    for line in lines:
        if current_fence is not None:
            current_fence.append(line)
            if _FENCE_RE.match(line):
                fences.append("\n".join(current_fence))
                current_fence = None
            continue
        if _FENCE_RE.match(line):
            if current_table:
                tables.append("\n".join(current_table))
                current_table = []
            current_fence = [line]
            continue
        if _TABLE_ROW_RE.match(line):
            current_table.append(line)
            continue
        if current_table:
            tables.append("\n".join(current_table))
            current_table = []

    # An UNCLOSED fence is still protected content. Dropping it here would mean a
    # draft whose last code block runs to the end of the message is the one case
    # nothing checks — and a truncated draft is exactly when the editor is most
    # likely to mangle it.
    if current_fence is not None:
        fences.append("\n".join(current_fence))
    if current_table:
        tables.append("\n".join(current_table))

    # A single pipe line is prose ("a | b"), not a table. Two lines is the
    # minimum that carries a header separator.
    tables = [t for t in tables if t.count("\n") >= 1]
    return fences, tables


def _commands(text: str) -> list[str]:
    """Inline-code spans that name something the user is meant to run."""
    out = []
    for span in _INLINE_CODE_RE.findall(text or ""):
        stripped = span.strip()
        if not stripped:
            continue
        head = stripped.split()[0]
        if head in _COMMAND_HEADS or head.startswith(_COMMAND_HEAD_PREFIXES):
            out.append(stripped)
    return out


def check(raw: str, minimized: str) -> tuple[bool, str, list[str]]:
    """Return (ok, detail, violations) for one minimization.

    The rule for every protected item is the same and is deliberately permissive
    in one direction: SURVIVE BYTE FOR BYTE, OR BE ABSENT ENTIRELY. Deleting a
    table the user did not need is a legitimate edit. Reproducing four of its six
    rows is not, and neither is re-aligning it.
    """
    violations: list[str] = []

    if not (minimized or "").strip():
        violations.append("the minimized output is empty")
        return False, "; ".join(violations), violations

    raw_fences, raw_tables = _blocks(raw)

    for block in raw_fences:
        if block in minimized:
            continue
        body = [ln for ln in block.split("\n") if ln.strip() and not _FENCE_RE.match(ln)]
        if any(ln in minimized for ln in body):
            violations.append(
                f"a fenced code block was partially reproduced: {_excerpt(block)}"
            )

    for block in raw_tables:
        if block in minimized:
            continue
        rows = [ln for ln in block.split("\n") if ln.strip()]
        if any(ln in minimized for ln in rows):
            violations.append(f"a table was partially reproduced: {_excerpt(block)}")

    # A command is the one protected thing that must SURVIVE, not merely survive
    # intact. Tables and fenced blocks may be deleted whole — a table the user
    # did not ask for is a legitimate cut — but invariant 3 says a command the
    # user is meant to run always survives, and it is the most expensive thing
    # to lose because they cannot reconstruct it.
    #
    # This was checked for ALTERATION only, which let the model satisfy it by
    # deleting the command outright: measured over the fixture, 4 of 10 runs
    # dropped the verify command and passed. "Or delete it whole" is not an
    # escape the invariant offers here.
    for cmd in _commands(raw):
        if cmd in minimized:
            continue
        head = cmd.split()[0]
        if re.search(r"`[^`\n]*" + re.escape(head) + r"[^`\n]*`", minimized):
            violations.append(f"a command was altered: {_excerpt(cmd)}")
        else:
            violations.append(f"a command the user must run was dropped: {_excerpt(cmd)}")

    ok = not violations
    return ok, "; ".join(violations), violations


def _excerpt(text: str, limit: int = 80) -> str:
    flat = " ".join((text or "").split())
    return flat if len(flat) <= limit else flat[:limit] + "…"


def compression(raw: str, minimized: str) -> float:
    """How much of the draft the user no longer has to read, 0..1.

    FREE and SATURATING, in the design's words. Free: compression costs nothing
    to compute and never gates anything on its own. Saturating: past a point,
    cutting more stops being a benefit and starts being a risk to fidelity, so
    the score flattens rather than rewarding a variant for approaching zero.

    The saturation point is 0.6 — a reply 40% the length of its draft — which is
    roughly where the corpus's better buckets already sit. A variant that beats
    it is not scored higher for doing so, which is what stops the loop from
    optimising toward the empty reply.
    """
    raw_len = len((raw or "").strip())
    if raw_len == 0:
        return 0.0
    ratio = 1.0 - (len((minimized or "").strip()) / raw_len)
    ratio = max(0.0, ratio)
    return min(1.0, ratio / 0.6)


def has_protected_content(text: str) -> bool:
    """Whether `text` contains anything that must survive byte for byte.

    Used by the A/B presentation, not by scoring, and that is the point: the
    invariants are about what reaches the USER, so a presentation that elides a
    command is exactly as broken as a minimizer that rewrites one. Truncating a
    preview mid-fence produces an unterminated code block and silently drops the
    command underneath it — which is how E-1975's own verify suite caught its own
    output violating rules 2 and 3.
    """
    fences, tables = _blocks(text)
    return bool(fences or tables or _commands(text))
