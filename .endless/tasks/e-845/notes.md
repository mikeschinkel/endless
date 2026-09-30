Implemented as part of E-844 —

hook and channel errors now log to ~/.config/endless/log/hook.log and channel.log respectively, using io.MultiWriter for both stderr and file output.
