It runs migrations in full (DDL plus their DML) and never serves a hook or touches business data, which is what makes it immune to the binary-expects-a-schema failure.

self_dev only.
