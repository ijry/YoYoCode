package app

// Pet hosts the two upstream pet routes as one section: the live active-session
// panel (src/app/pet-panel, a separate window upstream) and the desktop sprite
// panel (src/app/pet). The sprite subsystem (sprites, marketplace) is a later
// milestone; the sessions panel reads the real pet_list_active_sessions payload.

import "github.com/egoist/mygo/ui"

func (s *Shell) viewPet(c *ui.Context) {
	t := c.Theme()
	if s.petTab == "" {
		s.petTab = petTabSessions
	}
	ui.Column(c).Fill().Padding(20).Gap(12).Children(func() {
		ui.Row(c.Key("pet-tabs")).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "Pet").FontSize(22).Bold()
			ui.Spacer(c)
			for _, tab := range []struct{ id, label string }{
				{petTabSessions, "Active sessions"},
				{petTabSprite, "Sprite"},
			} {
				active := s.petTab == tab.id
				var clicked bool
				if active {
					clicked = ui.PrimaryButton(c.Key("pet-tab-"+tab.id), tab.label).Clicked()
				} else {
					clicked = ui.Button(c.Key("pet-tab-"+tab.id), tab.label).Clicked()
				}
				if clicked {
					s.petTab = tab.id
				}
			}
		})
		if s.petTab == petTabSprite {
			ui.Text(c, "The desktop sprite panel mirrors src/app/pet; its backend (pet sprites and the marketplace) is a later milestone.").TextColor(t.TextMuted).Wrap()
			return
		}
		s.viewPetPanel(c)
	})
}
