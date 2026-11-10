E-2586 gives verify.toml (task and project level) an `env` table whose values are strings with `$VAR` references expanded against the run's environment. A value can't be computed at run time: for example, E-2440 could only point GOPROXY at its file proxy while a proposed tag is still unpublished by doing that work in a setup script. E-2586 records this as out of scope (its P10).

While answering E-2586's plan questions on 2026-11-09, Mike suggested expr-lang for computed values. It has a small syntax that other languages could implement if the verify.toml spec is ever adopted outside Endless, Gomion and Go:
- https://expr-lang.org/
- https://github.com/expr-lang/expr
- https://wundergraph.com/blog/expr-lang-go-centric-expression-language

Mike framed it as later work, to do when a suite needs it.
