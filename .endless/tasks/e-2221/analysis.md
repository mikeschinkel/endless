## The open design question, which is the whole cost

"Duplicated" judged by display means answering "which live boards currently show this task", and board membership is computed rather than stored: the focal task plus its epic children, dependents, blockers and frames. Ownership needs one indexed query over `session_tasks`; display membership needs every live session's board evaluated, or membership materialised somewhere it can be read back.

`session status` repaints every two seconds, and the ◆/~ column is the precedent for what the render path may pay for: the expensive half is computed by a background job and READ by the display, never computed by it (E-1758, and the unlanded-verdict cache under `.git/info/`).

Settle before implementing:

- **What counts as a duplicate?** "Shown on two live boards", or "shown on two live boards for a reason that implies work"? A blocker listed on five boards is co-appearance by construction, and marking all of it produces noise rather than signal — the opposite of what the marker is for.
- **Are board frames in scope?** An epic parent frame appears on the board of every one of its children by construction. Marking all of those says little; answering "no" leaves the reported symptom (E-1486, an epic) unfixed. That tension is the heart of this task.
- **Where is it computed?** If per-repaint evaluation of every live board is too expensive, this needs the cached-verdict shape rather than a wider query on the render path.
- **Does the existing vocabulary still fit?** `DuplicateWork` and `OwnedElsewhere` currently split on ownership. If duplication becomes a display property, decide whether they remain two fields, and what `OwnedElsewhere` means for a row nobody owns.
