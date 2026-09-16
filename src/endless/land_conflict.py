"""Evidence capture and classification for a rebase conflict during land.

`endless worktree land` rebases a task branch onto its base branch. When that
rebase conflicts, the land has a few hundred milliseconds in which the truth is
still on disk — the unmerged paths, both sides of every conflicting hunk, which
commit failed to replay — and then it runs `git rebase --abort` and all of it is
gone. Everything after that point is reconstruction, and reconstruction is where
guessing comes from.

So this module does two separable things, and keeps them separable:

**Capture** runs while the rebase is still in progress and writes down what is
true, uncapped and uninterpreted. It draws no conclusions, because a fact
recorded wrong cannot be un-wronged later.

**Classification** runs whenever someone asks — `endless worktree diagnose`, or
`land --dry-run` on a rehearsal — and reads only the capture. It answers *which
kind* of conflict this is, and it answers only when it can prove the answer.

The split matters because those two are not equally certain. Classification is
deterministic: whether the base branch already holds a commit's patch, whether
every conflicting path is an endless-managed auto-file, whether a name the
branch still uses has been deleted from the base branch — each is a question git
answers the same way every time. *Resolution* is not deterministic, and one
class (a genuine semantic overlap between two people's edits) is not
mechanically resolvable at all.

The rule that follows, and the reason this module exists:

    Prescribe only what is proven. Otherwise print the evidence and stop.

Not "print the evidence and offer the two most likely fixes." A recovery
presented as a candidate is read as an instruction — the tool printed it, so the
tool is recommending it — and two of the five classes here have recoveries that
would silently ship broken code. A branch whose conflicting hunk still calls a
function the base branch deleted will "recover" into something that raises on
first use, and the land will report success. Someone who knows the codebase
catches that. Someone who does not has no way to.

Storage lives in the worktree's sandbox, per ED-1554: a sandbox is where
per-worktree state that is not source code belongs, it lives and dies with the
worktree, and it needs no cleanup sweep of its own. `_evidence_dir` is the only
place that resolves it, so when the sandbox root moves the move is one line.
"""

from __future__ import annotations

import json
import re
import shutil
import subprocess
from dataclasses import asdict, dataclass, field
from datetime import datetime, timezone
from pathlib import Path

# ── Classes ────────────────────────────────────────────────────────────────
#
# Three are proven and carry a prescription; two are not and deliberately carry
# none. Naming them as data (rather than as branches of a rendering function)
# keeps the --json consumer and the human reader looking at the same vocabulary.

CLASS_ALREADY_LANDED = "already-landed"
CLASS_AUTO_FILE_ONLY = "auto-file-only"
CLASS_ORPHANED_LEDGER_BASE = "orphaned-ledger-base"
CLASS_SYMBOL_SUPERSESSION = "symbol-supersession"
CLASS_SEMANTIC_OVERLAP = "semantic-overlap"

CLASS_TITLES = {
    CLASS_ALREADY_LANDED: "already-landed content",
    CLASS_AUTO_FILE_ONLY: "auto-file-only conflict",
    CLASS_ORPHANED_LEDGER_BASE: "orphaned ledger base",
    CLASS_SYMBOL_SUPERSESSION: "symbol supersession",
    CLASS_SEMANTIC_OVERLAP: "semantic overlap",
}

# The on-disk capture format. Bumped when a field's MEANING changes; a reader
# that finds a version it does not know refuses rather than misreading it.
EVIDENCE_SCHEMA = 1

EVIDENCE_FILENAME = "conflict.json"

# Identifiers, for the symbol-supersession test. Deliberately the common subset
# of every C-family, Python, Go and shell identifier rather than any one
# language's grammar: the test that follows does not care what the token means,
# only whether the base branch still contains it anywhere.
_IDENT_RE = re.compile(r"[A-Za-z_][A-Za-z0-9_]*")

