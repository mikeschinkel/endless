task_cmd.py probes for an 'eswt' shell function and recommends running 'eval "$(endless shell-init)"' with the comment 'Adds eswt shell helper func'.

But shell-init does not currently ship eswt; it ships esu/esp/esf.

The recommendation is therefore misleading until E-1180 lands.
