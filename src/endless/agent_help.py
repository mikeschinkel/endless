"""Agent-facing CLI output: `--help` augmentation, and refusals.

When a Claude Code agent (or a human passing `--agent-view`) runs `<cmd> --help`,
prepend a directive pointing at the guide section that explains the command,
above Click's normal help. The agent reached for `--help`, so the signpost to
the guide lives there. The map file *points*; the guide section *explains* —
no duplication, so nothing drifts.

`agent_error` (E-2097) is the same audience seen from the other side: an agent
that pipes a refusal through `tail -3` keeps the end of the guidance and never
sees the verdict, so a refusal rendered for an agent repeats its one-line
verdict at BOTH ends. Both surfaces ask the same question — is an agent reading
this? — and `agent_facing` is the one place that answers it.

This module has no dependency on `cli`; the concrete `AgentAware*` Click classes
are defined in `cli.py` (where `DBAwareGroup` lives) by mixing `AgentHelpMixin`
in. `_AGENT_VIEW` is set by the root group's argv pre-scan when `--agent-view`
appears in any position (mirroring how `--db` is consumed).
"""

from __future__ import annotations

import os
import sys

import click

from endless import agent_env
from endless.guide_map import load_map

_AGENT_VIEW = False
_AGENT_FORMAT = False

#: Was ENDLESS_AUDIENCE=agent already set when this process started?
#:
#: Read ONCE, at import, and deliberately not re-read. `_export_audience` writes
#: the same variable for endless-go subprocesses, and a live read would feed
#: this process's own decision back in as if somebody outside had made it —
#: harmless in a real run, which handles one invocation, and a latch in the test
#: suite, which runs thousands through one interpreter: the first agent-facing
#: invocation would make every later one agent-facing too.
#:
#: Go asks the same question (internal/refusal.Agent), so an operator who
#: exports it gets both halves of Endless rendering for the same reader instead
#: of one each way.
_AMBIENT_AUDIENCE = os.environ.get("ENDLESS_AUDIENCE") == "agent"


def set_agent_view(value: bool) -> None:
    global _AGENT_VIEW
    _AGENT_VIEW = value


def agent_view_requested() -> bool:
    return _AGENT_VIEW


def set_agent_format(value: bool) -> None:
    """Record that this invocation asked for the agent RENDERING (E-2159).

    `--agent`, its retired alias `--llm`, and `--format agent` are three
    spellings of one thing (E-1504), and a caller that asks for the agent
    rendering of a command's OUTPUT is an agent for the purposes of its
    refusals too. Before this, `endless task show --agent` produced a machine
    rendering on stdout and a human refusal on stderr, from the same
    invocation.

    Set once, by the root group's argv pre-scan — the same place `--agent-view`
    is consumed — rather than per call site: the flag can appear in any
    position, and thirty commands carry the surface.
    """
    global _AGENT_FORMAT
    _AGENT_FORMAT = value


def agent_facing() -> bool:
    """An agent is reading this output, or a human asked to see what one sees.

    Public since E-2097, which needed the same question answered for refusals
    (`agent_error`) as for `--help`. It stayed private while `--help` was the
    only caller; a second caller made "reuse this" the whole point, since the
    alternative is a fresh spelling of a question this module has already
    consolidated twice.

    Harness detection is `agent_env`'s job (E-1962). This module used to carry
    its own `is_claude_code_agent()` keyed on CLAUDECODE=1 — a third spelling
    of the same question, alongside `task_cmd._running_under_agent()` and the
    detector itself. Folded in E-1966: one detector, no copies that can drift
    apart. E-2006 folded the last of it — the `!= UNKNOWN` comparison itself,
    which was still written out here and in `_running_under_agent` — into
    `agent_env.present`. E-2097 retired `_running_under_agent` too: both its
    callers wanted this question, and one of them was already spelling it out
    as `_running_under_agent() or agent_view_requested()`.

    E-2159 added two more sources: `--agent` / `--format agent`, recorded by
    set_agent_format, and an ambient ENDLESS_AUDIENCE=agent, which is the same
    question Go's internal/refusal.Agent asks. Harness detection itself is NOT
    widened — an agent under a harness Endless does not recognise still reads as
    a human, exactly as it does everywhere else. The fix for that is a detector
    entry backed by a real environment dump, not a looser test in one consumer.
    """
    return (agent_env.present() or _AGENT_VIEW or _AGENT_FORMAT
            or _AMBIENT_AUDIENCE)


