package app

// import_sessions.go mirrors upstream src/app/import-sessions: the full-screen
// window for importing conversations from other agents' stores. Its backend
// (import_sessions) is not yet ported, so this is an honest placeholder tracking
// the upstream route.

import "github.com/egoist/mygo/ui"

func (s *Shell) viewImportSessions(c *ui.Context) {
	t := c.Theme()
	ui.Text(c, "Import sessions").FontSize(22).Bold()
	ui.Text(c, "The import window mirrors src/app/import-sessions; its backend (import_sessions) is a later milestone.").TextColor(t.TextMuted).Wrap()
}
