Project-root resume was rejected (that is the main checkout); prompting for a title was rejected (the user is resuming because they do not know what the session was).

Widen --print-decision to the plain resume path so it is testable without exec'ing claude, and add a table-driven drift test asserting every session-ref entry point accepts ES-<id>.