def _command_path(ctx: click.Context) -> str:
    """ctx.command_path is 'endless task spawn'; drop the leading prog name."""
    parts = ctx.command_path.split(" ", 1)
    return parts[1] if len(parts) > 1 else ""


def current_command_path() -> str | None:
    """'task add' for `endless task add`, or None outside a running command.

    Lets a refusal name the verb that produced it without every validator
    threading its own command string down from the CLI layer — the validators
    are shared by several verbs, so the string they would thread is the one
    thing they cannot know.
    """
    ctx = click.get_current_context(silent=True)
    if ctx is None:
        return None
    return _command_path(ctx) or None


# The stable marker that makes one line of an agent's scrollback identifiable
# as an Endless refusal without the surrounding message. It has to carry the
# project name: the command alone does not identify us — `task add` is also
# Taskwarrior's verb — and a bare "ERROR:" is in every tool's output, so it
# greps to noise.
#
# It does NOT repeat the word "error". Click already supplies that (see
# _CLICK_ERROR_PREFIX), and the line reads as one sentence rather than two:
#
#     Error: [Endless] task add: title 107>100 chars. …
ERROR_SENTINEL = "[Endless]"

# Click prints a ClickException as `Error: <message>`, so the message's first
# line arrives with that prefix and the bracket's two ends would not match byte
# for byte unless the closing copy carries it too. The coupling is deliberate
# and pinned by tests/test_agent_error_bracket.py, which compares the RENDERED
# first and last lines rather than the message string.
_CLICK_ERROR_PREFIX = "Error: "


def agent_error(summary: str, guidance: str, command: str | None = None) -> str:
    """Bracket a refusal with a repeated one-line verdict, for agents (E-2097).

    Returns the message to hand to `click.ClickException`.

    For a human, that is `guidance` unchanged — byte for byte today's message.
    A repeated long line is noise to a reader who was never going to truncate
    it, so the duplication is agent-only by design.

    For an agent, the same guidance arrives bracketed:

        [Endless] task add: <summary>

        … guidance, unchanged …

        Error: [Endless] task add: <summary>

    The two verdict lines are IDENTICAL on purpose. Split them — problem first,
    remedy last — and `head -N` yields the problem without the fix while
    `tail -N` yields the fix without the problem. Identical means whichever end
    a truncating pipe leaves is sufficient alone, and a reader who sees both
    reads one repeat rather than two findings.

    `summary` must be ONE line and carry the whole verdict: the measured
    numbers, where the content belongs instead, and whether anything changed.
    Length is not a constraint — `head`/`tail` are line-based, so a
    200-character line survives whole — but a paragraph is, because a paragraph
    invites exactly the skimming this exists to defeat.
    """
    if not agent_facing():
        return guidance
    if command is None:
        command = current_command_path()
    verdict = (f"{ERROR_SENTINEL} {command}: {summary}" if command
               else f"{ERROR_SENTINEL}: {summary}")
    return f"{verdict}\n\n{guidance.rstrip()}\n\n{_CLICK_ERROR_PREFIX}{verdict}"


# ─── refusals that say whether the agent must report them (E-2159) ───────────
#
# Every refusal, warning and notice Endless raises must answer one question for
# the agent reading it: can you continue without asking the user?
#
# "You must do X instead of Y" is a NO. The agent calls the command again doing
# X, and the user never hears about it. The failure that opened this line of
# work (E-2155) was a refusal the agent handled correctly and then narrated in
# its handoff anyway, because nothing in the message said not to.
#
# So a class is not optional. `Refusal` has no public constructor that takes a
# message alone: every entry point below names a class, which is what makes
# "did anyone decide?" a question the AST check in tests/test_refusal_sites.py
# can answer instead of a question a reviewer has to.
#
#     no_report   the command can be retried differently; say nothing
#     report      only the user can decide; stop and ask
#     report_if   turns on something only the agent knows; name both branches
#     fault       Endless itself broke; tell the user, do not retry
#     relay       endless-go's own refusal, already classified by Go
#     warn / info nothing is blocked
#
# Go's internal/refusal is the same thing on the other side of the boundary,
# and the two renderings are held byte-identical by one shared golden file:
# tests/fixtures/refusal-verdicts.json, asserted here by
# tests/test_refusal_verdicts.py and in Go by TestGoldenVerdicts.

NO_REPORT = "no-report"
REPORT = "report"
REPORT_IF = "report-if"
FAULT = "fault"
WARN = "warn"
INFO = "info"

