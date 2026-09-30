Extraction should strip trailing punctuation and lowercase before registering. (2) The add path accepts a value that already exists in the resolved registry (project > machine > defaults), appending a duplicate line to .endless/verbs.jsonl instead of erroring; it should refuse with a clear error naming the existing verb (case-insensitive), leaving the registry unchanged.

The two interlock: once (1) normalizes 'create,' to 'create', the (2) dedup guard is what prevents re-adding the already-registered 'create'.

Also clean up the existing 'create,' junk verb.
