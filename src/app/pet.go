package app

// Pet mirrors upstream src/app/pet/page.tsx — the desktop sprite panel. The
// pet subsystem (sprites, marketplace) is not yet ported on the backend, so
// this page is a placeholder tracking the upstream route.

import "github.com/egoist/mygo/ui"

func (s *Shell) viewPet(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Fill().Padding(20).Gap(12).Children(func() {
		ui.Text(c, "Pet").FontSize(22).Bold()
		ui.Text(c, "The desktop pet panel mirrors src/app/pet; its backend (pet_* sprites and marketplace) is a later milestone.").TextColor(t.TextMuted)
	})
}
