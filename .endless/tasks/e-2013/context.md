`endless worktree land` runs from the main checkout's editable install and ff-merges main underneath its own live process, so modules imported AFTER the merge come from the new source while ones already in sys.modules stay at the old one.

E-2011 renamed endless.project_path.normalize to resolved, main advanced, and recording the landing died on `cannot import name 'resolved' from 'endless.project_path'` — leaving no task_landings row and the branch tip unmerged.

Re-running lands cleanly because a fresh process loads the new source throughout, so this is a half-completed land and a confusing error, not corruption.