# Prose, stripped before identifiers are extracted. A word inside a comment or
# a string literal is text, not a reference to anything, and counting it fills a
# report with "and", "the", "cannot" — which buries the two names that mattered
# in the sixty that did not, and is how a true finding becomes an unreadable one.
#
# Applied in order: block strings first (a `#` inside a docstring is prose, not
# a comment), then single-line strings, then block and line comments. Each
# pattern REQUIRES its closing delimiter, so an unbalanced opener — which a
# conflict hunk, being a fragment, often has — matches nothing and strips
# nothing rather than swallowing the rest of the text.
#
# Deliberately lexical and language-agnostic: these delimiters cover the
# C-family, Python, Go, JS, Rust and shell. A language that marks prose some
# other way simply gets the unfiltered behaviour — noisier, never wrong in a way
# that prescribes something.
_PROSE_RES = (
    re.compile(r'""".*?"""|\'\'\'.*?\'\'\'', re.DOTALL),
    re.compile(r"'[^'\n]*'|\"[^\"\n]*\"|`[^`\n]*`"),
    re.compile(r"/\*.*?\*/", re.DOTALL),
    re.compile(r"(?:#|//)[^\n]*"),
)

# `git grep -E` alternation size. Large enough that a typical hunk is one call,
# small enough that a pathological one does not build a regex git will refuse.
_GREP_CHUNK = 150

_MARKER_OURS = "<<<<<<< "
_MARKER_ANCESTOR = "||||||| "
_MARKER_SPLIT = "======="
_MARKER_THEIRS = ">>>>>>> "


# ── Evidence ───────────────────────────────────────────────────────────────

@dataclass
class ConflictHunk:
    """One conflicting region of one file, both sides kept whole.

    `base_side` is git's "ours" and `branch_side` is git's "theirs" — which are
    the reverse of what those words suggest during a rebase. A rebase replays
    your commits ON TOP of the base branch, so at the moment of conflict HEAD is
    the base branch and the thing being applied is your commit. Every consumer
    here wants "the side the branch wrote", so the field is named for that
    rather than for git's positional convention.
    """

    path: str
    base_side: str
    branch_side: str
    # The merge-base side, present only under diff3/zdiff3 conflict style.
    ancestor_side: str = ""
    # True when the sides came from the index stages rather than from conflict
    # markers in the working tree — a binary file, or an add/add with no
    # line-level merge to mark up.
    from_stages: bool = False


@dataclass
class ConflictEvidence:
    """Everything the moment of failure knew, written down before the abort.

    Uncapped on purpose. A truncated hunk is the one that mattered often enough
    that a size limit would be a bug with a nice name, and this is written once
    per failed land, not per event.
    """

    schema: int
    captured_at: str
    task_id: str
    phase: str
    worktree_path: str
    base_branch: str
    base_tip: str
    branch: str
    branch_tip: str
    merge_base: str
    rebase_head: str
    rebase_head_subject: str
    unmerged_paths: list[str]
    hunks: list[ConflictHunk]
    # `git cherry <base> <branch>` verbatim: "+ <sha>" for a commit whose patch
    # the base branch lacks, "- <sha>" for one it already has under some other
    # SHA. Captured rather than recomputed because the base branch moves.
    cherry: list[str]
    # True when this came from `land --dry-run` rehearsing on a throwaway
    # branch, not from a land that actually failed.
    rehearsal: bool = False

    def to_dict(self) -> dict:
        return asdict(self)

    @classmethod
    def from_dict(cls, raw: dict) -> "ConflictEvidence":
        hunks = [ConflictHunk(**h) for h in raw.get("hunks", [])]
        return cls(**{**raw, "hunks": hunks})

    def cherry_mark(self, sha: str) -> str:
        """The `git cherry` mark for one commit: "+", "-", or "" if unlisted."""
        for line in self.cherry:
            parts = line.split()
            if len(parts) == 2 and parts[1] == sha:
                return parts[0]
        return ""


