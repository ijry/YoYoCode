package app

// project_boot.go mirrors upstream src/app/project-boot: the full-screen project
// bootstrap window (a ProjectBootWorkspace). Its backend (project_boot_*) is not
// yet ported, so this is an honest placeholder tracking the upstream route.

import "github.com/egoist/mygo/ui"

func (s *Shell) viewProjectBoot(c *ui.Context) {
	t := c.Theme()
	ui.Text(c, "Project boot").FontSize(22).Bold()
	ui.Text(c, "The project bootstrap window mirrors src/app/project-boot; its backend (project_boot_*) is a later milestone.").TextColor(t.TextMuted).Wrap()
}
