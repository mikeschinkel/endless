Edit needed in .endless/plans/E-987.md — auto-commit list table gains a row for '.endless/config.json' with note 'AI-driven phrase add and similar mutations write here frequently per E-1056', and the 'Explicitly NOT in the auto-commit list' table loses its config.json row. When E-987's actual implementation lands, the auto_commit_paths default in code/config should include '.endless/config.json'.

Verification: 'endless phrase add verb foo' followed by 'endless worktree land' from clean main auto-commits the config change as part of land.