@dataclass
class Classification:
    """What the evidence proves, and — only then — what to do about it."""

    klass: str
    proven: bool
    summary: str
    # Facts, each already checked. Rendered as-is under "Evidence".
    evidence: list[str] = field(default_factory=list)
    # Commands to run, in order. EMPTY unless `proven` — that invariant is the
    # whole point, and `render_human` asserts it rather than trusting callers.
    prescription: list[str] = field(default_factory=list)
    # Names the branch still uses that the base branch has deleted.
    superseded_symbols: list[str] = field(default_factory=list)
    # Names appearing on both sides of a conflicting hunk.
    overlapping_symbols: list[str] = field(default_factory=list)

    def to_dict(self) -> dict:
        d = asdict(self)
        d["title"] = CLASS_TITLES.get(self.klass, self.klass)
        return d


# ── Storage ────────────────────────────────────────────────────────────────

def _evidence_dir(worktree_path: Path) -> Path:
    """Where one worktree's land-conflict capture lives.

    THE single place the destination is resolved (deliberately — see the module
    docstring). ED-1554 put per-worktree state in the worktree's sandbox and
    gave the sandbox the worktree's own lifetime, so a capture is cleaned up by
    the same reaper that removes the worktree and needs no sweep of its own.

    Composed from the sandbox root rather than written out, so it follows the
    root wherever it goes.
    """
    from endless import config

    return config.sandbox_root(worktree_path.name) / "land-conflict"


def evidence_path(worktree_path: Path) -> Path:
    """The capture file for one worktree. One file: the latest failure is the
    one anyone is diagnosing, and a history nothing reads is a retention
    problem invented for free."""
    return _evidence_dir(worktree_path) / EVIDENCE_FILENAME


def store_evidence(worktree_path: Path, ev: ConflictEvidence) -> Path | None:
    """Persist the capture. Returns where it went, or None if it could not.

    Never raises: the caller is mid-failure and about to print a message the
    user needs, and an OSError about a directory would replace it with a stack
    trace. But it does not pretend either — a caller that is about to tell
    someone to run `endless worktree diagnose` has to know whether there will
    be anything there to diagnose.
    """
    path = evidence_path(worktree_path)
    try:
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(json.dumps(ev.to_dict(), indent=2) + "\n")
    except OSError:
        return None
    return path


def load_evidence(worktree_path: Path) -> ConflictEvidence | None:
    """Read the capture back, or None when there is none to read.

    A capture written by a newer schema reads as absent rather than as
    something to reinterpret — `diagnose` says it has nothing and exits
    non-zero, which is true and safe, where a partial read would be neither.
    """
    path = evidence_path(worktree_path)
    try:
        raw = json.loads(path.read_text())
    except (OSError, ValueError):
        return None
    if not isinstance(raw, dict) or raw.get("schema") != EVIDENCE_SCHEMA:
        return None
    try:
        return ConflictEvidence.from_dict(raw)
    except TypeError:
        return None


# ── Capture ────────────────────────────────────────────────────────────────

def _git(args: list[str], cwd: Path) -> subprocess.CompletedProcess:
    """Run git, never raising. Every call site here is on a failure path where
    an unavailable answer is a blank field, not a second failure.

    `errors="replace"` for the same reason: a conflict can involve a file that
    is not valid text — a binary asset, a file in another encoding — and a
    capture that died decoding one would take the whole conflict report with it.
    An unreadable byte becomes U+FFFD and the rest of the evidence survives.
    """
    return subprocess.run(
        ["git", *args], cwd=str(cwd),
        capture_output=True, text=True, errors="replace", check=False,
    )


def _out(args: list[str], cwd: Path) -> str:
    r = _git(args, cwd)
    return r.stdout.strip() if r.returncode == 0 else ""


def parse_conflict_markers(text: str, path: str) -> list[ConflictHunk]:
    """Split a conflicted file into hunks, keeping both sides whole.

    Handles `merge`, `diff3` and `zdiff3` conflict styles — the ancestor block
    of the latter two is captured rather than skipped, since it is exactly the
    "what did this look like before either side touched it" a reader wants.

    A file with no markers yields no hunks; the caller falls back to the index
    stages, which is the case for a binary conflict or an add/add.
    """
    hunks: list[ConflictHunk] = []
    ours: list[str] = []
    ancestor: list[str] = []
    theirs: list[str] = []
    # None = outside a conflict; otherwise which block we are accumulating.
    side: str | None = None

    for line in text.splitlines():
        if line.startswith(_MARKER_OURS):
            side, ours, ancestor, theirs = "ours", [], [], []
            continue
        if side is None:
            continue
        if line.startswith(_MARKER_ANCESTOR):
            side = "ancestor"
            continue
        if line == _MARKER_SPLIT or line.startswith(_MARKER_SPLIT + " "):
            side = "theirs"
            continue
        if line.startswith(_MARKER_THEIRS):
            hunks.append(ConflictHunk(
                path=path,
                base_side="\n".join(ours),
                branch_side="\n".join(theirs),
                ancestor_side="\n".join(ancestor),
            ))
            side = None
            continue
        {"ours": ours, "ancestor": ancestor, "theirs": theirs}[side].append(line)

    return hunks


