'endless setup' has no tmux subcommand and setup.py contains zero tmux references, so the tmux-conf line that 'endless tmux init' documents as its own trigger is never written — making init unreachable for anyone who has not hand-edited their tmux config. The only working path is running 'endless tmux apply' by hand, which is ephemeral and must be repeated after every tmux server restart, and the reference guide documents apply without ever mentioning init or the conf line.

So a PRODUCT user gets no second status row, no popup menus, and no way to make either persist.
