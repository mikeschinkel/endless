The E-1710 audit found ~36 of 47 committed segment files in main's .endless/db-ledger (~1500 events, ~1/3 of the ledger) are e2e/test-fixture ledgers that create fixture tasks ('smoke epic', 'Coordinate breakdown epic', 'Implement alpha/beta/gamma') reusing real task ids 1..~150.

On a full rebuild-db they collide with real ids (UNIQUE conflicts) and corrupt low-id state.
