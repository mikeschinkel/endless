"""Is this record still authoritative? (E-2095)

A status line is read as PROVENANCE — where this row came from — and not as a
caveat on the content beneath it. So an agent quotes an obsolete task, a
replaced one, or a decision nobody ever accepted, as though it governed. That
has happened repeatedly, and the status was on screen every time.

This module answers one question for a task or a decision — is the record
straightforwardly authoritative? — and renders the answer as a banner that
states the caveat BEFORE the content it qualifies.

Two kinds, named as such. "Not ever" is a record that was retired, declined,
rejected, replaced or superseded; "not yet" is a decision still proposed. A
proposed decision is not a retired one and the wording must not let them blur.

Shape follows E-2097, which shipped it: ONE dense line, emitted as both the
FIRST and the LAST line of the output, identical rather than split. Split them —
verdict first, remedy last — and `head -N` yields the verdict without the remedy
while `tail -N` yields the remedy without the verdict. Identical means whichever
end a truncating pipe leaves is sufficient alone.

AUDIENCE: agents only, and this is stricter than E-2097's split, deliberately.
There the underlying message is a REFUSAL a human must see or the command fails
silently, so only the bracketing is gated. Here there is no message a human is
owed — the status line and the `Replaced by:` link are already on screen, and a
person reads them as the qualifiers they are. The banner exists because an agent
does not. So the whole thing is gated, and a human reaches it only by asking,
with `--agent-view`, to see what an agent sees.

Machine formats take the fact as a KEY instead (see `as_json`): nothing
truncates JSON by lines, so the repetition would buy nothing there.
"""

from __future__ import annotations

from dataclasses import dataclass, field

from endless import agent_help


@dataclass(frozen=True)
class Caveat:
    """Why a record is not straightforwardly authoritative.

    `kind` is "not-ever" or "not-yet" — the distinction the wording must carry.
    `see` names the records that took over, when there are any; it is what turns
    the banner from a warning into a redirection.
    """

    kind: str
    reason: str
    see: list[str] = field(default_factory=list)

    def summary(self, subject: str) -> str:
        """The one dense line, minus the sentinel and command."""
        verdict = "is NOT authoritative" if self.kind == "not-ever" \
            else "is NOT YET authoritative"
        if self.see:
            instruction = "Read " + ", ".join(self.see) + " instead."
        elif self.kind == "not-yet":
            instruction = "Do not quote it as settled."
        else:
            instruction = "Do not quote it as current."
        return f"{subject} {verdict} — {self.reason}. {instruction}"

    def as_json(self, subject: str) -> dict:
        """The machine shape: the same facts, unrepeated."""
        return {
            "authoritative": False,
            "kind": self.kind,
            "reason": self.reason,
            "see": list(self.see),
            "summary": self.summary(subject),
        }


def for_task(status: str | None, replaced_by: list[str] | None,
             duplicates: list[str] | None) -> Caveat | None:
    """The caveat on a task row, or None when it is straightforwardly current.

    The two relations trigger regardless of status, because that is the point of
    them: `task replace` deliberately holds shipped work at the status it earned,
    so a REPLACED task can read `confirmed` and still not be the record to quote.
    """
    if replaced_by:
        return Caveat("not-ever", "it has been replaced by "
                      + ", ".join(replaced_by), list(replaced_by))
    if duplicates:
        return Caveat("not-ever", "it duplicates "
                      + ", ".join(duplicates) + ", which is the record kept",
                      list(duplicates))
    if status == "obsolete":
        # NOT "retired before the work ever shipped" (E-2144). That asserted a
        # fact this function cannot see and is sometimes flatly wrong: E-1421 is
        # an epic that landed twice and is obsolete, so the banner called its two
        # landings imaginary. What `obsolete` licenses saying is what the word
        # means — this is not the thing to do any more — and that holds whether
        # or not any of it shipped.
        return Caveat("not-ever",
                      "it is obsolete: retired as no longer needed")
    if status == "superseded":
        # Reached only when the relation is missing: `replaced_by` is checked
        # first above and names the successor, which is the useful answer. The
        # status alone can only say THAT something took over, so it says that
        # and no more rather than inventing a successor it cannot see.
        return Caveat("not-ever",
                      "it is superseded: another task took the work over")
    if status == "declined":
        return Caveat("not-ever",
                      "it is declined: an active decision not to do the work")
    return None


def for_decision(status: str | None,
                 superseded_by: list[str] | None) -> Caveat | None:
    """The caveat on a decision row, or None when it governs.

    `proposed` is the "not yet" case and the reason this module has two kinds at
    all: a proposed decision has not been rejected, it has not been decided, and
    quoting it as settled asserts an agreement nobody made.
    """
    if status == "superseded":
        if superseded_by:
            return Caveat("not-ever", "it has been superseded by "
                          + ", ".join(superseded_by), list(superseded_by))
        return Caveat("not-ever", "it has been superseded")
    if status == "rejected":
        return Caveat("not-ever",
                      "it was rejected: considered and turned down")
    if status == "proposed":
        return Caveat("not-yet",
                      "it is still proposed and nobody has accepted it")
    return None


def banner(caveat: Caveat | None, subject: str) -> str | None:
    """The line to emit before AND after the record, or None to emit nothing.

    Returns None for a human, which is the whole gate: `agent_facing()` is
    `agent_env.present() or --agent-view`, and it is asked HERE rather than
    re-spelled at each call site. E-1966, E-2006 and E-2097 each folded a
    competing spelling of that question back into one place; a fourth would
    undo that.

    It carries agent_help's `[Endless]` sentinel for the reason that marker
    exists: one line of an agent's scrollback has to be identifiable as Endless
    speaking without the surrounding output, and `task show` alone does not
    identify us. It is NOT a refusal and carries no `Error:` prefix — Click adds
    that to an exception, and this is ordinary output with a caveat on it.
    """
    if caveat is None or not agent_help.agent_facing():
        return None
    command = agent_help.current_command_path()
    head = f"{agent_help.ERROR_SENTINEL} {command}: " if command \
        else f"{agent_help.ERROR_SENTINEL} "
    return head + caveat.summary(subject)
