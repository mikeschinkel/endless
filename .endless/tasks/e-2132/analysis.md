One cause, two halves: the scope a command uses is inferred from ambient state and never declared. Stop the guess, and state the answer. Filed as one task per ED-1550 rule 3.

== HALF 1: stop the guess ==

The sibling comment on dbContextDir forbids exactly this for XDG_CONFIG_HOME: 'Deliberately NOT satisfied by XDG_CONFIG_HOME: an env var can be exported once and silently route every later command to the wrong DB -- the exact failure mode the gate exists to kill. Only a per-invocation flag (or the hook's ForceRealDB) counts as explicit.' CWD does the same thing without even an export, and the guard accepts it.

The distinction is already modelled. dbContextFromFlag exists to separate an explicit --config-dir from a cwd-self-detected sandbox, and SelfDetectWorktreeSandbox deliberately calls setDetectedContextDir rather than SetDBContextDir so it will not suppress the hook/tmux main pin. Everything needed is present; dbContextExplicit() simply reads the wrong one of the two. Detection should decide WHERE TO LOOK; only a flag should decide THAT YOU MAY OPEN IT -- config and log routing can stay CWD-derived without granting permission to read or write rows.

Blast radius. Unaffected: hooks (ForceRealDB sets dbPathOverride), tmux / session-status / project-status (PinMainDB), verify suites and tests (--config-dir), and the sandbox subcommand (no DB init). Affected deliberately: un-flagged DB-opening invocations from inside a self-dev worktree, including the Python CLI when no --db was passed. OPEN QUESTION for the plan: does the Python CLI keep self-detecting and thread --config-dir itself, or must it too refuse? That is the ergonomic call and the reason this needs a plan rather than a one-line patch.

== HALF 2: state the answer ==

Owner decisions, taken 2026-09-11:
  1. Announce ALWAYS, on every read -- not only when the scope was inferred rather than named. Repetition is cheaper than a rule about when to look.
  2. Machine formats carry it as a FIELD IN THE PAYLOAD, not on stderr.

Decision 2 departs from rowcap.echo_footer's precedent, which sends the omission trace to stderr for --json/--tsv so '| jq' will not choke, and the departure is deliberate: that footer is prose COMMENTARY ABOUT an answer and would corrupt a payload, whereas the store a result came from is DATA ABOUT THE RESULT and belongs in it. For array payloads the field goes per-row (non-breaking; rows such as list-live's already carry project_id); for object payloads, once at top level. --llm and human output carry it in-band on stdout; session list already prints 'Sessions - project: probe' and that header is the precedent.

Why stderr was rejected, from the agent's own account: in the incident session it wrote 2>/dev/null, 2>&1 | head, | tail -5 and 2>&1 | python3 -c repeatedly, each of which discards stderr or buries it under a truncation. Ranked by what actually reaches an agent: (1) a field in the structure it parses, near-unmissable because it destructures it; (2) stdout it quotes back, high, because what is in the pasted text is in the claim; (3) stderr, moderate and hardest to see when the command SUCCEEDED and returned the wanted data; (4) documentation read at session start, near zero at the moment of error. Hook stderr is weaker still -- it arrives as system-reminder text an agent is told to treat as background.

Scope note. The DATABASE half is self-dev-only: apply_db_choice refuses --db outright on a non-self-dev project because such a project has one DB. The PROJECT half reaches everyone -- session list, session-query list-live and project-status all scope to 'the project enclosing cwd', so a command run from a worktree, a sibling checkout, or a directory inside a different registered project answers about a project the caller did not name.

== THE INCIDENT ==

A session working E-2105 ran 'endless-go session-query list-live --project-root <main checkout>' from its worktree. --project-root named the main checkout; the binary silently answered from the worktree sandbox, returning 2 rows where main holds 59. The session reported it to the owner as a probable product defect, having also unset XDG_CONFIG_HOME in the belief that this was what selected the sandbox. It then ran the same command against two different binaries, got identical output, and read the agreement as corroboration -- a sound control for 'did my change alter this?' and none at all for 'is this number right?'. The refusal that would have ended this at the first command was already written and already unreachable.