# The directives, in one place because they are a contract with the agent
# reading them and must not drift — between call sites, or between languages.
#
# _NO_REPORT_DIRECTIVE says "now or in your summary" deliberately. The handoff
# is where the leak happened: the agent obeyed the refusal in the moment and
# then listed it as something the user should know about at the end of the turn.
_NO_REPORT_DIRECTIVE = ("Handle this yourself: do not mention this refusal to "
                        "the user, now or in your summary.")
_REPORT_DIRECTIVE = "Stop and ask; do not work around it."
_REPORT_IF_DIRECTIVE = "do not mention this refusal."
_FAULT_DIRECTIVE = ("Endless itself failed: tell the user, and do not retry "
                    "more than once.")
_WARN_DIRECTIVE = ("Nothing is blocked. Handle this yourself; do not mention "
                   "it unless the user asks.")

#: The environment contract with endless-go. The audience is decided once, here,
#: and exported so a relayed Go refusal renders for the same reader who asked
#: the question. Without it `endless worktree drop --agent` run by a human would
#: relay a human-rendered probe refusal, and `--agent-view` would stop showing a
#: human what an agent actually sees — the only thing that flag is for.
AUDIENCE_VAR = "ENDLESS_AUDIENCE"
AUDIENCE_AGENT = "agent"


def _clause(s: str) -> str:
    """A fragment that reads correctly embedded mid-sentence in a directive."""
    return s.strip().rstrip(".")


def _sentence(s: str) -> str:
    """A fragment given a terminating period, so the verdict reads as prose."""
    s = s.strip()
    if not s or s[-1] in ".!?":
        return s
    return s + "."


def _one_line(s: str) -> str:
    """Collapse a summary to one line.

    The verdict's whole value is that `head -1` and `tail -1` both yield the
    whole of it, which an embedded newline quietly destroys.
    """
    return " ".join(s.split())


class Refusal(click.ClickException):
    """A refusal that tells an agent whether to report it.

    A `ClickException`, so raising one behaves exactly as raising a
    `ClickException` did: Click catches it, prints `Error: <message>` to stderr
    and exits with `exit_code`. What changes is what the message SAYS when an
    agent is the one reading it.

    For a human the message is `text` — today's message, byte for byte. Nothing
    the classification added appears there. The verdict and the directive are
    ADDITIVE and reach the agent only, which is what makes this a change nobody
    has to re-approve: a person's stderr after E-2159 is what it was before it.

    The one exception runs the other way. `human_remedy` names a bypass today's
    message already offers — `--force`, a destructive git escape — and it is
    REMOVED for an agent rather than added for a person. A bypass named in a
    refusal is a bypass an agent will take, which is the opposite of stopping to
    ask.

    Do not construct this directly; use the class-named factories below.
    """

    def __init__(self, cls, summary, text=None, *, remedy="", decision="",
                 condition="", human_remedy="", detail="", command=None,
                 exit_code=1):
        self.cls = cls
        self.summary = summary
        self.text = text if text is not None else summary
        self.remedy = remedy
        self.decision = decision
        self.condition = condition
        self.human_remedy = human_remedy
        self.detail = detail
        # Resolved NOW, not when the message is rendered. Click renders a
        # ClickException by calling show() from its own error handler, which
        # runs AFTER the command's context has exited — so asking for the
        # current command path at render time gets None, and the verdict loses
        # the one thing that says which verb refused. Construction happens
        # inside the command; that is the only moment the answer exists.
        self.command = command if command is not None else current_command_path()
        super().__init__(self.text)
        self.exit_code = exit_code

    # ---- rendering ---------------------------------------------------------

    def verdict(self) -> str:
        """The one-line summary-plus-directive an agent reads."""
        where = self.command
        head = f"{ERROR_SENTINEL} {where}: " if where else f"{ERROR_SENTINEL}: "
        return head + " ".join(
            p for p in [_one_line(self.summary), *self._directive()] if p
        )

    def _directive(self) -> list[str]:
        if self.cls == NO_REPORT:
            return [_sentence(self.remedy), _NO_REPORT_DIRECTIVE]
        if self.cls == REPORT:
            return [f"This needs the user: {_clause(self.decision)}.",
                    _REPORT_DIRECTIVE]
        if self.cls == REPORT_IF:
            return [f"If {_clause(self.condition)}, stop and ask — "
                    f"{_clause(self.decision)}; otherwise {_clause(self.remedy)} "
                    f"and {_REPORT_IF_DIRECTIVE}"]
        if self.cls == FAULT:
            return [_FAULT_DIRECTIVE]
        if self.cls == WARN:
            return [_sentence(self.remedy), _WARN_DIRECTIVE]
        if self.cls == INFO:
            return []
        return [_FAULT_DIRECTIVE]

    def _body(self, *, for_agent: bool) -> str:
        """The message proper: today's text, plus the human-only remedy.

        The remedy joins with a SPACE when it continues the sentence and with
        NOTHING when it starts its own line. A bypass is sometimes a trailing
        clause ("Pass --force to remove it anyway.") and sometimes its own
        paragraph, and today's message already carries whichever it is — so an
        unconditional space would leave a trailing one at the end of the line
        before a paragraph, which is a byte a person's message did not have.
        """
        body = self.text or ""
        if self.human_remedy and not for_agent:
            if not body or self.human_remedy.startswith(("\n", " ")):
                body += self.human_remedy
            else:
                body = f"{body} {self.human_remedy}"
        if self.detail:
            body = body + "\n" + self.detail.rstrip("\n")
        return body

    def format_message(self) -> str:
        """The message Click prints, for whichever audience is reading.

        Computed here rather than at construction because the audience is
        settled by the root group's argv pre-scan, which runs before any command
        body but after some module-level constants are built.

        A multi-line message is bracketed by identical verdicts (E-2097): head
        leaves the first, tail leaves the last, and either is sufficient alone.
        A message that is already one line is returned as the verdict by itself
        — no pipe can split a single line, so a second copy would be noise
        rather than insurance.
        """
        if self.cls == INFO or not agent_facing():
            return self._body(for_agent=False)
        verdict = self.verdict()
        body = self._body(for_agent=True)
        if "\n" not in body:
            return verdict
        return f"{verdict}\n\n{body}\n\n{_CLICK_ERROR_PREFIX}{verdict}"


