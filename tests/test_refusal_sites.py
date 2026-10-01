"""Every refusal says whether the agent must report it (E-2159).

An agent reports a refusal only when it cannot continue without input from the
user. "You must do X instead of Y" is not such a case: the agent calls the
command again doing X, and the user never needs to hear about it. The failure
that opened this line of work was a refusal the agent handled correctly and
then narrated in its handoff anyway, because nothing in the message said not
to.

A bare `raise click.ClickException("...")` cannot say that, so it is not
allowed to exist. `endless.agent_help` owns refusals, and its factories are
named for the classes, which makes "did anyone decide?" a question this file
answers rather than one a reviewer has to.

The Go half is guarded the same way, by TestNoStderrOutsideThisPackage in
internal/refusal, over os.Stderr instead of over these constructors. One rule,
two enforcements, because Endless refuses in two languages and an agent reading
its scrollback should not have to know which one produced a given line.
"""

import ast
from pathlib import Path

SRC = Path(__file__).resolve().parent.parent / "src" / "endless"

#: agent_help defines the Refusal class and its factories, so it is the one
#: module that necessarily constructs the things named below. There is no other
#: exemption and there is deliberately no allowlist of unconverted sites: an
#: allowlist would be exactly the unclassified default this rule exists to end.
HELPER_MODULE = "agent_help.py"

#: Exception types that put a message in front of a user. Subclasses count:
#: `class Foo(click.ClickException)` is the same act with a longer spelling.
REFUSAL_TYPES = frozenset({
    "ClickException", "UsageError", "BadParameter", "BadOptionUsage",
    "BadArgumentUsage", "NoSuchOption", "MissingParameter", "FileError",
    "Refusal", "Relay",
})

RULE = """

  A refusal must tell the agent reading it whether to report it to the user.
  Raise one of endless.agent_help's class-named factories instead, and pick the
  class from ONE question — can the agent continue without asking the user?

    agent_help.no_report(summary, remedy)
        It can. "Do X instead of Y" is this class: the agent retries doing X
        and the user never hears about it. Most refusals are this.

    agent_help.report(summary, decision)
        It cannot. `decision` names the judgment that is the user's to make,
        phrased as the thing being decided, not as an instruction.

    agent_help.report_if(summary, condition, remedy, decision)
        It depends on something this command cannot see — usually what the user
        asked for. Name both branches; the agent, which holds the conversation,
        decides. Resolve the condition in code wherever you can and raise a
        definite class instead.

    agent_help.fault(summary)
        Endless itself broke.

    agent_help.relay(stderr_text)          endless-go's own, already classified
    agent_help.relay_foreign(refusal, out) git or a hook script: classify here
    agent_help.warn.no_report / .report    nothing is blocked
    agent_help.warn.record(code, summary)  the errors channel, not stderr
    agent_help.info(text)                  a notice; no verdict, no directive
    agent_help.passthrough_exit(code)      a child's exit, printing nothing

  A warning the USER should act on that blocks nothing does not belong on
  stderr at all — warn.record puts it in the errors channel, where they see it
  on the session-status badge and in `endless errors show`, and the agent
  spends nothing on it.

  Click's own usage errors and an uncaught exception are classified once, in
  the root group (cli.DBAwareGroup._classified_main). Nothing else needs to —
  and that is why `ParamType.fail()` is not listed here: it raises a UsageError,
  which the root group already classifies NO-REPORT, and rewriting those sites
  would cost Click's "Invalid value for '--flag'" context for nothing.
"""


def _python_files():
    return sorted(p for p in SRC.rglob("*.py") if p.name != HELPER_MODULE)


def _attr_path(node):
    """'click.ClickException' for an Attribute/Name chain, else ''."""
    parts = []
    while isinstance(node, ast.Attribute):
        parts.append(node.attr)
        node = node.value
    if isinstance(node, ast.Name):
        parts.append(node.id)
    return ".".join(reversed(parts))


