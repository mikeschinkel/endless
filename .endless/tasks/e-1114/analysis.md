Fix: setpgid the subshell, signal the whole group on enter exit and on signal handler. Also consider blocking destroy when the sandbox has live writers, with a clear error pointing at the offender.
