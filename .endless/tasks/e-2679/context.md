Filed from gomion's E-2628 session, 2026-11-26, at Mike's request.

How it works today: the conflicts_with task link (endless guide tasks) means "A and B must not run at the same time, because their plans touch the same files, and neither must precede the other". It is symmetric and advisory, and session status draws it as A <> B. task show labels it "Conflicts with", and task add takes it as --conflicts-with.

What prompted it: Mike met the link for the first time on two go-cfgstore tasks and asked whether it was how session status decides one task should happen before another. The name gives the cause (the edits would collide) rather than the rule a reader acts on (do not run them together), and it invites reading it as an ordering. Mike, 2026-11-26: "conflicts with is confusing if it means not concurrently"; the name to use is "Not concurrent with".
