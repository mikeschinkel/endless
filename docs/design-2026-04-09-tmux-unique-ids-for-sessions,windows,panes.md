# tmux Metadata for External UUID Management

## Purpose

Provide a stable place to store externally-generated UUIDs on:

* sessions
* windows
* panes

tmux acts only as a **key/value store per object**.

---

## Core Mechanism: User Options

tmux supports arbitrary metadata via `@key`.

### Set value

```bash
tmux set-option -t <target> @key <value>
```

### Get value

```bash
tmux display-message -p -t <target> '#{@key}'
```

### Targets

* Session → `$<id>`
* Window  → `@<id>`
* Pane    → `%<id>`

---

## Required Keys

Use explicit keys per object type:

* `@session_uuid`
* `@window_uuid`
* `@pane_uuid`

---

## Read / Ensure Pattern (to implement in Go/Python)

### Pseudocode

```
value = tmux_get(target, key)

if value == "":
    uuid = generate_uuid()     # external
    tmux_set(target, key, uuid)
    return uuid

return value
```

---

## CLI Primitives (what your program should call)

### Get

```bash
tmux display-message -p -t <target> '#{@key}'
```

### Set

```bash
tmux set-option -t <target> @key <value>
```

---

## Enumerating Objects

Your program should discover all objects and then apply the ensure pattern.

### Panes

```bash
tmux list-panes -a -F '#{pane_id}'
```

### Windows

```bash
tmux list-windows -a -F '#{window_id}'
```

### Sessions

```bash
tmux list-sessions -F '#{session_id}'
```

---

## Optional: Include metadata inline

More efficient (fewer subprocess calls):

### Panes

```bash
tmux list-panes -a -F '#{pane_id} #{@pane_uuid}'
```

### Windows

```bash
tmux list-windows -a -F '#{window_id} #{@window_uuid}'
```

### Sessions

```bash
tmux list-sessions -F '#{session_id} #{@session_uuid}'
```

---

## Behavior Guarantees

* User options are:

    * scoped to the object
    * mutable
    * immediately visible
* No schema enforcement (keys may be absent)
* Values are plain strings
* Safe to overwrite

---

## Constraints

* Data lifetime = tmux server lifetime
* No persistence across restart
* No atomic "set-if-not-exists" → must handle race externally if needed

---

## Recommended Flow (your system)

1. Enumerate sessions/windows/panes
2. For each:

    * read UUID
    * if missing → generate + set
3. Store mapping in SQLite:

   ```
   uuid ↔ tmux_id (session/window/pane)
   ```
4. Refresh periodically or via hooks

---

## Minimal Example (conceptual)

```bash
# get pane UUID
tmux display-message -p -t %3 '#{@pane_uuid}'

# set pane UUID (value provided externally)
tmux set-option -pt %3 @pane_uuid '123e4567-e89b-12d3-a456-426614174000'
```

