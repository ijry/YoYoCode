package app

// settings_science.go mirrors upstream src/app/settings/science: the bundled
// science skill packs, installable per agent (science_list). This build ships
// none, so the list is empty by design — the same honest state as Experts.

import "github.com/egoist/mygo/ui"

func (s *Shell) viewSettingsScience(c *ui.Context) {
	t := c.Theme()
	ui.Row(c.Key("sc-head")).Gap(10).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "Science").FontSize(22).Bold()
		ui.Spacer(c)
		if ui.Button(c.Key("sc-refresh"), "Refresh").Clicked() {
			s.invalidate("science-list")
		}
	})
	ui.Text(c, "Bundled science skill packs, installable per agent.").FontSize(12).TextColor(t.TextMuted)
	if e := s.errFor("science-list"); e != "" {
		ui.Text(c, "backend error: "+e).TextColor(t.Danger)
		return
	}
	list := asList(s.ensure("science-list", "science_list", nil))
	if list == nil {
		ui.Text(c, "Loading…").FontSize(12).TextColor(t.TextMuted)
		return
	}
	if len(list) == 0 {
		ui.Text(c, "No science packs in this build.").FontSize(12).TextColor(t.TextMuted)
		return
	}
	ui.Scroll(c).Grow(1).Children(func() {
		ui.Column(c).Gap(6).Children(func() {
			for i, p := range list {
				id := str(p, "id")
				name := firstNonEmpty(str(p, "name"), str(p, "display_name"), id)
				ui.Column(c.Key(itoa("sc", i))).Gap(2).Padding(10).Background(t.Surface).Radius(8).Children(func() {
					ui.Text(c, name).Bold()
					if desc := str(p, "description"); desc != "" {
						ui.Text(c, desc).FontSize(12).TextColor(t.TextMuted).MaxLines(3).Wrap()
					}
				})
			}
		})
	})
}
