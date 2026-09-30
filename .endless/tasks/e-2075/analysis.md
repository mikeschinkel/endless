`jobs list`, `verb list`, `phrase list`, `worktree list` and `project list` are uncapped and should be audited in the same pass.

The mechanism already exists and is tested; this is adoption, not design: thread rowcap.limit_options through each command, resolve with machine=as_json, and echo_footer after the render.

session trail and session search read through Go (session-query), so those two need the cap applied on the Python render side or a matching footer in the Go renderer — that is the only open question.
