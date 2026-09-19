# One model does all three jobs

    minimizer   sonnet/medium   follows the instruction, produces the reply
    judge       sonnet/medium   scores that reply
    generator   sonnet/medium   writes the candidate instructions

A challenger instruction is written by sonnet, executed by sonnet, and graded by
sonnet. Promotion is decided on that grade.

The failure this invites is not a bug that shows up in a test. It is a bias: a
scorer sharing the writer's blind spots will systematically prefer replies in its
own idiom, and the loop will promote toward that idiom while reporting that
quality improved. Nothing in the corpus can distinguish "genuinely better" from
"more agreeable to sonnet", because sonnet is the only opinion recorded.

The agent/minimizer pair is already independent — Opus writes the draft, sonnet
cuts it — which is why the cut has real adversarial value. The scoring pair has
no such split.

# What to do

Make the model per ROLE selectable, and require the judge to differ from the
minimizer and the generator. `config.internal_model(purpose)` already exists for
exactly this shape (`verb_check`, `triage`); the minimizer's three roles are
pinned in code instead, with a comment arguing that a per-machine override would
make two installs disagree about what "minimized" means. That argument holds for
the MINIMIZER and does not hold for the judge: a judge that agrees with the
writer is worth less than one that does not, whoever runs it.

Mike intends to allow a different vendor (ChatGPT) as the adversary. That breaks
the agent/minimizer correlation, which is already broken. The one worth breaking
is the judge.

# How to know it mattered

Before changing anything, re-score a slice of the existing corpus with a
different model and compare per-row fidelity against what sonnet recorded. If
they agree closely, the concern is theoretical and this can be closed cheaply. If
they diverge, the divergence is the size of the bias the promotion gate is
currently blind to.
