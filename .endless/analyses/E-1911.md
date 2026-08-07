## Why the current shape fails

`task report` computes only what the user cannot already derive, prints a
steering prompt telling the agent to relay just that, and a PostToolUse hook
forbids adding anything else. Two distinct failure modes follow.

1. **It reports state, not deltas.** The inputs are current task status,
   follow-ups filed, gated notes, and worktree cleanliness. None of those can
   express "the thing you personally watched fail is now fixed." After a
   `just land` fails in the user's terminal and the agent repairs it, every
   computed input reads normal — so the report emits "nothing to report" at
   exactly the moment the user's last observation is stale and the actionable
   fact (branch resolved, re-landable) is the only thing worth saying.

2. **It makes the agent the filter.** Deciding which facts survive the cut is a
   judgment call made under a hook that forbids hedging. A wrong cut is
   invisible to the user, because the omitted material never appears.

## The inversion

Let the agent answer verbosely, then append the concise report after an
explicit separator. Properties this buys:

- Nothing Endless would have forced out is lost — the user sees both halves.
- The concise half can be molded **incrementally**, one response shape at a
  time, instead of having to be correct for every kind of agent reply up front.
  That is the practical point: the current all-or-nothing contract has to be
  fixed everywhere at once, which is why its unintended consequences cannot be
  tackled one at a time.
- Verbosity stops being the thing under control. The concise block carries the
  guarantee; the verbose block carries the context.

## Scope to settle

- The separator's form, and whether it must be machine-detectable (a Stop hook
  that validates the concise half needs to find it reliably).
- Whether the concise block is emitted by `task report` itself or appended by a
  Stop hook after the agent's reply.
- How the concise half evolves: which response shapes it must eventually cover
  (clean handoff, recovered failure, blocked, question-for-user, no-op turn).

## Dependency — E-1901

E-1901 (Enforce verbatim task report relay with a Stop-hook) is `unverified`
and enforces the OPPOSITE contract: the report IS the entire message. A
two-part reply is precisely what that hook rejects. E-1901 must either learn
the separator or be sequenced behind this task. Do not implement the append
before resolving that.

## Observed instance (E-1905)

A `just land` failed on a rebase conflict; the agent resolved it, re-verified
green, and ran `task report`. The report emitted "nothing to report", so the
relayed message carried three --json notes but omitted the actionable fact that
the branch was resolved and re-landable. The user had to ask for it explicitly.
