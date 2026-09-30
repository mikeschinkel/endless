Scope: stderr-only (status + warning lines).

The stdout 'cd <path>' line keeps the full path because shlex.quote turns ~ into a literal that the shell will not expand — eval-safety wins over brevity for the consumed line.

Implementation: small _short_path(p) helper in session_cmd.py, called from _emit_resolution_status only.