def _hunks_for_path(worktree_path: Path, rel_path: str) -> list[ConflictHunk]:
    """Both sides of every conflicting region of one unmerged path."""
    try:
        text = (worktree_path / rel_path).read_text()
    except (OSError, UnicodeDecodeError):
        text = ""

    if hunks := parse_conflict_markers(text, rel_path):
        return hunks

    # No markers: the working-tree file is binary, or git left the path
    # unmerged without a line-level merge to mark up. The index still holds
    # both sides whole — stage 2 is the base branch, stage 3 is the branch.
    base_side = _git(["show", f":2:{rel_path}"], worktree_path)
    branch_side = _git(["show", f":3:{rel_path}"], worktree_path)
    if base_side.returncode != 0 and branch_side.returncode != 0:
        return []
    return [ConflictHunk(
        path=rel_path,
        base_side=base_side.stdout if base_side.returncode == 0 else "",
        branch_side=branch_side.stdout if branch_side.returncode == 0 else "",
        from_stages=True,
    )]


def capture_evidence(
    worktree_path: Path,
    base_branch: str,
    branch: str,
    *,
    task_id: str,
    phase: str,
    rebase_in_progress: bool,
    rehearsal: bool = False,
) -> ConflictEvidence:
    """Record the live conflict state. MUST be called before `rebase --abort`.

    `branch` is the branch ref, not HEAD: a rebase in progress leaves HEAD
    detached partway through the replay, while the branch ref still points at
    the tip the user committed. Asking HEAD would record where the rebase got
    to, which is machinery, in place of what the user has, which is the subject.

    `rebase_in_progress` gates the REBASE_HEAD read, and has no default so that
    every caller has to answer it (E-2122). REBASE_HEAD is a plain ref: with no
    rebase running it either does not resolve or still holds a value left by a
    DIFFERENT operation, and recording that as "the commit that failed to
    replay" would put a fiction in the permanent record — the one place a wrong
    fact cannot be corrected later, because by then the truth is gone.
    """
    unmerged = [
        ln for ln in
        _out(["diff", "--name-only", "--diff-filter=U"], worktree_path).splitlines()
        if ln.strip()
    ]

    rebase_head = (
        _out(["rev-parse", "REBASE_HEAD"], worktree_path)
        if rebase_in_progress else ""
    )
    rebase_head_subject = (
        _out(["log", "-1", "--format=%s", "REBASE_HEAD"], worktree_path)
        if rebase_head else ""
    )

    base_tip = _out(["rev-parse", base_branch], worktree_path)
    branch_tip = _out(["rev-parse", branch], worktree_path)
    merge_base = (
        _out(["merge-base", base_tip, branch_tip], worktree_path)
        if base_tip and branch_tip else ""
    )
    cherry = [
        ln for ln in
        _out(["cherry", base_branch, branch], worktree_path).splitlines()
        if ln.strip()
    ]

    hunks: list[ConflictHunk] = []
    for rel in unmerged:
        hunks.extend(_hunks_for_path(worktree_path, rel))

    return ConflictEvidence(
        schema=EVIDENCE_SCHEMA,
        captured_at=datetime.now(timezone.utc).isoformat(timespec="seconds"),
        task_id=task_id,
        phase=phase,
        worktree_path=str(worktree_path),
        base_branch=base_branch,
        base_tip=base_tip,
        branch=branch,
        branch_tip=branch_tip,
        merge_base=merge_base,
        rebase_head=rebase_head,
        rebase_head_subject=rebase_head_subject,
        unmerged_paths=unmerged,
        hunks=hunks,
        cherry=cherry,
        rehearsal=rehearsal,
    )


