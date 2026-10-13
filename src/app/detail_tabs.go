package app

// detail_tabs.go is the workspace main area's tab strip: the conversation plus
// the folder-scoped aux panels (files, git changes, terminal), mirroring
// upstream's workspace tabs (src/components/layout/aux-panel-*). The
// workspace-level routes (automations / tasks / repositories / canvas / import /
// project-boot) stay in the sidebar; these folder-scoped panels live here as
// tabs instead of sidebar rows.

import "github.com/egoist/mygo/ui"

func (s *Shell) viewDetailTabs(c *ui.Context) {
	if s.detailTab == "" {
		s.detailTab = "chat"
	}
	ui.Row(c.Key("detail-tabs")).Gap(4).AlignItems(ui.Center).Children(func() {
		for _, tab := range []struct{ id, label string }{
			{"chat", "Chat"},
			{"files", "Files"},
			{"changes", "Changes"},
			{"terminal", "Terminal"},
		} {
			active := s.detailTab == tab.id
			var clicked bool
			if active {
				clicked = ui.PrimaryButton(c.Key("dt-"+tab.id), tab.label).Clicked()
			} else {
				clicked = ui.Button(c.Key("dt-"+tab.id), tab.label).Clicked()
			}
			if clicked {
				s.detailTab = tab.id
			}
		}
	})
	ui.Divider(c)
	switch s.detailTab {
	case "files":
		s.viewFiles(c)
	case "changes":
		s.viewChanges(c)
	case "terminal":
		s.viewTerminal(c)
	default:
		s.viewConversationDetail(c)
	}
}
