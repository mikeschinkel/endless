# Brief

Work out, in a session for the endless project, whether Endless needs task templates, user-defined task types, both, or neither.

Questions to settle:
- What recurring task shapes exist today besides the Go package release (search the task list for repeated plans), and what varies between instances?
- Does the shape need new behavior (a different status lifecycle, gates, fields), which points to a user-defined type, or only a pre-filled plan and item set, which points to a template?
- Where would templates live (the database, a file in the repo, a shared file under the Endless config directory), and how do they relate to the existing plan-item format and the verify suite?
- What does each option cost in the guide, the status lifecycle and the type-aware gates?

The outcome records a recommendation and the options it rejected, with the trade-offs for each, and proposes the tasks to file. It changes no code.
