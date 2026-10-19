Requested by the user:

- `endless task steward E-N` prints the steward session of task E-N as ES-N alone on stdout, the form `session goto` accepts unambiguously, so `endless session goto $(endless task steward E-N)` works. With no steward, or an ambiguous one, it prints nothing on stdout, says which on stderr (naming the candidates when ambiguous), and exits non-zero.
- `endless session goto --steward E-N` goes to that steward session directly.
- `endless task show E-N` gains a `Steward:` line, for example `Steward: ES-1352 (E-2300)`, `Steward: ambiguous (ES-1, ES-2)` or `Steward: none`.
- `endless task update E-N --steward E-M` makes the session that claimed E-M the steward of E-N. A session holds exactly one task, so its claimed task names it.

Stewardship stays calculated. `--steward` writes the existing per-board rows: it pins E-N on the chosen session's board (as `session unhide --task` does) and hides it on every other candidate's board (as `session hide --task` does). The result still corrects itself when that session ends.
