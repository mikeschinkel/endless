Three rules: (1) CLI args accept both 697 and E-697 when expecting a plan ID — strip prefix internally. (2) All output (CLI, web dashboard, guide doc) always displays E-NNN. (3) In conversation, E-697 means Endless, plain 697 is ambiguous — AI should ask for clarification not assume. Implementation: update plan show, plan detail, plan add output, web templates, guide doc.

Similar to Git short-SHA pattern: input forgiving, output strict.
