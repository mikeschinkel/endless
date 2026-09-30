Approach options to decide later: file-watching daemon (fsnotify on .endless/config.json, plans/snapshots/, etc.) reporting via 'endless session show' or the channel; periodic check on hook events comparing mtimes; an 'endless status' command that surfaces pending auto-commits with an 'endless commit-pending' helper.

Revisit when accidental bundling becomes a real complaint or attribution loss starts to bite.
