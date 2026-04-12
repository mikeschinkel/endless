# Run Hook on Every Prompt (Cross-Shell)

## Goal

Execute a command **every time a prompt is shown** (i.e., before user input).

The command should:

* run asynchronously (non-blocking)
* receive current working directory
* be safe to run frequently

---

# 1. ZSH

## Mechanism

Use `precmd` hook (runs before each prompt render)

## Implementation

Add to `~/.zshrc`:

```zsh
function __prompt_hook() {
  local cwd="$PWD"

  # run async, detached
  your_binary "$cwd" >/dev/null 2>&1 &
}

autoload -Uz add-zsh-hook
add-zsh-hook precmd __prompt_hook
```

## Notes

* `precmd` runs once per prompt
* Do NOT use `PROMPT` substitution for this
* Backgrounding (`&`) is required to avoid lag

---

# 2. BASH

## Mechanism

Use `PROMPT_COMMAND`

## Implementation

Add to `~/.bashrc`:

```bash
__prompt_hook() {
  local cwd="$PWD"

  your_binary "$cwd" >/dev/null 2>&1 &
}

# preserve existing PROMPT_COMMAND if present
if [ -n "$PROMPT_COMMAND" ]; then
  PROMPT_COMMAND="__prompt_hook; $PROMPT_COMMAND"
else
  PROMPT_COMMAND="__prompt_hook"
fi
```

## Notes

* Runs before each prompt display
* Must prepend, not overwrite existing value

---

# 3. POWERSHELL

## Mechanism

Override `prompt` function

## Implementation

Add to `$PROFILE`:

```powershell
function global:prompt {
    $cwd = (Get-Location).Path

    Start-Process -WindowStyle Hidden -FilePath "your_binary" -ArgumentList "$cwd" | Out-Null

    # preserve existing prompt behavior
    return "PS $cwd> "
}
```

## Notes

* PowerShell prompt is a function
* You MUST preserve prompt output or users lose prompt

Alternative (safer, preserves existing prompt automatically):

```powershell
$oldPrompt = $function:prompt

function global:prompt {
    $cwd = (Get-Location).Path

    Start-Process -WindowStyle Hidden -FilePath "your_binary" -ArgumentList "$cwd" | Out-Null

    & $oldPrompt
}
```

---

# 4. CMD (Command Prompt)

## Mechanism (limited)

Use `PROMPT` env var + wrapper

CMD has **no native prompt hook**, so use a workaround.

## Option A (recommended): wrapper via `doskey`

```cmd
doskey prompt_hook=your_binary $P ^& prompt $G
```

Then:

```cmd
prompt $E[92m$p$g
```

## Option B: AutoRun registry

Set:

```
HKCU\Software\Microsoft\Command Processor\AutoRun
```

Value:

```cmd
your_binary %CD%
```

## Notes

* CMD does NOT have a true per-prompt hook
* AutoRun runs per shell start, not per prompt
* Full parity with Bash/Zsh is not possible

---

# 5. Cross-Shell Binary Contract

Ensure binary:

### Input

* Accepts current directory as argument
* Example:

  ```
  your_binary "/current/path"
  ```

### Behavior

* Must exit quickly
* Must handle concurrent runs safely
* Should internally deduplicate runs (important)

---

# 6. Throttling (Important)

Since prompts can fire frequently:

Implement **debounce logic** inside your binary:

Example strategies:

* skip if last run < N seconds ago
* skip if no filesystem mtime changes
* use lockfile or SQLite timestamp

---

# 7. Testing Checklist

Verify:

* Zsh: runs on every Enter
* Bash: runs on every Enter
* PowerShell: runs on every Enter
* CMD: acceptable limitation documented

---

# 8. Non-Blocking Guarantee

All implementations MUST:

* run in background
* not write to stdout/stderr
* not delay prompt

