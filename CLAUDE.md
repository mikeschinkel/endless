# Endless Project Rules

## Build

Use `just build` to build everything (templ generate, tailwind CSS, Go binaries). All Go binaries are output to `./bin/`. Use `just install` to build and symlink to `/usr/local/bin/`.

**NEVER build Go binaries to the project root or `/usr/local/bin/` directly.**

## Tests

Use `just test` to run Python tests.
