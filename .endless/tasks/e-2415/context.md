Sessions are machine data and stay out of the project ledger. Ledger actors today carry only the local ES number (session_id), which means nothing on another machine and collides across machines. session_tasks rows are live-only: nothing replays them, so a rebuild or another machine loses which sessions touched a task, including non-mutating interest such as touch.

Agreed with Mike while reconciling E-2291 (EQ-93): carry the UUID and the ES number side by side; never replace the ES number. The machine that ran a session keeps showing ES-NNNN with its transcript link.

Open design point — display width: task show's Sessions: block aligns sessions in a narrow column (ES-1276, 7 characters). A session from another machine has no local ES row, so it needs an equally concise form, e.g. a short UUID prefix (ES~7844f), with the host shown in a legend or in session show rather than inline.

Out of scope, a maybe for later: capturing transcripts themselves (compressed binary, explicit opt-in per project) for teams that want to see why something happened. Transcripts are large, can hold secrets, and do not belong in git history by default.