# ── Detectors ──────────────────────────────────────────────────────────────

def _identifiers(text: str, *, code_only: bool = False) -> set[str]:
    """Every identifier-shaped token in `text`.

    `code_only` drops comments and string literals first — see _PROSE_RES. Both
    reported symbol lists ask for it, for the same reason: a name is only worth
    printing if it refers to something.
    """
    if code_only:
        for prose in _PROSE_RES:
            text = prose.sub(" ", text)
    return set(_IDENT_RE.findall(text))


def _present_at_rev(worktree_path: Path, rev: str, names: set[str]) -> set[str]:
    """Which of `names` appear anywhere in the tree at `rev`.

    One `git grep` per chunk of names rather than one per name: the question is
    asked of every identifier in every conflicting hunk, and a subprocess each
    turns a diagnostic into a wait.

    Failure reads as "present" for every name in the failed chunk, which is the
    conservative direction: the supersession test only fires on names it can
    show are ABSENT, so an unanswered grep suppresses a claim rather than
    inventing one.
    """
    if not rev or not names:
        return set()

    found: set[str] = set()
    ordered = sorted(names)
    for i in range(0, len(ordered), _GREP_CHUNK):
        chunk = ordered[i:i + _GREP_CHUNK]
        r = _git(
            ["grep", "-I", "-h", "-o", "-w", "-E", "|".join(chunk), rev, "--", "."],
            worktree_path,
        )
        if r.returncode == 0:
            found.update(ln.strip() for ln in r.stdout.splitlines() if ln.strip())
        elif r.returncode != 1:
            # Not "no matches" (1) — git could not answer. Suppress, do not guess.
            found.update(chunk)
    return found


def _superseded_symbols(ev: ConflictEvidence, worktree_path: Path) -> list[str]:
    """Names the branch side still uses that the base branch has deleted.

    Three conditions, all required, and the third is what makes the finding a
    proof rather than a hunch:

      1. the name appears in the branch side of a conflicting hunk;
      2. it appears NOWHERE in the base branch's tree — so nothing on base
         defines it, and code using it after a merge cannot resolve it;
      3. it DID appear at the merge base — so it is not simply a name this
         branch introduced (those are absent from base for the innocent reason
         that they are new), it is one that existed and that the base branch
         removed.

    Together those say: the base branch deleted this, and the branch's side of
    the conflict has not caught up. Taking the branch's side — which is what
    both of the recoveries this command replaced would do — reintroduces a
    reference with nothing behind it.

    Condition 2 is deliberately "appears nowhere", not "has no definition".
    Proving a *definition* needs a parser per language; proving ABSENCE needs
    only `git grep`, and absence is strictly stronger. The cost is silence on a
    name that survives on base in some unrelated place, which is the right way
    to be wrong here.
    """
    candidates: set[str] = set()
    for h in ev.hunks:
        candidates |= _identifiers(h.branch_side, code_only=True)
    if not candidates:
        return []

    on_base = _present_at_rev(worktree_path, ev.base_tip, candidates)
    missing = candidates - on_base
    if not missing:
        return []
    existed_before = _present_at_rev(worktree_path, ev.merge_base, missing)
    return sorted(missing & existed_before)


def _overlapping_symbols(ev: ConflictEvidence) -> list[str]:
    """Names both sides of a conflicting hunk mention — the surface the two
    edits share, and the shortest description of what a human has to reconcile.

    Code only. Two sides of a conflict in a well-commented file share most of
    the English language, and a list that opens with "a, actually, and, cannot"
    is not a description of anything.
    """
    names: set[str] = set()
    for h in ev.hunks:
        names |= (_identifiers(h.base_side, code_only=True)
                  & _identifiers(h.branch_side, code_only=True))
    return sorted(names)


