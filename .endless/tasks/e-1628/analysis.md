Landing should pin the real/main DB regardless of caller routing, like hook/channel/tmux do via PinMainDB (E-1450/E-1429).

Workaround: run land with XDG_CONFIG_HOME set to the real config dir.