# ---- the class-named factories ---------------------------------------------
#
# Each returns a Refusal to RAISE. Returning rather than raising keeps `raise`
# at the call site, where a reader looking for the exit sees it.


def no_report(summary, remedy, *, text=None, detail="", command=None,
              exit_code=1, human_remedy=""):
    """A refusal the agent resolves alone. `remedy` says what to do instead."""
    return Refusal(NO_REPORT, summary, text, remedy=remedy, detail=detail,
                   command=command, exit_code=exit_code,
                   human_remedy=human_remedy)


def report(summary, decision, *, text=None, detail="", command=None,
           exit_code=1, human_remedy=""):
    """A refusal only the user can clear.

    `decision` names the judgment that is theirs — phrased as the thing being
    decided ("whether to remove a worktree a live session is using"), not as an
    instruction.
    """
    return Refusal(REPORT, summary, text, decision=decision, detail=detail,
                   command=command, exit_code=exit_code,
                   human_remedy=human_remedy)


def report_if(summary, condition, remedy, decision, *, text=None, detail="",
              command=None, exit_code=1, human_remedy=""):
    """A refusal whose class turns on something the command cannot see.

    `condition` is the test ("the user did not ask for the subtree to go"),
    `decision` the consequence of getting it wrong ("removal cannot be undone"),
    `remedy` the other branch ("retry with --cascade").

    Reach for it only when the command genuinely cannot resolve the condition
    itself. Where it can — from the actor, the call path, process ancestry, or
    state already in hand — resolving it in code and raising a definite class is
    strictly better than handing the agent a question.
    """
    return Refusal(REPORT_IF, summary, text, condition=condition, remedy=remedy,
                   decision=decision, detail=detail, command=command,
                   exit_code=exit_code, human_remedy=human_remedy)


def fault(summary, *, text=None, detail="", command=None, exit_code=1):
    """Endless itself breaking.

    Also where an unclassified failure lands: an uncaught exception reaching the
    root group renders as one, so the failure mode of forgetting to classify is
    "tell the user", not "stay silent".
    """
    return Refusal(FAULT, summary, text, detail=detail, command=command,
                   exit_code=exit_code)


class Relay(Refusal):
    """endless-go's own refusal, passed through with no second directive.

    Go classified it at the site that raised it and its verdict lines are
    already at both ends of the text, so the right thing to do here is
    NOTHING — no re-wording, no `Error: ` prefix in front of Go's sentinel, no
    Python directive contradicting Go's. `show` writes the bytes and stops.
    """

    def show(self, file=None):
        click.echo(self.text.rstrip("\n"), err=True)