def ledger_orphans(worktree_path: Path, ev: ConflictEvidence) -> dict | None:
    """Ask `endless-go worktree ledger-orphans` which of the branch's ledger
    commits the base branch provably already holds. None when it cannot answer.

    Shelled out rather than reimplemented. The rule is ED-1553's — a worktree's
    fork point is identified by ledger CONTENT, never by the SHA that held it —
    and it already has an implementation, in Go, on the auto-commit path. Two
    copies of one git predicate in two languages is how the behind-base bug came
    to need fixing twice; the language boundary is cheaper than the divergence.
    """
    binary = shutil.which("endless-go")
    if not binary or not ev.base_tip or not ev.branch_tip:
        return None
    r = subprocess.run(
        [binary, "worktree", "ledger-orphans",
         "--repo", str(worktree_path),
         "--base", ev.base_tip, "--branch", ev.branch_tip],
        capture_output=True, text=True, check=False,
    )
    if r.returncode != 0:
        return None
    try:
        return json.loads(r.stdout)
    except ValueError:
        return None


def _is_auto_file(rel_path: str) -> bool:
    """True for an endless-managed file — one endless writes and commits itself.

    worktree_cmd's own predicate, borrowed rather than restated: land's message
    and this classifier must agree about what an auto-file is, and two copies of
    a glob list is how they would stop agreeing. Imported inside the function
    because at module scope it would be a cycle — worktree_cmd is what calls
    into here.
    """
    from endless.worktree_cmd import _is_auto_file as owner

    return owner(rel_path)


# ── Classification ─────────────────────────────────────────────────────────

