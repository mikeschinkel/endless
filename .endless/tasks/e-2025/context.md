ED-1570 halts a binary whose schema version is behind the database.

After an upgrade migrates the ledger, every long-running process still holding the old binary image — background agents, the jobs runner — halts on its next connect and dies mid-flight, which can take down tens of live sessions at once.