def _is_nonzero_exit(node):
    """A `sys.exit(2)` / `ctx.exit(1)` / `SystemExit(1)` with a non-zero code.

    Zero is not a refusal — it is a command finishing — and a NON-LITERAL
    argument is counted, because `sys.exit(code)` is how a child's failing exit
    status is passed on and that is `passthrough_exit`'s whole job.
    """
    if not node.args:
        return False
    arg = node.args[0]
    if isinstance(arg, ast.Constant):
        return arg.value not in (0, None)
    return True


def _offenders(tree):
    """(line, what) for every unclassified refusal in one parsed module."""
    found = []
    for node in ast.walk(tree):
        # `class Foo(click.ClickException)` — a refusal type by inheritance.
        if isinstance(node, ast.ClassDef):
            for base in node.bases:
                name = _attr_path(base).rsplit(".", 1)[-1]
                if name in REFUSAL_TYPES:
                    found.append((node.lineno,
                                  f"class {node.name}({_attr_path(base)})"))
            continue

        if not isinstance(node, ast.Call):
            continue
        path = _attr_path(node.func)
        tail = path.rsplit(".", 1)[-1]

        if tail in REFUSAL_TYPES:
            found.append((node.lineno, f"{path}(...)"))
        elif path in ("sys.exit", "os._exit", "ctx.exit"):
            if _is_nonzero_exit(node):
                found.append((node.lineno, f"{path}(<non-zero>)"))
        elif tail == "SystemExit":
            found.append((node.lineno, "SystemExit(...)"))
        elif path in ("click.echo", "click.secho"):
            for kw in node.keywords:
                if kw.arg == "err" and not (
                        isinstance(kw.value, ast.Constant)
                        and kw.value.value is False):
                    found.append((node.lineno, f"{path}(..., err=True)"))
        elif path == "print":
            for kw in node.keywords:
                if kw.arg == "file" and _attr_path(kw.value) == "sys.stderr":
                    found.append((node.lineno, "print(..., file=sys.stderr)"))
        elif path == "sys.stderr.write":
            found.append((node.lineno, "sys.stderr.write(...)"))
    return found


def test_every_refusal_names_a_class():
    bad = []
    scanned = 0
    for path in _python_files():
        tree = ast.parse(path.read_text(), filename=str(path))
        scanned += 1
        rel = path.relative_to(SRC.parent.parent)
        for line, what in _offenders(tree):
            bad.append(f"  {rel}:{line}  {what}")

    assert scanned > 40, f"the walk only reached {scanned} modules — did it stop early?"
    assert not bad, "unclassified refusals:\n" + "\n".join(sorted(bad)) + RULE


def test_the_check_can_see_an_offender():
    """Guard the guard.

    A source scan that silently reaches nothing passes forever while enforcing
    nothing, which is the failure mode every check of this shape has.
    """
    sample = ast.parse(
        "import sys, click\n"
        "class Custom(click.ClickException): pass\n"
        "def f():\n"
        "    raise click.ClickException('x')\n"
        "    raise click.UsageError('y')\n"
        "    click.echo('z', err=True)\n"
        "    print('w', file=sys.stderr)\n"
        "    sys.stderr.write('v')\n"
        "    sys.exit(2)\n"
        "    ctx.exit(1)\n"
    )
    found = {what for _, what in _offenders(sample)}
    for expected in (
        "class Custom(click.ClickException)",
        "click.ClickException(...)",
        "click.UsageError(...)",
        "click.echo(..., err=True)",
        "print(..., file=sys.stderr)",
        "sys.stderr.write(...)",
        "sys.exit(<non-zero>)",
        "ctx.exit(<non-zero>)",
    ):
        assert expected in found, f"the matcher no longer recognises {expected}"


def test_the_check_allows_what_is_not_a_refusal():
    """Ordinary output and a clean exit are not refusals and must stay quiet."""
    sample = ast.parse(
        "import sys, click\n"
        "def f():\n"
        "    click.echo('to stdout')\n"
        "    click.echo('explicitly stdout', err=False)\n"
        "    sys.exit(0)\n"
        "    ctx.exit()\n"
        "    print('plain')\n"
    )
    assert _offenders(sample) == []
