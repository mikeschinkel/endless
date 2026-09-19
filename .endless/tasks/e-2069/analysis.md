Reproduce with the e-1956 fixture: register a throwaway project, add two ready tasks, `task update E-1 --status assumed`, `task replace E-1 --by E-2`, then `task list --all`.

  task show E-1   -> 'Status:     assumed (replaced by E-2)'   correct
  task list --all -> 'E-1  now  assumed  n/a  ...'             missing

Verified against the tree at E-1891's parent commit, so E-1891 did not cause it.
tests/tasks/e-1956-verify.sh line ~302 asserts it and now fails, having
previously exited at two E-1185-era stale greps upstream of it.

E-1956's own pytest suite passes, so whatever covers this is not covering the
list renderer's column — worth checking whether the batched replaced_by_map
lookup is wired into the list path at all, or only into show.