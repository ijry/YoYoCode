package app

// computer.go mirrors upstream's desktop-only computer-use overlays
// (src/app/computer-indicator + src/app/computer-marker). Upstream draws each in
// its own window: the indicator is a strip pinned above every window while any
// window is shared, the marker a transparent window moved onto the spot an
// agent's action landed. This native build folds the indicator into the single
// window as a top strip. The marker has no single-window analog — it must be
// positioned over an arbitrary screen point — so it is intentionally not drawn.
//
// The port's backend refuses computer use: every computer_* command except
// computer_available returns an error (src-go/web/handlers/computer.go), and
// computer_available reports available=false. So the strip stays hidden here —
// the same "unavailable" state Settings → Computer Use already reports.

import (
	"fmt"

	"github.com/egoist/mygo/ui"
)

// computerAvailable reports whether this backend serves computer use, and on
// which platform (computer_available).
func (s *Shell) computerAvailable() (available bool, platform string) {
	info := asObj(s.ensure("computer-available", "computer_available", nil))
	if info == nil {
		return false, ""
	}
	if v, ok := info["available"].(bool); ok {
		available = v
	}
	return available, str(info, "platform")
}

// viewComputerIndicator draws the "agents are using your computer" strip: a
// status dot (violet = read, red = control), a summary and a Stop. It renders
// only while computer use is available AND something is shared; on a backend
// that refuses computer use it renders nothing.
func (s *Shell) viewComputerIndicator(c *ui.Context) {
	available, _ := s.computerAvailable()
	if !available {
		return
	}
	state := asObj(s.ensure("computer-shared", "computer_shared_state", nil))
	if state == nil {
		return // nothing shared (or the call refused) → no strip
	}
	shared := asListVal(state["shared"])
	if len(shared) == 0 {
		return
	}
	t := c.Theme()
	acting := false
	for _, w := range shared {
		if str(w, "level") == "control" {
			acting = true
		}
	}
	dot := t.Accent
	label := fmt.Sprintf("Agents can read %d window(s)", len(shared))
	if acting {
		dot = t.Danger
		label = fmt.Sprintf("Agents can control %d window(s)", len(shared))
	}
	ui.Row(c.Key("computer-strip")).FillWidth().Background(t.Surface).PaddingX(12).PaddingY(5).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "\u25CF").FontSize(9).TextColor(dot)
		ui.Text(c, label).FontSize(12)
		ui.Spacer(c)
		if ui.Button(c.Key("computer-stop"), "Stop").Clicked() {
			s.computerStop()
		}
	})
}

// computerStop ends every sharing (computer_stop).
func (s *Shell) computerStop() {
	go func() {
		_, _ = s.B.raw("computer_stop", map[string]any{})
		s.invalidate("computer-shared")
		s.svc.Invalidate()
	}()
}
