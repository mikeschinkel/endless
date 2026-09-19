# Register verbs automatically via claude haiku when task add rejects a title

## Problem
`task add` rejects titles whose first word isn't a registered verb. Claude
sessions take the path of least resistance — rewriting the title instead of
registering the verb — because rewriting is one command and `verb add` requires
writing a definition. This repeats across sessions: the same agents propose the
same verbs and get the same rejection, wasting tokens.

Three transcripts in 2026-05 sessions confirmed the pattern (bind, pass,
auto-collapse). A fourth instance occurred during the very session in which
this task was being designed: the assistant noted that `register` was not a
registered verb, then rewrote the proposed title to use `automate` instead of
registering `register`.

Stronger error wording was considered and rejected — the existing message is
already direct and was ignored three times.

## Solution
On verb-validation failure, `task add` shells out to `claude --model haiku -p`
with a strict prompt asking whether the title's first word is a verb. If YES,
parse the definition and auto-register the verb (same effect as `endless verb
add`), print a user-visible notice, and proceed with task creation. If NO,
fall through to the current error.

## Design

### Validator prompt (verbatim)
```
Is '<word>' a verb? If it is say 'YES:' and then provide a definition. Your
definition should be a single statement that does NOT include the verb in the
statement. If not, just reply 'NO'
```

Tested on haiku — clean output. Apfel was tested and rejected (it replied YES
while explaining the word was a noun).

### Execution
- Go: `exec.Command("claude", "--model", "haiku", "-p", prompt)`. Direct PATH
  lookup bypasses shell aliases/helpers — the user's `claude` shell wrapper
  that waits on channel-approval will not be hit. No `command claude` needed
  because we are not going through a shell at all.
- Runs only on a cache miss (first time a verb is seen). Latency is invisible
  against task-add's existing cost.

### Response handling
- Trim whitespace, check `YES:` prefix → take the rest as the definition.
- Anything else (`NO`, garbled, non-zero exit) → return the standard verb-
  rejection error to the caller.

### Validator scope
Validator validates the one word it's given — it does not propose alternates.
For hyphenated compounds like `auto-collapse` where haiku correctly says NO,
the calling agent rewrites the title (current behavior). The rewrite typically
uses a single verb like `collapse`, which auto-registers cleanly. No special
hyphenation handling.

### Failure modes
- `claude` not on PATH → error tells the user to install claude. Unlikely from
  inside a Claude session.
- Haiku returns non-`YES:` for a real verb → falls through to standard error.
  Caller can `--force` or rewrite.
- Haiku returns `YES:` for a non-verb → spurious registration. Low cost; verb
  list grows. Verbs only need to be recognizable as actions for human
  readability — periodic curation is not a goal.

### User-visible output on success
```
Auto-registered verb 'bind': to fasten or attach two things together.
• Added E-NNNN: Bind sibling-pane Claude session on 'task claim' ...
```

### Implementation notes
- Auto-register codepath reuses internal verb-add logic, not a shell-out to
  `endless verb add`.
- `verbs.json` is a global-config artifact; existing auto-commit-to-main
  policy applies.

## Verification
- `endless task add 'Strengthen the X' ...` in a scratch project where
  `strengthen` isn't registered → expect auto-register notice + task created.
- `endless task add 'Frobnicate the X' ...` → expect standard verb-rejection
  error.
- `endless task add 'Auto-collapse the X' ...` → haiku says NO → standard
  error.
- `endless verb list` shows newly registered verbs after success cases.