def relay(stderr_text, *, exit_code=1, command=None):
    """Relay endless-go's stderr verbatim. See Relay."""
    return Relay(FAULT, _one_line(stderr_text) or "endless-go refused.",
                 stderr_text, command=command, exit_code=exit_code)


def relay_foreign(refusal, child_stderr):
    """Attach a FOREIGN child's own output to a refusal classified at the site.

    git, a project hook script, a shell. Unlike `relay`, nothing upstream
    classified this: only the site knows what it shelled out to do and what a
    failure means, so it builds the refusal and this just carries the child's
    words along as detail. Passing the output through matters — a git error
    quoted verbatim is usually the whole diagnosis, and rewording it in
    Endless's voice loses that.
    """
    child = (child_stderr or "").rstrip("\n")
    if child:
        refusal.detail = (refusal.detail + "\n" + child).strip("\n")
    return refusal


def info(text, *, err=False):
    """Print a progress notice. Not a refusal: no verdict, no directive.

    It exists so that a site writing such a line still names a class, which is
    what keeps the AST check enforceable without an exemption list.
    """
    click.echo(text, err=err)


class warn:
    """Warnings: something the reader should know that blocks nothing.

    Most warnings should not be here. One the USER should act on but that blocks
    nothing belongs in the errors channel — `warn.record` — where it reaches
    them through the session-status badge and `endless errors show`, and costs
    the agent nothing. `warn.no_report` and `warn.report` are for the ones that
    are genuinely about the command the reader just ran.
    """

    @staticmethod
    def no_report(summary, remedy, *, text=None, command=None):
        click.echo(Refusal(WARN, summary, text, remedy=remedy,
                           command=command).format_message(), err=True)

    @staticmethod
    def report(summary, decision, *, text=None, command=None):
        click.echo(Refusal(REPORT, summary, text, decision=decision,
                           command=command).format_message(), err=True)

    @staticmethod
    def record(code, summary, *, source="", detail="", fingerprint=""):
        """Send a warning to the errors channel instead of to stderr.

        The user sees it on the session-status badge and in `endless errors
        show`; the agent spends nothing on it. `code` must name an entry in the
        internal/faults catalog — `endless-go errors record` refuses an unknown
        one, which is what stops this becoming a way to invent classifications
        that have no docs/errors.md section.

        Never raises. A diagnostic that fails the command it was diagnosing is
        worse than a diagnostic that is lost.
        """
        import subprocess
        from endless import config

        args = ["endless-go", *config.go_db_context_args(), "errors", "record",
                "--code", code, "--summary", summary]
        for flag, value in (("--source", source), ("--detail", detail),
                            ("--fingerprint", fingerprint)):
            if value:
                args += [flag, value]
        try:
            subprocess.run(args, capture_output=True, timeout=10, check=False)
        except Exception:
            pass


def passthrough_exit(code):
    """End with a child process's own exit code, printing nothing.

    The child already wrote whatever it had to say to the stderr it inherited.
    Adding a message here would mean explaining, in Endless's voice, an outcome
    Endless did not produce.
    """
    raise SystemExit(code)


# ─── the standalone loop, and the two failures nobody classifies per site ────


def abort(refusal):
    """Print a refusal and end the process.

    For the paths that run BEFORE Click can catch an exception — the root
    group's argv pre-scan, which consumes `--db` and `--agent-view` by hand.
    Raising there would escape `main()` as an uncaught exception and be
    rendered as a fault, which would be a lie about a mistyped flag.
    """
    refusal.show()
    raise SystemExit(refusal.exit_code)


def _usage_command(e):
    """The verb a Click usage error came from, for its verdict line.

    Click attaches the context it failed in, which knows the full command path;
    outside one — a parse failure before any subcommand was resolved — there is
    no verb to name, and the verdict says so by omitting it.
    """
    ctx = getattr(e, "ctx", None)
    if ctx is None:
        return None
    parts = ctx.command_path.split(" ", 1)
    return parts[1] if len(parts) > 1 else None


def _usage_summary(e):
    """One line naming what was wrong with the invocation.

    Click's own usage errors are one line each, except FormatArg's
    `--format agent` refusal, which is deliberately three so a reader learns
    that the spelling was right and the command has no agent rendering. Taking
    the first line keeps the verdict scannable; the full text is still the body.
    """
    return e.format_message().split("\n", 1)[0].strip()


