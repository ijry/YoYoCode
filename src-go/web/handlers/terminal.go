package handlers

// Terminal commands (B6): spawn/write/resize/snapshot/kill/list over a real
// PTY, mirroring web/handlers/terminal.rs. Output streams on the
// terminal://output/{id} firehose channel (and terminal://exit/{id}); the
// manager lives on AppState so the background reader can broadcast without a
// request context.

import (
	appstate "yoyocode/src-go/app_state"
	"yoyocode/src-go/terminal"
	"yoyocode/src-go/web"
)

func terminalManager(c *web.Context) *terminal.Manager {
	if st, ok := c.State.(*appstate.AppState); ok && st != nil {
		return st.Terminal
	}
	return nil
}

func RegisterTerminal(reg *Registry) {
	reg.Register("terminal_spawn", func(c *web.Context) (any, error) {
		mgr := terminalManager(c)
		if mgr == nil {
			return nil, web.ErrTaskExecution("terminal manager unavailable")
		}
		id, err := mgr.Spawn(terminal.SpawnOptions{
			TerminalID:     GetString(c, "terminalId", "terminal_id"),
			WorkingDir:     GetString(c, "workingDir", "working_dir"),
			Shell:          GetString(c, "shell"),
			InitialCommand: GetString(c, "initialCommand", "initial_command"),
		})
		if err != nil {
			return nil, web.ErrTaskExecution(err.Error())
		}
		return id, nil
	})

	reg.Register("terminal_write", func(c *web.Context) (any, error) {
		mgr := terminalManager(c)
		if mgr == nil {
			return nil, web.ErrTaskExecution("terminal manager unavailable")
		}
		id := GetString(c, "terminalId", "terminal_id")
		if err := mgr.Write(id, []byte(GetString(c, "data"))); err != nil {
			return nil, web.ErrTaskExecution(err.Error())
		}
		return nil, nil
	})

	reg.Register("terminal_resize", func(c *web.Context) (any, error) {
		mgr := terminalManager(c)
		if mgr == nil {
			return nil, web.ErrTaskExecution("terminal manager unavailable")
		}
		id := GetString(c, "terminalId", "terminal_id")
		cols := uint16(GetInt(c, "cols"))
		rows := uint16(GetInt(c, "rows"))
		if err := mgr.Resize(id, cols, rows); err != nil {
			return nil, web.ErrTaskExecution(err.Error())
		}
		return nil, nil
	})

	reg.Register("terminal_snapshot", func(c *web.Context) (any, error) {
		mgr := terminalManager(c)
		if mgr == nil {
			return terminal.TerminalSnapshot{Alive: false}, nil
		}
		return mgr.Snapshot(GetString(c, "terminalId", "terminal_id")), nil
	})

	reg.Register("terminal_kill", func(c *web.Context) (any, error) {
		mgr := terminalManager(c)
		if mgr == nil {
			return nil, web.ErrTaskExecution("terminal manager unavailable")
		}
		if err := mgr.Kill(GetString(c, "terminalId", "terminal_id")); err != nil {
			return nil, web.ErrTaskExecution(err.Error())
		}
		return nil, nil
	})

	reg.Register("terminal_list", func(c *web.Context) (any, error) {
		mgr := terminalManager(c)
		if mgr == nil {
			return []terminal.TerminalInfo{}, nil
		}
		return mgr.List(), nil
	})
}
