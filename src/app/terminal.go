package app

// terminal.go mirrors upstream's workspace terminal panel: a PTY spawned in the
// selected folder, its buffered output and a line input. Output arrives live on
// the terminal://output/{id} firehose channel (which only repaints — the frames
// carry no body), so the text itself comes from terminal_snapshot, refetched
// whenever a terminal channel fires (see ensureStream).

import (
	"encoding/json"

	"github.com/egoist/mygo/ui"
)

// viewTerminal renders the folder terminal: a start prompt, or the live
// snapshot with a line input.
func (s *Shell) viewTerminal(c *ui.Context) {
	t := c.Theme()
	root := s.folderPathByID(s.selectedFolder)
	ui.Row(c.Key("term-head")).AlignItems(ui.Center).Children(func() {
		ui.Spacer(c)
		if s.termID != "" {
			if ui.Button(c.Key("term-refresh"), "Refresh").Clicked() {
				s.invalidate("term:" + s.termID)
			}
			if ui.Button(c.Key("term-kill"), "Kill").Clicked() {
				s.killTerminal()
			}
		}
	})
	if s.selectedFolder == "" || root == "" {
		ui.Text(c, "Select a folder to open a terminal in it.").FontSize(12).TextColor(t.TextMuted)
		return
	}
	if s.termID == "" {
		if ui.PrimaryButton(c.Key("term-start"), "Start terminal").Clicked() {
			s.spawnTerminal(root)
		}
		ui.Text(c, "Opens a shell in "+root+".").FontSize(12).TextColor(t.TextMuted).Wrap()
		return
	}
	snap := asObj(s.ensure("term:"+s.termID, "terminal_snapshot", map[string]any{"terminalId": s.termID}))
	if e := s.errFor("term:" + s.termID); e != "" {
		ui.Text(c, "terminal error: "+e).FontSize(12).TextColor(t.Danger).Wrap()
		return
	}
	if snap == nil {
		ui.Text(c, "Loading…").FontSize(12).TextColor(t.TextMuted)
		return
	}
	if alive, _ := snap["alive"].(bool); !alive {
		ui.Text(c, "Terminal exited.").FontSize(12).TextColor(t.TextMuted)
	}
	ui.Scroll(c.Key("term-out")).Grow(1).Children(func() {
		ui.Text(c, str(snap, "data")).FontSize(11).TextColor(t.Text).Wrap()
	})
	ui.Row(c.Key("term-input")).Gap(6).Children(func() {
		ui.TextInput(c.Key("term-line"), &s.termInput).Placeholder("command…").Grow(1)
		if ui.PrimaryButton(c.Key("term-send"), "Send").Disabled(s.termInput == "").Clicked() {
			s.sendTerminalLine()
		}
	})
}

// spawnTerminal starts a PTY in root and records its id.
func (s *Shell) spawnTerminal(root string) {
	svc := s.svc
	go func() {
		raw, err := s.B.raw("terminal_spawn", map[string]any{"workingDir": root})
		s.mu.Lock()
		if err != nil {
			s.actionMsg = "terminal: " + err.Error()
		} else {
			var id string
			_ = json.Unmarshal(raw, &id)
			s.termID = id
		}
		s.mu.Unlock()
		svc.Invalidate()
	}()
}

// sendTerminalLine writes the composer line to the PTY followed by CR.
func (s *Shell) sendTerminalLine() {
	id := s.termID
	line := s.termInput
	if id == "" || line == "" {
		return
	}
	s.termInput = ""
	svc := s.svc
	go func() {
		_, _ = s.B.raw("terminal_write", map[string]any{"terminalId": id, "data": line + "\r"})
		s.mu.Lock()
		delete(s.cache, "term:"+id)
		s.mu.Unlock()
		svc.Invalidate()
	}()
}

// killTerminal ends the PTY and clears the active id.
func (s *Shell) killTerminal() {
	id := s.termID
	if id == "" {
		return
	}
	svc := s.svc
	go func() {
		_, _ = s.B.raw("terminal_kill", map[string]any{"terminalId": id})
		s.mu.Lock()
		s.termID = ""
		delete(s.cache, "term:"+id)
		s.mu.Unlock()
		svc.Invalidate()
	}()
}