def run_standalone(invoke):
    """Run a Click command and give a class to the failures no site raises.

    Two of them, and both are classified HERE because classifying them at the
    site is impossible:

      - Click's own usage errors. An unknown option, a missing argument, a
        `ParamType.fail()` — Click raises these itself, from inside its parser,
        and they are NO-REPORT usage by definition: the invocation was
        malformed, the agent rewrites it, and the user never needs to hear that
        a flag was misspelled.

      - An uncaught exception. It reached here because nobody expected it, so
        nobody classified it, and E-2159's decision 4 says what to do with
        that: render it as a FAULT. Forgetting therefore fails towards "tell
        the user", not towards an agent quietly swallowing a bug.

    `invoke` is the command's own `main(..., standalone_mode=False)`. Click's
    standalone mode does the printing and exiting itself, which is why it is
    switched off and its block replicated here — faithfully, including the
    EPIPE case, which is not an error at all: a reader closed the pipe, and
    Endless has nothing to say about that to anybody.

    It lives in this module rather than in `cli` because these exits ARE the
    refusal machinery, and the machinery belongs where `Refusal.show` does.
    """
    import errno
    import traceback

    try:
        invoke()
    except click.exceptions.Exit as e:
        raise SystemExit(e.exit_code)
    except click.exceptions.Abort:
        info("Aborted!", err=True)
        raise SystemExit(1)
    except click.UsageError as e:
        # A Refusal is already classified; only Click's own are not.
        #
        # The MESSAGE is rewritten in place rather than the exception being
        # replaced. UsageError.show prints the context's usage block and the
        # "Try '<cmd> --help'" line before the message, and both come from
        # `e.ctx` — swapping in a different exception drops them, which would
        # change what a person reads.
        if not isinstance(e, Refusal):
            e.message = no_report(
                _usage_summary(e),
                "Correct the invocation and retry",
                text=e.format_message(),
                command=_usage_command(e),
            ).format_message()
        e.show()
        raise SystemExit(e.exit_code)
    except click.ClickException as e:
        e.show()
        raise SystemExit(e.exit_code)
    except OSError as e:
        if e.errno != errno.EPIPE:
            raise
        # Somebody piped us into `head`. Not a failure, and not ours to narrate
        # — Click's own wrapper swallows the flush that would otherwise raise
        # again on the way out.
        sys.stdout = click.utils.PacifyFlushWrapper(sys.stdout)
        sys.stderr = click.utils.PacifyFlushWrapper(sys.stderr)
        raise SystemExit(1)
    except (KeyboardInterrupt, SystemExit):
        raise
    except Exception as e:
        fault(
            f"{type(e).__name__}: {e}",
            detail="".join(traceback.format_exception(e)).rstrip("\n"),
        ).show()
        raise SystemExit(1)
    raise SystemExit(0)


def agent_block(ctx: click.Context) -> str | None:
    """The directive text to prepend, or None when there's nothing to add."""
    cmd_path = _command_path(ctx)
    if not cmd_path:
        return None  # the root group already points at the guide via its content

    entry = load_map(cmd_path)
    header = ("\033[1m▸ AGENT — read this before using this command:\033[0m"
              if _color(ctx) else "▸ AGENT — read this before using this command:")
    lines = [header]

    if entry is None:
        lines.append("    No guide section is mapped to this command yet. Run "
                     "`endless guide` for the index.")
    elif entry.sections:
        # Just the directive — the agent is being told which section to read,
        # not choosing among sections, so the section's `covers` summary adds
        # noise here. `covers` earns its place in the index table (`endless
        # guide`), where you scan to *find* the section. Command-specific notes
        # stay: they're not in the guide.
        for slug in entry.sections:
            lines.append(f"    endless guide {slug}")
        if entry.note:
            for note_line in entry.note.splitlines():
                lines.append(f"    {note_line}")
    elif entry.gap:
        lines.append(f"    No guide section covers this yet — {entry.gap}")
        lines.append("    Run `endless guide` for the index.")
    else:
        lines.append("    Run `endless guide` for the index.")
    return "\n".join(lines) + "\n"


def _color(ctx: click.Context) -> bool:
    try:
        return ctx.color is not False and os.isatty(1)
    except Exception:
        return False


class AgentHelpMixin:
    """Mixin for Click Command/Group: prepend the agent block to --help.

    Mixed in by `AgentAwareCommand`/`AgentAwareGroup` in cli.py. Never raises
    into help rendering — a missing/garbled map file degrades to no block.
    """

    def format_help(self, ctx, formatter):  # type: ignore[override]
        if agent_facing():
            try:
                block = agent_block(ctx)
            except Exception:
                block = None
            if block:
                formatter.write(block)
                formatter.write("\n")
        super().format_help(ctx, formatter)
