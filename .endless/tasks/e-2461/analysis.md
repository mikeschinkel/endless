Decided with the user in the planning conversation:

- No override past a blocker, only `endless task unblock`. A blocks link is the hard dependency: if the code does not depend on the blocker, the link is wrong and removing it corrects the record. This matches claim, spawn and prime (E-2296).
- A blocker releases only when terminal (confirmed, assumed, completed, declined, obsolete, superseded). A `landed` blocker, merged but with post-land items open, still blocks, as E-2444 states and as claim reads it. One meaning of "blocked" for every caller.

Decided without asking, open to objection:

- Blockers in other projects count, as they do for claim; E-2405's land past a go-cfgstore blocker is the case that prompted this.
- The check runs before anything moves, and `--dry-run` refuses the same way.
- `--record-only` skips it: it records a land that already happened.
- A re-run whose branch is already merged skips it: refusing then protects nothing and leaves the database unmigrated and the land unrecorded.