def classify(ev: ConflictEvidence, worktree_path: Path) -> Classification:
    """Decide which kind of conflict this is, from the capture alone.

    Ordered MOST SPECIFIC first, and the order is the contract: an earlier class
    is a stronger statement about the same evidence, so the first one that fires
    wins. The last is not a finding at all — it is the honest report that
    nothing here is mechanically decidable.

    The orphaned-ledger-base test runs BEFORE the auto-file-only test, and that
    is deliberate rather than incidental: an orphaned ledger commit conflicts on
    a ledger file, which is an endless-managed auto-file, so the coarser class
    would swallow every instance of the finer one and the mid-branch case — the
    one land will NOT fix for you — would never be reported at all.
    """
    land = f"endless worktree land {ev.task_id}" if ev.task_id else \
        "endless worktree land <id>"
    wt = str(worktree_path)

    # 1. Already-landed content. `git cherry` compares PATCHES, not SHAs, so a
    #    "-" survives the amend or rebase that changed the commit's identity.
    if ev.rebase_head and ev.cherry_mark(ev.rebase_head) == "-":
        return Classification(
            klass=CLASS_ALREADY_LANDED,
            proven=True,
            summary=(
                f"{ev.base_branch} already contains an equivalent of the commit "
                f"that failed to replay."
            ),
            evidence=[
                f"git cherry {ev.base_branch} {ev.branch} marks "
                f"{ev.rebase_head[:12]} with '-', which means {ev.base_branch} "
                f"already holds an equivalent patch under a different SHA.",
                "Resolving the conflict in place and running "
                "`git rebase --continue` would therefore apply that content a "
                "second time.",
            ],
            prescription=[
                f"git -C {wt} diff {ev.base_branch}...{ev.branch} > /tmp/land-delta.patch",
                f"git -C {wt} reset --hard {ev.base_branch}",
                f"git -C {wt} apply --3way /tmp/land-delta.patch",
                f"git -C {wt} commit -am '<describe your change>'",
                land,
            ],
        )

    # 2. Orphaned ledger base. The branch forked off a ledger commit that main
    #    has since amended, so the branch carries a stale prefix of a segment
    #    main has appended to.
    from endless.worktree_cmd import DB_LEDGER_DIR

    orphans = ledger_orphans(worktree_path, ev)
    # Firing on "the branch has orphans somewhere" would misattribute an
    # ordinary source conflict that merely happens to sit on a stale ledger
    # base. The conflict has to BE the orphan: either the commit that failed to
    # replay is one, or the whole conflict is confined to the ledger.
    ledger_only = bool(ev.unmerged_paths) and all(
        p.startswith(DB_LEDGER_DIR + "/") for p in ev.unmerged_paths
    )
    if (orphans and orphans.get("orphans")
            and (ev.rebase_head in orphans["orphans"] or ledger_only)):
        by_sha = {c["sha"]: c for c in orphans.get("commits", [])}
        listing = [
            f"{sha[:12]}  {by_sha.get(sha, {}).get('subject', '')}"
            for sha in orphans["orphans"]
        ]
        evidence = [
            "These commits on the branch hold ledger content "
            f"{ev.base_branch} provably already has — every ledger file they "
            f"touch is a byte-prefix of the same file on {ev.base_branch}, and "
            "ledger segments are append-only:",
            *[f"  {ln}" for ln in listing],
        ]
        if orphans.get("mid_branch"):
            return Classification(
                klass=CLASS_ORPHANED_LEDGER_BASE,
                proven=True,
                summary=(
                    "The branch carries orphaned ledger commits, and at least "
                    "one sits mid-branch."
                ),
                evidence=evidence + [
                    "Land drops orphaned ledger commits automatically only when "
                    "they form an unbroken run at the base of the branch. At "
                    "least one of these sits behind a commit of yours, and "
                    "dropping a mid-branch commit can take real work with it — "
                    "so nothing is prescribed. Inspect each with "
                    f"`git -C {wt} show <sha>`.",
                ],
            )
        last = orphans.get("last_contiguous_orphan", "")
        return Classification(
            klass=CLASS_ORPHANED_LEDGER_BASE,
            proven=True,
            summary=(
                "The branch is based on ledger commits "
                f"{ev.base_branch} has since amended."
            ),
            evidence=evidence + [
                "They form an unbroken run at the base of the branch, so "
                "dropping them removes no authored work.",
            ],
            prescription=[
                f"git -C {wt} rebase --onto {ev.base_branch} {last}",
                land,
            ],
        )

    # 3. Auto-file-only. Endless owns these files and commits them itself, so
    #    the branch's copy is never work anyone authored — taking the base
    #    branch's is lossless by construction.
    if ev.unmerged_paths and all(_is_auto_file(p) for p in ev.unmerged_paths):
        from endless.worktree_cmd import AUTO_COMMIT_GLOBS

        globs = " ".join(AUTO_COMMIT_GLOBS)
        return Classification(
            klass=CLASS_AUTO_FILE_ONLY,
            proven=True,
            summary="Every conflicting file is an endless-managed auto-file.",
            evidence=[
                "These files are written and committed by endless itself, never "
                "by hand, so the branch's copy holds no authored work to lose.",
            ],
            prescription=[
                f"git -C {wt} checkout {ev.base_branch} -- {globs}",
                land,
            ],
        )

    # 4. Symbol supersession. The one class where a recovery is actively
    #    dangerous, so it is the one where naming what is missing IS the output.
    superseded = _superseded_symbols(ev, worktree_path)
    if superseded:
        return Classification(
            klass=CLASS_SYMBOL_SUPERSESSION,
            proven=True,
            summary=(
                f"Your branch still uses {len(superseded)} name(s) that "
                f"{ev.base_branch} has deleted."
            ),
            evidence=[
                f"These names appear in your side of the conflict, existed at "
                f"the fork point ({ev.merge_base[:12]}), and appear nowhere in "
                f"{ev.base_branch} today:",
                *[f"  {name}" for name in superseded],
                f"{ev.base_branch} removed them. Both of the recoveries a "
                f"conflict message would normally offer — resolving in place, "
                f"or resetting and re-applying your delta — put your side back, "
                f"which reintroduces these references with nothing behind them. "
                f"The land would then succeed and the code would fail on first "
                f"use.",
                "Your branch's work here is superseded. What it should become "
                "is a judgement about intent, which this command does not have, "
                "so it prescribes nothing.",
            ],
            superseded_symbols=superseded,
        )

    # 5. Semantic overlap. Nothing above fired, which means two people edited
    #    the same thing and only a human knows which edit was meant to win.
    return Classification(
        klass=CLASS_SEMANTIC_OVERLAP,
        proven=False,
        summary=(
            f"Your branch and {ev.base_branch} both changed the same code."
        ),
        evidence=[
            "None of the mechanical classes apply: the commit is not already "
            f"on {ev.base_branch}, the conflict is not confined to "
            "endless-managed files, no orphaned ledger commits are involved, "
            f"and every name your side uses still exists on {ev.base_branch}.",
            "That leaves a genuine overlap between two intentional edits. "
            "Which one is right is not derivable from the repository — a human "
            "or an agent with the context has to choose. Both sides are printed "
            "in full below so that choice can be made from the evidence.",
        ],
        overlapping_symbols=_overlapping_symbols(ev),
    )


