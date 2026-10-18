package tmuxservers

import (
	"github.com/mikeschinkel/endless/internal/settings"
)

// The tmux keys (E-2401).
const (
	SocketDirSetting     settings.ID = "tmux.socket_dir"
	ConsoleServerSetting settings.ID = "tmux.console_server"
	TasksServerSetting   settings.ID = "tmux.tasks_server"
	TasksSessionSetting  settings.ID = "tmux.tasks_session"
	TerminalSetting      settings.ID = "tmux.terminal"
)

// Settings declares the tmux keys. Machine file only: see config.Tmux for why
// a project may not choose a server. Each default is the one Resolve supplies
// when the key is unset.
func (Servers) Settings() []settings.Setting {
	tmux := func(id settings.ID, kind settings.Kind, def string) settings.Setting {
		return settings.Setting{ID: id, Feature: "tmux", Files: settings.MachineFile,
			Kind: kind, Default: def}
	}
	return []settings.Setting{
		tmux(SocketDirSetting, settings.PathKind, DefaultDir()),
		tmux(ConsoleServerSetting, settings.NameKind, DefaultConsoleServer),
		tmux(TasksServerSetting, settings.NameKind, DefaultTasksServer),
		tmux(TasksSessionSetting, settings.NameKind, DefaultTasksSession),
		tmux(TerminalSetting, settings.CommandKind, ""),
	}
}

var _ settings.Provider = Servers{}

func init() {
	settings.Register(Servers{}.Settings()...)
}
