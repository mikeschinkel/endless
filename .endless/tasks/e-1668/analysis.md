Widened 2026-09-12 to absorb E-2132 (obsolete, replaced_by this). Owner's call: one task, not two — ED-1550 rule 3.

== HALF 1: stop the guess (this task's original scope) ==

E-1368's cwd self-detect satisfies the E-1429 gate. dbContextExplicit() is 'dbContextDir != "" || dbPathOverride != ""', and cmd/endless-go/main.go calls monitor.SelfDetectWorktreeSandbox() for every subcommand at startup, which sets dbContextDir from CWD. So a guessed context satisfies a gate whose own doc comment says only a flag or ForceRealDB counts.

The sibling comment on dbContextDir forbids exactly this for XDG_CONFIG_HOME: 'an env var can be exported once and silently route every later command to the wrong DB -- the exact failure mode the gate exists to kill'. CWD does the same thing without even an export.

The distinction is already modelled and simply never consulted: dbContextFromFlag separates an explicit flag from a cwd-detected sandbox, and SelfDetectWorktreeSandbox deliberately calls setDetectedContextDir rather than SetDBContextDir so it will not suppress the hook/tmux main pin. The principle to encode: detection decides WHERE TO LOOK; only a flag decides THAT YOU MAY OPEN IT. Config and log routing can stay CWD-derived without granting permission to read or write rows.

Python already refuses without --db inside a self-dev worktree (E-1429 gated it at both layers); it is the Go backstop that was defeated, which is why the damage surfaces only on direct endless-go invocations.

== HALF 2: state the answer (absorbed from E-2132) ==

Owner decisions, 2026-09-11/12:
  1. Announce ALWAYS, not only when the scope was inferred — for the DATABASE, in a self-dev project.
  2. Machine formats carry it as a FIELD IN THE PAYLOAD, not on stderr.
  3. Exempt contexts PINNED IN CODE (hook ForceRealDB; tmux, session-status, project-status PinMainDB). If the caller could not have influenced the choice there is nothing to disambiguate, and the tmux status line has no room for it.
  4. endless-go takes --db main|sandbox; --config-dir retires, with a --db-dir escape for callers that must name a directory without owning the process environment.
  5. WRITES announce as well as reads. E-1429's founding incident was a write — a test 'endless task add' that landed in the real ledger as E-1425 — and a write to the wrong database is the damaging case, where a wrong read only misleads.
  6. Downstream (non-self-dev), where --db does not exist and the PROJECT is the only half that reaches the user: announce the project ONLY when it is not the one enclosing cwd. With one database and usually one project, always-on is repetition.

Decisions 1, 5 and 6 are one rule, and the plan states it as one: ANNOUNCE WHAT THE INVOCATION RESOLVED, WHEN IT COULD HAVE RESOLVED OTHERWISE. In self-dev the database always could have; downstream the project only sometimes could; a pin never could.

Decision 2 departs from rowcap.echo_footer's precedent, which sends the omission trace to stderr for --json/--tsv so '| jq' will not choke. The departure is deliberate: that footer is prose COMMENTARY ABOUT an answer and would corrupt a payload, whereas the store a result came from is DATA ABOUT THE RESULT and belongs in it.

Why stderr lost, from the agent's own account of the incident below: in that session it wrote 2>/dev/null, 2>&1 | head, | tail -5 and 2>&1 | python3 -c repeatedly, each of which discards stderr or buries it under a truncation. Ranked by what actually reaches an agent: (1) a field in the structure it parses, near-unmissable because it destructures it; (2) stdout it quotes back, high, because what is in the pasted text is in the claim it makes; (3) stderr, moderate, and hardest to see when the command SUCCEEDED and returned the wanted data; (4) documentation read at session start, near zero at the moment of error. Hook stderr is weaker still — it arrives as system-reminder text an agent is told to treat as background.

== THE INCIDENT THAT RE-FOUND THIS ==

A session working E-2105 ran 'endless-go session-query list-live --project-root <main checkout>' from inside its worktree, using MAIN's binary — precisely the foreign-binary case this task was filed for. --project-root named the main checkout; the binary silently answered from the worktree sandbox, returning 2 rows where main holds 59. The session reported it to the owner as a probable product defect, having also unset XDG_CONFIG_HOME in the belief that this was what selected the sandbox. It then ran the same command against two different binaries, got identical output, and read the agreement as corroboration — a sound control for 'did my change alter this?' and none at all for 'is this number right?'. The refusal this task exists to restore would have ended it at the first command.