# ── Rendering ──────────────────────────────────────────────────────────────

def _display_path(p: str) -> str:
    home = str(Path.home())
    return p.replace(home, "~", 1) if p.startswith(home) else p


def render_human(ev: ConflictEvidence, cl: Classification) -> str:
    """The default `diagnose` output: facts, then class, then — only if proven —
    what to run."""
    # The invariant this whole command exists to hold, checked rather than
    # trusted: an unproven class never reaches a reader with a command in hand.
    assert cl.proven or not cl.prescription, \
        f"unproven class {cl.klass} carries a prescription"

    out: list[str] = []
    title = f"Land conflict — {ev.task_id}" if ev.task_id else "Land conflict"
    if ev.rehearsal:
        title += " (rehearsal)"
    out.append(title)
    out.append("─" * len(title))
    out.append(f"Captured:  {ev.captured_at}")
    out.append(f"Phase:     {ev.phase}")
    out.append(f"Worktree:  {_display_path(ev.worktree_path)}")
    out.append(f"Branch:    {ev.branch} @ {ev.branch_tip[:12]}")
    out.append(f"Base:      {ev.base_branch} @ {ev.base_tip[:12]}")
    if ev.merge_base:
        out.append(f"Forked at: {ev.merge_base[:12]}")
    if ev.rebase_head:
        subj = f" {ev.rebase_head_subject}" if ev.rebase_head_subject else ""
        out.append(f"Failed to replay: {ev.rebase_head[:12]}{subj}")
    out.append("")

    out.append("Conflicting files:")
    for p in ev.unmerged_paths or ["(none recorded)"]:
        out.append(f"  {p}")
    out.append("")

    verdict = "proven" if cl.proven else "NOT proven"
    out.append(f"Classification: {CLASS_TITLES.get(cl.klass, cl.klass)} ({verdict})")
    out.append(f"  {cl.summary}")
    out.append("")

    if cl.evidence:
        out.append("Evidence:")
        for line in cl.evidence:
            out.append(f"  {line}" if not line.startswith("  ") else line)
        out.append("")

    if cl.prescription:
        out.append("Recovery (this class is proven; these steps are safe):")
        for step in cl.prescription:
            out.append(f"  {step}")
        out.append("")
    else:
        out.append("No recovery is prescribed. This command prints only what it")
        out.append("can prove, and a recovery it cannot prove is a guess a reader")
        out.append("would follow as an instruction.")
        out.append("")

    if cl.overlapping_symbols:
        out.append("Names appearing on both sides of a conflicting hunk:")
        out.append("  " + ", ".join(cl.overlapping_symbols))
        out.append("")

    if not cl.proven and ev.hunks:
        out.append("Both sides, in full:")
        for h in ev.hunks:
            out.append("")
            out.append(f"  ── {h.path} ──")
            out.append(f"  {ev.base_branch} side:")
            out.extend(f"    {ln}" for ln in h.base_side.splitlines() or ["(empty)"])
            out.append("  your branch's side:")
            out.extend(f"    {ln}" for ln in h.branch_side.splitlines() or ["(empty)"])
        out.append("")

    return "\n".join(out).rstrip() + "\n"


def render_json(ev: ConflictEvidence, cl: Classification) -> str:
    from endless import provenance

    return json.dumps(
        provenance.attach(
            {"evidence": ev.to_dict(), "classification": cl.to_dict()}),
        indent=2,
    ) + "\n"
